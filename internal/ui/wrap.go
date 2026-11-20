package ui

// 長い行の折り返し（FR-156。基本設計の「行の折り返し」）。
//
// Frame.Wrap が true なら、バッファの行を本文の幅（textWidth）で見た目の行に分けて描く（Vim の 'wrap'。'linebreak' はオフで、文字の切れ目で分ける）。
// 分け方は wrapper の 1 か所で決め、描画（drawWrapped）、カーソルの位置（placeCursor）、スクロール（scrollWrapped）、gj と gk（WrapLines）が使う。
// 行番号の列、印の列、構文と診断と検索の一致と範囲の色、選択の色は、見た目の行に分けた後の表示領域の行（viewRow）と、
// バイトの範囲（span）の上に載るので、折り返しのための分岐を持たない。
//
// 見せ方は Neovim 0.12.5 の既定（'display' は lastline、'showbreak' は空、'smoothscroll' はオフ）に合わせた。2026-11-20 に tmux の
// 41 列の画面で、半角の行、全角の行、タブを含む行、画面より長い行を確かめた。
//
//   - 全角文字が見た目の行の最後の 1 列に入らなければ、その列に ">"（StyleNonText）を出し、文字を次の見た目の行へ送る
//   - タブと ^A、<80> の形の表記は、見た目の行の右の端で分けて、残りを次の見た目の行に描く
//   - 続きの見た目の行の頭には、記号 SymbolShowBreak（既定は空）を StyleNonText で描き、その右から文字を描く（Vim の 'showbreak'）
//   - 行番号の列と印の列は、行の最初の見た目の行にだけ出し、続きの行は空ける（行番号の列は StyleLineNr の空白で埋める）
//   - 表示領域の最後に収まりきらない行は、入るところまで描き、最後の行の右の 3 列に "@@@"（StyleNonText）を出す。
//     ただし、その行が表示領域の最初の行なら出さない（nvim 0.12.5 で、画面より長い行の先頭を最初の行にしたときに出なかった）
//   - 表示領域より高い行は、カーソルの見た目の行が入るように、行の途中の見た目の行から描く（Vim の w_skipcol。Renderer.skip）。
//     そのときは、表示領域の最初の行の左の 3 列に "<<<"（StyleNonText）を出す（nvim は行番号の列の上にも描く）
//   - インサートモードで、ちょうど見た目の行の幅で終わる行の行末にいるカーソルは、次の見た目の行の左の端に置く（nvim と同じく、
//     行の見た目の行の数は増やさない）
//   - 折り返すときは横にスクロールしない
//
// タブの幅は、行の先頭からの表示の列で決める（折り返さないときと同じ）。Vim は 'showbreak' の列もタブの幅の計算に入れるので、
// SymbolShowBreak が空でないときは、続きの行のタブの幅が Vim と違うことがある（docs/vim-differences.md）。

const (
	// lastLineMark は、表示領域の最後に収まりきらない行の、最後の行の右の端に出す印である（Vim の 'display' の lastline。'fillchars' の lastline の既定）。
	lastLineMark = "@@@"
	// skipMark は、行の途中の見た目の行から描くときに、表示領域の最初の行の左の端に出す印である（Neovim の w_skipcol の "<<<"）。
	skipMark = "<<<"
)

// wrapper は、バッファの 1 行を見た目の行に分ける規則である。
type wrapper struct {
	width   int // 見た目の行の列数（本文の幅）。1 以上
	sbw     int // 続きの見た目の行の頭に描く記号の列数。width-2 以下（全角文字を 1 つは置ける）
	tabStop int
}

// wrapPos は、行を折り返したときのセルの位置である。row は行の見た目の行のうち何番目か（0 から数える）、
// x はその見た目の行の左の端（本文の左の端）からの列である。続きの見た目の行は、記号の列（sbw）から文字を置く。
type wrapPos struct{ row, x int }

// less は、p が q より前（上の行か、同じ行の左）にあるかを返す。
func (p wrapPos) less(q wrapPos) bool {
	return p.row < q.row || p.row == q.row && p.x < q.x
}

