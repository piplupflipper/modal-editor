package ui

import (
	"fmt"
	"slices"
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

// stripCodeFences は、Markdown の本文から、コードの囲みの行（行頭の 3 つまでの空白の後に ``` か ~~~ が続く行。
// CommonMark の fenced code block の開きと閉じ）を除き、囲みの中身の行は残す（#151）。
// gopls のホバーは、宣言を ```go の囲みに入れて返すので、囲みの行がそのまま窓に出ていた。
// 囲みの開きと閉じの対応は追わない。4 つ以上の ` で開いた囲みの中の ``` の行も除くが、ホバーの本文の宣言の囲みでは使われないと見て、簡単な方を選んだ。
func stripCodeFences(s string) string {
	lines := strings.Split(s, "\n")
	out := lines[:0:0]
	for _, l := range lines {
		t := strings.TrimLeft(l, " ")
		if len(l)-len(t) <= 3 && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// drawPopup は、popup/show の本文を、カーソルの近くに浮かべて描く（基本設計 6.7）。
//
// 窓は、カーソルの文字の列から始まり、カーソルの下の行に置く。下に収まらず、上の方が広ければ上の行に置く。
// バッファの表示領域の中だけに描き、ステータス行と出力の欄には重ねない。
// 幅は popupMaxWidth 列と画面の幅の小さい方を上限に、本文の最も長い行に合わせ、左右に 1 列の余白を置く。
// 長い行は空白の位置で折り返す（wrapWords）。行が popupMaxRows 行と置ける行数に収まらなければ、最後の行に残りの行数を書く。
// Markdown は、呼び出し側（Render）がコードの囲みの行だけを除き（stripCodeFences）、他の記法は整形せずに出す
// （6.7 の format は、整形するかを ui が決めてよいとしている）。
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
		for _, or := range wrapWords(i, l, maxW-2, tabStop) {
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

// wrapWords は、s を幅 width の行に、空白（半角の空白とタブ）の位置で折り返した、行ごとのバイトの範囲を返す
// （Vim の 'linebreak' に近い。ホバーの本文の英語の文を単語の途中で切らないため。#155）。空の行は 1 行にする。
//
// 幅を超える文字の前に、同じ行の中で空白があれば、最後の空白の後から次の行を始める。
// 空白が無ければ（幅より長い単語、空白で区切らない日本語の文）、wrapLine と同じく幅を超える文字の前で切る。
// 行の末尾で幅を超える空白は、どの行にも含めずに飛ばす（次の行を空白で始めない）。
func wrapWords(line int, s string, width, tabStop int) []outputRow {
	rows := []outputRow{{line: line}}
	col, rowCol := 0, 0
	// brk は、いまの行の中で最後の空白の後のバイト位置、brkCol はその表示の列、brkEnd はそこで切るときのいまの行の終わりである。
	brk, brkCol, brkEnd := -1, 0, 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, tabStop)
		cur := &rows[len(rows)-1]
		blank := gl.kind == glyphTab || (gl.kind == glyphText && gl.text == " ")
		if gl.col+gl.width-rowCol > width && off > cur.start {
			if blank {
				col += gl.width
				off += gl.size
				brk, brkCol, brkEnd = off, col, cur.end
				continue
			}
			if brk > cur.start {
				// 最後の空白の後まで戻り、そこから次の行を始める。
				cur.end = brkEnd
				off, col = brk, brkCol
				gl = nextGlyph(s, off, col, tabStop)
			}
			rows = append(rows, outputRow{line: line, start: off, end: off, col: gl.col})
			cur = &rows[len(rows)-1]
			rowCol = gl.col
		}
		col += gl.width
		off += gl.size
		cur.end = off
		if blank {
			brk, brkCol, brkEnd = off, col, off
		}
	}
	return rows
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

// ChoicePrompt は、選択肢の問いの最後の行に出す、選び方の案内である（Frame.Choice）。n は番号で選べる選択肢の数である。
// HitEnterPrompt と同じく、ASCII の語の前後に空白を置く。狭い端末でも <Esc> まで見えるように、文にせず短くする（40 列に収まる）。
func ChoicePrompt(n int) string {
	switch n {
	case 0:
		return "<Esc> で取り消し"
	case 1:
		return "1 で選ぶか <Esc> で取り消し"
	}
	return fmt.Sprintf("1〜%d で選ぶか <Esc> で取り消し", n)
}

// wrapText は、s を幅 width の行に折り返した文字列を返す（wrapLine の範囲を文字列にしたもの）。
func (r *Renderer) wrapText(s string, width int) []string {
	rows := wrapLine(0, s, max(width, 1), tabStopOr(r.TabStop))
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = s[row.start:row.end]
	}
	return out
}

// drawChoice は、選択肢の問いを、画面の下から上へ広げて描き、カーソルの位置を返す（基本設計 6.7）。
// 最後の行に選び方の案内（ChoicePrompt。続けて届いた問いなら前に "問い 2: "）を描き、その上に知らせの行（Choice.Note）と
// 問いの文の行と、選択肢を 1 つずつ "[番号] ラベル" の行で描く。
// 長い行は折り返す。改行を含むメッセージ（drawMessageLines）と違って画面の半分で止めず、案内の行を除く画面の全体まで広げる。
// 収まらなければ、選択肢を残すために問いの文の行を先に減らし（最低 1 行）、それでも収まらなければ選択肢の行を減らす。
// 減らした側の最後の行には、省いた行数を書く。カーソルは案内の末尾（幅に収まらなければ最後の列）に置く。
func (r *Renderer) drawChoice(g *Grid, theme Theme, c *Choice) Cursor {
	a := theme.attr(StyleMessageInfo)
	var title, items []string
	// 知らせの行は問いの文の前に置き、問いの文と同じく減らす（上の行から残すので、知らせが先に残る）。
	for _, l := range slices.Concat(textLines(c.Note), textLines(c.Title)) {
		title = append(title, r.wrapText(l, r.Width)...)
	}
	for i, l := range c.Labels {
		// ラベルの改行は空白にして、選択肢を 1 つの行（折り返しを除く）に保つ。
		l = strings.Join(strings.Fields(l), " ")
		items = append(items, r.wrapText(fmt.Sprintf("[%d] %s", i+1, l), r.Width)...)
	}
	if c.Omitted > 0 {
		items = append(items, r.wrapText(fmt.Sprintf("（他の %d 件は番号が無いので選べません）", c.Omitted), r.Width)...)
	}
	avail := max(r.Height-1, 0)
	tk := min(len(title), max(avail-len(items), min(avail, 1)))
	if 0 < tk && tk < len(title) {
		title = append(title[:tk-1:tk-1], fmt.Sprintf("（問いの文の残り %d 行）", len(title)-(tk-1)))
	}
	ik := min(len(items), avail-tk)
	if ik < len(items) {
		if ik > 0 {
			items = append(items[:ik-1:ik-1], fmt.Sprintf("（残り %d 行）", len(items)-(ik-1)))
		} else {
			items = nil
		}
	}
	lines := slices.Concat(title[:tk], items)
	top := r.Height - 1 - len(lines)
	blankRow := func(row int) {
		for col := range r.Width {
			g.set(row, col, blank(Attr{}))
		}
	}
	for i, l := range lines {
		blankRow(top + i)
		r.drawText(g, top+i, 0, r.Width, l, a)
	}
	row := r.Height - 1
	blankRow(row)
	prompt := ChoicePrompt(len(c.Labels))
	if c.Index > 0 {
		prompt = fmt.Sprintf("問い %d: %s", c.Index, prompt)
	}
	end := r.drawText(g, row, 0, r.Width, prompt, a)
	return Cursor{Row: row, Col: min(end, max(r.Width-1, 0)), Shape: CursorBlock, Visible: r.Width > 0}
}
