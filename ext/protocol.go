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
// 本体は、BaseVersion が現在のバージョン番号と一致した場合だけ Edits を適用し、一致しなければ全体を破棄する。
// 本体が Edits を新しい内容に合わせて書き換えることはしない。
// Edits の各範囲は、BaseVersion の時点のバッファの位置で書く。
// 編集の列の中で範囲が重なる場合の扱いは、本体の適用の側（internal/buffer）が定める。
//
// req: FR-15
type Transaction struct {
	// BaseVersion は、編集を計算するもとにしたバッファのバージョン番号である。
	BaseVersion Version `json:"baseVersion"`
	// Edits は、適用する編集の列である。
	Edits []Edit `json:"edits"`
}
