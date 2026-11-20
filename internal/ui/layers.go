package ui

// バッファの行の文字に重ねる色の順（span.order）。大きい方を上に重ねる（sweep.attr）。
// 色の種類を足すときは、この表に行を足し、上下の種類との順をここで決める。
//
//	順                          色の種類                               入れる所
//	diffLineOrder（-2）         見比べの行の色（diff.add など）          drawer.diffSpans
//	diffTextOrder（-1）         見比べの違う文字の色（diff.text）         drawer.diffSpans
//	0 以上 diagnosticOrder 未満  構文の色（decoration/set の一覧での位置）  drawer.prepare（Decorations）
//	diagnosticOrder + i         診断（i は Frame.Diagnostics での位置）    drawer.collectSpans
//	searchOrder                 検索の一致（search。FR-137）             drawer.searchSpans
//	（span の外）               選択（visual）                          drawer.attrAt が span の後に重ねる
//
// 見比べの色は構文の色より下に置き、構文の文字の色が差分の背景の色の上に出るようにする。
// 一致の色は診断の上、選択の下に置く（#275 の本文）。選択だけは span にせず、カーソルの文字を除いて attrAt で最後に重ねる。
// カーソルの文字には、選択の色を重ねない（端末のカーソルが反転で描かれても文字が読めるように。Vim と同じ）。
const (
	diffLineOrder   = -2
	diffTextOrder   = -1
	diagnosticOrder = 1 << 40
	searchOrder     = 1 << 41
)
