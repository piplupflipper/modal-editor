package editor

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// emacsInsertKeys は、config.example.jsonc の keymap.insert と同じ、インサートモードの Emacs 風の 9 個のキーである（#257）。
var emacsInsertKeys = map[string]string{
	"<C-a>": "insert.line-start", "<C-e>": "insert.line-end",
	"<C-f>": "insert.right", "<C-b>": "insert.left",
	"<C-n>": "insert.down", "<C-p>": "insert.up",
	"<C-h>": "insert.delete-prev-char", "<C-d>": "insert.delete-char", "<C-k>": "insert.delete-to-line-end",
}

// insertCase は、text（"|" がカーソル）で keys を打った後のテキストとカーソル（行:バイト）である。
type insertCase struct {
	text, keys   string
	want, cursor string
}

// runInsertCases は、操作の表 km を引く command.State で cases を確かめる。
func runInsertCases(t *testing.T, km *input.Keymap, cases []insertCase) {
	t.Helper()
	for _, tc := range cases {
		lines := strings.Split(tc.text, "\n")
		var at buffer.Position
		for i, l := range lines {
			if j := strings.Index(l, "|"); j >= 0 {
				at = buffer.Position{Line: i, Byte: j}
				lines[i] = l[:j] + l[j+1:]
			}
		}
		buf := buffer.New(strings.Join(lines, "\n") + "\n")
		st := command.New(buf, input.NewParser(km))
		st.SetCursor(at)
		for _, k := range editcmd.MustParseKeys(tc.keys) {
			st.Feed(k)
		}
		got := strings.TrimSuffix(buf.Text(), "\n")
		c := st.Cursor()
		if cursor := fmt.Sprintf("%d:%d", c.Line, c.Byte); got != tc.want || cursor != tc.cursor || st.Mode() != command.ModeNormal {
			t.Errorf("%q %s: text %q cursor %s mode %v, want %q %s", tc.text, tc.keys, got, cursor, st.Mode(), tc.want, tc.cursor)
		}
		if n := st.Registers().Pushes(); n != 0 {
			t.Errorf("%q %s: レジスターの履歴に %d 件積んだ", tc.text, tc.keys, n)
		}
	}
}

