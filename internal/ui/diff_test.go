package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// diffTheme は、差分の色を背景の色の番号で読み分けるテーマである。
func diffTheme() Theme {
	return Theme{
		StyleDiffAdd:    {Bg: Indexed(4)},
		StyleDiffChange: {Bg: Indexed(5)},
		StyleDiffDelete: {Fg: Indexed(12), Bg: Indexed(6)},
		StyleDiffText:   {Bg: Indexed(9), Bold: true},
		StyleKeyword:    {Fg: Indexed(3)},
	}
}

// rowTexts は、表示領域の最初の n 行の文字を、右の空白を除いて返す。
func rowTexts(s Screen, n int) []string {
	out := make([]string, n)
	for i := range n {
		out[i] = strings.TrimRight(s.Grid.RowText(i), " ")
	}
	return out
}

// TestRenderDiff は、差分の色と詰め物の行の描き方を確かめる。
// 詰め物は行の上に描き、最後の行の下の詰め物は行の数の位置に置く。変えた行と足した行は、行末から右の端までを行の色で埋め、
// 違う文字の範囲を diff.text で重ね、構文の文字の色は差分の背景の色の上に出す。
//
// req: FR-99
func TestRenderDiff(t *testing.T) {
	// A の側: a b c d e。b を変え（中の 1 文字が違う）、b の下に詰め物 1 行、d は A だけの行、最後の行の下に詰め物 1 行。
	b := buffer.New("a\nbx\nc\nd\ne\n")
	r := &Renderer{Width: 10, Height: 10, Theme: diffTheme()}
	f := &Frame{
		Text: b,
		Diff: &Diff{
			Lines: map[int]DiffLine{
				1: {Style: StyleDiffChange, Text: []ByteRange{{1, 2}}},
				3: {Style: StyleDiffAdd},
			},
			Fill: map[int]int{2: 1, 5: 1},
		},
		Decorations: NewDecorations([]Highlight{{Range: buffer.Range{Start: buffer.Position{Line: 1}, End: buffer.Position{Line: 1, Byte: 1}}, Style: StyleKeyword}}, b.LineCount()),
	}
	s := r.Render(f)
	want := []string{"a", "bx", "----------", "c", "d", "e", "----------", "~"}
	if got := rowTexts(s, len(want)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rows = %q, want %q", got, want)
	}
	g := s.Grid
	if a := g.At(1, 0).Attr; a.Bg != Indexed(5) || a.Fg != Indexed(3) {
		t.Errorf("変えた行の構文の色の文字: %+v, want 背景 5 と文字 3", a)
	}
	if a := g.At(1, 1).Attr; a.Bg != Indexed(9) || !a.Bold {
		t.Errorf("違う文字: %+v, want diff.text", a)
	}
	if a := g.At(1, 9).Attr; a.Bg != Indexed(5) {
		t.Errorf("変えた行の右の端: %+v, want diff.change", a)
	}
	if a := g.At(4, 5).Attr; a.Bg != Indexed(4) {
		t.Errorf("足した行の右: %+v, want diff.add", a)
	}
	if a := g.At(2, 0).Attr; a.Bg != Indexed(6) {
		t.Errorf("詰め物の行: %+v, want diff.delete", a)
	}
	if a := g.At(0, 3).Attr; a != (Attr{}) {
		t.Errorf("同じ行: %+v, want 色なし", a)
	}

	// カーソルの画面の行は、詰め物の行を数える。
	f.Cursor = buffer.Position{Line: 4}
	if s := r.Render(f); s.Cursor.Row != 5 {
		t.Errorf("カーソルの行 = %d, want 5", s.Cursor.Row)
	}
}

// TestRenderDiffTopFill は、最初の行の上の詰め物と、両側をそろえるための SetDiffTop と、詰め物の行によるスクロールを確かめる。
//
// req: FR-100
func TestRenderDiffTopFill(t *testing.T) {
	b := buffer.New("c\nd\ne\nf\ng\n")
	r := &Renderer{Width: 6, Height: 5, Theme: diffTheme()} // 表示領域は 3 行
	f := &Frame{Text: b, Diff: &Diff{Fill: map[int]int{0: 2, 3: 2}}}

	// 0 行目では、その上の詰め物を全部見せる。
	s := r.Render(f)
	if got, want := rowTexts(s, 3), []string{"------", "------", "c"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("先頭 = %q, want %q", got, want)
	}
	if line, fill := r.DiffTop(); line != 0 || fill != 2 {
		t.Errorf("DiffTop = %d, %d, want 0, 2", line, fill)
	}

	// もう片側に合わせて、詰め物の途中から見せる。
	r.SetDiffTop(0, 1)
	s = r.Render(f)
	if got, want := rowTexts(s, 3), []string{"------", "c", "d"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("SetDiffTop(0, 1) = %q, want %q", got, want)
	}

	// カーソルを下へ動かすと、詰め物を含めてカーソルの行が表示領域に収まるようにスクロールする。
	f.Cursor = buffer.Position{Line: 3} // f。上に詰め物が 2 行ある
	s = r.Render(f)
	if got, want := rowTexts(s, 3), []string{"------", "------", "f"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("f へ移った = %q, want %q", got, want)
	}
	if s.Cursor.Row != 2 {
		t.Errorf("カーソルの行 = %d, want 2", s.Cursor.Row)
	}

	// ScrollPos で読んだ表示の位置を SetScrollPos で戻すと、詰め物の行の数も戻る（SetScroll は詰め物を 0 にする）。
	// 打つたびの検索（FR-153）が、打ち始めたときの位置へ戻すのに使う。
	f.Cursor = buffer.Position{}
	r.SetDiffTop(0, 2)
	r.Render(f)
	pos := r.ScrollPos()
	r.SetScroll(0, 0)
	r.SetScrollPos(pos)
	s = r.Render(f)
	if got, want := rowTexts(s, 3), []string{"------", "------", "c"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("SetScrollPos = %q, want %q", got, want)
	}

	// 見比べを終えたら（Diff が nil）、詰め物を描かない。
	f.Diff = nil
	s = r.Render(f)
	if got := rowTexts(s, 3); strings.Contains(strings.Join(got, ""), "-") {
		t.Errorf("見比べの後に詰め物が残った: %q", got)
	}
}
