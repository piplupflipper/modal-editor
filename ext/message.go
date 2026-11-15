package ext

import "encoding/json"

// メソッドの名前（基本設計 5.3）。名前は仮である。
//
// 「リクエスト」は応答を返し、「通知」は応答を返さない（JSON-RPC 2.0）。
const (
	// MethodInitialize は、本体から拡張機能へのリクエストである。
	// params は InitializeParams、result は InitializeResult。
	// 応答が届いた時点で、拡張機能は稼働中になる。
	MethodInitialize = "initialize"

	// MethodBufferOpen は、本体から拡張機能への通知である。params は BufferOpenParams。
	MethodBufferOpen = "buffer/open"
	// MethodBufferChange は、本体から拡張機能への通知である。params は BufferChangeParams。
	MethodBufferChange = "buffer/change"
	// MethodBufferClose は、本体から拡張機能への通知である。params は BufferCloseParams。
	MethodBufferClose = "buffer/close"

	// MethodMotionResolve は、本体から拡張機能へのリクエストである。
	// params は MotionResolveParams、result は MotionResolveResult。
	MethodMotionResolve = "motion/resolve"
	// MethodTextObjectResolve は、本体から拡張機能へのリクエストである。
	// params は TextObjectResolveParams、result は TextObjectResolveResult。
	MethodTextObjectResolve = "textObject/resolve"
	// MethodOperatorApply は、本体から拡張機能へのリクエストである。
	// params は OperatorApplyParams、result は OperatorApplyResult。
	MethodOperatorApply = "operator/apply"

	// MethodPing は、本体から拡張機能への生存確認のリクエストである。
	// params は PingParams、result は PingResult。
	MethodPing = "ping"
	// MethodCancel は、本体から拡張機能への通知である。params は CancelParams。
	MethodCancel = "$/cancel"

	// MethodBufferEdit は、拡張機能から本体へのリクエストである。
	// params は BufferEditParams、result は BufferEditResult。
	MethodBufferEdit = "buffer/edit"
	// MethodDecorationSet は、拡張機能から本体への通知である。params は DecorationSetParams。
	MethodDecorationSet = "decoration/set"
	// MethodMessageShow は、拡張機能から本体への通知である。params は MessageShowParams。
	MethodMessageShow = "message/show"
	// MethodBufferResync は、拡張機能から本体への通知である。params は BufferResyncParams。
	// 差分が欠けたと判断した拡張機能が、全文の送り直しを求める（基本設計 5.3 の本文）。
	MethodBufferResync = "buffer/resync"
)

// InitializeParams は、initialize の params である。
type InitializeParams struct {
	// APIVersion は、本体が実装している拡張機能 API のバージョンである。
	APIVersion string `json:"apiVersion"`
	// Settings は、利用者の設定ファイルのうち、この拡張機能に向けた部分である。
	// 形は拡張機能ごとに決まるので、本体は解釈せずに JSON のまま渡す。設定が無ければ省く。
	Settings json.RawMessage `json:"settings,omitempty"`
}

// InitializeResult は、initialize の result である。
type InitializeResult struct {
	// APIVersion は、拡張機能が前提にした拡張機能 API のバージョンである。
	// 本体のバージョンと食い違った場合の扱いは未決である。
	APIVersion string `json:"apiVersion"`
}

// BufferOpenParams は、buffer/open の params である。
// 本体は、拡張機能が稼働中になった時点で開いているバッファと、その後に開いたバッファについて送る。
// 再起動した拡張機能にも、開いているバッファの全文を送り直す（基本設計 5.2）。
// buffer/resync を受け取ったときも、そのバッファについて送り直す。
// 拡張機能は、既に持っているバッファについて受け取った場合、持っている内容を Text で置き換える。
type BufferOpenParams struct {
	Buffer BufferID `json:"buffer"`
	// Path は、バッファに対応するファイルのパスである。ファイルに対応しないバッファでは空。
	Path string `json:"path,omitempty"`
	// FileType は、ファイルの種類（"markdown" など）である。決まらなければ空。
	FileType string `json:"fileType,omitempty"`
	// Version は、Text の時点のバージョン番号である。
	Version Version `json:"version"`
	// Text は、バッファの全文である。
	Text string `json:"text"`
}

