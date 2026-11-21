package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// 起動の案内は、行の並びを表示領域の行の中ほどに置き、各行を表示領域の幅の中ほどに寄せて、"~" の行の上に描く。
// 文字の無いセル（左の "~" と空の行）は下に描いたもののまま残し、ステータス行とコマンドラインの行は覆わない。
// 上と下に 1 行、左と右に 1 列の余りが残らなければ描かない。
//
// req: FR-205
func TestRenderIntro(t *testing.T) {
	intro := &Intro{Lines: []string{"ab", "", "xyz", "あい"}}
	render := func(width, height int) Screen {
		r := &Renderer{Width: width, Height: height}
		return r.Render(&Frame{Text: buffer.New(""), Intro: intro})
	}

	s := render(12, 10) // 表示領域は 8 行で、上に (8-4)/2 = 2 行を空ける
	got := rowTexts(s, 10)
	want := []string{
		"",
		"~",
		"~    ab",
		"~",
		"~   xyz",
		"~   あい",
		"~",
		"~",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d 行目 = %q, want %q（画面 =\n%s）", i, got[i], want[i], strings.Join(got, "\n"))
		}
	}
	// ステータス行とコマンドラインの行は、案内を出さないときと同じである。
	plain := rowTexts((&Renderer{Width: 12, Height: 10}).Render(&Frame{Text: buffer.New("")}), 10)
	if got[8] != plain[8] || got[9] != plain[9] {
		t.Errorf("ステータス行とコマンドラインの行 = %q, want %q", got[8:], plain[8:])
	}
	if a := s.Grid.At(2, 5).Attr; a != (Attr{}) {
		t.Errorf("案内の文字の属性 = %+v、色を付けない", a)
	}

	// 余りの境目: 幅は最も広い行（4 列）に 2 列を足した 6 列から、行は 4 行に 2 行を足した 6 行（画面は 8 行）から描く。
	for _, c := range []struct {
		width, height int
		shown         bool
	}{
		{6, 8, true}, {5, 8, false}, {6, 7, false}, {80, 7, false}, {5, 40, false},
	} {
		text := strings.Join(rowTexts(render(c.width, c.height), c.height-2), "\n")
		if shown := strings.Contains(text, "ab"); shown != c.shown {
			t.Errorf("%d 列 %d 行の画面に描いたか = %v, want %v:\n%s", c.width, c.height, shown, c.shown, text)
		}
	}
	// 幅が奇数の余りなら、左を切り捨てる（Neovim 0.12.5 の do_intro_line と同じ）。
	if row := rowTexts(render(7, 8), 4)[1]; row != "~ ab" {
		t.Errorf("7 列の画面の 1 行目 = %q", row)
	}

	// 拡張機能の進み具合は、案内の上に重ねる（窓の重なりの表で起動の案内より上）。
	r := &Renderer{Width: 12, Height: 10}
	prog := []Progress{{Title: "p1", Percentage: -1}, {Title: "p2", Percentage: -1}, {Title: "p3", Percentage: -1}, {Title: "p4", Percentage: -1}}
	got = rowTexts(r.Render(&Frame{Text: buffer.New(""), Intro: intro, Progress: prog}), 8)
	// 表示領域の下の半分（4 行）に進み具合を出し、案内の xyz と あい の行を覆う。
	if got[4] != "~    p1 ..." || got[5] != "~    p2 ..." || got[2] != "~    ab" {
		t.Errorf("進み具合と重なる画面 =\n%s", strings.Join(got, "\n"))
	}
}

// :intro の案内は、表示領域を空白で塗ってから描き、収まらない行と列は切る。
// 案内の上には、スクロールバーなど、窓の重なりの表で上にある部品が重なる。
//
// req: FR-207
func TestRenderIntroClear(t *testing.T) {
	text := buffer.New(strings.Repeat("hello\n", 100))
	intro := &Intro{Lines: []string{"ab", "", "xyz"}, Clear: true}
	r := &Renderer{Width: 10, Height: 7}
	s := r.Render(&Frame{Text: text, Intro: intro, Scrollbar: &Scrollbar{}})
	got := rowTexts(s, 5)
	want := []string{"", "    ab", "", "   xyz", ""}
	for i := range 5 {
		// 右端の列はスクロールバーのつまみが重なる（窓の重なりの表で起動の案内より上）。
		if got[i] != want[i] {
			t.Errorf("%d 行目 = %q, want %q", i, got[i], want[i])
		}
	}
	if a := s.Grid.At(0, 9).Attr; a != DefaultTheme()[StyleScrollbar] {
		t.Errorf("右端のつまみの属性 = %+v、案内の上に重ねる", a)
	}

	// 表示領域に収まらなくても、収まる行と列だけを描く。
	small := &Renderer{Width: 2, Height: 4}
	got = rowTexts(small.Render(&Frame{Text: text, Intro: intro}), 2)
	if got[0] != "ab" || got[1] != "" {
		t.Errorf("2 列 2 行の表示領域 = %q", got)
	}
	tiny := &Renderer{Width: 2, Height: 3}
	if row := rowTexts(tiny.Render(&Frame{Text: text, Intro: &Intro{Lines: []string{"xyz"}, Clear: true}}), 1)[0]; row != "xy" {
		t.Errorf("1 行の表示領域 = %q", row)
	}
}
