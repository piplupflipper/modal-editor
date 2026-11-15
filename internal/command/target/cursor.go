package target

import (
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// cursor は、バッファの上を 1 文字ずつ進み戻りする位置である。
// Vim の inc()、dec() と同じく、各行の最後の文字の後に行末の位置を 1 つ置いて数える。
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
		_, size := utf8.DecodeRuneInString(line[c.Byte:])
		c.Byte += size
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
		_, size := utf8.DecodeLastRuneInString(c.line()[:c.Byte])
		c.Byte -= size
		return moved
	}
	if c.Line > 0 {
		c.Line--
		c.Byte = len(c.line())
		return crossed
	}
	return stuck
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

// 文字の分類（Vim の cls()）。
const (
	classBlank = 0 // 空白、タブ、行末の位置
	classPunct = 1 // 単語の文字でない記号。W などでは空白以外の全て
	classWord  = 2 // 英数字、_、ASCII 以外の文字
)

// classOf は、文字の分類を返す。
// 単語の文字は Vim の 'iskeyword' の既定に相当する英数字と _ に、ASCII 以外の文字を加えたものである。
// Vim は ASCII 以外の文字を Unicode の分類（CJK の文字や全角の記号を別の分類にする）で分けるが、ここでは分けない。
func classOf(r rune, big bool) int {
	switch {
	case r == 0 || r == ' ' || r == '\t':
		return classBlank
	case big:
		return classPunct
	case r == '_' || r >= utf8.RuneSelf ||
		'0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z':
		return classWord
	}
	return classPunct
}

// cls は、位置の文字の分類を返す。
func (c *cursor) cls(big bool) int {
	return classOf(c.char(), big)
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

// lastCharByte は、行の最後の文字の位置を返す。空の行では 0 を返す。
func lastCharByte(line string) int {
	if line == "" {
		return 0
	}
	_, size := utf8.DecodeLastRuneInString(line)
	return len(line) - size
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
