package term

import (
	"bytes"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// lines は、ui.Text を満たす行の列である。
type lines []string

func (l lines) LineCount() int { return max(len(l), 1) }
func (l lines) Line(i int) string {
	if i < len(l) {
		return l[i]
	}
	return ""
}

// paintTheme は、16 色、256 色、24 ビット色、下線の色と飾りのすべてを使うテーマである。
func paintTheme() ui.Theme {
	return ui.Theme{
		ui.StyleKeyword:           {Fg: ui.Indexed(3), Bold: true},
		ui.StyleString:            {Fg: ui.Indexed(13), Italic: true},
		ui.StyleComment:           {Fg: ui.Indexed(244)},
		ui.StyleFunction:          {Fg: ui.RGB(0x12, 0x34, 0x56), Bg: ui.RGB(1, 2, 3)},
		ui.StyleType:              {Bg: ui.Indexed(4)},
		ui.StyleNumber:            {Bg: ui.Indexed(12)},
		ui.StyleConstant:          {Bg: ui.Indexed(200)},
		ui.StyleVisual:            {Reverse: true},
		ui.StyleStatusLine:        {Reverse: true, Bold: true},
		ui.StyleNonText:           {Fg: ui.Indexed(12)},
		ui.StyleSpecialKey:        {Fg: ui.Indexed(12)},
		ui.StyleMessageError:      {Fg: ui.Indexed(15), Bg: ui.Indexed(1)},
		ui.StyleDiagnosticError:   {Undercurl: true, Sp: ui.Indexed(9)},
		ui.StyleDiagnosticWarning: {Underline: true, Sp: ui.RGB(9, 8, 7)},
	}
}

var pieces = []string{"a", "b", "あ", "い", "\t", "é", " ", "🍣", "\x01", "xyz", "日本語"}

var styles = []string{
	ui.StyleKeyword, ui.StyleString, ui.StyleComment, ui.StyleFunction, ui.StyleType, ui.StyleNumber, ui.StyleConstant,
}

// randomFrame は、全角文字、タブ、結合文字、制御文字、色、選択、診断を混ぜた画面の入力を作る。
func randomFrame(rnd *rand.Rand) *ui.Frame {
	n := 1 + rnd.IntN(12)
	ls := make(lines, n)
	// bounds は、各行の文字の境界のバイト位置である。
	bounds := make([][]int, n)
	for i := range ls {
		var b strings.Builder
		bounds[i] = []int{0}
		for range rnd.IntN(16) {
			b.WriteString(pieces[rnd.IntN(len(pieces))])
			bounds[i] = append(bounds[i], b.Len())
		}
		ls[i] = b.String()
	}
	pos := func() buffer.Position {
		l := rnd.IntN(n)
		return buffer.Position{Line: l, Byte: bounds[l][rnd.IntN(len(bounds[l]))]}
	}
	var hs []ui.Highlight
	for range rnd.IntN(6) {
		hs = append(hs, ui.Highlight{Range: buffer.Range{Start: pos(), End: pos()}, Style: styles[rnd.IntN(len(styles))]})
	}
	f := &ui.Frame{
		Text:        ls,
		Name:        []string{"", "a.go", "日本語.txt"}[rnd.IntN(3)],
		Modified:    rnd.IntN(2) == 0,
		Mode:        ui.Mode(rnd.IntN(5)),
		Cursor:      pos(),
		Decorations: ui.NewDecorations(hs, n),
	}
	if f.Mode == ui.ModeVisual || f.Mode == ui.ModeVisualLine {
		f.Selection = ui.Selection{Active: true, Start: pos(), End: f.Cursor, Linewise: f.Mode == ui.ModeVisualLine}
	}
	if f.Mode == ui.ModeCommandLine {
		f.CommandLine = "s/あ/b/"
	}
	switch rnd.IntN(3) {
	case 0:
		f.Message = ui.Message{Text: "E37: 保存していない", Level: ui.MessageError}
	case 1:
		f.OperatorPending = f.Mode == ui.ModeNormal
	}
	if rnd.IntN(2) == 0 {
		f.Diagnostics = []ui.Diagnostic{{Range: buffer.Range{Start: pos(), End: pos()}, Severity: ui.SeverityError}}
	}
	return f
}

// TestPaintMatchesGrid は、続けて描いた画面の差分を端末の模型へ流し、模型の画面が毎回の格子と一致することを確かめる。
// 全角文字の片側だけを上書きしたときの崩れ、属性の切り替えの漏れ、大きさの変化による全体の書き直しを、乱数の画面で揺さぶる。
func TestPaintMatchesGrid(t *testing.T) {
	for seed := range uint64(40) {
		rnd := rand.New(rand.NewPCG(seed, 104)) //nolint:gosec // 再現できる無作為な画面の列を作るためで、暗号の用途ではない
		w, h := 20, 8
		r := &ui.Renderer{Width: w, Height: h, Theme: paintTheme()}
		v := newVT(w, h)
		var p Painter
		var buf bytes.Buffer
		for step := range 60 {
			if rnd.IntN(15) == 0 {
				w, h = 5+rnd.IntN(30), 3+rnd.IntN(10)
				r.Width, r.Height = w, h
				v = newVT(w, h)
			}
			s := r.Render(randomFrame(rnd))
			buf.Reset()
			p.Paint(&buf, s)
			v.write(t, buf.String())
			if t.Failed() {
				return
			}
			func() {
				defer func() {
					if t.Failed() {
						t.Logf("seed %d, step %d", seed, step)
					}
				}()
				v.check(t, s)
			}()
		}
	}
}

// TestPaintNoChange は、同じ画面をもう一度描くと何も書かないことを確かめる。
func TestPaintNoChange(t *testing.T) {
	r := &ui.Renderer{Width: 20, Height: 5}
	f := &ui.Frame{Text: lines{"hello", "あいう"}}
	var p Painter
	var buf bytes.Buffer
	p.Paint(&buf, r.Render(f))
	buf.Reset()
	p.Paint(&buf, r.Render(f))
	if buf.Len() != 0 {
		t.Errorf("同じ画面で %q を書いた", buf.String())
	}
}

// TestPaintMinimal は、1 文字の変更で、変わったセル（足した文字と、ステータス行の列の番号）と、
// カーソルを置き直す分だけを書くことを確かめる。
func TestPaintMinimal(t *testing.T) {
	r := &ui.Renderer{Width: 20, Height: 5}
	var p Painter
	var buf bytes.Buffer
	p.Paint(&buf, r.Render(&ui.Frame{Text: lines{"hello"}, Mode: ui.ModeInsert, Cursor: buffer.Position{Byte: 5}}))
	buf.Reset()
	// ステータス行の [+] の表示を変えないように Modified を保ったまま、1 文字足す。
	p.Paint(&buf, r.Render(&ui.Frame{Text: lines{"hello!"}, Mode: ui.ModeInsert, Cursor: buffer.Position{Byte: 6}}))
	want := "\x1b[?25l\x1b[1;6H\x1b[0m!\x1b[4;14H\x1b[0;1;7m7\x1b[1;7H\x1b[?25h"
	if got := buf.String(); got != want {
		t.Errorf("Paint = %q, want %q", got, want)
	}
}

// TestPaintAttrSwitches は、同じ属性のセルが続く間は SGR を出し直さないことを確かめる。
func TestPaintAttrSwitches(t *testing.T) {
	r := &ui.Renderer{Width: 30, Height: 3, Theme: paintTheme()}
	hs := []ui.Highlight{
		{Range: buffer.Range{Start: buffer.Position{Byte: 0}, End: buffer.Position{Byte: 6}}, Style: ui.StyleKeyword},
		{Range: buffer.Range{Start: buffer.Position{Byte: 7}, End: buffer.Position{Byte: 13}}, Style: ui.StyleFunction},
	}
	f := &ui.Frame{Text: lines{"return result"}, Decorations: ui.NewDecorations(hs, 1)}
	var p Painter
	var buf bytes.Buffer
	p.Paint(&buf, r.Render(f))
	out := buf.String()
	// 1 行目: keyword と function の 2 回（間の空白は消した画面と同じなので書かない）。ステータス行: statusline の 1 回。
	// コマンドラインの行は空白なので書かない。最初の画面の消去の \x1b[0m を足して 4 回。
	if n := len(regexp.MustCompile(`\x1b\[[0-9;:]*m`).FindAllString(out, -1)); n != 4 {
		t.Errorf("SGR が %d 回（want 4）: %q", n, out)
	}
	if !strings.Contains(out, "\x1b[0;1;33mreturn\x1b[1;8H\x1b[0;38;2;18;52;86;48;2;1;2;3mresult") {
		t.Errorf("1 行目の書き方が違う: %q", out)
	}
}

// TestPaintWideOverwrite は、全角文字の片側だけが変わったときに、端末に残りの半分が崩れて残らないことを確かめる。
func TestPaintWideOverwrite(t *testing.T) {
	r := &ui.Renderer{Width: 10, Height: 3}
	v := newVT(10, 3)
	var p Painter
	var buf bytes.Buffer
	for _, text := range []string{"あいう", "aいう", "あいう", "xあいう", "あいう", "a\tb", "あいう"} {
		s := r.Render(&ui.Frame{Text: lines{text}})
		buf.Reset()
		p.Paint(&buf, s)
		v.write(t, buf.String())
		v.check(t, s)
	}
}

// TestPaintInvalidate は、Invalidate の後は画面の全体を書き直すことを確かめる。
func TestPaintInvalidate(t *testing.T) {
	r := &ui.Renderer{Width: 10, Height: 3}
	f := &ui.Frame{Text: lines{"abc"}}
	var p Painter
	var buf bytes.Buffer
	p.Paint(&buf, r.Render(f))
	p.Invalidate()
	buf.Reset()
	p.Paint(&buf, r.Render(f))
	v := newVT(10, 3)
	v.write(t, "XXXXXXXXXX") // 端末に別のものが書かれた
	v.write(t, buf.String())
	v.check(t, r.Render(f))
}
