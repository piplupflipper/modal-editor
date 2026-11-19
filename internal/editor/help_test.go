package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/docs"
	"github.com/piplupflipper/modal-editor/internal/command"
)

// cursorLine は、カーソルのあるバッファのカーソルの行を返す。
func (h *harness) cursorLine() string {
	var s string
	h.e.do(func() { s = h.e.cur.buf.Line(h.e.cur.state.Cursor().Line) })
	return s
}

// :help は docs/usage.md を、編集を禁止したファイルでないバッファのタブに出し、語の説明の行へカーソルを置く。
// 同じ client の 2 回目の :help は、新しいタブを作らずに同じタブへ移る。見つからない語は E149 にする。
//
// req: FR-88, FR-89
func TestHelp(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":help<CR>")
	h.settle()
	var title string
	var readOnly bool
	var tabs int
	h.e.do(func() { title, readOnly, tabs = h.e.cur.title, h.e.cur.buf.ReadOnly(), len(h.e.view.tabs) })
	if title != helpTitle || !readOnly || tabs != 2 {
		t.Fatalf(":help: title %q, read-only %v, tabs %d, message %q", title, readOnly, tabs, h.message())
	}
	if got := h.text(); !strings.HasPrefix(got, strings.TrimRight(docs.Usage, "\n")) {
		t.Errorf(":help text does not start with docs/usage.md: %.80q", got)
	}
	if got := h.cursorLine(); got != "# 設定と標準の拡張機能の使い方" {
		t.Errorf(":help cursor line = %q", got)
	}
	if !strings.Contains(h.term.screen(), helpTitle) {
		t.Errorf("tab line does not show %s:\n%s", helpTitle, h.term.screen())
	}

	// ファイルでないバッファは編集の禁止の状態で始まる（FR-88）。
	h.keys("x")
	h.settle()
	if msg := h.message(); msg != "E21: 編集を禁止したバッファは変更できません" {
		t.Errorf("x in the help: message %q", msg)
	}

	for _, tt := range []struct{ word, want string }{
		{"readonly", "`:readonly`"},
		{":tabmove", "`:tabmove"},
		{"registers.historySize", "historySize"},
		{"leader", "`leader`"},
		{"<lt>C-o>", "`<C-o>`"},
	} {
		h.keys(":h " + tt.word + "<CR>")
		h.settle()
		if got := h.cursorLine(); !strings.Contains(got, tt.want) {
			t.Errorf(":h %s: cursor line %q does not contain %q (message %q)", tt.word, got, tt.want, h.message())
		}
	}
	h.e.do(func() { tabs = len(h.e.view.tabs) })
	if tabs != 2 {
		t.Errorf("repeated :help made %d tabs", tabs)
	}

	h.keys(":help no-such-word-227<CR>")
	h.settle()
	if msg := h.message(); msg != "E149: no-such-word-227 のヘルプがありません" {
		t.Errorf(":help unknown: message %q", msg)
	}

	// ファイルのタブから :help で、ヘルプのタブへ移る。
	h.keys(":b 1<CR>:help readonly<CR>")
	h.settle()
	h.e.do(func() { title = h.e.cur.title })
	if title != helpTitle || !strings.Contains(h.cursorLine(), "`:readonly`") {
		t.Errorf(":help from a file tab: title %q, line %q", title, h.cursorLine())
	}
}

// :help の文書の後には、読み込んだ拡張機能の操作の説明文（宣言ファイルの description）の節が付く。
//
// req: FR-89
func TestHelpExtensions(t *testing.T) {
	specs := fakeSpecs(t)
	specs[0].Manifest.Contributes.TextObjects[0].Description = "行のテスト用の説明"
	h := start(t, Config{Extensions: specs})
	h.keys(":help fake.line<CR>")
	h.settle()
	if got := h.cursorLine(); got != "- `fake.line`（拡張機能 fake のテキストオブジェクト。キーは `il`）: 行のテスト用の説明" {
		t.Errorf(":help fake.line: cursor line %q (message %q)", got, h.message())
	}
}

