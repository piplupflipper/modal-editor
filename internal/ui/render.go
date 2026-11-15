package ui

import (
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// Point は、画面上の位置である。Row と Col は 0 から数える。
type Point struct {
	Row, Col int
}

// Screen は、1 回の描画の結果である。
type Screen struct {
	// Grid は、画面の全体の内容である。
	Grid *Grid
	// Cursor は、端末のカーソルを置く位置である。
	Cursor Point
}

// Renderer は、Frame から画面の格子を作る。
//
// 画面は上から、バッファの表示領域（Height-2 行）、ステータス行（1 行）、コマンドラインの行（1 行）に分ける。
// バッファの行は折り返さず、長い行は横にスクロールして表示する（Vim の 'nowrap'）。
//
// Renderer は、縦と横のスクロールの位置を、描画をまたいで持つ。
// スクロールの位置は表示のための状態で、編集の状態（バッファ、カーソル、モード）は持たず、変更もしない。
// イベントループの goroutine 1 つだけが使う前提で、排他制御をしない（基本設計 2.1）。
type Renderer struct {
	// Width と Height は、画面の列数と行数である。
	Width, Height int
	// TabStop はタブ幅である。0 以下なら DefaultTabStop を使う。
	TabStop int
	// Theme は、色の種類の名前から属性への対応である。nil なら DefaultTheme を使う。
	Theme Theme

	top  int // 表示領域の最初の行の番号（0 から数える）
	left int // 表示領域の左の端の列（行の先頭を 0 とする表示の列）
}

// Top は、表示領域の最初の行の番号（0 から数える）を返す。
func (r *Renderer) Top() int { return r.top }

// Left は、表示領域の左の端に当たる表示の列（0 から数える）を返す。
func (r *Renderer) Left() int { return r.left }

// SetScroll は、スクロールの位置を変える。負の値は 0 にする。
// 次の Render は、この位置からカーソルが画面に収まるようにスクロールする。
func (r *Renderer) SetScroll(top, left int) {
	r.top, r.left = max(top, 0), max(left, 0)
}

// Columns は、このレンダラーのタブ幅で数える Columns を返す。internal/command の State.Columns に差し込む。
func (r *Renderer) Columns() Columns {
	return Columns{TabStop: r.TabStop}
}

// textRows は、バッファの表示領域の行数を返す。
func (r *Renderer) textRows() int {
	return max(r.Height-2, 0)
}

// emptyText は、Frame.Text が nil のときに使う、空の 1 行のバッファである。
type emptyText struct{}

func (emptyText) LineCount() int  { return 1 }
func (emptyText) Line(int) string { return "" }

// cursorPlace は、カーソルの文字の画面上の配置である。
type cursorPlace struct {
	line int // 行の番号
	byte int // 行の中のバイト位置
	col  int // カーソルを置く表示の列
	end  int // カーソルの文字が占める最後の列の次の列。カーソルを画面に収める判定に使う
}

// Render は、f を描いた画面を返す。
// カーソルが表示領域に収まるように、縦と横のスクロールの位置を更新する。
func (r *Renderer) Render(f *Frame) Screen {
	theme := r.Theme
	if theme == nil {
		theme = DefaultTheme()
	}
	text := f.Text
	if text == nil {
		text = emptyText{}
	}
	g := NewGrid(r.Width, r.Height)
	cur := r.placeCursor(text, f)
	r.scroll(text.LineCount(), cur)

	rows := r.textRows()
	d := drawer{r: r, g: g, theme: theme, text: text, f: f, cur: cur}
	d.collectSpans(rows)
	for i := range rows {
		n := r.top + i
		if n >= text.LineCount() {
			g.set(i, 0, Cell{Text: "~", Width: 1, Attr: theme.attr(StyleNonText)})
			continue
		}
		d.drawLine(i, n)
	}
	screen := Screen{Grid: g, Cursor: Point{Row: cur.line - r.top, Col: cur.col - r.left}}
	if r.Height >= 2 {
		r.drawStatus(g, theme, text, f, cur)
	}
	if r.Height >= 1 {
		if p, ok := r.drawCommandLine(g, theme, f); ok {
			screen.Cursor = p
		}
	}
	return screen
}

// placeCursor は、カーソルの位置をバッファの中に収め、画面上の列を求める。
//
// ノーマルモードとビジュアルモードでは、タブの上のカーソルをタブの最後の列に置く。インサートモードでは最初の列に置く（Vim と同じ）。
// 行末より後ろ（インサートモードで行末に挿入するとき）は、行の表示幅の列に置く。
func (r *Renderer) placeCursor(text Text, f *Frame) cursorPlace {
	line := min(max(f.Cursor.Line, 0), text.LineCount()-1)
	s := text.Line(line)
	b := min(max(f.Cursor.Byte, 0), len(s))
	tabStop := tabStopOr(r.TabStop)
	col := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, tabStop)
		if b < gl.off+gl.size {
			p := cursorPlace{line: line, byte: gl.off, col: gl.col, end: gl.col + 1}
			switch {
			case gl.kind == glyphTab && f.Mode != ModeInsert:
				p.col = gl.col + gl.width - 1
				p.end = p.col + 1
			case gl.kind == glyphText && gl.width == 2:
				p.end = gl.col + 2
			}
			return p
		}
		col += gl.width
		off += gl.size
	}
	return cursorPlace{line: line, byte: len(s), col: col, end: col + 1}
}

