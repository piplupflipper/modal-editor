package ui

// バッファの行の文字に重ねる色の順（span.order）。大きい方を上に重ねる（sweep.attr）。
// 色の種類を足すときは、この表に行を足し、上下の種類との順をここで決める。
//
//	順                          色の種類                                    入れる所
//	diffLineOrder（-2）         見比べの行の色（diff.add など）               drawer.diffSpans
//	diffTextOrder（-1）         見比べの違う文字の色（diff.text）              drawer.diffSpans
//	0 以上 glyphOrder 未満       構文の色（decoration/set の一覧での位置）       drawer.prepare（Decorations）
//	glyphOrder                  文字の表記の色（specialkey、whitespace、行末の記号の nontext） drawer.look と drawer.eolCell（attrAt の mark）
//	diagnosticOrder + i         診断（i は Frame.Diagnostics での位置）         drawer.collectSpans
//	searchOrder                 検索の一致（search。FR-137）                  drawer.searchSpans
//	highlightOrder + i          範囲の色（i は Frame.Highlights での位置。FR-140、FR-153） drawer.collectSpans
//	（span の外）               選択（visual）                               drawer.attrAt が span の後に重ねる
//
// 見比べの色は構文の色より下に置き、構文の文字の色が差分の背景の色の上に出るようにする。
// 文字の表記の色（^A などの表記の specialkey、空白の記号の whitespace、行末の記号の nontext。FR-170）は構文の色の上、診断の下に置く。
// Vim 9.1 と Neovim 0.12.5 は、コメントと文字列の中の空白の記号と ^A を、構文の色でなく SpecialKey と Whitespace の色で描く
// （2026-11-21 に、Go のファイルの行末の空白、生の文字列とブロックコメントの中の行頭の空白とタブ文字、C のコメントの中の ^A を
// tmux で確かめた。PR #334 のレビューの必須 1）。表記の色は文字ごとに決まるので span にせず、attrAt が mark として受け取り、この順に重ねる。
// 一致の色は診断の上、選択の下に置く（#275 の本文）。
// 範囲の色（ヤンクした範囲の一時的な色）は一致の色の上、選択の下に置く（#283 の本文）。ヤンクした直後に、一致の色の中でもヤンクした範囲が見えるようにする。
// 打つたびの検索の今の一致（#296）は、検索のためだけの順を作らず、internal/editor が範囲の色の一覧の最後に足す。
// このため一致の色（search）とヤンクした範囲の色の上、選択の下になる。nvim 0.12.5 も、打っている間の今の一致には、ほかの一致の Search の上に IncSearch を出す。
// 選択だけは span にせず、カーソルの文字を除いて attrAt で最後に重ねる。
// カーソルの文字には、選択の色を重ねない（端末のカーソルが反転で描かれても文字が読めるように。Vim と同じ）。
const (
	diffLineOrder   = -2
	diffTextOrder   = -1
	glyphOrder      = diagnosticOrder - 1
	diagnosticOrder = 1 << 40
	searchOrder     = 1 << 41
	highlightOrder  = 1 << 42
)
