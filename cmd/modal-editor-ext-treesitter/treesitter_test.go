package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

const testTimeout = 5 * time.Second

// goSource は、Go の文法の主な要素を含むソースである。
// 行 4 に多バイト文字を置き、位置がバイト数で数えられることを確かめる。
const goSource = `package main

import "fmt"

// 挨拶を返す
func greet(name string) string {
	var n int = 42
	if name == "" && n > 0 {
		return "a\tb"
	}
	x := len(name)
	fmt.Println(x, nil, true)
	return name
}`

// newTestHighlighter は、言語の表を用意した highlighter を作る。document を直接作って試すためのもので、Server は動かさない。
func newTestHighlighter(t *testing.T) *highlighter {
	t.Helper()
	h, err := newHighlighter()
	if err != nil {
		t.Fatalf("newHighlighter: %v", err)
	}
	t.Cleanup(h.close)
	return h
}

// parse は、言語 fileType で text を全文から解析した document を返す。
func parse(t *testing.T, h *highlighter, fileType, text string) *document {
	t.Helper()
	d := newDocument(h.langs[fileType], text)
	t.Cleanup(d.close)
	return d
}

// styleOf は、text の中で n 番目（0 始まり）に現れる token をちょうど覆う範囲の色を返す。そのような範囲が無ければ空である。
func styleOf(t *testing.T, d *document, decs []ext.Decoration, token string, n int) string {
	t.Helper()
	off := -1
	for range n + 1 {
		i := strings.Index(string(d.src[off+1:]), token)
		if i < 0 {
			t.Fatalf("token %q #%d not found", token, n)
		}
		off += 1 + i
	}
	want := ext.Range{Start: d.position(off), End: d.position(off + len(token))}
	for _, dec := range decs {
		if dec.Range == want {
			return dec.Style
		}
	}
	return ""
}

// TestDecorationsGo は、Go のソースの各要素に 6.1 の表の論理名の範囲が付くことを確かめる。
//
// req: FR-35
func TestDecorationsGo(t *testing.T) {
	h := newTestHighlighter(t)
	d := parse(t, h, "go", goSource)
	decs := d.decorations()
	tests := []struct {
		token string
		n     int
		want  string
	}{
		{"package", 0, "keyword"},
		{"import", 0, "keyword"},
		{`"fmt"`, 0, "string"},
		{"// 挨拶を返す", 0, "comment"},
		{"func", 0, "keyword"},
		{"greet", 0, "function"},
		{"name", 0, "variable"},
		{"string", 0, "type"},
		{"var", 0, "keyword"},
		{"int", 0, "type"},
		{"=", 0, "operator"},
		{"42", 0, "number"},
		{"==", 0, "operator"},
		{`""`, 0, "string"},
		{"&&", 0, "operator"},
		{"return", 0, "keyword"},
		{":=", 0, "operator"},
		{"len", 0, "function"},     // function.builtin
		{"Println", 0, "function"}, // function.method
		{"fmt", 1, "variable"},
		{"nil", 0, "constant"},  // constant.builtin
		{"true", 0, "constant"}, // constant.builtin
	}
	for _, tt := range tests {
		if got := styleOf(t, d, decs, tt.token, tt.n); got != tt.want {
			t.Errorf("%q #%d: style = %q, want %q", tt.token, tt.n, got, tt.want)
		}
	}
	// 文字列の中のエスケープは文字列の色でつながり、"a\tb" 全体が 1 つの範囲になる。
	if got := styleOf(t, d, decs, `"a\tb"`, 0); got != "string" {
		t.Errorf(`"a\tb": style = %q, want "string"`, got)
	}
	// 多バイト文字を含むコメントの範囲は、バイト数で数えた行末までである。
	comment := ext.Range{Start: ext.Position{Line: 4, Byte: 0}, End: ext.Position{Line: 4, Byte: len("// 挨拶を返す")}}
	if !slices.Contains(decs, ext.Decoration{Range: comment, Style: "comment"}) {
		t.Errorf("comment range %+v not found in %+v", comment, decs)
	}
	checkWellFormed(t, d, decs)
}

// TestDecorationsJSON は、JSON の各要素に論理名の範囲が付くことを確かめる。
//
// req: FR-35
func TestDecorationsJSON(t *testing.T) {
	h := newTestHighlighter(t)
	d := parse(t, h, "json", `{"a": [1, -2.5e3, "x\n", true, null]}`)
	decs := d.decorations()
	tests := []struct {
		token string
		want  string
	}{
		{`"a"`, "string"}, // string.special.key
		{"1", "number"},
		{"-2.5e3", "number"},
		{`"x\n"`, "string"},
		{"true", "constant"},
		{"null", "constant"},
	}
	for _, tt := range tests {
		if got := styleOf(t, d, decs, tt.token, 0); got != tt.want {
			t.Errorf("%q: style = %q, want %q", tt.token, got, tt.want)
		}
	}
	checkWellFormed(t, d, decs)
}

