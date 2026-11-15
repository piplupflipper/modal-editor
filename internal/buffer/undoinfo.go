package buffer

import (
	"slices"
	"strings"
)

// UndoInfo は、直前の Undo か Redo が取り消した（やり直した）単位について、カーソルを決める材料をまとめたものである。
// Buffer はカーソルを持たないので、カーソルの位置は呼び出し側が SetUndoCursor で単位に記録し、ここで受け取る。
//
// 変わった行の範囲（Top と Lines）は、Vim のアンドゥの単位が行単位で保存する範囲に当たる。
// 取り消す前と後のテキストを行で比べ、先頭と末尾の同じ行を除いた範囲である。
type UndoInfo struct {
	// Cursor は、単位に SetUndoCursor で記録した位置（変更の前のカーソル。Vim の uh_cursor）である。
	// HasCursor が false なら記録が無く、Cursor はゼロ値である。
	Cursor    Position
	HasCursor bool
	// Top は、変わった行の範囲の最初の行の番号（取り消した後のテキストで数える）である。
	// 取り消しの前と後で全ての行が同じなら、変更の始まりの行である。
	Top int
	// Lines は、取り消した後のテキストで、変わった行の範囲が占める行の数である。範囲の行が全て消えたなら 0 である。
	Lines int
}

// SetUndoCursor は、最後に記録したアンドゥの単位（次の Undo が取り消す単位）に、変更の前のカーソルの位置 p を記録する。
// その単位に既に記録があれば何もしない。グループの中の 2 回目以降の Apply の後に呼んでも、最初の記録が残る。
// アンドゥの単位が無ければ何もしない。
//
// 記録した位置は、その単位を取り消したときとやり直したときに LastUndo で返す。
// Vim がアンドゥの単位ごとに変更の前のカーソルを覚えて戻すのに当たる（FR-07）。
func (b *Buffer) SetUndoCursor(p Position) {
	if len(b.undo) == 0 {
		return
	}
	last := &b.undo[len(b.undo)-1]
	if last.hasCursor {
		return
	}
	last.cursor, last.hasCursor = p, true
}

// LastUndo は、直前の Undo か Redo が取り消した（やり直した）単位の情報を返す。
// Undo と Redo が false を返した場合は、その前の情報のままである。
func (b *Buffer) LastUndo() UndoInfo {
	return b.lastUndo
}

// applyTracked は、履歴に記録した編集の列 edits を適用し、変わった行の範囲を lastUndo に記録する。
func (b *Buffer) applyTracked(edits []Edit, s step) {
	lo, hiPre, hiCur := lineSpan(edits)
	var before []string
	if lo >= 0 {
		before = slices.Clone(b.lines[lo : hiPre+1])
	}
	b.mustApplyAll(edits)
	info := UndoInfo{Cursor: s.cursor, HasCursor: s.hasCursor, Top: max(lo, 0)}
	if lo < 0 {
		b.lastUndo = info
		return
	}
	after := b.lines[lo : hiCur+1]
	same := 0
	for same < len(before) && same < len(after) && before[same] == after[same] {
		same++
	}
	tail := 0
	for tail < len(before)-same && tail < len(after)-same &&
		before[len(before)-1-tail] == after[len(after)-1-tail] {
		tail++
	}
	if same == len(before) && same == len(after) {
		// 行の内容が変わらない編集（同じテキストへの置き換え）だけなら、変更の始まりの行を範囲にする。
		b.lastUndo = info
		return
	}
	info.Top = lo + same
	info.Lines = len(after) - same - tail
	b.lastUndo = info
}

// lineSpan は、順に適用する編集の列 edits が変える行を全て含む範囲を返す。
// lo は範囲の最初の行、hiPre は適用する前のテキストでの最後の行、hiCur は適用した後のテキストでの最後の行である。
// 範囲の間にある変わらない行も含める。edits が空なら lo は -1 である。
//
// 各編集は、その前の編集を適用した後の位置で数える（Transaction と同じ）。
// 編集は始まりの行より前の行を動かさないので、始まりの行の最小値は、どの時点のテキストで数えても同じである。
func lineSpan(edits []Edit) (lo, hiPre, hiCur int) {
	lo = -1
	for _, e := range edits {
		s, end := e.Range.Start.Line, e.Range.End.Line
		k := strings.Count(e.Text, "\n")
		if lo < 0 {
			lo, hiPre, hiCur = s, end, s+k
			continue
		}
		if end > hiCur {
			// 範囲より後の行は、適用する前と後で行の数の差だけずれている。
			hiPre += end - hiCur
		}
		if hiCur > end {
			hiCur += k - (end - s)
		} else {
			hiCur = s + k
		}
		lo = min(lo, s)
	}
	return lo, hiPre, hiCur
}
