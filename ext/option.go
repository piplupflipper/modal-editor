package ext

import (
	"encoding/json"
	"errors"
)

// MethodOptionSet は、拡張機能から本体へのリクエストである。params は OptionSetParams、result は OptionSetResult。
// 本体の設定の項目（基本設計 7.3 の設定の表）の値を、拡張機能から変える（#349。FR-190、FR-191）。
//
// 項目の名前は、設定ファイルと :option.set と同じ正規の名前（"indent.width" など）で書く。
// 値は項目の型の JSON の値（真偽は true か false、整数は 10 進の整数、文字列は JSON の文字列）で書き、null はその項目の値を取り下げる。
// 本体は、表に無い名前、範囲の合わない項目（下の Buffer）、ファイルから判定して決める項目（filetype）、型の違う値、範囲の外の値を、
// :option.set と同じ検査で確かめ、1 つでも通らなければどの値も入れずに、code が CodeInvalidParams の誤りで応答する。
//
// # バッファごとの項目
//
// Buffer を書くと、そのバッファの範囲がバッファごとの項目を変える。値はバッファの初めの値の段の最後の段（.editorconfig の後）に入り、
// 利用者が :set と :option.set で変えた値には勝たない（基本設計 7.3 の「バッファごとの項目の初めの値」）。
// 拡張機能ごとに値を分けて持ち、同じ項目に複数の拡張機能が値を入れたら、宣言ファイルを先に読んだ拡張機能の値が勝つ
// （本体の操作の提供者の選び方と同じ順。基本設計 5.5）。後から届いた順にしないのは、拡張機能の起動の速さで値が変わらないようにするためである。
// 値は Options に書いた項目だけを変え（書かなかった項目は前に入れた値のまま）、null で取り下げる。
// ファイルの種類が変わっても、本体は値を捨てない（buffer/open を新しいファイルの種類で送り直すので、拡張機能が送り直す）。
// 送り直した値に、その種類を OptionSetParams.FileType で付けると、本体のファイルの種類ごとの段と同じく :set の値を隠す。
//
// # エディタ全体の項目
//
// Buffer を省く（0）と、範囲がエディタ全体の項目を変える。値は設定ファイルの値に勝ち、利用者が :set と :option.set で変えた値には勝たない。
// 複数の拡張機能の勝ち負けは、バッファごとの項目と同じである。
//
// # 拡張機能が止まったとき
//
// 拡張機能が異常で終わった、無効になった、:ext sync で止めたときは、本体はその拡張機能が入れた値をすべて取り下げる
// （他の表示のデータと同じ。基本設計 6.7）。起動し直した拡張機能は、buffer/open を受けて値を送り直す。
//
// 古い本体（0.14.0 まで）は method not found で応答する。
const MethodOptionSet = "option/set"

// MethodOptionGet は、拡張機能から本体へのリクエストである。params は OptionGetParams、result は OptionGetResult。
// 本体の設定の項目の今の値（利用者が :set で変えた値、拡張機能が入れた値、初めの値の段を重ねた、本体が使う値）を読む（#349。FR-192）。
//
// 古い本体（0.14.0 まで）は method not found で応答する。
const MethodOptionGet = "option/get"

// OptionSetParams は、option/set の params である。
type OptionSetParams struct {
	// Buffer は、範囲がバッファごとの項目を変えるバッファである。省く（0）と、範囲がエディタ全体の項目を変える。
	// Buffer を書いたときにエディタ全体の項目を、省いたときにバッファごとの項目を書くと、誤りで断る。
	Buffer BufferID `json:"buffer,omitempty"`
	// Options は、項目の正規の名前から値への対応である。値は項目の型の JSON の値か、取り下げる null である。空にできない。
	Options map[string]json.RawMessage `json:"options"`
	// FileType は、バッファごとの項目の値を、どのファイルの種類のバッファ（buffer/open の fileType）として決めたかである。省ける。
	// バッファのファイルの種類が変わった（:set filetype=、種類の決まる名前での保存）後に、この拡張機能がそのバッファへ送る最初の
	// option/set で、FileType が今のファイルの種類と同じなら、本体は Options に値を書いた項目の :set の値を隠し、この値を使う
	// （本体のファイルの種類ごとの段と同じ。Neovim の ftplugin が :set の値を上書きするのと同じ考え方。PR #352 のレビューの推奨 9）。
	// ファイルの種類の変更と関係なく送った値（設定ファイルが変わったなど）と、FileType を省いた値は、:set の値を隠さない。
	FileType string `json:"fileType,omitempty"`
}

// Validate は、option/set の params の形を確かめる。項目の名前と値は、本体が設定の表で確かめる。
func (p OptionSetParams) Validate() error {
	if len(p.Options) == 0 {
		return errors.New("option/set: empty options")
	}
	return nil
}

// OptionSetResult は、option/set の result である。中身は無いが、後から項目を足せるように構造体にしている。
type OptionSetResult struct{}

// OptionUnset は、OptionSetParams.Options の値に書いて、その項目の値を取り下げる JSON の null である。
var OptionUnset = json.RawMessage("null")

// OptionGetParams は、option/get の params である。
type OptionGetParams struct {
	// Buffer は、範囲がバッファごとの項目を読むバッファである。省く（0）と、エディタ全体の項目だけを読める。
	Buffer BufferID `json:"buffer,omitempty"`
	// Names は、読む項目の正規の名前である。空なら、Buffer を書いたときはすべての項目を、省いたときはエディタ全体のすべての項目を読む。
	// 表に無い名前と、Buffer を省いたときのバッファごとの項目は、誤りで断る。
	Names []string `json:"names,omitempty"`
}

// OptionGetResult は、option/get の result である。
type OptionGetResult struct {
	// Values は、項目の正規の名前から今の値（項目の型の JSON の値）への対応である。
	Values map[string]json.RawMessage `json:"values"`
}
