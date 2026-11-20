package editor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 試験で色の名前 incsearch、search、visual に付ける色である。
var (
	incAttr  = ui.Attr{Bg: ui.RGB(7, 7, 7)}
	srchAttr = ui.Attr{Bg: ui.RGB(1, 2, 3)}
	visAttr  = ui.Attr{Bg: ui.RGB(4, 5, 6)}
)

// incText は、20 行のテキストである。2 行目（0 から数える）に fox、14 行目に foo が 2 つある。
var incText = func() string {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = fmt.Sprintf("l%d", i)
	}
	lines[2], lines[14] = "l2 fox", "l14 foo foo"
	return strings.Join(lines, "\n") + "\n"
}()

// startInc は、a.txt に text を書き、40 列 8 行の偽の端末（タブの行、5 行の表示領域、ステータス行、コマンドラインの行）で起動する。
// options は設定の値である。色の名前 incsearch、search、visual の色を incAttr、srchAttr、visAttr にする。
func startInc(t *testing.T, text string, options map[string]option.Value) *harness {
	t.Helper()
	dir := writeFiles(t, map[string]string{"a.txt": text})
	term := newFakeTerm()
	term.w, term.h = 40, 8
	h := startTerm(t, term, Config{
		WorkDir: dir, Options: options,
		Colors: map[string]ui.Attr{ui.StyleIncSearch: incAttr, ui.StyleSearch: srchAttr, ui.StyleVisual: visAttr},
	}, "a.txt")
	h.settle()
	return h
}

// marks は、画面のバッファの表示領域（1 行目から 5 行目）の、色 a のセルの印を "|" でつないで返す（searchMarks）。
func (h *harness) marks(a ui.Attr) string {
	rows := make([]string, 5)
	for i := range rows {
		rows[i] = searchMarks(h.term, i+1, a)
	}
	return strings.Join(rows, "|")
}

// topLine は、表示領域の最初の行の番号を返す。イベントループの goroutine で呼ぶ。
func (h *harness) topLine() int { return h.e.renderer.Top() }

// / でパターンを打つたびに、カーソルから次の一致へ画面を動かし、その一致に incsearch の色を付ける。
// search.highlight が真なら、ほかの一致に search の色を付ける。ステータス行のルーラーも一致の位置を出す（nvim 0.12.5 と同じ）。
// <Esc> と <C-c> で取り消すと、カーソルと画面は打つ前に戻り、ジャンプリストと直前の検索は変わらない。
// <CR> で決めたときだけ、一致へ移り、ジャンプリストと直前の検索を記録する。
//
// req: FR-153
func TestIncSearchScreen(t *testing.T) {
	h := startInc(t, incText, map[string]option.Value{option.SearchHighlight: option.BoolValue(true)})
	var jumps string
	h.e.do(func() { jumps, _, _ = h.e.jumpList() })
	h.keys("/fo")
	h.eventually("/fo の一致", func() bool {
		return h.marks(incAttr) == "||   ^^||" && h.marks(srchAttr) == "||||" && h.topLine() == 0
	})
	h.keys("o")
	h.eventually("/foo の一致へ画面を動かす", func() bool {
		return h.topLine() == 12 && h.screenRow(3) == "l14 foo foo" &&
			h.marks(incAttr) == "||    ^^^||" && h.marks(srchAttr) == "||        ^^^||"
	})
	if got := h.statusRow(); !strings.Contains(got, "15,5") {
		t.Errorf("ルーラーが一致の位置でない: %q", got)
	}
	for _, cancel := range []string{"<Esc>", "<C-c>"} {
		if cancel == "<C-c>" {
			h.keys("/foo")
			h.eventually("/foo の一致", func() bool { return h.topLine() == 12 })
		}
		h.keys(cancel)
		h.settle()
		h.eventually(cancel+" で戻る", func() bool {
			list, _, cur := h.e.jumpList()
			return h.topLine() == 0 && cur == "a.txt:1.0" && list == jumps && h.e.cur.state.LastSearch().Pattern == "" &&
				h.marks(incAttr) == "||||" && h.marks(srchAttr) == "||||"
		})
	}
	h.keys("/foo<CR>")
	h.settle()
	h.eventually("<CR> で移る", func() bool {
		list, _, cur := h.e.jumpList()
		return cur == "a.txt:15.4" && list != jumps && h.e.cur.state.LastSearch().Pattern == "foo" &&
			h.marks(incAttr) == "||||" && h.marks(srchAttr) == "||    ^^^ ^^^||"
	})
}