// checkWellFormed は、一覧の範囲が空でなく、互いに重ならず先頭から順に並び、名前が 6.1 の表にあることを確かめる。
func checkWellFormed(t *testing.T, d *document, decs []ext.Decoration) {
	t.Helper()
	prevEnd := 0
	for i, dec := range decs {
		s, err := d.offset(dec.Range.Start)
		if err != nil {
			t.Fatalf("decs[%d]: start: %v", i, err)
		}
		e, err := d.offset(dec.Range.End)
		if err != nil {
			t.Fatalf("decs[%d]: end: %v", i, err)
		}
		if s >= e || s < prevEnd {
			t.Fatalf("decs[%d] = %+v: empty, or overlaps or precedes the previous range", i, dec)
		}
		if _, ok := styleByName(dec.Style); !ok {
			t.Errorf("decs[%d]: style %q is not in the table", i, dec.Style)
		}
		prevEnd = e
	}
}

// TestStyleFor は、キャプチャ名から論理名への対応を確かめる。
func TestStyleFor(t *testing.T) {
	tests := map[string]string{
		"comment":            "comment",
		"keyword":            "keyword",
		"function.method":    "function",
		"function.builtin":   "function",
		"constant.builtin":   "constant",
		"string.special.key": "string",
		"escape":             "string",
		"punctuation.delim":  "punctuation",
		"property":           "",
		"variable.parameter": "variable",
		"embedded":           "",
	}
	for capture, want := range tests {
		if got := styleFor(capture).String(); got != want {
			t.Errorf("styleFor(%q) = %q, want %q", capture, got, want)
		}
	}
}

// TestQueriesMapCaptures は、公式のクエリのキャプチャ名のうち、色を付けないものが想定のものだけであることを確かめる。
// 文法の版を上げてキャプチャ名が変わったときに気付くためである。
func TestQueriesMapCaptures(t *testing.T) {
	h := newTestHighlighter(t)
	want := map[string][]string{"go": {"property"}, "json": nil}
	for ft, l := range h.langs {
		var unmapped []string
		for i, n := range l.query.CaptureNames() {
			if l.styles[i] == noStyle {
				unmapped = append(unmapped, n)
			}
		}
		if !slices.Equal(unmapped, want[ft]) {
			t.Errorf("%s: unmapped captures = %v, want %v", ft, unmapped, want[ft])
		}
	}
}

// applyText は、逐次の数え方の編集を、行の列に素朴に適用した全文を返す（検証用の参照実装）。
func applyText(text string, edits []ext.Edit) string {
	lines := strings.Split(text, "\n")
	for _, e := range edits {
		s, en := e.Range.Start, e.Range.End
		joined := lines[s.Line][:s.Byte] + e.Text + lines[en.Line][en.Byte:]
		lines = slices.Replace(lines, s.Line, en.Line+1, strings.Split(joined, "\n")...)
	}
	return strings.Join(lines, "\n")
}

// randomPosition は、text の中の、ASCII の文字の境界の位置を 1 つ選ぶ。多バイト文字の途中は避ける。
func randomPosition(r *rand.Rand, text string) ext.Position {
	lines := strings.Split(text, "\n")
	for {
		l := r.IntN(len(lines))
		b := r.IntN(len(lines[l]) + 1)
		if b == len(lines[l]) || lines[l][b] < 0x80 {
			return ext.Position{Line: l, Byte: b}
		}
	}
}

func lessPos(p, q ext.Position) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Byte < q.Byte)
}

// fragments は、無作為な編集で挿入するテキストの候補である。構文を壊すものと直すものを混ぜる。
var fragments = []string{"", "x", "\n", "{", "}", "(", ")", `"`, "`", "/*", "*/", "// c\n", "func f() {\n\treturn 1\n}\n", "\t", "日本", ":= 3", "if a {", "\n\n"}