// wrapper は、直前の layout の本文の幅と、記号の設定で、行の分け方を返す。
func (r *Renderer) wrapper() wrapper {
	w := max(r.textWidth(), 1)
	sbw := StringWidth(r.symbol(SymbolShowBreak), DefaultTabStop)
	if sbw > w-2 {
		sbw = 0
	}
	return wrapper{width: w, sbw: sbw, tabStop: tabStopOr(r.TabStop)}
}

// rowStart は、見た目の row 行目の、文字を置き始める列を返す。
func (w wrapper) rowStart(row int) int {
	if row == 0 {
		return 0
	}
	return w.sbw
}

// nextRow は、p を次の見た目の行の、文字を置き始める列へ進める。
func (w wrapper) nextRow(p *wrapPos) {
	p.row++
	p.x = w.sbw
}

// place は、文字 gl を p から置き、文字の最初のセルの位置を first で返して、p を文字の次のセルへ進める。
// 次のセルが見た目の行の右の端を越えても、次の文字を置くまでは次の見た目の行へ進めない。
// このため、置いた後の p.row は、文字の最後のセルの見た目の行である。
//
// 全角文字が見た目の行に収まらなければ次の見た目の行へ送り、送る前の位置（">" を出す位置）を pad で返す（padded）。
// 見た目の行の頭でも収まらない（幅が 1 列しかない）ときは送らずに置き、右の端からはみ出させる（描くときは ">" にする）。
// タブと ^A などの表記は、セルごとに置き、右の端で分ける。
func (w wrapper) place(p *wrapPos, gl glyph) (first, pad wrapPos, padded bool) {
	if gl.kind == glyphText {
		switch {
		case gl.width == 2 && p.x+2 > w.width && p.x > w.rowStart(p.row):
			pad, padded = *p, p.x < w.width
			w.nextRow(p)
		case p.x >= w.width:
			w.nextRow(p)
		}
		first = *p
		p.x += gl.width
		return first, pad, padded
	}
	for i := range gl.width {
		if p.x >= w.width {
			w.nextRow(p)
		}
		if i == 0 {
			first = *p
		}
		p.x++
	}
	return first, pad, padded
}

// eol は、行の最後の文字を置いた後の p から、行末の位置（行の区切りを表すセル）を返す。
// 見た目の行の右の端を越えていれば、次の見た目の行の左の端にする。
func (w wrapper) eol(p wrapPos) wrapPos {
	if p.x >= w.width {
		w.nextRow(&p)
	}
	return p
}

// rows は、行 s を折り返した見た目の行の数（1 以上）を返す。空の行は 1 行である。
// 行末の位置が次の見た目の行に出る（ちょうど幅で終わる）ときも、その行は数えない。
func (w wrapper) rows(s string) int {
	p := wrapPos{}
	col := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, w.tabStop)
		w.place(&p, gl)
		col += gl.width
		off += gl.size
	}
	return p.row + 1
}

// locate は、行 s の表示の列 col（行の先頭から数える。Columns の列）のセルの位置を返す。
// col が行の表示幅以上なら、行末の位置から右へ col と表示幅の差だけ進めた位置を返す。x は見た目の行の幅を超えない（width-1 で止める）。
// これは Vim の nv_screengo が、望みの列を最後の見た目の行の最後の列で止めるのと同じである。
func (w wrapper) locate(s string, col int) wrapPos {
	p := wrapPos{}
	c := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, c, w.tabStop)
		q := p
		first, _, _ := w.place(&p, gl)
		if col < gl.col+gl.width {
			if gl.kind == glyphText {
				return wrapPos{row: first.row, x: min(first.x+max(col-gl.col, 0), w.width-1)}
			}
			for range max(col-gl.col, 0) + 1 {
				if q.x >= w.width {
					w.nextRow(&q)
				}
				q.x++
			}
			q.x--
			return q
		}
		c += gl.width
		off += gl.size
	}
	p.x = min(p.x+col-c, w.width-1)
	return p
}

