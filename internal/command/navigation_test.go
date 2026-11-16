package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// fakeViewport は、テストの表示領域である。
type fakeViewport struct{ top, rows int }

func (v *fakeViewport) Top() int       { return v.top }
func (v *fakeViewport) Rows() int      { return v.rows }
func (v *fakeViewport) SetTop(top int) { v.top = top }

// navText は、矢印キーなどの突き合わせに使ったテキストである。
const navText = "abc def\nghijkl\n\n  mn opq\nrs"

// navCase は、Vim 9.1 と突き合わせた、矢印キー、<Home>、<End>、インサートモードのカーソルキーのケースである。
//
// 期待値は、Vim 9.1（パッチ 1-16）を `vim -Nu NONE -n --not-a-term` で起動し、navText を置いてカーソルを先頭に置き、
// keys を :normal で入力した後のテキストとカーソル（行:バイト）と curswant である。
// アンドゥとドットリピートを含むケース（u と . のあるもの）は、:normal の中ではアンドゥの単位が変わるので、
// tmux の中の Vim に tmux send-keys でキーを送って取った。
// want が -1 のケースは、望みの列を比べない。
type navCase struct {
	keys   string
	text   string
	cursor string
	want   int
}

var navCases = []navCase{
	// ノーマルモード。h、j、k、l、0、$ と同じ働き。
	{"<Right><Right>", navText, "0:2", 2},
	{"<Right><Right><Left>", navText, "0:1", 1},
	{"<Right><Right><Left><Down>", navText, "1:1", 1},
	{"<Right><Right><Left><Down><Down>", navText, "2:0", 1},
	{"<Right><Right><Left><Down><Down><Up>", navText, "1:1", 1},
	{"<End>", navText, "0:6", target.WantEOL},
	{"<End><Down>", navText, "1:5", target.WantEOL},
	{"<End><Down><Home>", navText, "1:0", 0},
	{"<End><Down><Home><Down><Down><End>", navText, "3:7", target.WantEOL},
	{"3<Right>", navText, "0:3", 3},
	{"3<Right>2<Down>", navText, "2:0", 3},
	{"3<Right>2<Down>9<Up>", navText, "0:3", 3},
	{"3<Right>2<Down>9<Up>3<Left>", navText, "0:0", 0},
	{"<Left>", navText, "0:0", 0},
	{"<Up>", navText, "0:0", 0},
	{"G<Right><Right><Right>", navText, "4:1", 1},
	// オペレーターの後とビジュアルモード。
	{"d<Right>", "bc def\nghijkl\n\n  mn opq\nrs", "0:0", -1},
	{"d<Right>d<Down>", "\n  mn opq\nrs", "0:0", -1},
	{"d<End>", "\nghijkl\n\n  mn opq\nrs", "0:0", -1},
	{"d<End>j$d<Home>", "\nl\n\n  mn opq\nrs", "1:0", -1},
	{"v<Right><Right>d", " def\nghijkl\n\n  mn opq\nrs", "0:0", -1},
	{"v<Right><Right>dvj<End>d", "\n  mn opq\nrs", "0:0", -1},
	// インサートモード。
	{"ifoo<Left>bar<Esc>", "fobaroabc def\nghijkl\n\n  mn opq\nrs", "0:4", -1},
	{"ifoo<Left>bar<Esc>u", "fooabc def\nghijkl\n\n  mn opq\nrs", "0:2", -1},
	{"ifoo<Left>bar<Esc>uu", navText, "0:0", -1},
	{"ifoo<Left>bar<Esc>j0.", "fobaroabc def\nbarghijkl\n\n  mn opq\nrs", "1:2", -1},
	{`ifoo<Left>bar<Esc>".p`, "fobarbaroabc def\nghijkl\n\n  mn opq\nrs", "0:7", -1},
	{`ifoo<Left><Esc>".p`, "fofoooabc def\nghijkl\n\n  mn opq\nrs", "0:4", -1},
	{"ifoo<Left><Esc>j0.", "fooabc def\nfooghijkl\n\n  mn opq\nrs", "1:2", -1},
	{"ifoo<Left><Left>x<Right>y<Esc>uu", "fooabc def\nghijkl\n\n  mn opq\nrs", "0:1", -1},
	{"3ifoo<Left>bar<Esc>", "fobaroabc def\nghijkl\n\n  mn opq\nrs", "0:4", -1},
	{"3ifoo<Left>bar<Esc>u", "fooabc def\nghijkl\n\n  mn opq\nrs", "0:2", -1},
	{"3ifoo<Left><Esc>", "fooabc def\nghijkl\n\n  mn opq\nrs", "0:1", -1},
	{"ifoo<Right><Esc>", "fooabc def\nghijkl\n\n  mn opq\nrs", "0:3", -1},
	{"ifoo<Down>x<Esc>", "fooabc def\nghixjkl\n\n  mn opq\nrs", "1:3", -1},
	{"ifoo<Down>x<Esc>u", "fooabc def\nghijkl\n\n  mn opq\nrs", "1:3", -1},
	{"Ax<Up><Esc>", "abc defx\nghijkl\n\n  mn opq\nrs", "0:7", -1},
	{"jAx<Up>y<Esc>", "abc defy\nghijklx\n\n  mn opq\nrs", "0:7", -1},
	{"jjjAx<Up>y<Esc>", "abc def\nghijkl\ny\n  mn opqx\nrs", "2:0", -1},
	{"ix<Home>y<Esc>", "yxabc def\nghijkl\n\n  mn opq\nrs", "0:0", -1},
	{"ix<End>y<Esc>", "xabc defy\nghijkl\n\n  mn opq\nrs", "0:8", -1},
	{"i<End><Esc>", navText, "0:6", -1},
	{"jjji<Down>z<Esc>", "abc def\nghijkl\n\n  mn opq\nzrs", "4:0", -1},
	{"i<Left>z<Esc>", "zabc def\nghijkl\n\n  mn opq\nrs", "0:0", -1},
	{"A<Right>z<Esc>", "abc defz\nghijkl\n\n  mn opq\nrs", "0:7", -1},
	{"ix<PageDown><Esc>", "xabc def\nghijkl\n\n  mn opq\nrs", "4:0", -1},
	{"oxyz<Up>w<Esc>", "abcw def\nxyz\nghijkl\n\n  mn opq\nrs", "0:3", -1},
	{"oxyz<Up>w<Esc>u", "abc def\nxyz\nghijkl\n\n  mn opq\nrs", "0:3", -1},
	{"oxyz<Up>w<Esc>j.", "abcw def\nxywz\nghijkl\n\n  mn opq\nrs", "1:2", -1},
}

