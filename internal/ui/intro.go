package ui

// Intro は、バッファの表示領域の中央に重ねて出す起動の案内である（Frame.Intro。Vim と Neovim の :intro の画面。FR-205、FR-207）。
// 文面は internal/editor が決める。ui は行を並べて描くだけで、文面の意味（版、設定ファイルの場所）を知らない。
type Intro struct {
	// Lines は、案内の行である。上から順に 1 行ずつ描く。Text が空の行は何も描かずに 1 行を空ける。
	Lines []IntroLine
	// Clear は、表示領域を空白で塗ってから描くことを表す（:intro。FR-207）。
	// false なら、案内の全体が表示領域に余白付きで収まるときだけ描き、収まらなければ何も描かない（drawIntro の doc）。
	Clear bool
}

// IntroLine は、起動の案内の 1 行である（Intro.Lines）。
type IntroLine struct {
	// Text は、行の文字である。
	Text string
	// Elide は、幅に収まらなければ、左を省略の印（introEllipsis）に置き換えて詰めてよい行であることを表す（設定ファイルのパスなど）。
	// 案内の全体が収まるかは、この行を除いた行で決める（drawIntro の doc）。
	Elide bool
}

// introEllipsis は、詰めた行（IntroLine.Elide）の左に付ける省略の印である。
const introEllipsis = "…"

// drawIntro は、起動の案内 in を、バッファの表示領域 area の中央に重ねて描く。in が nil か行が無ければ何も描かない。
// 重ねる順は layers.go の「窓の重なり」の表にある。
//
//   - 行の並びの全体を、表示領域の行の中ほど（上の余りの行数が (表示領域の行数 - 行数) / 2）に置き、
//     各行を、表示領域の幅の中ほど（左の余りの列数が (表示領域の幅 - その行の表示幅) / 2）に置く（Neovim 0.12.5 の intro_message と同じく、行ごとに中央へ寄せる）。
//     列をそろえたい行は、文面の側で空白を足して表示幅をそろえる
//   - 文字は既定の属性（色を付けない）で描き、文字の無いセルは下に描いたもの（"~" の行）のまま残す
//   - in.Clear が false なら、上と下に 1 行以上、左と右に 1 列以上の余りが残るときだけ描く。幅は、詰めてよい行（IntroLine.Elide）を除いた行で決める。
//     起動の案内を出すのは名前の無い空のバッファだけなので、上の 1 行はバッファの最初の行（カーソルの行）、左の 1 列は "~" の列である。
//     どちらも覆わないので、カーソルの行と "~" の行は狭い端末でも見える。収まらないときに一部の行だけ描くことはしない
//     （Neovim 0.12.5 も、上の余りが 2 行より少ないか、画面が 50 列より狭いと描かない）
//   - 詰めてよい行が左と右に 1 列の余りを残して収まらなければ、左を省略の印にして、余りを残して収まる長さまで詰める（elideLeft）
//   - in.Clear が true なら、表示領域を空白で塗ってから描き、収まらない行は切る（詰めてよい行は左を詰め、ほかの行は右を切る）。
//     Neovim 0.12.5 の :intro が、画面を消してから大きさによらず出すのと同じである
//
// req: FR-205, FR-207
func (r *Renderer) drawIntro(g *Grid, in *Intro, area textArea) {
	if in == nil || len(in.Lines) == 0 || area.rows <= 0 || area.width <= 0 {
		return
	}
	tabStop := tabStopOr(r.tabStop)
	maxW := 0
	for _, l := range in.Lines {
		if !l.Elide {
			maxW = max(maxW, StringWidth(l.Text, tabStop))
		}
	}
	n := len(in.Lines)
	room := area.width - 2 // 左と右に 1 列を残した幅
	if in.Clear {
		for row := area.top; row < area.top+area.rows; row++ {
			for x := range area.width {
				g.set(row, x, blank(Attr{}))
			}
		}
		room = max(room, 1)
	} else if area.rows < n+2 || room < maxW {
		return
	}
	top := area.top + max((area.rows-n)/2, 0)
	for i, l := range in.Lines {
		row := top + i
		if row >= area.top+area.rows {
			break
		}
		s := l.Text
		if s == "" {
			continue
		}
		if l.Elide {
			s = elideLeft(s, room, tabStop)
		}
		w := StringWidth(s, tabStop)
		col := max((area.width-w)/2, 0)
		end := min(col+w, area.width)
		clearSplit(g, row, col, end)
		r.drawText(g, row, col, area.width, s, Attr{})
	}
}

// elideLeft は、s の表示幅が w を超えれば、左を省略の印（introEllipsis）に置き換えて、表示幅が w に収まる長さまで詰めた形を返す。
// 詰めるのは文字の境目で、残すのは s の末尾の側である（パスのファイル名の側を残す）。w が印の幅より狭ければ、印を w で切った形を返す。
func elideLeft(s string, w, tabStop int) string {
	if StringWidth(s, tabStop) <= w {
		return s
	}
	mark := StringWidth(introEllipsis, tabStop)
	for off := 0; off < len(s); {
		gl := nextGlyph(s, off, 0, tabStop)
		off += gl.size
		if rest := s[off:]; StringWidth(rest, tabStop)+mark <= w {
			return introEllipsis + rest
		}
	}
	if mark <= w {
		return introEllipsis
	}
	return ""
}
