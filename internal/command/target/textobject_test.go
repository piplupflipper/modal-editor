package target

import (
	"errors"
	"testing"
)

// textObjectCase は、テキストオブジェクト 1 回の入力と期待する結果である。
type textObjectCase struct {
	obj   string
	text  string // "|" でカーソルの位置を示す
	count int
	want  string // 範囲のテキスト（Span.Range で切り出したもの）
	kind  Kind
	fail  bool // ErrNoTarget を期待する
}

// textObjectTests は、TestTextObjects の表である。
var textObjectTests = []textObjectCase{
	// iw aw iW aW
	{obj: "iw", text: "foo b|ar baz", want: "bar", kind: Inclusive},
	{obj: "iw", text: "|foo", want: "foo", kind: Inclusive},
	{obj: "iw", text: "fo|o", want: "foo", kind: Inclusive},
	{obj: "iw", text: "foo|.bar", want: ".", kind: Inclusive},
	{obj: "iw", text: "foo  |  bar", want: "    ", kind: Inclusive},
	{obj: "iw", text: "|foo bar", count: 2, want: "foo ", kind: Inclusive},
	{obj: "iw", text: "|foo bar", count: 3, want: "foo bar", kind: Inclusive},
	{obj: "iw", text: "x 日本|語 y", want: "日本語", kind: Inclusive},
	{obj: "iw", text: "|", want: "", kind: Inclusive},
	{obj: "iw", text: "a\n|\nb", want: "", kind: Inclusive},
	{obj: "iw", text: "|foo", count: 3, fail: true},
	{obj: "iW", text: "a foo.b|ar c", want: "foo.bar", kind: Inclusive},
	{obj: "aw", text: "foo b|ar baz", want: "bar ", kind: Inclusive},
	{obj: "aw", text: "foo b|ar", want: " bar", kind: Inclusive}, // 後に空白が無ければ前の空白
	{obj: "aw", text: "  f|oo", want: "foo", kind: Inclusive},    // 字下げは含めない
	{obj: "aw", text: "foo| bar baz", want: " bar", kind: Inclusive},
	{obj: "aw", text: "|a b c", count: 2, want: "a b ", kind: Inclusive},
	{obj: "aw", text: "|foo\nbar", want: "foo", kind: Inclusive},
	{obj: "aw", text: "x 日|本 y", want: "日本 ", kind: Inclusive},
	{obj: "aW", text: "x a.|b y", want: "a.b ", kind: Inclusive},
	{obj: "aw", text: "a b|.", want: ".", kind: Inclusive},
	{obj: "aw", text: "|a b\nc", count: 2, want: "a b", kind: Inclusive},
	{obj: "iw", text: "|a b\nc", count: 4, want: "a b\nc", kind: Inclusive}, // 改行も 1 つと数える

	// i" a" i' a'
	{obj: `i"`, text: `x = "a|bc" y`, want: "abc", kind: Exclusive},
	{obj: `a"`, text: `x = "a|bc" y`, want: `"abc" `, kind: Exclusive},
	{obj: `a"`, text: `x = "a|bc"`, want: ` "abc"`, kind: Exclusive},
	{obj: `i"`, text: `"ab" |"cd"`, want: "cd", kind: Exclusive},    // 引用符の上は行の先頭から対を数える
	{obj: `i"`, text: `"ab|" "cd"`, want: "ab", kind: Exclusive},    // 閉じる引用符の上
	{obj: `i"`, text: `"ab" |x "cd"`, want: " x ", kind: Exclusive}, // 2 つの文字列の間
	{obj: `i"`, text: `"a\"|b"`, want: `a\"b`, kind: Exclusive},
	{obj: `i"`, text: `x| "ab"`, want: "ab", kind: Exclusive}, // 前に無ければ後ろの対
	{obj: `i"`, text: `"|"`, want: "", kind: Exclusive},
	{obj: `i"`, text: `"あ|い"`, want: "あい", kind: Exclusive},
	{obj: `i"`, text: `"a|b"`, count: 2, want: `"ab"`, kind: Exclusive},
	{obj: `i"`, text: "a|bc", fail: true},
	{obj: `i"`, text: `"a|bc`, fail: true},
	{obj: `i"`, text: "\"a\n|b\"", fail: true}, // 行をまたがない
	{obj: `i"`, text: "|", fail: true},
	{obj: "i'", text: "'x|y'", want: "xy", kind: Exclusive},
	{obj: "a'", text: "f('x|y')", want: "'xy'", kind: Exclusive},

	// i( a( ib ab i[ a[ i{ a{ iB aB
	{obj: "i(", text: "f(a, |b)", want: "a, b", kind: Inclusive},
	{obj: "a(", text: "f(a, |b)", want: "(a, b)", kind: Inclusive},
	{obj: "i(", text: "f|(ab)", want: "ab", kind: Inclusive},
	{obj: "i(", text: "f(ab|)", want: "ab", kind: Inclusive},
	{obj: "i)", text: "f(a|b)", want: "ab", kind: Inclusive},
	{obj: "ib", text: "f(a|b)", want: "ab", kind: Inclusive},
	{obj: "ab", text: "f(a|b)", want: "(ab)", kind: Inclusive},
	{obj: "i(", text: "((a|))", count: 2, want: "(a)", kind: Inclusive},
	{obj: "a(", text: "((a|))", count: 2, want: "((a))", kind: Inclusive},
	{obj: "i(", text: "(|)", want: "", kind: Exclusive},
	{obj: "i(", text: "（(あ|い)）", want: "あい", kind: Inclusive},
	{obj: "i(", text: "(a\n|b)", want: "a\nb", kind: Inclusive},
	{obj: "i(", text: "((a|))", count: 3, fail: true},
	// 囲む対が無ければ、後ろの最初の対（Vim 9.1）
	{obj: "i(", text: "|f(a, (b, c), d)", want: "a, (b, c), d", kind: Inclusive},
	{obj: "i[", text: "|x [a] y", want: "a", kind: Inclusive},
	{obj: "a{", text: "|x {a} y", want: "{a}", kind: Inclusive},
	{obj: "i(", text: "(a) |x (b)", want: "b", kind: Inclusive},
	{obj: "i(", text: "|x\n(a)", want: "a", kind: Inclusive},
	{obj: "i(", text: "|x (\na\n) y", want: "a\n", kind: Exclusive},
	{obj: "i(", text: "|x (a (b))", count: 2, want: "b", kind: Inclusive},
	{obj: "i(", text: "|x (a) y", count: 2, fail: true},
	{obj: "i(", text: "(a) |x", fail: true},
	// エスケープした括弧は数えない
	{obj: "i(", text: "f(a \\) |b)", want: "a \\) b", kind: Inclusive},
	{obj: "i[", text: "[a \\]| b]", want: "a \\] b", kind: Inclusive},
	{obj: "i(", text: "f(a \\\\) |b)", fail: true}, // バックスラッシュが 2 つならエスケープではない
	{obj: "i(", text: "a|b", fail: true},
	{obj: "i(", text: "(a|b", fail: true},
	{obj: "i(", text: "|", fail: true},
	{obj: "i[", text: "[1, |2]", want: "1, 2", kind: Inclusive},
	{obj: "a]", text: "x[1|]", want: "[1]", kind: Inclusive},
	{obj: "i{", text: "f {\n  a|b\n}", want: "  ab\n", kind: Exclusive}, // AdjustExclusive で行単位になる
	{obj: "i{", text: "{\n|  x\n}", want: "  x\n", kind: Exclusive},     // 字下げの中から
	{obj: "iB", text: "{\n  x\n  |}", want: "  x\n", kind: Exclusive},   // 閉じ括弧の前の字下げは含めない
	{obj: "a{", text: "f {\n  a|b\n}", want: "{\n  ab\n}", kind: Inclusive},
	{obj: "aB", text: "{a|}", want: "{a}", kind: Inclusive},
	{obj: "i(", text: "(a|\n  )", want: "a\n", kind: Exclusive}, // 閉じ括弧の前の字下げを含めない
	{obj: "i(", text: "(\n|a)", want: "a", kind: Inclusive},     // 開き括弧が行末なら次の行から

	// ip ap
	{obj: "ip", text: "|a\nb\n\n\nc\nd", want: "a\nb", kind: Linewise},
	{obj: "ip", text: "a\n|b\n\n\nc\nd", want: "a\nb", kind: Linewise},
	{obj: "ip", text: "a\nb\n|\n\nc\nd", want: "\n", kind: Linewise}, // 白い行の並び
	{obj: "ip", text: "|a\nb\n\n\nc\nd", count: 2, want: "a\nb\n\n", kind: Linewise},
	{obj: "ip", text: "|a\nb\n\n\nc\nd", count: 3, want: "a\nb\n\n\nc\nd", kind: Linewise},
	{obj: "ip", text: "|a\nb\n\n\nc\nd", count: 4, fail: true},
	{obj: "ip", text: "|", want: "", kind: Linewise},
	{obj: "ap", text: "|a\nb\n\n\nc\nd", want: "a\nb\n\n", kind: Linewise},
	{obj: "ap", text: "a\nb\n|\n\nc\nd", want: "\n\nc\nd", kind: Linewise},
	{obj: "ap", text: "a\nb\n\n\n|c\nd", want: "\n\nc\nd", kind: Linewise}, // 後に白い行が無ければ前の白い行
	{obj: "ap", text: "|a\n  \nb", want: "a\n  ", kind: Linewise},          // 空白だけの行も白い行
	{obj: "ap", text: "|a\n\nb\n\nc", count: 2, want: "a\n\nb\n", kind: Linewise},
	{obj: "ap", text: "|", fail: true},
	{obj: "ip", text: "|あ\nい\n\nう", want: "あ\nい", kind: Linewise},
}

