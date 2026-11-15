package target

import (
	"unicode"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// cursor は、バッファの上を 1 文字ずつ進み戻りする位置である。
// Vim の inc()、dec() と同じく、各行の最後の文字の後に行末の位置を 1 つ置いて数える。
// 1 文字は、基底の文字とその後に続く結合文字を合わせたもの（nextCell、prevCell）である。
type cursor struct {
	b *buffer.Buffer
	buffer.Position
}

// inc と dec の戻り値。Vim の inc()、dec() の戻り値に合わせる。
const (
	moved     = 0  // 同じ行の中で進んだか戻った
	crossed   = 1  // 隣の行へ移った
	reachedNL = 2  // 行末の位置に着いた（inc だけが返す）
	stuck     = -1 // バッファの端で動けなかった
)

func (c *cursor) line() string {
	return c.b.Line(c.Line)
}

// atEOL は、行末の位置にいるかどうかを返す。
func (c *cursor) atEOL() bool {
	return c.Byte >= len(c.line())
}

// lineEmpty は、空の行の先頭にいるかどうかを返す。
func (c *cursor) lineEmpty() bool {
	return c.line() == ""
}

// char は、位置の文字を返す。行末の位置では 0 を返す。
func (c *cursor) char() rune {
	if c.atEOL() {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(c.line()[c.Byte:])
	return r
}

// inc は、1 文字進む。行末の位置からは次の行の先頭へ進む。
func (c *cursor) inc() int {
	line := c.line()
	if c.Byte < len(line) {
		c.Byte = nextCell(line, c.Byte)
		if c.Byte < len(line) {
			return moved
		}
		return reachedNL
	}
	if c.Line < c.b.LineCount()-1 {
		c.Line++
		c.Byte = 0
		return crossed
	}
	return stuck
}

// dec は、1 文字戻る。行の先頭からは前の行の行末の位置へ戻る。
func (c *cursor) dec() int {
	if c.Byte > 0 {
		c.Byte = prevCell(c.line(), c.Byte)
		return moved
	}
	if c.Line > 0 {
		c.Line--
		c.Byte = len(c.line())
		return crossed
	}
	return stuck
}

// nextCell は、line の i バイト目の文字の次の文字の位置を返す。i の文字の後に続く結合文字は飛ばす（Vim の utfc_ptr2len()）。
// i は len(line) より小さいこと。
func nextCell(line string, i int) int {
	_, size := utf8.DecodeRuneInString(line[i:])
	i += size
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		if !isComposing(r) {
			break
		}
		i += size
	}
	return i
}

// prevCell は、line の i バイト目の前の文字の先頭の位置を返す。結合文字は前の基底の文字と合わせて戻る（Vim の utf_head_off()）。
// 行頭から結合文字が続くときは、行頭へ戻る。i は 0 より大きいこと。
func prevCell(line string, i int) int {
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:i])
		i -= size
		if !isComposing(r) {
			break
		}
	}
	return i
}

// incl は、inc と同じだが、空でない行の行末の位置を飛ばす（Vim の incl()）。
func (c *cursor) incl() int {
	r := c.inc()
	if r >= crossed && c.Byte != 0 {
		r = c.inc()
	}
	return r
}

// decl は、dec と同じだが、空でない行の行末の位置を飛ばす（Vim の decl()）。
func (c *cursor) decl() int {
	r := c.dec()
	if r == crossed && c.Byte != 0 {
		r = c.dec()
	}
	return r
}

// oneLeft は、同じ行の中で 1 文字戻る。行の先頭なら false を返す。
func (c *cursor) oneLeft() bool {
	if c.Byte == 0 {
		return false
	}
	c.dec()
	return true
}

// cls は、位置の文字の分類を返す（Vim の cls()）。
//
// Vim は基底の文字と、その後に続く結合文字（濁点 U+3099、異体字セレクタ U+FE0F や U+E0100 など）を合わせて 1 文字とする。
// inc と dec は結合文字を飛ばすが、Request の Cursor が結合文字を指すこともあるので、結合文字の上では、同じ行の中で前にある最初の結合文字でない文字の分類を返す。
// 行頭から結合文字が続くときは、その結合文字そのものの分類を返す（Vim でも前に基底の文字が無ければ単独の 1 文字になる）。
//
// 不正な UTF-8 のバイトは、Vim と同じくそのバイトの値（U+0080 から U+00FF）として分類する。
func (c *cursor) cls(big bool) int {
	line := c.line()
	i := c.Byte
	for i > 0 && i < len(line) {
		r, _ := utf8.DecodeRuneInString(line[i:])
		if !isComposing(r) {
			break
		}
		_, size := utf8.DecodeLastRuneInString(line[:i])
		i -= size
	}
	return classOf(classRune(line, i), big)
}

// classRune は、分類に使う、line の i バイト目の文字を返す。行末の位置では 0 を返す。
// 不正な UTF-8 のバイトは、utf8.RuneError ではなくバイトの値を返す（Vim の utf_ptr2char() と同じ）。
func classRune(line string, i int) rune {
	if i >= len(line) {
		return 0
	}
	r, size := utf8.DecodeRuneInString(line[i:])
	if r == utf8.RuneError && size == 1 {
		return rune(line[i])
	}
	return r
}

// isComposing は、前の文字と合わせて 1 文字になる結合文字かを返す（Vim の utf_iscomposing()）。
//
// Unicode の一般カテゴリ Mn と Me の文字である。Mc（U+0903 などの前進する結合文字）は含めない。
// Vim 9.1（パッチ 1-16）で strchars('a' .. nr2char(c), 1) が 1 になる文字（U+0080 から U+10FFFF）と、
// Go 1.27 の unicode.Mn と unicode.Me が一致することを確かめた。
func isComposing(r rune) bool {
	return r >= 0x300 && unicode.In(r, unicode.Mn, unicode.Me)
}

// skipClass は、分類が class である間進むか戻る。バッファの端に着いたら true を返す（Vim の skip_chars()）。
func (c *cursor) skipClass(class int, big, forward bool) bool {
	for c.cls(big) == class {
		var r int
		if forward {
			r = c.inc()
		} else {
			r = c.dec()
		}
		if r == stuck {
			return true
		}
	}
	return false
}

// isBlank は、空白かタブかを返す。
func isBlank(r rune) bool {
	return r == ' ' || r == '\t'
}

// firstNonBlankByte は、行の最初の空白でない文字の位置を返す。
// 空白だけの行では最後の文字の位置を、空の行では 0 を返す（Vim の beginline(BL_WHITE | BL_FIX)）。
func firstNonBlankByte(line string) int {
	for i, r := range line {
		if !isBlank(r) {
			return i
		}
	}
	return lastCharByte(line)
}

// lastCharByte は、行の最後の文字（結合文字が続くならその基底の文字）の位置を返す。空の行では 0 を返す。
func lastCharByte(line string) int {
	if line == "" {
		return 0
	}
	return prevCell(line, len(line))
}

// charColumn は、行の先頭から byteOff までの文字数を返す。
func charColumn(line string, byteOff int) int {
	return utf8.RuneCountInString(line[:byteOff])
}

// byteOfColumn は、行の先頭から col 文字目の位置を返す。行の文字数を超える col は最後の文字にする。
func byteOfColumn(line string, col int) int {
	i := 0
	for off := range line {
		if i == col {
			return off
		}
		i++
	}
	return lastCharByte(line)
}

// isLineWhite は、空の行か、空白とタブだけの行かを返す（Vim の linewhite()）。
func isLineWhite(line string) bool {
	for _, r := range line {
		if !isBlank(r) {
			return false
		}
	}
	return true
}
