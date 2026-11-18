package editor

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// タブ（基本設計 4.4 の段階 3 のタブとバッファ）。
//
// 各編集の client は 1 本のタブの行を持ち（clientView.tabs）、1 つのバッファはどれか 1 つの client のタブになる（FR-62）。
// バッファの owner は、そのバッファをタブに持つ client で、どのタブでもないバッファ（拡張機能が focus を false にして開いたもの。
// 端末の入力が閉じた client の未保存のバッファ）では nil である。
// 隠れたバッファは持たない（FR-64）。タブを閉じるとバッファも閉じる。タブを移るときは、未保存の変更があっても E37 で断らない。

// addTab は、どのタブでもないバッファ bs を、読み込んでいる client のタブにする。カーソルのあるタブの右に置く。
// bs の State のページ単位のスクロール（command.State.Viewport）を、この client の描画の部品につなぎ直す。
func (e *Editor) addTab(bs *bufferState) {
	c := e.view
	i := slices.Index(c.tabs, e.cur)
	c.tabs = slices.Insert(c.tabs, i+1, bs)
	bs.owner = c
	bs.state.Viewport = viewport{r: e.renderer}
}

// withView は、client c を読み込んで f を呼び、元の client へ戻す。
func (e *Editor) withView(c *clientView, f func()) {
	prev := e.view
	e.switchView(c)
	f()
	if prev != nil && prev.attached() {
		e.switchView(prev)
	}
}

// focusOwner は、別の client のタブのバッファ bs を開こうとしたときに、その client のペインへフォーカスを移す（FR-63）。
// その client が入力の途中でなければ（idle）、その client のカーソルのあるタブを bs にする。
// 入力の途中（インサートモード、コマンドラインモード、ビジュアルモード、キーの列の途中）なら、タブは替えずに、
// 開くよう頼まれたことをその client のメッセージの行に出す（PR #223 のレビューの必須 1。利用者がフォーカスの移った先で
// 打ち続けたキーが、見ていないファイルのコマンドにならないため）。
// ペインへのフォーカスの移動は Config.FocusPane で頼む（別の goroutine）。
// 移せないとき（端末多重化ソフトの外、ペインを知らない）は、メッセージで知らせるだけにする。
// タブを bs にしたかを返す。
//
// req: FR-63
func (e *Editor) focusOwner(bs *bufferState) bool {
	o := bs.owner
	from := e.view.info.Pane
	asker := e.view.info.ID
	name := e.bufferName(bs)
	switched := false
	e.withView(o, func() {
		switch {
		case e.cur == bs:
			switched = true
		case e.viewIdle():
			e.enter(bs)
			switched = true
		default:
			e.message = infoMessage(fmt.Sprintf("client %d が %s を開こうとしました（入力の途中なのでタブは替えません）", asker, name))
		}
	})
	to := o.info.Pane
	if e.cfg.FocusPane == nil || from == "" || to == "" {
		e.message = infoMessage(fmt.Sprintf("%s は %s のタブです", name, ownerName(bs)))
		return switched
	}
	e.message = infoMessage(fmt.Sprintf("%s は %s のタブです（そのペインへ移ります）", name, ownerName(bs)))
	focus, log := e.cfg.FocusPane, e.log
	go func() {
		defer e.recoverPanic()
		if err := focus(from, to); err != nil {
			log.Warn("ペインへフォーカスを移せない", "from", from, "pane", to, "err", err)
		}
	}()
	return switched
}

// viewIdle は、読み込んでいる client が入力の途中でないか（ノーマルモードで、キーの列とキーマップの展開の途中でなく、
// hit-enter の問いも出していないか）を返す。別の client からカーソルのあるタブを替えてよいかを決める（focusOwner）。
func (e *Editor) viewIdle() bool {
	if e.cur == nil {
		return true
	}
	st := e.cur.state
	return st.Mode() == command.ModeNormal && !st.Pending() && !e.remap.pendingKeys() && e.regList == nil && !e.hitEnter()
}