// req: FR-03
func TestTextObjects(t *testing.T) {
	for _, tt := range textObjectTests {
		b, cur := parse(t, tt.text)
		f, ok := LookupTextObject(textObjectID(t, tt.obj))
		if !ok {
			t.Fatalf("text object %q not found", tt.obj)
		}
		n, given := count(tt.count)
		span, err := f(b, Request{Cursor: cur, Count: n, CountGiven: given})
		if tt.fail {
			if !errors.Is(err, ErrNoTarget) {
				t.Errorf("%d%s on %q: got %+v, %v, want ErrNoTarget", tt.count, tt.obj, tt.text, span, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%d%s on %q: error = %v", tt.count, tt.obj, tt.text, err)
			continue
		}
		got, err := b.Slice(span.Range(b))
		if err != nil || got != tt.want || span.Kind != tt.kind {
			t.Errorf("%d%s on %q = %q (%v), %v, want %q (%v)", tt.count, tt.obj, tt.text, got, span.Kind, err, tt.want, tt.kind)
		}
	}
}

// req: FR-03
func TestTextObjectAdjust(t *testing.T) {
	// i{ の範囲をオペレーターに渡すと、括弧の行を残して中の行だけを行単位で扱う。
	b, cur := parse(t, "f {\n  a|b\n  c\n}")
	f, _ := LookupTextObject("inner-brace")
	span, err := f(b, Request{Cursor: cur})
	if err != nil {
		t.Fatal(err)
	}
	got := AdjustExclusive(b, span)
	want := Span{Start: pos(1, 0), End: pos(2, 0), Kind: Linewise}
	if got != want {
		t.Errorf("AdjustExclusive(i{) = %+v, want %+v", got, want)
	}
}
