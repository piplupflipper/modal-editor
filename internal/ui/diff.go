package ui

// 差分の色と詰め物の行（基本設計 6.8。FR-99、FR-100）。
//
// 見比べている間は、Frame.Diff に、行ごとの差分の色と、行の上に描く詰め物の行の数を詰める。
// 詰め物の行は Vim の diff の filler と同じく、テキストの行の間に "-" で埋めた行として描き、カーソルは置かない。
// 表示の最初の行（Renderer.Top）の上の詰め物は、下から topFill 行だけ見せる（Vim の w_topfill）。
// 両側のスクロールをそろえる（FR-100）のは internal/editor で、DiffTop と SetDiffTop で最初の行と詰め物の数を読み書きする。

// 差分の色の名前（基本設計 6.8、7.1。仮）。
const (
	// StyleDiffAdd は、片側にだけある行である（Vim の DiffAdd）。
	StyleDiffAdd = "diff.add"
	// StyleDiffChange は、両側にあり内容が違う行である（Vim の DiffChange）。
	StyleDiffChange = "diff.change"
	// StyleDiffDelete は、もう片側にだけある行の位置に描く詰め物の行である（Vim の DiffDelete）。
	StyleDiffDelete = "diff.delete"
	// StyleDiffText は、変えた行の中の、違う文字の範囲である（Vim の DiffText）。StyleDiffChange の上に重ねる。
	StyleDiffText = "diff.text"
)

// fillerText は、詰め物の行を埋める文字である（Vim の 'fillchars' の diff の既定と同じ）。
const fillerText = "-"

// Diff は、見比べている側の画面の、差分の色と詰め物の行である（Frame.Diff）。
type Diff struct {
	// Lines は、行の番号（0 から数える）から、その行の差分の色への対応である。無い行は色を付けない。
	Lines map[int]DiffLine
	// Fill は、行の番号から、その行の上に描く詰め物の行の数への対応である。
	// 行の数（Text.LineCount）の位置は、最後の行の下に描く詰め物である。
	Fill map[int]int
}

// DiffLine は、1 行の差分の色である。
type DiffLine struct {
	// Style は、行の全体に付ける色の名前（StyleDiffAdd か StyleDiffChange）である。行末から右の端までも同じ色で埋める。
	Style string
	// Text は、行の中の違う部分のバイトの範囲 [Start, End) の一覧で、StyleDiffText で重ねる。
	Text []ByteRange
}

// ByteRange は、1 行の中のバイトの範囲 [Start, End) である。
type ByteRange struct {
	Start, End int
}

// fill は、行 line の上に描く詰め物の行の数を返す。
func (d *Diff) fill(line int) int {
	if d == nil {
		return 0
	}
	return d.Fill[line]
}

// DiffTop は、表示の最初の行と、その上に見せている詰め物の行の数を返す（FR-100 のスクロールのそろえ）。
func (r *Renderer) DiffTop() (line, fill int) { return r.top, r.topFill }

// SetDiffTop は、表示の最初の行を line に、その上に見せる詰め物の行の数を fill にする（FR-100 のスクロールのそろえ）。
// 次の Render は、カーソルが表示領域に収まらなければ、この位置からスクロールする。
func (r *Renderer) SetDiffTop(line, fill int) {
	r.top, r.topFill = max(line, 0), max(fill, 0)
	r.skip = 0
}

// screenRow は、行 line（表示の最初の行より後ろ）の最初の見た目の行を描く、表示領域の中の行の番号を返す。詰め物の行を数に入れる。
// 行を折り返すときは、行の高さ（見た目の行の数）を足す。最初の行の途中から描いていれば、隠した見た目の行を引く。
func (r *Renderer) screenRow(text Text, f *Frame, line int) int {
	if f.Diff == nil && !r.wrap {
		return line - r.top
	}
	row := r.topFill - r.skip
	for n := r.top; n < line; n++ {
		row += r.height(text, n) + f.Diff.fill(n+1)
	}
	return row
}

