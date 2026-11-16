package ui

import (
	"fmt"
	"strings"
)

// 画面の部品の大きさ（仮）。
const (
	// signColumnWidth は、印の列の列数である。Vim の 'signcolumn' の列数（2）と同じである。
	signColumnWidth = 2
	// outputMaxRows は、出力の欄の中身の行数の上限である。Vim の :copen の既定の高さ（10 行）と同じである。
	outputMaxRows = 10
)

// signMark は、印の列に出す 1 つの印の表記と色と優先順位である。
type signMark struct {
	text  string // 2 列に収まる表記
	style string
	rank  int // 大きいほど優先する
}

// 印の表記と優先順位（基本設計 6.7）。
//
// 1 つの行に複数の印があれば、次の順に高いものを 1 つだけ出す。
//
//  1. executionPoint（デバッグ対象が止まった位置）
//  2. breakpoint
//  3. breakpointUnverified
//  4. 診断の印。error、warning、info、hint の順
//
// デバッガーの印を診断の印より上にするのは、デバッグ中に利用者が見る位置（止まった行と置いたブレークポイント）を、
// 編集中に出る診断で隠さないためである。止まった行のブレークポイントは "=>" に隠れるが、続行すると印が戻る。
// 表記は ASCII にし、端末の文字幅（'ambiwidth'）で列がずれないようにする。
var (
	signMarks = map[SignKind]signMark{
		SignExecutionPoint:       {text: "=>", style: StyleSignExecutionPoint, rank: 7},
		SignBreakpoint:           {text: "B ", style: StyleSignBreakpoint, rank: 6},
		SignBreakpointUnverified: {text: "b ", style: StyleSignBreakpointUnverified, rank: 5},
	}
	diagnosticMarks = map[Severity]signMark{
		SeverityError:   {text: "E ", style: StyleSignDiagnosticError, rank: 4},
		SeverityWarning: {text: "W ", style: StyleSignDiagnosticWarning, rank: 3},
		SeverityInfo:    {text: "I ", style: StyleSignDiagnosticInfo, rank: 2},
		SeverityHint:    {text: "H ", style: StyleSignDiagnosticHint, rank: 1},
	}
)

// layout は、f に合わせて、印の列の幅と出力の欄の行数を決める。Render の最初に呼ぶ。
//
// 印の列は、Vim の 'signcolumn' の既定（auto）と同じく、印か診断が 1 つでもあれば出し、無ければ出さない。
// 出力の欄は、画面の下（ステータス行とコマンドラインの行の間）に、欄の中身と欄の名前の行を置く。
// 中身の行数は outputMaxRows 行と、バッファの表示領域の半分の小さい方にする。
// 画面が低く、バッファの表示領域と欄の中身を 1 行ずつも取れないときは、欄を出さない。
func (r *Renderer) layout(f *Frame) {
	r.gutter = max(r.GutterWidth, 0)
	if len(f.Signs) > 0 || len(f.Diagnostics) > 0 {
		r.gutter = max(r.gutter, signColumnWidth)
	}
	r.outRows = 0
	if f.Output != nil {
		r.outRows = min(outputMaxRows, (r.Height-3)/2)
		if r.outRows < 1 {
			r.outRows = 0
		}
	}
}

// OutputRows は、直前の Render で出力の欄の中身に使った行数を返す。欄を出していなければ 0 である。
func (r *Renderer) OutputRows() int { return r.outRows }

// TextRows は、直前の Render でバッファの表示領域に使った行数を返す。
func (r *Renderer) TextRows() int { return r.textRows() }

// signsByLine は、表示領域の行ごとに、優先順位の最も高い印を返す。印の無い行はゼロ値である。
// DAP のブレークポイントと止まった位置（基本設計 6.4）は、この印で見る。
//
// req: FR-31
func (d *drawer) signsByLine(rows int) []signMark {
	if d.r.gutter == 0 {
		return nil
	}
	out := make([]signMark, rows)
	top := d.r.top
	put := func(line int, m signMark) {
		if i := line - top; i >= 0 && i < rows && m.rank > out[i].rank {
			out[i] = m
		}
	}
	for _, s := range d.f.Signs {
		if m, ok := signMarks[s.Kind]; ok {
			put(s.Line, m)
		}
	}
	for _, dg := range d.f.Diagnostics {
		if m, ok := diagnosticMarks[dg.Severity]; ok {
			put(min(dg.Range.Start.Line, dg.Range.End.Line), m)
		}
	}
	return out
}

// drawSign は、表示領域の row 行目の印の列を描く。
func (d *drawer) drawSign(row int, m signMark) {
	w := min(d.r.gutter, d.r.Width)
	for c := range w {
		d.g.set(row, c, blank(Attr{}))
	}
	if m.rank == 0 {
		return
	}
	d.r.drawText(d.g, row, 0, min(signColumnWidth, w), m.text, d.theme.attr(m.style))
}

