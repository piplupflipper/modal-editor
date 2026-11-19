package main

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/ext"
)

// この節は、位置の変換と、編集に合わせた行のずらしである。

// toDAPLine は、本体の行（0 から数える）を、DAP の行へ変換する。startAt1 は initialize で送った linesStartAt1 である。
func toDAPLine(line int, startAt1 bool) int {
	if startAt1 {
		return line + 1
	}
	return line
}

// fromDAPLine は、DAP の行を本体の行（0 から数える）へ変換する。負になる値は 0 にする。
func fromDAPLine(line int, startAt1 bool) int {
	if startAt1 {
		line--
	}
	return max(line, 0)
}

// utf16ColumnToByte は、DAP の列（UTF-16 のコード単位。startAt1 なら 1 から数える）を、
// 行 text の中のバイトの位置（本体の位置。基本設計 4.1）へ変換する。
//
// 列が文字の途中（サロゲートペアの間）を指すときは、その文字の先頭に寄せる。
// 行の外を指すときは行末にする。不正な UTF-8 のバイトは 1 バイトを 1 文字（U+FFFD、1 コード単位）として数える。
func utf16ColumnToByte(text string, column int, startAt1 bool) int {
	if startAt1 {
		column--
	}
	if column <= 0 {
		return 0
	}
	units := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		if units+n > column {
			return i
		}
		units += n
		i += size
		if units == column {
			return i
		}
	}
	return len(text)
}

// shiftLine は、行 line の先頭の位置を、1 つの編集 e（逐次の数え方）の後の行へずらす。
// ずらし方は、本体が印をずらす方法（基本設計 6.7 の表示のデータ）と同じにし、印の行と拡張機能の持つ行が食い違わないようにする。
//
//   - 位置が編集の範囲より前: そのまま
//   - 位置が編集の範囲の終わり以降: 編集で増減した行数だけ動かす
//   - 位置が編集の範囲と重なる: 編集の後のテキストの範囲の開始（e.Range.Start の行）に寄せる
func shiftLine(line int, e ext.Edit) int {
	start, end := e.Range.Start, e.Range.End
	switch {
	case line < start.Line || (line == start.Line && start.Byte > 0):
		return line
	case line > end.Line || (line == end.Line && end.Byte == 0):
		removed := end.Line - start.Line
		added := strings.Count(e.Text, "\n")
		return line + added - removed
	default:
		return start.Line
	}
}

// shiftBreakpoints は、行の昇順の bps を、編集の列 edits（逐次の数え方）の後の行へずらし、
// 同じ行に重なったものを 1 つにまとめた新しい列を返す。まとめるときは、前にあったほうの状態を残す。
func shiftBreakpoints(bps []bp, edits []ext.Edit) []bp {
	out := slices.Clone(bps)
	for _, e := range edits {
		for i := range out {
			out[i].line = shiftLine(out[i].line, e)
		}
	}
	return normalize(out)
}

// normalize は、bps を行の昇順に並べ、同じ行のものを 1 つにまとめる。並べ替えは安定で、まとめるときは前のほうを残す。
func normalize(bps []bp) []bp {
	slices.SortStableFunc(bps, func(a, b bp) int { return a.line - b.line })
	return slices.CompactFunc(bps, func(a, b bp) bool { return a.line == b.line })
}
