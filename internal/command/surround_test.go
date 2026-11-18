package command

import (
	"fmt"
	"testing"
)

// surroundCase は、囲みの操作を vim-sandwich と突き合わせたケースである。
// text の "|" はカーソルで、want と cursor（行:バイト）は入力の後のバッファとカーソルである。
type surroundCase struct {
	text, keys   string
	want, cursor string
}

// sandwichCases は、Neovim 0.12.5 を `nvim --clean --headless` で起動し、runtimepath に vim-sandwich だけを足して
// 取った期待値である。ケースごとに起動し直し、setline() の後でアンドゥの履歴を消してから、keys を feedkeys(keys, "xt") で入力した。
// どのケースも、入力の後はノーマルモードで、無名のレジスターは変わらない。
var sandwichCases = []surroundCase{
	// sa（FR-73）。開きと閉じのどちらの文字でも同じ組で、空白は足さない。カーソルは囲んだテキストの頭へ移る。
	{"foo b|ar", "saiw(", "foo (bar)", "0:5"},
	{"foo b|ar", "saiw)", "foo (bar)", "0:5"},
	{"foo b|ar", "saiw[", "foo [bar]", "0:5"},
	{"foo b|ar", "saiw]", "foo [bar]", "0:5"},
	{"foo b|ar", "saiw{", "foo {bar}", "0:5"},
	{"foo b|ar", "saiw}", "foo {bar}", "0:5"},
	{"foo b|ar", "saiw<lt>", "foo <bar>", "0:5"},
	{"foo b|ar", "saiw>", "foo <bar>", "0:5"},
	{"foo b|ar", "saiw\"", "foo \"bar\"", "0:5"},
	{"foo b|ar", "saiw'", "foo 'bar'", "0:5"},
	{"foo b|ar", "saiw`", "foo `bar`", "0:5"},
	{"foo b|ar", "saiw*", "foo *bar*", "0:5"},
	{"foo b|ar", "saiwあ", "foo あbarあ", "0:7"},
	{"foo b|ar baz", "sa2w(", "foo b(ar baz)", "0:6"},
	{"foo b|ar baz", "sa$(", "foo b(ar baz)", "0:6"},
	{"foo b|ar", "sa0(", "(foo b)ar", "0:1"},
	{"a|b", "sal(", "a(b)", "0:2"},
	{"|", "sal(", "()", "0:1"},
	{"日本 語|句", "saiw(", "日本 (語句)", "0:8"},
	// 行単位の対象では、開きと閉じを独立した行にし、最初の行と最後の行の字下げを付ける。
	{"a|b\ncd", "saj(", "(\nab\ncd\n)", "1:0"},
	{"  |a\n  b\nc", "saip(", "  (\n  a\n  b\nc\n)", "1:2"},
	{"\ta|b\n  cd", "saj[", "\t[\n\tab\n  cd\n  ]", "1:1"},
	{"|ab cd ef", "saiw(ww.", "(ab) (cd) ef", "0:6"},
	{"foo b|ar", "saiw(u", "foo bar", "0:4"},
	{"  a|b", "saiw(u", "  ab", "0:2"},
	{"x\na|b\ncd", "saj(u", "x\nab\ncd", "1:0"},
	{"foo b|ar", "saiw<Esc>", "foo bar", "0:5"},
	// sd（FR-74）。カーソルの上の開きと閉じも、カーソルを含む囲みである。カーソルは消した開きの位置へ移る。
	{"(f|oo) x", "sd(", "foo x", "0:0"},
	{"(f|oo) x", "sd)", "foo x", "0:0"},
	{"|(foo) x", "sd(", "foo x", "0:0"},
	{"(foo|) x", "sd)", "foo x", "0:0"},
	{"(foo) |x", "sd(", "(foo) x", "0:6"},
	{"f|(a)", "sd(", "fa", "0:1"},
	{"( f|oo )", "sd(", " foo ", "0:1"},
	{"(  f|oo)", "sd(", "  foo", "0:2"},
	{"x(  f|oo)", "sd(", "x  foo", "0:1"},
	{"(\tf|oo)", "sd(", "\tfoo", "0:1"},
	{"(foo(b|ar)baz)", "sd(", "(foobarbaz)", "0:4"},
	{"(foo(b|ar)baz)", "2sd(", "foo(bar)baz", "0:0"},
	{"(foo(b|ar)baz)", "3sd(", "(foo(bar)baz)", "0:6"},
	{"(a [b|) c]", "sd(", "a [b c]", "0:0"},
	{"<<a|>b>", "sd<lt>", "<ab>", "0:1"},
	{"[a|]", "sd]", "a", "0:0"},
	{"{a|}", "sd{", "a", "0:0"},
	{"(\nf|oo\n)", "sd(", "\nfoo\n", "0:0"},
	{"  (\n  f|oo\n  )", "sd(", "  \n  foo\n  ", "0:1"},
	{"(a\nb|\nc)", "sd)", "a\nb\nc", "0:0"},
	{"( \nf|oo)", "sd(", " \nfoo", "0:0"},
	{"(  )|", "sd(", "  ", "0:0"},
	{"abc(f|oo)", "sd(", "abcfoo", "0:3"},
	{"あ(い|)う", "sd(", "あいう", "0:3"},
	// 引用符は 1 行の中だけで探し、カーソルが引用符の上なら行の頭から数えた順で開きか閉じかを決める。
	{"\"a\" |b \"c\"", "sd\"", "\"a b c\"", "0:2"},
	{"|\"a\" b \"c\"", "sd\"", "a b \"c\"", "0:0"},
	{"\"a|\" b \"c\"", "sd\"", "a b \"c\"", "0:0"},
	{"\"a\" b |\"c\"", "sd\"", "\"a\" b c", "0:6"},
	{"\"a\" b \"c|\"", "sd\"", "\"a\" b c", "0:6"},
	{"\"a\"| \"b\" \"c\"", "sd\"", "\"a b\" \"c\"", "0:2"},
	{"x |\"a", "sd\"", "x \"a", "0:2"},
	{"'a|'", "sd'", "a", "0:0"},
	{"`a|`", "sd`", "a", "0:0"},
	{"\"  f|oo\"", "sd\"", "  foo", "0:2"},
	{"\"a\\\"|b\" c", "sd\"", "a\\\"b c", "0:0"},
	{"\"a\\\\\"|b\" c", "sd\"", "\"a\\\\b c", "0:4"},
	{"\"a\nb|\"", "sd\"", "\"a\nb\"", "1:1"},
	{"\"a |\"b\" c\"", "2sd\"", "\"a \"b\" c\"", "0:3"},
	// 引用符でない同じ文字は、行をまたいで探す。
	{"xa|x", "sdx", "a", "0:0"},
	{"xax |b xax", "sdx", "xa b ax", "0:2"},
	{"xa|x b xax", "sdx", "a b xax", "0:0"},
	{"xa\n|bx", "sdx", "a\nb", "0:0"},
	{"xa xb|x cx", "2sdx", "xa xbx cx", "0:5"},
	// 空白は、空白とタブの続きを 1 つの囲みとして消す。
	{"a |x b", "sd<Space>", "axb", "0:1"},
	{"a  |b  c", "sd<Space>", "abc", "0:1"},
	{"a| b c", "sd<Space>", "abc", "0:1"},
	{"|a b", "sd<Space>", "a b", "0:0"},
	{"a\t|x  b", "sd<Space>", "axb", "0:1"},
	{"(a) (|b)", "sd(0.", "a b", "0:0"},
	{"(f|oo) (bar)", "sd(w.", "foo bar", "0:4"},
	{"(f|oo)", "sd(u", "(foo)", "0:0"},
	{"x\n(a\nb|)", "sd(u", "x\n(a\nb)", "1:0"},
	{"(a|b) (c)", "sd(w.u", "ab (c)", "0:3"},
	{"(f|oo)", "sd<Esc>", "(foo)", "0:2"},
	// sr（FR-75）。カーソルは新しい開きの直後へ移る。
	{"\"f|oo\"", "sr\"'", "'foo'", "0:1"},
	{"(f|oo)", "sr([", "[foo]", "0:1"},
	{"(f|oo)", "sr(]", "[foo]", "0:1"},
	{"(f|oo)", "sr(<lt>", "<foo>", "0:1"},
	{"(f|oo)", "sr({", "{foo}", "0:1"},
	{"(f|oo)", "sr(\"", "\"foo\"", "0:1"},
	{"(f|oo)", "sr(x", "xfoox", "0:1"},
	{"(f|oo)", "sr(<Space>", " foo ", "0:1"},
	{"( f|oo )", "sr([", "[ foo ]", "0:1"},
	{"<a|>", "sr<lt>(", "(a)", "0:1"},
	{"x (f|oo) y", "sr([", "x [foo] y", "0:3"},
	{"(foo(b|ar)baz)", "2sr([", "[foo(bar)baz]", "0:1"},
	{"(foo) (b|ar)", "sr([0.", "[foo] [bar]", "0:1"},
	{"(a\n  |b\nc)", "sr([", "[a\n  b\nc]", "0:1"},
	{"(\n  f|oo)", "sr([", "[\n  foo]", "1:2"},
	{"a |x b", "sr<Space>(", "a(x)b", "0:2"},
	{"a  |b  c", "sr<Space>(", "a(b)c", "0:2"},
	{"(f|oo)", "sr([u", "(foo)", "0:0"},
	{"(f|oo)", "sr(<Esc>", "(foo)", "0:2"},
	{"(foo) |x", "sr([", "(foo) x", "0:6"},
}

