package scroll

import "testing"

// uniform は、どの行の高さも 1 の n 行である。
type uniform int

func (n uniform) LineCount() int { return int(n) }
func (uniform) Rows(int) int     { return 1 }

// 100 行のバッファを 10 行の表示領域で見るときの位置を、Neovim 0.12.5（--clean、nowrap）の値と比べる。
// 期待値は、2026-11-21 に nvim --headless で、1 行目から各キーを打った後の line("w0") を 0 から数えたものである
// （internal/editor の TestScrollNeovim の jk と z のケースの一部）。
//
// req: FR-174
func TestViewNeovim(t *testing.T) {
	tests := []struct {
		name              string
		margin, top, line int // キーの前の最初の行と、カーソルが移った行（0 から数える）
		op                func(v *View, line int)
		want              int
	}{
		{"50G", 0, 0, 49, func(v *View, l int) { v.Follow(l, 0, 0) }, 45},
		{"47G の後", 0, 45, 46, func(v *View, l int) { v.Follow(l, 0, 0) }, 45},
		{"60G", 0, 45, 59, func(v *View, l int) { v.Follow(l, 0, 0) }, 50},
		{"47G（余白 5）", 5, 45, 46, func(v *View, l int) { v.Follow(l, 0, 0) }, 42},
		{"60G（余白 5）", 5, 42, 59, func(v *View, l int) { v.Follow(l, 0, 0) }, 55},
		{"8j（余白 5）", 5, 0, 10, func(v *View, l int) { v.Follow(l, 0, 0) }, 5},
		{"zt", 0, 10, 19, func(v *View, l int) { v.CursorTop(l, 0, 1, 0, true) }, 19},
		{"zt（余白 5）", 5, 10, 19, func(v *View, l int) { v.CursorTop(l, 0, 1, 0, true) }, 15},
		{"zz", 0, 19, 19, func(v *View, l int) { v.CursorHalfway(l, true, false) }, 15},
		{"zb", 0, 15, 19, func(v *View, l int) { v.CursorBot(l, 0, true) }, 10},
		{"zb（余白 5）", 5, 15, 19, func(v *View, l int) { v.CursorBot(l, 0, true) }, 14},
		{"95zz（バッファの末尾）", 0, 94, 94, func(v *View, l int) { v.CursorHalfway(l, true, false) }, 90},
		{"98zb", 0, 90, 97, func(v *View, l int) { v.CursorBot(l, 0, true) }, 88},
	}
	for _, tt := range tests {
		v := View{Lines: uniform(100), Height: 10, Margin: tt.margin, Top: tt.top}
		tt.op(&v, tt.line)
		if v.Top != tt.want || v.Skip != 0 {
			t.Errorf("%s: 最初の行 %d、隠す行 %d, want %d、0", tt.name, v.Top, v.Skip, tt.want)
		}
	}
}

// 余白の外へ動かすカーソルの行（CursorCorrect）は、最初の行が 0 なら上の余白を、最後の行まで見えていれば下の余白を求めない。
// <C-e> と <C-y>（ScrollRedraw）は、表示領域を送り、余白があってバッファの端で動けなければカーソルを 1 行送る。
//
// req: FR-174
func TestCursorCorrectAndScrollRedraw(t *testing.T) {
	v := View{Lines: uniform(100), Height: 10, Margin: 3, Top: 20}
	if got := v.CursorCorrect(20); got != 23 {
		t.Errorf("上の余白: %d, want 23", got)
	}
	if got := v.CursorCorrect(29); got != 26 {
		t.Errorf("下の余白: %d, want 26", got)
	}
	v.Top = 0
	if got := v.CursorCorrect(0); got != 0 {
		t.Errorf("最初の行が 0: %d, want 0", got)
	}
	v.Top = 90
	if got := v.CursorCorrect(99); got != 99 {
		t.Errorf("最後の行まで見える: %d, want 99", got)
	}
	// 余白の 3 行を残すので、<C-e> は 1 行目のカーソルを 4 行目（0 から数えて 4）へ送る。
	v = View{Lines: uniform(100), Height: 10, Margin: 3}
	if got := v.ScrollRedraw(true, 1, 0, false); got != 4 || v.Top != 1 {
		t.Errorf("<C-e>: カーソル %d、最初の行 %d, want 4、1", got, v.Top)
	}
	// 最後の行が最初の行なら送れないので、カーソルを 1 行下へ動かす。
	v = View{Lines: uniform(100), Height: 10, Margin: 3, Top: 99}
	if got := v.ScrollRedraw(true, 1, 98, false); got != 99 || v.Top != 99 {
		t.Errorf("末尾の <C-e>: カーソル %d、最初の行 %d, want 99、99", got, v.Top)
	}
}
