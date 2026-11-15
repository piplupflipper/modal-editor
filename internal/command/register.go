package command

import (
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/input"
)

// Register は、レジスター 1 つの内容である（FR-04）。
//
// Linewise が true なら行単位のテキストで、Text は行を "\n" でつないだもの（最後の行の後の改行は含めない）である。
// Vim の getreg() が返す行単位のテキストは最後に改行が付くが、ここでは付けない。
// false なら文字単位のテキストで、Text の "\n" は行の区切りである。
type Register struct {
	Text     string
	Linewise bool

	// keys は、q で記録したキーの列である。Text からキーへ戻すと失われるキー（矢印キーなど）を保つ。
	// 記録したもの以外では nil である。
	keys []input.Key
}

// empty は、内容が無いかを返す。
func (r Register) empty() bool { return r.Text == "" && !r.Linewise && len(r.keys) == 0 }

// appendTo は、r の後に add を追記した内容を返す（"A のような大文字の名前への書き込み）。
// どちらかが行単位なら、結果は行単位になり、間に改行を入れる（Vim と同じ）。
func (r Register) appendTo(add Register) Register {
	if r.empty() {
		return add
	}
	out := Register{Linewise: r.Linewise || add.Linewise}
	sep := ""
	if out.Linewise {
		sep = "\n"
	}
	out.Text = r.Text + sep + add.Text
	if r.keys != nil && add.keys != nil {
		out.keys = append(slices.Clone(r.keys), add.keys...)
	}
	return out
}

// Registers は、レジスターの集合である（FR-04）。ゼロ値は全て空のレジスターである。
//
// 名前と働きは Vim に合わせる。
//
//	"      無名のレジスター。最後に書き込んだレジスターの内容を指す
//	a〜z   名前付きのレジスター。A〜Z で書き込むと追記する
//	0      直前のヤンクの内容
//	1〜9   削除の履歴。行をまたぐ削除と行単位の削除で 1 に入り、それまでの内容は 1 つずつずれる
//	-      1 行の中の小さな削除
//	_      ブラックホール。書き込んでも何も残らず、読むと空である
//	.      直前にインサートモードで入力したテキスト（読み出しだけ）
//	:      直前に実行したコマンドライン（読み出しだけ）
//
// クリップボード（+、*）は後回しで、読み書きすると失敗する。
//
// req: FR-04
type Registers struct {
	unnamed  Register
	named    [26]Register
	numbered [10]Register
	small    Register
	inserted string
	cmdline  string
}

// Get は、名前が name のレジスターの内容を返す。name が 0 なら無名のレジスターである。
// 読めない名前なら false を返す。
func (r *Registers) Get(name rune) (Register, bool) {
	switch {
	case name == 0 || name == '"':
		return r.unnamed, true
	case name >= 'a' && name <= 'z':
		return r.named[name-'a'], true
	case name >= 'A' && name <= 'Z':
		return r.named[name-'A'], true
	case name >= '0' && name <= '9':
		return r.numbered[name-'0'], true
	}
	switch name {
	case '-':
		return r.small, true
	case '_':
		return Register{}, true
	case '.':
		return Register{Text: r.inserted}, true
	case ':':
		return Register{Text: r.cmdline}, true
	}
	return Register{}, false
}

// Set は、名前が name のレジスターに reg を書き込む。大文字の名前は追記する。
// 無名のレジスターは、書き込んだレジスターの内容を指すようになる。
// 書き込めない名前（.、:、+、* など）なら false を返す。
func (r *Registers) Set(name rune, reg Register) bool {
	p := r.slot(name)
	if p == nil {
		return name == '_'
	}
	if name >= 'A' && name <= 'Z' {
		reg = p.appendTo(reg)
	}
	*p = reg
	r.unnamed = reg
	return true
}

// slot は、書き込める名前のレジスターを指すポインターを返す。書き込めない名前と _ では nil を返す。
func (r *Registers) slot(name rune) *Register {
	switch {
	case name == 0 || name == '"':
		return &r.unnamed
	case name >= 'a' && name <= 'z':
		return &r.named[name-'a']
	case name >= 'A' && name <= 'Z':
		return &r.named[name-'A']
	case name >= '0' && name <= '9':
		return &r.numbered[name-'0']
	case name == '-':
		return &r.small
	}
	return nil
}

