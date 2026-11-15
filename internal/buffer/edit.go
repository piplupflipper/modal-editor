package buffer

import (
	"errors"
	"fmt"
)

// Position は、バッファの中の位置を、行の番号と行の先頭からのバイト数の組で表す（基本設計 4.1）。
// どちらも 0 から数える。
// Byte は行の長さ（行末）を指してもよい。
//
// Byte は文字の境界を指す必要がある。
// 文字の境界とは、正しい UTF-8 の多バイト文字の途中ではない位置である。
// 不正な UTF-8 のバイトは 1 バイトを 1 文字として数えるので、その前後はどちらも境界になる。
// これは、行の先頭から utf8.DecodeRuneInString（または文字列に対する range）で進めたときに止まる位置と一致する。
type Position struct {
	Line int
	Byte int
}

// Less は、p が q より前にあるかどうかを返す。
func (p Position) Less(q Position) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Byte < q.Byte
}

// String は、デバッグとエラーメッセージのために位置を "行:バイト" の形で返す。
func (p Position) String() string {
	return fmt.Sprintf("%d:%d", p.Line, p.Byte)
}

// Range は、Start から End の直前までの範囲を表す。
// Start と End が等しい範囲は空で、その位置への挿入に使う。
type Range struct {
	Start Position
	End   Position
}

// Edit は、範囲 Range を Text で置き換える編集を表す（基本設計 4.2）。
// 挿入は空の範囲への置き換え、削除は空の Text への置き換えとして表す。
// Text の改行は LF（"\n"）とし、改行は行の区切りとして扱う。
type Edit struct {
	Range Range
	Text  string
}

// Transaction は、1 回の操作でまとめて適用する編集の列と、元にしたバージョン番号である（基本設計 4.2）。
//
// Edits は先頭から順に適用し、各編集の位置は、その前の編集を適用した後のテキストに対する位置として解釈する。
// LSP の TextEdit の列のように、全ての位置を適用前のテキストに対して解釈するのではない。
type Transaction struct {
	BaseVersion uint64
	Edits       []Edit
}

// Change は、バッファに適用した変更の記録である。
// 拡張機能へ送る差分（FR-16、基本設計 5.3 の buffer/change）の材料にする。
//
// Before の版のテキストに Edits を先頭から順に適用すると、After の版のテキストになることを保証する。
// アンドゥとリドゥの結果も同じ形で返すので、受け取る側は通常の編集と区別しなくてよい。
//
// req: FR-16
type Change struct {
	Before uint64
	After  uint64
	Edits  []Edit
}

// ErrVersionMismatch は、トランザクションの元にしたバージョン番号が、バッファの現在の番号と一致しないことを表す。
var ErrVersionMismatch = errors.New("buffer: version mismatch")

// ErrInvalidPosition は、編集や読み出しの位置がバッファの外にあるか、UTF-8 の文字の途中を指すことを表す。
var ErrInvalidPosition = errors.New("buffer: invalid position")

// ErrInvalidRange は、範囲の終わりが始まりより前にあることを表す。
var ErrInvalidRange = errors.New("buffer: invalid range")
