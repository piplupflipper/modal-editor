package term

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// Decoded は、端末から読んだバイト列を解読した 1 件である。キー 1 つか、貼り付けたテキスト 1 つである。
type Decoded struct {
	// Key は、解読したキーである。正規化済み（editcmd.Key.Normalize）である。Paste が空でなければ読まない。
	Key editcmd.Key
	// Paste は、bracketed paste で貼り付けたテキストである。空でなければ、この 1 件は貼り付けである。
	// 改行は "\n" にそろえてあり、"\n" と "\t" の他の制御文字と、不正な UTF-8 は含まない。中身の無い貼り付けは送らない。
	Paste string
}

// 制御のバイトと、bracketed paste の始まりと終わりの印。
const (
	esc = 0x1b
	// maxSeqLen は、CSI の列の長さの上限である。これを超えても終わりの文字が来なければ、壊れた列として捨てる。
	maxSeqLen = 64
)

var pasteEnd = []byte("\x1b[201~")

// Decoder は、端末から読んだバイト列をキーへ解読する。ゼロ値で使える。
//
// 端末は、矢印キーなどを ESC で始まる列で送り、Esc キーの単独の押下も ESC の 1 バイトで送る。
// 両者は、ESC の後に続くバイトが時間内に届くかで区別する（Vim の 'ttimeout' と同じ考え方）。
// Feed は、ESC で始まる列が途中で切れていたら、残りが届くまで解読を保留する（Pending が true になる）。
// 呼び出し側は、保留が一定の時間（Terminal では Options.EscTimeout）続いたら Flush を呼び、保留中のバイトを確定させる。
//
// 解読の規則は次のとおりである。
//
//   - 0x0d は <CR>、0x09 は <Tab>、0x08 と 0x7f は <BS>（internal/command のレジスターのキーの読み方と同じ）、
//     0x00 は <C-@>、0x0a は <C-j>、その他の 0x01〜0x1a は <C-a>〜<C-z>、0x1c〜0x1f は <C-\>、<C-]>、<C-^>、<C-_>
//   - ESC の後に CSI と SS3 の列でないキーが続けば、<Esc> とそのキーの 2 つにする（Vim と Neovim と同じ）。
//     xterm の metaSendsEscape の Alt のキー（ESC と文字）は Alt にしない。Alt は CSI の修飾キーの引数でだけ付く
//   - CSI（ESC [）と SS3（ESC O）の列は、矢印、Home、End、PageUp、PageDown、Delete、Shift-Tab、修飾キーの付いたそれらと、
//     Insert、F1〜F20、CSI u と CSI 27 ; 修飾 ; 文字 ~ の形の文字のキーを解読する。
//     それ以外の列（マウス、フォーカスの通知など）は捨てる
//   - bracketed paste（ESC [ 200 ~ から ESC [ 201 ~ まで）の中身は、キーにせず、終わりの印までをまとめて 1 件の Decoded.Paste にする
//     （#114。Vim と同じく、ノーマルモードで貼った文字列をコマンドとして実行しないため）。
//     "\r\n"、"\r"、"\n" は "\n" 1 つにし、"\t" は残し、それ以外の制御文字は捨てる（貼り付けた ^C で中断しないため）。
//     貼り付けの中では保留の時間切れを使わない
//   - 不正な UTF-8 のバイトは捨てる
type Decoder struct {
	buf []byte
	// paste は、bracketed paste の中身を読んでいる途中かどうかである。
	paste bool
	// pasteCR は、貼り付けの中で、直前に読んだ文字が '\r' だったかどうかである（"\r\n" を 1 つの改行にする）。
	pasteCR bool
	// pasted は、読んでいる途中の貼り付けの、ここまでのテキストである。
	pasted []byte
}

// Feed は、端末から読んだバイト列 b を足して解読し、確定したキーを返す。
// 途中で切れた列は保留し、次の Feed か Flush で解読する。
func (d *Decoder) Feed(b []byte) []Decoded {
	d.buf = append(d.buf, b...)
	return d.decode(false)
}

