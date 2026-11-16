// Package editcmd は、キー入力（Key）と、キーの列から解釈した編集コマンドの値（Command）の型を持つ（基本設計 3 章）。
//
// 基本設計 2 章の土台の層にあり、このリポジトリの他のパッケージを import しない。
// キーの列を値に解釈する internal/input と、値を実行する internal/command は同じ層にあり、互いを import しないので、
// 両方が使う型をここに置く。このパッケージは型と、その表記の読み書きだけを持ち、解釈と実行はしない。
package editcmd

import (
	"fmt"
	"strings"
	"unicode"
)

// Code は、キーの種類を表す。
// 文字のキーは CodeRune で、文字そのものは Key.Rune に入る。
type Code int

// キーの種類。
const (
	CodeRune Code = iota // 文字のキー
	CodeEsc
	CodeEnter
	CodeTab
	CodeBackspace
	CodeDelete
	CodeUp
	CodeDown
	CodeLeft
	CodeRight
	CodeHome
	CodeEnd
	CodePageUp
	CodePageDown
	CodeInsert
	// CodeF1 は F1 キーである。F2 から F{MaxFunctionKey} までは CodeF1 に続く値で、FunctionKey で引く。
	CodeF1
)

// MaxFunctionKey は、キーの種類を持つ F キーの最大の番号である。
// 端末の解読（internal/term）が読む xterm と VTE の F キーの範囲（F1〜F20）に合わせる。
const MaxFunctionKey = 20

// FunctionKey は、n 番の F キー（F1 なら 1）の種類を返す。n が 1 から MaxFunctionKey の範囲になければ ok は false である。
func FunctionKey(n int) (c Code, ok bool) {
	if n < 1 || n > MaxFunctionKey {
		return 0, false
	}
	return CodeF1 + Code(n-1), true
}

func init() {
	for n := 1; n <= MaxFunctionKey; n++ {
		c, _ := FunctionKey(n)
		codeNames[c] = fmt.Sprintf("F%d", n)
	}
}

// codeNames は、特殊キーの表記（<Esc> の Esc の部分）である。F キーの名前（F1〜F20）は init で足す。
// ParseKeys と Key.String がこの表を共有する。
var codeNames = map[Code]string{
	CodeEsc:       "Esc",
	CodeEnter:     "CR",
	CodeTab:       "Tab",
	CodeBackspace: "BS",
	CodeDelete:    "Del",
	CodeUp:        "Up",
	CodeDown:      "Down",
	CodeLeft:      "Left",
	CodeRight:     "Right",
	CodeHome:      "Home",
	CodeEnd:       "End",
	CodePageUp:    "PageUp",
	CodePageDown:  "PageDown",
	CodeInsert:    "Insert",
}

// runeNames は、文字のキーのうち <...> で書く名前である（Vim と同じ）。
// Key.String は、修飾キーの無い ' ' と '<' だけをこの名前で書き、'|' と '\' はそのまま書く。
var runeNames = map[rune]string{
	' ':  "Space",
	'<':  "lt",
	'|':  "Bar",
	'\\': "Bslash",
}

// Mod は、修飾キーの組み合わせを表すビットの集合である。
// 文字のキーの修飾キーは Key.Normalize の規則で正規化する。
type Mod uint8

// 修飾キー。
const (
	ModCtrl Mod = 1 << iota
	ModAlt
	ModShift
)

// Key は、1 回のキー入力を表す。
// 端末ライブラリに依存しない値で、比較（==）とマップのキーに使える。
// 比較の前に Normalize で正規化しておく（ParseKeys と Rune、Special が返す値は正規化済み）。
// マクロはこの型の列として記録し、再生のときは 1 個ずつ Parser.Feed へ渡す。
//
// req: FR-05
type Key struct {
	Code Code
	Rune rune // Code が CodeRune のときの文字。それ以外では 0
	Mod  Mod
}

// Rune は、修飾キーの無い文字のキーを返す。
func Rune(r rune) Key { return Key{Code: CodeRune, Rune: r} }

// Special は、修飾キーの無い特殊キーを返す。
func Special(c Code) Key { return Key{Code: c} }

// Esc は、Esc キーである。
var Esc = Special(CodeEsc)

// Normalize は、同じ入力を表す Key を 1 つの値にそろえて返す。端末のバイト列の解読もこの規則に従う。
//
//   - Ctrl つきの英字は小文字にする（<C-W> と <C-w> は同じキー）。Shift はそのまま残す
//   - Ctrl の無い Shift つきの英字は、大文字にして Shift を外す（<S-a> は A）
//   - 英字でない文字の Shift と、特殊キーの修飾キーはそのまま残す
func (k Key) Normalize() Key {
	if k.Code != CodeRune || !unicode.IsLetter(k.Rune) {
		return k
	}
	switch {
	case k.Mod&ModCtrl != 0:
		k.Rune = unicode.ToLower(k.Rune)
	case k.Mod&ModShift != 0:
		k.Rune = unicode.ToUpper(k.Rune)
		k.Mod &^= ModShift
	}
	return k
}

