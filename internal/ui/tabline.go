package ui

// Tab は、タブの行に出すタブ 1 つである（FR-62。基本設計 4.4 の段階 3 のタブとバッファ）。
type Tab struct {
	// Name は、タブに出すバッファの名前である。空なら "[No Name]" と出す。
	Name string
	// Modified は、バッファに保存していない変更があるかどうかである。true なら名前の後に未保存の印（記号 SymbolModified）を出す。
	Modified bool
	// ReadOnly は、バッファの編集を禁止しているかどうかである。true なら名前の後に編集の禁止の印（記号 SymbolReadOnly）を出す（FR-87）。
	ReadOnly bool
	// Current は、カーソルのあるタブかどうかである。StyleTabLineSelected の色で描く。
	Current bool
}

// TabLineMode は、タブの行を出す場合を決める（設定の tabline。仮）。
type TabLineMode int

const (
	// TabLineMultiple は、タブが 2 つ以上あるときだけタブの行を出す（Vim の 'showtabline' の既定の 1 と同じ。既定）。
	TabLineMultiple TabLineMode = iota
	// TabLineAlways は、タブが 1 つでもタブの行を出す（Vim の 'showtabline' の 2）。
	TabLineAlways
	// TabLineNever は、タブの行を出さない（Vim の 'showtabline' の 0）。
	TabLineNever
)

// tabLineShown は、f のタブの行を出すかを返す。画面が低く、バッファの表示領域、ステータス行、コマンドラインの行を
// 1 行ずつも取れないときは出さない。
func (r *Renderer) tabLineShown(f *Frame) bool {
	if r.Height < 4 || len(f.Tabs) == 0 {
		return false
	}
	switch r.TabLine {
	case TabLineAlways:
		return true
	case TabLineNever:
		return false
	case TabLineMultiple:
	}
	return len(f.Tabs) >= 2
}

// tabLabel は、タブ t の見出しの文字列を返す。名前の前後に空白を 1 つずつ置き、未保存と編集の禁止の印を名前の後に続ける
// （ステータス行の " name [+] [RO]" と同じ並び）。
func (r *Renderer) tabLabel(t Tab) string {
	name := t.Name
	if name == "" {
		name = "[No Name]"
	}
	s := " " + name
	if t.Modified {
		s += " " + r.symbol(SymbolModified)
	}
	if t.ReadOnly {
		s += " " + r.symbol(SymbolReadOnly)
	}
	return s + " "
}

// drawTabLine は、画面の一番上の行にタブの行を描く（FR-62）。
// タブの見出しを左から並べ、カーソルのあるタブを StyleTabLineSelected、他を StyleTabLine、残りの列を StyleTabLineFill で描く。
// 見出しが幅に収まらなければ、カーソルのあるタブが収まるまで左のタブを省き、省いたことを左の端の "<" で示す。
// 右に収まらないタブの見出しは、収まる列で切る。
//
// req: FR-62, FR-87
func (r *Renderer) drawTabLine(g *Grid, theme Theme, tabs []Tab) {
	fill := theme.attr(StyleTabLineFill)
	for c := range r.Width {
		g.set(0, c, blank(fill))
	}
	labels := make([]string, len(tabs))
	widths := make([]int, len(tabs))
	cur := 0
	for i, t := range tabs {
		labels[i] = r.tabLabel(t)
		widths[i] = StringWidth(labels[i], r.TabStop)
		if t.Current {
			cur = i
		}
	}
	// カーソルのあるタブの右の端までが幅に収まるように、左のタブを省く。省いたら左の端の 1 列を "<" に使う。
	first := 0
	for first < cur {
		lead := 0
		if first > 0 {
			lead = 1
		}
		if sumTo(widths, first, cur)+lead <= r.Width {
			break
		}
		first++
	}
	col := 0
	if first > 0 {
		g.set(0, 0, Cell{Text: "<", Width: 1, Attr: theme.attr(StyleTabLine)})
		col = 1
	}
	want := col
	for i := first; i < len(tabs) && col < r.Width; i++ {
		a := theme.attr(StyleTabLine)
		if tabs[i].Current {
			a = theme.attr(StyleTabLineSelected)
		}
		want += widths[i]
		if col = r.drawText(g, 0, col, r.Width, labels[i], a); col < want {
			break // 収まらずに切れた
		}
	}
}

// sumTo は、widths[from] から widths[to] までの合計を返す。
func sumTo(widths []int, from, to int) int {
	w := 0
	for _, x := range widths[from : to+1] {
		w += x
	}
	return w
}

// renderWithTabLine は、タブの行を出すときの Render である。バッファの表示領域からステータス行とコマンドラインの行までを、
// 1 行低い画面として描き、その下に 1 行ずらして置き、空いた一番上の行にタブの行を描く。
func (r *Renderer) renderWithTabLine(f *Frame) Screen {
	r.Height--
	r.tabRows = 0
	body := r.render(f)
	r.Height++
	r.tabRows = 1
	theme := r.Theme
	if theme == nil {
		theme = DefaultTheme()
	}
	g := NewGrid(r.Width, r.Height)
	for i := range body.Grid.Height() {
		g.SetRow(i+1, body.Grid.Row(i))
	}
	r.drawTabLine(g, theme, f.Tabs)
	cur := body.Cursor
	cur.Row++
	return Screen{Grid: g, Cursor: cur}
}