// 接頭辞の後の ? で、その接頭辞で始まるキーの一覧を、編集を禁止したファイルでないバッファのタブに出す。
// 一覧は、本体の既定のキー、LSP と DAP の既定のキー、利用者のキーマップ、拡張機能の contributes から作る。
// :keys は、引数の接頭辞（無ければすべて）で絞った一覧を出す。
//
// req: FR-88, FR-90
func TestKeyList(t *testing.T) {
	specs := fakeSpecs(t)
	specs[0].Manifest.Contributes.TextObjects[0].Description = "行のテスト用の説明"
	cfg := Config{
		Extensions: specs,
		Keymap:     map[string]map[string]string{"normal": {"<Leader>lx": ":w<CR>", ",gs": ":w<CR>"}},
	}
	h := start(t, cfg)

	h.keys("<Space>l?")
	h.settle()
	var title string
	var readOnly bool
	h.e.do(func() { title, readOnly = h.e.cur.title, h.e.cur.buf.ReadOnly() })
	if title != keyListTitle || !readOnly {
		t.Fatalf("<Space>l?: title %q, read-only %v, message %q", title, readOnly, h.message())
	}
	text := h.text()
	for _, want := range []string{
		"キーの一覧（<Space>l で始まるもの。",
		"<Space>la", "コードアクション（LSP）",
		"<Space>lx", "設定", "→ :w<CR>",
		"<Space>l?", "LSP のキーの一覧",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("<Space>l? list lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<Space>dc") {
		t.Errorf("<Space>l? list has <Space>dc:\n%s", text)
	}

	// 同じ client の 2 回目は、同じタブの中身を替える。
	h.keys("<Space>d?")
	h.settle()
	var tabs int
	h.e.do(func() { tabs = len(h.e.view.tabs) })
	if text = h.text(); !strings.Contains(text, "<Space>dc") || strings.Contains(text, "<Space>la") || tabs != 2 {
		t.Errorf("<Space>d?: tabs %d, text:\n%s", tabs, text)
	}

	// 既定のキーに無い接頭辞の ? も、登録の接頭辞から作る（g?、利用者のキーマップの ,g?）。
	h.keys("g?")
	h.settle()
	if text = h.text(); !strings.Contains(text, "gg") || !strings.Contains(text, "gq") || strings.Contains(text, "<Space>") {
		t.Errorf("g?: text:\n%s", text)
	}
	h.keys(",g?")
	h.settle()
	if text = h.text(); !strings.Contains(text, ",gs") {
		t.Errorf(",g?: text %q, message %q", text, h.message())
	}

	h.keys(":keys<CR>")
	h.settle()
	text = h.text()
	for _, want := range []string{"キーの一覧（すべて。", "q{レジスター}", "il", "拡張機能 fake", "行のテスト用の説明", "fake.slow", "sr", "囲みを替える"} {
		if !strings.Contains(text, want) {
			t.Errorf(":keys lacks %q:\n%s", want, text)
		}
	}
	h.keys(":keys <lt>Leader>d<CR>")
	h.settle()
	if text = h.text(); !strings.Contains(text, "キーの一覧（<Space>d で始まるもの。") {
		t.Errorf(":keys <Leader>d: %q", text)
	}
	h.keys(":keys zz<CR>")
	h.settle()
	if msg := h.message(); msg != "zz で始まるキーはありません" {
		t.Errorf(":keys zz: message %q", msg)
	}

	h.keys("x")
	h.settle()
	if msg := h.message(); msg != "E21: 編集を禁止したバッファは変更できません" {
		t.Errorf("x in the key list: message %q", msg)
	}
}

// それ自体が操作のキー（d）の後の ? は、キーの一覧ではなく ? の検索のまま動く。
func TestKeyListKeepsSearch(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\nxfoo\nbar\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("Gd?foo<CR>")
	h.settle()
	// 終わりが行頭の exclusive な範囲なので、前の行の末尾までを消す（Vim の :help o_v の規則）。
	if got := h.text(); got != "abc\nx\nbar" {
		t.Errorf("d?foo: text %q, message %q", got, h.message())
	}
}

// マクロの再生の途中でキーの一覧のタブへ移ったら、再生の残りのキーは移った先のタブで動く（Vim のマクロのキーがカーソルのあるバッファへ入るのと同じ）。
//
// req: FR-90
func TestKeyListInMacro(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	// b レジスターに <Space>d?jj を入れて再生する。
	h.e.do(func() { h.e.cur.state.Registers().Set('b', command.Register{Text: " d?jj"}) })
	h.keys("@b")
	h.settle()
	var title string
	var line int
	h.e.do(func() { title, line = h.e.cur.title, h.e.cur.state.Cursor().Line })
	if title != keyListTitle || line != 2 {
		t.Errorf("@b: title %q, cursor line %d, message %q", title, line, h.message())
	}
}
