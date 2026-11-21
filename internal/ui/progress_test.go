package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// 進み具合を、バッファの表示領域の右下に、最も新しいものを最後の行にして重ねて描く。1 行は「本文 (百分率%) 表題 印」を右に寄せ、
// 右の端に 1 列を空け、左に 1 列の空白を置く。続いているものと終わったもので、表題と印の色と印を変える。
// 表示領域の半分の行までしか出さず、幅に収まらなければ本文から削る。ポップアップは進み具合の上に描く。
//
// req: FR-178
func TestRenderProgress(t *testing.T) {
	text := buffer.New(strings.Repeat(strings.Repeat("x", 30)+"\n", 8))
	theme := Theme{StyleProgress: {Fg: Indexed(2)}, StyleProgressTitle: {Fg: Indexed(3)}, StyleProgressDone: {Fg: Indexed(4)}}
	render := func(width, height int, f *Frame) Screen {
		r := &Renderer{Width: width, Height: height, Theme: theme}
		f.Text = text
		return r.Render(f)
	}

	items := []Progress{
		{Title: "old", Message: "a", Percentage: -1},
		{Title: "gopls", Message: "3/10", Percentage: 30},
		{Title: "ts", Percentage: -1, Done: true},
	}
	s := render(30, 8, &Frame{Progress: items}) // 表示領域は 6 行で、半分の 3 行まで出す
	got := rowTexts(s, 6)
	want := []string{
		strings.Repeat("x", 30),
		strings.Repeat("x", 30),
		strings.Repeat("x", 30),
		strings.Repeat("x", 19) + " a old ...x",
		strings.Repeat("x", 8) + " 3/10 (30%) gopls ...x",
		strings.Repeat("x", 21) + " ts donex",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("画面 =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if a := s.Grid.At(4, 10).Attr; a != theme[StyleProgress] {
		t.Errorf("本文の色 = %+v", a)
	}
	if a := s.Grid.At(4, 22).Attr; a != theme[StyleProgressTitle] {
		t.Errorf("続いているものの表題の色 = %+v", a)
	}
	if a := s.Grid.At(5, 25).Attr; a != theme[StyleProgressDone] {
		t.Errorf("終わったものの表題の色 = %+v", a)
	}

	// 表示領域が 4 行なら 2 行までで、古いものを出さない。
	got = rowTexts(render(30, 6, &Frame{Progress: items}), 4)
	if !strings.HasSuffix(got[2], " gopls ...x") || !strings.HasSuffix(got[3], " ts donex") || strings.Contains(strings.Join(got, ""), "old") {
		t.Errorf("4 行の表示領域 = %q", got)
	}
	// 表示領域が 1 行なら出さない。
	if row := rowTexts(render(30, 3, &Frame{Progress: items}), 1)[0]; row != strings.Repeat("x", 30) {
		t.Errorf("1 行の表示領域 = %q", row)
	}

	// 狭い画面では本文から削り、それでも収まらなければ表題と印を右で切る。
	long := []Progress{{Title: "gopls", Message: "Loading packages", Percentage: 50}}
	if row := rowTexts(render(20, 8, &Frame{Progress: long}), 6)[5]; row != "x Loading gopls ...x" {
		t.Errorf("狭い画面 = %q", row)
	}
	if row := rowTexts(render(8, 8, &Frame{Progress: long}), 6)[5]; row != "gopls .x" {
		t.Errorf("とても狭い画面 = %q", row)
	}

	// 記号は設定の symbols で変えられ、空なら付けない。
	r := &Renderer{Width: 30, Height: 8, Theme: theme, Symbols: Symbols{SymbolProgressRunning: "", SymbolProgressDone: "✔"}}
	got = rowTexts(r.Render(&Frame{Text: text, Progress: items[1:]}), 6)
	if !strings.HasSuffix(got[4], " 3/10 (30%) goplsx") || !strings.HasSuffix(got[5], " ts ✔x") {
		t.Errorf("記号を変えた画面 = %q", got[4:])
	}

	// ポップアップは進み具合の上に描く。
	s = render(30, 8, &Frame{Progress: items, Float: &Float{View: &Frame{Text: buffer.New("popup\n")}, Center: true}})
	if !strings.Contains(strings.Join(rowTexts(s, 6), "\n"), "popup") {
		t.Errorf("ポップアップが無い:\n%s", strings.Join(rowTexts(s, 6), "\n"))
	}
}