// TestNavigationKeysVim は、矢印キー、<Home>、<End> と、インサートモードのカーソルキーを Vim 9.1 と突き合わせる。
// インサートモードのカーソルキーは、入力をアンドゥとドットリピートの単位で区切り、回数による繰り返しをやめる。
//
// req: FR-01, FR-07
func TestNavigationKeysVim(t *testing.T) {
	for _, tc := range navCases {
		t.Run(tc.keys, func(t *testing.T) {
			s := New(newBuffer(navText), input.NewParser(input.DefaultKeymap()))
			s.Viewport = &fakeViewport{rows: 22} // Vim の 24 行の端末の表示領域。テキストが全部入る
			feedKeys(s, tc.keys)
			if got := bufText(s); got != tc.text {
				t.Errorf("テキスト = %q, want %q", got, tc.text)
			}
			c := s.Cursor()
			if got := fmt.Sprintf("%d:%d", c.Line, c.Byte); got != tc.cursor {
				t.Errorf("カーソル = %s, want %s", got, tc.cursor)
			}
			if tc.want >= 0 && s.Want() != tc.want {
				t.Errorf("望みの列 = %d, want %d", s.Want(), tc.want)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("モード = %v", s.Mode())
			}
		})
	}
}

func bufText(s *State) string {
	b := s.Buffer()
	lines := make([]string, b.LineCount())
	for i := range lines {
		lines[i] = b.Line(i)
	}
	return strings.Join(lines, "\n")
}

// pageText は、ページ単位のスクロールの突き合わせに使った 30 行のテキストである。3 の倍数の行は空白で始まり、14 行目は空である。
func pageText() string {
	lines := make([]string, 30)
	for i := range lines {
		n := i + 1
		switch {
		case n == 14:
		case n%3 == 0:
			lines[i] = fmt.Sprintf("  line %d abcdef", n)
		default:
			lines[i] = fmt.Sprintf("line %d abcdef", n)
		}
	}
	return strings.Join(lines, "\n")
}