// 一致が無いときと、打っている途中のパターンが正規表現として正しくないときは、誤りを出さずに一致を出さず、
// カーソルと画面を打つ前のままにする。打っている間は、直前の検索の一致にも search の色を付けない（nvim 0.12.5 と同じ）。
// :nohlsearch で消した後でも、打っている間はほかの一致に search の色を付け、取り消したら消したままに戻る。
// 誤りは <CR> で決めたときに、今の検索と同じく出す。
//
// req: FR-153
func TestIncSearchNoMatch(t *testing.T) {
	h := startInc(t, incText, map[string]option.Value{option.SearchHighlight: option.BoolValue(true)})
	h.keys("/fox<CR>gg")
	h.settle()
	h.eventually("直前の検索の色", func() bool { return h.marks(srchAttr) == "||   ^^^||" })
	h.keys("/zzz")
	h.settle()
	h.eventually("一致の無いパターン", func() bool {
		return h.screenRow(7) == "/zzz" && h.marks(srchAttr) == "||||" && h.marks(incAttr) == "||||" && h.topLine() == 0
	})
	h.keys("<BS><BS><BS>l2 (")
	h.settle()
	h.eventually("正しくないパターン", func() bool {
		return h.screenRow(7) == "/l2 (" && h.e.message.Text == "" && h.marks(incAttr) == "||||" &&
			h.e.cur.state.Cursor().String() == "0:0"
	})
	h.keys("<CR>")
	h.settle()
	h.eventually("<CR> で誤りを出す", func() bool { return strings.HasPrefix(h.e.message.Text, "E383") })
	h.keys(":noh<CR>/fo")
	h.settle()
	h.eventually(":nohlsearch の後に打つ", func() bool {
		return h.marks(incAttr) == "||   ^^||" && h.marks(srchAttr) == "||||"
	})
	h.keys("<Esc>")
	h.settle()
	h.eventually("取り消した後は消したまま", func() bool { return h.marks(srchAttr) == "||||" && h.marks(incAttr) == "||||" })
}

// オペレーターの後の /（d/foo）、ビジュアルモードの /、? の後ろ向き、回数と <C-g> も、打っている間に同じく一致を出す。
// ビジュアルモードでは、選択を一致まで広げて出す。
//
// req: FR-153
func TestIncSearchModes(t *testing.T) {
	h := startInc(t, "abc foo bar\nfoo x foo\n", nil)
	h.keys("d/foo")
	h.eventually("d/foo", func() bool { return h.marks(incAttr) == "    ^^^||||" })
	h.keys("<CR>")
	h.settle()
	h.eventually("d/foo<CR>", func() bool { return h.screenRow(1) == "foo bar" })
	h.keys("v/x")
	// 1 行目は、行末のセル（改行）まで選択の色が付く。選択は一致の先頭の文字（2 行目の x）まで選択の色で出し、
	// 一致には incsearch の色を付けない（nvim 0.12.5 と同じ。PR #306 のレビューの推奨 1）。
	h.eventually("v/x の選択", func() bool {
		return h.marks(incAttr) == "||||" && h.marks(visAttr) == "^^^^^^^^|^^^^^|||"
	})
	h.keys("<Esc><Esc>G$?foo")
	h.eventually("?foo", func() bool { return h.marks(incAttr) == "|      ^^^|||" })
	h.keys("<C-g>")
	h.eventually("?foo<C-g>（末尾の方へ動いて折り返す）", func() bool { return h.marks(incAttr) == "^^^||||" })
	h.keys("<Esc>gg02/foo")
	h.eventually("2/foo", func() bool { return h.marks(incAttr) == "|      ^^^|||" })
	// 回数を付けたときは、<C-t> 1 回で回数の数だけ前の一致へ動く（1:6 から 1:0、0:0、折り返して 1:6、1:0）。
	h.keys("<C-t><C-t>")
	h.eventually("2/foo<C-t><C-t>", func() bool { return h.marks(incAttr) == "|^^^|||" })
	h.keys("<CR>")
	h.settle()
	h.eventually("2/foo<C-t><C-t><CR>", func() bool { return h.e.cur.state.Cursor().String() == "1:0" })
}