// scroll は、カーソルが表示領域に収まるように、縦と横のスクロールの位置を更新する。
//
// 縦は Vim 9.1 の 'scrolloff' が 0、'scrolljump' が 1 の既定に合わせる。
// カーソルが画面の上か下へ少しだけ出たときは、カーソルが画面の端の行に来るまでスクロールする。
// 大きく離れたとき（上は max(h/2-1, 2) 行以上、下は (h+1)/2 行より多い。h は表示領域の行数）は、カーソルの行を画面の中ほどに置く。
// この境目と中ほどの位置は、端末の Vim で高さを変えて測った値に合わせた。ただし Vim は前の画面の状態によって中ほどの位置が 1 行ずれることがあり、それは写していない。
// 中ほどに置くときは、バッファの最終行より後ろの "~" の行が出ないように最初の行を詰める。
//
// 横は 'sidescroll' が 0 の既定に合わせ、カーソルの文字が画面の外に出たら、カーソルを画面の幅の半分の位置に置く。
func (r *Renderer) scroll(lineCount int, cur cursorPlace) {
	if h := r.textRows(); h > 0 {
		switch {
		case cur.line < r.top:
			if r.top-cur.line >= max(h/2-1, 2) {
				r.top = cur.line - (h-1)/2
			} else {
				r.top = cur.line
			}
		case cur.line >= r.top+h:
			if cur.line-(r.top+h-1) <= (h+1)/2 {
				r.top = cur.line - h + 1
			} else {
				r.top = min(cur.line-h/2, lineCount-h)
			}
		}
		r.top = max(r.top, 0)
	}
	if w := r.Width; w > 0 && (cur.col < r.left || cur.end > r.left+w) {
		r.left = max(cur.col-w/2, 0)
	}
}

// span は、1 行の中の、属性を重ねるバイトの範囲 [start, end) である。
// end が行の長さより大きければ、行末の 1 セル（行の区切りを表すセル）も含む。
type span struct {
	start, end int
	attr       Attr
}

// drawer は、1 回の描画の作業の状態である。
type drawer struct {
	r     *Renderer
	g     *Grid
	theme Theme
	text  Text
	f     *Frame
	cur   cursorPlace
	spans [][]span // 表示領域の行ごとの、構文の色と診断の範囲（重ねる順）
	sel   []span   // 表示領域の行ごとの、選択の範囲（end が 0 なら選択なし）
}

