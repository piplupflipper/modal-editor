package diff

import "sort"

// LineKind は、片側の画面に出す行の、差分の色の種類である（基本設計 6.8 の diff.add と diff.change）。
type LineKind int

const (
	// LineChanged は、両側に行がある塊の行で、内容が違う行である（Vim の DiffChange）。
	LineChanged LineKind = iota + 1
	// LineOnlyHere は、この側にだけある行である（Vim の DiffAdd）。もう片側には、同じ位置に詰め物の行を描く。
	LineOnlyHere
)

// Mark は、片側の 1 行の差分の色である。
type Mark struct {
	// Line は、0 から数えた行の番号である。
	Line int
	Kind LineKind
	// Other は、Kind が LineChanged のとき、行の中の差分（Inline）を比べる、もう片側の行の番号である。
	// 塊の中の同じ順番の行どうしを比べる（Vim の DiffText と同じ組み方）。比べる行が無ければ -1 である。
	Other int
}

// Layout は、片側の画面の行の並び（差分の色と、詰め物の行の位置）である（基本設計 6.8。FR-99、FR-100）。
//
// 画面の行は、テキストの各行の前に、その行の上に描く詰め物の行（Vim の diff の filler）を並べたものである。
// 最後の行の下の詰め物は、行の番号 Lines（画面に出す行の数）の位置に置く。
// 0 行のテキスト（空のファイル）も、画面には空の行 1 つが出るので、Lines は 1 である。
//
// 両側の Layout は、画面の行の数（Rows）が等しく、同じ番号の画面の行どうしが対応する。
// 塊 1 つは、両側の範囲の長いほうの行数を両側で占め、短いほうの側は、塊の行の下（塊の次の行の上）に、長さの差だけ詰め物を描く。
// 0 行のテキストの側は、Hunk の doc の規則で、空の行を変えた行とし、詰め物を 1 行少なくする。
type Layout struct {
	// Lines は、この側の画面に出す行の数（テキストの行の数。0 行なら 1）である。
	Lines int
	// Marks は、色を付ける行を、行の番号の順に並べたものである。
	Marks []Mark
	// fillAt と fillCum は、詰め物を描く行の番号（昇順）と、その行までの詰め物の数の累計（その行の分を含む）である。
	fillAt  []int
	fillCum []int
}

// Layout は、side の側の画面の行の並びを返す。
// 計算量は塊の数と、塊の中の行の数に比例する。
func (r *Result) Layout(side Side) Layout {
	n := r.lineCount(side)
	l := Layout{Lines: max(n, 1)}
	addFill := func(line, k int) {
		if k <= 0 {
			return
		}
		if m := len(l.fillAt); m > 0 && l.fillAt[m-1] == line {
			l.fillCum[m-1] += k
			return
		}
		prev := 0
		if m := len(l.fillCum); m > 0 {
			prev = l.fillCum[m-1]
		}
		l.fillAt = append(l.fillAt, line)
		l.fillCum = append(l.fillCum, prev+k)
	}
	other := side.Other()
	for _, h := range r.Hunks {
		s, o := h.Span(side), h.Span(other)
		for i := range s.Len() {
			m := Mark{Line: s.Start + i, Kind: LineOnlyHere, Other: -1}
			if i < o.Len() {
				m.Kind, m.Other = LineChanged, o.Start+i
			}
			l.Marks = append(l.Marks, m)
		}
		fill := o.Len() - s.Len()
		if n == 0 {
			// 空のファイルの側は、画面に出る空の行を変えた行にし、詰め物を 1 行少なくする（Hunk の doc）。
			l.Marks = append(l.Marks, Mark{Line: 0, Kind: LineChanged, Other: -1})
			fill--
		}
		// 塊の後ろにテキストの行が無ければ（末尾の塊と、空のファイル）、最後の行の下に描く。
		pos := s.End
		if pos >= n {
			pos = l.Lines
		}
		addFill(pos, fill)
	}
	return l
}

// Fill は、行 line の上に描く詰め物の行の数を返す。line が Lines なら、最後の行の下の詰め物の数である。
func (l Layout) Fill(line int) int {
	i := sort.SearchInts(l.fillAt, line)
	if i == len(l.fillAt) || l.fillAt[i] != line {
		return 0
	}
	if i == 0 {
		return l.fillCum[0]
	}
	return l.fillCum[i] - l.fillCum[i-1]
}

// Fills は、詰め物を描く行の番号から、その行の上に描く詰め物の行の数への対応を返す（Fill の全部）。
func (l Layout) Fills() map[int]int {
	out := make(map[int]int, len(l.fillAt))
	prev := 0
	for i, line := range l.fillAt {
		out[line] = l.fillCum[i] - prev
		prev = l.fillCum[i]
	}
	return out
}

// fillThrough は、行 line まで（line の上の分を含む）の詰め物の数の合計を返す。
func (l Layout) fillThrough(line int) int {
	i := sort.Search(len(l.fillAt), func(i int) bool { return l.fillAt[i] > line })
	if i == 0 {
		return 0
	}
	return l.fillCum[i-1]
}

// Row は、行 line が出る画面の行の番号（最初の画面の行を 0 とする）を返す。line の上の詰め物の行は、この番号の直前に並ぶ。
func (l Layout) Row(line int) int {
	return line + l.fillThrough(line)
}

// Rows は、画面の行の数（行と、すべての詰め物の行）を返す。
func (l Layout) Rows() int {
	return l.Lines + l.fillThrough(l.Lines)
}

// LineAtRow は、画面の行 row が、どの行の上の詰め物か、その行そのものかを返す。
// line は、row 以降で最初に出る行で、fill は、row から line の直前までの詰め物の行の数（row が line そのものなら 0）である。
// 表示の最初の画面の行を row にするスクロールの位置（行 line の上の詰め物を下から fill 行だけ見せる）を、両側でそろえるのに使う（FR-100）。
// row が最後の行より後ろ（最後の行の下の詰め物か、画面の行の外）なら、最後の行と 0 を返す。負なら 0 行目と、その上の詰め物の全部を返す。
func (l Layout) LineAtRow(row int) (line, fill int) {
	if row < 0 {
		return 0, l.Fill(0)
	}
	// Row は line について狭義に増えるので、Row(line) >= row となる最初の行を二分探索で求める。
	line = sort.Search(l.Lines, func(i int) bool { return l.Row(i) >= row })
	if line >= l.Lines {
		return l.Lines - 1, 0
	}
	return line, l.Row(line) - row
}
