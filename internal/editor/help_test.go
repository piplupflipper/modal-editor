package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/docs"
	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// cursorLine は、カーソルのあるバッファのカーソルの行を返す。
func (h *harness) cursorLine() string {
	var s string
	h.e.do(func() { s = h.e.cur.buf.Line(h.e.cur.state.Cursor().Line) })
	return s
}

// wrappedLine は、カーソルの行から、窓の幅で折り返した続きの行をつないだ元の行を返す（ヘルプのポップアップは長い行を折り返す）。
// 折り返しは空白を行末に残すので、続きの行を区切りなしでつなぐと元の行に戻る。最大で n 行をつなぐ。
func (h *harness) wrappedLine(n int) string {
	var s string
	h.e.do(func() {
		b, l := h.e.cur.buf, h.e.cur.state.Cursor().Line
		for i := l; i < min(l+n, b.LineCount()); i++ {
			s += b.Line(i)
		}
	})
	return s
}

// :help は docs/usage.md を、編集を禁止したファイルでないバッファの部品で、画面の中央のポップアップにフォーカスを入れて出し、
// 語の説明の行へカーソルを置く。タブは増やさない。見つからない語は E149 にする。
//
// req: FR-88, FR-89
func TestHelp(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":help<CR>")
	h.settle()
	var title string
	var readOnly, focused, center bool
	var tabs int
	h.e.do(func() {
		title, readOnly, tabs = h.e.cur.title, h.e.cur.buf.ReadOnly(), len(h.e.view.tabs)
		if f := h.e.float; f != nil {
			focused, center = f.focused && f.bs == h.e.cur, f.center
		}
	})
	if title != helpTitle || !readOnly || tabs != 1 || !focused || !center {
		t.Fatalf(":help: title %q, read-only %v, tabs %d, focused %v, center %v, message %q", title, readOnly, tabs, focused, center, h.message())
	}
	first, _, _ := strings.Cut(docs.Usage, "\n")
	if got := h.text(); !strings.HasPrefix(got, first+"\n") {
		t.Errorf(":help text does not start with docs/usage.md: %.80q", got)
	}
	if got := h.cursorLine(); got != "# 設定と標準の拡張機能の使い方" {
		t.Errorf(":help cursor line = %q", got)
	}
	if !strings.Contains(h.term.screen(), "# 設定と標準の拡張機能の使い方") {
		t.Errorf("screen does not show the help:\n%s", h.term.screen())
	}

	// ファイルでないバッファは編集の禁止の状態で始まる（FR-88）。
	h.keys("x")
	h.settle()
	if msg := h.message(); msg != "E21: 編集を禁止したバッファは変更できません" {
		t.Errorf("x in the help: message %q", msg)
	}

	// PR #230 のレビューの入力（w、e、d、x、:e、s）は、語を含む別の単語の見出しへ飛ばず、そのキーかコマンドの行へ移る。
	for _, tt := range []struct{ word, want string }{
		{"w", "- `w`: 次の単語の先頭へ"},
		{"e", "- `e`: 単語の末尾へ"},
		{"d", "- `d`: 消す"},
		{"x", "- `x`: "},
		{"s", "`sa`、`sd`、`sr`"},
		{":e", "| `:e`、`:edit`"},
		{":w", "| `:w`、`:write`"},
		{":s", "| `:s`、`:substitute`"},
		{"readonly", "| `readonly` |"},
		{":readonly", "`:readonly`"},
		{":tabmove", "`:tabmove"},
		{"reg", "`:registers`"},
		{"registers.historySize", "historySize"},
		{"leader", "`leader`"},
		{"<lt>C-o>", "`<C-o>`"},
		{"K", "`K`"},
		// Parser が先に読むキーと、注記の箇条書きに当たっていた語（#233）。
		{"q", "- `q`: マクロの記録を始める"},
		{".", "- `.`: 直前の変更を繰り返す"},
		{"@", "- `@`: マクロを再生する"},
		{"sa", "- `sa`: "},
		{":!", "`:!`"},
		{":wqall", "`:wqall`"},
		// 操作の ID と同じ語は、説明の節があればそこに当たり、操作の ID の一覧の行には他に当たる所が無いときだけ当たる（#257 のレビューの推奨 2）。
		{"rename", "| `:rename 新しい名前` |"},
		{"references", "| `<leader>lr` | `references` |"},
		{"definition", "| `definition` |"},
		{"hover", "`hover` を打って"},
		{"undo", "| `undo` | 操作 |"},
		// 点で区切った ID は、最後の部分の JSON のキー（statusline の "right"）より一覧の行を先に当てる。
		{"insert.right", "| `insert.right` | 操作 |"},
		// ID の語は、操作の ID の節の本文（tmux の prefix の注記、書き方の規則）と、"action": "語" の行より一覧の行を先に当てる
		// （PR #258 の再レビューの推奨 2）。
		{"insert.left", "| `insert.left` | 操作 |"},
		{"toggle-case", "| `toggle-case` | オペレーター |"},
		{"toggle-case-char", "| `toggle-case-char` | 操作 |"},
		{"line-start", "| `line-start` | モーション |"},
		{"page-down", "| `page-down` | 操作 |"},
		{"line-end", "| `line-end` | モーション |"},
		// main と同じ行に当たる語（再レビューで main と比べた 11 語。rename、references、definition、hover は上にある）。
		{"delete", "| `:d`、`:delete`（`:d`） |"},
		{"insert", "モードの名前は `normal`、`visual`、`insert`"},
		{"visual", "| `visual` | ビジュアルモードの選択の範囲 |"},
		{"format", "| `gq{モーション}` | `format` |"},
		{"debug.continue", "| `<leader>dc` | `debug.continue` |"},
		{"codeAction", "| `<leader>la` | `codeAction` |"},
		{"left", "`statusline` は、`left` と `right`"},
	} {
		h.keys(":h " + tt.word + "<CR>")
		h.settle()
		if got := h.cursorLine(); !strings.Contains(got, tt.want) {
			t.Errorf(":h %s: cursor line %q does not contain %q (message %q)", tt.word, got, tt.want, h.message())
		}
	}
	h.e.do(func() { tabs = len(h.e.view.tabs) })
	if tabs != 1 {
		t.Errorf("repeated :help made %d tabs", tabs)
	}

	h.keys(":help no-such-word-227<CR>")
	h.settle()
	if msg := h.message(); msg != "E149: no-such-word-227 のヘルプがありません" {
		t.Errorf(":help unknown: message %q", msg)
	}

	h.keys(":he<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf(":he: message %q", msg)
	}

	// <Esc> で閉じてファイルのタブへ戻り、ファイルのタブから :help で、またポップアップに出す。
	h.keys("<Esc>")
	h.settle()
	var closed bool
	h.e.do(func() { closed = h.e.float == nil && h.e.cur.path != "" })
	if !closed {
		t.Errorf("<Esc> did not close the help popup")
	}
	h.keys(":help :readonly<CR>")
	h.settle()
	h.e.do(func() { title = h.e.cur.title })
	if title != helpTitle || !strings.Contains(h.cursorLine(), "`:readonly`") {
		t.Errorf(":help from a file tab: title %q, line %q", title, h.cursorLine())
	}
	// :tabopen でタブへ広げる（FR-70）。
	h.keys(":tabopen<CR>")
	h.settle()
	h.e.do(func() { title, tabs, closed = h.e.cur.title, len(h.e.view.tabs), h.e.float == nil })
	if title != helpTitle || tabs != 2 || !closed {
		t.Errorf(":tabopen: title %q, tabs %d, popup closed %v, message %q", title, tabs, closed, h.message())
	}
}

