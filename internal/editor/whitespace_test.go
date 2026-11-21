package editor

import (
	"strings"
	"testing"
)

// :set list と :set list! で空白の記号の描き方を切り替え（FR-170）、カーソルの列と望みの列も切り替わることを確かめる。
// 期待値は、2026-11-21 に nvim 0.12.5（--clean）と Vim 9.1 で同じテキストを開き、:set list の有無で "\tabc" の 1 文字目のカーソルと、
// そこから j で移った位置を確かめた結果である（list なら画面の 0 列目に出て、j で下の行の 1 文字目へ移る。nolist なら 7 列目と 8 文字目）。
// ポップアップの中のバッファ（ヘルプなど）には記号を描かないので、client の画面のバッファの State だけが、タブの最初の列で数える。
//
// req: FR-170
func TestWhitespaceList(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "\tabc  \n0123456789\n"})
	term := newFakeTerm()
	term.w, term.h = 30, 8
	h := startTerm(t, term, Config{WorkDir: dir}, "a.txt")
	h.settle()
	cursorAfterJ := func() string {
		var pos string
		h.keys("<Esc>gg0j")
		h.settle()
		h.e.do(func() { pos = h.e.cur.state.Cursor().String() })
		return pos
	}
	if got := cursorAfterJ(); got != "1:7" {
		t.Errorf("既定（nolist）の j の後のカーソル = %s, want 1:7", got)
	}
	h.screenHas("既定では記号を描かない", "\n        abc\n")

	h.keys(":set list<CR>")
	h.settle()
	h.screenHas(":set list の後は既定の記号で描く", "\n>       abc--\n")
	if got := cursorAfterJ(); got != "1:0" {
		t.Errorf(":set list の後の j のカーソル = %s, want 1:0", got)
	}
	h.keys("gg0")
	h.settle()
	h.eventually("タブ文字の上のカーソルはタブの最初の列", func() bool {
		h.term.mu.Lock()
		defer h.term.mu.Unlock()
		return h.term.last.Cursor.Col == 0
	})

	h.keys(":set list!<CR>")
	h.settle()
	h.screenHas(":set list! で戻す", "\n        abc\n")
	if got := cursorAfterJ(); got != "1:7" {
		t.Errorf(":set list! の後の j のカーソル = %s, want 1:7", got)
	}
	h.keys(":set list?<CR>")
	h.settle()
	if got := h.message(); !strings.Contains(got, "whitespace.show=false") {
		t.Errorf(":set list? のメッセージ = %q, want whitespace.show=false を含む", got)
	}
}
