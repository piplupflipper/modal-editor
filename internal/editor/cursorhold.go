package editor

import (
	"time"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// カーソルが止まった契機（基本設計 6.7 の「カーソルが止まった契機」。Vim と Neovim の CursorHold。FR-171）。
//
// 利用者がキーを打たずに設定 cursorHold.delayMs のミリ秒が経ち、その client がノーマルモードで何も待っていなければ、
// 「カーソルが止まった」として、契機を待つもの（holdListener）を順に呼ぶ。診断のポップアップ（diagnosticPopup）のためだけのタイマーにせず、
// 後で、表示の変化を拡張機能に知らせる口（#317 の第 3 弾）も同じ契機に足せるようにした。
//
//   - client ごとに持つ。キーはどの client から届いたかで分かれ、ポップアップも client ごとに 1 つだからである（clientView.holdStop）
//   - キーを処理するたびに（afterKey）、その client のタイマーを止めて掛け直す。外部のコマンドと再生中の拡張機能の待ちが終わって
//     入力を続けたときも掛け直す（resumeInput）。キーの他の出来事（拡張機能の診断、編集、ファイルの読み直し）では掛け直さない。
//     Vim と同じく、止まったと 1 回知らせたら、次のキーを打つまでは知らせ直さない（起動した直後もキーを打つまでは知らせない。
//     2026-11-21 に Neovim 0.12.5 の CursorHold で確かめた）
//   - 時間の待ちはイベントループの外のタイマー（Editor.afterFunc）で行い、時間が来たら inspect でイベントループに戻して知らせる
//     （基本設計 2.1。編集の状態はイベントループの goroutine だけが触る）。止める前に来たタイマーは、世代（clientView.holdGen）が
//     違うので捨てる
//   - 知らせるのは、時間が来た時点で、その client がノーマルモードでキーの列の途中でなく、マクロを記録しておらず（Neovim の
//     :help CursorHold）、問い、hit-enter の問い、外部のコマンドと再生中の拡張機能の待ち、先打ちのキーが無いときだけである（holdReady）。
//     知らせなかったときも掛け直さない。インサートモードを <Esc> で抜けたら、そのキーで掛け直す
//   - 契機を待つものが 1 つも無い（どれも設定で切っている）なら、タイマーを掛けない。キーを打つたびにタイマーを作らないためである

// holdListener は、カーソルが止まった契機を待つものである。
type holdListener struct {
	// wants は、契機を待つかを返す。false なら、この契機のためのタイマーを掛けない。キーを打つたびに読む。
	wants func(e *Editor) bool
	// held は、カーソルが止まったときに、止まった client を読み込んだ状態で呼ぶ。
	held func(e *Editor)
}

// holdListeners は、カーソルが止まった契機を待つものの一覧である。並べた順に呼ぶ。
// 契機を待つものを足すときは、ここに足す。
// パッケージの変数にしないのは、呼ぶ関数からこの一覧へ戻る参照（キーの処理の armHold）が初期化の循環になるためである。
func holdListeners() []holdListener {
	return []holdListener{
		{wants: (*Editor).wantsDiagnosticPopup, held: (*Editor).showDiagnosticPopup},
	}
}

// armHold は、読み込んでいる client の、カーソルが止まった契機のタイマーを掛け直す（パッケージの doc の上の節）。
// 契機を待つものが無ければ、前のタイマーを止めるだけにする。
//
// req: FR-171
func (e *Editor) armHold() {
	c := e.view
	if c == nil || c.detached {
		return
	}
	e.stopHold(c)
	if !e.holdWanted() {
		return
	}
	gen := c.holdGen
	d := time.Duration(e.options.Get(option.CursorHoldDelayMs).Int()) * time.Millisecond
	stop := e.stop
	c.holdStop = e.afterFunc(d, func() {
		select {
		case e.inspect <- func() { e.fireHold(c, gen) }:
		case <-stop:
		}
	})
}

// stopHold は、client c の、カーソルが止まった契機のタイマーを止める。止める前に送り始めた知らせは、世代が違うので捨てる。
func (e *Editor) stopHold(c *clientView) {
	if c.holdStop != nil {
		c.holdStop()
		c.holdStop = nil
	}
	c.holdGen++
}

// holdWanted は、カーソルが止まった契機を待つものがあるかを返す。
func (e *Editor) holdWanted() bool {
	for _, l := range holdListeners() {
		if l.wants(e) {
			return true
		}
	}
	return false
}

// fireHold は、client c の世代 gen のタイマーの時間が来たときに、イベントループで呼ぶ。
// 掛け直した後（世代が違う）か、c を外した後なら何もしない。c がまだ止まっていれば（holdReady）、契機を待つものを順に呼ぶ。
//
// req: FR-171
func (e *Editor) fireHold(c *clientView, gen uint64) {
	if c.detached || c.holdGen != gen {
		return
	}
	c.holdStop = nil
	e.withView(c, func() {
		if !e.holdReady() {
			return
		}
		for _, l := range holdListeners() {
			if l.wants(e) {
				l.held(e)
			}
		}
	})
}

// holdReady は、読み込んでいる client が、カーソルが止まったと知らせてよい状態かを返す（パッケージの doc の上の節）。
// ノーマルモードでキーの列の途中でなく、マクロを記録していないことは、Neovim 0.12.5 の :help CursorHold の条件と同じである。
func (e *Editor) holdReady() bool {
	c := e.view
	if e.cur == nil || e.quit || e.asking() || e.activeChoice() != nil || e.hitEnter() || e.regList != nil ||
		e.suspension() != nil || e.keys.has(c) || e.remap.pendingKeys() {
		return false
	}
	st := e.cur.state
	return st.Mode() == command.ModeNormal && !st.Pending() && st.Recording() == 0
}
