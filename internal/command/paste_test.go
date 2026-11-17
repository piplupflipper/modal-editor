package command

import (
	"errors"
	"fmt"
	"testing"
)

// pasteCase は、Vim 9.1 と突き合わせた貼り付けのケースである。
//
// 期待値は、Vim 9.1（vim -Nu NONE -i NONE）を tmux 3.4（TERM=xterm-256color）の中で起動し、
// before のキーを送ってから paste を tmux の paste-buffer -p（bracketed paste）で貼り、after のキーを送った後の、
// バッファの内容、カーソル（行:バイト。0 から数える）、レジスター（"）である（2026-11-16）。
// 同じ手順で本体の実行ファイルにも貼り、内容とカーソルが一致することを確かめた（PR の説明に方法と件数を書いた）。
// レジスターは、空の状態から始めた値で、無名のレジスター（履歴の先頭。FR-51）と比べる。"" は確かめない。
// ビジュアルモードの貼り付けは、FR-51 で置き換えたテキストを履歴に積まないので、Vim と違って空のまま（"）である。
type pasteCase struct {
	text, before, paste, after string
	want, cursor               string
	unnamed                    string
}

var pasteCases = []pasteCase{
	// ノーマルモード。行の先頭なら i、それ以外なら a の位置から入れる。
	{text: "|abc\ndef", paste: "dd", want: "ddabc\ndef", cursor: "0:1"},
	{text: "a|bc\ndef", paste: "XY", want: "abXYc\ndef", cursor: "0:3"},
	{text: "ab|c\ndef", paste: "XY", want: "abcXY\ndef", cursor: "0:4"},
	{text: "a|bc\ndef", paste: "X\nY", want: "abX\nYc\ndef", cursor: "1:0"},
	{text: "a|bc\ndef", paste: "X\n", want: "abX\nc\ndef", cursor: "1:0"},
	{text: "abc\n|\ndef", paste: "XY", want: "abc\nXY\ndef", cursor: "1:1"},
	{text: "|", paste: "XY\nZ", want: "XY\nZ", cursor: "1:0"},
	{text: "あ|いう", paste: "えお", want: "あいえおう", cursor: "0:9"},
	{text: "a|bc", paste: "a\tb", want: "aba\tbc", cursor: "0:4"},
	// 回数とオペレーターを打ちかけていれば、それも貼り付けも捨てる。
	{text: "|abc\ndef", before: "2", paste: "XY", after: "x", want: "bc\ndef", cursor: "0:0", unnamed: "v:a"},
	{text: "|abc\ndef", before: "d", paste: "XY", after: "x", want: "bc\ndef", cursor: "0:0", unnamed: "v:a"},
	// アンドゥの 1 単位で、ドットリピートは a でテキストを入れる。
	{text: "a|bc\ndef", paste: "X\nY", after: "u", want: "abc\ndef", cursor: "0:2"},
	{text: "a|bc\ndefg", paste: "XY", after: "j.", want: "abXYc\ndefgXY", cursor: "1:5"},
	{text: "|abc\ndefg", paste: "XY", after: "jl.", want: "XYabc\ndefXYg", cursor: "1:4"},
	{text: "a|bc\ndefg", paste: "XY", after: "j0.", want: "abXYc\ndXYefg", cursor: "1:2"},

	// ビジュアルモード。選択を消して、消した位置から入れる。消したテキストは履歴に積まない（FR-51。Vim は "-か "1 に入れる）。
	{text: "a|bcd\ndef", before: "vl", paste: "Z", want: "aZd\ndef", cursor: "0:1"},
	{text: "ab|cd\ndef", before: "vl", paste: "Z", want: "abZ\ndef", cursor: "0:2"},
	{text: "|abcd\ndef", before: "vl", paste: "Z", want: "Zcd\ndef", cursor: "0:0"},
	{text: "ab|cd\ndef", before: "vh", paste: "Z", want: "aZd\ndef", cursor: "0:1"},
	{text: "a|bcd\ndef\nghi", before: "vj", paste: "Z", want: "aZf\nghi", cursor: "0:1"},
	{text: "abcd\ne|fgh", before: "vkl", paste: "Z", want: "abZgh", cursor: "0:2"},
	{text: "|abc\ndef", before: "v$", paste: "Z", want: "Zdef", cursor: "0:0"},
	{text: "a|bcd\ndef", before: "vl", paste: "X\nY", want: "aX\nYd\ndef", cursor: "1:0"},
	{text: "a|bcd\ndef", before: "vl", paste: "Z", after: "u", want: "abcd\ndef", cursor: "0:1"},
	{text: "a|bcd\ndefg", before: "vl", paste: "Z", after: "j.", want: "aZd\ndeZfg", cursor: "1:2"},
	{text: "abc\n|def\nghi", before: "V", paste: "Z\n", want: "abc\nZ\nghi", cursor: "2:0"},
	{text: "abc\ndef\n|ghi", before: "V", paste: "Z\n", want: "abc\ndef\nZ\n", cursor: "3:0"},
	{text: "abc\n|def\nghi", before: "V", paste: "Z", want: "abc\nZghi", cursor: "1:0"},
	{text: "|abc\ndef", before: "Vj", paste: "Z", want: "Z", cursor: "0:0"},
	{text: "abc\n|def\nghi", before: "Vj", paste: "Z", want: "abc\nZ", cursor: "1:0"},
	{text: "abc\n\n|ghi", before: "V", paste: "Z", want: "abc\nZ", cursor: "1:0"},
	{text: "abc\n|def\nghi", before: "V", paste: "Z\n", after: "u", want: "abc\ndef\nghi", cursor: "1:0"},

	// インサートモードでは、そのまま入れてインサートモードに残る（<Esc> で抜ける）。
	{text: "|abc\ndef", before: "i", paste: "P\tQ\nR", after: "<Esc>", want: "P\tQ\nRabc\ndef", cursor: "1:0"},
}