// writable は、オペレーターの書き込み先にできる名前であるかを返す。
func writable(name rune) bool {
	return name == 0 || name == '_' || (&Registers{}).slot(name) != nil
}

// yank は、ヤンクした内容を書き込む。名前が無ければ 0 と無名のレジスターに入れる。
func (r *Registers) yank(name rune, reg Register) {
	if name == 0 || name == '"' {
		r.numbered[0] = reg
		r.unnamed = reg
		return
	}
	r.Set(name, reg)
}

// delete は、削除した内容を書き込む。
//
// 名前が無ければ、small（1 行の中の削除）なら - に、そうでなければ 1〜9 をずらして 1 に入れる。
// 名前があれば、その名前に入れ、small でなければ 1 にも入れる。
// forceOne は、%、{、} などのモーションの削除で、1 行の中でも 1 に入れることを表す（Vim と同じ）。
// _ には何も書かず、無名のレジスターも変えない。
func (r *Registers) delete(name rune, reg Register, small, forceOne bool) {
	if name == '_' {
		return
	}
	if !small || forceOne {
		copy(r.numbered[2:], r.numbered[1:9])
		r.numbered[1] = reg
	}
	switch {
	case name != 0 && name != '"':
		r.Set(name, reg)
		return
	case small:
		r.small = reg
	}
	r.unnamed = reg
}

// rawText は、マクロとして記録したキーの列を、Vim のレジスターと同じ生のテキストにする。
// <Esc> は "\x1b"、<CR> は "\r"、<Tab> は "\t"、<BS> は "\x08"、Ctrl つきの英字は制御文字にする。
// 生の文字にできないキー（矢印キーなど）は、Vim の表記（<Up> など）で書く。
func rawText(keys []input.Key) string {
	var sb strings.Builder
	for _, k := range keys {
		switch {
		case k.Code == input.CodeRune && k.Mod == 0:
			sb.WriteRune(k.Rune)
		case k.Code == input.CodeRune && k.Mod == input.ModCtrl && k.Rune >= 'a' && k.Rune <= 'z':
			sb.WriteRune(k.Rune - 'a' + 1)
		case k.Mod == 0 && k.Code == input.CodeEsc:
			sb.WriteByte(0x1b)
		case k.Mod == 0 && k.Code == input.CodeEnter:
			sb.WriteByte('\r')
		case k.Mod == 0 && k.Code == input.CodeTab:
			sb.WriteByte('\t')
		case k.Mod == 0 && k.Code == input.CodeBackspace:
			sb.WriteByte(0x08)
		default:
			sb.WriteString(k.String())
		}
	}
	return sb.String()
}

// keysFromText は、レジスターのテキストをキーの列にする（rawText の逆）。
// "\n" は <CR> として読む（行単位のレジスターを @ で再生するとき、行の区切りは <CR> に当たる）。
func keysFromText(text string) []input.Key {
	keys := make([]input.Key, 0, len(text))
	for _, r := range text {
		switch {
		case r == 0x1b:
			keys = append(keys, input.Special(input.CodeEsc))
		case r == '\r' || r == '\n':
			keys = append(keys, input.Special(input.CodeEnter))
		case r == '\t':
			keys = append(keys, input.Special(input.CodeTab))
		case r == 0x08 || r == 0x7f:
			keys = append(keys, input.Special(input.CodeBackspace))
		case r >= 1 && r <= 26:
			keys = append(keys, input.Key{Code: input.CodeRune, Rune: 'a' + r - 1, Mod: input.ModCtrl}.Normalize())
		default:
			keys = append(keys, input.Rune(r))
		}
	}
	return keys
}

// macroKeys は、レジスターの内容を、再生するキーの列にする。
func (r Register) macroKeys() []input.Key {
	if r.keys != nil {
		return r.keys
	}
	text := r.Text
	if r.Linewise {
		text += "\n"
	}
	return keysFromText(text)
}