// :help の文書の後には、読み込んだ拡張機能の操作の説明文（宣言ファイルの description）の節が付く。
//
// req: FR-89
func TestHelpExtensions(t *testing.T) {
	specs := fakeSpecs(t)
	specs[0].Manifest.Contributes.TextObjects[0].Description = "行のテスト用の説明"
	c := &specs[0].Manifest.Contributes
	c.Commands = append(c.Commands, ext.Contribution{ID: "fake.leader", Keys: "<Leader>z"})
	h := start(t, Config{Extensions: specs, Leader: ","})
	h.keys(":help fake.line<CR>")
	h.settle()
	if got, want := h.wrappedLine(2), "- `fake.line`（拡張機能 fake のテキストオブジェクト。キーは `il`）: 行のテスト用の説明"; !strings.HasPrefix(got, want) {
		t.Errorf(":help fake.line: cursor line %q (message %q)", got, h.message())
	}
	// 宣言の <Leader> は、キーの一覧と同じく設定の leader に置き換えて出す。
	h.keys(":help fake.leader<CR>")
	h.settle()
	if got, want := h.wrappedLine(2), "- `fake.leader`（拡張機能 fake のコマンド。キーは `,z`）"; !strings.HasPrefix(got, want) {
		t.Errorf(":help fake.leader: cursor line %q (message %q)", got, h.message())
	}
}