// fitDiff は、見比べている間のスクロールの位置を、詰め物の行に合わせて直す。scroll の後に呼ぶ。
// prevTop は、この Render で scroll などが動かす前の最初の行である。
//
// 最初の行が変わったか、見比べを始めたら、その上の詰め物は見せない。ただし最初の行が 0 行目なら全部を見せる（Vim でファイルの先頭まで戻ったときと同じ）。
// 詰め物の行のためにカーソルの行が表示領域の下に出たら、出なくなるまで 1 行ずつ下へスクロールする。
func (r *Renderer) fitDiff(f *Frame, text Text, cur cursorPlace, prevTop int) {
	if f.Diff == nil {
		r.topFill, r.diffOn = 0, false
		return
	}
	if r.top != prevTop || !r.diffOn {
		r.diffOn = true
		r.topFill = 0
		if r.top == 0 {
			r.topFill = f.Diff.fill(0)
		}
	}
	r.topFill = min(r.topFill, f.Diff.fill(r.top))
	rows := r.textRows()
	for rows > 0 && r.top < cur.line && r.screenRow(text, f, cur.line)+r.height(text, cur.line)-1 >= rows {
		// 1 画面の行ずつ送る。次の行へ進んだら、その上の詰め物を全部見せた状態から送る。
		// 行を折り返すときは、カーソルの行の最後の見た目の行が入るまで送る（scrollWrapped と同じく、カーソルの行の全体を見せる）。
		if r.topFill > 0 {
			r.topFill--
		} else {
			r.top++
			r.topFill = f.Diff.fill(r.top)
		}
	}
}

// drawFiller は、表示領域の row 行目に詰め物の行を描く。印の列は空ける。
// 行番号の列は、番号を書かずに StyleLineNr の空白で埋める（Vim 9.1 と同じ。PR #272 のレビューの推奨 1）。
func (d *drawer) drawFiller(row int) {
	a := d.theme.attr(StyleDiffDelete)
	sign, ln := d.r.signWidth(), d.theme.attr(StyleLineNr)
	for c := range d.r.textLeft() {
		if c < sign {
			d.g.set(row, c, blank(Attr{}))
		} else {
			d.g.set(row, c, blank(ln))
		}
	}
	for c := d.r.textLeft(); c < d.r.Width; c++ {
		d.g.set(row, c, Cell{Text: fillerText, Width: 1, Attr: a})
	}
}

// diffSpans は、line 行目（長さ ln）の差分の色の範囲を返す。行の全体の色を構文の色より下に、違う文字の範囲をその上に重ねる。
func (d *drawer) diffSpans(line, ln int) []span {
	if d.f.Diff == nil {
		return nil
	}
	dl, ok := d.f.Diff.Lines[line]
	if !ok {
		return nil
	}
	out := []span{{start: 0, end: ln + 1, order: diffLineOrder, attr: d.theme.attr(dl.Style)}}
	text := d.theme.attr(StyleDiffText)
	for _, t := range dl.Text {
		start, end := min(max(t.Start, 0), ln), min(max(t.End, 0), ln)
		if start < end {
			out = append(out, span{start: start, end: end, order: diffTextOrder, attr: text})
		}
	}
	return out
}

// diffFillRest は、差分の色のある line 行目の、行末のセルの右から右の端までを、行の色で埋める（Vim と同じ）。
// from は、表示領域の row 行目の、行末のセルの次のセルの、本文の左の端からの列である。
func (d *drawer) diffFillRest(row, line, from int) {
	if d.f.Diff == nil {
		return
	}
	dl, ok := d.f.Diff.Lines[line]
	if !ok {
		return
	}
	a := d.theme.attr(dl.Style)
	w, x0 := d.r.textWidth(), d.r.textLeft()
	for x := max(from, 0); x < w; x++ {
		d.g.set(row, x0+x, blank(a))
	}
}
