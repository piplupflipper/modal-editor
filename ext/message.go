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
//
// req: FR-16
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
//
// FromVersion が拡張機能の持つバージョン番号と一致しない場合、拡張機能は差分が欠けたと判断する（基本設計 5.3）。
//
// req: FR-16
type BufferChangeParams struct {
	Buffer BufferID `json:"buffer"`
	// FromVersion は、Edits を適用する前のバージョン番号である。
	FromVersion Version `json:"fromVersion"`
	// ToVersion は、Edits を適用した後のバージョン番号である。
	ToVersion Version `json:"toVersion"`
	// Edits は、適用した編集の列である。各範囲は、直前の編集を適用した後の位置で書く。
	Edits []Edit `json:"edits"`
}

// BufferCloseParams は、buffer/close の params である。
type BufferCloseParams struct {
	Buffer BufferID `json:"buffer"`
}

// MotionResolveParams は、motion/resolve の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
//
// req: FR-11, FR-12
type MotionResolveParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.motions に書いたモーションの ID である。
	ID string `json:"id"`
	// Cursor は、問い合わせた時点のカーソル位置である。
	Cursor Position `json:"cursor"`
	// Count は、回数である。利用者が省略した場合は 1。
	Count int `json:"count"`
}

// MotionResolveResult は、motion/resolve の result である。
type MotionResolveResult struct {
	// Version は、計算のもとにしたバージョン番号である。
	// 本体は、現在のバージョン番号と一致しなければ結果を破棄する。
	Version Version `json:"version"`
	// Position は、移動先の位置である。
	Position Position `json:"position"`
}

// TextObjectResolveParams は、textObject/resolve の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
//
// req: FR-11, FR-12
type TextObjectResolveParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.textObjects に書いたテキストオブジェクトの ID である。
	ID string `json:"id"`
	// Cursor は、問い合わせた時点のカーソル位置である。
	Cursor Position `json:"cursor"`
	// Count は、回数である。利用者が省略した場合は 1。
	Count int `json:"count"`
}

// TextObjectResolveResult は、textObject/resolve の result である。
type TextObjectResolveResult struct {
	// Version は、計算のもとにしたバージョン番号である。
	// 本体は、現在のバージョン番号と一致しなければ結果を破棄する。
	Version Version `json:"version"`
	// Range は、テキストオブジェクトの範囲である。
	Range Range `json:"range"`
}

// OperatorApplyParams は、operator/apply の params である。
// キー入力ごとに結果が要る問い合わせで、本体は期限付きで応答を待つ（基本設計 5.4）。
//
// req: FR-11, FR-12
type OperatorApplyParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// ID は、宣言ファイルの contributes.operators に書いたオペレーターの ID である。
	ID string `json:"id"`
	// Range は、オペレーターを適用する範囲である。
	Range Range `json:"range"`
	// Count は、回数である。利用者が省略した場合は 1。
	Count int `json:"count"`
}

// OperatorApplyResult は、operator/apply の result である。
//
// req: FR-15
type OperatorApplyResult struct {
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
//
// req: FR-17
type CancelParams struct {
	// ID は、取り消すリクエストの JSON-RPC の id である。
	// id は数値と文字列のどちらでもよいので、受け取った形のまま持つ。
	ID json.RawMessage `json:"id"`
}

// BufferEditParams は、buffer/edit の params である。
// 補完の確定や整形など、拡張機能の側から始める編集に使う。
//
// req: FR-15
type BufferEditParams struct {
	Buffer      BufferID    `json:"buffer"`
	Transaction Transaction `json:"transaction"`
}

// BufferEditResult は、buffer/edit の result である。
//
// req: FR-15
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
// req: FR-35
type DecorationSetParams struct {
	Buffer BufferID `json:"buffer"`
	// Version は、一覧を計算するもとにしたバージョン番号である。
	Version Version `json:"version"`
	// Decorations は、バッファ全体の色を付ける範囲の一覧である。前に送った一覧を置き換える。
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