// Plain は、修飾キーの無い文字のキーであるかを返す。
// レジスター名と回数には、この種類のキーだけを受け付ける。
func (k Key) Plain() bool { return k.Code == CodeRune && k.Mod == 0 }

// String は、キーを Vim の表記で返す（例: a、<Esc>、<C-w>、<lt>、<Space>）。
// 結果を ParseKeys に渡すと元のキーに戻る。
func (k Key) String() string {
	var name string
	if k.Code == CodeRune {
		if k.Mod == 0 && k.Rune != ' ' && k.Rune != '<' {
			return string(k.Rune)
		}
		name = string(k.Rune)
		if n, ok := runeNames[k.Rune]; ok {
			name = n
		}
		if k.Mod == 0 {
			return "<" + name + ">"
		}
	} else {
		var ok bool
		if name, ok = codeNames[k.Code]; !ok {
			name = fmt.Sprintf("Code%d", int(k.Code))
		}
	}
	var b strings.Builder
	b.WriteByte('<')
	if k.Mod&ModCtrl != 0 {
		b.WriteString("C-")
	}
	if k.Mod&ModAlt != 0 {
		b.WriteString("A-")
	}
	if k.Mod&ModShift != 0 {
		b.WriteString("S-")
	}
	b.WriteString(name)
	b.WriteByte('>')
	return b.String()
}

// KeysString は、キーの列を Vim の表記で連ねた文字列を返す。
func KeysString(keys []Key) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k.String())
	}
	return b.String()
}

// ParseKeys は、Vim の表記のキーの列（例: "d2w"、"d<Esc>"、"<C-w>j"、"<Space>w"）をキーの列にする。
// キーマップの表、拡張機能の宣言の keys、利用者の設定で使う。返すキーは正規化済みである。
//
// '<' から次の '>' までを、修飾キー（C-、A-、M-、S-）と名前として読む。
// 名前は特殊キー（Esc、CR、Tab など）、Space、lt、Bar、Bslash、または修飾キーつきの 1 文字である。
// 大文字と小文字は区別しない。名前が分からない場合（<x>、<Foo>）はエラーを返す。
// 後ろに '>' が無い '<' は、ただの文字として読む。このため "<" と "<<" はそのまま書ける。
func ParseKeys(s string) ([]Key, error) {
	var keys []Key
	rs := []rune(s)
	for i := 0; i < len(rs); {
		if rs[i] != '<' {
			keys = append(keys, Rune(rs[i]))
			i++
			continue
		}
		k, n, err := parseAngle(rs[i:])
		if err != nil {
			return nil, fmt.Errorf("キーの表記 %q: %w", s, err)
		}
		if n == 0 {
			keys = append(keys, Rune('<'))
			i++
			continue
		}
		keys = append(keys, k)
		i += n
	}
	return keys, nil
}

// MustParseKeys は ParseKeys と同じで、エラーの場合は panic する。本体の表とテストのためにある。
func MustParseKeys(s string) []Key {
	keys, err := ParseKeys(s)
	if err != nil {
		panic(err)
	}
	return keys
}

// parseAngle は、'<' で始まる rs の先頭の <...> を 1 つのキーとして読み、読んだ文字数を返す。
// 後ろに '>' が無ければ n は 0 で、'<' はただの文字である。
func parseAngle(rs []rune) (k Key, n int, err error) {
	j := 1
	var mod Mod
	for j+2 < len(rs) && rs[j+1] == '-' {
		m, ok := modOf(rs[j])
		if !ok {
			break
		}
		mod |= m
		j += 2
	}
	var name string
	if j+1 < len(rs) && rs[j+1] == '>' {
		// 1 文字の名前。'>' 自身も名前になれる（<C->>）
		name, n = string(rs[j]), j+2
	} else {
		end := -1
		for x := j; x < len(rs); x++ {
			if rs[x] == '>' {
				end = x
				break
			}
		}
		if end < 0 {
			return Key{}, 0, nil
		}
		name, n = string(rs[j:end]), end+1
	}
	raw := string(rs[:n])
	for c, cn := range codeNames {
		if strings.EqualFold(name, cn) {
			return Key{Code: c, Mod: mod}, n, nil
		}
	}
	for r, rn := range runeNames {
		if strings.EqualFold(name, rn) {
			return Key{Code: CodeRune, Rune: r, Mod: mod}.Normalize(), n, nil
		}
	}
	if r := []rune(name); len(r) == 1 && mod != 0 {
		return Key{Code: CodeRune, Rune: r[0], Mod: mod}.Normalize(), n, nil
	}
	return Key{}, 0, fmt.Errorf("知らないキーの名前 %s（ただの < は <lt> と書く）", raw)
}

func modOf(r rune) (Mod, bool) {
	switch r {
	case 'C', 'c':
		return ModCtrl, true
	case 'A', 'a', 'M', 'm':
		return ModAlt, true
	case 'S', 's':
		return ModShift, true
	}
	return 0, false
}
