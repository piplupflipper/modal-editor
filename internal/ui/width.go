package ui

import (
	"fmt"
	"sort"
	"unicode"
	"unicode/utf8"
)

// DefaultTabStop は、タブ幅の既定値である（仮。Vim の 'tabstop' の既定と同じ 8）。
// 設定で変える前提で、Renderer.TabStop と Columns.TabStop で差し替える。
const DefaultTabStop = 8

// glyphKind は、1 つの文字を画面にどう出すかの種類である。
type glyphKind int

const (
	// glyphText は、文字をそのまま出す（幅 1 か 2）。
	glyphText glyphKind = iota
	// glyphTab は、タブである。次のタブ位置までを空白で埋める。
	glyphTab
	// glyphSpecial は、そのままでは出せない文字を ^A や <80> の形で出す。
	glyphSpecial
)

// glyph は、行の中の 1 文字（後に続く結合文字を含む）の、画面上の配置である。
type glyph struct {
	off   int       // 行の先頭からのバイト位置
	size  int       // バイト数。後に続く結合文字を含む
	col   int       // 画面上の開始の列（行の先頭を 0 とする）
	width int       // 画面上の列数
	kind  glyphKind // 出し方
	// text は、出す文字列である。glyphText では文字と結合文字、glyphSpecial では "^A" などの表記、glyphTab では空。
	text string
}

// nextGlyph は、行 line の off から始まる 1 文字を、列 col に置いたときの配置を返す。
// off は文字の境界（buffer.Position の Byte と同じ条件）を指し、len(line) より小さい必要がある。
//
// 表示幅は Vim 9.1 の 'ambiwidth' が single の既定に合わせる（width_test.go の TestRuneWidthVim を参照）。
//   - タブ: 次のタブ位置（tabStop の倍数）まで
//   - U+0000〜U+001F と U+007F: ^@ 〜 ^_ と ^? の 2 列
//   - U+0080〜U+009F と、不正な UTF-8 の 1 バイト: <80> の形の 4 列（16 進は小文字）
//   - Vim が表示できない文字として扱う U+200B などの書式文字: <200b> の形
//   - 結合文字（Unicode の Mn と Me）: 0 列。前の文字と合わせて 1 文字にする（internal/command/target の扱いと同じ）
//   - wideTable にある文字: 2 列
//   - それ以外: 1 列
//
// 行頭の結合文字は前の文字が無いので、空白に重ねて 1 列にする（Vim の strdisplaywidth と同じ幅）。
// タブと ^A などの後の結合文字は、幅 0 で、画面には出さない。
func nextGlyph(line string, off, col, tabStop int) glyph {
	r, size := utf8.DecodeRuneInString(line[off:])
	g := glyph{off: off, size: size, col: col}
	switch {
	case r == utf8.RuneError && size == 1:
		g.kind, g.text = glyphSpecial, fmt.Sprintf("<%02x>", line[off])
	case r == '\t':
		g.kind = glyphTab
		g.width = tabStop - col%tabStop
	case r < 0x20:
		g.kind, g.text = glyphSpecial, "^"+string(r+'@')
	case r == 0x7f:
		g.kind, g.text = glyphSpecial, "^?"
	case r < 0xa0 && r >= 0x80:
		g.kind, g.text = glyphSpecial, fmt.Sprintf("<%02x>", r)
	case unprintable(r):
		g.kind, g.text = glyphSpecial, fmt.Sprintf("<%04x>", r)
	case isComposing(r):
		g.kind, g.text = glyphText, " "+string(r)
		g.width = 1
	default:
		g.kind, g.text = glyphText, string(r)
		g.width = runeWidth(r)
	}
	if g.kind == glyphSpecial {
		g.width = len(g.text)
	}
	// 後に続く結合文字を、同じ 1 文字に含める。
	for g.off+g.size < len(line) {
		c, n := utf8.DecodeRuneInString(line[g.off+g.size:])
		if (c == utf8.RuneError && n == 1) || !isComposing(c) {
			break
		}
		if g.kind == glyphText {
			g.text += line[g.off+g.size : g.off+g.size+n]
		}
		g.size += n
	}
	return g
}