// 見比べている間に / で打っても、最初の行の上の詰め物の行は残り、<Esc> で取り消した後も打つ前と同じ画面の位置に戻る
// （nvim 0.12.5 の nvim -d でも、打っている間と <Esc> の後に詰め物の行が残る。PR #306 のレビューの必須 1）。
//
// req: FR-153
func TestIncSearchDiffFill(t *testing.T) {
	term := newFakeTerm()
	term.w, term.h = 40, 12
	h, _, _, _ := startDiffPairTerm(t, term, map[string]string{
		"a.txt": "alpha\nbeta foo\ngamma\ndelta\n",
		"b.txt": "new1\nnew2\nnew3\nalpha\nbeta foo\ngamma\ndelta\n",
	})
	diffTop := func() string {
		top, fill := h.e.viewOf(h.e.clients[0]).renderer.DiffTop()
		return fmt.Sprintf("%d+%d", top, fill)
	}
	h.eventually("打つ前の詰め物", func() bool { return diffTop() == "0+3" })
	h.keys("/fo")
	h.eventually("打っている間の詰め物", func() bool {
		_, _, found := h.e.cur.state.IncSearchMatch()
		return found && diffTop() == "0+3"
	})
	h.keys("<Esc>")
	h.settle()
	h.eventually("<Esc> の後の詰め物", func() bool { return h.e.inc == nil && diffTop() == "0+3" })
	h.keys("/delta<CR>")
	h.settle()
	h.eventually("<CR> で移った後", func() bool { return h.e.cur.state.Cursor().String() == "3:0" && diffTop() == "0+3" })
}

// findCall は、偽の findMatch の 1 回の呼び出しである。
type findCall struct {
	pattern  string
	deadline time.Duration
	release  chan error
	released bool // fakeFind.mu で守る
}

// fakeFind は、Editor.findMatch に差し替える偽物である。呼ばれたら calls に積み、release で返させるまで返さない。
// 返させた値が nil なら本物の target.FindMatch の結果を（取り消しを無視して）返し、nil でなければその誤りを返す。
// イベントループがキーの間で描くと、打ったキーの数より検索の回数が増えることがあるので、試験は回数を数えずに、
// 最後の呼び出しのパターンで待つ（waitLast。PR #306 のレビューの必須 2）。
type fakeFind struct {
	mu    sync.Mutex
	calls []*findCall
}

// newFakeFind は、h のエディタの findMatch を偽物に替える。試験の終わりに、返していない呼び出しを返させる。
func newFakeFind(h *harness) *fakeFind {
	ff := &fakeFind{}
	h.e.do(func() { h.e.findMatch = ff.find })
	h.t.Cleanup(func() { ff.releaseAll(context.Canceled) })
	return ff
}

func (f *fakeFind) find(ctx context.Context, src target.Lines, from buffer.Position, s target.Search, count, steps int) (target.Match, error) {
	c := &findCall{pattern: s.Pattern, release: make(chan error, 1)}
	if d, ok := ctx.Deadline(); ok {
		c.deadline = time.Until(d)
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if err := <-c.release; err != nil {
		return target.Match{}, err
	}
	return target.FindMatch(context.Background(), src, from, s, count, steps)
}

// count は、呼ばれた回数を返す。
func (f *fakeFind) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// waitLast は、from 番目より後の呼び出しがあり、最後の呼び出しのパターンが pattern になるまで待ち、その呼び出しを返す。
func (f *fakeFind) waitLast(h *harness, from int, pattern string) *findCall {
	h.t.Helper()
	var c *findCall
	h.eventually(pattern+" の検索", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if n := len(f.calls); n > from && f.calls[n-1].pattern == pattern {
			c = f.calls[n-1]
			return true
		}
		return false
	})
	return c
}

