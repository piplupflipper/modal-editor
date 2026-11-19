package diff

import (
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// Patch は、差分の塊を片側へ取り込む編集である（FR-101 の do と dp）。
//
// Edit は、Side の側のバッファに対する編集で、位置は Compute に渡した Text を FromBuffer で作ったときのバッファに対して数える。
// 呼び出し側は、そのときのバッファのバージョン番号を元にしたトランザクション（buffer.Transaction）にして適用する。
// 計算の後にバッファが編集されていれば、計算し直してから作り直す。
//
// 末尾の改行の有無は変えない（Vim 9.1 の :diffget と :diffput も 'endofline' を変えない）。
//
// 全ての行を消す編集の後も、バッファは空の行 1 つとして残し、行の無いバッファ（buffer.Buffer.MarkNoLines）にはしない。
// Vim 9.1 で、相手が空のファイルの塊を :diffget すると空の行 1 つになり、書き出すと "\n" になる（:1d で消したときの "" とは違う）。
// 2026-11-18 に手元の Vim 9.1 で確かめた（#185 のレビュー）。
type Patch struct {
	Side Side
	Edit buffer.Edit
}

// Obtain は、i 番目の塊について、side の側の行を、もう片側の行で置き換える編集を返す（Vim の do。diff obtain）。
// i は 0 以上 len(r.Hunks) 未満でなければ panic する。
func (r *Result) Obtain(i int, side Side) Patch {
	h := r.Hunks[i]
	src, dst := r.Text(side.Other()), r.Text(side)
	ss, ds := h.Span(side.Other()), h.Span(side)
	return Patch{Side: side, Edit: replaceLines(dst.Lines, ds, src.Lines[ss.Start:ss.End])}
}

// Put は、i 番目の塊について、side の側の行で、もう片側の行を置き換える編集を返す（Vim の dp。diff put）。
// 返す Patch の Side は、もう片側である。
// i は 0 以上 len(r.Hunks) 未満でなければ panic する。
func (r *Result) Put(i int, side Side) Patch {
	return r.Obtain(i, side.Other())
}

// replaceLines は、lines の行の範囲 span を repl で置き換える、buffer.Buffer に対する編集を返す。
// lines は buffer.Buffer の行で、0 行なら行の無いバッファ（空の行 1 つ）を表す。
// 全ての行を消すと、バッファは空の行 1 つになる（Patch の doc）。
//
// buffer.Buffer のテキストは最後の行の後の改行を含まないので、最後の行を含む範囲の消去と、末尾への追加では、
// 前の行の終わりの改行を消すか、最後の行の終わりに改行を足す。
func replaceLines(lines []string, span Span, repl []string) buffer.Edit {
	n := len(lines)
	endOf := func(line int) buffer.Position { return buffer.Position{Line: line, Byte: len(lines[line])} }
	switch {
	case n == 0:
		// 行の無いバッファへの追加。
		return buffer.Edit{Text: strings.Join(repl, "\n")}
	case span.Len() > 0 && len(repl) > 0:
		return buffer.Edit{
			Range: buffer.Range{Start: buffer.Position{Line: span.Start}, End: endOf(span.End - 1)},
			Text:  strings.Join(repl, "\n"),
		}
	case span.Len() > 0 && span.End < n:
		// 行の消去。次の行の先頭までを消す。
		return buffer.Edit{Range: buffer.Range{Start: buffer.Position{Line: span.Start}, End: buffer.Position{Line: span.End}}}
	case span.Len() > 0 && span.Start > 0:
		// 最後の行を含む消去。前の行の終わりの改行から消す。
		return buffer.Edit{Range: buffer.Range{Start: endOf(span.Start - 1), End: endOf(span.End - 1)}}
	case span.Len() > 0:
		// 全ての行の消去。空の行 1 つを残す。
		return buffer.Edit{Range: buffer.Range{End: endOf(n - 1)}}
	case span.Start < n:
		// 行の前への追加。
		p := buffer.Position{Line: span.Start}
		return buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: strings.Join(repl, "\n") + "\n"}
	default:
		// 最後の行の後への追加。
		p := endOf(n - 1)
		return buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: "\n" + strings.Join(repl, "\n")}
	}
}
