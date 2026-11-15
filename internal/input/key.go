// Package input は、キーの列を編集コマンドの値に解釈する（基本設計 3 章）。
//
// 端末のバイト列からキーへの解読は、端末ライブラリの裁定（issue #9）を待つため、まだ持たない。
// このパッケージはバッファを変更しない。解釈した値の実行は internal/command が受け持つ。
package input

import (
	"fmt"
	"strings"
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
)

// codeNames は、特殊キーの表記（<Esc> の Esc の部分）である。
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
}

// Mod は、修飾キーの組み合わせを表すビットの集合である。
// Shift は、文字のキーでは大文字などの文字に織り込み、Mod には立てない。
type Mod uint8

// 修飾キー。
const (
	ModCtrl Mod = 1 << iota
	ModAlt
	ModShift
)

// Key は、1 回のキー入力を表す。
// 端末ライブラリに依存しない値で、比較（==）とマップのキーに使える。
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

// isPlain は、修飾キーの無い文字のキーであるかを返す。
// レジスター名、回数、f や r の文字の引数には、この種類のキーだけを受け付ける。
func (k Key) isPlain() bool { return k.Code == CodeRune && k.Mod == 0 }

// String は、キーを Vim の表記で返す（例: a、<Esc>、<C-w>、<lt>）。
// 結果を ParseKeys に渡すと元のキーに戻る。
func (k Key) String() string {
	var name string
	if k.Code == CodeRune {
		if k.Mod == 0 {
			if k.Rune == '<' {
				return "<lt>"
			}
			return string(k.Rune)
		}
		name = string(k.Rune)
		if k.Rune == '<' {
			name = "lt"
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

// ParseKeys は、Vim の表記のキーの列（例: "d2w"、"d<Esc>"、"<C-w>j"）をキーの列にする。
// キーマップの表、拡張機能の宣言の keys、テストで使う。
//
// <...> の中が特殊キーの名前、修飾キーつきの表記（C-、A-、S-）、lt のどれでもない場合、
// '<' はただの文字として扱う。このため "<" と "<<" はそのまま書ける。
func ParseKeys(s string) []Key {
	var keys []Key
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '<' {
			if end := indexRune(rs[i+1:], '>'); end >= 0 {
				if k, ok := parseAngle(string(rs[i+1 : i+1+end])); ok {
					keys = append(keys, k)
					i += end + 1
					continue
				}
			}
		}
		keys = append(keys, Rune(rs[i]))
	}
	return keys
}

func indexRune(rs []rune, r rune) int {
	for i, x := range rs {
		if x == r {
			return i
		}
	}
	return -1
}

// parseAngle は、<...> の中身をキーにする。名前が分からなければ ok は false。
func parseAngle(body string) (Key, bool) {
	var mod Mod
	for len(body) > 2 && body[1] == '-' {
		switch body[0] {
		case 'C', 'c':
			mod |= ModCtrl
		case 'A', 'a', 'M', 'm':
			mod |= ModAlt
		case 'S', 's':
			mod |= ModShift
		default:
			return Key{}, false
		}
		body = body[2:]
	}
	if strings.EqualFold(body, "lt") {
		return Key{Code: CodeRune, Rune: '<', Mod: mod}, true
	}
	for c, name := range codeNames {
		if strings.EqualFold(body, name) {
			return Key{Code: c, Mod: mod}, true
		}
	}
	if rs := []rune(body); len(rs) == 1 && mod != 0 {
		return Key{Code: CodeRune, Rune: rs[0], Mod: mod}, true
	}
	return Key{}, false
}
