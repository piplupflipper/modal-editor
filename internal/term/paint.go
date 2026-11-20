package term

import (
	"bytes"
	"strconv"

	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// Painter は、ui の画面（ui.Screen）を端末へ書く制御シーケンスにする。
// 前回に書いた格子を覚えておき、変わったセルだけを書く。ゼロ値で使え、最初の Paint は画面の全体を書く。
//
// 書き方は次のとおりである。
//
//   - 前回と格子の大きさが違うとき（最初と、端末の大きさが変わったとき）は、画面を消して（ED 2）から、空白でないセルを書く
//   - 変わったセルの並びの先頭へ CUP で移り、続くセルはカーソルが進むのに任せて続けて書く
//   - 全角文字は 2 つのセルを 1 つとして扱う。左右のどちらかが変わったら、文字ごと書き直す。
//     前回の全角文字の片側だけを上書きすると、端末が残りの半分を消すので、前回の全角文字に掛かるセルも書き直す。
//     ui.Cell の約束（続きのセルは左のセルと同じ属性）を守る格子では片側だけが変わることは無いが、守らない格子でも画面を崩さないために行う
//   - 文字の属性（SGR）は、前に書いたセルと属性が違うときだけ切り替える。切り替えは、0 で戻してから要る属性を並べる 1 つの SGR にする
//   - 書いている間はカーソルを隠し（DECTCEM）、書き終えたらカーソルを置いて形（DECSCUSR）を変え、表示する
//   - 既定の属性の空白（以下、既定の空白）は、空白の文字を並べずに消す制御シーケンスで書くことがある。
//     変わったセルから行末までが既定の空白なら、EL（行末まで消す）で消し、その行の残りは書かない。
//     そうでなくても、変わった既定の空白の並びが、前回の全角文字の右の半分の列を含むなら、ECH（n 文字を消す）で消す。
//     どちらも、消す前に属性を既定に戻す（EL と ECH は今の属性の背景の色で消すため）
//
// 既定の空白を消す制御シーケンスで書くのは、tmux の中で、前回の全角文字の右の半分が端末に残るのを避けるためである（#297）。
// tmux 3.6 は、全角文字の左の半分に空白が書かれると、自分の画面の右の半分を既定の空白にし、外側の端末には知らせない。
// 続けて右の半分の列に既定の空白が書かれても、自分の画面と同じなので、外側の端末へは空白を送らずにカーソルを右へ進める（CUF）。
// 外側の端末が、左の半分を上書きされた全角文字の右の半分を消さないと、右の半分が残る。
// tmux は、中で書かれた EL と ECH を、外側の端末へも消す操作として送る。
//
// 端末の状態（属性、カーソルの位置と形と表示）は Painter が覚えている前提で差分を書く。
// 端末へ別のものを書いた後（端末を一時的に戻して別のプログラムを動かした後など）は Invalidate を呼ぶ。
type Painter struct {
	prev []ui.Cell
	w, h int
	// valid は、prev と、以下の端末の状態の記憶が、端末の画面と合っているかどうかである。
	valid bool

	attr          ui.Attr
	shape         ui.CursorShape
	cursorVisible bool
	cursorRow     int
	cursorCol     int
}

// Invalidate は、次の Paint で画面の全体を書き直させる。
func (p *Painter) Invalidate() { p.valid = false }

// Paint は、s を端末に出すための制御シーケンスを buf へ書く。前回と同じ画面なら何も書かない。
func (p *Painter) Paint(buf *bytes.Buffer, s ui.Screen) {
	g := s.Grid
	var w, h int
	if g != nil {
		w, h = g.Width(), g.Height()
	}
	hidden := false
	hide := func() {
		if !hidden {
			if p.cursorVisible || !p.valid {
				buf.WriteString("\x1b[?25l")
			}
			hidden = true
		}
	}
	if !p.valid || w != p.w || h != p.h {
		hide()
		buf.WriteString("\x1b[0m\x1b[H\x1b[2J")
		p.attr = ui.Attr{}
		p.w, p.h = w, h
		p.prev = make([]ui.Cell, w*h)
		for i := range p.prev {
			p.prev[i] = ui.Cell{Text: " ", Width: 1}
		}
		// 形は不明なので、必ず書かせる。
		p.shape = -1
		p.cursorVisible = false
		p.valid = true
	}

	// 端末のカーソルの位置。不明なら row を -1 にする。
	row, col := -1, -1
	changed := make([]bool, w)
	for r := range h {
		cur := g.Row(r)
		prev := p.prev[r*w : (r+1)*w]
		if !markChanged(changed, cur, prev) {
			continue
		}
		// tail は、行末まで既定の空白が続く並びの先頭の列である。
		tail := w
		for tail > 0 && isDefaultBlank(cur, tail-1) {
			tail--
		}
		for c := 0; c < w; c++ {
			if !changed[c] {
				continue
			}
			text, width, attr, ok := outCell(cur, c)
			if !ok {
				continue // 左の全角文字と一緒に書いた
			}
			if c >= tail {
				hide()
				p.erase(buf, &row, &col, r, c, "\x1b[K")
				break
			}
			if n, wide := blankRun(changed, cur, prev, c); wide {
				hide()
				p.erase(buf, &row, &col, r, c, "\x1b["+strconv.Itoa(n)+"X")
				c += n - 1
				continue
			}
			hide()
			if row != r || col != c {
				writeCUP(buf, r, c)
			}
			if attr != p.attr {
				writeSGR(buf, attr)
				p.attr = attr
			}
			buf.WriteString(text)
			row, col = r, c+width
			if col >= w {
				// 右端の後のカーソルの位置は端末によって違うので、不明にする。
				row = -1
			}
		}
		copy(prev, cur)
	}

	cs := s.Cursor
	if !cs.Visible {
		if !hidden && p.cursorVisible {
			buf.WriteString("\x1b[?25l")
		}
		p.cursorVisible = false
		return
	}
	if cs.Shape != p.shape {
		buf.WriteString("\x1b[")
		buf.WriteString(strconv.Itoa(decscusr(cs.Shape)))
		buf.WriteString(" q")
		p.shape = cs.Shape
	}
	// セルを書いた（hidden）なら、カーソルはセルの後ろへ動いているので、置き直す。
	if hidden || cs.Row != p.cursorRow || cs.Col != p.cursorCol || !p.cursorVisible {
		writeCUP(buf, cs.Row, cs.Col)
	}
	if hidden || !p.cursorVisible {
		buf.WriteString("\x1b[?25h")
	}
	p.cursorRow, p.cursorCol, p.cursorVisible = cs.Row, cs.Col, true
}

// markChanged は、changed[c] に、行の c 列目を書き直すかどうかを入れる。1 つでも書き直すなら true を返す。
// 全角文字に掛かるセルは、組の両方を書き直す（今回の組と、前回の組の両方）。
func markChanged(changed []bool, cur, prev []ui.Cell) bool {
	anyChanged := false
	for c := range changed {
		changed[c] = cur[c] != prev[c]
		anyChanged = anyChanged || changed[c]
	}
	if !anyChanged {
		return false
	}
	// 組を広げると、隣の組にまた掛かることがあるので、変わらなくなるまで繰り返す。
	for again := true; again; {
		again = false
		for c := range changed {
			if !changed[c] {
				continue
			}
			wide := cur[c].Width == 2 || prev[c].Width == 2
			cont := cur[c].Width == 0 || prev[c].Width == 0
			if wide && c+1 < len(changed) && !changed[c+1] {
				changed[c+1], again = true, true
			}
			if cont && c > 0 && !changed[c-1] {
				changed[c-1], again = true, true
			}
		}
	}
	return true
}

// erase は、端末の row 行 col 列（0 から数える）へ移り、属性を既定に戻してから、消す制御シーケンス seq（EL か ECH）を書く。
// curRow と curCol は端末のカーソルの位置の記憶で、seq はカーソルを動かさないので、row 行 col 列にする。
func (p *Painter) erase(buf *bytes.Buffer, curRow, curCol *int, row, col int, seq string) {
	if *curRow != row || *curCol != col {
		writeCUP(buf, row, col)
	}
	if p.attr != (ui.Attr{}) {
		writeSGR(buf, ui.Attr{})
		p.attr = ui.Attr{}
	}
	buf.WriteString(seq)
	*curRow, *curCol = row, col
}

// outCell は、行 cur の c 列目のセルを、端末へ書く文字と、その幅（1 か 2）と属性にする。
// 左の全角文字と一緒に書く続きのセルなら、ok を false にする。
// 左に全角文字の無い続きのセルと、右端からはみ出す全角文字は、書けないので、その属性の空白にする。
func outCell(cur []ui.Cell, c int) (text string, width int, attr ui.Attr, ok bool) {
	cell := cur[c]
	if cell.Width == 0 {
		if c > 0 && cur[c-1].Width == 2 {
			return "", 0, ui.Attr{}, false
		}
		return " ", 1, cell.Attr, true
	}
	text, width = cell.Text, cell.Width
	if text == "" {
		text = " "
	}
	if width == 2 && c+1 >= len(cur) {
		text, width = " ", 1
	}
	if width != 2 {
		width = 1
	}
	return text, width, cell.Attr, true
}

// isDefaultBlank は、行 cur の c 列目のセルが、端末へ書くと既定の属性の空白になるかどうかである。
func isDefaultBlank(cur []ui.Cell, c int) bool {
	text, width, attr, ok := outCell(cur, c)
	return ok && text == " " && width == 1 && attr == ui.Attr{}
}

// blankRun は、c 列目から続く、変わった既定の空白の並びの長さ n を返す。
// 並びが前回の全角文字の右の半分の列を含むなら、wide を true にする。c 列目が既定の空白でなければ、n は 0 である。
func blankRun(changed []bool, cur, prev []ui.Cell, c int) (n int, wide bool) {
	for i := c; i < len(cur) && changed[i] && isDefaultBlank(cur, i); i++ {
		n++
		wide = wide || prev[i].Width == 0
	}
	return n, wide
}

// decscusr は、カーソルの形を DECSCUSR の引数にする。点滅しない形を使う。
func decscusr(s ui.CursorShape) int {
	switch s {
	case ui.CursorBar:
		return 6
	case ui.CursorUnderline:
		return 4
	case ui.CursorBlock:
		return 2
	}
	return 0
}

// writeCUP は、カーソルを row 行 col 列（0 から数える）へ移す CUP を書く。
func writeCUP(buf *bytes.Buffer, row, col int) {
	buf.WriteString("\x1b[")
	buf.WriteString(strconv.Itoa(row + 1))
	buf.WriteByte(';')
	buf.WriteString(strconv.Itoa(col + 1))
	buf.WriteByte('H')
}

// writeSGR は、文字の属性を a にする SGR を書く。0 で戻してから、要る属性を並べる。
func writeSGR(buf *bytes.Buffer, a ui.Attr) {
	buf.WriteString("\x1b[0")
	if a.Bold {
		buf.WriteString(";1")
	}
	if a.Italic {
		buf.WriteString(";3")
	}
	switch {
	case a.Undercurl:
		buf.WriteString(";4:3")
	case a.Underline:
		buf.WriteString(";4")
	}
	if a.Reverse {
		buf.WriteString(";7")
	}
	writeColor(buf, a.Fg, 30, 90, 38)
	writeColor(buf, a.Bg, 40, 100, 48)
	writeUnderlineColor(buf, a.Sp)
	buf.WriteByte('m')
}

// writeUnderlineColor は、下線と波線の色（SGR 58）の引数を、':' で区切った下位の引数の形（58:5:n、58:2::r:g:b）で書く。
// ';' で区切ると、58 を知らない端末が続く 5 や 2 や色の値を、点滅や薄い表示などの別の SGR として読むおそれがある。
// ':' の形は、ITU-T T.416 の下位の引数の書き方である（PR #111 のレビューの推奨 9）。
// どの端末がこの形を解釈し、知らない端末が引数を 1 つとして捨てるかは、端末ごとに確かめていない。
func writeUnderlineColor(buf *bytes.Buffer, c ui.Color) {
	if n, ok := c.Index(); ok {
		buf.WriteString(";58:5:")
		buf.WriteString(strconv.Itoa(int(n)))
		return
	}
	if r, g, b, ok := c.RGB(); ok {
		buf.WriteString(";58:2::")
		buf.WriteString(strconv.Itoa(int(r)))
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(int(g)))
		buf.WriteByte(':')
		buf.WriteString(strconv.Itoa(int(b)))
	}
}