// cursor は、行 s の byteOff の文字にカーソルを置く位置を返す。tabLast なら、タブの最後のセルにする（ノーマルモード）。
// byteOff が行の長さ以上なら、行末の位置（eol）を返す。
func (w wrapper) cursor(s string, byteOff int, tabLast bool) wrapPos {
	p := wrapPos{}
	col := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, w.tabStop)
		first, _, _ := w.place(&p, gl)
		if byteOff < off+gl.size {
			if tabLast && gl.kind == glyphTab {
				return wrapPos{row: p.row, x: p.x - 1}
			}
			return first
		}
		col += gl.width
		off += gl.size
	}
	return w.eol(p)
}

// rowCol は、行 s の見た目の row 行目の、最初のセルの表示の列（行の先頭から数える）を返す。
// row が見た目の行の数以上なら、行の表示幅を返す。
func (w wrapper) rowCol(s string, row int) int {
	if row <= 0 {
		return 0
	}
	p := wrapPos{}
	col := 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, w.tabStop)
		if gl.kind == glyphText {
			if first, _, _ := w.place(&p, gl); first.row >= row {
				return gl.col
			}
		} else {
			for i := range gl.width {
				if p.x >= w.width {
					w.nextRow(&p)
				}
				if p.row >= row {
					return gl.col + i
				}
				p.x++
			}
		}
		col += gl.width
		off += gl.size
	}
	return col
}

// byteAt は、行 s の見た目の row 行目の x 列にある文字の位置を返す。
// x がその見た目の行の最後の文字より右（全角文字を送った ">" の列を含む）なら、その見た目の行の最後の文字を返し、
// 続きの行の記号の列なら、その見た目の行の最初の文字を返す。row が見た目の行の数以上なら、行の最後の文字を返す。空の行は 0 である。
func (w wrapper) byteAt(s string, row, x int) int {
	p := wrapPos{}
	col, best, last := 0, -1, 0
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, w.tabStop)
		first, _, _ := w.place(&p, gl)
		if first.row > row {
			break
		}
		if p.row >= row {
			// この文字のセルが row 行目に掛かる。最後のセルが x 以上か、次の行まで続くなら、x はこの文字の上か左にある。
			if p.row > row || p.x-1 >= x {
				return off
			}
			best = off
		}
		last = off
		col += gl.width
		off += gl.size
	}
	if best >= 0 {
		return best
	}
	return last
}

// span は、行 s のうち、見た目の k0 行目から k1 行目の手前までに掛かる文字のバイトの範囲 [from, to) を返す。
// 検索の一致を集める範囲（searchSpans）に使う。
func (w wrapper) span(s string, k0, k1 int) (from, to int) {
	p := wrapPos{}
	col := 0
	from = -1
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, col, w.tabStop)
		first, _, _ := w.place(&p, gl)
		if first.row >= k1 {
			return max(from, off), off
		}
		if from < 0 && p.row >= k0 {
			from = off
		}
		col += gl.width
		off += gl.size
	}
	if from < 0 {
		from = len(s)
	}
	return from, len(s)
}

// WrapLines は、行を折り返して描くときの見た目の行の数え方である（gj と gk。FR-157）。Renderer.WrapLines で作る。
// internal/command の State.ScreenLines（target.ScreenLines）の、折り返しの位置の計算に使う（internal/editor がつなぐ）。
// internal/command とは同じ層なので、インターフェースを import せずに、同じ形のメソッドを持たせる。
//
// 列は、Columns と同じく行の先頭から数えた表示の列で、x は見た目の行の左の端（本文の左の端）からの列である。
type WrapLines struct {
	w wrapper
}

// WrapLines は、直前の Render の本文の幅で、行を折り返す数え方を返す。画面の大きさがまだ届いていなければ、幅を 1 列として数える。
func (r *Renderer) WrapLines() WrapLines {
	return WrapLines{w: r.wrapper()}
}

// Rows は、行 line を折り返した見た目の行の数（1 以上）を返す。
func (l WrapLines) Rows(line string) int { return l.w.rows(line) }