// TestIncrementalMatchesFull は、buffer/change の編集を増分で適用して解析し直した結果が、
// 編集後の全文を全文から解析し直した結果と、構文木も色の一覧も一致することを確かめる。
// 1 回の buffer/change に複数の編集を逐次の数え方で並べたものも含める。
//
// req: FR-35
func TestIncrementalMatchesFull(t *testing.T) {
	h := newTestHighlighter(t)
	r := rand.New(rand.NewPCG(46, 1)) //nolint:gosec // 再現できる無作為な編集の列を作るためで、暗号の用途ではない
	for _, ft := range []string{"go", "json"} {
		text := goSource
		if ft == "json" {
			text = `{"a": [1, 2, {"b": "c"}], "d": null}`
		}
		inc := parse(t, h, ft, text)
		for step := range 300 {
			edits := make([]ext.Edit, 1+r.IntN(3))
			cur := text
			for i := range edits {
				a, b := randomPosition(r, cur), randomPosition(r, cur)
				if lessPos(b, a) {
					a, b = b, a
				}
				if r.IntN(2) == 0 {
					b = a // 挿入だけ
				}
				edits[i] = ext.Edit{Range: ext.Range{Start: a, End: b}, Text: fragments[r.IntN(len(fragments))]}
				cur = applyText(cur, edits[i:i+1])
			}
			if err := inc.applyEdits(edits); err != nil {
				t.Fatalf("%s step %d: applyEdits(%+v): %v", ft, step, edits, err)
			}
			text = cur
			if string(inc.src) != text {
				t.Fatalf("%s step %d: text = %q, want %q", ft, step, inc.src, text)
			}
			full := newDocument(h.langs[ft], text)
			if g, w := dumpTree(inc.tree), dumpTree(full.tree); g != w {
				t.Fatalf("%s step %d: tree differs after %+v\n got: %s\nwant: %s", ft, step, edits, g, w)
			}
			if g, w := inc.decorations(), full.decorations(); !slices.Equal(g, w) {
				t.Fatalf("%s step %d: decorations differ after %+v\n got: %+v\nwant: %+v", ft, step, edits, g, w)
			}
			full.close()
		}
	}
}

// dumpTree は、構文木のすべてのノードの種類と、バイト位置と行・列の範囲を、深さ優先の順に並べた文字列を返す。
// ToSexp は位置を含まないので、Tree.Edit に渡す行・列の誤りを見つけるために位置も並べる。
func dumpTree(tree *sitter.Tree) string {
	var sb strings.Builder
	c := tree.Walk()
	defer c.Close()
	for {
		n := c.Node()
		fmt.Fprintf(&sb, "%s[%d-%d %v-%v]", n.Kind(), n.StartByte(), n.EndByte(), n.StartPosition(), n.EndPosition())
		if n.IsMissing() {
			sb.WriteString("MISSING")
		}
		if c.GotoFirstChild() {
			sb.WriteByte('(')
			continue
		}
		for !c.GotoNextSibling() {
			if !c.GotoParent() {
				return sb.String()
			}
			sb.WriteByte(')')
		}
		sb.WriteByte(' ')
	}
}

// TestApplyEditsOutOfRange は、全文の外を指す編集を誤りにすることを確かめる。
func TestApplyEditsOutOfRange(t *testing.T) {
	h := newTestHighlighter(t)
	for _, e := range []ext.Edit{
		{Range: ext.Range{Start: ext.Position{Line: 5, Byte: 0}, End: ext.Position{Line: 5, Byte: 0}}},
		{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 4}, End: ext.Position{Line: 0, Byte: 4}}},
		{Range: ext.Range{Start: ext.Position{Line: 1, Byte: 0}, End: ext.Position{Line: 0, Byte: 0}}},
	} {
		d := parse(t, h, "json", "[1,\n2]")
		if err := d.applyEdits([]ext.Edit{e}); err == nil {
			t.Errorf("applyEdits(%+v) = nil, want error", e)
		}
	}
}

// connect は、highlighter の Server を io.Pipe の上で動かし、本体の役の接続と、受け取った decoration/set の通り道を返す。
func connect(t *testing.T) (*rpc.Conn, <-chan ext.DecorationSetParams) {
	t.Helper()
	h, err := newHighlighter()
	if err != nil {
		t.Fatalf("newHighlighter: %v", err)
	}
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	served := make(chan error, 1)
	s := h.server()
	go func() { served <- s.Serve(context.Background(), extR, extW) }()
	got := make(chan ext.DecorationSetParams, 16)
	conn := rpc.NewConn(hostR, hostW, rpc.HandlerFuncs{
		Notification: func(_ context.Context, n *rpc.Notification) {
			if n.Method != ext.MethodDecorationSet {
				return
			}
			var p ext.DecorationSetParams
			if err := json.Unmarshal(n.Params, &p); err != nil {
				t.Errorf("decoration/set: %v", err)
				return
			}
			got <- p
		},
	})
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve = %v, want nil", err)
			}
		case <-time.After(testTimeout):
			t.Error("Serve did not return after the host closed")
		}
		h.close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var init ext.InitializeResult
	if err := conn.Call(ctx, ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion}, &init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return conn, got
}

