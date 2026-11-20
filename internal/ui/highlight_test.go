package ui

import (
	"regexp"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// highlightTheme は、範囲の色の試験に使う配色である。範囲、一致、選択の色を見分けられるようにした。
var highlightTheme = Theme{
	StyleYank:   {Bg: Indexed(2)},
	StyleSearch: {Bg: Indexed(3)},
	StyleVisual: {Bg: Indexed(5)},
}

// 範囲の色（Frame.Highlights）は、行ごとに範囲に掛かる文字にだけ付け、行末の 1 セルには付けない。
// 空の行には付かない（nvim 0.12.5 の vim.hl.on_yank で、yj と複数の行の vjjy の画面を 2026-11-20 に tmux で確かめた）。
// 色の無い名前の範囲は付けない。
//
// req: FR-140
func TestRenderHighlights(t *testing.T) {
	hl := highlightTheme[StyleYank]
	rg := func(l1, b1, l2, b2 int) buffer.Range { return buffer.Range{Start: pos(l1, b1), End: pos(l2, b2)} }
	tests := []struct {
		name string
		hls  []Highlight
		want []string // 行ごとの marks
	}{
		{"1 行の中", []Highlight{{Range: rg(0, 1, 0, 3), Style: StyleYank}}, []string{" ^^", "", ""}},
		{"行単位（行の先頭から最後の行の行末）", []Highlight{{Range: rg(0, 0, 1, 0), Style: StyleYank}}, []string{"^^^^^^^", "", ""}},
		{"行をまたぐ範囲と空の行", []Highlight{{Range: rg(0, 2, 2, 3), Style: StyleYank}}, []string{"  ^^^^^", "", "^^^"}},
		{"テキストの外の位置は切る", []Highlight{{Range: rg(2, 1, 9, 9), Style: StyleYank}}, []string{"", "", " ^^^^^^"}},
		{"逆向きの範囲", []Highlight{{Range: rg(0, 3, 0, 1), Style: StyleYank}}, []string{" ^^", "", ""}},
		{"色の無い名前", []Highlight{{Range: rg(0, 0, 0, 3), Style: "nosuch"}}, []string{"", "", ""}},
		{"範囲が無い", nil, []string{"", "", ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Renderer{Width: 20, Height: 8, Theme: highlightTheme}
			// カーソルは最後の行の後ろの方に置き、カーソルの文字にも範囲の色が付くことも確かめる（テキストの外の位置の行）。
			s := r.Render(&Frame{Text: buffer.New("abc def\n\nghi jkl\n"), Cursor: pos(2, 4), Highlights: tt.hls})
			for i, want := range tt.want {
				if got := marks(s, i, hl); got != want {
					t.Errorf("%d 行目 = %q, want %q（%q）", i, got, want, s.Grid.RowText(i))
				}
			}
		})
	}
}

// 範囲の色は、検索の一致の色の上に重ね、選択の色（visual）の下に置く（#283 の本文）。
// 一覧の後ろの範囲を、前の範囲の上に重ねる。
//
// req: FR-140
func TestRenderHighlightLayers(t *testing.T) {
	yank, search, vis := highlightTheme[StyleYank], highlightTheme[StyleSearch], highlightTheme[StyleVisual]
	b := buffer.New("foo bar foo\n")
	hls := []Highlight{{Range: buffer.Range{Start: pos(0, 2), End: pos(0, 9)}, Style: StyleYank}}

	// 一致（0:0〜0:3 と 0:8〜0:11）と重なったところは範囲の色にする。選択（0:6〜0:8）と重なったところは選択の色にする。
	r := &Renderer{Width: 20, Height: 8, Theme: highlightTheme}
	s := r.Render(&Frame{
		Text: b, Cursor: pos(0, 10), Search: regexp.MustCompile("foo"), Highlights: hls,
		Selection: Selection{Active: true, Start: pos(0, 6), End: pos(0, 7)},
	})
	if got, want := marks(s, 0, yank), "  ^^^^  ^"; got != want {
		t.Errorf("範囲の色 = %q, want %q", got, want)
	}
	if got, want := marks(s, 0, search), "^^       ^^"; got != want {
		t.Errorf("一致の色 = %q, want %q", got, want)
	}
	if got, want := marks(s, 0, vis), "      ^^"; got != want {
		t.Errorf("選択の色 = %q, want %q", got, want)
	}

	// 後ろの範囲を上に重ねる。
	other := Attr{Bg: Indexed(6)}
	theme := highlightTheme.WithColors(map[string]Attr{StyleSearch: other})
	hls2 := []Highlight{
		{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 4)}, Style: StyleYank},
		{Range: buffer.Range{Start: pos(0, 2), End: pos(0, 6)}, Style: StyleSearch},
	}
	r = &Renderer{Width: 20, Height: 8, Theme: theme}
	s = r.Render(&Frame{Text: b, Cursor: pos(0, 10), Highlights: hls2})
	if got, want := marks(s, 0, yank), "^^"; got != want {
		t.Errorf("前の範囲 = %q, want %q", got, want)
	}
	if got, want := marks(s, 0, other), "  ^^^^"; got != want {
		t.Errorf("後ろの範囲 = %q, want %q", got, want)
	}
}

// 浮かべた窓の中には、範囲の色を付けない（行番号と検索の一致の色と同じ）。
//
// req: FR-140
func TestFloatIgnoresHighlights(t *testing.T) {
	yank := highlightTheme[StyleYank]
	view := &Frame{
		Text:       buffer.New("abc\n"),
		Highlights: []Highlight{{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 3)}, Style: StyleYank}},
	}
	r := &Renderer{Width: 20, Height: 8, Theme: highlightTheme}
	s := r.Render(&Frame{Text: buffer.New("x\n"), Float: &Float{View: view, Center: true}})
	for row := range 8 {
		if got := marks(s, row, yank); got != "" {
			t.Errorf("%d 行目に範囲の色 = %q（%q）", row, got, s.Grid.RowText(row))
		}
	}
}
