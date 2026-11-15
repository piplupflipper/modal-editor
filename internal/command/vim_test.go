package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/input"
)

// vimCase は、Vim 9.1 と突き合わせたケースである。
//
// 期待値は、Vim 9.1 を `vim -Nu NONE -es` で起動し、text（"|" がカーソル）を置いてから
// keys を feedkeys(keys, "xt") で入力した後（Vim のエラーで止まった場合はそこまで）の、バッファの内容、カーソル（行:バイト）、
// レジスター（"、0、1、-、a）の getregtype() と getreg() を ":" でつないだものである。
// 入力の前に、" と 0 に文字単位の "U"、a に文字単位の "A"、b に行単位の "L1"、"L2" を入れ、他のレジスターを空にしておく。
// 行単位のレジスターは、Vim と同じく最後に改行を付けて比べる。
type vimCase struct {
	text, keys                  string
	want, cursor                string
	unnamed, r0, r1, rMinus, ra string
}

// vimCases は、Vim 9.1 の結果である（PR の説明に作り方を書いた）。
var vimCases = []vimCase{
	{"abc |def ghi", "dw", "abc ghi", "0:4", "v:def ", "v:U", ":", "v:def ", "v:A"},
	{"abc |def\nghi", "dw", "abc \nghi", "0:3", "v:def", "v:U", ":", "v:def", "v:A"},
	{"|a b c d e f g", "d3w", "d e f g", "0:0", "v:a b c ", "v:U", ":", "v:a b c ", "v:A"},
	{"|a b c d e f g", "2d3w", "g", "0:0", "v:a b c d e f ", "v:U", ":", "v:a b c d e f ", "v:A"},
	{"a\n|b\nc", "5dd", "a", "0:0", "V:b\nc\n", "v:U", "V:b\nc\n", ":", "v:A"},
	{"a\n|b\nc\nd", "3dd", "a", "0:0", "V:b\nc\nd\n", "v:U", "V:b\nc\nd\n", ":", "v:A"},
	{"a\nb\n|c", "2dd", "a\nb\nc", "2:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\nb\n|c", "dd", "a\nb", "1:0", "V:c\n", "v:U", "V:c\n", ":", "v:A"},
	{"|a", "dd", "", "0:0", "V:a\n", "v:U", "V:a\n", ":", "v:A"},
	{"  |foo\nbar\nbaz", "dj", "baz", "0:0", "V:  foo\nbar\n", "v:U", "V:  foo\nbar\n", ":", "v:A"},
	{"x |foo\nbar baz", "d2w", "x baz", "0:2", "v:foo\nbar ", "v:U", "v:foo\nbar ", ":", "v:A"},
	{"  |foo\n  bar  \nz", "d2w", "z", "0:0", "V:  foo\n  bar  \n", "v:U", "V:  foo\n  bar  \n", ":", "v:A"},
	{"  |foo\n  bar  x\nz", "d2w", "  x\nz", "0:2", "v:foo\n  bar  ", "v:U", "v:foo\n  bar  ", ":", "v:A"},
	{"a  |foo\n bar  \nz", "d2w", "a  \nz", "0:2", "v:foo\n bar  ", "v:U", "v:foo\n bar  ", ":", "v:A"},
	{"|foo\nbar", "de", "\nbar", "0:0", "v:foo", "v:U", ":", "v:foo", "v:A"},
	{"ab|c def", "db", "c def", "0:0", "v:ab", "v:U", ":", "v:ab", "v:A"},
	{"abc d|ef", "d0", "ef", "0:0", "v:abc d", "v:U", ":", "v:abc d", "v:A"},
	{"abc d|ef", "d$", "abc d", "0:4", "v:ef", "v:U", ":", "v:ef", "v:A"},
	{"abc d|ef", "d^", "ef", "0:0", "v:abc d", "v:U", ":", "v:abc d", "v:A"},
	{"a|bc\nd\ne", "dG", "", "0:0", "V:abc\nd\ne\n", "v:U", "V:abc\nd\ne\n", ":", "v:A"},
	{"a\nb|c\nd", "dgg", "d", "0:0", "V:a\nbc\n", "v:U", "V:a\nbc\n", ":", "v:A"},
	{"a|bcabc", "dfc", "aabc", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bcabc", "dtc", "acabc", "0:1", "v:b", "v:U", ":", "v:b", "v:A"},
	{"a|bcabc", "dfz", "abcabc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"abca|bc", "dFa", "abcbc", "0:3", "v:a", "v:U", ":", "v:a", "v:A"},
	{"|a (b c) d", "d%", " d", "0:0", "v:a (b c)", "v:U", "v:a (b c)", "v:a (b c)", "v:A"},
	{"|a\n\nb", "d}", "\nb", "0:0", "V:a\n", "v:U", "V:a\n", ":", "v:A"},
	{"a\n\n|b", "d{", "a\nb", "1:0", "V:\n", "v:U", "V:\n", ":", "v:A"},
	{"x(a |b c)y", "di(", "x()y", "0:2", "v:a b c", "v:U", ":", "v:a b c", "v:A"},
	{"x(a |b c)y", "da(", "xy", "0:1", "v:(a b c)", "v:U", ":", "v:(a b c)", "v:A"},
	{"x(a |b c)y", "dib", "x()y", "0:2", "v:a b c", "v:U", ":", "v:a b c", "v:A"},
	{"x(a |b c)y", "di)", "x()y", "0:2", "v:a b c", "v:U", ":", "v:a b c", "v:A"},
	{"x{a |b c}y", "diB", "x{}y", "0:2", "v:a b c", "v:U", ":", "v:a b c", "v:A"},
	{"x{a |b c}y", "da}", "xy", "0:1", "v:{a b c}", "v:U", ":", "v:{a b c}", "v:A"},
	{"x[a |b]y", "di]", "x[]y", "0:2", "v:a b", "v:U", ":", "v:a b", "v:A"},
	{"x \"a |b\" y", "di\"", "x \"\" y", "0:3", "v:a b", "v:U", ":", "v:a b", "v:A"},
	{"x \"a |b\" y", "da\"", "x y", "0:2", "v:\"a b\" ", "v:U", ":", "v:\"a b\" ", "v:A"},
	{"foo b|ar baz", "diw", "foo  baz", "0:4", "v:bar", "v:U", ":", "v:bar", "v:A"},
	{"foo b|ar baz", "daw", "foo baz", "0:4", "v:bar ", "v:U", ":", "v:bar ", "v:A"},
	{"a\n\nb|c\nd\n\ne", "dap", "a\n\ne", "2:0", "V:bc\nd\n\n", "v:U", "V:bc\nd\n\n", ":", "v:A"},
	{"a\n\nb|c\nd\n\ne", "dip", "a\n\n\ne", "2:0", "V:bc\nd\n", "v:U", "V:bc\nd\n", ":", "v:A"},
	{"abc |def ghi", "cwX<Esc>", "abc X ghi", "0:4", "v:def", "v:U", ":", "v:def", "v:A"},
	{"abc |def ghi", "c2wX<Esc>", "abc X", "0:4", "v:def ghi", "v:U", ":", "v:def ghi", "v:A"},
	{"abc  |  def", "cwX<Esc>", "abc  Xdef", "0:5", "v:  ", "v:U", ":", "v:  ", "v:A"},
	{"abc |def ghi", "cWX<Esc>", "abc X ghi", "0:4", "v:def", "v:U", ":", "v:def", "v:A"},
	{"a\n  |bc\nd", "ccX<Esc>", "a\nX\nd", "1:0", "V:  bc\n", "v:U", "V:  bc\n", ":", "v:A"},
	{"a\n  |bc\nd", "cjX<Esc>", "a\nX", "1:0", "V:  bc\nd\n", "v:U", "V:  bc\nd\n", ":", "v:A"},
	{"foo b|ar baz", "ciwX<Esc>", "foo X baz", "0:4", "v:bar", "v:U", ":", "v:bar", "v:A"},
	{"x \"a |b\" y", "ci\"X<Esc>", "x \"X\" y", "0:3", "v:a b", "v:U", ":", "v:a b", "v:A"},
	{"abc d|ef", "c$X<Esc>", "abc dX", "0:5", "v:ef", "v:U", ":", "v:ef", "v:A"},
	{"a|bc", "clXY<Esc>", "aXYc", "0:2", "v:b", "v:U", ":", "v:b", "v:A"},
	{"ab|c def", "yb", "abc def", "0:0", "v:ab", "v:ab", ":", ":", "v:A"},
	{"a\n  b|c\nd", "yk", "a\n  bc\nd", "0:0", "V:a\n  bc\n", "V:a\n  bc\n", ":", ":", "v:A"},
	{"abcd\n  ab|cdef\nd", "yk", "abcd\n  abcdef\nd", "0:3", "V:abcd\n  abcdef\n", "V:abcd\n  abcdef\n", ":", ":", "v:A"},
	{"a\n  b|c\nd", "yj", "a\n  bc\nd", "1:3", "V:  bc\nd\n", "V:  bc\nd\n", ":", ":", "v:A"},
	{"xyzw\nabcd|ef\n\nd", "yip", "xyzw\nabcdef\n\nd", "0:0", "V:xyzw\nabcdef\n", "V:xyzw\nabcdef\n", ":", ":", "v:A"},
	{"a\nb|c\nd", "yy", "a\nbc\nd", "1:1", "V:bc\n", "V:bc\n", ":", ":", "v:A"},
	{"a\nb|c\nd", "2yy", "a\nbc\nd", "1:1", "V:bc\nd\n", "V:bc\nd\n", ":", ":", "v:A"},
	{"xyzw\nab|cdef", "y}", "xyzw\nabcdef", "1:2", "v:cdef", "v:cdef", ":", ":", "v:A"},
	{"x\n  ab|cdef", "y{", "x\n  abcdef", "0:0", "v:x\n  ab", "v:x\n  ab", ":", ":", "v:A"},
	{"ab|c def", "yiw", "abc def", "0:0", "v:abc", "v:abc", ":", ":", "v:A"},
	{"a|bc", "\"cyiw", "abc", "0:0", "v:abc", "v:U", ":", ":", "v:A"},
	{"|ab\n\tcd\n\n  ", ">3j", "\tab\n\t\tcd\n\n\t  ", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|\t\tab\n         cd", "<lt>j", "\tab\n cd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab", "3>>", "ab", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|b\ncd", ">>", "\tab\ncd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|b\ncd", "2>>", "\tab\n\tcd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|b", "<lt><lt>", "ab", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|b\ncd", "<lt>j", "ab\ncd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc", "g~iw", "AbC", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc dE", "g~~", "AbC De", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc dE", "guiw", "abc dE", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc dE", "gUU", "ABC DE", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc dE", "gUw", "aBC dE", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|Bc dE\nfG", "guj", "abc de\nfg", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|Bc\n dE", "g~~", "  AbC\n dE", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|Bc\n dE", "2g~~", "  AbC\n De", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|Bc\n dE", "g~j", "  AbC\n De", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|Bc\n dE", "2yy", "  aBc\n dE", "0:3", "V:  aBc\n dE\n", "V:  aBc\n dE\n", ":", ":", "v:A"},
	{"  a|Bc\n dE", "gUk", "  aBc\n dE", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{" x\n  a|Bc", "gUk", " X\n  ABC", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{" x\n  a|Bc", "2gUU", " x\n  aBc", "1:3", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "3x", "", "0:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"a|bc", "5x", "a", "0:0", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"|", "x", "", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "X", "bc", "0:0", "v:a", "v:U", ":", "v:a", "v:A"},
	{"|abc", "X", "abc", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c", "3X", "c", "0:0", "v:ab", "v:U", ":", "v:ab", "v:A"},
	{"|abcd", "3rx", "xxxd", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|abcd", "5rx", "abcd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bcd", "2r<CR>", "a\nd", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bcd", "r<Tab>", "a\tcd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|aBc", "~", "ABc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|aBc", "5~", "AbC", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"日本|語", "ra", "日本a", "0:6", "v:U", "v:U", ":", ":", "v:A"},
	{"日本|語", "~", "日本語", "0:6", "v:U", "v:U", ":", ":", "v:A"},
	{"日本|語 テスト", "x", "日本 テスト", "0:6", "v:語", "v:U", ":", "v:語", "v:A"},
	{"|a. \n  b", "J", "a.  b", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"|a.\n  b", "J", "a.  b", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\n)b", "J", "a)b", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\n", "J", "a", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|\nb", "J", "b", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|a \nb", "J", "a b", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\t\nb", "J", "a\tb", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|a?\nb", "J", "a?  b", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\nb\nc", "5J", "a b c", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\nb\nc\nd", "3J", "a b c\nd", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|b", "J", "a\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\n  \nb", "3J", "a b", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "p", "abUc", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "P", "aUbc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "\"bp", "abc\nL1\nL2", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "\"bP", "L1\nL2\nabc", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "3\"ap", "abAAAc", "0:4", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "p", "U", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "\"zp", "abc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"x|yz\nab", "yjP", "xyz\nab\nxyz\nab", "0:0", "V:xyz\nab\n", "V:xyz\nab\n", ":", ":", "v:A"},
	{"x|yz\nab", "v$yp", "xyyz\nz\nab", "0:2", "v:yz\n", "v:yz\n", ":", ":", "v:A"},
	{"x|yz\nab", "vjyp", "xyyz\nabz\nab", "0:2", "v:yz\nab", "v:yz\nab", ":", ":", "v:A"},
	{"x|yz\nab", "vjy$p", "xyzyz\nab\nab", "0:3", "v:yz\nab", "v:yz\nab", ":", ":", "v:A"},
	{"a|bc\nd", "2\"bp", "abc\nL1\nL2\nL1\nL2\nd", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "3ixy<Esc>", "axyxyxybc", "0:6", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "3axy<Esc>", "abxyxyxyc", "0:7", "v:U", "v:U", ":", ":", "v:A"},
	{"  a|bc", "Ixy<Esc>", "  xyabc", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "2Axy<Esc>", "abcxyxy", "0:6", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nd", "3oxy<Esc>", "abc\nxy\nxy\nxy\nd", "3:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nd", "2Oxy<Esc>", "xy\nxy\nabc\nd", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "ix<CR>y<Esc>", "ax\nybc", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "ixy<BS>z<Esc>", "axzbc", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "i<Esc>", "abc", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "a<Esc>", "abc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "a<Esc>", "", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "Ax<Esc>", "x", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"日|本語", "ix<Esc>", "日x本語", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "i<Tab><Esc>", "a\tbc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"ab|c", "a<CR><Esc>", "abc\n", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "ixy<Esc>u", "abc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"abc |def ghi", "dwu", "abc def ghi", "0:4", "v:def ", "v:U", ":", "v:def ", "v:A"},
	{"abc |def ghi", "dwu<C-r>", "abc ghi", "0:4", "v:def ", "v:U", ":", "v:def ", "v:A"},
	{"a\nb|c\nd", "ddju", "a\nbc\nd", "1:0", "V:bc\n", "v:U", "V:bc\n", ":", "v:A"},
	{"a\n|b\nc\nd", "ddu", "a\nb\nc\nd", "1:0", "V:b\n", "v:U", "V:b\n", ":", "v:A"},
	{"abc def |ghi", "dw0u", "abc def ghi", "0:8", "v:ghi", "v:U", ":", "v:ghi", "v:A"},
	{"a|bc", "3ixy<Esc>u", "abc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|b", "ddu", "a\nb", "1:0", "V:b\n", "v:U", "V:b\n", ":", "v:A"},
	{"a\n  |bc\nd", "ddu", "a\n  bc\nd", "1:2", "V:  bc\n", "v:U", "V:  bc\n", ":", "v:A"},
	{"a\n  b|c\nd", "cwX<Esc>u", "a\n  bc\nd", "1:3", "v:c", "v:U", ":", "v:c", "v:A"},
	{"ab\n|cd\nef", "yypu", "ab\ncd\nef", "1:0", "V:cd\n", "V:cd\n", ":", ":", "v:A"},
	{"ab\n|cd\nef", "Ju", "ab\ncd\nef", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|b\ncd", ">>u", "ab\ncd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"x\n  a|b\ncd", "dku", "x\n  ab\ncd", "0:0", "V:x\n  ab\n", "v:U", "V:x\n  ab\n", ":", "v:A"},
	{"  ab|c", "xuu", "  abc", "0:4", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|abc", "xxxuu", "bc", "0:0", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|abc", "xxx2u", "bc", "0:0", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|abc", "xxx3u<C-r>", "bc", "0:0", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|abc", "xxx3u2<C-r>", "c", "0:0", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|abc", "u", "abc", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "x<C-r>", "bc", "0:0", "v:a", "v:U", ":", "v:a", "v:A"},
	{"a|bc", "cwX<Esc>u", "abc", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"|a b c d e", "dw.", "c d e", "0:0", "v:b ", "v:U", ":", "v:b ", "v:A"},
	{"|a b c d e f g", "2dw3.", "f g", "0:0", "v:c d e ", "v:U", ":", "v:c d e ", "v:A"},
	{"|a b c d e f g", "2dw3..", "", "0:0", "v:f g", "v:U", ":", "v:f g", "v:A"},
	{"|abc abc abc", "cwX<Esc>w.", "X X abc", "0:2", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"|ab\ncd\nef", "Ax<Esc>j.", "abx\ncdx\nef", "1:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd\nef", "ix<Esc>j3.", "xab\nxxxcd\nef", "1:2", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd", "3ix<Esc>j.", "xxxab\ncxxxd", "1:3", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd", "oxy<Esc>.", "ab\nxy\nxy\ncd", "2:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|abcdef", "x.", "cdef", "0:0", "v:b", "v:U", ":", "v:b", "v:A"},
	{"|abcdef", "rxl.", "xxcdef", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "ylp.", "aaabc", "0:2", "v:a", "v:a", ":", ":", "v:A"},
	{"a\n|b\nc\nd", "dddd\"1p.", "a\nd\nc\nb", "3:0", "V:c\n", "v:U", "V:c\n", ":", "v:A"},
	{"a\n|b\nc\nd\ne", "dddddd\"1p..", "a\ne\nd\nc\nb", "4:0", "V:d\n", "v:U", "V:d\n", ":", "v:A"},
	{"|a b c", "dwyw.", "c", "0:0", "v:b ", "v:b ", ":", "v:b ", "v:A"},
	{"|a b c", "dwu.", "b c", "0:0", "v:a ", "v:U", ":", "v:a ", "v:A"},
	{"|ab cd", "ciwX<Esc>w.", "X X", "0:2", "v:cd", "v:U", ":", "v:cd", "v:A"},
	{"|ab\ncd\nef", "J.", "ab cd ef", "0:5", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd", ">>j.", "\tab\n\tcd", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd", "~.", "AB\ncd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab cd ef", "\"adw.", "ef", "0:0", "v:cd ", "v:U", ":", ":", "v:cd "},
	{"|ab\ncd\nef", "ddj.", "cd", "0:0", "V:ef\n", "v:U", "V:ef\n", ":", "v:A"},
	{"|a\nb\nc\nd\ne\nf", "2ddj.", "c\nf", "1:0", "V:d\ne\n", "v:U", "V:d\ne\n", ":", "v:A"},
	{"|abc", "ix<CR><Esc>.", "x\nx\nabc", "2:0", "v:U", "v:U", ":", ":", "v:A"},
	{"x|yz", "Ox<Esc>j.", "x\nx\nxyz", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|a b c", "dwdwdd", "", "0:0", "V:c\n", "v:U", "V:c\n", "v:b ", "v:A"},
	{"|a\nb\nc", "dddd", "c", "0:0", "V:b\n", "v:U", "V:b\n", ":", "v:A"},
	{"|ab cd", "\"adw\"Adw", "", "0:0", "v:ab cd", "v:U", ":", ":", "v:ab cd"},
	{"|ab\ncd", "\"ayy\"Ayw", "ab\ncd", "0:0", "V:ab\nab\n", "v:U", ":", ":", "V:ab\nab\n"},
	{"|ab\ncd", "\"ayw\"Ayy", "ab\ncd", "0:0", "V:ab\nab\n", "v:U", ":", ":", "V:ab\nab\n"},
	{"|ab cd", "\"_dw", "cd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab\ncd", "\"add", "cd", "0:0", "V:ab\n", "v:U", "V:ab\n", ":", "V:ab\n"},
	{"|ab cd", "\"-dw", "cd", "0:0", "v:ab ", "v:U", ":", "v:ab ", "v:A"},
	{"|ab cd", "yw\"0p", "aab b cd", "0:3", "v:ab ", "v:ab ", ":", ":", "v:A"},
	{"|ab cd", "ywdw\"0P", "ab cd", "0:2", "v:ab ", "v:ab ", ":", "v:ab ", "v:A"},
	{"|ab\ncd\nef", "dddd\"2p", "ef\nab", "1:0", "V:cd\n", "v:U", "V:cd\n", ":", "v:A"},
	{"|ab cd", "\"ayw\"ap", "aab b cd", "0:3", "v:ab ", "v:U", ":", ":", "v:ab "},
	{"|ab cd", "\"Ayw", "ab cd", "0:0", "v:Aab ", "v:U", ":", ":", "v:Aab "},
	{"|ab cd", "dw\"-p", "cab d", "0:3", "v:ab ", "v:U", ":", "v:ab ", "v:A"},
	{"|ab cd", "\"1yw", "ab cd", "0:0", "v:ab ", "v:U", "v:ab ", ":", "v:A"},
	{"|ab cd", "xp", "ba cd", "0:1", "v:a", "v:U", ":", "v:a", "v:A"},
	{"|ab cd", "ddp", "\nab cd", "1:0", "V:ab cd\n", "v:U", "V:ab cd\n", ":", "v:A"},
	{"|a\nb", "ddp", "b\na", "1:0", "V:a\n", "v:U", "V:a\n", ":", "v:A"},
	{"|c d", "dddd", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|a b\nc d", "qaddq@a@a", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:dd"},
	{"|", "dd", "", "0:0", "V:\n", "v:U", "V:\n", ":", "v:A"},
	{"|c d", "dd3dd", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddu", "c d", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddix<Esc>dd", "", "0:0", "V:x\n", "v:U", "V:x\n", ":", "v:A"},
	{"|a\nb", "2dddd", "", "0:0", "V:a\nb\n", "v:U", "V:a\nb\n", ":", "v:A"},
	{"|ab cd", "qadwqu@a", "cd", "0:0", "v:ab ", "v:U", ":", "v:ab ", "v:dw"},
	{"|a b c d e f", "qadwq2@a", "d e f", "0:0", "v:c ", "v:U", ":", "v:c ", "v:dw"},
	{"|a b c d e f", "qadwq@a@@", "d e f", "0:0", "v:c ", "v:U", ":", "v:c ", "v:dw"},
	{"|a b c", "qaxq\"ap", " xb c", "0:1", "v:a", "v:U", ":", "v:a", "v:x"},
	{"|a b c", "qaixy<Esc>q\"ap", "xyixy\u001ba b c", "0:5", "v:U", "v:U", ":", ":", "v:ixy\u001b"},
	{"|a b c", "qaqqAdwq@a", "c", "0:0", "v:b ", "v:U", ":", "v:b ", "v:dw"},
	{"|ab\ncd\nef", "qaA!<Esc>jq2@a", "ab!\ncd!\nef!", "2:2", "v:U", "v:U", ":", ":", "v:A!\u001bj"},
	{"|ab\ncd\nef", "qaA!<Esc>jq5@a", "ab!\ncd!\nef!", "2:2", "v:U", "v:U", ":", ":", "v:A!\u001bj"},
	{"|x", "qadwqu\"ap", "xdw", "0:2", "v:x", "v:U", ":", "v:x", "v:dw"},
	{"|ab\ncd\nef", "qa:s/./X/<CR>jq@a", "Xb\nXd\nef", "2:0", "v:U", "v:U", ":", ":", "v::s/./X/\rj"},
	{"|ab\nab\nab", ":s/a/X/<CR>j@:", "Xb\nXb\nab", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab cd", "qbdwq\"bp", "cdwd", "0:2", "v:ab ", "v:U", ":", "v:ab ", "v:A"},
	{"|a1 a2 a3", "qaxq\"ayy", "1 a2 a3", "0:0", "V:1 a2 a3\n", "v:U", ":", "v:a", "V:1 a2 a3\n"},
	{"|abc", "qaxqqbxq@b", "", "0:0", "v:c", "v:U", ":", "v:c", "v:x"},
	{"|ab\ncd\nef\ngh", "qaddq3@a", "", "0:0", "V:gh\n", "v:U", "V:gh\n", ":", "v:dd"},
	{"|xyz", "qaxp~q@a", "yXZ", "0:2", "v:z", "v:U", ":", "v:z", "v:xp~"},
	{"|abcdef", "qaxxq@au", "cdef", "0:0", "v:d", "v:U", ":", "v:d", "v:xx"},
	{"|abcdef", "qaxxq2@au", "cdef", "0:0", "v:f", "v:U", ":", "v:f", "v:xx"},
	{"|abcdef", "qaxq3@au", "bcdef", "0:0", "v:d", "v:U", ":", "v:d", "v:x"},
	{"|abc", "qaix<Esc>aY<Esc>q@au", "xYabc", "0:1", "v:U", "v:U", ":", ":", "v:ix\u001baY\u001b"},
	{"|abcdefgh", "qaxxq@a@@u", "efgh", "0:0", "v:f", "v:U", ":", "v:f", "v:xx"},
	{"|abcdefgh", "qaxxq@a@@uu", "cdefgh", "0:0", "v:f", "v:U", ":", "v:f", "v:xx"},
	{"|abcdef", "qaxxq3@auu", "bcdef", "0:0", "v:f", "v:U", ":", "v:f", "v:xx"},
	{"|a1\na2\na3", ":s/a/b/<CR>j2@:u", "b1\na2\na3", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|abcdef", "qaxxq@a.u", "ef", "0:0", "v:e", "v:U", ":", "v:e", "v:xx"},
	{"a|bc def\nghi", "vld", "a def\nghi", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc def\nghi", "vjd", "ai", "0:1", "v:bc def\ngh", "v:U", "v:bc def\ngh", ":", "v:A"},
	{"a|bc def\nghi", "Vd", "ghi", "0:0", "V:abc def\n", "v:U", "V:abc def\n", ":", "v:A"},
	{"a|bc def\nghi", "vey", "abc def\nghi", "0:1", "v:bc", "v:bc", ":", ":", "v:A"},
	{"abc |def\nghi", "vbd", "ef\nghi", "0:0", "v:abc d", "v:U", ":", "v:abc d", "v:A"},
	{"a|bc def\nghi", "vjoy", "abc def\nghi", "0:1", "v:bc def\ngh", "v:bc def\ngh", ":", ":", "v:A"},
	{"a|bc def\nghi", "v$d", "aghi", "0:1", "v:bc def\n", "v:U", "v:bc def\n", ":", "v:A"},
	{"a\n|\nb", "vd", "a\nb", "1:0", "v:\n", "v:U", "v:\n", ":", "v:A"},
	{"a|bc def\nghi", "viwd", " def\nghi", "0:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"abc d|ef\nghi", "vlliwy", "abc def\nghi", "0:5", "v:ef\nghi", "v:ef\nghi", ":", ":", "v:A"},
	{"a|bc\n\nd", "vipd", "\nd", "0:0", "V:abc\n", "v:U", "V:abc\n", ":", "v:A"},
	{"a|bc def", "vlcX<Esc>", "aX def", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc def", "vlxp", "a bcdef", "0:3", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc def\nghi", "vj~", "aBC DEF\nGHi", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bC def\nghi", "vlU", "aBC def\nghi", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|BC def", "vlu", "abc def", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nd", "Vj>", "\tabc\n\td", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nd", "V3>", "\t\t\tabc\nd", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"\ta|bc\nd", "V<lt>", "abc\nd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nd", "vjs<Esc>", "a", "0:0", "v:bc\nd", "v:U", "v:bc\nd", ":", "v:A"},
	{"a|bcdef ghijk", "vld.", "af ghijk", "0:1", "v:de", "v:U", ":", "v:de", "v:A"},
	{"|ab\ncd\nef\ngh", "Vjd.", "", "0:0", "V:ef\ngh\n", "v:U", "V:ef\ngh\n", ":", "v:A"},
	{"|abc\nde\nfghij\nkl", "vjd.", "ghij\nkl", "0:0", "v:e\nf", "v:U", "v:e\nf", ":", "v:A"},
	{"a|bc", "vlyP", "abcbc", "0:2", "v:bc", "v:bc", ":", ":", "v:A"},
	{"a|bc def", "vVd", "", "0:0", "V:abc def\n", "v:U", "V:abc def\n", ":", "v:A"},
	{"a|bc\ndef", "Vvd", "ac\ndef", "0:1", "v:b", "v:U", ":", "v:b", "v:A"},
	{"a|bc def", "vvd", "abc def", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc def", "v<Esc>d", "abc def", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc def", "vl\"ay", "abc def", "0:1", "v:bc", "v:U", ":", ":", "v:bc"},
	{"a|bc def", "v2ly", "abc def", "0:1", "v:bc ", "v:bc ", ":", ":", "v:A"},
	{"|日本語", "vld", "語", "0:0", "v:日本", "v:U", ":", "v:日本", "v:A"},
	{"ab|c def", "vhhd", " def", "0:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"a|bc\ndef", "Vjy", "abc\ndef", "0:0", "V:abc\ndef\n", "V:abc\ndef\n", ":", ":", "v:A"},
	{"  a|bc\n def", "Vjy", "  abc\n def", "0:0", "V:  abc\n def\n", "V:  abc\n def\n", ":", ":", "v:A"},
	{"  a|bc\n def", "Vj~", "  ABC\n DEF", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"x\n  a|bc\n def", "Vky", "x\n  abc\n def", "0:0", "V:x\n  abc\n", "V:x\n  abc\n", ":", ":", "v:A"},
	{"a (b |c) d", "vi(d", "a () d", "0:3", "v:b c", "v:U", ":", "v:b c", "v:A"},
	{"a (b |c) d", "va(y", "a (b c) d", "0:2", "v:(b c)", "v:(b c)", ":", ":", "v:A"},
	{"a|bc def", "vlgUw", "aBC def", "0:4", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc def ghi", "veyw.", "abc def ghi", "0:4", "v:bc", "v:bc", ":", ":", "v:A"},
	{"|ab cd ef", "vecX<Esc>w.", "X X ef", "0:2", "v:cd", "v:U", ":", "v:cd", "v:A"},
	{"|ab\ncd\nef", "Vy", "ab\ncd\nef", "0:0", "V:ab\n", "V:ab\n", ":", ":", "v:A"},
	{"a\nb|c\nd", "Vky", "a\nbc\nd", "0:0", "V:a\nbc\n", "V:a\nbc\n", ":", ":", "v:A"},
	{"a b|c", "vxu", "a bc", "0:3", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a|bc\ndef\nghi", "Vjj:s/./X/<CR>", "Xbc\nXef\nXhi", "2:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|ab cd", "v\"_d", "b cd", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\ndef", ":s/b/X/<CR>", "aXc\ndef", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nb\nbb", ":%s/b/X/g<CR>", "aXc\nX\nXX", "2:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nb\nbb", "Vj:s/b/Y/<CR>", "aYc\nY\nbb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nb\nbb", ":2d<CR>", "abc\nbb", "1:0", "V:b\n", "v:U", "V:b\n", ":", "v:A"},
	{"a|bc\nb\nbb", ":2d<CR>p", "abc\nbb\nb", "2:0", "V:b\n", "v:U", "V:b\n", ":", "v:A"},
	{"a|bc\nb\nbb", ":2d a<CR>\"ap", "abc\nbb\nb", "2:0", "V:b\n", "v:U", "V:b\n", ":", "V:b\n"},
	{"a|bc\nb", ":2<CR>", "abc\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nb", ":s/b/X/<CR>u", "abc\nb", "0:0", "v:U", "v:U", ":", ":", "v:A"},
}

// vimReg は、レジスターの内容を、Vim の getregtype() と getreg() を ":" でつないだ形にする。
func vimReg(r Register) string {
	switch {
	case r.empty():
		return ":"
	case r.Linewise:
		return "V:" + r.Text + "\n"
	}
	return "v:" + r.Text
}

// 編集コマンドの実行の結果（バッファ、カーソル、レジスター）が、Vim 9.1 と一致する。
//
// req: FR-01, FR-02, FR-04, FR-05, FR-06, FR-07
func TestVim(t *testing.T) {
	for _, tc := range vimCases {
		t.Run(fmt.Sprintf("%q/%s", tc.text, tc.keys), func(t *testing.T) {
			s := setup(t, tc.text)
			s.registers.unnamed = Register{Text: "U"}
			s.registers.numbered[0] = Register{Text: "U"}
			s.registers.named[0] = Register{Text: "A"}
			s.registers.named[1] = Register{Text: "L1\nL2", Linewise: true}
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
			r := &s.registers
			for _, c := range []struct {
				name      string
				got, want string
			}{
				{`"`, vimReg(r.unnamed), tc.unnamed},
				{"0", vimReg(r.numbered[0]), tc.r0},
				{"1", vimReg(r.numbered[1]), tc.r1},
				{"-", vimReg(r.small), tc.rMinus},
				{"a", vimReg(r.named[0]), tc.ra},
			} {
				if c.got != c.want {
					t.Errorf("register %s = %q, want %q", c.name, c.got, c.want)
				}
			}
		})
	}
}

// setup は、text（"|" がカーソル）を内容とするバッファの State を作る。
func setup(t *testing.T, text string) *State {
	t.Helper()
	before, after, ok := strings.Cut(text, "|")
	if !ok {
		after, before = before, ""
	}
	lines := strings.Split(before, "\n")
	s := New(newBuffer(before+after), input.NewParser(input.DefaultKeymap()))
	s.SetCursor(pos(len(lines)-1, len(lines[len(lines)-1])))
	return s
}
