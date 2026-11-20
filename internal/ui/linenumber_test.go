package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// countText は、行数だけを持つテキストである。どの行も "x" である。
type countText int

func (n countText) LineCount() int { return int(n) }
func (countText) Line(int) string  { return "x" }

// 行番号の列の幅は、最小の列数と、バッファの最後の行の番号の桁数に 1 を足した数の大きい方である。
// 期待値は、2026-11-20 に Vim 9.1（vim -Nu NONE -n --not-a-term）で、:set number と 'numberwidth' を変えて
// getwininfo() の textoff を読んだ値である。幅は画面に見えている行でなく最後の行で決まるので、先頭を見ていても変わらない。
//
// req: FR-136
func TestLineNumberWidth(t *testing.T) {
	tests := []struct {
		minWidth int
		lines    []int
		want     []int
	}{
		{4, []int{1, 9, 10, 99, 100, 999, 1000, 9999, 10000, 100000}, []int{4, 4, 4, 4, 4, 4, 5, 5, 6, 7}},
		{5, []int{1, 9, 10, 99, 100, 999, 1000, 9999, 10000, 100000}, []int{5, 5, 5, 5, 5, 5, 5, 5, 6, 7}},
		{1, []int{1, 9, 10, 99, 100, 999, 1000, 9999, 10000, 100000}, []int{2, 2, 3, 3, 4, 4, 5, 5, 6, 7}},
	}
	for _, tt := range tests {
		for i, n := range tt.lines {
			r := &Renderer{Width: 40, Height: 6}
			s := r.Render(&Frame{Text: countText(n), LineNumbers: LineNumbers{Show: true, MinWidth: tt.minWidth}})
			if got := s.Cursor.Col; got != tt.want[i] {
				t.Errorf("minWidth=%d lines=%d: テキストの左の端 = %d, want %d", tt.minWidth, n, got, tt.want[i])
			}
		}
	}
	// 出さなければ幅は 0 である。
	r := &Renderer{Width: 40, Height: 6}
	if s := r.Render(&Frame{Text: countText(1000), LineNumbers: LineNumbers{MinWidth: 4}}); s.Cursor.Col != 0 {
		t.Errorf("出さないときのテキストの左の端 = %d, want 0", s.Cursor.Col)
	}
}

// 行番号の列は印の列の右、テキストの左に置き、数字は右に寄せて最後の 1 列を空ける。列の全体に linenr の色を付ける。
// 最終行より後の "~" の行には番号を出さない（Vim と同じく "~" は左の端に出す）。カーソルは列の分だけ右にずれる。
//
// req: FR-135
func TestRenderLineNumbers(t *testing.T) {
	theme := testTheme()
	theme[StyleLineNr] = Attr{Fg: Indexed(11)}
	b := buffer.New("a\nb\nc\n")
	ln := LineNumbers{Show: true, MinWidth: 4}

	r := &Renderer{Width: 12, Height: 6, Theme: theme}
	s := r.Render(&Frame{Text: b, Cursor: pos(1, 0), LineNumbers: ln})
	if got, want := rowTexts(s, 4), []string{"  1 a", "  2 b", "  3 c", "~"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("印の列が無いとき: rows = %q, want %q", got, want)
	}
	if s.Cursor.Row != 1 || s.Cursor.Col != 4 {
		t.Errorf("印の列が無いときのカーソル = %d,%d, want 1,4", s.Cursor.Row, s.Cursor.Col)
	}
	for c := range 4 {
		if a := s.Grid.At(0, c).Attr; a != theme[StyleLineNr] {
			t.Errorf("行番号の列の %d 列目の色 = %+v, want linenr", c, a)
		}
	}
	if a := s.Grid.At(0, 4).Attr; a != (Attr{}) {
		t.Errorf("テキストの色 = %+v, want 色なし", a)
	}
	if a := s.Grid.At(3, 0).Attr; a != theme[StyleNonText] {
		t.Errorf("~ の行の色 = %+v, want nontext", a)
	}

	// 印の列（診断の印）があれば、その右に置く。
	diag := []Diagnostic{{Range: buffer.Range{Start: pos(1, 0), End: pos(1, 1)}, Severity: SeverityError}}
	s = r.Render(&Frame{Text: b, Cursor: pos(1, 0), LineNumbers: ln, Diagnostics: diag})
	if got, want := rowTexts(s, 4), []string{"    1 a", "E   2 b", "    3 c", "~"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("印の列があるとき: rows = %q, want %q", got, want)
	}
	if s.Cursor.Row != 1 || s.Cursor.Col != 6 {
		t.Errorf("印の列があるときのカーソル = %d,%d, want 1,6", s.Cursor.Row, s.Cursor.Col)
	}

	// 描くたびに Frame の値を読むので、次の描画で出さなければ列は消える（:set number! の切り替え）。
	s = r.Render(&Frame{Text: b, Cursor: pos(1, 0)})
	if got, want := rowTexts(s, 3), []string{"a", "b", "c"}; strings.Join(got, "|") != strings.Join(want, "|") || s.Cursor.Col != 0 {
		t.Errorf("出さないとき: rows = %q, カーソルの列 = %d", got, s.Cursor.Col)
	}
	s = r.Render(&Frame{Text: b, Cursor: pos(1, 0), LineNumbers: LineNumbers{Show: true, MinWidth: 6}})
	if got := rowTexts(s, 1)[0]; got != "    1 a" || s.Cursor.Col != 6 {
		t.Errorf("最小の列数を変えたとき: row = %q, カーソルの列 = %d", got, s.Cursor.Col)
	}
}

