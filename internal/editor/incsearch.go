package editor

import (
	"context"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 打つたびの検索（基本設計 6.7 の「打つたびの検索」。FR-153）の、イベントループの側。
//
// command.State が、/ か ? でパターンを打っている間の問い（command.State.IncSearch）と、受け取った結果を持つ。ここは次を行う。
//
//   - 描くたびに（render）、client のカーソルのあるバッファの State の問いを読み、前に始めた問いと違えば、イベントループの外の
//     goroutine で一致を探し始める（startIncSearch）。前の検索は取り消す。問いは描くときに読むので、描く前に続けて届いたキー
//     （貼り付け、先打ち）は、まとめて 1 回だけ検索する。マクロとドットリピートの再生の中では、State が問いを返さない
//   - 結果は inspect でイベントループへ戻し、今の問いの結果だけを State へ渡す（incSearchDone）。前の問いの結果が後から届いたら捨てる
//   - 時間の上限（search.incrementalTimeoutMs）を超えたら、その回は一致を出さない
//   - 画面の位置は、打ち始めたときの位置を覚えておき、描くたびにそこへ戻してから、出す一致（無ければカーソル）が収まるように動かす
//     （Vim 9.1 の may_do_incsearch_highlighting が、毎回 old_viewstate に戻してから一致へ動かすのと同じ）。
//     打ち終えたら（<CR>、<Esc>、<C-c>、空の行の <BS>）、覚えた位置へ戻す。<CR> で移った先は、その後の描画が、ふつうの移動と同じく画面に収める
//   - 検索するテキストは、バッファの行を写したもの（target.Snapshot）である。打っている間にバッファが変わらなければ、写しを使い回す
//
// 出す一致の色とほかの一致の色は、検索のためだけの描画の経路を作らず、範囲の色の一覧（ui.Frame.Highlights）と、
// 検索の一致の色付けのパターン（ui.Frame.Search。FR-137）に詰める（bufferFrame）。

// incSearch は、1 つの client の、/ か ? でパターンを打っている間の打つたびの検索の状態である（viewFields.inc）。
type incSearch struct {
	// bs は、打っているバッファである。
	bs *bufferState
	// gen は、打っている入力の番号（command.IncSearch.Gen）である。
	gen uint64
	// top と left は、打ち始めたときの画面の位置（ui.Renderer の Top と Left）である。
	top, left int
	// q は、最後に検索を始めた問いである。started が false なら、まだ始めていない。
	q       command.IncSearch
	started bool
	// seq は、検索を始めるたびに進む番号である。結果がこれと違えば、前の問いの結果なので捨てる。
	seq uint64
	// cancel は、最後に始めた検索を取り消す。
	cancel context.CancelFunc
	// lines は、検索するテキストの写しである。buf と version は、写したときのバッファとバージョン番号である。
	lines   target.LineSnapshot
	buf     *buffer.Buffer
	version uint64
}

// incSearchQuery は、読み込んでいる client の、打つたびの検索の問いを返す。
// 打てるのは、カーソルのあるバッファがタブのバッファのときだけである。ポップアップの中のバッファで打った / は、打つたびの検索をしない
// （ポップアップの中の画面の位置は、窓の部品が決めるため）。
func (e *Editor) incSearchQuery() (command.IncSearch, bool) {
	if e.cur == nil || e.cur != e.tab() {
		return command.IncSearch{}, false
	}
	return e.cur.state.IncSearch()
}

// syncIncSearch は、読み込んでいる client の打つたびの検索を、State の今の問いに合わせる。描く前に呼ぶ（render）。
// 打ち終えていれば、取り消して画面の位置を戻す。打っていれば、画面の位置を打ち始めたときに戻し、問いが変わっていれば検索し直す。
//
// req: FR-153
func (e *Editor) syncIncSearch() {
	q, ok := e.incSearchQuery()
	inc := e.inc
	if inc != nil && (!ok || inc.gen != q.Gen || inc.bs != e.cur) {
		e.endIncSearch()
		inc = nil
	}
	if !ok {
		return
	}
	if inc == nil {
		inc = &incSearch{bs: e.cur, gen: q.Gen, top: e.renderer.Top(), left: e.renderer.Left()}
		e.inc = inc
	}
	e.renderer.SetScroll(inc.top, inc.left)
	if inc.started && inc.q == q {
		return
	}
	e.startIncSearch(inc, q)
}

// endIncSearch は、打つたびの検索を終える。走っている検索を取り消し、打っていたバッファを出したままなら、画面の位置を打ち始めたときに戻す。
func (e *Editor) endIncSearch() {
	inc := e.inc
	e.inc = nil
	if inc.cancel != nil {
		inc.cancel()
	}
	if inc.bs == e.cur {
		e.renderer.SetScroll(inc.top, inc.left)
	}
}

// startIncSearch は、問い q の一致を、イベントループの外の goroutine で探し始める。前の検索は取り消す。
// 時間の上限は、始めるときの search.incrementalTimeoutMs の値である（基本設計 7.3 の「エディタ全体」の行の規則）。
// テキストは、写しが今のバッファのものでなければ写し直す。写すのはイベントループの goroutine で、量は行の数に比例する。
//
// req: FR-153
func (e *Editor) startIncSearch(inc *incSearch, q command.IncSearch) {
	if inc.cancel != nil {
		inc.cancel()
	}
	if b := inc.bs.buf; inc.lines == nil || inc.buf != b || inc.version != b.Version() {
		inc.lines, inc.buf, inc.version = target.Snapshot(b), b, b.Version()
	}
	inc.q, inc.started = q, true
	inc.seq++
	limit := time.Duration(e.options.Get(option.SearchIncrementalTimeoutMs).Int()) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	inc.cancel = cancel
	c, seq, lines, find, stop := e.view, inc.seq, inc.lines, e.findMatch, e.stop
	go func() {
		defer e.recoverPanic()
		defer cancel()
		m, err := find(ctx, lines, q.From, q.Search, q.Count, q.Steps)
		select {
		case e.inspect <- func() { e.incSearchDone(c, inc, seq, q, m, err) }:
		case <-stop:
		}
	}()
}

// incSearchDone は、client c の打つたびの検索 inc の、seq 番目の検索の結果を受け取る。イベントループの goroutine で呼ぶ。
// 後に始めた検索がある（seq が違う）か、打ち終えていれば、捨てる。State も、今の問いの結果でなければ捨てる（command.State.ShowIncSearch）。
// 誤り（見つからない、パターンが正しくない、時間の上限を超えた）は、一致が無いとして渡し、利用者には知らせない。
// <CR> で決めたときに、同じ誤りを今の検索と同じく知らせる。
//
// req: FR-153
func (e *Editor) incSearchDone(c *clientView, inc *incSearch, seq uint64, q command.IncSearch, m target.Match, err error) {
	if !c.attached() || e.viewOf(c).inc != inc || inc.seq != seq {
		return
	}
	inc.bs.state.ShowIncSearch(q, m, err == nil)
}

// incSearchPattern は、打つたびの検索をしている間に、検索の一致の色（search）を付けるパターンを返す。
// 打っている間は、直前の検索のパターンの代わりに、出している一致を見つけたパターンに付け、一致を出していなければどこにも付けない
// （nvim 0.12.5 の 'hlsearch' と同じ。2026-11-20 に、一致の無いパターンを打っている間に直前の検索の一致の色が消えることと、
// :nohlsearch の後でも打っている間はほかの一致に色が付くことを確かめた）。ok が false なら打っていない。
//
// req: FR-153
func (e *Editor) incSearchPattern() (pattern string, ok bool) {
	if e.inc == nil || e.inc.bs != e.cur {
		return "", false
	}
	pat, _, found := e.cur.state.IncSearchMatch()
	if !found {
		return "", true
	}
	return pat, true
}

// incSearchFrame は、打つたびの検索で出す一致を、描画の入力 f に足す。一致を出していなければ何もしない。
// 画面を一致へ動かすため、f.Cursor を一致の先頭にし（ステータス行のルーラーも一致の位置を出す。nvim 0.12.5 と同じ）、
// 一致の範囲に色の名前 incsearch の色を、範囲の色の一覧（f.Highlights）の最後に足す。
// 空の一致（^ など）は、その位置の 1 文字に付ける。行末の位置の空の一致（$ など）は、範囲の色が行末のセルに付かないので出ない。
//
// req: FR-153
func (e *Editor) incSearchFrame(f *ui.Frame) {
	if e.inc == nil || e.inc.bs != e.cur {
		return
	}
	_, m, found := e.cur.state.IncSearchMatch()
	if !found {
		return
	}
	r := m.Range
	if r.Start == r.End {
		if b := e.cur.buf; r.Start.Line < b.LineCount() {
			line := b.Line(r.Start.Line)
			if r.Start.Byte < len(line) {
				_, size := utf8.DecodeRuneInString(line[r.Start.Byte:])
				r.End.Byte += size
			}
		}
	}
	f.Cursor = m.Range.Start
	f.Highlights = append(slices.Clip(f.Highlights), ui.Highlight{Range: r, Style: ui.StyleIncSearch})
}
