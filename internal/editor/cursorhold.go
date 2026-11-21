package editor

import (
	"time"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// カーソルが止まった契機（基本設計 6.7 の「カーソルが止まった契機」。Vim と Neovim の CursorHold と CursorHoldI。FR-171）。
//
// 利用者がキーを打たずに設定 cursorHold.delayMs のミリ秒が経ったら、「カーソルが止まってから時間が経った」として、
// 契機を待つもの（holdListener）のうち、その client の今の状態で知らせを受けるもの（holdListener.ready）を順に呼ぶ。
// 診断のポップアップ（diagpopup.go）のためだけのタイマーにせず、止まってから時間が経ったら動くものを、後で同じ一覧に足せるようにした。
// 時間は契機を待つものすべてに共通の 1 つである（Vim の CursorHold と CursorHoldI が、どちらも 'updatetime' で動くのと同じ）。
// 表示の変化（カーソル、見えている行、モード）を拡張機能に知らせる口（#317 の第 3 弾）は、この契機に載せない。
// 変化のたびに間引いて送る別の契機（Neovim の CursorMoved と WinScrolled に当たる）にする（PR #333 のレビューの裁定要へのオーケストレータの決定）。
//
//   - client ごとに持つ。キーはどの client から届いたかで分かれ、ポップアップも client ごとに 1 つだからである（clientView.holdStop）
//   - キーを処理するたびに（afterKey）、その client のタイマーを止めて掛け直す。外部のコマンドと再生中の拡張機能の待ちが終わって
//     入力を続けたときも掛け直す（resumeInput）。キーでない出来事（端末の大きさの変更、拡張機能の応答での移動（gd の結果）、
//     拡張機能の診断と編集、別の client の編集、ファイルの読み直し）では掛け直さない。Neovim 0.12.5 の CursorHold も、キーを打たずに
//     nvim_win_set_cursor でカーソルを動かしても来直さない（PR #333 のレビューで確かめた）
//   - ペインの部品の client（拡張機能の pane/open。component.go）のキーでは掛け直さず、元の client のタイマーを止める。ペインのキーは
//     元の client の操作として扱うが、利用者が見ているのはペインなので、元の client の画面に診断のポップアップを出さないためである
//     （Neovim も、別の窓へ移ると open_float の窓を閉じる。paneInput）
//   - Vim と同じく、止まったと 1 回知らせたら、次のキーを打つまでは知らせ直さない（起動した直後もキーを打つまでは知らせない。
//     2026-11-21 に Neovim 0.12.5 の CursorHold で確かめた）
//   - 時間の待ちはイベントループの外のタイマー（Editor.afterFunc）で行い、時間が来たら inspect でイベントループに戻して知らせる
//     （基本設計 2.1。編集の状態はイベントループの goroutine だけが触る）。止める前に来たタイマーは、世代（clientView.holdGen）が
//     違うので捨てる
//   - 知らせてよいかの条件は 2 段に分ける。どの契機を待つものにも共通の条件（holdReady）は、終わる途中でなく、確認の問い、選択肢の問い、
//     hit-enter の問い、レジスターの一覧、外部のコマンドと再生中の拡張機能の待ち、先打ちのキー、キーマップの続きの待ちが無いことである。
//     モードなどの条件は、契機を待つものごとに持つ（holdListener.ready）。診断のポップアップは、ノーマルモードでキーの列の途中でなく、
//     マクロを記録していないとき（Neovim 0.12.5 の :help CursorHold の条件）だけ受ける。インサートモードで受けるもの（CursorHoldI に当たる）も足せる。
//     知らせなかったときも掛け直さない。インサートモードを <Esc> で抜けたら、そのキーで掛け直す
//   - 契機を待つものが 1 つも無い（どれも設定で切っている）なら、タイマーを掛けない。キーを打つたびにタイマーを作らないためである

// holdListener は、カーソルが止まった契機を待つものである。
type holdListener struct {
	// wants は、契機を待つかを返す。false なら、この契機のためのタイマーを掛けない。キーを打つたびと、時間が来たときに読む。
	wants func(e *Editor) bool
	// ready は、止まった client の今の状態（モードなど）で知らせを受けるかを返す。共通の条件（holdReady）を満たしたときだけ読む。
	ready func(e *Editor) bool
	// held は、カーソルが止まったときに、止まった client を読み込んだ状態で呼ぶ。
	held func(e *Editor)
}

// defaultHoldListeners は、本体の、カーソルが止まった契機を待つものの一覧である（Editor.holdListeners の初めの値）。並べた順に呼ぶ。
// 契機を待つものを足すときは、ここに足す。
// パッケージの変数にしないのは、呼ぶ関数からこの一覧へ戻る参照（キーの処理の armHold）が初期化の循環になるためである。
func defaultHoldListeners() []holdListener {
	return []holdListener{
		{wants: (*Editor).wantsDiagnosticPopup, ready: (*Editor).normalHoldReady, held: (*Editor).showDiagnosticPopup},
	}
}

// normalHoldReady は、読み込んでいる client が、ノーマルモードでキーの列の途中でなく、マクロを記録していないかを返す
// （Neovim 0.12.5 の :help CursorHold の条件。holdListener.ready）。
//
// req: FR-171
func (e *Editor) normalHoldReady() bool {
	st := e.cur.state
	return st.Mode() == command.ModeNormal && !st.Pending() && st.Recording() == 0
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
	for _, l := range e.holdListeners {
		if l.wants(e) {
			return true
		}
	}
	return false
}

// fireHold は、client c の世代 gen のタイマーの時間が来たときに、イベントループで呼ぶ。
// 掛け直した後（世代が違う）か、c を外した後なら何もしない。c が共通の条件を満たせば（holdReady）、
// 契機を待つもののうち、待っていて（wants）今の状態で受ける（ready）ものを順に呼ぶ。
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
		for _, l := range e.holdListeners {
			if l.wants(e) && l.ready(e) {
				l.held(e)
			}
		}
	})
}

// holdReady は、読み込んでいる client が、どの契機を待つものにも共通の条件で、カーソルが止まったと知らせてよい状態かを返す
// （パッケージの doc の上の節）。モードの条件は、契機を待つものごとに持つ（holdListener.ready）。
func (e *Editor) holdReady() bool {
	c := e.view
	return e.cur != nil && !e.quit && !e.asking() && e.activeChoice() == nil && !e.hitEnter() && e.regList == nil &&
		e.suspension() == nil && !e.keys.has(c) && !e.remap.pendingKeys()
}
