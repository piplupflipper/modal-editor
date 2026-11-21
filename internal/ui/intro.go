package ui

// Intro は、バッファの表示領域の中央に重ねて出す起動の案内である（Frame.Intro。Vim と Neovim の :intro の画面。FR-205、FR-207）。
// 文面は internal/editor が決める。ui は行を並べて描くだけで、文面の意味（版、設定ファイルの場所）を知らない。
type Intro struct {
	// Lines は、案内の行である。上から順に 1 行ずつ描く。空の行は何も描かずに 1 行を空ける。
	Lines []string
	// Clear は、表示領域を空白で塗ってから描くことを表す（:intro。FR-207）。
	// false なら、案内の全体が表示領域に余白付きで収まるときだけ描き、収まらなければ何も描かない（drawIntro の doc）。
	Clear bool
}

// drawIntro は、起動の案内 in を、バッファの表示領域 area の中央に重ねて描く。in が nil か行が無ければ何も描かない。
// 重ねる順は layers.go の「窓の重なり」の表にある。
//
//   - 行の並びの全体を、表示領域の行の中ほど（上の余りの行数が (表示領域の行数 - 行数) / 2）に置き、
//     各行を、表示領域の幅の中ほど（左の余りの列数が (表示領域の幅 - その行の表示幅) / 2）に置く（Neovim 0.12.5 の intro_message と同じく、行ごとに中央へ寄せる）。
//     列をそろえたい行は、文面の側で空白を足して表示幅をそろえる
//   - 文字は既定の属性（色を付けない）で描き、文字の無いセルは下に描いたもの（"~" の行）のまま残す
//   - in.Clear が false なら、上と下に 1 行以上、左と右に 1 列以上の余りが残るときだけ描く。
//     起動の案内を出すのは名前の無い空のバッファだけなので、上の 1 行はバッファの最初の行（カーソルの行）、左の 1 列は "~" の列である。
//     どちらも覆わないので、カーソルの行と "~" の行は狭い端末でも見える。収まらないときに一部だけ描くことはしない
//     （Neovim 0.12.5 も、上の余りが 2 行より少ないか、画面が 50 列より狭いと描かない）
//   - in.Clear が true なら、表示領域を空白で塗ってから描き、収まらない行と列は切る（Neovim 0.12.5 の :intro が、
//     画面を消してから大きさによらず出すのと同じ）
//
// req: FR-205, FR-207
func (r *Renderer) drawIntro(g *Grid, in *Intro, area textArea) {
	if in == nil || len(in.Lines) == 0 || area.rows <= 0 || area.width <= 0 {
		return
	}
	tabStop := tabStopOr(r.tabStop)
	widths := make([]int, len(in.Lines))
	maxW := 0
	for i, l := range in.Lines {
		widths[i] = StringWidth(l, tabStop)
		maxW = max(maxW, widths[i])
	}
	n := len(in.Lines)
	if in.Clear {
		for row := area.top; row < area.top+area.rows; row++ {
			for x := range area.width {
				g.set(row, x, blank(Attr{}))
			}
		}
	} else if area.rows < n+2 || area.width < maxW+2 {
		return
	}
	top := area.top + max((area.rows-n)/2, 0)
	for i, l := range in.Lines {
		row := top + i
		if row >= area.top+area.rows {
			break
		}
		if l == "" {
			continue
		}
		col := max((area.width-widths[i])/2, 0)
		end := min(col+widths[i], area.width)
		clearSplit(g, row, col, end)
		r.drawText(g, row, col, area.width, l, Attr{})
	}
}
