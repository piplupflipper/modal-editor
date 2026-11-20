package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// searchTheme は、一致の色の試験に使う配色である。一致、選択、行番号、診断の色を見分けられるようにした。
var searchTheme = Theme{
	StyleSearch:          {Bg: Indexed(3)},
	StyleVisual:          {Bg: Indexed(5)},
	StyleLineNr:          {Fg: Indexed(1)},
	StyleDiagnosticError: {Underline: true, Sp: Indexed(9)},
}

// marks は、row 行目の左から 20 列の、属性が a のセルを "^"、それ以外を " " にした文字列を、右の空白を除いて返す。
func marks(s Screen, row int, a Attr) string {
	var b strings.Builder
	for c := range 20 {
		if s.Grid.At(row, c).Attr == a {
			b.WriteByte('^')
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// 直前の検索のパターン（Frame.Search）の一致に、色の名前 search の色を付ける。
// 1 行に複数の一致があればどれにも付け、多バイト文字は文字の全体（全角文字は 2 列）に付ける。
// 空の一致はその位置の 1 文字に、行末の位置なら行末の 1 列に付ける（Vim 9.1 で /^、/$、/x* の画面を 2026-11-20 に tmux で確かめた）。
// パターンは行ごとに当てるので、行をまたぐ一致は無い。
//
// req: FR-137
func TestRenderSearchMatches(t *testing.T) {
	hl := searchTheme[StyleSearch]
	tests := []struct {
		name, text, pattern string
		want                []string // 行ごとの marks
	}{
		{"1 行に複数の一致", "foo bar foo\nxfoo", "foo", []string{"^^^     ^^^", " ^^^"}},
		{"多バイト文字", "日本語の日本\nabc", "日本", []string{"^^^^    ^^^^", ""}},
		{"一致の無い行", "abc\nfoo", "foo", []string{"", "^^^"}},
		{"行をまたぐパターン", "ab\ncd", `b\nc`, []string{"", ""}},
		{"空の一致（^）", "abc\n\nfoo", "^", []string{"^", "^", "^"}},
		{"空の一致（$）は行末の 1 列", "abc\nfoo", "$", []string{"   ^", "   ^"}},
		{"空の一致（x*）", "abc\naxxb", "x*", []string{"^^^^", "^^^ ^"}},
		{"多バイト文字の前の空の一致", "日本\nx", "^", []string{"^^", "^"}},
		{"空の一致と空でない一致", "abc", `b|$`, []string{" ^ ^"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Renderer{Width: 20, Height: 8, Theme: searchTheme}
			// カーソルは試す行の外（最後の行の後の行は無いので、最後の行の先頭）に置き、選択も無いので、カーソルの文字にも一致の色が付く。
			s := r.Render(&Frame{Text: buffer.New(tt.text + "\n"), Search: regexp.MustCompile(tt.pattern)})
			for i, want := range tt.want {
				if got := marks(s, i, hl); got != want {
					t.Errorf("%d 行目 = %q, want %q（%q）", i, got, want, s.Grid.RowText(i))
				}
			}
		})
	}

	// Search が nil なら色を付けない。
	r := &Renderer{Width: 20, Height: 8, Theme: searchTheme}
	s := r.Render(&Frame{Text: buffer.New("foo\n")})
	if got := marks(s, 0, hl); got != "" {
		t.Errorf("Search が nil = %q", got)
	}
}

// 一致の色は、構文の色と診断の上に重ね、選択の色（visual）の下に置く（#275 の本文）。行番号の列には付けない。
//
// req: FR-137
func TestRenderSearchLayers(t *testing.T) {
	hl, vis := searchTheme[StyleSearch], searchTheme[StyleVisual]
	b := buffer.New("foo bar\nx1 foo\n")
	re := regexp.MustCompile("foo|1")

	// 選択（0:1 から 0:4）と重なったところは選択の色にする。カーソル（0:6）の文字は選択の外。
	r := &Renderer{Width: 20, Height: 8, Theme: searchTheme}
	s := r.Render(&Frame{Text: b, Cursor: pos(0, 6), Search: re, Selection: Selection{Active: true, Start: pos(0, 1), End: pos(0, 4)}})
	if got := marks(s, 0, hl); got != "^" {
		t.Errorf("選択と重なる行の一致の色 = %q, want %q", got, "^")
	}
	if got := marks(s, 0, vis); got != " ^^^^" {
		t.Errorf("選択と重なる行の選択の色 = %q, want %q", got, " ^^^^")
	}

	// 診断の上に重ねる。診断の下線は残り、背景は一致の色になる。診断があるので、左の 2 列は印の列である。
	diag := []Diagnostic{{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 2)}, Severity: SeverityError}}
	r = &Renderer{Width: 20, Height: 8, Theme: searchTheme}
	s = r.Render(&Frame{Text: b, Cursor: pos(1, 0), Search: re, Diagnostics: diag})
	if got := marks(s, 0, searchTheme[StyleDiagnosticError].Over(hl)); got != "  ^^" {
		t.Errorf("診断と重なる一致 = %q, want %q", got, "  ^^")
	}
	if got := marks(s, 0, hl); got != "    ^" {
		t.Errorf("診断と重ならない一致 = %q, want %q", got, "    ^")
	}

	// 行番号の列（"  1 " と "  2 "）の数字 1 には付けず、本文の 1 には付ける。
	r = &Renderer{Width: 20, Height: 8, Theme: searchTheme}
	s = r.Render(&Frame{Text: b, Cursor: pos(0, 4), Search: re, LineNumbers: LineNumbers{Show: true, MinWidth: 4}})
	if got := rowTexts(s, 2); got[0] != "  1 foo bar" || got[1] != "  2 x1 foo" {
		t.Fatalf("rows = %q", got)
	}
	if got := marks(s, 0, hl); got != "    ^^^" {
		t.Errorf("行番号のある 0 行目 = %q, want %q", got, "    ^^^")
	}
	if got := marks(s, 1, hl); got != "     ^ ^^^" {
		t.Errorf("行番号のある 1 行目 = %q, want %q", got, "     ^ ^^^")
	}
}

// 浮かべた窓の中には、窓の中のバッファの Frame に Search が入っていても一致の色を付けない（行番号の列と同じ）。
//
// req: FR-137
func TestRenderSearchNotInFloat(t *testing.T) {
	hl := searchTheme[StyleSearch]
	re := regexp.MustCompile("foo")
	r := &Renderer{Width: 20, Height: 8, Theme: searchTheme}
	view := &Frame{Text: buffer.New("foo\nfoo\n"), Search: re}
	s := r.Render(&Frame{Text: buffer.New("abc\ndef\nghi\n"), Search: re, Float: &Float{View: view}})
	if got := rowTexts(s, 3); !strings.Contains(got[1]+got[2], "foo") {
		t.Fatalf("窓が出ていない: %q", got)
	}
	for row := range 6 {
		if got := marks(s, row, hl); got != "" {
			t.Errorf("%d 行目に一致の色 = %q（%q）", row, got, s.Grid.RowText(row))
		}
	}
}

// readCounter は、読んだ行の数を数えるテキストである。
type readCounter struct {
	Text
	reads map[int]int
}

func (c *readCounter) Line(i int) string {
	c.reads[i]++
	return c.Text.Line(i)
}

// 一致を探すのは画面に見えている行だけで、色を付けても読む行は増えない（バッファの全体を走査しない）。
//
// req: FR-137
func TestRenderSearchVisibleLinesOnly(t *testing.T) {
	var sb strings.Builder
	for range 10000 {
		sb.WriteString("foo bar\n")
	}
	b := buffer.New(sb.String())
	for _, re := range []*regexp.Regexp{nil, regexp.MustCompile("foo")} {
		text := &readCounter{Text: b, reads: map[int]int{}}
		r := &Renderer{Width: 20, Height: 8, Theme: searchTheme}
		r.Render(&Frame{Text: text, Cursor: pos(5000, 0), Search: re})
		for line := range text.reads {
			if line < r.Top() || line >= r.Top()+r.TextRows() {
				t.Errorf("Search=%v: 見えていない %d 行目を読んだ（表示は %d 行目から %d 行）", re, line, r.Top(), r.TextRows())
			}
		}
	}
}