// removeTab は、読み込んでいる client のタブから bs を外す。bs がカーソルのあるタブなら、裏のタブ、無ければ右の（無ければ左の）タブへ移る。
// 残るタブが無ければ、client を終える印（quit）を立てる（FR-114）。カーソルのあるタブは、閉じた bs のまま残す（reapClients が client を外す）。
func (e *Editor) removeTab(bs *bufferState) {
	c := e.view
	i := slices.Index(c.tabs, bs)
	if i < 0 {
		return
	}
	if bs == e.cur {
		var next *bufferState
		switch {
		case e.alt != nil && e.alt != bs && e.alt.owner == c:
			next = e.alt
		case i+1 < len(c.tabs):
			next = c.tabs[i+1]
		case i > 0:
			next = c.tabs[i-1]
		}
		if next == nil {
			// req: FR-114
			e.quit = true
		} else {
			e.enter(next)
		}
	}
	c.tabs = slices.Delete(c.tabs, i, i+1)
	bs.owner = nil
}

// closeBufferState は、バッファ bs を閉じる（FR-64）。タブなら持ち主の client のタブから外し、拡張機能へ buffer/close を送り、
// 一覧と、すべての client の裏のタブとジャンプリストから除く。未保存の変更は確かめない（呼び出し側が確かめる）。
//
// req: FR-64, FR-16
func (e *Editor) closeBufferState(bs *bufferState) {
	if o := bs.owner; o != nil {
		e.withView(o, func() { e.removeTab(bs) })
	}
	if err := e.host.CloseBuffer(bs.id); err != nil {
		e.log.Error("buffer/close を送れない", "err", err)
	}
	e.bufs = slices.DeleteFunc(e.bufs, func(o *bufferState) bool { return o == bs })
	if e.deferred != nil && e.deferred.bs == bs {
		e.deferred = nil
	}
	e.eachView(func(*clientView) {
		if e.alt == bs {
			e.alt = nil
		}
		kept := e.jumps[:0]
		for i, j := range e.jumps {
			if j.buf == bs.id {
				if i < e.jumpIdx {
					e.jumpIdx--
				}
				continue
			}
			kept = append(kept, j)
		}
		e.jumps = kept
		if e.jumpIdx > len(e.jumps) {
			e.jumpIdx = len(e.jumps)
		}
	})
}

// nextTab は、読み込んでいる client のタブの並びで、bs から step 個先（負なら前）のタブを返す。端の次は反対の端へ回る。
// bs がタブでなければ、最初のタブから数える。
func (e *Editor) nextTab(bs *bufferState, step int) *bufferState {
	tabs := e.view.tabs
	n := len(tabs)
	if n == 0 {
		return nil
	}
	i := max(slices.Index(tabs, bs), 0)
	return tabs[((i+step)%n+n)%n]
}

// tabMove は、:tabmove を行う。カーソルのあるタブ bs を、読み込んでいる client のタブの並びの中で動かす（FR-65）。
// 位置の数え方は Vim 9.1 の :tabmove と同じで、並びの外を指せば E475 を返す。
//
// req: FR-65
func (e *Editor) tabMove(bs *bufferState, a ex.Action) error {
	tabs := e.view.tabs
	i := slices.Index(tabs, bs)
	if i < 0 {
		return fmt.Errorf("内部の誤り: カーソルのあるバッファ %d がタブにありません", bs.id)
	}
	n := len(tabs)
	var to int
	switch {
	case a.Relative:
		to = i + a.Count
	case a.Count < 0:
		to = n - 1
	case a.Count > n:
		to = -1
	case a.Count <= i:
		// N 番目（1 から数える）のタブの後ろ。動かす前の番号で数える（Vim 9.1 と同じ）。
		to = a.Count
	default:
		to = a.Count - 1
	}
	if to < 0 || to >= n {
		return fmt.Errorf("E475: 無効な引数です: %s", a.Text)
	}
	tabs = slices.Delete(tabs, i, i+1)
	e.view.tabs = slices.Insert(tabs, to, bs)
	return nil
}

// uiTabs は、読み込んでいる client のタブの行の中身を返す（FR-62、FR-87）。名前はファイル名（パスの最後の要素）である。
func (e *Editor) uiTabs() []ui.Tab {
	tabs := make([]ui.Tab, len(e.view.tabs))
	for i, bs := range e.view.tabs {
		name := ""
		if bs.path != "" {
			name = filepath.Base(bs.path)
		}
		tabs[i] = ui.Tab{Name: name, Modified: bs.buf.Modified(), ReadOnly: bs.buf.ReadOnly(), Current: bs == e.cur}
	}
	return tabs
}

// findPath は、絶対パス abs のファイルのバッファを返す。無ければ nil を返す。
func (e *Editor) findPath(abs string) *bufferState {
	for _, bs := range e.bufs {
		if bs.path == abs {
			return bs
		}
	}
	return nil
}