// pageCase は、Vim 9.1 と突き合わせた <PageUp> と <PageDown> のケースである。行と列は Vim と同じく 1 から数える。
//
// 期待値は、Vim 9.1（パッチ 1-16）を `vim -Nu NONE -n --not-a-term` で起動し、'laststatus' を 2、'lines' を rows+2 にして
// 表示領域を rows 行にし、winrestview() で最初の行を top、カーソルを line 行 col 列に置いてから keys を :normal で入力した後の、
// line('w0')、line('.')、col('.') である。
type pageCase struct {
	rows, top, line, col int
	keys                 string
	wTop, wLine, wCol    int
	fails                bool
}

var pageCases = []pageCase{
	{10, 1, 1, 5, "<PageDown>", 9, 9, 3, false},
	{10, 1, 5, 5, "<PageDown>", 9, 9, 3, false},
	{10, 1, 10, 5, "<PageDown>", 9, 9, 3, false},
	{10, 9, 9, 5, "<PageDown>", 17, 17, 1, false},
	{10, 17, 20, 5, "<PageDown>", 25, 25, 1, false},
	{10, 21, 25, 5, "<PageDown>", 30, 30, 3, false},
	{10, 25, 28, 5, "<PageDown>", 30, 30, 3, false},
	{10, 29, 29, 5, "<PageDown>", 30, 30, 3, false},
	{10, 30, 30, 5, "<PageDown>", 30, 30, 5, true},
	{10, 1, 1, 5, "2<PageDown>", 17, 17, 1, false},
	{10, 1, 1, 5, "3<PageDown>", 25, 25, 1, false},
	{10, 1, 1, 5, "9<PageDown>", 30, 30, 5, true},
	{10, 6, 6, 5, "<PageDown>", 14, 14, 1, false},
	{10, 1, 1, 5, "<PageUp>", 1, 1, 5, true},
	{10, 9, 9, 5, "<PageUp>", 1, 10, 1, false},
	{10, 9, 15, 5, "<PageUp>", 1, 10, 1, false},
	{10, 9, 18, 5, "<PageUp>", 1, 10, 1, false},
	{10, 5, 7, 5, "<PageUp>", 1, 6, 3, false},
	{10, 2, 2, 5, "<PageUp>", 1, 3, 3, false},
	{10, 10, 10, 5, "<PageUp>", 1, 10, 1, false},
	{10, 11, 11, 5, "<PageUp>", 3, 12, 3, false},
	{10, 12, 12, 5, "<PageUp>", 4, 13, 1, false},
	{10, 21, 30, 5, "<PageUp>", 13, 22, 1, false},
	{10, 21, 30, 5, "2<PageUp>", 5, 14, 1, false},
	{10, 21, 30, 5, "9<PageUp>", 1, 6, 5, true},
	{10, 9, 12, 5, "3<PageUp>", 1, 10, 5, true},
	{10, 30, 30, 5, "<PageUp>", 20, 29, 1, false},
	{10, 29, 29, 5, "<PageUp>", 20, 29, 1, false},
	{10, 28, 28, 5, "<PageUp>", 20, 29, 1, false},
	{10, 25, 28, 5, "<PageUp>", 17, 26, 1, false},
	{10, 1, 1, 5, "<PageDown><PageUp>", 1, 10, 1, false},
	{10, 1, 1, 5, "<PageDown><PageDown><PageDown><PageDown>", 30, 30, 3, false},
	{10, 9, 12, 5, "<PageDown>j<PageUp>", 9, 18, 3, false},
	{5, 1, 1, 5, "<PageDown>", 4, 4, 1, false},
	{5, 1, 1, 5, "<PageUp>", 1, 1, 5, true},
	{5, 10, 12, 5, "<PageUp>", 7, 11, 1, false},
	{5, 3, 3, 5, "<PageUp>", 1, 4, 1, false},
	{5, 4, 4, 5, "<PageUp>", 1, 5, 1, false},
	{5, 27, 27, 5, "<PageUp>", 24, 28, 1, false},
	{5, 28, 28, 5, "<PageUp>", 25, 29, 1, false},
	{5, 29, 29, 5, "<PageUp>", 25, 29, 1, false},
	{5, 30, 30, 5, "<PageUp>", 25, 29, 1, false},
	{5, 26, 27, 5, "<PageDown>", 30, 30, 3, false},
	{5, 25, 27, 5, "<PageDown>", 28, 28, 1, false},
	{4, 1, 1, 5, "<PageDown>", 4, 4, 1, false},
	{4, 10, 10, 5, "<PageUp>", 7, 10, 1, false},
	{4, 28, 28, 5, "<PageUp>", 25, 28, 1, false},
	{4, 29, 29, 5, "<PageUp>", 26, 29, 1, false},
	{4, 30, 30, 5, "<PageUp>", 26, 29, 1, false},
	{4, 3, 3, 5, "<PageUp>", 1, 3, 3, false},
	{3, 1, 1, 5, "<PageDown>", 4, 4, 1, false},
	{3, 10, 10, 5, "<PageUp>", 7, 9, 3, false},
	{3, 30, 30, 5, "<PageUp>", 27, 29, 1, false},
	{2, 1, 1, 5, "<PageDown>", 3, 3, 3, false},
	{2, 10, 10, 5, "<PageUp>", 8, 9, 3, false},
	{1, 1, 1, 5, "<PageDown>", 2, 2, 1, false},
	{1, 10, 10, 5, "<PageUp>", 9, 9, 3, false},
	{40, 1, 1, 5, "<PageDown>", 30, 30, 3, false},
	{40, 1, 5, 5, "<PageUp>", 1, 5, 5, true},
	{40, 1, 5, 5, "<PageDown><PageUp>", 1, 29, 1, false},
}