// BufferChangeParams は、buffer/change の params である。
// 本体は、バッファの変更（トランザクションの適用、アンドゥ、リドゥ）のたびに送る。
//
// Edits の位置は、Transaction と同じ逐次の数え方で書く。
// 先頭の編集は FromVersion の時点のテキストの位置で、以後の編集は直前の編集までを適用した後のテキストの位置で数える。
// 拡張機能は、Edits を先頭から順に適用すると、ToVersion の時点のテキストを得る。
//
// FromVersion が拡張機能の持つバージョン番号と一致しない場合、拡張機能は差分が欠けたと判断し、
// Edits を適用せずに buffer/resync を送る（基本設計 5.3）。
// 一致した場合は、Edits を適用した後に ToVersion を自分の番号にする。
// ToVersion は FromVersion より大きい。拡張機能は差が 1 であることを前提にしない。
type BufferChangeParams struct {
	Buffer BufferID `json:"buffer"`
	// FromVersion は、Edits を適用する前のバージョン番号である。
	FromVersion Version `json:"fromVersion"`
	// ToVersion は、Edits を適用した後のバージョン番号である。
	ToVersion Version `json:"toVersion"`
	// Edits は、適用した編集の列である。位置は逐次の数え方で書く。
	Edits []Edit `json:"edits"`
}

// BufferCloseParams は、buffer/close の params である。
type BufferCloseParams struct {
	Buffer BufferID `json:"buffer"`
}

// MotionResolveParams は、motion/resolve の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
type MotionResolveParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.motions に書いたモーションの ID である。
	ID string `json:"id"`
	// Cursor は、問い合わせた時点のカーソル位置である。
	Cursor Position `json:"cursor"`
	// Count は、回数である。0 は省略を表す。
	Count int `json:"count"`
	// Args は、操作の引数である。無ければ省く。
	Args json.RawMessage `json:"args,omitempty"`
}

// MotionResolveResult は、motion/resolve の result である。
type MotionResolveResult struct {
	// OK は、移動先が見つかったかどうかである。false なら操作は失敗する。
	OK bool `json:"ok"`
	// Version は、計算のもとにしたバージョン番号である。
	// 本体は、現在のバージョン番号と一致しなければ結果を破棄する。
	Version Version `json:"version"`
	// Position は、移動先の位置である。
	Position Position `json:"position"`
	// Linewise は、オペレーターと組み合わせたときに行単位で扱うかどうかである（Vim の j や G）。
	// false（省略を含む）なら文字単位で扱う。
	Linewise bool `json:"linewise,omitempty"`
	// Inclusive は、文字単位で扱うときに、移動先の文字を範囲に含めるかどうかである（Vim の e や f）。
	// false（省略を含む）なら含めない（exclusive）。Linewise が true なら読まない。
	Inclusive bool `json:"inclusive,omitempty"`
}

// TextObjectResolveParams は、textObject/resolve の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
type TextObjectResolveParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.textObjects に書いたテキストオブジェクトの ID である。
	ID string `json:"id"`
	// Cursor は、問い合わせた時点のカーソル位置である。
	Cursor Position `json:"cursor"`
	// Count は、回数である。0 は省略を表す。
	Count int `json:"count"`
	// Args は、操作の引数である。無ければ省く。
	Args json.RawMessage `json:"args,omitempty"`
}

// TextObjectResolveResult は、textObject/resolve の result である。
type TextObjectResolveResult struct {
	// OK は、範囲が見つかったかどうかである。false なら操作は失敗する。
	OK bool `json:"ok"`
	// Version は、計算のもとにしたバージョン番号である。
	// 本体は、現在のバージョン番号と一致しなければ結果を破棄する。
	Version Version `json:"version"`
	// Range は、テキストオブジェクトの範囲である。
	Range Range `json:"range"`
	// Linewise は、範囲を行単位で扱うかどうかである（Vim の ip）。false（省略を含む）なら文字単位で扱う。
	Linewise bool `json:"linewise,omitempty"`
}

