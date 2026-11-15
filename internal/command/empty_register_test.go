package command

import "testing"

// emptyRegisterCases は、空のテキストをレジスターに書き込む操作を、Vim 9.1 と突き合わせたケースである（#79）。
//
// 期待値の形と入力の前のレジスターは vimCase と同じである。ただし Vim は Ex モードでない起動
// （`vim -Nu NONE -n --not-a-term -c 'source <script>'`）で動かし、ケースごとに setline() の後でアンドゥの履歴を消してから、
// keys を feedkeys(keys, "xt") で入力した。インサートモードで終わったケースは無い。
//
// 空の行の C、c$、ciw、yl、y$ などは、空の文字単位のテキストを書き込む（getregtype() は v）。
// 空の行の D、x、cl、s と、行の無いバッファの c$ は書き込まない。空のテキストが入ったレジスターの p は何もしない。
var emptyRegisterCases = []vimCase{
	{"a\n|\nb", "c$<Esc>", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "CX<Esc>", "a\nX\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>p", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>P", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>u", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>j.", "a\n\n", "2:0", "v:b", "v:U", ":", "v:b", "v:A"},
	{"a\n|\nb", "\"aC<Esc>", "a\n\nb", "1:0", "v:", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "\"AC<Esc>", "a\n\nb", "1:0", "v:A", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "\"aC<Esc>\"ap", "a\n\nb", "1:0", "v:", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "\"_C<Esc>", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "yl", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "ylp", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "y$", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "y0", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "yw", "a\n\nb", "1:0", "V:\n", "V:\n", ":", ":", "v:A"},
	{"a\n|\nb", "yiw", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "d$", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "D", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "dl", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "x", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "X", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "cl<Esc>", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "s<Esc>", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "cw<Esc>", "a\n\nb", "1:0", "V:\n", "v:U", "V:\n", ":", "v:A"},
	{"a\n|\nb", "ce<Esc>", "a\n", "1:0", "v:\nb", "v:U", "v:\nb", ":", "v:A"},
	{"a\n|\nb", "ciw<Esc>", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "diw", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "c0<Esc>", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "d0", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "dh", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "ch<Esc>", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "qaq", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "qaq@a", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "qaqp", "a\nU\nb", "1:0", "v:U", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "C<Esc>\"-p", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>yyp", "a\n\n\nb", "2:0", "V:\n", "V:\n", ":", "v:", "v:A"},
	{"a\n|\nb", "\"ayl", "a\n\nb", "1:0", "v:", "v:U", ":", ":", "v:"},
	{"a\n|\nb", "\"Ayl", "a\n\nb", "1:0", "v:A", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "\"Ayl\"ap", "a\nA\nb", "1:0", "v:A", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "vy", "a\n\nb", "1:0", "v:\n", "v:\n", ":", ":", "v:A"},
	{"a\n|\nb", "vd", "a\nb", "1:0", "v:\n", "v:U", "v:\n", ":", "v:A"},
	{"a\n|\nb", "vc<Esc>", "a\nb", "1:0", "v:\n", "v:U", "v:\n", ":", "v:A"},
	{"a\n|\nb", "C<Esc>@\"", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "\"ayl@a", "a\n\nb", "1:0", "v:", "v:U", ":", ":", "v:"},
	{"a|bc", "C<Esc>p", "abc", "0:2", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc", "d$C<Esc>", "", "0:0", "v:a", "v:U", ":", "v:a", "v:A"},
	{"a|bc", "$C<Esc>", "ab", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a|bc", "$a<Esc>C<Esc>", "ab", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a|bc", "llC<Esc>", "ab", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a|bc", "\"aylC<Esc>", "a", "0:0", "v:bc", "v:U", ":", "v:bc", "v:b"},
	{"a|bc", "\"aC<Esc>\"AC<Esc>", "", "0:0", "v:bca", "v:U", ":", ":", "v:bca"},
	{"ab\n|\ncd", "C<Esc>jp", "ab\n\ncd", "2:0", "v:", "v:U", ":", "v:", "v:A"},
	{"ab\n|\ncd", "C<Esc>kp", "ab\n\ncd", "0:0", "v:", "v:U", ":", "v:", "v:A"},
	{"ab\n|\ncd", "C<Esc>jP", "ab\n\ncd", "2:0", "v:", "v:U", ":", "v:", "v:A"},
	{"|", "C<Esc>", "", "0:0", "v:", "v:U", ":", "v:", "v:A"},
	{"|", "yl", "", "0:0", "v:", "v:", ":", ":", "v:A"},
	{"|", "C<Esc>p", "", "0:0", "v:", "v:U", ":", "v:", "v:A"},
	{"|c d", "ddC<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddciw<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddc$<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddyl", "", "0:0", "v:", "v:", "V:c d\n", ":", "v:A"},
	{"|c d", "ddy$", "", "0:0", "v:", "v:", "V:c d\n", ":", "v:A"},
	{"|c d", "ddyiw", "", "0:0", "v:", "v:", "V:c d\n", ":", "v:A"},
	{"|c d", "ddcc<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvc<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvy", "", "0:0", "v:", "v:", "V:c d\n", ":", "v:A"},
	{"|c d", "ddVc<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvC<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvD", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvY", "", "0:0", "V:\n", "V:\n", "V:c d\n", ":", "v:A"},
	{"|c d", "ddvS<Esc>", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
}

// 空のテキストを書き込む操作と書き込まない操作、空のテキストが入ったレジスターの p と @ が、Vim 9.1 と一致する。
//
// req: FR-04
func TestVimEmptyRegister(t *testing.T) {
	runVimCases(t, emptyRegisterCases)
}