// writeColor は、色 c の SGR の引数を書く。
// 16 色の番号は base（0〜7）と bright（8〜15）の短い形にし、それ以外の番号は ext;5;n、24 ビット色は ext;2;r;g;b にする。
// 文字と背景の色の 38 と 48 は ';' の形で書く（下線の色は writeUnderlineColor が ':' の形で書く）。
func writeColor(buf *bytes.Buffer, c ui.Color, base, bright, ext int) {
	if n, ok := c.Index(); ok {
		switch {
		case n < 8:
			buf.WriteByte(';')
			buf.WriteString(strconv.Itoa(base + int(n)))
		case n < 16:
			buf.WriteByte(';')
			buf.WriteString(strconv.Itoa(bright + int(n) - 8))
		default:
			buf.WriteByte(';')
			buf.WriteString(strconv.Itoa(ext))
			buf.WriteString(";5;")
			buf.WriteString(strconv.Itoa(int(n)))
		}
		return
	}
	if r, g, b, ok := c.RGB(); ok {
		buf.WriteByte(';')
		buf.WriteString(strconv.Itoa(ext))
		buf.WriteString(";2;")
		buf.WriteString(strconv.Itoa(int(r)))
		buf.WriteByte(';')
		buf.WriteString(strconv.Itoa(int(g)))
		buf.WriteByte(';')
		buf.WriteString(strconv.Itoa(int(b)))
	}
}