// ownSurroundCases は、vim-sandwich と意図して違う点（基本設計 4.8 と、surround.go の説明）と、
// vim-sandwich と比べられない組み合わせ（マクロ、回数つきのドットリピート）のケースである。期待値は設計から決めた。
var ownSurroundCases = []surroundCase{
	// 回数は、他のオペレーターと同じく対象の回数に掛ける（vim-sandwich の 2saiw( は 2 重に囲む）。
	{"foo b|ar baz", "2saiw(", "foo (bar )baz", "0:5"},
	// <CR> は囲みの文字に使えない。
	{"foo b|ar", "saiw<CR>", "foo bar", "0:5"},
	{"(f|oo)", "sd<CR>", "(foo)", "0:2"},
	{"(f|oo)", "sr(<CR>", "(foo)", "0:2"},
	// 回数つきのドットリピートは、記録した回数を置き換える（2. は内側から 2 番目）。
	{"((a) (b|))", "sd(02l2.", "(a) b", "0:0"},
	// マクロで繰り返せる。
	{"|(a)\n(b)\n(c)", "qasd(jq2@a", "a\nb\nc", "2:0"},
	{"|a\nb", "qasaiw(jq@a", "(a)\n(b)", "1:1"},
	// 名前を指定しても、レジスターは変わらない。
	{"(f|oo)", "\"asd(", "foo", "0:0"},
}

