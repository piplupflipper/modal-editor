package main

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
)

// refChar は、行 line の先頭からのバイト数 b を、unicode/utf16 で数えた UTF-16 の位置にする（検証用の参照実装）。
func refChar(line string, b int) int {
	return len(utf16.Encode([]rune(line[:b])))
}

// TestCharConversion は、バイト数と LSP の文字の位置の変換を、無作為な行で参照実装と突き合わせる。
// 文字の境界のバイト数は往復で元に戻り、文字の途中を指す LSP の位置は文字の先頭に寄ることを確かめる。
func TestCharConversion(t *testing.T) {
	pieces := []string{"a", "日", "𝄞", "é", "\t", " ", "\xff"}
	r := rand.New(rand.NewPCG(50, 1)) //nolint:gosec // 再現できる無作為な行を作るためで、暗号の用途ではない
	for range 300 {
		var sb strings.Builder
		for range r.IntN(8) {
			sb.WriteString(pieces[r.IntN(len(pieces))])
		}
		line := sb.String()
		var bounds []int
		for i := 0; i <= len(line); {
			bounds = append(bounds, i)
			if i == len(line) {
				break
			}
			_, size := utf8.DecodeRuneInString(line[i:])
			i += size
		}
		for _, b := range bounds {
			if got, want := toChar(line, b, encUTF16), refChar(line, b); got != want {
				t.Errorf("toChar(%q, %d, utf-16) = %d, want %d", line, b, got, want)
			}
			if got := toChar(line, b, encUTF32); got != utf8.RuneCountInString(line[:b]) {
				t.Errorf("toChar(%q, %d, utf-32) = %d", line, b, got)
			}
			for _, enc := range []encoding{encUTF8, encUTF16, encUTF32} {
				if got := fromChar(line, toChar(line, b, enc), enc); got != b {
					t.Errorf("fromChar(toChar(%q, %d, %d)) = %d", line, b, enc, got)
				}
			}
		}
		// どの LSP の位置も、文字の境界に変換される。
		for ch := -1; ch <= refChar(line, len(line))+2; ch++ {
			if got := fromChar(line, ch, encUTF16); !slices.Contains(bounds, got) {
				t.Errorf("fromChar(%q, %d, utf-16) = %d, not a character boundary", line, ch, got)
			}
		}
	}
	// サロゲートペアの間は、その文字の先頭に寄せる。行の長さを超える位置は行末にする。
	if got := fromChar("a𝄞b", 2, encUTF16); got != 1 {
		t.Errorf("fromChar inside a surrogate pair = %d, want 1", got)
	}
	if got := fromChar("a日", 2, encUTF8); got != 1 {
		t.Errorf("fromChar inside a UTF-8 character = %d, want 1", got)
	}
	if got := fromChar("ab", 10, encUTF16); got != 2 {
		t.Errorf("fromChar beyond the line = %d, want 2", got)
	}
}

// TestFromLSPVirtualLine は、言語サーバーに送った末尾の改行の後ろの空の行と、その後ろの位置を、最後の行の行末に変換することを確かめる。
func TestFromLSPVirtualLine(t *testing.T) {
	ls := lines{"ab", "日"}
	for _, p := range []lspPosition{{Line: 2}, {Line: 2, Character: 5}, {Line: 9}} {
		got, virtual := ls.fromLSP(p, encUTF16)
		if got != pos(1, 3) || !virtual {
			t.Errorf("fromLSP(%+v) = %+v, %v; want 1:3, true", p, got, virtual)
		}
	}
	if got := ls.rangeFromLSP(lspRange{Start: lspPosition{Line: 1, Character: 1}, End: lspPosition{Line: 0}}, encUTF16); got.End != got.Start {
		t.Errorf("reversed range = %+v, want empty at the start", got)
	}
}

