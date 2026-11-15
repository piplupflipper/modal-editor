package ext

// BufferID は、本体が開いているバッファを識別する番号である。
// 本体がバッファを開くときに割り当て、閉じるまで変えない。
// 閉じたバッファの番号を、別のバッファに使い回すかどうかは保証しない。
type BufferID uint64

// Version は、バッファのバージョン番号である。
// バッファが変更されるたびに 1 増える（基本設計 4.2）。
type Version uint64

// Position は、バッファの中の位置である（基本設計 4.1）。
// 行の番号と、行の先頭からのバイト数の組で表し、どちらも 0 から数える。
// テキストは UTF-8 で数え、LSP の UTF-16 の単位には変換しない。
type Position struct {
	// Line は、0 から数えた行の番号である。
	Line int `json:"line"`
	// Byte は、行の先頭から数えたバイト数である。
	Byte int `json:"byte"`
}

// Range は、バッファの中の範囲である。
// Start を含み、End を含まない。
// Start と End が同じ位置なら、空の範囲（挿入の位置）を表す。
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Edit は、1 つの範囲の置き換えである（基本設計 4.2）。
// 挿入は空の Range に Text を置く場合、削除は Text を空にする場合として表す。
type Edit struct {
	// Range は、置き換える範囲である。
	Range Range `json:"range"`
	// Text は、置き換えた後のテキストである。
	Text string `json:"text"`
}

// Transaction は、1 回の操作でまとめて適用する編集の列と、元にしたバージョン番号である（基本設計 4.2）。
//
// 拡張機能 API の契約として、次のとおりに扱う。
//
//   - 本体は、BaseVersion が現在のバージョン番号と一致した場合だけ Edits を適用し、一致しなければ全体を破棄する。
//     本体が Edits を新しい内容に合わせて書き換えることはしない
//   - Edits は先頭から順に適用する。各編集の範囲は、同じ列の中で直前の編集までを適用した後のテキストの位置で数える
//     （逐次の数え方。LSP の textDocument/didChange の contentChanges と同じ）。
//     先頭の編集は BaseVersion の時点のテキストの位置で数える。
//     このため、範囲の重なりという状態は生じない
//   - いずれかの編集の範囲がその時点のテキストの外を指すか、UTF-8 の文字の途中を指すか、終わりが始まりより前にある場合、
//     本体はトランザクション全体を破棄する。一部の編集だけを適用することは無い
//   - Edits が 1 つ以上なら、適用に成功するとその数に関わらずバージョン番号は 1 増える。
//     Edits が空なら、テキストを何も変えず、バージョン番号も増えない（buffer/edit では result の Applied が false になる）
//
// buffer/change の Edits も同じ数え方で書く（BufferChangeParams）。
// このため、本体は適用したトランザクションの Edits を、そのまま他の拡張機能へ送れる。
type Transaction struct {
	// BaseVersion は、編集を計算するもとにしたバッファのバージョン番号である。
	BaseVersion Version `json:"baseVersion"`
	// Edits は、適用する編集の列である。位置は逐次の数え方で書く。
	Edits []Edit `json:"edits"`
}
