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

// マウスの出来事のキーの種類（FR-184）。Vim と Neovim と同じく、マウスの出来事を名前の付いたキーとして扱い（<LeftMouse> など）、
// 設定の keymap で他のキーと同じく割り当て直せるようにする。
// 出来事の位置はキーに含めず、キーと一緒に届ける（internal/term の Event の MouseRow と MouseCol）。
// キーの値を比較とマップのキーに使い続けるためで、Vim もマウスの位置をキーの外（mouse_row と mouse_col）に持つ。
// 複数回のクリック（<2-LeftMouse>）は、押した回数を修飾キーと同じビット（ModClick2 など）に持つ。
//
// 値は F キーの後に置く。ソケットの上ではキーの種類を数で送るので（internal/protocol）、既存の値をずらさない。
const (
	// CodeLeftMouse は、左のボタンを押したことである（Vim の <LeftMouse>）。
	CodeLeftMouse Code = CodeF1 + MaxFunctionKey + iota
	// CodeLeftDrag は、左のボタンを押したまま動かしたことである（<LeftDrag>）。
	CodeLeftDrag
	// CodeLeftRelease は、左のボタンを離したことである（<LeftRelease>）。
	CodeLeftRelease
	// CodeMiddleMouse、CodeMiddleDrag、CodeMiddleRelease は、中のボタンの出来事である（<MiddleMouse> など）。
	CodeMiddleMouse
	CodeMiddleDrag
	CodeMiddleRelease
	// CodeRightMouse、CodeRightDrag、CodeRightRelease は、右のボタンの出来事である（<RightMouse> など）。
	CodeRightMouse
	CodeRightDrag
	CodeRightRelease
	// CodeScrollWheelUp と CodeScrollWheelDown は、ホイールを上と下へ回したことである（<ScrollWheelUp>、<ScrollWheelDown>）。
	CodeScrollWheelUp
	CodeScrollWheelDown
	// CodeScrollWheelLeft と CodeScrollWheelRight は、ホイールを左と右へ傾けたことである（<ScrollWheelLeft>、<ScrollWheelRight>）。
	CodeScrollWheelLeft
	CodeScrollWheelRight
)

// IsMouse は、k がマウスの出来事のキーかを返す。
func (k Key) IsMouse() bool { return k.Code >= CodeLeftMouse && k.Code <= CodeScrollWheelRight }

// IsMousePress は、k がボタンを押したことのキー（<LeftMouse>、<MiddleMouse>、<RightMouse>）かを返す。
// 複数回のクリック（ModClick2 など）は、押したことのキーにだけ付く。
func (k Key) IsMousePress() bool {
	return k.Code == CodeLeftMouse || k.Code == CodeMiddleMouse || k.Code == CodeRightMouse
}

// Clicks は、k が何回目のクリックか（1 から 4）を返す。押したことのキーでなければ 1 である。
func (k Key) Clicks() int {
	return int((k.Mod&modClicks)>>modClicksShift) + 1
}

// WithClicks は、k を n 回目のクリック（1 から 4。範囲の外は寄せる）にしたキーを返す。
func (k Key) WithClicks(n int) Key {
	n = min(max(n, 1), 4)
	k.Mod = k.Mod&^modClicks | Mod(n-1)<<modClicksShift
	return k
}

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
	// マウスの出来事の名前は Vim と同じである（:help mouse-overview の <LeftMouse> など）。
	CodeLeftMouse:        "LeftMouse",
	CodeLeftDrag:         "LeftDrag",
	CodeLeftRelease:      "LeftRelease",
	CodeMiddleMouse:      "MiddleMouse",
	CodeMiddleDrag:       "MiddleDrag",
	CodeMiddleRelease:    "MiddleRelease",
	CodeRightMouse:       "RightMouse",
	CodeRightDrag:        "RightDrag",
	CodeRightRelease:     "RightRelease",
	CodeScrollWheelUp:    "ScrollWheelUp",
	CodeScrollWheelDown:  "ScrollWheelDown",
	CodeScrollWheelLeft:  "ScrollWheelLeft",
	CodeScrollWheelRight: "ScrollWheelRight",
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

// 複数回のクリックの回数（Key.Clicks）を入れるビット。修飾キーの上の 2 ビットに、回数から 1 を引いた値（0 から 3）を入れる
// （Vim の MOD_MASK_2CLICK、MOD_MASK_3CLICK、MOD_MASK_4CLICK に当たる）。表記は <2-LeftMouse> のように数と - を前に付ける。
const (
	modClicksShift     = 3
	modClicks      Mod = 3 << modClicksShift
	// ModClick2、ModClick3、ModClick4 は、2 回目、3 回目、4 回目のクリックである。
	ModClick2 Mod = 1 << modClicksShift
	ModClick3 Mod = 2 << modClicksShift
	ModClick4 Mod = 3 << modClicksShift
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
	if n := k.Clicks(); n > 1 {
		fmt.Fprintf(&b, "%d-", n)
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
	clicks := 1
	for j+2 < len(rs) && rs[j+1] == '-' {
		if c := rs[j]; c >= '2' && c <= '4' {
			// 複数回のクリックの回数（<2-LeftMouse>）。押したことのキーにだけ付けられる（下で確かめる）。
			clicks = int(c - '0')
			j += 2
			continue
		}
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
			k := Key{Code: c, Mod: mod}
			if clicks > 1 {
				if !k.IsMousePress() {
					return Key{}, 0, fmt.Errorf("クリックの回数は、ボタンを押したことのキーにだけ付けられる: %s", raw)
				}
				k = k.WithClicks(clicks)
			}
			return k, n, nil
		}
	}
	if clicks > 1 {
		return Key{}, 0, fmt.Errorf("クリックの回数は、ボタンを押したことのキーにだけ付けられる: %s", raw)
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
