package main

import (
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
)

// encoding は、LSP の位置の文字の数え方（PositionEncodingKind）である。
// LSP 3.17 では、既定は UTF-16 で、クライアントは general.positionEncodings で他の数え方を申し出られ、
// サーバーは capabilities.positionEncoding で 1 つを選ぶ。サーバーが省いたら UTF-16 である（LSP 3.17 の仕様）。
type encoding int

const (
	// encUTF16 は、UTF-16 のコード単位で数える。LSP の既定である。
	encUTF16 encoding = iota
	// encUTF8 は、UTF-8 のコード単位（バイト）で数える。本体の位置と同じ数え方である。
	encUTF8
	// encUTF32 は、UTF-32 のコード単位（Unicode のコードポイント）で数える。
	encUTF32
)

// parseEncoding は、PositionEncodingKind の文字列を encoding にする。知らない値と空は UTF-16 にする。
// サーバーは、クライアントが申し出た数え方（utf-8 と utf-16）か utf-16 しか選べないので、知らない値は来ない前提である。
func parseEncoding(s string) encoding {
	switch s {
	case "utf-8":
		return encUTF8
	case "utf-32":
		return encUTF32
	default:
		return encUTF16
	}
}

// units は、文字 r を enc で数えたコード単位の数を返す。size は r の UTF-8 のバイト数である。
// 不正な UTF-8 のバイトは、1 バイトを 1 文字として数え、U+FFFD として扱う。
// ただし、本体は全文を JSON の文字列で送り、Go の encoding/json は不正なバイトを U+FFFD（UTF-8 で 3 バイト）に置き換えるので、
// ext/server の複製と、この拡張機能の写しには不正なバイトが現れない。
// このため、不正な UTF-8 を含むバッファでは、複製の行のバイト数が本体と違い、位置がずれる（ext の契約の問題で、別に扱う）。
func (enc encoding) units(r rune, size int) int {
	switch enc {
	case encUTF8:
		return size
	case encUTF32:
		return 1
	case encUTF16:
		if r >= 0x10000 && r != utf8.RuneError {
			return 2
		}
		return 1
	}
	return 1
}

// toChar は、行 line の先頭からのバイト数 b を、LSP の文字の位置（enc のコード単位の数）にする。
// b は文字の境界を指す前提である。行の長さを超える b は行末として扱う。
func toChar(line string, b int, enc encoding) int {
	b = min(max(b, 0), len(line))
	if enc == encUTF8 {
		return b
	}
	n := 0
	for i := 0; i < b; {
		r, size := utf8.DecodeRuneInString(line[i:])
		n += enc.units(r, size)
		i += size
	}
	return n
}

// fromChar は、LSP の文字の位置 ch（enc のコード単位の数）を、行 line の先頭からのバイト数にする。
//
// ch が行の長さを超えるときは行末にする（LSP 3.17 の仕様は、行の長さを超える文字の位置を行の長さに戻すとしている）。
// ch が 1 つの文字の途中（UTF-16 のサロゲートペアの間、UTF-8 の多バイト文字の途中）を指すときは、その文字の先頭に寄せる。
// 本体は文字の途中を指す位置を誤りとするので、返す位置は必ず文字の境界である。
func fromChar(line string, ch int, enc encoding) int {
	if ch <= 0 {
		return 0
	}
	n := 0
	for i := 0; i < len(line); {
		r, size := utf8.DecodeRuneInString(line[i:])
		u := enc.units(r, size)
		if n+u > ch {
			return i
		}
		n += u
		i += size
	}
	return len(line)
}

// lspPosition は、LSP の位置（0 から数えた行と、enc で数えた文字の位置）である。
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspRange は、LSP の範囲である。End を含まない。
type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

// lines は、位置の変換のもとにする、ある時点の文書の行である。
// 本体と同じく、全文を "\n" で区切ったもので、末尾の改行は含めない（ext.BufferOpenParams）。
//
// 言語サーバーへ送る文書は、末尾に "\n" を 1 つ足したものである（document の doc）。
// このため、言語サーバーから見た文書は、lines の後ろに空の行が 1 つある。
// この空の行を、以下では仮想の行と呼ぶ。行の番号は len(lines) である。
type lines []string

// toLSP は、本体の位置 p を、enc で数えた LSP の位置にする。p は lines の中を指す前提である。
func (ls lines) toLSP(p ext.Position, enc encoding) lspPosition {
	if p.Line < 0 {
		return lspPosition{}
	}
	if p.Line >= len(ls) {
		return lspPosition{Line: len(ls)}
	}
	return lspPosition{Line: p.Line, Character: toChar(ls[p.Line], p.Byte, enc)}
}

// fromLSP は、LSP の位置 p を本体の位置にする。virtual は、p が仮想の行（またはそれより後ろ）を指したかどうかである。
//
// 本体の位置は lines の中に収める。
// 仮想の行とそれより後ろの位置は、lines の最後の行の行末（末尾の改行の直前）にする。
// 負の位置は文書の先頭にする。
func (ls lines) fromLSP(p lspPosition, enc encoding) (pos ext.Position, virtual bool) {
	switch {
	case p.Line < 0:
		return ext.Position{}, false
	case p.Line >= len(ls):
		last := len(ls) - 1
		return ext.Position{Line: last, Byte: len(ls[last])}, true
	}
	return ext.Position{Line: p.Line, Byte: fromChar(ls[p.Line], p.Character, enc)}, false
}

// rangeFromLSP は、LSP の範囲 r を本体の範囲にする。終わりが始まりより前なら、始まりにそろえる。
func (ls lines) rangeFromLSP(r lspRange, enc encoding) ext.Range {
	s, _ := ls.fromLSP(r.Start, enc)
	e, _ := ls.fromLSP(r.End, enc)
	if e.Line < s.Line || (e.Line == s.Line && e.Byte < s.Byte) {
		e = s
	}
	return ext.Range{Start: s, End: e}
}