func receive(t *testing.T, ch <-chan ext.DecorationSetParams) ext.DecorationSetParams {
	t.Helper()
	select {
	case p := <-ch:
		return p
	case <-time.After(testTimeout):
		t.Fatal("decoration/set did not arrive")
		return ext.DecorationSetParams{}
	}
}

// TestServeOpenAndChange は、本体の役から buffer/open と buffer/change を JSON-RPC で送り、
// それぞれのバージョンの decoration/set が届き、差分の後の一覧が全文から解析した一覧と一致することを確かめる。
//
// req: FR-35
func TestServeOpenAndChange(t *testing.T) {
	conn, got := connect(t)
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, FileType: "go", Text: goSource}); err != nil {
		t.Fatal(err)
	}
	p := receive(t, got)
	h := newTestHighlighter(t)
	if want := parse(t, h, "go", goSource).decorations(); p.Buffer != 1 || p.Version != 1 || !slices.Equal(p.Decorations, want) {
		t.Fatalf("open: got buffer %d version %d, decorations equal = %v", p.Buffer, p.Version, slices.Equal(p.Decorations, want))
	}
	// 1 回の buffer/change で、行 6 の 42 を 7 に、続けて（逐次の数え方で）行 0 の後ろにコメントの行を足す。
	edits := []ext.Edit{
		{Range: ext.Range{Start: ext.Position{Line: 6, Byte: 13}, End: ext.Position{Line: 6, Byte: 15}}, Text: "7"},
		{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 12}, End: ext.Position{Line: 0, Byte: 12}}, Text: "\n/* x */"},
	}
	if err := conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{Buffer: 1, FromVersion: 1, ToVersion: 2, Edits: edits}); err != nil {
		t.Fatal(err)
	}
	p = receive(t, got)
	after := applyText(goSource, edits)
	if !strings.Contains(after, "var n int = 7") {
		t.Fatalf("test edit is wrong: %q", after)
	}
	want := parse(t, h, "go", after).decorations()
	if p.Version != 2 || !slices.Equal(p.Decorations, want) {
		t.Errorf("change: version %d, decorations %+v, want version 2, %+v", p.Version, p.Decorations, want)
	}
	if !slices.Contains(p.Decorations, ext.Decoration{Range: ext.Range{Start: ext.Position{Line: 1, Byte: 0}, End: ext.Position{Line: 1, Byte: 7}}, Style: "comment"}) {
		t.Errorf("inserted comment is not decorated: %+v", p.Decorations)
	}
}

// TestServeFileTypes は、文法を持たない種類のファイルでは一覧を送らず、
// 送り直しで種類が文法を持たないものに変わったら空の一覧を送って色を消すことを確かめる。
func TestServeFileTypes(t *testing.T) {
	conn, got := connect(t)
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, FileType: "markdown", Text: "# a"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 2, Version: 1, FileType: "json", Text: "[1]"}); err != nil {
		t.Fatal(err)
	}
	// 通知は順に処理されるので、最初に届くのはバッファ 2 の一覧である。
	if p := receive(t, got); p.Buffer != 2 || len(p.Decorations) != 1 {
		t.Fatalf("first decoration/set = %+v, want buffer 2 with 1 decoration", p)
	}
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 2, Version: 2, FileType: "", Text: "[1]"}); err != nil {
		t.Fatal(err)
	}
	if p := receive(t, got); p.Buffer != 2 || p.Version != 2 || p.Decorations == nil || len(p.Decorations) != 0 {
		t.Fatalf("decoration/set after file type change = %+v, want an empty list for buffer 2 version 2", p)
	}
}

// TestManifest は、宣言ファイルが検査を通り、filetype の契機が languages と一致することを確かめる。
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
		t.Fatalf("Validate = %v, %v; want no warnings and no error", warnings, verr)
	}
	if m.Command != "./modal-editor-ext-treesitter" {
		t.Errorf("command = %q, want ./modal-editor-ext-treesitter", m.Command)
	}
	var patterns, want []string
	for _, a := range m.Activation {
		if a.On == ext.ActivationFileType {
			patterns = append(patterns, a.Pattern)
		}
	}
	for _, l := range languages {
		want = append(want, l.fileType)
	}
	slices.Sort(patterns)
	slices.Sort(want)
	if !slices.Equal(patterns, want) {
		t.Errorf("filetype patterns = %v, want %v", patterns, want)
	}
}