// Pending は、ESC で始まる列か UTF-8 の文字が途中で切れていて、保留しているバイトがあるかを返す。
// bracketed paste の中身を読んでいる途中は false を返す（貼り付けの終わりの印まで待つ）。
func (d *Decoder) Pending() bool { return !d.paste && len(d.buf) > 0 }

// Flush は、保留しているバイトを、続きが来ないものとして確定させ、キーを返す。
// ESC の単独は <Esc>、途中で切れた ESC [ と ESC O の列は <Esc> と、続く文字にする。途中で切れた UTF-8 は捨てる。
// 貼り付けの途中なら、終わりの印が来ないもの（入力の終わり）として、ここまでのテキストを貼り付けとして返す。
func (d *Decoder) Flush() []Decoded {
	out := d.decode(true)
	if d.paste {
		d.pasteText(d.buf, true)
		d.buf = nil
		out = d.endPaste(out)
	}
	return out
}

// endPaste は、貼り付けの状態を抜け、ここまでのテキストが空でなければ out に足して返す。
func (d *Decoder) endPaste(out []Decoded) []Decoded {
	if len(d.pasted) > 0 {
		out = append(out, Decoded{Paste: string(d.pasted)})
	}
	d.paste, d.pasteCR, d.pasted = false, false, nil
	return out
}

func (d *Decoder) decode(flush bool) []Decoded {
	var out []Decoded
	for len(d.buf) > 0 {
		var n int
		if d.paste {
			n = d.pasteChunk(&out)
		} else {
			n = d.one(&out, flush)
		}
		if n == 0 {
			break
		}
		d.buf = d.buf[n:]
	}
	if len(d.buf) == 0 {
		d.buf = nil
	} else {
		// 保留する分だけを新しい配列へ写し、読み終えた前の部分を手放す。
		d.buf = append([]byte(nil), d.buf...)
	}
	return out
}

// one は、d.buf の先頭のキー 1 つ（または貼り付けの始まりの印）を解読して out に足し、読んだバイト数を返す。
// 続きが届いていなければ 0 を返す。flush が true なら 0 を返さない。
func (d *Decoder) one(out *[]Decoded, flush bool) int {
	b := d.buf
	if b[0] != esc {
		k, ok, n := plainKey(b, flush)
		if ok {
			*out = append(*out, Decoded{Key: k})
		}
		return n
	}
	if len(b) == 1 {
		if !flush {
			return 0
		}
		*out = append(*out, Decoded{Key: editcmd.Esc})
		return 1
	}
	switch b[1] {
	case '[':
		dec, kind, n := parseCSI(b)
		if n == 0 {
			if !flush {
				return 0
			}
			// 時間切れ: ESC は単独の <Esc> とし、'[' と残りは次の周回で文字として読む。
			*out = append(*out, Decoded{Key: editcmd.Esc})
			return 1
		}
		switch kind {
		case csiKey:
			*out = append(*out, dec)
		case csiPasteStart:
			d.paste, d.pasteCR = true, false
		case csiIgnored:
		}
		return n
	case 'O':
		if len(b) == 2 {
			if !flush {
				return 0
			}
			*out = append(*out, Decoded{Key: editcmd.Esc})
			return 1
		}
		if dec, ok := ss3Key(b[2]); ok {
			*out = append(*out, dec)
			return 3
		}
		// SS3 の列でなければ、<Esc> の直後に O と次のキーを打ったもの（インサートモードを抜けて O で行を開く）とする。
		*out = append(*out, Decoded{Key: editcmd.Esc})
		return 1
	}
	// ESC の後に列でないキーが続いたら、<Esc> と、そのキーの 2 つにする（Vim と Neovim と同じ）。
	// 本体は Alt の付いたキーを扱わないので、<A-x> にすると Esc もキーも失われる（ix<Esc>j を速く打つとインサートモードに残る）。
	// tmux は、外側の端末から Esc を受けて escape-time（既定 500 ms）の間に届いたキーを ESC とキーの組で送るので、
	// 人が <Esc> の直後に打ったキーもこの形で届く。
	*out = append(*out, Decoded{Key: editcmd.Esc})
	return 1
}

