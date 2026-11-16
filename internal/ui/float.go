package ui

import (
	"fmt"
	"strings"
)

// 浮かべる表示の大きさの上限（仮）。
const (
	// popupMaxWidth は、popup/show の窓の列数の上限である（枠の余白を含む）。
	popupMaxWidth = 80
	// popupMaxRows は、popup/show の窓の行数の上限である。
	popupMaxRows = 12
)

// textLines は、s を行に分け、末尾の空の行を除いて返す。
func textLines(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	return lines
}

// clearSplit は、row 行目の [x0, x1) の列を上書きする前に、その外にはみ出す全角文字の半分を空白にする。
// 全角文字の右半分だけ、または左半分だけが上書きされると、端末で残りの半分がずれて見えるためである。
func clearSplit(g *Grid, row, x0, x1 int) {
	if x0 > 0 && x0 < g.Width() && g.At(row, x0).Width == 0 {
		g.set(row, x0-1, blank(g.At(row, x0-1).Attr))
	}
	if x1 > 0 && x1 < g.Width() && g.At(row, x1).Width == 0 {
		g.set(row, x1, blank(g.At(row, x1).Attr))
	}
}

// drawPopup は、popup/show の本文を、カーソルの近くに浮かべて描く（基本設計 6.7）。
//
// 窓は、カーソルの文字の列から始まり、カーソルの下の行に置く。下に収まらず、上の方が広ければ上の行に置く。
// バッファの表示領域の中だけに描き、ステータス行と出力の欄には重ねない。
// 幅は popupMaxWidth 列と画面の幅の小さい方を上限に、本文の最も長い行に合わせ、左右に 1 列の余白を置く。
// 長い行は折り返す。行が popupMaxRows 行と置ける行数に収まらなければ、最後の行に残りの行数を書く。
// Markdown は整形せず、そのまま出す（6.7 の format は、整形するかを ui が決めてよいとしている）。
func (r *Renderer) drawPopup(g *Grid, theme Theme, text string, curRow, curCol int) {
	lines := textLines(text)
	maxW := min(r.Width, popupMaxWidth)
	if len(lines) == 0 || maxW < 3 {
		return
	}
	tabStop := tabStopOr(r.TabStop)
	var rows []outputRow
	width := 0
	for i, l := range lines {
		for _, or := range wrapLine(i, l, maxW-2, tabStop) {
			rows = append(rows, or)
			width = max(width, StringWidth(l[or.start:or.end], tabStop))
		}
	}
	textRows := r.textRows()
	below, above := textRows-curRow-1, curRow
	useAbove := len(rows) > below && above > below
	space := below
	if useAbove {
		space = above
	}
	h := min(len(rows), popupMaxRows, space)
	if h <= 0 {
		return
	}
	top := curRow + 1
	if useAbove {
		top = curRow - h
	}
	note := ""
	if h < len(rows) {
		note = fmt.Sprintf("（残り %d 行）", len(rows)-h+1)
		width = max(width, StringWidth(note, tabStop))
	}
	boxW := min(width+2, maxW)
	x0 := max(min(curCol, r.Width-boxW), 0)
	a := theme.attr(StylePopup)
	special := theme.attr(StyleSpecialKey)
	for i := range h {
		row := top + i
		clearSplit(g, row, x0, x0+boxW)
		for c := range boxW {
			g.set(row, x0+c, blank(a))
		}
		if i == h-1 && note != "" {
			r.drawText(g, row, x0+1, x0+boxW-1, note, a)
			continue
		}
		or := rows[i]
		r.drawSegmentAt(g, row, x0+1, boxW-2, lines[or.line], or, a, special)
	}
}

// drawMessageLines は、改行を含むメッセージを、コマンドラインの行から上へ広げて描く（Vim のメッセージの行が広がるのと同じ）。
// 広げるのは画面の半分の行までで、収まらなければ、最後の行に省いた行数を書く。長い行は右の端で切る。
// 上の行は、バッファの表示領域とステータス行の上に重ねる。次のキー入力でメッセージが消えると元に戻る。
// prompt が空でなければ、最後の行に prompt を pa の属性で描き、メッセージの行はその上に置く（問いの行は画面の半分に数えない）。
// prompt を描いた最後の行の、描いた文字の次の列を返す。
func (r *Renderer) drawMessageLines(g *Grid, lines []string, a Attr, prompt string, pa Attr) int {
	limit := max(r.Height/2, 1)
	if prompt != "" {
		limit = min(limit, r.Height-1)
	}
	switch {
	case limit <= 0:
		lines = nil
	case len(lines) > limit:
		rest := len(lines) - (limit - 1)
		lines = append(lines[:limit-1:limit-1], fmt.Sprintf("（残り %d 行）", rest))
	}
	rows := len(lines)
	if prompt != "" {
		rows++
	}
	top := r.Height - rows
	blankRow := func(row int) {
		for c := range r.Width {
			g.set(row, c, blank(Attr{}))
		}
	}
	for i, l := range lines {
		blankRow(top + i)
		r.drawText(g, top+i, 0, r.Width, l, a)
	}
	if prompt == "" {
		return 0
	}
	blankRow(r.Height - 1)
	return r.drawText(g, r.Height-1, 0, r.Width, prompt, pa)
}

// HitEnterPrompt は、改行を含むメッセージの下に出す、続けるためのキーを促す問いである（Frame.HitEnter）。
// Vim 9.1 の日本語のメッセージ（"Press ENTER or type command to continue"）に、ASCII の語の前後の空白を足したもの。
const HitEnterPrompt = "続けるには ENTER を押すかコマンドを入力してください"
