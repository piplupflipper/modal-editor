package ui

import (
	"strconv"
	"strings"
)

// Progress は、バッファの表示領域の右下に重ねて出す 1 つの進み具合である（Frame.Progress。progress/report。FR-178）。
type Progress struct {
	// Title は、処理の表題である。
	Title string
	// Message は、本文である。空なら出さない。
	Message string
	// Percentage は、0 から 100 の百分率である。負なら出さない。
	Percentage int
	// Done は、終わった進み具合であることを表す。表題と印を StyleProgressDone で描き、印に SymbolProgressDone を使う。
	Done bool
}

// progressPadding は、進み具合の行の右に空ける列数である（fidget.nvim の window の x_padding の既定の 1）。
const progressPadding = 1

// drawProgress は、進み具合の一覧 items（古い順）を、バッファの表示領域（rows 行）の右下に、1 つを 1 行にして重ねて描く
// （fidget.nvim の既定の出し方。FR-178）。
//
//   - 行は、表示領域の最後の行から上へ積み、最も新しいものを最後の行に置く
//   - 1 行は「本文 (百分率%) 表題 印」で、右の端から progressPadding 列を空けて右に寄せる。本文と百分率は StyleProgress、
//     表題と印は続いている間は StyleProgressTitle、終わったら StyleProgressDone で描く。行の左の 1 列は空白にして、下の本文と分ける
//   - 出す行数は表示領域の半分（切り捨て）までで、収まらなければ古いものを出さない。表示領域が 2 行より少なければ出さない
//   - 行が幅に収まらなければ、まず本文と百分率を末尾から削る。それでも収まらなければ、表題と印を右の端で切る
//
// バッファの本文、印の列、行番号の列、折り返しの印の上に描き、ポップアップ（Frame.Float と Frame.List）とメッセージの行はその上に描く
// （fidget.nvim の窓の zindex の既定の 45 が、浮かべた窓の既定の 50 より下にあるのと同じ）。
//
// req: FR-178
func (r *Renderer) drawProgress(g *Grid, theme Theme, items []Progress, rows int) {
	n := min(len(items), rows/2)
	width := r.Width - progressPadding
	if n <= 0 || width <= 0 {
		return
	}
	items = items[len(items)-n:]
	for i, p := range items {
		r.drawProgressLine(g, theme, rows-n+i, width, p)
	}
}

// drawProgressLine は、進み具合 p を row 行目の列 width の手前まで、右に寄せて描く（drawProgress の doc の 1 行の形）。
func (r *Renderer) drawProgressLine(g *Grid, theme Theme, row, width int, p Progress) {
	body := p.Message
	if p.Percentage >= 0 {
		pct := "(" + strconv.Itoa(min(p.Percentage, 100)) + "%)"
		if body == "" {
			body = pct
		} else {
			body += " " + pct
		}
	}
	style, mark := StyleProgressTitle, r.symbol(SymbolProgressRunning)
	if p.Done {
		style, mark = StyleProgressDone, r.symbol(SymbolProgressDone)
	}
	tail := p.Title
	if mark != "" {
		if tail != "" {
			tail += " "
		}
		tail += mark
	}
	tabStop := tabStopOr(r.tabStop)
	tailW := StringWidth(tail, tabStop)
	bodyW := StringWidth(body, tabStop)
	sep := 0
	if body != "" && tail != "" {
		sep = 1
	}
	if over := bodyW + sep + tailW - width; over > 0 {
		body = strings.TrimRight(cutWidth(body, bodyW-over-sep, tabStop), " ")
		bodyW = StringWidth(body, tabStop)
		if body == "" {
			sep = 0
		}
	}
	total := min(bodyW+sep+tailW, width)
	start := width - total
	x0 := max(start-1, 0)
	clearSplit(g, row, x0, width)
	for x := x0; x < width; x++ {
		g.set(row, x, blank(Attr{}))
	}
	col := start
	if body != "" {
		col = r.drawText(g, row, col, width, body, theme.attr(StyleProgress)) + sep
	}
	r.drawText(g, row, col, width, tail, theme.attr(style))
}

// cutWidth は、s の先頭から、表示幅が w を超えない所までを返す。w が 0 以下なら空を返す。
func cutWidth(s string, w, tabStop int) string {
	col := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, tabStop)
		if gl.col+gl.width > w {
			return s[:off]
		}
		col = gl.col + gl.width
		off += gl.size
	}
	return s
}