// 横にスクロールするときは、行番号の列を除いた幅で数え、行番号の列は動かさない。
//
// req: FR-135
func TestRenderLineNumbersScroll(t *testing.T) {
	b := buffer.New("abcdefghijklmnopqrstuvwxyz\n")
	r := &Renderer{Width: 10, Height: 4}
	ln := LineNumbers{Show: true, MinWidth: 4}
	// テキストの幅は 6 列。7 列目（g）へ動くと、Vim の 'sidescroll' が 0 と同じく、カーソルを幅の半分の位置に置く。
	r.Render(&Frame{Text: b, LineNumbers: ln})
	s := r.Render(&Frame{Text: b, Cursor: pos(0, 6), LineNumbers: ln})
	if r.Left() != 3 {
		t.Errorf("左の端 = %d, want 3", r.Left())
	}
	if got := rowTexts(s, 1)[0]; got != "  1 defghi" {
		t.Errorf("row = %q, want %q", got, "  1 defghi")
	}
	if s.Cursor.Col != 4+6-3 {
		t.Errorf("カーソルの列 = %d, want %d", s.Cursor.Col, 4+6-3)
	}
	// 最後の文字（26 列目の z）へ動いても、行番号の列に重ねずに描く。
	s = r.Render(&Frame{Text: b, Cursor: pos(0, 25), LineNumbers: ln})
	if got := rowTexts(s, 1)[0]; got != "  1 wxyz" || s.Cursor.Col != 4+25-22 {
		t.Errorf("行末: row = %q, カーソルの列 = %d", got, s.Cursor.Col)
	}
}

// 行番号は、バッファの表示領域だけに出す。浮かべた窓の中、出力の欄、見比べている間の詰め物の行には出さない。
// カーソルの近くに浮かべる窓は、行番号の列の分だけ右にずれたカーソルの列に置く。
//
// req: FR-135
func TestRenderLineNumbersOnlyInBuffer(t *testing.T) {
	b := buffer.New("abc\ndef\nghi\n")
	ln := LineNumbers{Show: true, MinWidth: 4}

	// 浮かべた窓。窓の中のバッファの Frame に LineNumbers が入っていても読まない。
	// 窓（左右に 1 列の余白）は、行番号の列の分だけ右にずれたカーソルの列（4 + 1）から置く。
	r := &Renderer{Width: 20, Height: 8}
	view := &Frame{Text: buffer.New("pq\nrs\n"), LineNumbers: ln}
	s := r.Render(&Frame{Text: b, Cursor: pos(0, 1), LineNumbers: ln, Float: &Float{View: view}})
	if got, want := rowTexts(s, 3), []string{"  1 abc", "  2 d pq", "  3 g rs"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("浮かべた窓: rows = %q, want %q", got, want)
	}

	// 出力の欄。
	r = &Renderer{Width: 20, Height: 12}
	s = r.Render(&Frame{Text: b, LineNumbers: ln, Output: &Output{Title: "out", Lines: []string{"o1", "o2"}, Bottom: -1}})
	rows := r.TextRows()
	if got := rowTexts(s, rows+2)[rows+1:]; got[0] != "o1" {
		t.Errorf("出力の欄の最初の行 = %q, want %q", got[0], "o1")
	}

	// 見比べている間の詰め物の行。番号は出さず、行番号の列は linenr の色の空白で埋める（Vim 9.1 と同じ。PR #272 のレビューの推奨 1）。
	// 印の列は色を付けずに空ける。
	theme := Theme{StyleLineNr: {Fg: Indexed(1), Bg: Indexed(4)}, StyleDiffDelete: {Bg: Indexed(6)}}
	r = &Renderer{Width: 12, Height: 8, Theme: theme}
	s = r.Render(&Frame{Text: b, LineNumbers: ln, Diff: &Diff{Fill: map[int]int{1: 1, 3: 1}}})
	want := []string{"  1 abc", "    --------", "  2 def", "  3 ghi", "    --------", "~"}
	if got := rowTexts(s, len(want)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("詰め物の行: rows = %q, want %q", got, want)
	}
	for c := range 4 {
		if a := s.Grid.At(1, c).Attr; a != theme[StyleLineNr] {
			t.Errorf("詰め物の行の行番号の列の %d 列目の色 = %+v, want linenr", c, a)
		}
	}
	diag := []Diagnostic{{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 1)}, Severity: SeverityError}}
	s = r.Render(&Frame{Text: b, LineNumbers: ln, Diagnostics: diag, Diff: &Diff{Fill: map[int]int{1: 1}}})
	for c := range 6 {
		want := theme[StyleLineNr]
		if c < 2 {
			want = Attr{}
		}
		if a := s.Grid.At(1, c).Attr; a != want {
			t.Errorf("印の列があるときの詰め物の行の %d 列目の色 = %+v, want %+v", c, a, want)
		}
	}
}
