package main

import (
	"context"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// objectSource は、テキストオブジェクトの対象（関数、関数リテラル、メソッド、型、引数）を含む Go のソースである。
const objectSource = `package main

type Point struct {
	X, Y int
}

type (
	A int
	B interface {
		M()
	}
)

// add は和を返す。
func add(a int, b int) int {
	f := func(x int) int { return x + 1 }
	return f(a) + b
}

func (p Point) Sum(xs ...int) int {
	return p.X + call(1, 2,
		3,
	)
}

func empty() {}

func multi[T any, U comparable](t T) {
}`

// specByID は、textObjectSpecs から id の定義を引く。
func specByID(t *testing.T, id string) textObjectSpec {
	t.Helper()
	for _, s := range textObjectSpecs {
		if s.id == id {
			return s
		}
	}
	t.Fatalf("text object %q not found", id)
	return textObjectSpec{}
}

// cursorAt は、src の中で marker が最初に現れる位置を返す。
func cursorAt(t *testing.T, d *document, marker string) ext.Position {
	t.Helper()
	i := strings.Index(string(d.src), marker)
	if i < 0 {
		t.Fatalf("marker %q not found", marker)
	}
	return d.position(i)
}

// sliceRange は、範囲 r のテキストを返す。linewise なら開始の行の先頭から終わりの行の末尾までである。
func sliceRange(d *document, r ext.Range, linewise bool) string {
	if linewise {
		return string(d.src[d.lineStarts[r.Start.Line] : d.lineStarts[r.End.Line]+d.lineLen(r.End.Line)])
	}
	start, _ := d.offset(r.Start)
	end, _ := d.offset(r.End)
	return string(d.src[start:end])
}

// TestTextObjectGo は、Go のソースの各位置で、テキストオブジェクトの範囲と行単位かどうかが期待どおりであることを確かめる。
//
// req: FR-11, FR-35
func TestTextObjectGo(t *testing.T) {
	h := newTestHighlighter(t)
	d := parse(t, h, "go", objectSource)
	addFunc := "func add(a int, b int) int {\n\tf := func(x int) int { return x + 1 }\n\treturn f(a) + b\n}"
	tests := []struct {
		name     string
		id       string
		marker   string
		count    int
		ok       bool
		want     string
		linewise bool
	}{
		{"af: 関数の全体は行単位", "treesitter.function.around", "return f(a)", 1, true, addFunc, true},
		{"af: カーソルが func の上", "treesitter.function.around", "func add", 1, true, addFunc, true},
		{"af: 関数リテラルは文字単位", "treesitter.function.around", "x + 1", 1, true, "func(x int) int { return x + 1 }", false},
		{"2af: 外側の関数", "treesitter.function.around", "x + 1", 2, true, addFunc, true},
		{"3af: 外側が無い", "treesitter.function.around", "x + 1", 3, false, "", false},
		{"if: 1 行の本体は空白を除いた内側", "treesitter.function.inner", "x + 1", 1, true, "return x + 1", false},
		{"if: 複数行の本体は間の行", "treesitter.function.inner", "return f(a)", 1, true, "\tf := func(x int) int { return x + 1 }\n\treturn f(a) + b", true},
		{"if: 空の本体は幅 0", "treesitter.function.inner", "empty", 1, true, "", false},
		{"if: 間に行の無い複数行の本体は幅 0", "treesitter.function.inner", "comparable", 1, true, "", false},
		{"af: メソッド", "treesitter.function.around", "p.X", 1, true, "func (p Point) Sum(xs ...int) int {\n\treturn p.X + call(1, 2,\n\t\t3,\n\t)\n}", true},
		{"af: 関数の外", "treesitter.function.around", "package", 1, false, "", false},
		{"af: コメントは含めない", "treesitter.function.around", "和を返す", 1, false, "", false},
		{"ac: 構造体の宣言の全体", "treesitter.class.around", "X, Y", 1, true, "type Point struct {\n\tX, Y int\n}", true},
		{"ic: 構造体の内側", "treesitter.class.inner", "X, Y", 1, true, "\tX, Y int", true},
		{"ac: まとめた宣言の中の型", "treesitter.class.around", "A int", 1, true, "\tA int", true},
		{"ic: 構造体でない型", "treesitter.class.inner", "A int", 1, true, "int", false},
		{"ac: まとめた宣言の中のインターフェース", "treesitter.class.around", "M()", 1, true, "\tB interface {\n\t\tM()\n\t}", true},
		{"ic: インターフェースの内側", "treesitter.class.inner", "M()", 1, true, "\t\tM()", true},
		{"ia: 引数", "treesitter.parameter.inner", "b int", 1, true, "b int", false},
		{"aa: 最初の引数は後ろの区切りまで", "treesitter.parameter.around", "a int", 1, true, "a int, ", false},
		{"aa: 最後の引数は前の区切りから", "treesitter.parameter.around", "b int", 1, true, ", b int", false},
		{"ia: 可変長の引数", "treesitter.parameter.inner", "xs", 1, true, "xs ...int", false},
		{"aa: 呼び出しの引数は次の行の要素の直前まで", "treesitter.parameter.around", "2,", 1, true, "2,\n\t\t", false},
		{"aa: 複数行の最後の引数は後ろの区切りまでで、行を占めれば行単位", "treesitter.parameter.around", "3,", 1, true, "\t\t3,", true},
		{"ia: 後ろに区切りのある引数は文字単位", "treesitter.parameter.inner", "3,", 1, true, "3", false},
		{"aa: 区切りの上のカーソル", "treesitter.parameter.around", ", 2", 1, true, "1, ", false},
		{"2ia: 外側に並びが無い", "treesitter.parameter.inner", "a) + b", 2, false, "", false},
		{"ia: 型引数", "treesitter.parameter.inner", "U comparable", 1, true, "U comparable", false},
		{"aa: 型引数", "treesitter.parameter.around", "T any", 1, true, "T any, ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, linewise, ok, err := d.textObject(specByID(t, tt.id), cursorAt(t, d, tt.marker), tt.count)
			if err != nil {
				t.Fatalf("textObject: %v", err)
			}
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v (range %+v)", ok, tt.ok, r)
			}
			if !ok {
				return
			}
			if got := sliceRange(d, r, linewise); got != tt.want || linewise != tt.linewise {
				t.Errorf("got %q linewise %v, want %q linewise %v", got, linewise, tt.want, tt.linewise)
			}
		})
	}
}

