package ui

import "strings"

// Color は、文字か背景の色である。
// ゼロ値の ColorDefault は端末の既定の色を表し、それ以外は 256 色の番号を Indexed で表す。
// 端末の制御のライブラリ（#9 で裁定待ち）が決まったら、そのライブラリの色の型へ変換する。
type Color uint16

// ColorDefault は、端末の既定の色である。
const ColorDefault Color = 0

// Indexed は、256 色の番号 n（0〜255）の色を返す。0〜15 は端末の 16 色である。
func Indexed(n uint8) Color {
	return Color(n) + 1
}

// Index は、256 色の番号を返す。ColorDefault なら ok が false である。
func (c Color) Index() (n uint8, ok bool) {
	if c == ColorDefault {
		return 0, false
	}
	return uint8(c - 1), true //nolint:gosec // c は Indexed で作った 1〜256 の値なので、1 を引けば uint8 に収まる
}

// Attr は、セルの表示の属性である。ゼロ値は、端末の既定の色で飾りの無い表示である。
type Attr struct {
	Fg Color // 文字の色
	Bg Color // 背景の色
	Sp Color // 下線と波線の色。ColorDefault なら文字の色
	// Bold は太字、Italic は斜体、Underline は下線、Undercurl は波線、Reverse は文字と背景の色の入れ替えである。
	Bold, Italic, Underline, Undercurl, Reverse bool
}

// Over は、a の上に o を重ねた属性を返す。
// 色は o が ColorDefault でなければ o の色にし、飾り（太字など）は a と o のどちらかにあれば付ける。
func (a Attr) Over(o Attr) Attr {
	if o.Fg != ColorDefault {
		a.Fg = o.Fg
	}
	if o.Bg != ColorDefault {
		a.Bg = o.Bg
	}
	if o.Sp != ColorDefault {
		a.Sp = o.Sp
	}
	a.Bold = a.Bold || o.Bold
	a.Italic = a.Italic || o.Italic
	a.Underline = a.Underline || o.Underline
	a.Undercurl = a.Undercurl || o.Undercurl
	a.Reverse = a.Reverse || o.Reverse
	return a
}

// Cell は、格子の 1 つのセルである。
//
// 全角文字は 2 つのセルを使う。左のセルが Width 2 で文字を持ち、右のセルは Width 0、Text が空の続きのセルになる。
// 続きのセルの Attr は左のセルと同じにする。
type Cell struct {
	// Text は、セルに出す文字列である。1 文字と、それに続く結合文字から成る。空白のセルは " " である。
	Text string
	// Width は、セルの文字が占める列数（1 か 2）である。全角文字の続きのセルは 0 である。
	Width int
	Attr  Attr
}

// blank は、属性 a の空白のセルを返す。
func blank(a Attr) Cell {
	return Cell{Text: " ", Width: 1, Attr: a}
}

// Grid は、画面の内容を行 × 列のセルで表したものである。
// 端末へ出す部分は、前の Grid との差分のセルだけを書く形にできる。
type Grid struct {
	width, height int
	cells         []Cell
}

// NewGrid は、幅 width、高さ height の、空白で埋めた Grid を返す。負の大きさは 0 にする。
func NewGrid(width, height int) *Grid {
	width, height = max(width, 0), max(height, 0)
	g := &Grid{width: width, height: height, cells: make([]Cell, width*height)}
	for i := range g.cells {
		g.cells[i] = blank(Attr{})
	}
	return g
}

// Width は、列の数を返す。
func (g *Grid) Width() int { return g.width }

// Height は、行の数を返す。
func (g *Grid) Height() int { return g.height }

// At は、row 行 col 列のセルを返す（どちらも 0 から数える）。範囲の外では空白のセルを返す。
func (g *Grid) At(row, col int) Cell {
	if row < 0 || row >= g.height || col < 0 || col >= g.width {
		return blank(Attr{})
	}
	return g.cells[row*g.width+col]
}

// set は、row 行 col 列にセルを置く。範囲の外なら何もしない。
func (g *Grid) set(row, col int, c Cell) {
	if row < 0 || row >= g.height || col < 0 || col >= g.width {
		return
	}
	g.cells[row*g.width+col] = c
}

// Row は、row 行目のセルの写しを返す。
func (g *Grid) Row(row int) []Cell {
	if row < 0 || row >= g.height {
		return nil
	}
	return append([]Cell(nil), g.cells[row*g.width:(row+1)*g.width]...)
}

// RowText は、row 行目の文字を、全角文字の続きのセルを飛ばしてつないだ文字列を返す。行末の空白も含める。
func (g *Grid) RowText(row int) string {
	var b strings.Builder
	for _, c := range g.Row(row) {
		if c.Width > 0 {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// String は、各行の RowText を改行でつないだ文字列を返す。
func (g *Grid) String() string {
	rows := make([]string, g.height)
	for i := range rows {
		rows[i] = g.RowText(i)
	}
	return strings.Join(rows, "\n")
}
