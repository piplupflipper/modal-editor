package diff

import udiff "github.com/aymanbagabas/go-udiff"

// Inline は、変えた行どうし a と b の中で、違う部分のバイトの範囲を返す（行の中の差分の色付けに使う）。
// 1 つ目の戻り値は a の中の範囲、2 つ目は b の中の範囲で、どちらも位置の順に並び、同じ番号の範囲どうしが対応する。
// 片側だけにある文字の範囲は、もう片側では長さ 0 の範囲になる。
// 範囲の境界は UTF-8 の文字の境界にある（go-udiff の Strings が文字の単位で比べるため）。
//
// 文字の単位の Myers のアルゴリズムで比べるので、Vim の 'diffopt' の inline:char に近い。
// 単語の単位にはそろえない。
// 違う文字が多いと、Compute と同じく探索の上限で打ち切り、最小とは限らない範囲を返す。
func Inline(a, b string) (sa, sb []Span) {
	edits := udiff.Strings(a, b)
	if len(edits) == 0 {
		return nil, nil
	}
	sa = make([]Span, len(edits))
	sb = make([]Span, len(edits))
	// delta は、それまでの置き換えで b の位置が a の位置からずれた量である。
	delta := 0
	for i, e := range edits {
		sa[i] = Span{e.Start, e.End}
		start := e.Start + delta
		sb[i] = Span{start, start + len(e.New)}
		delta += len(e.New) - (e.End - e.Start)
	}
	return sa, sb
}