// listText は、フォーカスのあるポップアップの見出しと、バッファの中身の前に改行を足したものを返す。
func (h *harness) listText() (head, text string) {
	h.e.do(func() {
		if f := h.e.float; f != nil {
			head = f.title
		}
		text = "\n" + h.e.cur.buf.Text()
	})
	return head, text
}

// 接頭辞の後の ? で、その接頭辞で始まるキーの一覧を、一覧の部品（編集を禁止したファイルでないバッファ）で画面の中央のポップアップに出す。
// 一覧は、本体の既定のキー、LSP と DAP の既定のキー、利用者のキーマップ、拡張機能の contributes から作る。
// :keys は、引数の接頭辞（無ければすべて）で絞った一覧を出す。
//
// req: FR-88, FR-90
func TestKeyList(t *testing.T) {
	specs := fakeSpecs(t)
	specs[0].Manifest.Contributes.TextObjects[0].Description = "行のテスト用の説明"
	cfg := Config{
		Extensions: specs,
		Keymap:     map[string]map[string]string{"normal": {"<Leader>lx": ":w<CR>", ",gs": ":w<CR>", "<Leader>la": "dd", "s": "cl"}},
	}
	h := start(t, cfg)

	h.keys("<Space>l?")
	h.settle()
	var title string
	var readOnly, list bool
	h.e.do(func() {
		title, readOnly = h.e.cur.title, h.e.cur.buf.ReadOnly()
		list = h.e.float != nil && h.e.float.kind == floatList && h.e.float.focused && h.e.float.center
	})
	if title != keyListTitle || !readOnly || !list {
		t.Fatalf("<Space>l?: title %q, read-only %v, list popup %v, message %q", title, readOnly, list, h.message())
	}
	head, text := h.listText()
	if !strings.HasPrefix(head, "LSP のキーの一覧（<Space>l で始まるものと LSP の操作。") {
		t.Errorf("<Space>l? head = %q", head)
	}
	for _, want := range []string{
		"コードアクション（LSP）（設定の keymap が優先）", "→ dd",
		"<Space>lx", "設定", "→ :w<CR>",
		"<Space>l?", "LSP のキーの一覧",
		"\n<C-]> ",
		// <leader>l で始まらない LSP の操作のキー（#227 のオーケストレータの決定）。
		"\nK ", "\ngq ", "\n<C-]> ", "整形する（LSP）",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("<Space>l? list lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "<Space>dc") {
		t.Errorf("<Space>l? list has <Space>dc:\n%s", text)
	}
	// 大文字と小文字を区別しない順（同じなら小文字が先）で、? の行は末尾に置く。
	order := []string{"\n<Space>ld ", "\n<Space>lD ", "\n<Space>le ", "\n<Space>lE ", "\n<Space>lx ", "\n<Space>l? "}
	for i := 1; i < len(order); i++ {
		if a, b := strings.Index(text, order[i-1]), strings.Index(text, order[i]); a < 0 || b < 0 || a > b {
			t.Errorf("%q (%d) should come before %q (%d):\n%s", order[i-1], a, order[i], b, text)
		}
	}
	// 一覧の行は、表示の文字列のほかに、キーの列と操作を持つ。
	var rows []keyRow
	h.e.do(func() { rows = h.e.keyRows(input.KeyGroupLSP, editcmd.MustParseKeys("<Space>l")) })
	var builtin, user *keyRow
	for i := range rows {
		if rows[i].Label == "<Space>la" && rows[i].Source == sourceBuiltin {
			builtin = &rows[i]
		}
		if rows[i].Label == "<Space>la" && rows[i].Source == sourceUser {
			user = &rows[i]
		}
	}
	if builtin == nil || builtin.Ref.ID != "codeAction" || !builtin.Ref.Provider() || builtin.Kind != input.BindAction || builtin.Overridden != input.MapNormal {
		t.Errorf("builtin <Space>la row = %+v", builtin)
	}
	if user == nil || editcmd.KeysString(user.RHS) != "dd" || user.Modes != input.MapNormal || user.Overridden != 0 {
		t.Errorf("user <Space>la row = %+v", user)
	}

	// 一覧の中で打った ? も、前の一覧を閉じて新しい一覧に替える。タブは増やさない。
	h.keys("<Space>d?")
	h.settle()
	var tabs int
	h.e.do(func() { tabs = len(h.e.view.tabs) })
	if _, text = h.listText(); !strings.Contains(text, "<Space>dc") || strings.Contains(text, "<Space>la") || tabs != 1 {
		t.Errorf("<Space>d?: tabs %d, text:\n%s", tabs, text)
	}

	// 既定のキーに無い接頭辞の ? も、登録の接頭辞から作る（g?、利用者のキーマップの ,g?）。
	h.keys("g?")
	h.settle()
	if _, text = h.listText(); !strings.Contains(text, "gg") || !strings.Contains(text, "gq") || strings.Contains(text, "<Space>") {
		t.Errorf("g?: text:\n%s", text)
	}
	h.keys(",g?")
	h.settle()
	if _, text = h.listText(); !strings.Contains(text, ",gs") {
		t.Errorf(",g?: text %q, message %q", text, h.message())
	}

	h.keys(":keys<CR>")
	h.settle()
	head, text = h.listText()
	for _, want := range []string{"q{レジスター}", "il", "拡張機能 fake", "行のテスト用の説明", "fake.slow", "sr", "囲みを替える"} {
		if !strings.Contains(text, want) {
			t.Errorf(":keys lacks %q:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(head, "キーの一覧（すべて。") {
		t.Errorf(":keys head = %q", head)
	}
	// 利用者のキーマップの s は、sa、sd、sr の手前の部分なので、それらの行にも上書きの印を付ける（#233）。
	if !strings.Contains(text, "囲みを替える（sr{前の文字}{後の文字}）（設定の keymap が優先）") {
		t.Errorf(":keys: sr is not marked as overridden by s:\n%s", text)
	}
	h.keys(":keys <lt>Leader>d<CR>")
	h.settle()
	if head, _ = h.listText(); !strings.HasPrefix(head, "キーの一覧（<Space>d で始まるもの。") {
		t.Errorf(":keys <Leader>d: %q", head)
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

// キーの一覧の <CR> は、ポップアップを閉じてから、カーソルの行のキーを打ったのと同じく実行する（#236）。
// 回数のように引数を続けるキーと、ノーマルモードでないキーは実行せずに知らせる。
//
// req: FR-90
func TestKeyListSelect(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	cfg := Config{WorkDir: dir, Keymap: map[string]map[string]string{"normal": {"<Leader>zz": "dd"}}}
	h := start(t, cfg, "a.txt")
	h.keys("<Space>z?")
	h.settle()
	if _, text := h.listText(); !strings.HasPrefix(text, "\n<Space>zz  n  設定  → dd\n") {
		t.Fatalf("<Space>z?: %q (message %q)", text, h.message())
	}
	h.keys("<CR>")
	h.settle()
	var closed bool
	h.e.do(func() { closed = h.e.float == nil })
	if got := h.text(); got != "two\nthree" || !closed {
		t.Errorf("<CR> on <Space>zz: text %q, popup closed %v, message %q", got, closed, h.message())
	}

	h.keys(":keys<CR>G<CR>")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "一覧からは実行できません") {
		t.Errorf("<CR> on the last row (a parser key): message %q", msg)
	}
}

// マクロの再生の途中でキーの一覧のポップアップを出したら、再生の残りのキーはポップアップの中で動く（Vim のマクロのキーがカーソルのあるバッファへ入るのと同じ）。
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