// TestFormattingTransaction は、TextEdit の列を、開始の位置の降順（同じ位置は配列の添字の降順）に並べた逐次の数え方の編集にすることと、
// 末尾の改行の後ろに触れる編集の書き換えを確かめる。
//
// req: FR-30
func TestFormattingTransaction(t *testing.T) {
	te := func(sl, sc, el, ec int, text string) textEdit {
		return textEdit{Range: lspRange{Start: lspPosition{Line: sl, Character: sc}, End: lspPosition{Line: el, Character: ec}}, NewText: text}
	}
	tests := []struct {
		name  string
		text  string
		edits []textEdit
		want  string
	}{
		{"same start: inserts in array order, then replace", "abc", []textEdit{te(0, 1, 0, 1, "X"), te(0, 1, 0, 1, "Y"), te(0, 1, 0, 2, "B")}, "aXYBc"},
		{"ascending order", "a b c", []textEdit{te(0, 0, 0, 1, "A"), te(0, 2, 0, 3, "BB"), te(0, 4, 0, 5, "")}, "A BB "},
		{"across lines with surrogate pairs", "𝄞x\ny𝄞z", []textEdit{te(0, 2, 1, 1, "-"), te(1, 3, 1, 4, "Z")}, "𝄞-𝄞Z"},
		{"replace the whole document including the final newline", "a\nb", []textEdit{te(0, 0, 2, 0, "A\nB\n")}, "A\nB"},
		{"insert after the final newline", "a", []textEdit{te(1, 0, 1, 0, "b\n")}, "a\nb"},
		{"insert an empty line after the final newline", "a", []textEdit{te(1, 0, 1, 0, "\n")}, "a\n"},
		{"delete trailing empty lines", "a\n\n", []textEdit{te(0, 1, 3, 0, "\n")}, "a"},
		{"no-op edit is dropped", "a", []textEdit{te(0, 0, 0, 0, "")}, "a"},
		{"empty insert after the final newline is dropped", "a", []textEdit{te(1, 0, 1, 0, "")}, "a"},
		// 以下の 3 つは、実際の gopls（v0.24.0）が末尾の空の行を消すときに返した編集の形である（全文の末尾に "\n" を足して送ったもの）。
		{"gopls: trailing empty lines after package", "package a\n\n", []textEdit{te(1, 0, 3, 0, "")}, "package a"},
		{"gopls: trailing empty lines after func", "package a\n\nfunc f() {}\n\n\n", []textEdit{te(3, 0, 6, 0, "")}, "package a\n\nfunc f() {}"},
		{"gopls: trailing empty lines after a block", "package a\n\nfunc f() {\n}\n\n", []textEdit{te(4, 0, 6, 0, "")}, "package a\n\nfunc f() {\n}"},
		{"delete to the end with a non-newline text keeps the final newline", "a\nb", []textEdit{te(1, 0, 2, 0, "c")}, "a\nc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := lines(strings.Split(tt.text, "\n"))
			tx := formattingTransaction(ls, true, 7, tt.edits, encUTF16)
			if tt.want == tt.text {
				if tx != nil {
					t.Fatalf("transaction = %+v, want nil", tx)
				}
				return
			}
			if tx == nil || tx.BaseVersion != 7 {
				t.Fatalf("transaction = %+v", tx)
			}
			if got := applyEdits(tt.text, tx.Edits); got != tt.want {
				t.Errorf("result = %q, want %q (edits %+v)", got, tt.want, tx.Edits)
			}
		})
	}
	if tx := formattingTransaction(lines{"a"}, true, 1, nil, encUTF16); tx != nil {
		t.Errorf("no edits: transaction = %+v, want nil", tx)
	}
}

// TestFormattingTransactionNoFinalNewline は、書き出したときに末尾に改行が付かないバッファ（全文に "\n" を足さずに送ったもの）の整形の結果を、
// 仮想の行の書き換えをせずにトランザクションにすることを確かめる。
func TestFormattingTransactionNoFinalNewline(t *testing.T) {
	te := func(sl, sc, el, ec int, text string) textEdit {
		return textEdit{Range: lspRange{Start: lspPosition{Line: sl, Character: sc}, End: lspPosition{Line: el, Character: ec}}, NewText: text}
	}
	tests := []struct {
		name  string
		text  string
		edits []textEdit
		want  string
	}{
		// gofmt は末尾に改行を置くので、改行の無い全文には改行を足す編集を返す。本体のバッファには空の行が 1 つ増える。
		{"add the final newline", "package a", []textEdit{te(0, 9, 0, 9, "\n")}, "package a\n"},
		{"replace the whole document", "a\nb", []textEdit{te(0, 0, 1, 1, "A\nB\n")}, "A\nB\n"},
		{"delete trailing empty lines", "a\n\n", []textEdit{te(0, 1, 2, 0, "\n")}, "a\n"},
		{"position beyond the end is clamped to the end", "a", []textEdit{te(1, 0, 1, 0, "b")}, "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := formattingTransaction(lines(strings.Split(tt.text, "\n")), false, 3, tt.edits, encUTF16)
			if tx == nil {
				t.Fatal("transaction = nil")
			}
			if got := applyEdits(tt.text, tx.Edits); got != tt.want {
				t.Errorf("result = %q, want %q (edits %+v)", got, tt.want, tx.Edits)
			}
		})
	}
}