// TestPageVim は、<PageUp> と <PageDown> のスクロールとカーソルを Vim 9.1 と突き合わせる。
//
// req: FR-03
func TestPageVim(t *testing.T) {
	for _, tc := range pageCases {
		t.Run(fmt.Sprintf("rows%d_top%d_%d,%d_%s", tc.rows, tc.top, tc.line, tc.col, tc.keys), func(t *testing.T) {
			s := New(newBuffer(pageText()), input.NewParser(input.DefaultKeymap()))
			vp := &fakeViewport{top: tc.top - 1, rows: tc.rows}
			s.Viewport = vp
			s.SetCursor(pos(tc.line-1, tc.col-1))
			failed := false
			for _, r := range feedKeys(s, tc.keys) {
				failed = failed || r.Err != nil
			}
			c := s.Cursor()
			got := fmt.Sprintf("%d %d %d", vp.top+1, c.Line+1, c.Byte+1)
			if want := fmt.Sprintf("%d %d %d", tc.wTop, tc.wLine, tc.wCol); got != want {
				t.Errorf("最初の行、カーソルの行、列 = %s, want %s", got, want)
			}
			if failed != tc.fails {
				t.Errorf("失敗 = %v, want %v", failed, tc.fails)
			}
		})
	}
}

// TestPageModes は、ビジュアルモードの <PageDown> が選択を広げ、オペレーターの後では誤りになり、
// 表示領域が無ければ失敗することを確かめる。
func TestPageModes(t *testing.T) {
	t.Run("ビジュアルモード", func(t *testing.T) {
		s := New(newBuffer(pageText()), input.NewParser(input.DefaultKeymap()))
		s.Viewport = &fakeViewport{rows: 10}
		feedKeys(s, "v<PageDown>d")
		if got, want := s.Buffer().Line(0), "ine 9 abcdef"; got != want { // Vim 9.1 と同じ
			t.Errorf("1 行目 = %q, want %q", got, want)
		}
	})
	t.Run("オペレーターの後", func(t *testing.T) {
		s := New(newBuffer(pageText()), input.NewParser(input.DefaultKeymap()))
		vp := &fakeViewport{rows: 10}
		s.Viewport = vp
		rs := feedKeys(s, "d<PageDown>")
		if rs[1].Err == nil || vp.top != 0 || s.Buffer().LineCount() != 30 {
			t.Errorf("d<PageDown>: err=%v top=%d 行数=%d", rs[1].Err, vp.top, s.Buffer().LineCount())
		}
	})
	t.Run("表示領域が無い", func(t *testing.T) {
		s := New(newBuffer(pageText()), input.NewParser(input.DefaultKeymap()))
		if r := s.Feed(editcmd.Special(editcmd.CodePageDown)); r.Err == nil || s.Cursor().Line != 0 {
			t.Errorf("err=%v カーソル=%v", r.Err, s.Cursor())
		}
	})
	t.Run("カーソルが表示領域の外", func(t *testing.T) {
		// 描画の前に続けて動いた場合。カーソルを下の端に寄せた表示領域（21〜30 行）から動かす。
		s := New(newBuffer(pageText()), input.NewParser(input.DefaultKeymap()))
		vp := &fakeViewport{rows: 10}
		s.Viewport = vp
		s.SetCursor(pos(29, 0))
		feedKeys(s, "<PageUp>")
		if vp.top != 12 || s.Cursor().Line != 21 {
			t.Errorf("top=%d カーソル=%v, want 12, 21", vp.top, s.Cursor())
		}
	})
}