// collectSpans は、構文の色と診断と選択の範囲を、表示領域の行ごとに分ける。
//
// req: FR-35
func (d *drawer) collectSpans(rows int) {
	d.spans = make([][]span, rows)
	d.sel = make([]span, rows)
	top := d.r.top
	add := func(rg buffer.Range, a Attr, widen bool) {
		if a == (Attr{}) {
			return
		}
		s, e := rg.Start, rg.End
		if e.Less(s) {
			s, e = e, s
		}
		for n := max(s.Line, top); n <= e.Line && n < top+rows && n < d.text.LineCount(); n++ {
			ln := len(d.text.Line(n))
			b0, b1 := 0, ln
			if n == s.Line {
				b0 = min(max(s.Byte, 0), ln)
			}
			if n == e.Line {
				b1 = min(max(e.Byte, 0), ln)
			}
			if widen && s == e {
				b1 = b0 + 1
			}
			if b0 < b1 {
				d.spans[n-top] = append(d.spans[n-top], span{start: b0, end: b1, attr: a})
			}
		}
	}
	for _, h := range d.f.Decorations {
		add(h.Range, d.theme.attr(h.Style), false)
	}
	for _, dg := range d.f.Diagnostics {
		add(dg.Range, d.theme.attr("diagnostic."+string(dg.Severity)), true)
	}

	sel := d.f.Selection
	if !sel.Active {
		return
	}
	s, e := sel.Start, sel.End
	if e.Less(s) {
		s, e = e, s
	}
	a := d.theme.attr(StyleVisual)
	for n := max(s.Line, top); n <= e.Line && n < top+rows && n < d.text.LineCount(); n++ {
		ln := len(d.text.Line(n))
		b0, b1 := 0, ln+1
		if !sel.Linewise {
			if n == s.Line {
				b0 = s.Byte
			}
			if n == e.Line && e.Byte < ln {
				b1 = e.Byte + 1
			}
		}
		d.sel[n-top] = span{start: b0, end: b1, attr: a}
	}
}

// attrAt は、表示領域の row 行目で、バイトの範囲 [off, off+size) の文字に重ねる属性を返す。
// size が 0 なら行末のセルを表し、行末を含む範囲だけを重ねる。
func (d *drawer) attrAt(row, line, off, size int, base Attr) Attr {
	a := base
	covers := func(sp span) bool {
		if size == 0 {
			return sp.start <= off && sp.end > off
		}
		return sp.start < off+size && sp.end > off
	}
	for _, sp := range d.spans[row] {
		if covers(sp) {
			a = a.Over(sp.attr)
		}
	}
	if sp := d.sel[row]; sp.end > 0 && covers(sp) {
		// カーソルの文字は反転しない。端末のカーソルが反転で描かれても文字が読めるようにするためで、Vim と同じ。
		if line != d.cur.line || off != d.cur.byte {
			a = a.Over(sp.attr)
		}
	}
	return a
}

// drawLine は、バッファの line 行目を、表示領域の row 行目に描く。
func (d *drawer) drawLine(row, line int) {
	s := d.text.Line(line)
	left, w := d.r.left, d.r.Width
	tabStop := tabStopOr(d.r.TabStop)
	col := 0
	for off := 0; off < len(s) && col < left+w; {
		gl := nextGlyph(s, off, col, tabStop)
		col += gl.width
		off += gl.size
		if gl.col+gl.width <= left {
			continue
		}
		var base Attr
		if gl.kind == glyphSpecial {
			base = d.theme.attr(StyleSpecialKey)
		}
		a := d.attrAt(row, line, gl.off, gl.size, base)
		x := gl.col - left
		switch gl.kind {
		case glyphTab:
			for i := range gl.width {
				d.g.set(row, x+i, blank(a))
			}
		case glyphSpecial:
			for i, c := range gl.text {
				d.g.set(row, x+i, Cell{Text: string(c), Width: 1, Attr: a})
			}
		case glyphText:
			switch {
			case gl.width < 2:
				d.g.set(row, x, Cell{Text: gl.text, Width: 1, Attr: a})
			case x < 0:
				// 全角文字の左半分が画面の左の端より左にある。Vim と同じく "<" を出す。
				d.g.set(row, 0, Cell{Text: "<", Width: 1, Attr: a.Over(d.theme.attr(StyleNonText))})
			case x+1 >= w:
				// 右の端に半分だけ掛かる全角文字は出さない。
				d.g.set(row, x, blank(a))
			default:
				d.g.set(row, x, Cell{Text: gl.text, Width: 2, Attr: a})
				d.g.set(row, x+1, Cell{Width: 0, Attr: a})
			}
		}
	}
	if col >= left && col < left+w {
		if a := d.attrAt(row, line, len(s), 0, Attr{}); a != (Attr{}) {
			d.g.set(row, col-left, blank(a))
		}
	}
}

