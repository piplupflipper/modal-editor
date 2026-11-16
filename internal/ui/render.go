package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// CursorShape は、カーソルの形である。
type CursorShape int

// カーソルの形。
const (
	// CursorBlock は、文字を覆う箱の形である。ノーマルモードとビジュアルモードで使う。
	CursorBlock CursorShape = iota
	// CursorBar は、文字の左の縦線である。インサートモードとコマンドラインモードで使う。
	CursorBar
	// CursorUnderline は、文字の下の線である。オペレーターの後でモーションを待つ間に使う（Vim の既定の 'guicursor' の o と同じ）。
	CursorUnderline
)

// Cursor は、端末のカーソルの位置と形である。Row と Col は 0 から数える。
type Cursor struct {
	Row, Col int
	Shape    CursorShape
	// Visible は、カーソルを表示するかどうかである。カーソルを置く場所が画面に無いとき（表示領域が 0 行のときなど）は false である。
	Visible bool
}

// Screen は、1 回の描画の結果である。
type Screen struct {
	// Grid は、画面の全体の内容である。
	Grid *Grid
	// Cursor は、端末のカーソルの位置と形である。
	Cursor Cursor
}

// Renderer は、Frame から画面の格子を作る。
//
// 画面は上から、バッファの表示領域、ステータス行（1 行）、コマンドラインの行（1 行）に分ける。
// 出力の欄（Frame.Output）を開いていれば、ステータス行とコマンドラインの行の間に、欄の中身と欄の名前の行を置く（Vim の :copen の窓と同じ並び）。
// バッファの表示領域の左には、印の列（印か診断があるときだけ 2 列）を置く（layout）。GutterWidth で列を空けておくこともできる。
// バッファの行は折り返さず、長い行は横にスクロールして表示する（Vim の 'nowrap'）。出力の欄の行は折り返す。
//
// Renderer は、縦と横のスクロールの位置を、描画をまたいで持つ。
// 表示領域の行数が変わったとき（出力の欄の開け閉め、端末の大きさの変更）は、カーソルの行が表示領域の中で同じ割合の位置に来るように縦の位置を決める（fitHeight）。
// スクロールの位置は表示のための状態で、編集の状態（バッファ、カーソル、モード）は持たず、変更もしない。
// イベントループの goroutine 1 つだけが使う前提で、排他制御をしない（基本設計 2.1）。
type Renderer struct {
	// Width と Height は、画面の列数と行数である。
	Width, Height int
	// TabStop はタブ幅である。0 以下なら DefaultTabStop を使う。
	TabStop int
	// Theme は、色の種類の名前から属性への対応である。nil なら DefaultTheme を使う。
	Theme Theme
	// GutterWidth は、バッファの表示領域の左に必ず空ける欄（印、行番号）の列数である。テキストはその右から描く。
	// 印か診断があれば、少なくとも印の列の 2 列を空ける。
	GutterWidth int

	top  int // 表示領域の最初の行の番号（0 から数える）
	left int // 表示領域の左の端の列（行の先頭を 0 とする表示の列）

	// gutter と outRows は、直前の Render（layout）で決めた、左の欄の列数と出力の欄の中身の行数である。
	gutter, outRows int

	// 以下は、表示領域の行数が変わったときに最初の行を決める（fitHeight）ための、直前の Render の状態である。
	rendered bool // Render を 1 回以上行ったか
	rows     int  // 直前の Render の表示領域の行数
	fraction int  // カーソルの行の、表示領域の中の相対位置（Vim の w_fraction。fractionMult 分の 1 の単位）
	fracRow  int  // fraction を最後に使ったときのカーソルの画面の行に 1 を足したもの。0 なら未設定（Vim の w_prev_fraction_row）
}

// fractionMult は、fraction の単位である（Vim の FRACTION_MULT と同じ値）。
const fractionMult = 16384

// Top は、表示領域の最初の行の番号（0 から数える）を返す。
func (r *Renderer) Top() int { return r.top }

// Left は、表示領域の左の端に当たる表示の列（0 から数える）を返す。
func (r *Renderer) Left() int { return r.left }

// SetScroll は、スクロールの位置を変える。負の値は 0 にする。
// 次の Render は、この位置からカーソルが画面に収まるようにスクロールする。
func (r *Renderer) SetScroll(top, left int) {
	r.top, r.left = max(top, 0), max(left, 0)
}