// emacsKeymap は、本体の操作の表に emacsInsertKeys を割り当てた表を返す。
func emacsKeymap(t *testing.T) *input.Keymap {
	t.Helper()
	km := input.DefaultKeymap()
	if errs := applyKeymapActions(km, map[string]map[string]string{"insert": emacsInsertKeys}, editcmd.MustParseKeys("<Space>"), nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	return km
}

// インサートモードの <Del>（insert.delete-char）は、Vim 9.1 のインサートモードの <Del> と同じ結果になる。
// アンドゥの単位、ドットリピート、回数による繰り返し、マクロ、カーソルキーで区切った後の扱いは <BS> と同じである。
//
// 期待値は、Vim 9.1（パッチ 1-16）を `vim -Nu NONE -n --not-a-term` で起動し、:set backspace=indent,eol,start の後に
// text を置いてアンドゥの履歴を消し、keys を feedkeys(keys, "xt") で入力した後のテキストとカーソル（行:バイト）である（PR の説明に方法を書いた）。
//
// req: FR-127
func TestInsertDeleteCharVim(t *testing.T) {
	runInsertCases(t, input.DefaultKeymap(), []insertCase{
		{"a|bc", "i<Del><Esc>", "ac", "0:0"},
		{"a|bc", "i<Del><Del><Del><Esc>", "a", "0:0"},
		{"a|bc", "A<Del><Esc>", "abc", "0:2"},
		{"ab|c\nde", "A<Del><Esc>", "abcde", "0:2"},
		{"ab|c\n  de", "A<Del><Esc>", "abc  de", "0:2"},
		{"ab|c\n\nde", "A<Del><Esc>", "abc\nde", "0:2"},
		{"|\nde", "i<Del><Esc>", "de", "0:0"},
		{"|", "i<Del><Esc>", "", "0:0"},
		{"|", "i<Del>x<Esc>", "x", "0:0"},
		{"ab|c\nde", "jA<Del><Esc>", "abc\nde", "1:1"},
		{"ab|c\nde", "jA<Del>x<Esc>", "abc\ndex", "1:2"},
		{"あ|いう", "i<Del><Esc>", "あう", "0:0"},
		{"あい|う", "a<Del><Esc>", "あいう", "0:6"},
		{"あい|う\nえ", "A<Del><Esc>", "あいうえ", "0:6"},
		{"a|bc", "ix<Del>y<Esc>", "axyc", "0:2"},
		{"a|bcdef", "ix<Del>y<Esc>u", "abcdef", "0:1"},
		{"a|bcdef", "ix<Del>y<Esc>l.", "axyxydef", "0:4"},
		{"a|bcdef", "i<Del><Esc>l.", "adef", "0:0"},
		{"a|bcdef", "i<Del><Del><Esc>u", "abcdef", "0:1"},
		{"a|bcdef", "i<Del><Esc>u", "abcdef", "0:1"},
		{"a|bcdef", "3ix<Del><Esc>", "axxxef", "0:3"},
		{"a|bcdefgh", "3i<Del><Esc>", "aefgh", "0:0"},
		{"ab|c\nde\nfg", "A<Del><Esc>j.", "abcde\nfg", "1:1"},
		{"a|bcdef\nabcdef", "qai<Del>x<Esc>qj0l@a", "axcdef\naxcdef", "1:1"},
		{"a|bcdef", "ix<Left><Del><Esc>", "abcdef", "0:0"},
		{"a|bcdef", "ix<Left><Del><Esc>u", "axbcdef", "0:1"},
		{"a|bcdef", "ix<Left><Del><Esc>l.", "acdef", "0:0"},
		{"a|bcdef", "ix<Del><Left><Esc>u", "abcdef", "0:1"},
		{"a|bc\nd", "A<Del><Del><Esc>", "abc", "0:2"},
		{"ab|c\nde", "A<Del><Esc>u", "abc\nde", "0:2"},
	})
}

// insert.delete-to-line-end は、カーソルから行末まで消し、行末の位置では次の行をつなげる（Emacs の C-k）。
// 消したテキストはレジスターと履歴に入れない。アンドゥの単位、ドットリピート、回数による繰り返し、マクロは <BS> と同じ扱いである。
//
// req: FR-127
func TestInsertDeleteToLineEnd(t *testing.T) {
	runInsertCases(t, emacsKeymap(t), []insertCase{
		{"a|bcdef", "i<C-k><Esc>", "a", "0:0"},
		{"ab|c\nde", "A<C-k><Esc>", "abcde", "0:2"},
		{"ab|c\n  de", "A<C-k><Esc>", "abc  de", "0:2"},
		{"a|bc", "A<C-k><Esc>", "abc", "0:2"},
		{"|", "i<C-k>x<Esc>", "x", "0:0"},
		{"あ|いう", "i<C-k><Esc>", "あ", "0:0"},
		{"a|bcdef", "i<C-k><Esc>u", "abcdef", "0:1"},
		{"a|bc\nabc", "i<C-k>x<Esc>j0l.", "ax\nax", "1:1"},
		{"a|bc\nabc", "qai<C-k>x<Esc>qj0l@a", "ax\nax", "1:1"},
		{"a|bc\nd\ne", "3i<C-k><Esc>", "a\ne", "0:0"},
	})
}

// config.example.jsonc の Emacs 風の 9 個のキーは、表のとおりに働く。行頭、行末、最初と最後の行、空のバッファ、全角文字の上では、
// 動けない移動と消すものの無い削除は何もしない。
//
// req: FR-126, FR-127
func TestEmacsInsertKeys(t *testing.T) {
	runInsertCases(t, emacsKeymap(t), []insertCase{
		{"ab|c def", "i<C-a>X<Esc>", "Xabc def", "0:0"},
		{"ab|c def", "i<C-e>X<Esc>", "abc defX", "0:7"},
		{"ab|c", "i<C-f>X<Esc>", "abcX", "0:3"},
		{"ab|c", "i<C-b>X<Esc>", "aXbc", "0:1"},
		{"abc\nd|ef", "i<C-p>X<Esc>", "aXbc\ndef", "0:1"},
		{"a|bc\ndef", "i<C-n>X<Esc>", "abc\ndXef", "1:1"},
		{"ab|c", "i<C-h><Esc>", "ac", "0:0"},
		{"ab|c", "i<C-d><Esc>", "ab", "0:1"},
		{"a|bc", "i<C-k><Esc>", "a", "0:0"},
		// 行頭、行末、最初と最後の行。
		{"|abc", "i<C-b>X<Esc>", "Xabc", "0:0"},
		{"|abc", "i<C-a>X<Esc>", "Xabc", "0:0"},
		{"ab|c", "A<C-f>X<Esc>", "abcX", "0:3"},
		{"ab|c", "A<C-e>X<Esc>", "abcX", "0:3"},
		{"a|bc", "i<C-n>X<Esc>", "aXbc", "0:1"},
		{"a|bc", "i<C-p>X<Esc>", "aXbc", "0:1"},
		{"abc\n|de", "i<C-h><Esc>", "abcde", "0:2"},
		{"|abc", "i<C-h>X<Esc>", "Xabc", "0:0"},
		// 空のバッファ。
		{"|", "i<C-a><C-e><C-f><C-b><C-n><C-p><C-h><C-d><C-k>X<Esc>", "X", "0:0"},
		// 全角文字の上。
		{"あ|いう", "i<C-f>X<Esc>", "あいXう", "0:6"},
		{"あ|いう", "i<C-b>X<Esc>", "Xあいう", "0:0"},
		{"あ|いう", "i<C-h><Esc>", "いう", "0:0"},
		{"あ|いう", "i<C-d><Esc>", "あう", "0:0"},
		{"あいう\nか|きく", "i<C-p>X<Esc>", "あXいう\nかきく", "0:3"},
		{"あ|いう\nかきく", "i<C-n>X<Esc>", "あいう\nかXきく", "1:3"},
	})
}

// 設定を書かない状態では、インサートモードの修飾キーの付いたキーは何もしない（今までどおり）。
//
// req: FR-127
func TestInsertUnmappedKeys(t *testing.T) {
	runInsertCases(t, input.DefaultKeymap(), []insertCase{
		{"a|bc", "i<C-a><C-e><C-k><C-d><C-h><S-Left><Insert>X<Esc>", "aXbc", "0:1"},
	})
}

// 操作の表のインサートモードの操作の ID（internal/input）と、実装のある ID（internal/command）は同じである。
//
// req: FR-127
func TestInsertActionIDs(t *testing.T) {
	var table []string
	for _, a := range input.Actions() {
		if a.Modes&input.MapInsert != 0 {
			table = append(table, a.ID)
		}
	}
	impl := command.InsertActionIDs()
	slices.Sort(table)
	slices.Sort(impl)
	if !slices.Equal(table, impl) {
		t.Errorf("操作の表 %v, 実装 %v", table, impl)
	}
}

// 設定の keymap の右辺の操作は、ノーマルモード、ビジュアルモード、インサートモードのキーに割り当てられ、文字列の右辺と並べて使える。
// 操作を割り当てたキーを含むマクロの記録と再生、ドットリピートは同じ結果になる。テキストのバッファのポップアップの中でも効く（FR-71）。
// 使えない項目は設定の誤りとして知らせ、その項目だけを使わない。キーの一覧では出どころを「設定」にする。
//
// req: FR-126, FR-127
func TestKeymapActionConfig(t *testing.T) {
	insert := map[string]string{"<BS>": "insert.delete-char"}
	for k, v := range emacsInsertKeys {
		insert[k] = v
	}
	cfg := Config{
		Keymap: map[string]map[string]string{"normal": {"Q": "dd"}, "insert": {"jk": "<Esc>"}},
		KeymapActions: map[string]map[string]string{
			"normal":      {"<C-e>": "line-end", "Q": "line-start", "<Leader>x": "no-such"},
			"visual":      {"<C-e>": "line-end"},
			"insert":      insert,
			"commandLine": {"<C-a>": "line-start"},
		},
	}
	dir := writeFiles(t, map[string]string{"a.txt": "abc def\nxyz\nfoo\n", "b.txt": "bar\n"})
	cfg.WorkDir = dir
	h := start(t, cfg, "a.txt")
	if msg := h.message(); !strings.HasPrefix(msg, "設定の誤り 3 件") {
		t.Errorf("message = %q", msg)
	}
	// ノーマルモードとビジュアルモードの操作。文字列の右辺（Q の dd）は操作の項目（Q）より先に効く。
	h.keys("<C-e>")
	h.settle()
	var c buffer.Position
	h.e.do(func() { c = h.e.cur.state.Cursor() })
	if c != (buffer.Position{Line: 0, Byte: 6}) {
		t.Errorf("normal <C-e>: cursor %v", c)
	}
	h.keys("0v<C-e>")
	h.settle()
	var mode command.Mode
	h.e.do(func() { c, mode = h.e.cur.state.Cursor(), h.e.cur.state.Mode() })
	if c.Line != 0 || c.Byte < 6 || mode != command.ModeVisual {
		t.Errorf("visual <C-e>: cursor %v, mode %v", c, mode)
	}
	h.keys("<Esc>")
	h.settle()
	// インサートモードの操作と、文字列の右辺（jk）。
	h.keys("i<C-e>!jk")
	h.settle()
	if got := h.text(); got != "abc def!\nxyz\nfoo" {
		t.Errorf("i<C-e>!jk: text %q", got)
	}
	// 上書きした <BS> は insert.delete-char、<C-h> は insert.delete-prev-char である。
	h.keys("0i<BS><Right><C-h>X<Esc>")
	h.settle()
	if got := h.text(); got != "Xc def!\nxyz\nfoo" {
		t.Errorf("<BS> と <C-h>: text %q", got)
	}
	// マクロの記録と再生、ドットリピート。
	h.keys("jqa0i<C-e>-<C-a>+<Esc>qj@a")
	h.settle()
	if got := h.text(); got != "Xc def!\n+xyz-\n+foo-" {
		t.Errorf("マクロ: text %q", got)
	}
	h.keys("0i<C-k>Z<Esc>k.")
	h.settle()
	if got := h.text(); got != "Xc def!\nZ\nZ" {
		t.Errorf("ドットリピート: text %q", got)
	}
	// キーの一覧。
	var rows []keyRow
	h.e.do(func() { rows = h.e.keyRows("", nil) })
	var userCA, builtinBS, defaultBS bool
	for _, r := range rows {
		switch {
		case r.Label == "<C-a>" && r.Modes == input.MapInsert:
			userCA = r.Source == sourceUser && r.Description == "行の先頭へ" && r.Ref.ID == "insert.line-start"
		case r.Label == "<BS>" && r.Modes == input.MapInsert && r.Source == sourceUser:
			builtinBS = r.Ref.ID == "insert.delete-char"
		case r.Label == "<BS>" && r.Modes == input.MapInsert:
			defaultBS = r.Ref.ID == "insert.delete-prev-char" && r.Overridden == input.MapInsert
		}
	}
	if !userCA || !builtinBS || !defaultBS {
		t.Errorf("キーの一覧: <C-a> %v, 設定の <BS> %v, 既定の <BS> %v", userCA, builtinBS, defaultBS)
	}

	// テキストのバッファのポップアップの中のインサートモード（FR-71）。
	h.keys(":w<CR>:e b.txt<CR>:popup<CR>A<C-a>P<C-e>Q<C-b><C-d><Esc>")
	h.settle()
	if fi, ok := h.float(); !ok || fi.text != "Pbar" {
		t.Errorf("ポップアップの中: %+v %v", fi, ok)
	}
}