// OperatorApplyParams は、operator/apply の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
type OperatorApplyParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.operators に書いたオペレーターの ID である。
	ID string `json:"id"`
	// Range は、オペレーターを適用する範囲である。
	Range Range `json:"range"`
	// Count は、回数である。0 は省略を表す。
	Count int `json:"count"`
	// Args は、操作の引数である。無ければ省く。
	Args json.RawMessage `json:"args,omitempty"`
}

// OperatorApplyResult は、operator/apply の result である。
type OperatorApplyResult struct {
	// OK は、オペレーターが編集を返したかどうかである。false なら操作は失敗し、本体はバッファを変えない。
	OK bool `json:"ok"`
	// Transaction は、オペレーターの編集である。
	// 本体は、BaseVersion が現在のバージョン番号と一致した場合だけ適用する。
	Transaction Transaction `json:"transaction"`
}

// PingParams は、ping の params である。中身は無い。
type PingParams struct{}

// PingResult は、ping の result である。中身は無い。
type PingResult struct{}

// CancelParams は、$/cancel の params である。
// 本体は、期限を超えたリクエストについて送る。
// 拡張機能が取り消しに応じなくても、本体は後から届いた結果を破棄する。
type CancelParams struct {
	// ID は、取り消すリクエストの JSON-RPC の id である。
	// id は数値と文字列のどちらでもよいので、受け取った形のまま持つ。
	ID json.RawMessage `json:"id"`
}

// BufferEditParams は、buffer/edit の params である。
// 補完の確定や整形など、拡張機能の側から始める編集に使う。
type BufferEditParams struct {
	Buffer      BufferID    `json:"buffer"`
	Transaction Transaction `json:"transaction"`
}

// BufferEditResult は、buffer/edit の result である。
type BufferEditResult struct {
	// Applied は、本体がトランザクションを適用したかどうかである。
	// BaseVersion が現在のバージョン番号と一致しなければ false。
	Applied bool `json:"applied"`
	// Version は、応答の時点のバージョン番号である。
	Version Version `json:"version"`
}

// Decoration は、色を付ける 1 つの範囲である。
type Decoration struct {
	Range Range `json:"range"`
	// Style は、色の種類の名前（"keyword" など）である。
	// 名前から色への対応は本体が持つ。名前の一覧は未定である。
	Style string `json:"style"`
}

// DecorationSetParams は、decoration/set の params である。
// 本体は、受け取り済みの一覧で描画し、描画のたびに拡張機能へ問い合わせない（基本設計 6.1）。
//
// 一覧は、送った拡張機能とバッファの組ごとに持つ。
// 本体は、同じ拡張機能から同じバッファについて前に受け取った一覧だけを置き換え、
// 他の拡張機能が同じバッファに付けた一覧は変えない。
// 空の一覧を送ると、その拡張機能がそのバッファに付けた色を消す。
type DecorationSetParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、一覧を計算するもとにしたバージョン番号である。
	Version Version `json:"version"`
	// Decorations は、バッファ全体について、この拡張機能が色を付ける範囲の一覧である。
	Decorations []Decoration `json:"decorations"`
}

// MessageLevel は、利用者へのメッセージの重要度である。
type MessageLevel string

// メッセージの重要度。
const (
	MessageInfo    MessageLevel = "info"
	MessageWarning MessageLevel = "warning"
	MessageError   MessageLevel = "error"
)

// MessageShowParams は、message/show の params である。
type MessageShowParams struct {
	Level MessageLevel `json:"level"`
	// Text は、表示するメッセージである。
	Text string `json:"text"`
}

// BufferResyncParams は、buffer/resync の params である。
// buffer/change の FromVersion が自分の番号と一致しなかった拡張機能が送る（FR-16 の差分が欠けた場合の回復）。
// 本体は、そのバッファの現在の全文を buffer/open で送り直す。
// 本体が既に閉じたバッファについて受け取った場合は、何もしない。
type BufferResyncParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、拡張機能が持っているバージョン番号である。本体はログに記録するだけで、送り直しの判断には使わない。
	Version Version `json:"version"`
}
