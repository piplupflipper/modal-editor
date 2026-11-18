package diff

import (
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// Patch は、差分の塊を片側へ取り込む編集である（FR-101 の do と dp）。
//
// Edit は、Side の側のバッファに対する編集で、位置は Compute に渡した Text を FromBuffer で作ったときのバッファに対して数える。
// 呼び出し側は、そのときのバッファのバージョン番号を元にしたトランザクション（buffer.Transaction）にして適用する。
// 計算の後にバッファが編集されていれば、計算し直してから作り直す。
//
// SetEOL が true なら、塊がテキストの最後の行を含むので、最後の行の後の改行の有無も EOL にそろえる。
// buffer.Buffer.ApplyReload に EOL を渡すと、編集と同じアンドゥの単位で記録できる。
// 末尾の改行を補う設定（buffer.Buffer.SetFixEndOfLine）が有効なバッファでは、EOL が false でも書き出すと改行が付くので、末尾の改行の違いは残る（Vim の 'fixendofline' と同じ）。
//
// NoLines が true なら、編集で Side の側の行が全て無くなる。
// バッファは空の行 1 つになるので、呼び出し側は buffer.Buffer.MarkNoLines を呼んで、行の無いバッファとして記録する。
type Patch struct {
	Side    Side
	Edit    buffer.Edit
	SetEOL  bool
	EOL     bool
	NoLines bool
}

// Obtain は、i 番目の塊について、side の側の行を、もう片側の行で置き換える編集を返す（Vim の do。diff obtain）。
// i は 0 以上 len(r.Hunks) 未満でなければ panic する。
func (r *Result) Obtain(i int, side Side) Patch {
	h := r.Hunks[i]
	src, dst := r.Text(side.Other()), r.Text(side)
	ss, ds := h.Span(side.Other()), h.Span(side)
	p := Patch{Side: side}
	if ss.End == len(src.Lines) {
		// 塊の後ろに同じ行が無いので、両側とも塊が最後の行を含む。
		p.SetEOL, p.EOL = true, src.EOL
	}
	p.Edit, p.NoLines = replaceLines(dst.Lines, ds, src.Lines[ss.Start:ss.End])
	return p
}

// Put は、i 番目の塊について、side の側の行で、もう片側の行を置き換える編集を返す（Vim の dp。diff put）。
// 返す Patch の Side は、もう片側である。
// i は 0 以上 len(r.Hunks) 未満でなければ panic する。
func (r *Result) Put(i int, side Side) Patch {
	return r.Obtain(i, side.Other())
}

// replaceLines は、lines の行の範囲 span を repl で置き換える、buffer.Buffer に対する編集を返す。
// lines は buffer.Buffer の行で、0 行なら行の無いバッファ（空の行 1 つ）を表す。
// 2 つ目の戻り値は、置き換えた後に行が無くなるかどうかである。
//
// buffer.Buffer のテキストは最後の行の後の改行を含まないので、最後の行を含む範囲の消去と、末尾への追加では、
// 前の行の終わりの改行を消すか、最後の行の終わりに改行を足す。
func replaceLines(lines []string, span Span, repl []string) (buffer.Edit, bool) {
	n := len(lines)
	endOf := func(line int) buffer.Position { return buffer.Position{Line: line, Byte: len(lines[line])} }
	switch {
	case n == 0:
		// 行の無いバッファへの追加。
		return buffer.Edit{Text: strings.Join(repl, "\n")}, len(repl) == 0
	case span.Len() > 0 && len(repl) > 0:
		return buffer.Edit{
			Range: buffer.Range{Start: buffer.Position{Line: span.Start}, End: endOf(span.End - 1)},
			Text:  strings.Join(repl, "\n"),
		}, false
	case span.Len() > 0 && span.End < n:
		// 行の消去。次の行の先頭までを消す。
		return buffer.Edit{Range: buffer.Range{Start: buffer.Position{Line: span.Start}, End: buffer.Position{Line: span.End}}}, false
	case span.Len() > 0 && span.Start > 0:
		// 最後の行を含む消去。前の行の終わりの改行から消す。
		return buffer.Edit{Range: buffer.Range{Start: endOf(span.Start - 1), End: endOf(span.End - 1)}}, false
	case span.Len() > 0:
		// 全ての行の消去。
		return buffer.Edit{Range: buffer.Range{End: endOf(n - 1)}}, true
	case span.Start < n:
		// 行の前への追加。
		p := buffer.Position{Line: span.Start}
		return buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: strings.Join(repl, "\n") + "\n"}, false
	default:
		// 最後の行の後への追加。
		p := endOf(n - 1)
		return buffer.Edit{Range: buffer.Range{Start: p, End: p}, Text: "\n" + strings.Join(repl, "\n")}, false
	}
}