// ctrlKey は、Ctrl つきの文字 r のキーを返す。
func ctrlKey(r rune) editcmd.Key {
	return editcmd.Key{Code: editcmd.CodeRune, Rune: r, Mod: editcmd.ModCtrl}.Normalize()
}

// plainKey は、ESC 以外で始まる b の先頭の 1 キーを解読する。
// ok が false でも n が正なら、n バイトを捨てる（不正な UTF-8）。n が 0 なら、UTF-8 の文字の続きが届いていない。
func plainKey(b []byte, flush bool) (k editcmd.Key, ok bool, n int) {
	c := b[0]
	switch {
	case c == '\r':
		return editcmd.Special(editcmd.CodeEnter), true, 1
	case c == '\t':
		return editcmd.Special(editcmd.CodeTab), true, 1
	case c == 0x08 || c == 0x7f:
		return editcmd.Special(editcmd.CodeBackspace), true, 1
	case c == 0x00:
		return ctrlKey('@'), true, 1
	case c >= 0x01 && c <= 0x1a:
		return ctrlKey(rune('a' + c - 1)), true, 1
	case c == esc:
		return editcmd.Esc, true, 1
	case c >= 0x1c && c <= 0x1f:
		return ctrlKey(rune("\\]^_"[c-0x1c])), true, 1
	case c < utf8.RuneSelf:
		return editcmd.Rune(rune(c)), true, 1
	}
	if !utf8.FullRune(b) {
		if !flush {
			return editcmd.Key{}, false, 0
		}
		return editcmd.Key{}, false, 1
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 {
		return editcmd.Key{}, false, 1
	}
	return editcmd.Rune(r), true, size
}

// csiKind は、CSI の列の解読の結果の種類である。
type csiKind int

const (
	csiIgnored    csiKind = iota // 捨てる列（知らない列、マウス、貼り付けの終わりの印の単独など）
	csiKey                       // キー（Decoded）
	csiPasteStart                // 貼り付けの始まりの印
)

// parseCSI は、ESC [ で始まる b の先頭の CSI の列を解読し、列のバイト数を返す。終わりの文字がまだ届いていなければ 0 を返す。
// 列の形は ECMA-48 に従う（引数のバイト 0x30〜0x3f、中間のバイト 0x20〜0x2f、終わりの文字 0x40〜0x7e）。
// 形に合わないバイトが来たら、その手前までを壊れた列として捨てる。
func parseCSI(b []byte) (Decoded, csiKind, int) {
	// 上限の長さで打ち切る。届き方（列のどこで分かれたか）に関わらず、同じ位置で切るためである。
	end := min(len(b), maxSeqLen)
	i := 2
	for i < end && b[i] >= 0x30 && b[i] <= 0x3f {
		i++
	}
	for i < end && b[i] >= 0x20 && b[i] <= 0x2f {
		i++
	}
	if i == maxSeqLen {
		return Decoded{}, csiIgnored, i
	}
	if i >= len(b) {
		return Decoded{}, csiIgnored, 0
	}
	final := b[i]
	if final < 0x40 || final > 0x7e {
		return Decoded{}, csiIgnored, i
	}
	n := i + 1
	params := string(b[2:i])
	if params != "" && strings.ContainsRune("<=>?", rune(params[0])) {
		// 私用の引数（マウスの SGR の報告、端末の属性の応答など）は、キーではない。
		return Decoded{}, csiIgnored, n
	}
	if strings.ContainsFunc(params, func(r rune) bool { return r >= 0x20 && r <= 0x2f }) {
		return Decoded{}, csiIgnored, n
	}
	ps := csiParams(params)
	param := func(j, def int) int {
		if j < len(ps) && ps[j] >= 0 {
			return ps[j]
		}
		return def
	}
	mod := modOf(param(1, 1))
	switch final {
	case 'A', 'B', 'C', 'D', 'H', 'F':
		return keyDecoded(cursorCode(final), mod), csiKey, n
	case 'Z':
		return keyDecoded(editcmd.CodeTab, mod|editcmd.ModShift), csiKey, n
	case 'P', 'Q', 'R', 'S':
		return fkeyDecoded(int(final-'P')+1, mod), csiKey, n
	case 'u':
		dec, ok := codepointKey(param(0, -1), modOf(param(1, 1)))
		if !ok {
			return Decoded{}, csiIgnored, n
		}
		return dec, csiKey, n
	case '~':
		return tildeKey(ps, param, mod, n)
	}
	return Decoded{}, csiIgnored, n
}

// tildeKey は、CSI 数 ~ の形の列を解読する。
func tildeKey(ps []int, param func(int, int) int, mod editcmd.Mod, n int) (Decoded, csiKind, int) {
	if len(ps) == 0 {
		return Decoded{}, csiIgnored, n
	}
	switch p := param(0, 0); p {
	case 1, 7:
		return keyDecoded(editcmd.CodeHome, mod), csiKey, n
	case 4, 8:
		return keyDecoded(editcmd.CodeEnd, mod), csiKey, n
	case 2:
		return keyDecoded(editcmd.CodeInsert, mod), csiKey, n
	case 3:
		return keyDecoded(editcmd.CodeDelete, mod), csiKey, n
	case 5:
		return keyDecoded(editcmd.CodePageUp, mod), csiKey, n
	case 6:
		return keyDecoded(editcmd.CodePageDown, mod), csiKey, n
	case 200:
		return Decoded{}, csiPasteStart, n
	case 27:
		// xterm の modifyOtherKeys: CSI 27 ; 修飾 ; 文字 ~
		dec, ok := codepointKey(param(2, -1), mod)
		if !ok {
			return Decoded{}, csiIgnored, n
		}
		return dec, csiKey, n
	default:
		if f, ok := fkeyTilde[p]; ok {
			return fkeyDecoded(f, mod), csiKey, n
		}
	}
	return Decoded{}, csiIgnored, n
}

// fkeyTilde は、CSI 数 ~ の数から F キーの番号への対応である（xterm と VTE の既定）。
var fkeyTilde = map[int]int{
	11: 1, 12: 2, 13: 3, 14: 4, 15: 5,
	17: 6, 18: 7, 19: 8, 20: 9, 21: 10,
	23: 11, 24: 12, 25: 13, 26: 14, 28: 15, 29: 16,
	31: 17, 32: 18, 33: 19, 34: 20,
}

// csiParams は、CSI の引数を ';' で分けた数の列にする。':' で区切った下位の引数は、最初の値だけを使う。
// 空の引数は -1 にする（呼び出し側が既定値に置き換える）。
func csiParams(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ";")
	ps := make([]int, len(parts))
	for i, p := range parts {
		p, _, _ = strings.Cut(p, ":")
		v, err := strconv.Atoi(p)
		if err != nil || p == "" {
			v = -1
		}
		ps[i] = v
	}
	return ps
}