// drawText は、row 行目の col 列から、列 limit の手前まで s を描き、描き終えた次の列を返す。
// 収まらない文字の手前で止める。
func (r *Renderer) drawText(g *Grid, row, col, limit int, s string, a Attr) int {
	tabStop := tabStopOr(r.TabStop)
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, tabStop)
		if gl.col+gl.width > limit {
			break
		}
		switch gl.kind {
		case glyphTab:
			for i := range gl.width {
				g.set(row, gl.col+i, blank(a))
			}
		case glyphSpecial:
			for i, c := range gl.text {
				g.set(row, gl.col+i, Cell{Text: string(c), Width: 1, Attr: a})
			}
		case glyphText:
			g.set(row, gl.col, Cell{Text: gl.text, Width: gl.width, Attr: a})
			if gl.width == 2 {
				g.set(row, gl.col+1, Cell{Width: 0, Attr: a})
			}
		}
		col = gl.col + gl.width
		off += gl.size
	}
	return col
}

// drawStatus は、下から 2 行目にステータス行を描く。
//
// 左にモードとバッファの名前と変更の有無（[+]）を、右にマクロの記録中の表示と、カーソルの位置と、表示している範囲の位置を出す。
// カーソルの位置は Vim のルーラーと同じく「行,バイトの列-表示の列」で、2 つの列が同じなら表示の列を省き、空の行では "0-1" と出す。
// 範囲の位置は Vim と同じく All、Top、Bot、または上に隠れた行の割合である。
// 幅が足りなければ、左の部分を右の端で切る。
func (r *Renderer) drawStatus(g *Grid, theme Theme, text Text, f *Frame, cur cursorPlace) {
	row := r.Height - 2
	a := theme.attr(StyleStatusLine)
	for c := range r.Width {
		g.set(row, c, blank(a))
	}

	name := f.Name
	if name == "" {
		name = "[No Name]"
	}
	leftPart := " " + f.Mode.String() + "  " + name
	if f.Modified {
		leftPart += " [+]"
	}

	var right []string
	if f.Recording != 0 {
		right = append(right, "recording @"+string(f.Recording))
	}
	var ruler string
	switch {
	case len(text.Line(cur.line)) == 0:
		ruler = fmt.Sprintf("%d,0-1", cur.line+1)
	case cur.byte == cur.col:
		ruler = fmt.Sprintf("%d,%d", cur.line+1, cur.byte+1)
	default:
		ruler = fmt.Sprintf("%d,%d-%d", cur.line+1, cur.byte+1, cur.col+1)
	}
	right = append(right, ruler, r.position(text.LineCount()))
	rightPart := strings.Join(right, "  ") + " "

	rw := StringWidth(rightPart, r.TabStop)
	start := max(r.Width-rw, 0)
	r.drawText(g, row, start, r.Width, rightPart, a)
	r.drawText(g, row, 0, start-1, leftPart, a)
}

// position は、表示している範囲の位置を、Vim のルーラーと同じ形（All、Top、Bot、NN%）で返す。
func (r *Renderer) position(lineCount int) string {
	above := r.top
	below := max(lineCount-(r.top+r.textRows()), 0)
	switch {
	case above == 0 && below == 0:
		return "All"
	case above == 0:
		return "Top"
	case below == 0:
		return "Bot"
	}
	return fmt.Sprintf("%d%%", above*100/(above+below))
}

// drawCommandLine は、最後の行にコマンドラインかメッセージを描く。
// コマンドラインモードでは ":" と入力中の文字列を出し、カーソルの位置を ok が true で返す。
// 入力中の文字列が幅に収まらなければ、末尾が見えるように先頭の側を隠す。
func (r *Renderer) drawCommandLine(g *Grid, theme Theme, f *Frame) (Point, bool) {
	row := r.Height - 1
	if f.Mode == ModeCommandLine {
		s := ":" + f.CommandLine
		tabStop := tabStopOr(r.TabStop)
		// 末尾の後ろのカーソルの 1 列を残して収まるまで、先頭の文字を落とす。
		for s != "" && StringWidth(s, tabStop)+1 > r.Width {
			s = s[nextGlyph(s, 0, 0, tabStop).size:]
		}
		end := r.drawText(g, row, 0, r.Width, s, Attr{})
		return Point{Row: row, Col: min(end, max(r.Width-1, 0))}, true
	}
	msg := f.Message.Text
	if msg == "" {
		return Point{}, false
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	style := StyleMessageInfo
	switch f.Message.Level {
	case MessageInfo:
	case MessageWarning:
		style = StyleMessageWarning
	case MessageError:
		style = StyleMessageError
	}
	r.drawText(g, row, 0, r.Width, msg, theme.attr(style))
	return Point{}, false
}
