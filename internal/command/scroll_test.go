package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/input"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// scrollState は、"line 1" から "line 100" の 100 行のバッファを、rows 行の表示領域で開いた State を作る。margin は scroll.margin である。
func scrollState(t *testing.T, rows, margin int) (*State, *fakeViewport) {
	t.Helper()
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	s := New(newBuffer(strings.Join(lines, "\n")), input.NewParser(input.DefaultKeymap()))
	vp := &fakeViewport{rows: rows, hold: -1}
	s.Viewport = vp
	o := fakeOptions{vs: &option.Values{}}
	o.set(t, option.ScrollMargin, option.IntValue(margin))
	s.Ex.Options = o
	return s, vp
}

// スクロールのキーは、オペレーターの後では誤りになり、バッファもカーソルも表示領域も変えない（Neovim の checkclearop）。
// z で始まるキーは、本体では z で誤りになり、続く 1 文字を新しいキーとして読む（Neovim は続く 1 文字も読んで捨てる。
// docs/vim-differences.md）。ここでは、続く文字が何もしない（t は文字を待つ）キーだけを確かめる。
//
// req: FR-173
func TestScrollKeysAfterOperator(t *testing.T) {
	for _, keys := range []string{"d<C-e>", "d<C-y>", "d<C-d>", "d<C-u>", "d<C-f>", "d<C-b>", "dzt", "dzz"} {
		s, vp := scrollState(t, 10, 0)
		vp.top = 20
		s.SetCursor(pos(25, 0))
		rs := feedKeys(s, keys)
		failed := false
		for _, r := range rs {
			failed = failed || r.Err != nil
		}
		if !failed || vp.top != 20 || s.Cursor() != pos(25, 0) || s.Buffer().LineCount() != 100 {
			t.Errorf("%s: 失敗 %v、最初の行 %d、カーソル %v、行数 %d", keys, failed, vp.top, s.Cursor(), s.Buffer().LineCount())
		}
	}
}

// バッファの端で表示領域もカーソルも動かない <C-u>、<C-d>、<C-b>、<C-f> は失敗する（Neovim のビープ。マクロも止まる）。
// <C-e> と <C-y> は、動かなくても失敗しない（Neovim と同じ）。
//
// req: FR-173
func TestScrollKeysAtEdge(t *testing.T) {
	tests := []struct {
		keys      string
		top, line int // 0 から数えた、キーの前の最初の行とカーソルの行
		fails     bool
	}{
		{"<C-u>", 0, 0, true},
		{"<C-b>", 0, 0, true},
		{"<C-y>", 0, 0, false},
		{"<C-d>", 90, 99, true},
		{"<C-f>", 99, 99, true},
		{"<C-e>", 99, 99, false},
	}
	for _, tt := range tests {
		s, vp := scrollState(t, 10, 0)
		vp.top = tt.top
		s.SetCursor(pos(tt.line, 0))
		r := feedKeys(s, tt.keys)[0]
		if (r.Err != nil) != tt.fails || vp.top != tt.top || s.Cursor().Line != tt.line {
			t.Errorf("%s: err %v、最初の行 %d、カーソル %v", tt.keys, r.Err, vp.top, s.Cursor())
		}
	}
}

// 回数を付けた <C-d> と <C-u> は、送る行数（Vim の 'scroll'）を回数にし、表示領域の行数が変わるまで回数の無い <C-d> と <C-u> も
// その行数で送る。表示領域の行数が変わったら、半分の行数に戻す（Neovim の win_comp_scroll）。回数は表示領域の行数で頭打ちにする。
//
// req: FR-173
func TestHalfPageAmount(t *testing.T) {
	s, vp := scrollState(t, 10, 0)
	step := func(keys string, top, line int) {
		t.Helper()
		feedKeys(s, keys)
		if vp.top != top || s.Cursor().Line != line {
			t.Errorf("%s: 最初の行 %d、カーソル %d, want %d、%d", keys, vp.top, s.Cursor().Line, top, line)
		}
	}
	step("<C-d>", 5, 5)
	step("3<C-d>", 8, 8)
	step("<C-d>", 11, 11)
	step("<C-u>", 8, 8)
	step("99<C-d>", 18, 18)
	step("<C-u>", 8, 8)
	vp.rows = 8
	step("<C-d>", 12, 12)
}

// 回数を付けた zt、zz、zb は、カーソルをその行へ移し、移る前の位置をジャンプリストに記録する。カーソルの行と同じ回数なら記録しない。
// zt、zz、zb は描画で余白を数え直させ（hold が負）、余白のある <C-e> と <C-y> は、カーソルの行にある間は描画で数え直させない。
//
// req: FR-173, FR-174
func TestScrollCursorJumpAndHold(t *testing.T) {
	s, vp := scrollState(t, 10, 0)
	nav := &jumpCountNav{}
	s.Navigator = nav
	feedKeys(s, "30zt")
	if s.Cursor().Line != 29 || vp.top != 29 || nav.marks != 1 || vp.hold != -1 {
		t.Errorf("30zt: カーソル %v、最初の行 %d、ジャンプ %d、hold %d", s.Cursor(), vp.top, nav.marks, vp.hold)
	}
	feedKeys(s, "30zb")
	if vp.top != 20 || nav.marks != 1 {
		t.Errorf("30zb: 最初の行 %d、ジャンプ %d", vp.top, nav.marks)
	}
	feedKeys(s, "<C-e>")
	if vp.hold != -1 {
		t.Errorf("余白の無い <C-e>: hold %d, want -1", vp.hold)
	}

	s, vp = scrollState(t, 10, 3)
	feedKeys(s, "<C-e>")
	if vp.top != 1 || s.Cursor().Line != 4 || vp.hold != 4 {
		t.Errorf("余白の 3 行を残す <C-e>: 最初の行 %d、カーソル %v、hold %d", vp.top, s.Cursor(), vp.hold)
	}
}
