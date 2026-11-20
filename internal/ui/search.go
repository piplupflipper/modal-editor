package ui

// searchOrder は、検索の一致の範囲を重ねる順である。構文の色、見比べの色、診断の上に重ねる。
// 選択の色は sweep で重ねた後に重ねる（attrAt）ので、一致の色より上になる（FR-137。#275 の本文）。
const searchOrder = 1 << 41

// searchSpans は、バッファの 1 行のテキスト s のうち、Frame.Search に一致する範囲を、一致の色の範囲として dst に足して返す（FR-137）。
//
//   - 一致は、行の先頭から重ならないように並べたもの（regexp の FindAllStringIndex）である。1 行に複数の一致があれば、どれにも付ける
//   - パターンは行ごとに当てるので、行をまたぐ一致は無い（検索の n と同じ。target の search）
//   - 空の一致（^、$、x* など）は、その位置の 1 文字に付ける。行末の位置なら行末の 1 セルに付ける（Vim 9.1 で :set hlsearch の後の
//     /^ と /$ の画面を 2026-11-20 に tmux で確かめた）。Go の regexp は、直前の一致の終わりに接する空の一致を返さないので、
//     axxb の /x* は a、xx、行末のセルに付き、b には付かない（Vim は b にも付ける）
//
// 呼ぶのは表示領域に描く行（drawLine）だけなので、時間は見えている行の長さの和に比例し、バッファの全体は走査しない。
//
// req: FR-137
func (d *drawer) searchSpans(dst []span, s string) []span {
	re := d.f.Search
	if re == nil {
		return dst
	}
	a := d.theme.attr(StyleSearch)
	if a == (Attr{}) {
		return dst
	}
	for _, m := range re.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		if start == end {
			// span の end が行の長さより大きければ、行末のセルを含む（span の doc）。文字の途中を指すことは無い（regexp は文字の境界で一致する）。
			end = start + 1
		}
		dst = append(dst, span{start: start, end: end, order: searchOrder, attr: a})
	}
	return dst
}