// isComposing は、r が結合文字（前の文字と合わせて 1 文字として扱う文字）かどうかを返す。
// internal/command/target と同じく、Unicode の Mn と Me を結合文字とする（Vim の utf_iscomposing() に当たる）。
func isComposing(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me)
}

// unprintable は、Vim 9.1 が表示できない文字として <200b> の形で出す文字かどうかを返す
// （Vim の utf_printable() が偽を返す文字。U+D800〜U+DFFF は Go の文字列の復号で現れないので除く）。
func unprintable(r rune) bool {
	switch {
	case r == 0x070f, r == 0x180e, r == 0xfeff:
		return true
	case r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e, r >= 0x2060 && r <= 0x206f:
		return true
	case r >= 0xfff9 && r <= 0xfffb, r == 0xfffe, r == 0xffff:
		return true
	}
	return false
}

// runeWidth は、そのまま出せる文字 r の表示幅（0、1、2）を返す。
func runeWidth(r rune) int {
	if isComposing(r) {
		return 0
	}
	if r < wideTable[0][0] {
		return 1
	}
	i := sort.Search(len(wideTable), func(i int) bool { return wideTable[i][1] >= r })
	if i < len(wideTable) && wideTable[i][0] <= r {
		return 2
	}
	return 1
}

// StringWidth は、行 s を列 0 から並べたときの表示幅を返す。タブ幅は tabStop（0 以下なら DefaultTabStop）で数える。
func StringWidth(s string, tabStop int) int {
	tabStop = tabStopOr(tabStop)
	col := 0
	for off := 0; off < len(s); {
		g := nextGlyph(s, off, col, tabStop)
		col += g.width
		off += g.size
	}
	return col
}

// tabStopOr は、0 以下のタブ幅を DefaultTabStop にして返す。
func tabStopOr(tabStop int) int {
	if tabStop <= 0 {
		return DefaultTabStop
	}
	return tabStop
}

// Columns は、望みの列（Vim の curswant）を表示幅で数える数え方である。
// internal/command の State.Columns（target.Columns）に差し込む（Column と ByteAt の 2 つのメソッドを持つ）。
// internal/command とは同じ層なので、インターフェースを import せずに、同じ形のメソッドを持たせる。
//
// ゼロ値は、タブ幅を DefaultTabStop で数える。
type Columns struct {
	// TabStop はタブ幅である。0 以下なら DefaultTabStop を使う。
	TabStop int
}

// Column は、行 line の byteOff の位置の文字の列を、表示幅で数えて返す。
//
// タブの上では、タブが占める最後の列を返す。
// Vim はノーマルモードでタブの上のカーソルをタブの最後の列に置き、その列を望みの列にするので、これに合わせた
// （"\tx" の 1 文字目で j を押すと、下の行の 8 列目へ移る）。
// 全角文字と ^A などの表記は、最初の列を返す。
// byteOff が文字の途中（結合文字の位置など）を指すときは、その文字の列を返す。行の長さ以上なら行の表示幅を返す。
func (c Columns) Column(line string, byteOff int) int {
	tabStop := tabStopOr(c.TabStop)
	col := 0
	for off := 0; off < len(line); {
		g := nextGlyph(line, off, col, tabStop)
		if byteOff < g.off+g.size {
			if g.kind == glyphTab {
				return g.col + g.width - 1
			}
			return g.col
		}
		col += g.width
		off += g.size
	}
	return col
}

// ByteAt は、行 line で列 col を占める文字の位置（バイト）を返す。
// col が行の表示幅以上なら最後の文字の位置を、空の行では 0 を返す。負の col は 0 列として扱う。
// タブや全角文字の途中の列は、その文字の位置を返す（Vim の coladvance() と同じ）。
func (c Columns) ByteAt(line string, col int) int {
	tabStop := tabStopOr(c.TabStop)
	last := 0
	cur := 0
	for off := 0; off < len(line); {
		g := nextGlyph(line, off, cur, tabStop)
		if col < g.col+g.width {
			return g.off
		}
		last = g.off
		cur += g.width
		off += g.size
	}
	return last
}
