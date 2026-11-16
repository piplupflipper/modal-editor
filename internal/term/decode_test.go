package term

import (
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// decodeAll は、in を 1 回で Feed し、保留が残れば Flush した結果を、キーの表記で返す。
func decodeAll(in string) string {
	var d Decoder
	out := d.Feed([]byte(in))
	if d.Pending() {
		out = append(out, d.Flush()...)
	}
	return decodedString(out)
}

func decodedString(ds []Decoded) string {
	var b strings.Builder
	for _, x := range ds {
		b.WriteString(x.Key.String())
	}
	return b.String()
}

// TestDecodeSequences は、代表的な端末が送る列の解読を確かめる。
// 「tmux」の行は、2026-11-16 に tmux 3.4（-f /dev/null の既定の設定）の send-keys が中のプログラムへ送ったバイトを
// 写したものである。「xterm」の行は xterm の制御シーケンスの文書（ctlseqs）の PC 形式のキーの表、
// 「VTE」の行は VTE（GNOME 端末など）が xterm と同じ列を送る範囲のものである。
func TestDecodeSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"文字", "abc", "abc"},
		{"UTF-8 の文字", "あ🍣é", "あ🍣é"},
		{"空白と <", " <", "<Space><lt>"},
		{"CR", "\r", "<CR>"},
		{"Tab", "\t", "<Tab>"},
		{"DEL は BS", "\x7f", "<BS>"},
		{"^H は BS", "\x08", "<BS>"},
		{"Ctrl の英字", "\x01\x17\x1a", "<C-a><C-w><C-z>"},
		{"^J は C-j", "\n", "<C-j>"},
		{"NUL は C-@", "\x00", "<C-@>"},
		{"^\\ ^] ^^ ^_", "\x1c\x1d\x1e\x1f", "<C-Bslash><C-]><C-^><C-_>"},
		{"ESC の単独", "\x1b", "<Esc>"},
		{"ESC ESC", "\x1b\x1b", "<Esc><Esc>"},

		{"tmux: 矢印", "\x1b[A\x1b[B\x1b[C\x1b[D", "<Up><Down><Right><Left>"},
		{"tmux: Home と End", "\x1b[1~\x1b[4~", "<Home><End>"},
		{"tmux: PageUp と PageDown", "\x1b[5~\x1b[6~", "<PageUp><PageDown>"},
		{"tmux: Delete", "\x1b[3~", "<Del>"},
		{"tmux: Insert", "\x1b[2~", "<Insert>"},
		{"tmux: F1", "\x1bOP", "<F1>"},
		{"tmux: F5 と F12", "\x1b[15~\x1b[24~", "<F5><F12>"},
		{"tmux: C-Up", "\x1b[1;5A", "<C-Up>"},
		{"tmux: S-Left", "\x1b[1;2D", "<S-Left>"},
		{"tmux: M-Right", "\x1b[1;3C", "<A-Right>"},
		{"tmux: C-Home", "\x1b[1;5H", "<C-Home>"},
		{"tmux: C-S-Left", "\x1b[1;6D", "<C-S-Left>"},
		{"tmux: BTab", "\x1b[Z", "<S-Tab>"},
		// tmux の M-a は ESC と文字で届く。本体は Alt を扱わないので、Vim と同じく <Esc> と文字の 2 つにする。
		{"tmux: M-a は Esc と a", "\x1ba", "<Esc>a"},
		{"tmux: M-C-a は Esc と C-a", "\x1b\x01", "<Esc><C-a>"},
		// <Esc> の直後に打ったキー（tmux の escape-time の間に届くと ESC とキーの組になる）。PR #111 のレビューの入力。
		{"インサートモードの x<Esc>j", "x\x1bj", "x<Esc>j"},
		{"ノーマルモードの <Esc>x", "\x1bx", "<Esc>x"},
		{"インサートモードの y<Esc>:", "y\x1b:", "y<Esc>:"},
		{"<Esc>O と続くキー（SS3 の列でない）", "\x1bOh", "<Esc>Oh"},
		{"ESC と不正な UTF-8 は Esc", "\x1b\xff", "<Esc>"},

		{"xterm: Home と End（通常のカーソルキーのモード）", "\x1b[H\x1b[F", "<Home><End>"},
		{"xterm: 矢印（アプリケーションのカーソルキーのモード）", "\x1bOA\x1bOB\x1bOC\x1bOD", "<Up><Down><Right><Left>"},
		{"xterm: Home と End（アプリケーションのモード）", "\x1bOH\x1bOF", "<Home><End>"},
		{"xterm: F1〜F4", "\x1bOP\x1bOQ\x1bOR\x1bOS", "<F1><F2><F3><F4>"},
		{"xterm: 修飾キーつきの F1", "\x1b[1;5P", "<C-F1>"},
		{"xterm: F6〜F10", "\x1b[17~\x1b[18~\x1b[19~\x1b[20~\x1b[21~", "<F6><F7><F8><F9><F10>"},
		{"xterm: F13〜F20", "\x1b[25~\x1b[26~\x1b[28~\x1b[29~\x1b[31~\x1b[32~\x1b[33~\x1b[34~", "<F13><F14><F15><F16><F17><F18><F19><F20>"},
		{"xterm: S-F5 と C-Insert", "\x1b[15;2~\x1b[2;5~", "<S-F5><C-Insert>"},
		{"xterm: C-Delete", "\x1b[3;5~", "<C-Del>"},
		{"xterm: S-PageUp", "\x1b[5;2~", "<S-PageUp>"},
		{"xterm: Meta は Alt", "\x1b[1;9A", "<A-Up>"},
		{"xterm: modifyOtherKeys の C-i", "\x1b[27;5;105~", "<C-i>"},
		{"xterm: modifyOtherKeys の C-S-a", "\x1b[27;6;65~", "<C-S-a>"},
		{"VTE: rxvt 形式の Home と End", "\x1b[7~\x1b[8~", "<Home><End>"},
		{"VTE: テンキーの Enter", "\x1bOM", "<CR>"},
		{"VTE: M-x と M-X は Esc と文字", "\x1bx\x1bX", "<Esc>x<Esc>X"},
		{"VTE: M-あ は Esc とあ", "\x1bあ", "<Esc>あ"},
		{"VTE: M-BS は Esc と BS", "\x1b\x7f", "<Esc><BS>"},

		{"CSI u: C-a", "\x1b[97;5u", "<C-a>"},
		{"CSI u: S-a は A", "\x1b[97;2u", "A"},
		{"CSI u: Esc", "\x1b[27u", "<Esc>"},
		{"CSI u: C-CR", "\x1b[13;5u", "<C-CR>"},
		{"CSI u: 私用の番号は捨てる", "\x1b[57399u", ""},
		{"CSI u: 絵文字", "\x1b[127843u", "🍣"},

		{"マウスの SGR の報告は捨てる", "\x1b[<0;10;5Ma", "a"},
		{"フォーカスの通知は捨てる", "\x1b[I\x1b[Ob", "b"},
		{"知らない CSI は捨てる", "\x1b[99~\x1b[5xc", "c"},
		{"CSI の途中の制御文字は壊れた列として捨てる", "\x1b[1\ra", "<CR>a"},
		{"不正な UTF-8 は捨てる", "a\xffb\xc3", "ab"},

		{"bracketed paste", "\x1b[200~a\x1bb\x1b[201~c", "abc"},
		{"bracketed paste の改行", "\x1b[200~a\r\nb\rc\nd\x1b[201~", "a<CR>b<CR>c<CR>d"},
		{"bracketed paste の Tab と制御文字", "\x1b[200~\ta\x03\x1b[201~", "<Tab>a"},
		{"bracketed paste の UTF-8", "\x1b[200~日本\x1b[201~", "日本"},
		{"bracketed paste の終わりの印の単独は捨てる", "\x1b[201~a", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeAll(tt.in); got != tt.want {
				t.Errorf("decode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestDecodeSplit は、列がどこで分かれて届いても、まとめて届いたときと同じキーになることを確かめる。
// 端末からの読み取りは、列の途中で切れて届くことがある。
func TestDecodeSplit(t *testing.T) {
	inputs := []string{
		"\x1b[1;5Aあx\x1b[3~",
		"\x1bOP\x1b[Zq",
		"\x1b[200~a\r\nいう\x1b[201~\x1b[A",
		"\x1ba🍣\x1b[97;5u",
	}
	for _, in := range inputs {
		want := decodeAll(in)
		for i := 1; i < len(in); i++ {
			var d Decoder
			var got []Decoded
			got = append(got, d.Feed([]byte(in[:i]))...)
			got = append(got, d.Feed([]byte(in[i:]))...)
			if d.Pending() {
				got = append(got, d.Flush()...)
			}
			if s := decodedString(got); s != want {
				t.Errorf("%q を %d バイト目で分けると %q, want %q", in, i, s, want)
			}
		}
	}
}

// TestDecodePending は、ESC で始まる列の途中で保留し、時間切れ（Flush）で確定させることを確かめる。
func TestDecodePending(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // Flush の結果
	}{
		{"ESC の単独は Esc", "\x1b", "<Esc>"},
		{"ESC [ は Esc と [", "\x1b[", "<Esc>["},
		{"ESC O は Esc と O", "\x1bO", "<Esc>O"},
		{"途中の CSI は Esc と残りの文字", "\x1b[1;5", "<Esc>[1;5"},
		{"途中の UTF-8 は捨てる", "\xe3\x81", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Decoder
			if got := d.Feed([]byte(tt.in)); len(got) != 0 {
				t.Fatalf("Feed(%q) = %q, want 保留", tt.in, decodedString(got))
			}
			if !d.Pending() {
				t.Fatal("Pending() = false, want true")
			}
			if got := decodedString(d.Flush()); got != tt.want {
				t.Errorf("Flush() = %q, want %q", got, tt.want)
			}
			if d.Pending() {
				t.Error("Flush の後も Pending() = true")
			}
		})
	}
}

// TestDecodePasteNoTimeout は、貼り付けの途中では保留の時間切れを使わないことを確かめる。
// 貼り付けの中身の ESC を、Esc キーとして実行しないためである。
func TestDecodePasteNoTimeout(t *testing.T) {
	var d Decoder
	got := d.Feed([]byte("\x1b[200~ab\x1b"))
	if d.Pending() {
		t.Error("貼り付けの途中で Pending() = true")
	}
	got = append(got, d.Feed([]byte("[201"))...)
	got = append(got, d.Feed([]byte("~:"))...)
	if s := decodedString(got); s != "ab:" {
		t.Errorf("got %q, want %q", s, "ab:")
	}
}

// TestDecodeLongPaste は、大きな貼り付けを細かく分けて届けても、文字を落とさず、保留が膨らまないことを確かめる。
func TestDecodeLongPaste(t *testing.T) {
	body := strings.Repeat("あいう\r\n", 2000)
	in := "\x1b[200~" + body + "\x1b[201~"
	var d Decoder
	var keys []editcmd.Key
	for chunk := range slices.Chunk([]byte(in), 7) {
		for _, x := range d.Feed(chunk) {
			keys = append(keys, x.Key)
		}
		if len(d.buf) > len(pasteEnd) {
			t.Fatalf("保留が %d バイト", len(d.buf))
		}
	}
	want := strings.Repeat("あいう<CR>", 2000)
	if got := editcmd.KeysString(keys); got != want {
		t.Errorf("貼り付けの結果が違う（長さ %d, want %d）", len(got), len(want))
	}
}

// FuzzDecoder は、どんなバイト列でも panic せず、分けて届けても結果が同じであることを確かめる。
func FuzzDecoder(f *testing.F) {
	for _, s := range []string{"\x1b[1;5A", "\x1b[200~a\r\n\x1b[201~", "\x1bあ", "\x1b[27;5;105~", "\xff\x1b["} {
		f.Add([]byte(s), 1)
	}
	f.Fuzz(func(t *testing.T, in []byte, split int) {
		whole := decodeAll(string(in))
		if len(in) == 0 {
			return
		}
		split = (split%len(in) + len(in)) % len(in)
		var d Decoder
		got := d.Feed(in[:split])
		got = append(got, d.Feed(in[split:])...)
		if d.Pending() {
			got = append(got, d.Flush()...)
		}
		// 分けた位置で保留が時間切れにならない限り、結果は同じになる。
		if s := decodedString(got); s != whole {
			t.Errorf("%q を %d で分けると %q, want %q", in, split, s, whole)
		}
	})
}