// Locate は、行 line の表示の列 col のセルが、何番目の見た目の行の、左の端から何列目にあるかを返す。
// col が行の表示幅以上なら、最後の見た目の行の、行末から col と表示幅の差だけ右の列を返す。列は見た目の行の幅から 1 を引いた数で止める。
func (l WrapLines) Locate(line string, col int) (row, x int) {
	p := l.w.locate(line, col)
	return p.row, p.x
}

// Column は、行 line の見た目の row 行目の、左の端から x 列目の表示の列を返す。
// x がその見た目の行の文字より右なら、見た目の行の最初の文字の列から数えて右へ伸ばした列を返す（望みの列に使う）。
func (l WrapLines) Column(line string, row, x int) int {
	return l.w.rowCol(line, row) + max(x-l.w.rowStart(row), 0)
}

// ByteAt は、行 line の見た目の row 行目の、左の端から x 列目にある文字の位置を返す。
// x がその見た目の行の最後の文字より右なら、その見た目の行の最後の文字を返す（全角文字を送った ">" の列も同じ）。
func (l WrapLines) ByteAt(line string, row, x int) int { return l.w.byteAt(line, row, x) }

// viewRow は、表示領域の 1 行に描く中身（見た目の行）である（Renderer.view）。
// 行番号の列、印の列、本文は、この行ごとの中身から描くので、折り返すかどうかで描き方を分けない。
type viewRow struct {
	// line は、描くバッファの行の番号である。見比べの詰め物の行なら -1 で、バッファの行数以上なら最終行より後の "~" の行である。
	line int
	// k は、行を折り返した見た目の行のうち何番目か（0 から数える）である。折り返さないときは 0 である。
	k int
}

// buildView は、スクロールの位置（top、topFill、skip）から、表示領域の rows 行の中身を決めて r.view に入れ、
// 最後まで描いた行の次の行を r.bot に入れる（最初の行を最後まで描けなくても、最初の行の次の行にする。nvim 0.12.5 のルーラーと同じ）。
// 見比べている間は、表示の最初の行の上に topFill 行、ほかの行の上にその行の詰め物の行（Diff.fill）を置く。
func (r *Renderer) buildView(text Text, f *Frame, rows int) {
	r.view = r.view[:0]
	n := text.LineCount()
	for range min(r.topFill, rows) {
		r.view = append(r.view, viewRow{line: -1})
	}
	r.bot = r.top
	for line := r.top; len(r.view) < rows; line++ {
		if line > r.top {
			for k := f.Diff.fill(line); k > 0 && len(r.view) < rows; k-- {
				r.view = append(r.view, viewRow{line: -1})
			}
			if len(r.view) >= rows {
				break
			}
		}
		if line >= n {
			r.view = append(r.view, viewRow{line: line})
			continue
		}
		h := r.height(text, line)
		k := 0
		if line == r.top {
			k = min(r.skip, h-1)
		}
		for ; k < h && len(r.view) < rows; k++ {
			r.view = append(r.view, viewRow{line: line, k: k})
		}
		if k == h {
			r.bot = line + 1
		}
	}
	r.bot = max(r.bot, r.top+1)
}

// rowLine は、直前の Render で、表示領域の i 行目に描いたバッファの行の番号を返す。詰め物の行なら -1 で、
// バッファの行数以上なら最終行より後の "~" の行である。プレビュー（drawPreview）が、行の色を付ける行を引くのに使う。
func (r *Renderer) rowLine(i int) int {
	if i >= 0 && i < len(r.view) {
		return r.view[i].line
	}
	return r.top + i
}

// cursorScreen は、カーソルを置く画面の行と列を返す。行を折り返すときは、カーソルの見た目の行と、その行の中の列から数える。
func (r *Renderer) cursorScreen(text Text, f *Frame, cur cursorPlace) (row, col int) {
	if !r.wrap {
		return r.screenRow(text, f, cur.line), r.textLeft() + cur.col - r.left
	}
	return r.screenRow(text, f, cur.line) + cur.row, r.textLeft() + cur.x
}