// release は、呼び出し c を err で返させる。返させた後なら何もしない。
func (f *fakeFind) release(c *findCall, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !c.released {
		c.released = true
		c.release <- err
	}
}

// releaseAll は、返していない呼び出しをすべて err で返させる。
func (f *fakeFind) releaseAll(err error) {
	f.mu.Lock()
	calls := slices.Clone(f.calls)
	f.mu.Unlock()
	for _, c := range calls {
		f.release(c, err)
	}
}

// 打つたびの検索はイベントループを止めない。検索が終わらない間も、打ったキーはすぐコマンドラインの行に出る。
// 後から届いた前の問いの結果は捨てる。時間の上限（search.incrementalTimeoutMs）を超えた回は、一致を出さない。
//
// req: FR-153
func TestIncSearchAsync(t *testing.T) {
	h := startInc(t, incText, map[string]option.Value{option.SearchIncrementalTimeoutMs: option.IntValue(50)})
	ff := newFakeFind(h)
	h.keys("/f")
	ff.waitLast(h, 0, "f")
	h.keys("ox")
	h.settle()
	h.eventually("検索の終わりを待たずに打ったキーを出す", func() bool { return h.screenRow(7) == "/fox" })
	c := ff.waitLast(h, 0, "fox")
	if c.deadline <= 0 || c.deadline > 50*time.Millisecond {
		t.Errorf("検索の上限まで %v", c.deadline)
	}
	ff.release(c, nil)
	h.eventually("今の問いの結果", func() bool { return h.marks(incAttr) == "||   ^^^||" })
	// 前の問い（f と、キーの間に描いたなら fo）の結果を後から届ける。
	ff.releaseAll(nil)
	time.Sleep(20 * time.Millisecond)
	h.settle()
	h.eventually("前の問いの結果は捨てる", func() bool { return h.marks(incAttr) == "||   ^^^||" })
	n := ff.count()
	h.keys("<BS>")
	ff.release(ff.waitLast(h, n, "fo"), context.DeadlineExceeded)
	h.eventually("時間の上限を超えた回は一致を出さない", func() bool {
		return h.marks(incAttr) == "||||" && h.screenRow(7) == "/fo" && h.e.cur.state.Cursor().String() == "0:0"
	})
	n = ff.count()
	h.keys("<CR>")
	h.settle()
	h.eventually("<CR> は上限を付けずに検索する", func() bool { return h.e.cur.state.Cursor().String() == "2:3" })
	if got := ff.count(); got != n {
		t.Errorf("<CR> で打つたびの検索をした: %d 回から %d 回", n, got)
	}
	h.e.do(func() {
		if h.e.inc != nil {
			t.Error("打ち終えた後に打つたびの検索の状態が残った")
		}
	})
}

// マクロとドットリピートの再生の中では、打つたびの検索をせず、<CR> を押したときと同じ結果になる。
//
// req: FR-153
func TestIncSearchReplay(t *testing.T) {
	h := startInc(t, "a foo b foo c foo d foo e foo\n", nil)
	ff := newFakeFind(h)
	h.keys("qa/foo")
	ff.waitLast(h, 0, "foo")
	ff.releaseAll(nil)
	n := ff.count()
	h.keys("<CR>qd/foo")
	ff.waitLast(h, n, "foo")
	ff.releaseAll(errors.New("unused"))
	h.keys("<CR>")
	h.settle()
	h.eventually("d/foo<CR>", func() bool { return h.e.cur.buf.Text() == "a foo c foo d foo e foo" })
	n = ff.count()
	h.keys(".")
	h.settle()
	h.eventually(".", func() bool {
		return h.e.cur.buf.Text() == "a foo d foo e foo" && h.e.cur.state.Cursor().String() == "0:2"
	})
	h.keys("@a")
	h.settle()
	h.eventually("@a", func() bool { return h.e.cur.state.Cursor().String() == "0:8" })
	if got := ff.count(); got != n {
		t.Errorf("再生の中で打つたびの検索をした: %d 回から %d 回", n, got)
	}
}