// ノーマルモードとビジュアルモードの貼り付けは、テキストをコマンドとして解釈せずに入れ、結果が Vim 9.1 と一致する（#114）。
func TestPasteVim(t *testing.T) {
	for _, tc := range pasteCases {
		t.Run(fmt.Sprintf("%q/%s/%q/%s", tc.text, tc.before, tc.paste, tc.after), func(t *testing.T) {
			s := setup(t, tc.text)
			feedKeys(s, tc.before)
			s.Paste(tc.paste)
			feedKeys(s, tc.after)
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if got := s.Cursor().String(); got != tc.cursor {
				t.Errorf("cursor = %s, want %s", got, tc.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			unnamed, _ := s.registers.Get(0)
			want := tc.unnamed
			if want == "" {
				want = ":"
			}
			if got := vimReg(unnamed); got != want {
				t.Errorf("register \" = %q, want %q", got, want)
			}
		})
	}
}

// TestPastePending は、回数を打ちかけた貼り付けが失敗になり、バッファを変えないことを確かめる。
func TestPastePending(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, "3")
	res := s.Paste("dd")
	if !errors.Is(res.Err, ErrFailed) {
		t.Errorf("Err = %v, want ErrFailed", res.Err)
	}
	if s.Pending() {
		t.Error("打ちかけた回数が残っている")
	}
	if got := s.buf.Text(); got != "abc" {
		t.Errorf("text = %q", got)
	}
}

// TestPastePendingRecording は、マクロの記録中に回数を打ちかけて貼ると、取り消しも記録し、再生で回数が後のキーに掛からないことを確かめる。
// Vim 9.1 で abcdef、ghijkl の先頭から qa3 の後に XY を貼り、xq、j0@a と打つと、bcdef、hijkl になる（2026-11-16、tmux 3.4 の paste-buffer -p）。
func TestPastePendingRecording(t *testing.T) {
	s := setup(t, "|abcdef\nghijkl")
	feedKeys(s, "qa3")
	s.Paste("XY")
	feedKeys(s, "xqj0@a")
	if got, want := s.buf.Text(), "bcdef\nhijkl"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := s.Cursor().String(); got != "1:0" {
		t.Errorf("cursor = %s, want 1:0", got)
	}
}

// TestPasteCommandLine は、コマンドラインモードの貼り付けが、改行で確定せずに文字 '\r' として入ることを確かめる（Vim 9.1 は ^M と表示する）。
func TestPasteCommandLine(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, ":")
	res := s.Paste("s/a/X/\nfoo\x03")
	if len(res.Changes) != 0 || s.buf.Text() != "abc" {
		t.Fatalf("貼り付けでコマンドを実行した: %q", s.buf.Text())
	}
	if s.Mode() != ModeCommandLine {
		t.Fatalf("mode = %v, want command line", s.Mode())
	}
	if got, want := string(s.cmdline), "s/a/X/\rfoo"; got != want {
		t.Errorf("cmdline = %q, want %q", got, want)
	}
}

// TestPasteRecording は、マクロの記録中の貼り付けが、同じ結果になるキーの列として記録され、再生で同じ編集になることを確かめる。
func TestPasteRecording(t *testing.T) {
	s := setup(t, "a|bcd\nefgh")
	feedKeys(s, "qq")
	s.Paste("X\ty\n")
	feedKeys(s, "vl")
	s.Paste("Z")
	feedKeys(s, "i")
	s.Paste("W")
	feedKeys(s, "<Esc>q")
	reg, _ := s.registers.Get('q')
	if want := "aX\ty\r\x1bvl\"_cZ\x1biW\x1b"; reg.Text != want {
		t.Errorf("記録 = %q, want %q", reg.Text, want)
	}
	if got, want := s.buf.Text(), "abX\ty\nWZ\nefgh"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	feedKeys(s, "j0@q")
	if got, want := s.buf.Text(), "abX\ty\nWZ\neX\ty\nWZh"; got != want {
		t.Errorf("再生の後の text = %q, want %q", got, want)
	}
}