// scrollWrapped は、行を折り返すとき（FR-156）の scroll で、表示領域を h 行として、カーソルが見えるように最初の行と skip を決める。
// 横の位置は 0 にする。Vim 9.1 の update_topline と curs_columns（'scrolloff' が 0、'smoothscroll' がオフ）を、見た目の行の数で写した。
//
//   - カーソルの行が最初の行より上: 離れが max(h/2-1, 2) 行（バッファの行で数える）以上なら、カーソルの行を中ほどに置く（scrollHalfway）。
//     そうでなければカーソルの行を最初の行にする
//   - カーソルの行が最初の行より下: カーソルの行の全体が表示領域に入るまで、行の単位で下へスクロールする（Vim の w_botline の扱い）。
//     スクロールする見た目の行の数が (h+1)/2 より多い（折り返さないときの scrollRows と同じ境目）か、離れが h 行より大きければ、中ほどに置く
//   - カーソルの行が表示領域より高いときだけ、最初の行をカーソルの行にして、行の途中の見た目の行から見せる（skip）。
//     カーソルの見た目の行が隠れたら、隠れなくなる最小の分だけ skip を動かす（nvim 0.12.5 で gj、gk、$ を打って確かめた）
//
// 折り返さないときの scrollRows と違い、表示領域の中ほどの位置は Vim の scroll_cursor_halfway と同じく、上と下の見た目の行の数をそろえて決める。
func (r *Renderer) scrollWrapped(text Text, cur cursorPlace, h int) {
	r.left = 0
	if h <= 0 {
		return
	}
	if r.top != cur.line {
		r.skip = 0
	}
	switch {
	case cur.line < r.top:
		if r.top-cur.line >= max(h/2-1, 2) {
			r.scrollHalfway(text, cur, h)
		} else {
			r.top = cur.line
		}
	case cur.line > r.top:
		if cur.line-r.top > h {
			r.scrollHalfway(text, cur, h)
			break
		}
		used := 0
		for n := r.top; n <= cur.line; n++ {
			used += r.height(text, n)
		}
		if used <= h {
			break
		}
		// カーソルの行が入るところまで、上の行を外す。
		t, u := cur.line, r.height(text, cur.line)
		for t > r.top && u+r.height(text, t-1) <= h {
			t--
			u += r.height(text, t)
		}
		scrolled := 0
		for n := r.top; n < t; n++ {
			scrolled += r.height(text, n)
		}
		if scrolled > (h+1)/2 {
			r.scrollHalfway(text, cur, h)
		} else {
			r.top = t
		}
	}
	if r.top != cur.line || r.height(text, cur.line) <= h {
		r.skip = 0
		return
	}
	if cur.row < r.skip {
		r.skip = cur.row
	}
	if cur.row >= r.skip+h {
		r.skip = cur.row - h + 1
	}
}

// scrollHalfway は、カーソルの行を表示領域（h 行）の中ほどに置く。Vim 9.1 の scroll_cursor_halfway を見た目の行の数で写したもので、
// カーソルの行の下と上に、交互に行を足して、上と下の見た目の行の数をそろえる。下に行が無ければ、"~" の行を 1 行ずつ数える
// （このため、バッファの末尾の近くでは、末尾の行が表示領域の下の端に来る）。カーソルの行が表示領域より高ければ、その行を最初の行にする。
// skip は 0 にする（scrollWrapped が決め直す）。
func (r *Renderer) scrollHalfway(text Text, cur cursorPlace, h int) {
	n := text.LineCount()
	top := cur.line
	r.skip = 0
	used := r.height(text, cur.line)
	above, below := 0, 0
	lo, bo := cur.line, cur.line
	for used <= h && top > 0 {
		if below <= above {
			if bo < n-1 {
				bo++
				hh := r.height(text, bo)
				used += hh
				if used > h {
					break
				}
				below += hh
			} else {
				below++
			}
		}
		if below > above {
			lo--
			hh := r.height(text, lo)
			used += hh
			if used > h {
				break
			}
			above += hh
			top = lo
		}
	}
	r.top = top
}
