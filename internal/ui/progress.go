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
	// Failed は、Done のうち、失敗して終わったものであることを表す。表題と印を StyleProgressFailed で描き、印に SymbolProgressFailed を使う。
	Failed bool
}

// progressPadding は、進み具合の行の右に空ける列数である（fidget.nvim の window の x_padding の既定の 1）。
const progressPadding = 1

// drawProgress は、進み具合の一覧 items（古い順）を、バッファの表示領域 area の右下に、1 つを 1 行にして重ねて描く
// （fidget.nvim の既定の出し方。FR-178）。重ねる順は layers.go の「窓の重なり」の表にある。
//
//   - 行は、表示領域の最後の行から上へ積み、最も新しいものを最後の行に置く
//   - 1 行は「本文 (百分率%) 表題 印」で、表示領域の右の端から progressPadding 列を空けて右に寄せる。本文と百分率は StyleProgress、
//     表題と印は、続いている間は StyleProgressTitle、終わったら StyleProgressDone、失敗して終わったら StyleProgressFailed で描く。
//     行の左の 1 列は空白にして、下の本文と分ける
//   - 出す行数は表示領域の半分（切り捨て）までで、収まらなければ古いものを出さない。表示領域が 2 行より少なければ出さない
//   - 行が幅に収まらなければ、本文と百分率を末尾から削り、それでも収まらなければ表題を末尾から削る。印は残す
//     （続いているか終わったかを、色だけでなく文字でも分かるようにする。PR #345 のレビューの推奨 4）。印だけでも収まらなければ、印を右の端で切る
//
// req: FR-178
func (r *Renderer) drawProgress(g *Grid, theme Theme, items []Progress, area textArea) {
	n := min(len(items), area.rows/2)
	width := area.width - progressPadding
	if n <= 0 || width <= 0 {
		return
	}
	items = items[len(items)-n:]
	for i, p := range items {
		r.drawProgressLine(g, theme, area.top+area.rows-n+i, width, p)
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
	switch {
	case p.Failed:
		style, mark = StyleProgressFailed, r.symbol(SymbolProgressFailed)
	case p.Done:
		style, mark = StyleProgressDone, r.symbol(SymbolProgressDone)
	}
	tabStop := tabStopOr(r.tabStop)
	// 削る順に並べる。本文、表題、印の順に削り、削って空になったものは、間の空白ごと出さない。
	parts := []string{body, p.Title, mark}
	widths := make([]int, len(parts))
	for i, s := range parts {
		widths[i] = StringWidth(s, tabStop)
	}
	total := func() int {
		t, k := 0, 0
		for i, w := range widths {
			if parts[i] != "" {
				t += w
				k++
			}
		}
		return t + max(k-1, 0)
	}
	for i := 0; i < len(parts)-1 && total() > width; i++ {
		// 削った後も空白 1 つ分を取るので、その分も削る。空になれば空白も要らない。
		over := total() - width
		parts[i] = strings.TrimRight(cutWidth(parts[i], widths[i]-over, tabStop), " ")
		widths[i] = StringWidth(parts[i], tabStop)
	}
	w := min(total(), width)
	start := width - w
	x0 := max(start-1, 0)
	clearSplit(g, row, x0, width)
	for x := x0; x < width; x++ {
		g.set(row, x, blank(Attr{}))
	}
	col := start
	for i, s := range parts {
		if s == "" {
			continue
		}
		if col > start {
			col++
		}
		a := theme.attr(style)
		if i == 0 {
			a = theme.attr(StyleProgress)
		}
		col = r.drawText(g, row, col, width, s, a)
	}
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
