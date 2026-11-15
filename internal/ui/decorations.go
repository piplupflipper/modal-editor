package ui

import (
	"cmp"
	"math"
	"slices"
)

// Decorations は、構文の色の範囲の一覧を、行で引けるように分けたものである。
//
// decoration/set はバッファ全体の一覧を送る（基本設計 6.1）。描画のたびに全体をなめないように、
// internal/editor は一覧を受け取ったときに NewDecorations で 1 回だけ作り、描画ではそれを Frame に渡す。
// 作った後は変更しないので、同じ値を何度の描画に渡してもよい。
type Decorations struct {
	lines [][]decoSpan // 行ごとの範囲。開始のバイトの順に並べる
}

// decoSpan は、1 行の中の構文の色の範囲 [start, end) である。end が math.MaxInt なら行末までを表す。
type decoSpan struct {
	start, end int
	order      int // 元の一覧での位置。重なったときは大きい方を上に重ねる
	style      string
}

// NewDecorations は、hs を行ごとに分けた Decorations を返す。
//
// 行をまたぐ範囲は、またいだ各行に分ける（途中の行は行の全体。行末の 1 セルは含めない）。
// End が Start より前の範囲は、2 つを入れ替えて扱う。空の範囲は捨てる。
// 位置は描画する時点のテキストを指す必要がある。行の長さを超えるバイト位置は、描画のときに行末に詰める。
//
// req: FR-35
func NewDecorations(hs []Highlight) *Decorations {
	d := &Decorations{}
	for i, h := range hs {
		s, e := h.Range.Start, h.Range.End
		if e.Less(s) {
			s, e = e, s
		}
		if s == e || e.Line < 0 {
			continue
		}
		for n := max(s.Line, 0); n <= e.Line; n++ {
			sp := decoSpan{start: 0, end: math.MaxInt, order: i, style: h.Style}
			if n == s.Line {
				sp.start = max(s.Byte, 0)
			}
			if n == e.Line {
				sp.end = e.Byte
			}
			if sp.start >= sp.end {
				continue
			}
			for len(d.lines) <= n {
				d.lines = append(d.lines, nil)
			}
			d.lines[n] = append(d.lines[n], sp)
		}
	}
	for _, l := range d.lines {
		slices.SortStableFunc(l, func(a, b decoSpan) int { return cmp.Compare(a.start, b.start) })
	}
	return d
}

// line は、n 行目の範囲を、開始のバイトの順に返す。d が nil でもよい。
func (d *Decorations) line(n int) []decoSpan {
	if d == nil || n < 0 || n >= len(d.lines) {
		return nil
	}
	return d.lines[n]
}