// modOf は、xterm の修飾キーの引数（1 + Shift 1、Alt 2、Ctrl 4、Meta 8 の和）を editcmd.Mod にする。Meta は Alt として扱う。
func modOf(p int) editcmd.Mod {
	m := p - 1
	if m <= 0 {
		return 0
	}
	var mod editcmd.Mod
	if m&1 != 0 {
		mod |= editcmd.ModShift
	}
	if m&(2|8) != 0 {
		mod |= editcmd.ModAlt
	}
	if m&4 != 0 {
		mod |= editcmd.ModCtrl
	}
	return mod
}

// cursorCode は、CSI と SS3 の矢印、Home、End の終わりの文字をキーの種類にする。
func cursorCode(final byte) editcmd.Code {
	switch final {
	case 'A':
		return editcmd.CodeUp
	case 'B':
		return editcmd.CodeDown
	case 'C':
		return editcmd.CodeRight
	case 'D':
		return editcmd.CodeLeft
	case 'H':
		return editcmd.CodeHome
	default:
		return editcmd.CodeEnd
	}
}

func keyDecoded(c editcmd.Code, mod editcmd.Mod) Decoded {
	return Decoded{Key: editcmd.Key{Code: c, Mod: mod}.Normalize()}
}

// fkeyDecoded は、n 番の F キーを返す。n は 1 から editcmd.MaxFunctionKey の範囲である（呼び出し側の表がそう作る）。
func fkeyDecoded(n int, mod editcmd.Mod) Decoded {
	c, _ := editcmd.FunctionKey(n)
	return keyDecoded(c, mod)
}