// 囲みの操作（sa、sd、sr）の結果が、vim-sandwich の既定の動作と一致する。
//
// req: FR-73, FR-74, FR-75
func TestSurround(t *testing.T) {
	for _, tc := range append(append([]surroundCase{}, sandwichCases...), ownSurroundCases...) {
		t.Run(fmt.Sprintf("%q/%s", tc.text, tc.keys), func(t *testing.T) {
			s := setup(t, tc.text)
			s.registers.history = []Register{{Text: "U", filled: true}}
			s.registers.named[0] = Register{Text: "A"}
			feedKeys(s, tc.keys)
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if got := s.Cursor().String(); got != tc.cursor {
				t.Errorf("cursor = %s, want %s", got, tc.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			if unnamed, _ := s.registers.Get(0); unnamed.Text != "U" {
				t.Errorf("無名のレジスターが %q に変わった", unnamed.Text)
			}
		})
	}
}

// 囲みの操作は、1 回のアンドゥで取り消せる。
//
// req: FR-73, FR-74, FR-75
func TestSurroundUndoUnit(t *testing.T) {
	for _, keys := range []string{"saiw(", "saj(", "sd(", "sr(["} {
		t.Run(keys, func(t *testing.T) {
			const text = "(foo)\nbar"
			s := setup(t, "(f|oo)\nbar")
			feedKeys(s, keys)
			if s.buf.Text() == text {
				t.Fatalf("%s が何も変えなかった", keys)
			}
			feedKeys(s, "u")
			if got := s.buf.Text(); got != text {
				t.Errorf("u の後 = %q, want %q", got, text)
			}
		})
	}
}