// EnterBuffer は、表示するバッファを替えたときに、スクロールの位置を、替えた先のバッファ（行数 lineCount）のカーソルの行 line に合わせて決め直す。
// 縦は、line を表示領域の中ほど（上から (h-1)/2 行目。h は直前の Render の表示領域の行数）に置き、
// 最初の行が先頭より前にも、バッファの最終行の後ろに "~" の行が出る位置にもならないように詰める。横は左の端に戻す。
// 次の Render は、この位置からカーソルが画面に収まるようにスクロールする。
//
// Vim 9.1 の enter_buffer（最初の行を 1 にしてから、バッファに残したカーソルで scroll_cursor_halfway を呼ぶ）に合わせた。
// 前のバッファの最初の行を使い続けると、定義へ移動して戻った（<C-o>）ときに、画面に収まる短いファイルの先頭の行が隠れる（#155）。
// 値は、tmux の 33 行と 32 行の表示領域で、400 行、40 行、22 行のファイルのカーソルの行を変えて、別のバッファから戻ったときの line("w0") を測って合わせた。
func (r *Renderer) EnterBuffer(lineCount, line int) {
	h := r.textRows()
	r.top = max(min(line-(h-1)/2, lineCount-h), 0)
	r.left = 0
}

// Columns は、このレンダラーのタブ幅で数える Columns を返す。internal/command の State.Columns に差し込む。
func (r *Renderer) Columns() Columns {
	return Columns{TabStop: r.TabStop}
}

// textRows は、バッファの表示領域の行数を返す。
// 出力の欄を開いていれば、欄の中身と欄の名前の行の分だけ減る。
func (r *Renderer) textRows() int {
	if r.outRows > 0 {
		return max(r.Height-3-r.outRows, 0)
	}
	return max(r.Height-2, 0)
}

// textLeft は、バッファのテキストを描く最初の画面の列を返す。
func (r *Renderer) textLeft() int {
	return min(max(r.gutter, r.GutterWidth, 0), max(r.Width, 0))
}