// TestDocumentHistory は、document が過去の版の行を maxHistory 個まで覚え、古い版の診断の位置をその版の行で変換できることを確かめる。
//
// req: FR-30
func TestDocumentHistory(t *testing.T) {
	d := &document{history: []snapshot{{version: 1, lines: lines{"日本 ERR"}}}}
	// 版 2 で先頭に 𝄞 を挿入する。版 1 の診断（UTF-16 で 3 文字目から）は、版 1 の行で変換する。
	if _, err := d.apply([]ext.Edit{edit(0, 0, 0, 0, "𝄞")}, 2, encUTF16, false); err != nil {
		t.Fatal(err)
	}
	old, err := d.at(1)
	if err != nil {
		t.Fatal(err)
	}
	r := old.rangeFromLSP(lspRange{Start: lspPosition{Character: 3}, End: lspPosition{Character: 6}}, encUTF16)
	if r != (ext.Range{Start: pos(0, len("日本 ")), End: pos(0, len("日本 ERR"))}) {
		t.Errorf("range at version 1 = %+v", r)
	}
	for v := ext.Version(3); v < 3+maxHistory; v++ {
		if _, err := d.apply([]ext.Edit{edit(0, 0, 0, 0, "x")}, v, encUTF16, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.history) != maxHistory {
		t.Errorf("history length = %d, want %d", len(d.history), maxHistory)
	}
	if _, err := d.at(1); !errors.Is(err, errNoSnapshot) {
		t.Errorf("at(1) after %d versions = %v, want errNoSnapshot", maxHistory, err)
	}
	if _, err := d.apply([]ext.Edit{edit(5, 0, 5, 0, "x")}, 99, encUTF16, true); err == nil {
		t.Error("apply with an edit outside the document: err = nil")
	}
}

func TestParseSettings(t *testing.T) {
	c, err := parseSettings(nil)
	if err != nil || c.servers["go"] == nil || c.servers["go"].Command != "gopls" || c.maxRestarts != defaultMaxRestarts {
		t.Fatalf("default settings = %+v, %v", c, err)
	}
	c, err = parseSettings(json.RawMessage(`{"servers": {"go": null, "rust": {"command": "rust-analyzer"}}, "maxRestarts": -2}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.servers["go"] != nil || c.servers["rust"].LanguageID != "rust" || c.maxRestarts != 0 {
		t.Errorf("settings = %+v", c)
	}
	if !c.servers["rust"].formatOnSave() || c.servers["rust"].tabSize() != 4 {
		t.Errorf("rust defaults = %+v", c.servers["rust"])
	}
	if _, err := parseSettings(json.RawMessage(`{"servers": {"x": {}}}`)); err == nil {
		t.Error("empty command: err = nil")
	}
	if _, err := parseSettings(json.RawMessage(`[]`)); err == nil {
		t.Error("invalid settings: err = nil")
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	sub := dir + "/a/b"
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/a/go.mod", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findRoot(sub+"/x.go", []string{"go.mod"}); got != dir+"/a" {
		t.Errorf("findRoot = %q, want %q", got, dir+"/a")
	}
	if got := findRoot(sub+"/x.go", []string{"no-such-marker"}); got != sub {
		t.Errorf("findRoot without markers = %q, want %q", got, sub)
	}
}

func TestHoverText(t *testing.T) {
	tests := []struct {
		raw, text, format string
	}{
		{`{"kind": "plaintext", "value": "p"}`, "p", "plaintext"},
		{`{"kind": "markdown", "value": "**m**"}`, "**m**", "markdown"},
		{`"s"`, "s", "markdown"},
		{`{"language": "go", "value": "func f()"}`, "```go\nfunc f()\n```", "markdown"},
		{`["a", {"language": "go", "value": "x"}]`, "a\n\n```go\nx\n```", "markdown"},
		{`null`, "", ""},
	}
	for _, tt := range tests {
		text, format := hoverText(json.RawMessage(tt.raw))
		if text != tt.text || format != tt.format {
			t.Errorf("hoverText(%s) = %q, %q; want %q, %q", tt.raw, text, format, tt.text, tt.format)
		}
	}
}

func TestParseLocations(t *testing.T) {
	r := `{"start": {"line": 1, "character": 2}, "end": {"line": 1, "character": 3}}`
	sel := `{"start": {"line": 4, "character": 5}, "end": {"line": 4, "character": 6}}`
	tests := []struct {
		raw  string
		uris []string
		pos  []lspPosition
	}{
		{`null`, nil, nil},
		{`{"uri": "file:///a", "range": ` + r + `}`, []string{"file:///a"}, []lspPosition{{1, 2}}},
		{`[{"uri": "file:///a", "range": ` + r + `}, {"uri": "file:///b", "range": ` + r + `}]`, []string{"file:///a", "file:///b"}, []lspPosition{{1, 2}, {1, 2}}},
		{`[{"targetUri": "file:///c", "targetRange": ` + r + `, "targetSelectionRange": ` + sel + `}]`, []string{"file:///c"}, []lspPosition{{4, 5}}},
	}
	for _, tt := range tests {
		ls, err := parseLocations(json.RawMessage(tt.raw))
		if err != nil {
			t.Fatalf("parseLocations(%s): %v", tt.raw, err)
		}
		var uris []string
		var ps []lspPosition
		for _, l := range ls {
			u, p, ok := l.target()
			if !ok {
				t.Errorf("%s: target not found", tt.raw)
			}
			uris, ps = append(uris, u), append(ps, p)
		}
		if !slices.Equal(uris, tt.uris) || !slices.Equal(ps, tt.pos) {
			t.Errorf("parseLocations(%s) = %v %v", tt.raw, uris, ps)
		}
	}
}

func TestSyncOptions(t *testing.T) {
	tests := []struct {
		raw              string
		openClose        bool
		change           int
		save, includeTxt bool
	}{
		{``, false, syncNone, false, false},
		{`2`, true, syncIncremental, false, false},
		{`{"openClose": true, "change": 1, "save": true}`, true, syncFull, true, false},
		{`{"openClose": true, "change": 2, "save": {"includeText": true}}`, true, syncIncremental, true, true},
		{`{"change": 2}`, false, syncIncremental, false, false},
	}
	for _, tt := range tests {
		c := serverCapabilities{TextDocumentSync: json.RawMessage(tt.raw)}
		o, save, inc := c.syncOptions()
		if o.OpenClose != tt.openClose || o.Change != tt.change || save != tt.save || inc != tt.includeTxt {
			t.Errorf("syncOptions(%s) = %+v %v %v", tt.raw, o, save, inc)
		}
	}
}

func TestURI(t *testing.T) {
	for _, p := range []string{"/a/b.go", "/a b/日本.go", "/a/#x%.go"} {
		got, ok := uriToPath(pathToURI(p))
		if !ok || got != p {
			t.Errorf("uriToPath(pathToURI(%q)) = %q, %v", p, got, ok)
		}
	}
	if _, ok := uriToPath("untitled:1"); ok {
		t.Error("uriToPath(untitled:1): ok = true")
	}
}

func TestManifest(t *testing.T) {
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if warnings, verr := m.Validate(); verr != nil || len(warnings) > 0 {
		t.Fatalf("Validate = %v, %v", warnings, verr)
	}
	if m.Command != "./modal-editor-ext-lsp" || !m.SaveHook {
		t.Errorf("manifest = %+v", m)
	}
	// 段階 2 のコマンド（lsp.definition など）は、provides で名乗る本体の操作に置き換えた（#203）。
	if len(m.Contributes.Commands) != 0 {
		t.Errorf("commands = %v, want none", m.Contributes.Commands)
	}
	if !slices.Equal(m.Provides, providedOps) {
		t.Errorf("provides = %v, want %v", m.Provides, providedOps)
	}
	for ft := range defaultServers() {
		if !slices.Contains(m.Activation, ext.Activation{On: ext.ActivationFileType, Pattern: ft}) {
			t.Errorf("no filetype activation for the default server %q", ft)
		}
	}
}