// outputRow は、出力の欄に出す 1 行の、Lines の行の番号とバイトの範囲 [start, end) である。
type outputRow struct {
	line, start, end int
	col              int // start の文字の、行の先頭からの表示の列
}

// wrapLine は、s を幅 width の行に折り返した、行ごとのバイトの範囲を返す（Vim の 'wrap'）。空の行は 1 行にする。
// 1 文字が幅より広ければ（幅 1 の欄の全角文字）、その文字だけで 1 行にする。
func wrapLine(line int, s string, width, tabStop int) []outputRow {
	rows := []outputRow{{line: line}}
	col, rowCol := 0, 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, tabStop)
		cur := &rows[len(rows)-1]
		if gl.col+gl.width-rowCol > width && off > cur.start {
			rows = append(rows, outputRow{line: line, start: off, end: off, col: gl.col})
			cur = &rows[len(rows)-1]
			rowCol = gl.col
		}
		col += gl.width
		off += gl.size
		cur.end = off
	}
	return rows
}

// outputRows は、出力の欄に出す行を、上から順に返す。
// Bottom の手前の行から上へ、欄の n 行が埋まるまで折り返した行を集める。足りなければ先頭の行から下へ集める。
func (r *Renderer) outputRows(o *Output, n int) []outputRow {
	lines := o.Lines
	// 最後の行が空なら、改行で終わったテキストの、まだ何も書かれていない行なので出さない。
	if k := len(lines); k > 0 && lines[k-1] == "" {
		lines = lines[:k-1]
	}
	bottom := len(lines)
	if o.Bottom >= 0 && o.Bottom < bottom {
		bottom = o.Bottom
	}
	tabStop := tabStopOr(r.TabStop)
	var rows []outputRow
	first := bottom
	for first > 0 && len(rows) < n {
		first--
		rows = append(wrapLine(first, lines[first], r.Width, tabStop), rows...)
	}
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	for next := bottom; len(rows) < n && next < len(lines); next++ {
		rows = append(rows, wrapLine(next, lines[next], r.Width, tabStop)...)
	}
	if len(rows) > n {
		rows = rows[:n]
	}
	return rows
}

// drawOutput は、出力の欄を、row0 行目から outRows 行と、その下の欄の名前の行に描く。
func (r *Renderer) drawOutput(g *Grid, theme Theme, o *Output, row0 int) {
	rows := r.outputRows(o, r.outRows)
	tabStop := tabStopOr(r.TabStop)
	special := theme.attr(StyleSpecialKey)
	for i, or := range rows {
		r.drawSegment(g, row0+i, o.Lines[or.line], or, special)
	}
	status := row0 + r.outRows
	a := theme.attr(StyleStatusLineNC)
	for c := range r.Width {
		g.set(status, c, blank(a))
	}
	total := len(o.Lines)
	if total > 0 && o.Lines[total-1] == "" {
		total--
	}
	last := 0
	if len(rows) > 0 {
		last = rows[len(rows)-1].line + 1
	}
	right := fmt.Sprintf("%d/%d ", last, total)
	start := max(r.Width-StringWidth(right, tabStop), 0)
	r.drawText(g, status, start, r.Width, right, a)
	r.drawText(g, status, 0, start-1, " [出力] "+strings.TrimSpace(o.Title), a)
}

// drawSegment は、折り返した 1 行（行 s の [or.start, or.end)）を、画面の row 行目の左の端から描く。
// タブの幅は、行の先頭からの列で決まるので、折り返した位置の列（or.col）から数え続ける。
// 制御文字（デバッグ対象の出力の端末のエスケープシーケンスなど）は、バッファと同じく ^[ の形で出し、端末へそのまま送らない。
func (r *Renderer) drawSegment(g *Grid, row int, s string, or outputRow, special Attr) {
	tabStop := tabStopOr(r.TabStop)
	col := or.col
	for off := or.start; off < or.end; {
		gl := nextGlyph(s, off, col, tabStop)
		x := gl.col - or.col
		switch gl.kind {
		case glyphTab:
			for i := range gl.width {
				g.set(row, x+i, blank(Attr{}))
			}
		case glyphSpecial:
			for i := range len(gl.text) {
				g.set(row, x+i, Cell{Text: gl.text[i : i+1], Width: 1, Attr: special})
			}
		case glyphText:
			if gl.width == 2 && x+1 >= r.Width {
				g.set(row, x, Cell{Text: ">", Width: 1})
				break
			}
			g.set(row, x, Cell{Text: gl.text, Width: gl.width})
			if gl.width == 2 {
				g.set(row, x+1, Cell{Width: 0})
			}
		}
		col += gl.width
		off += gl.size
	}
}