// textWidth は、バッファのテキストを描く列数を返す。
func (r *Renderer) textWidth() int {
	return max(r.Width-r.textLeft(), 0)
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
	r.layout(f)
	cur := r.placeCursor(text, f)
	resized := r.fitHeight(text.LineCount(), cur)
	r.scroll(text.LineCount(), cur)
	if resized {
		r.fracRow = cur.line - r.top + 1
	}
	r.rendered, r.rows = true, r.textRows()

	rows := r.textRows()
	d := drawer{r: r, g: g, theme: theme, text: text, f: f, cur: cur}
	d.collectSpans(rows)
	signs := d.signsByLine(rows)
	for i := range rows {
		n := r.top + i
		if n >= text.LineCount() {
			g.set(i, 0, Cell{Text: "~", Width: 1, Attr: theme.attr(StyleNonText)})
			continue
		}
		if signs != nil {
			d.drawSign(i, signs[i])
		}
		d.drawLine(i, n)
	}
	if f.Popup != "" && rows > 0 {
		text := f.Popup
		if f.PopupMarkdown {
			text = stripCodeFences(text)
		}
		r.drawPopup(g, theme, text, cur.line-r.top, r.textLeft()+cur.col-r.left)
	}
	screen := Screen{Grid: g, Cursor: Cursor{
		Row: cur.line - r.top, Col: r.textLeft() + cur.col - r.left, Shape: CursorBlock,
		Visible: rows > 0 && r.textWidth() > 0,
	}}
	switch {
	case f.Mode == ModeInsert:
		screen.Cursor.Shape = CursorBar
	case f.OperatorPending:
		screen.Cursor.Shape = CursorUnderline
	}
	if r.Height >= 2 {
		r.drawStatus(g, theme, text, f, cur, rows)
	}
	if r.outRows > 0 {
		r.drawOutput(g, theme, f.Output, rows+1)
	}
	if r.Height >= 1 {
		if c, ok := r.drawCommandLine(g, theme, f); ok {
			screen.Cursor = c
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
	r.scrollRows(lineCount, cur, r.textRows())
	if w := r.textWidth(); w > 0 && (cur.col < r.left || cur.end > r.left+w) {
		r.left = max(cur.col-w/2, 0)
	}
}

// scrollRows は、scroll の縦の部分で、表示領域を h 行として、カーソルの行が収まるように最初の行を更新する。
func (r *Renderer) scrollRows(lineCount int, cur cursorPlace, h int) {
	if h > 0 {
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
}

// fitHeight は、表示領域の行数が直前の Render から変わったとき（出力の欄を開け閉めした、端末の大きさが変わった）に、
// カーソルの行が、表示領域の中で前と同じ割合の位置に来るように最初の行を決める。
// Vim 9.1 の win_new_height と scroll_to_fraction（'splitkeep' が既定の cursor のとき）に合わせた。
//
// 前の割合は、前の行数のままカーソルを表示領域に収めた（Vim が高さを変える前に行う validate_cursor）ときのカーソルの画面の行から求める。
// 直前の Render の後に、カーソルの移動と行数の変更が 1 回の描画にまとまっても（G:output の続けての入力）、Vim と同じく移動の後の位置から求めるためである。
// 割合は、カーソルの画面の行が前の変更の後から動いていなければ前の値を使い続ける（Vim の w_prev_fraction_row）。
// 開け閉めを繰り返しても位置がずれていかないためである。
// 表示領域にバッファの全体が収まり、最初の行が先頭なら、最初の行を変えない（Vim と同じ）。
//
// この後の scroll が、カーソルが表示領域に収まるようにする。
// 前の行数が 0 でない変更のとき（Render が、scroll の後のカーソルの画面の行を w_prev_fraction_row として記録するとき）に true を返す。
func (r *Renderer) fitHeight(lineCount int, cur cursorPlace) bool {
	h, prev := r.textRows(), r.rows
	if !r.rendered || h == prev {
		return false
	}
	if prev > 0 {
		r.scrollRows(lineCount, cur, prev)
		if wrow := cur.line - r.top; prev > 1 && wrow+1 != r.fracRow {
			// 最初の行と最後の行のカーソルは、その行の真ん中にあるとして数える（Vim の set_fraction）。
			r.fraction = (wrow*fractionMult + fractionMult/2) / prev
		}
	}
	if h > 0 && (h < lineCount || r.top > 0) {
		wrow := (r.fraction*h - 1) / fractionMult
		r.top = min(max(cur.line-wrow, 0), max(lineCount-1, 0))
	}
	return prev > 0
}

// span は、1 行の中の、属性を重ねるバイトの範囲 [start, end) である。
// end が行の長さより大きければ、行末の 1 セル（行の区切りを表すセル）も含む。
type span struct {
	start, end int
	order      int // 重ねる順。大きい方を上に重ねる
	attr       Attr
}

// 重ねる順の基準。構文の色、診断の順に重ねる。
const diagnosticOrder = 1 << 40

// drawer は、1 回の描画の作業の状態である。
type drawer struct {
	r     *Renderer
	g     *Grid
	theme Theme
	text  Text
	f     *Frame
	cur   cursorPlace
	diags [][]span // 表示領域の行ごとの診断の範囲
	sel   []span   // 表示領域の行ごとの、選択の範囲（end が 0 なら選択なし）
	sweep sweep    // 行ごとに使い回す作業の領域
}

// collectSpans は、診断と選択の範囲を、表示領域の行ごとに分ける。
// 構文の色は Decorations が行ごとに分けて持っているので、ここでは分けない。
func (d *drawer) collectSpans(rows int) {
	d.diags = make([][]span, rows)
	d.sel = make([]span, rows)
	top := d.r.top
	for i, dg := range d.f.Diagnostics {
		a := d.theme.attr("diagnostic." + string(dg.Severity))
		if a == (Attr{}) {
			continue
		}
		s, e := dg.Range.Start, dg.Range.End
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
			if s == e {
				b1 = b0 + 1
			}
			if b0 < b1 {
				d.diags[n-top] = append(d.diags[n-top], span{start: b0, end: b1, order: diagnosticOrder + i, attr: a})
			}
		}
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

// sweep は、1 行の範囲を左から順に重ねるための作業の領域である。
// 文字を左から順に渡すと、その文字に掛かる範囲だけを active に持つので、文字ごとに行の範囲の全部をなめずに済む。
type sweep struct {
	spans  []span     // 開始の順に並べた、その行の範囲
	next   int        // まだ active に入れていない最初の範囲
	active []span     // いま見ている文字に掛かる範囲
	dirty  bool       // active の並びを重ねる順に直す必要があるか
	decos  []decoSpan // その行の構文の色の範囲（Decorations.appendLine の結果）
}

// reset は、行の範囲を空にする。
func (w *sweep) reset() {
	w.spans, w.active = w.spans[:0], w.active[:0]
	w.next, w.dirty = 0, false
}

// advance は、バイトの範囲 [off, off+size) の文字へ進む。size が 0 なら行末のセルを表す。
// 文字は左から順に渡す必要がある。
func (w *sweep) advance(off, size int) {
	hi := off + max(size, 1)
	for w.next < len(w.spans) && w.spans[w.next].start < hi {
		w.active = append(w.active, w.spans[w.next])
		w.next++
		w.dirty = true
	}
	k := 0
	for _, sp := range w.active {
		if sp.end > off {
			w.active[k] = sp
			k++
		}
	}
	w.active = w.active[:k]
}

// attr は、いまの文字に掛かる範囲の属性を、base の上に重ねる順に重ねて返す。
func (w *sweep) attr(base Attr) Attr {
	if w.dirty {
		slices.SortFunc(w.active, func(a, b span) int { return cmp.Compare(a.order, b.order) })
		w.dirty = false
	}
	for _, sp := range w.active {
		base = base.Over(sp.attr)
	}
	return base
}

// prepare は、表示領域の row 行目（バッファの line 行目、長さ ln）の構文の色と診断の範囲を sweep に詰める。
//
// req: FR-35
func (d *drawer) prepare(row, line, ln int) {
	w := &d.sweep
	w.reset()
	w.decos = d.f.Decorations.appendLine(w.decos[:0], line)
	for _, ds := range w.decos {
		a := d.theme.attr(d.f.Decorations.style(ds))
		start, end := min(ds.start, ln), min(ds.end, ln)
		if a == (Attr{}) || start >= end {
			continue
		}
		w.spans = append(w.spans, span{start: start, end: end, order: ds.order, attr: a})
	}
	if len(d.diags[row]) > 0 {
		w.spans = append(w.spans, d.diags[row]...)
		slices.SortStableFunc(w.spans, func(a, b span) int { return cmp.Compare(a.start, b.start) })
	}
}

// attrAt は、バッファの line 行目の、バイトの範囲 [off, off+size) の文字の属性を返す。size が 0 なら行末のセルを表す。
// 同じ行の中では、off の小さい順に呼ぶ必要がある。
func (d *drawer) attrAt(row, line, off, size int, base Attr) Attr {
	d.sweep.advance(off, size)
	a := d.sweep.attr(base)
	sp := d.sel[row]
	covers := sp.start < off+max(size, 1) && sp.end > off
	// カーソルの文字は反転しない。端末のカーソルが反転で描かれても文字が読めるようにするためで、Vim と同じ。
	if sp.end > 0 && covers && (line != d.cur.line || off != d.cur.byte) {
		a = a.Over(sp.attr)
	}
	return a
}

// drawLine は、バッファの line 行目を、表示領域の row 行目に描く。
//
// 行の先頭から文字の列を数え直すので、時間は行の長さ（表示の左の端より左の部分を含む）に比例する。
// 文字ごとの割り当ては、^A などの表記を作るときと、行頭の結合文字だけである。
func (d *drawer) drawLine(row, line int) {
	s := d.text.Line(line)
	left, w, x0 := d.r.left, d.r.textWidth(), d.r.textLeft()
	tabStop := tabStopOr(d.r.TabStop)
	d.prepare(row, line, len(s))
	put := func(x int, c Cell) {
		if x >= 0 && x < w {
			d.g.set(row, x0+x, c)
		}
	}
	col := 0
	for off := 0; off < len(s) && col < left+w; {
		gl := nextGlyph(s, off, col, tabStop)
		col += gl.width
		off += gl.size
		if gl.col+gl.width <= left {
			d.sweep.advance(gl.off, gl.size)
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
				put(x+i, blank(a))
			}
		case glyphSpecial:
			for i := range len(gl.text) {
				put(x+i, Cell{Text: gl.text[i : i+1], Width: 1, Attr: a})
			}
		case glyphText:
			switch {
			case gl.width < 2:
				put(x, Cell{Text: gl.text, Width: 1, Attr: a})
			case x < 0:
				// 全角文字の左半分が画面の左の端より左にある。Vim と同じく "<" を出す。
				put(0, Cell{Text: "<", Width: 1, Attr: a.Over(d.theme.attr(StyleNonText))})
			case x+1 >= w:
				// 右の端に半分だけ掛かる全角文字は、Vim と同じく最後の列に ">" を出す。
				put(x, Cell{Text: ">", Width: 1, Attr: a.Over(d.theme.attr(StyleNonText))})
			default:
				put(x, Cell{Text: gl.text, Width: 2, Attr: a})
				put(x+1, Cell{Width: 0, Attr: a})
			}
		}
	}
	if col >= left && col < left+w {
		if a := d.attrAt(row, line, len(s), 0, Attr{}); a != (Attr{}) {
			put(col-left, blank(a))
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
			for i := range len(gl.text) {
				g.set(row, gl.col+i, Cell{Text: gl.text[i : i+1], Width: 1, Attr: a})
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

// drawStatus は、row 行目（バッファの表示領域のすぐ下）にステータス行を描く。
//
// 左にモードとバッファの名前と変更の有無（[+]）を、右にマクロの記録中の表示と、カーソルの位置と、表示している範囲の位置を出す。
// カーソルの位置は Vim のルーラーと同じく「行,バイトの列-表示の列」で、2 つの列が同じなら表示の列を省き、空の行では "0-1" と出す。
// 範囲の位置は Vim と同じく All、Top、Bot、または上に隠れた行の割合である。
// 幅が足りなければ、左の部分を右の端で切る。
func (r *Renderer) drawStatus(g *Grid, theme Theme, text Text, f *Frame, cur cursorPlace, row int) {
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

// drawCommandLine は、最後の行にコマンドラインかメッセージを描く。選択肢の問い（Frame.Choice）があれば、どちらより先に問いを描く。
// コマンドラインモードでは ":" と入力中の文字列を出し、カーソル（縦線）を ok が true で返す。
// 入力中の文字列が幅に収まらなければ、末尾が見えるように先頭の側を隠す。
func (r *Renderer) drawCommandLine(g *Grid, theme Theme, f *Frame) (Cursor, bool) {
	row := r.Height - 1
	if f.Choice != nil {
		// 選択肢の問いの間のキーは問いへの答えにする（internal/editor）ので、コマンドラインモードでも問いを出す。
		return r.drawChoice(g, theme, f.Choice), true
	}
	if f.Mode == ModeCommandLine {
		prefix := ':'
		if f.CommandLineType != 0 {
			prefix = f.CommandLineType
		}
		s := string(prefix) + f.CommandLine
		tabStop := tabStopOr(r.TabStop)
		// 末尾の後ろのカーソルの 1 列を残して収まるまで、先頭の文字を落とす。
		for s != "" && StringWidth(s, tabStop)+1 > r.Width {
			s = s[nextGlyph(s, 0, 0, tabStop).size:]
		}
		end := r.drawText(g, row, 0, r.Width, s, Attr{})
		return Cursor{Row: row, Col: min(end, max(r.Width-1, 0)), Shape: CursorBar, Visible: r.Width > 0}, true
	}
	lines := textLines(f.Message.Text)
	if len(lines) == 0 {
		return Cursor{}, false
	}
	style := StyleMessageInfo
	switch f.Message.Level {
	case MessageInfo:
	case MessageWarning:
		style = StyleMessageWarning
	case MessageError:
		style = StyleMessageError
	}
	if len(lines) > 1 {
		if !f.HitEnter {
			r.drawMessageLines(g, lines, theme.attr(style), "", Attr{})
			return Cursor{}, false
		}
		end := r.drawMessageLines(g, lines, theme.attr(style), HitEnterPrompt, theme.attr(StyleMessageInfo))
		return Cursor{Row: row, Col: min(end, max(r.Width-1, 0)), Shape: CursorBlock, Visible: r.Width > 0}, true
	}
	r.drawText(g, row, 0, r.Width, lines[0], theme.attr(style))
	return Cursor{}, false
}
