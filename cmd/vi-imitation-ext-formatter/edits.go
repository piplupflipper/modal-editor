package main

import (
	"strings"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/diff"
)

// fileText は、バッファの本文 text（末尾の改行を含めない。基本設計 4.1）を、本体がファイルへ書き出すときと同じ中身にする。
// 整形ツールの標準入力に渡す。finalNewline は buffer/open の finalNewline である。
func fileText(text string, finalNewline bool) string {
	if finalNewline {
		return text + "\n"
	}
	return text
}

// bufferText は、整形ツールの出力 out（ファイルの中身）を、バッファの本文にする（fileText の逆）。
// 書き出すと末尾に改行が付くバッファ（finalNewline）では、out の末尾の改行を 1 つ除く。
// out の末尾に改行が無くても、そのまま本文にする（書き出すときに本体が改行を補う）。
func bufferText(out string, finalNewline bool) string {
	if finalNewline {
		return strings.TrimSuffix(out, "\n")
	}
	return out
}

// lineRange は、gq{モーション} の範囲の行の番号（両端を含む）である。
type lineRange struct{ first, last int }

// rangeLines は、provider/invoke の範囲を、その範囲の文字を含む行の範囲にする。
// 範囲の終わりが行の先頭（行単位のモーションの範囲が次の行の先頭で終わるもの）なら、その行は含めない。
func rangeLines(r ext.Range) lineRange {
	last := r.End.Line
	if r.End.Byte == 0 && r.End.Line > r.Start.Line {
		last--
	}
	return lineRange{first: r.Start.Line, last: last}
}

// overlaps は、元の本文の行 [start, end) の差分の塊が、範囲と重なるかを返す。
// 行を足すだけの塊（start == end。start の行の前に足す）は、足す位置が範囲の最初の行から最後の行の次の行までにあれば重なるとする。
func (r lineRange) overlaps(start, end int) bool {
	if start == end {
		return r.first <= start && start <= r.last+1
	}
	return start <= r.last && end > r.first
}

// formatTransaction は、バッファの本文 old を整形の結果 formatted にする編集を、元にしたバージョン番号 base のトランザクションにする（FR-175）。
//
// 全文を置き換えず、行の差分の塊（internal/diff の Compute。違う行の少ない日常の整形では最小に近い）ごとに 1 つの編集にする。
// 本体は外からの編集に合わせてカーソルをずらす（internal/editor の applyExternal）ので、変わっていない行にあるカーソルは
// 整形の後もその行に残る（全文の置き換えでは、カーソルが置き換えた範囲の中に入り、元の行に残らない）。
// アンドゥで戻る範囲も変わった行に限られ、他の拡張機能の複製（LSP の言語サーバー、tree-sitter の構文木）にも変わった行だけが届く。
//
// 編集は、元の本文の後ろの塊から順に並べる。拡張機能 API のトランザクションは逐次の数え方（ext.Transaction）だが、
// 後ろから適用すれば、前の塊の位置は元の本文の位置のままになる（LSP の拡張機能の整形と同じ並べ方。基本設計 6.3）。
//
// rg が nil でなければ、rg の行と重なる塊だけを使う（FR-176。conform.nvim の、範囲の整形に対応しない整形ツールの扱いと同じ）。
// 使う塊が無ければ nil を返す。
//
// req: FR-175, FR-176
func formatTransaction(old, formatted string, base ext.Version, rg *lineRange) *ext.Transaction {
	if old == formatted {
		return nil
	}
	a, b := strings.Split(old, "\n"), strings.Split(formatted, "\n")
	hunks := diff.Compute(diff.Text{Lines: a}, diff.Text{Lines: b}).Hunks
	var edits []ext.Edit
	for i := len(hunks) - 1; i >= 0; i-- {
		h := hunks[i]
		if rg != nil && !rg.overlaps(h.A.Start, h.A.End) {
			continue
		}
		edits = append(edits, hunkEdit(a, h.A.Start, h.A.End, b[h.B.Start:h.B.End]))
	}
	if len(edits) == 0 {
		return nil
	}
	return &ext.Transaction{BaseVersion: base, Edits: edits}
}

// hunkEdit は、行の列 lines（バッファの本文を改行で分けたもの）の行 [start, end) を repl の行で置き換える編集を返す。
//
// バッファの最後の行の後ろには改行が無い（本文は末尾の改行を含めない）ので、最後の行まで及ぶ塊は、
// 前の行の改行か、足す行の前の改行で繋ぐ。
func hunkEdit(lines []string, start, end int, repl []string) ext.Edit {
	n := len(lines)
	text := strings.Join(repl, "\n")
	if end < n {
		// 行の先頭から行の先頭まで。足す行の後ろには改行を付ける。
		if len(repl) > 0 {
			text += "\n"
		}
		return ext.Edit{Range: ext.Range{Start: ext.Position{Line: start}, End: ext.Position{Line: end}}, Text: text}
	}
	last := ext.Position{Line: n - 1, Byte: len(lines[n-1])}
	switch {
	case start == n:
		// 最後の行の後ろに行を足す。
		return ext.Edit{Range: ext.Range{Start: last, End: last}, Text: "\n" + text}
	case len(repl) == 0 && start > 0:
		// 最後の行までを消す。前の行の改行も消す。
		prev := ext.Position{Line: start - 1, Byte: len(lines[start-1])}
		return ext.Edit{Range: ext.Range{Start: prev, End: last}}
	default:
		// 最後の行までを置き換える（すべての行を消すときは、空の 1 行にする）。
		return ext.Edit{Range: ext.Range{Start: ext.Position{Line: start}, End: last}, Text: text}
	}
}