// codepointKey は、CSI u と modifyOtherKeys の文字の番号と修飾キーを、キーにする。
// 番号が制御文字の Tab、CR、Esc、BS なら特殊キーにする。私用領域の番号（kitty の機能キーの 57344〜63743）は捨てる。
func codepointKey(cp int, mod editcmd.Mod) (Decoded, bool) {
	switch {
	case cp == 9:
		return keyDecoded(editcmd.CodeTab, mod), true
	case cp == 13:
		return keyDecoded(editcmd.CodeEnter, mod), true
	case cp == 27:
		return keyDecoded(editcmd.CodeEsc, mod), true
	case cp == 8 || cp == 127:
		return keyDecoded(editcmd.CodeBackspace, mod), true
	case cp < 0x20 || cp > utf8.MaxRune || cp >= 0xe000 && cp <= 0xf8ff:
		return Decoded{}, false
	}
	r := rune(cp)
	if !utf8.ValidRune(r) {
		return Decoded{}, false
	}
	k := editcmd.Key{Code: editcmd.CodeRune, Rune: r, Mod: mod}
	return Decoded{Key: k.Normalize()}, true
}

// ss3Key は、ESC O の後の 1 文字を解読する（端末をアプリケーションのカーソルキーのモードにしたときの矢印と、F1〜F4）。
func ss3Key(c byte) (Decoded, bool) {
	switch c {
	case 'A', 'B', 'C', 'D', 'H', 'F':
		return keyDecoded(cursorCode(c), 0), true
	case 'P', 'Q', 'R', 'S':
		return fkeyDecoded(int(c-'P')+1, 0), true
	case 'M':
		return keyDecoded(editcmd.CodeEnter, 0), true
	}
	return Decoded{}, false
}

// pasteChunk は、貼り付けの中身を読める所まで読んでテキストに足し、読んだバイト数を返す。
// 終わりの印を読んだら、貼り付けの状態を抜け、テキストを 1 件の貼り付けとして out に足す。
func (d *Decoder) pasteChunk(out *[]Decoded) int {
	if end := bytes.Index(d.buf, pasteEnd); end >= 0 {
		d.pasteText(d.buf[:end], true)
		*out = d.endPaste(*out)
		return end + len(pasteEnd)
	}
	// 終わりの印の前半かもしれない末尾は残す。
	limit := len(d.buf) - partialSuffix(d.buf, pasteEnd)
	return d.pasteText(d.buf[:limit], false)
}

// pasteText は、貼り付けの中身 b を、改行をそろえ制御文字を除いて d.pasted に足し、読んだバイト数を返す。
// final が false なら、末尾の途中で切れた UTF-8 の文字は読まずに残す。
func (d *Decoder) pasteText(b []byte, final bool) int {
	i := 0
	for i < len(b) {
		c := b[i]
		cr := d.pasteCR
		d.pasteCR = false
		switch {
		case c == '\r':
			d.pasted = append(d.pasted, '\n')
			d.pasteCR = true
			i++
			continue
		case c == '\n':
			if !cr {
				d.pasted = append(d.pasted, '\n')
			}
			i++
			continue
		case c == '\t':
			d.pasted = append(d.pasted, '\t')
			i++
			continue
		case c < 0x20 || c == 0x7f:
			i++
			continue
		case c < utf8.RuneSelf:
			d.pasted = append(d.pasted, c)
			i++
			continue
		}
		if !utf8.FullRune(b[i:]) {
			if !final {
				d.pasteCR = cr
				return i
			}
			return len(b)
		}
		r, size := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || size > 1 {
			d.pasted = append(d.pasted, b[i:i+size]...)
		}
		i += size
	}
	return i
}

// partialSuffix は、b の末尾のうち、mark の先頭と一致する最も長い部分（mark そのものより短いもの）の長さを返す。
func partialSuffix(b, mark []byte) int {
	for n := min(len(b), len(mark)-1); n > 0; n-- {
		if bytes.Equal(b[len(b)-n:], mark[:n]) {
			return n
		}
	}
	return 0
}
