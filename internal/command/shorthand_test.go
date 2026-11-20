package command

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// shorthandCases は、省略形のコマンド（D、C、S）の、Vim 9.1 と突き合わせたケースである。
// Y（y$。FR-146）は yankToLineEndCases、Vim の Y（yy）は vimYankLineCases にある。
//
// 期待値の形と入力の前のレジスターは vimCase と同じである。ただし Vim は Ex モードでない起動
// （`vim -Nu NONE -n --not-a-term -c 'source <script>'`）で動かし、keys を feedkeys(keys, "xt") で入力した。
// 途中で失敗した操作があっても、後のキーは続けて入力される（Vim の feedkeys と同じ）。
//
// 省略形と同じ働きの cl、dl、cc の境界（空の行、行の無いバッファ）も含める。
// 空の行の C と、行をまたぐ変更のアンドゥの後のカーソル（2Du、字下げのある行の SX<Esc>u）は、#79 で Vim に合わせて表に戻した。
var shorthandCases = []vimCase{
	{"abc d|ef", "D", "abc d", "0:4", "v:ef", "v:U", ":", "v:ef", "v:A"},
	{"abc d|ef", "\"aD", "abc d", "0:4", "v:ef", "v:U", ":", ":", "v:ef"},
	{"abc d|ef", "\"AD", "abc d", "0:4", "v:Aef", "v:U", ":", ":", "v:Aef"},
	{"abc d|ef", "\"_D", "abc d", "0:4", "v:U", "v:U", ":", ":", "v:A"},
	{"abc d|ef\nghi\njkl", "2D", "abc d\njkl", "0:4", "v:ef\nghi", "v:U", "v:ef\nghi", ":", "v:A"},
	{"a|bc\nghi\njkl", "3D", "a", "0:0", "v:bc\nghi\njkl", "v:U", "v:bc\nghi\njkl", ":", "v:A"},
	{"a|bc\nghi\njkl", "5D", "a", "0:0", "v:bc\nghi\njkl", "v:U", "v:bc\nghi\njkl", ":", "v:A"},
	{"a\nb|c", "2D", "a\nbc", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "D", "", "0:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"ab|c", "D", "ab", "0:1", "v:c", "v:U", ":", "v:c", "v:A"},
	{"|", "D", "", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "D", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "2D", "a", "0:0", "V:\nb\n", "v:U", "V:\nb\n", ":", "v:A"},
	{"日|本語", "D", "日", "0:0", "v:本語", "v:U", ":", "v:本語", "v:A"},
	{"  |ab\ncd", "2D", "", "0:0", "V:  ab\ncd\n", "v:U", "V:  ab\ncd\n", ":", "v:A"},
	{"  |ab\ncd  ", "2D", "", "0:0", "V:  ab\ncd  \n", "v:U", "V:  ab\ncd  \n", ":", "v:A"},
	{"|ab\ncd", "2D", "", "0:0", "V:ab\ncd\n", "v:U", "V:ab\ncd\n", ":", "v:A"},
	{"a|bc\nabc", "Dj.", "a\n", "1:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"a|bc\nabc\nabc\nabc", "2Dj.", "a", "0:0", "V:abc\nabc\n", "v:U", "V:abc\nabc\n", ":", "v:A"},
	{"a|b\nc\nd\ne\nf\ng", "Dj2.", "a\ne\nf\ng", "1:0", "V:c\nd\n", "v:U", "V:c\nd\n", "v:b", "v:A"},
	{"a|b\nc\nd\ne\nf\ng", "2Dj.", "a\nf\ng", "1:0", "V:d\ne\n", "v:U", "V:d\ne\n", ":", "v:A"},
	{"a|b\nab", "\"aDj.", "a\n", "1:0", "v:ab", "v:U", ":", ":", "v:ab"},
	{"a|bc", "Du", "abc", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"|c d", "ddD", "", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|c d", "ddDp", "\nc d", "1:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"a|bc\nd", "2\"aD", "a", "0:0", "v:bc\nd", "v:U", "v:bc\nd", ":", "v:bc\nd"},
	{"a|bc\nd", "\"a2D", "a", "0:0", "v:bc\nd", "v:U", "v:bc\nd", ":", "v:bc\nd"},
	{"abc d|ef", "CX<Esc>", "abc dX", "0:5", "v:ef", "v:U", ":", "v:ef", "v:A"},
	{"abc d|ef", "\"aCX<Esc>", "abc dX", "0:5", "v:ef", "v:U", ":", ":", "v:ef"},
	{"abc d|ef\nghi\njkl", "2CX<Esc>", "abc dX\njkl", "0:5", "v:ef\nghi", "v:U", "v:ef\nghi", ":", "v:A"},
	{"a\nb|c", "2C", "a\nbc", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|abc", "CX<Esc>", "X", "0:0", "v:abc", "v:U", ":", "v:abc", "v:A"},
	{"ab|c", "CX<Esc>", "abX", "0:2", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a\n|\nb", "2CX<Esc>", "a\nX", "1:0", "v:\nb", "v:U", "v:\nb", ":", "v:A"},
	{"  |ab\ncd", "2CX<Esc>", "  X", "0:2", "v:ab\ncd", "v:U", "v:ab\ncd", ":", "v:A"},
	{"a|bc\nabc", "CX<Esc>j.", "aX\naX", "1:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc\nabc\nabc", "CX<Esc>j2.", "aX\naX", "1:1", "v:bc\nabc", "v:U", "v:bc\nabc", "v:bc", "v:A"},
	{"a|bc", "CX<Esc>u", "abc", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc", "C<Esc>", "a", "0:0", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"|c d", "ddCX<Esc>", "X", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"a|bc", "\"_CX<Esc>", "aX", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n  b|c\nd", "SX<Esc>", "a\nX\nd", "1:0", "V:  bc\n", "v:U", "V:  bc\n", ":", "v:A"},
	{"a\n  b|c\nd", "2SX<Esc>", "a\nX", "1:0", "V:  bc\nd\n", "v:U", "V:  bc\nd\n", ":", "v:A"},
	{"a\n  b|c\nd", "\"aSX<Esc>", "a\nX\nd", "1:0", "V:  bc\n", "v:U", "V:  bc\n", ":", "V:  bc\n"},
	{"a\nb|c\nd", "5SX<Esc>", "a\nX", "1:0", "V:bc\nd\n", "v:U", "V:bc\nd\n", ":", "v:A"},
	{"a\nb|c", "2S", "a\nbc", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "SX<Esc>", "X", "0:0", "V:\n", "v:U", "V:\n", ":", "v:A"},
	{"  a|b", "SX<Esc>", "X", "0:0", "V:  ab\n", "v:U", "V:  ab\n", ":", "v:A"},
	{"a|b\ncd", "SX<Esc>j.", "X\nX", "1:0", "V:cd\n", "v:U", "V:cd\n", ":", "v:A"},
	{"a|b\ncd\nef\ngh", "2SX<Esc>jj.", "X\nef\ngh", "2:0", "V:ab\ncd\n", "v:U", "V:ab\ncd\n", ":", "v:A"},
	{"a\nb|c\nd", "SX<Esc>u", "a\nbc\nd", "1:0", "V:bc\n", "v:U", "V:bc\n", ":", "v:A"},
	{"a\n  b|c\nd", "S<Esc>", "a\n\nd", "1:0", "V:  bc\n", "v:U", "V:  bc\n", ":", "v:A"},
	{"|c d", "ddSX<Esc>", "X", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"a|bc", "\"_SX<Esc>", "X", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	// ノーマルモードの s（cl）は囲みの操作に使うので既定に無い（要件定義 1.1）。もとの s のケースは cl で確かめる。
	{"a|bc", "clX<Esc>", "aXc", "0:1", "v:b", "v:U", ":", "v:b", "v:A"},
	{"a|bc", "2clX<Esc>", "aX", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"a|bc", "5clX<Esc>", "aX", "0:1", "v:bc", "v:U", ":", "v:bc", "v:A"},
	{"ab|c", "clX<Esc>", "abX", "0:2", "v:c", "v:U", ":", "v:c", "v:A"},
	{"a\n|\nb", "3clX<Esc>", "a\nX\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "\"aclX<Esc>", "aXc", "0:1", "v:b", "v:U", ":", ":", "v:b"},
	{"a|bc", "\"_clX<Esc>", "aXc", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc", "cl<Esc>", "ac", "0:0", "v:b", "v:U", ":", "v:b", "v:A"},
	{"|abcdef", "clX<Esc>l.", "XXcdef", "0:1", "v:b", "v:U", ":", "v:b", "v:A"},
	{"|abcdefgh", "2clX<Esc>l3.", "XXfgh", "0:1", "v:cde", "v:U", ":", "v:cde", "v:A"},
	{"|", "clX<Esc>", "X", "0:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "clX<Esc>", "a\nX\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"a\n|\nb", "dl", "a\n\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	{"|c d", "ddccX<Esc>", "X", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"a|bc", "clX<Esc>u", "abc", "0:1", "v:b", "v:U", ":", "v:b", "v:A"},
	{"日|本語", "clX<Esc>", "日X語", "0:3", "v:本", "v:U", ":", "v:本", "v:A"},
	{"日|本語", "2clX<Esc>", "日X", "0:3", "v:本語", "v:U", ":", "v:本語", "v:A"},
	{"|c d", "ddclX<Esc>", "X", "0:0", "V:c d\n", "v:U", "V:c d\n", ":", "v:A"},
	{"|ab\ncd\nef", "qaDjq@a", "\n\nef", "2:0", "v:cd", "v:U", ":", "v:cd", "v:Dj"},
	{"|ab\ncd\nef", "qaSX<Esc>jq2@a", "X\nX\nX", "2:0", "V:ef\n", "v:U", "V:ef\n", ":", "v:SX\u001bj"},
	{"|ab\ncd\nef", "qaclX<Esc>jq@a", "Xb\nXd\nef", "2:0", "v:c", "v:U", ":", "v:c", "v:clX\u001bj"},
	{"a|bc\nde", "2Du", "abc\nde", "0:1", "v:bc\nde", "v:U", "v:bc\nde", ":", "v:A"},
	{"a\n  b|c\nd", "SX<Esc>u", "a\n  bc\nd", "1:2", "V:  bc\n", "v:U", "V:  bc\n", ":", "v:A"},
	{"a\n|\nb", "CX<Esc>", "a\nX\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"|", "CX<Esc>", "X", "0:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "C<Esc>p", "a\n\nb", "1:0", "v:", "v:U", ":", "v:", "v:A"},
	{"a\n|\nb", "\"aCX<Esc>", "a\nX\nb", "1:0", "v:", "v:U", ":", ":", "v:"},
}

// 省略形のコマンドの、回数、レジスター、ドットリピート、アンドゥ、マクロと、行末、最終行、空の行、行の無いバッファの境界が、
// Vim 9.1 と一致する。
//
// req: FR-02, FR-04, FR-05, FR-06, FR-07
func TestVimShorthand(t *testing.T) {
	runVimCases(t, shorthandCases)
}

// yankToLineEndCases は、ノーマルモードの Y（y$。FR-146）の、Neovim 0.12.5 と突き合わせたケースである。
//
// Neovim は既定で Y を y$ に割り当てている（`:nmap Y` が `Y * y$` を返し、`:help Y-default` に書かれている）。
// 期待値の形と入力の前のレジスターは vimCase と同じである。Neovim 0.12.5 を `nvim --clean --headless` で起動し、
// ケースごとに新しいバッファへ undolevels を -1 にして text を置き、レジスターを入れてから、keys を feedkeys(keys, "xt") で入力した
// （PR #284 の説明に方法を書いた）。
// 回数は D と同じく y$ の回数で（3Y は 3y$）、カーソルは動かない。ヤンクは変更ではないので、. は直前の変更（x）を繰り返す。
var yankToLineEndCases = []vimCase{
	{"a\nb|c\nd", "Y", "a\nbc\nd", "1:1", "v:c", "v:c", ":", ":", "v:A"},
	{"a\nb|c\nd", "2Y", "a\nbc\nd", "1:1", "v:c\nd", "v:c\nd", ":", ":", "v:A"},
	{"a\nb|c\nd", "5Y", "a\nbc\nd", "1:1", "v:c\nd", "v:c\nd", ":", ":", "v:A"},
	{"a\nb|c", "2Y", "a\nbc", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a\nb|c\nd", "\"aY", "a\nbc\nd", "1:1", "v:c", "v:U", ":", ":", "v:c"},
	{"a\nb|c\nd", "\"AY", "a\nbc\nd", "1:1", "v:Ac", "v:U", ":", ":", "v:Ac"},
	{"a\nb|c\nd", "\"_Y", "a\nbc\nd", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "Y", "", "0:0", "v:", "v:", ":", ":", "v:A"},
	{"|c d", "ddY", "", "0:0", "v:", "v:", "V:c d\n", ":", "v:A"},
	{"a\nb|c\nd", "Yp", "a\nbcc\nd", "1:2", "v:c", "v:c", ":", ":", "v:A"},
	{"a\nb|c\nd", "YP", "a\nbcc\nd", "1:1", "v:c", "v:c", ":", ":", "v:A"},
	{"a\n  b|c\nd", "Y", "a\n  bc\nd", "1:3", "v:c", "v:c", ":", ":", "v:A"},
	{"|ab cd", "xY.", " cd", "0:0", "v:b", "v:b cd", ":", "v:b", "v:A"},
	{"a\nb|c\nd", "2\"aY", "a\nbc\nd", "1:1", "v:c\nd", "v:U", ":", ":", "v:c\nd"},
	{"a\nb|c\nd", "\"a2Y", "a\nbc\nd", "1:1", "v:c\nd", "v:U", ":", ":", "v:c\nd"},
	{"ab|c", "Y", "abc", "0:2", "v:c", "v:c", ":", ":", "v:A"},
	{"|abc", "Y", "abc", "0:0", "v:abc", "v:abc", ":", ":", "v:A"},
	{"日|本語", "Y", "日本語", "0:3", "v:本語", "v:本語", ":", ":", "v:A"},
	{"日|本語\nかき", "2Y", "日本語\nかき", "0:3", "v:本語\nかき", "v:本語\nかき", ":", ":", "v:A"},
	{"a|bc\nde\nfg", "3Y", "abc\nde\nfg", "0:1", "v:bc\nde\nfg", "v:bc\nde\nfg", ":", ":", "v:A"},
	{"a|bc\nde\nfg", "3Yp", "abbc\nde\nfgc\nde\nfg", "0:2", "v:bc\nde\nfg", "v:bc\nde\nfg", ":", ":", "v:A"},
	{"a\n|\nb", "Y", "a\n\nb", "1:0", "v:", "v:", ":", ":", "v:A"},
	{"a\n|\nb", "2Y", "a\n\nb", "1:0", "v:\nb", "v:\nb", ":", ":", "v:A"},
	{"|abc\nabc", "xYj.", "bc\nbc", "1:0", "v:a", "v:bc", ":", "v:a", "v:A"},
	{"a\nb|c\nd", "yyY", "a\nbc\nd", "1:1", "v:c", "v:c", ":", ":", "v:A"},
	{"|ab\ncd\nef", "qaYjq@a", "ab\ncd\nef", "2:0", "v:cd", "v:cd", ":", ":", "v:Yj"},
	{"|ab\ncd\nef", "qaYPjq@a", "abab\ncdd\nef", "2:1", "v:d", "v:d", ":", ":", "v:YPj"},
	{"a|bc", "Yu", "abc", "0:1", "v:bc", "v:bc", ":", ":", "v:A"},
	{"a|bc", "xYu", "abc", "0:1", "v:c", "v:c", ":", "v:b", "v:A"},
}

// ノーマルモードの Y は、D と同じく行の末尾まで（y$）をヤンクする。回数、レジスター、ドットリピート、アンドゥ、マクロと、
// 行末、最終行、空の行、行の無いバッファの境界が、Neovim 0.12.5 の Y と一致する。
//
// req: FR-146
func TestNeovimYankToLineEnd(t *testing.T) {
	runVimCases(t, yankToLineEndCases)
}

// vimYankLineCases は、Vim 9.1 の Y（yy）と突き合わせたケースである。#280 の前に shorthandCases にあった Y の行で、
// 期待値の取り方は shorthandCases と同じである。TestYankLineKeymap は、これにマクロのケース（qaYjq@a。同じ方法で取った）を足して確かめる。
var vimYankLineCases = []vimCase{
	{"a\nb|c\nd", "Y", "a\nbc\nd", "1:1", "V:bc\n", "V:bc\n", ":", ":", "v:A"},
	{"a\nb|c\nd", "2Y", "a\nbc\nd", "1:1", "V:bc\nd\n", "V:bc\nd\n", ":", ":", "v:A"},
	{"a\nb|c\nd", "5Y", "a\nbc\nd", "1:1", "V:bc\nd\n", "V:bc\nd\n", ":", ":", "v:A"},
	{"a\nb|c", "2Y", "a\nbc", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a\nb|c\nd", "\"aY", "a\nbc\nd", "1:1", "V:bc\n", "v:U", ":", ":", "V:bc\n"},
	{"a\nb|c\nd", "\"AY", "a\nbc\nd", "1:1", "V:A\nbc\n", "v:U", ":", ":", "V:A\nbc\n"},
	{"a\nb|c\nd", "\"_Y", "a\nbc\nd", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|", "Y", "", "0:0", "V:\n", "V:\n", ":", ":", "v:A"},
	{"|c d", "ddY", "", "0:0", "V:\n", "V:\n", "V:c d\n", ":", "v:A"},
	{"a\nb|c\nd", "Yp", "a\nbc\nbc\nd", "2:0", "V:bc\n", "V:bc\n", ":", ":", "v:A"},
	{"a\n  b|c\nd", "Y", "a\n  bc\nd", "1:3", "V:  bc\n", "V:  bc\n", ":", ":", "v:A"},
	{"|ab cd", "xY.", " cd", "0:0", "v:b", "V:b cd\n", ":", "v:b", "v:A"},
	{"a\nb|c\nd", "2\"aY", "a\nbc\nd", "1:1", "V:bc\nd\n", "v:U", ":", ":", "V:bc\nd\n"},
}

// 設定の keymap で Y に yank-line を割り当てると（FR-126）、Y は Vim 9.1 の Y（yy）と同じ結果になる。
// yank-line の ID は、Y の既定を y$ に変えた後も残す（#280）。マクロに記録した Y も、割り当てた yank-line で再生する。
//
// req: FR-126
func TestYankLineKeymap(t *testing.T) {
	keymap := func() *input.Keymap {
		km := input.DefaultKeymap()
		b, err := input.UserBinding(input.MapNormal, editcmd.MustParseKeys("Y"), "yank-line")
		if err != nil {
			t.Fatal(err)
		}
		if err := km.Override(b); err != nil {
			t.Fatal(err)
		}
		return km
	}
	runVimCasesKeymap(t, append(vimYankLineCases,
		vimCase{"|ab\ncd\nef", "qaYjq@a", "ab\ncd\nef", "2:0", "V:cd\n", "V:cd\n", ":", ":", "v:Yj"},
	), keymap)
}
