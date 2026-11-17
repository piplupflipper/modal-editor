package command

import "testing"

// visualDotCases は、ビジュアルモードのヤンクを挟んだドットリピートを、Vim 9.1 と突き合わせたケースである（#167）。
//
// 期待値の形と入力の前のレジスターは vimCase と同じである。ただし Vim は Ex モードでない起動
// （`vim -Nu NONE -i NONE -n --not-a-term -c 'source <script>'`）で動かし、ケースごとに setline() の後で
// keys を feedkeys(keys, "xt") で入力した。
//
// ヤンクは . の対象ではないので、. は直前の変更（vld、vlc）の範囲の大きさ（2 文字）に働く。
// ヤンクの範囲の大きさ（vllly の 4 文字、Vy の行単位）は使わない。
var visualDotCases = []vimCase{
	{"|abcdefghij", "vld0vllly0.", "efghij", "0:0", "v:cd", "v:cdef", ":", "v:cd", "v:A"},
	{"|abcdefghij", "vld0Vy.", "efghij", "0:0", "v:cd", "V:cdefghij\n", ":", "v:cd", "v:A"},
	{"|abcdefghij", "vlcX<Esc>0vllly0.", "Xdefghij", "0:0", "v:Xc", "v:Xcde", ":", "v:Xc", "v:A"},
	{"|abcdefghij", "vld0vllly0u0.", "cdefghij", "0:0", "v:ab", "v:cdef", ":", "v:ab", "v:A"},
}

// ビジュアルモードのヤンクの後の . は、直前の変更の範囲の大きさを使う。
//
// req: FR-06
func TestVimVisualDotAfterYank(t *testing.T) {
	runVimCases(t, visualDotCases)
}