// TestServeTextObject は、本体の役から textObject/resolve を JSON-RPC で送り、
// 編集の後のバージョンでも、構文木を持たない（解析の前の）バージョンでも、そのバージョンのテキストで範囲が返ることを確かめる。
//
// req: FR-11, FR-35
func TestServeTextObject(t *testing.T) {
	conn, got := connect(t)
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, FileType: "go", Text: objectSource}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 2, Version: 1, FileType: "json", Text: `{"a": [1, 2]}`}); err != nil {
		t.Fatal(err)
	}
	h := newTestHighlighter(t)
	d := parse(t, h, "go", objectSource)
	resolve := func(buf ext.BufferID, version ext.Version, id string, cursor ext.Position) ext.TextObjectResolveResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		var r ext.TextObjectResolveResult
		if err := conn.Call(ctx, ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: buf, Version: version, ID: id, Cursor: cursor}, &r); err != nil {
			t.Fatalf("textObject/resolve: %v", err)
		}
		return r
	}
	// 一覧が届く前（構文木を持たないかもしれない）と後の両方で、同じ範囲が返る。
	cursor := cursorAt(t, d, "x + 1")
	want := ext.TextObjectResolveResult{OK: true, Version: 1, Range: ext.Range{Start: cursorAt(t, d, "func(x"), End: cursorAt(t, d, "\n\treturn f(a)")}}
	if r := resolve(1, 1, "treesitter.function.around", cursor); r != want {
		t.Errorf("before decoration/set: %+v, want %+v", r, want)
	}
	receive(t, got)
	receive(t, got)
	if r := resolve(1, 1, "treesitter.function.around", cursor); r != want {
		t.Errorf("after decoration/set: %+v, want %+v", r, want)
	}
	// 1 行目に行を足すと、範囲は 1 行下がる。
	edit := ext.Edit{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 12}, End: ext.Position{Line: 0, Byte: 12}}, Text: "\n"}
	if err := conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{Buffer: 1, FromVersion: 1, ToVersion: 2, Edits: []ext.Edit{edit}}); err != nil {
		t.Fatal(err)
	}
	down := func(p ext.Position) ext.Position { return ext.Position{Line: p.Line + 1, Byte: p.Byte} }
	want2 := ext.TextObjectResolveResult{OK: true, Version: 2, Range: ext.Range{Start: down(want.Range.Start), End: down(want.Range.End)}}
	if r := resolve(1, 2, "treesitter.function.around", down(cursor)); r != want2 {
		t.Errorf("after change: %+v, want %+v", r, want2)
	}
	// テキストオブジェクトを持たない言語では失敗する。
	if r := resolve(2, 1, "treesitter.function.around", ext.Position{Line: 0, Byte: 8}); r.OK {
		t.Errorf("json: %+v, want ok false", r)
	}
}

// TestParseObjectCapture は、キャプチャ名が対象と部分に分かれ、知らない名前は対象にならないことを確かめる。
func TestParseObjectCapture(t *testing.T) {
	tests := []struct {
		name string
		want objectCapture
	}{
		{"function.outer", objectCapture{objectFunction, partOuter}},
		{"class.inner", objectCapture{objectClass, partInner}},
		{"parameter.inner", objectCapture{objectParameter, partInner}},
		{"function", objectCapture{}},
		{"loop.outer", objectCapture{}},
		{"function.body", objectCapture{}},
	}
	for _, tt := range tests {
		if got := parseObjectCapture(tt.name); got != tt.want {
			t.Errorf("parseObjectCapture(%q) = %+v, want %+v", tt.name, got, tt.want)
		}
	}
	// 書いたクエリのキャプチャ名は、どれも対象と部分に分かれる。
	h := newTestHighlighter(t)
	for _, l := range h.langs {
		if l.objects == nil {
			continue
		}
		for i, n := range l.objects.CaptureNames() {
			if l.objectCaptures[i].part == partNone {
				t.Errorf("%s: capture @%s is not a text object", l.fileType, n)
			}
		}
	}
}
