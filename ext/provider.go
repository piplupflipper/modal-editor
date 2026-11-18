package ext

import "encoding/json"

// MethodProviderInvoke は、本体から拡張機能へのリクエストである。
// params は ProviderInvokeParams、result は ProviderInvokeResult。
// 本体の操作（Operation）を、宣言ファイルの provides で名乗った拡張機能（提供者）へ問い合わせる（基本設計 5.3、5.5。FR-94）。
//
// 本体は、利用者が操作のキー（<leader>lh など）を打つか、コマンドラインから操作の名前で呼んだ（:rename 新しい名前）ときに、
// カーソルのあるバッファのファイルの種類で提供者を選んで送る（選び方は Manifest.Provides の doc）。
// 応答の扱いは command/execute と同じで、言語サーバーやデバッグアダプターの応答を待つ操作は、
// 受け付けたことだけを ok で返し、結果を後から editor/open、popup/show、buffer/edit などで届けてよい（基本設計 6 章の冒頭）。
const MethodProviderInvoke = "provider/invoke"

// Operation は、本体の操作の名前である（基本設計 5.5。名前は仮）。
// 本体は LSP と DAP のプロトコルを知らず、操作の名前と引数だけを提供者へ渡す。
type Operation string

// 本体の操作（基本設計 5.5 の表）。
//
// 移動の操作（OpDefinition、OpImplementation、OpTypeDefinition）は、Args の peek が true なら、
// 移らずに移動先の周りをポップアップに出す（要件定義 4.11 の「ポップアップで見る」。FR-110 の部品ができるまでは popup/show で出す）。
// OpFormat は、gq{モーション} から呼ばれたときは ProviderInvokeParams.Range に範囲が入る。範囲が無ければバッファの全体を整形する。
// OpRename は、Args の text が新しい名前である（コマンドラインの :rename 新しい名前）。
// OpBreakpointConditional は、Args の text が条件の式である（:breakpoint.conditional i > 3）。
//
// 診断の一覧と移動（<leader>le、<leader>lj など）は、本体が diagnostic/set で受け取った診断から作るので、操作の名前を持たない（基本設計 5.5）。
const (
	OpDefinition       Operation = "definition"
	OpImplementation   Operation = "implementation"
	OpTypeDefinition   Operation = "typeDefinition"
	OpHover            Operation = "hover"
	OpReferences       Operation = "references"
	OpDocumentSymbols  Operation = "documentSymbols"
	OpWorkspaceSymbols Operation = "workspaceSymbols"
	OpFormat           Operation = "format"
	OpRename           Operation = "rename"
	OpCodeAction       Operation = "codeAction"
	// OpCompletion は、名前だけを決めた操作である。補完の取り決めは未決（基本設計 10 章）で、本体はまだ問い合わせない。
	OpCompletion Operation = "completion"

	OpBreakpointToggle      Operation = "breakpoint.toggle"
	OpBreakpointConditional Operation = "breakpoint.conditional"
	OpBreakpointClear       Operation = "breakpoint.clear"
	OpDebugContinue         Operation = "debug.continue"
	OpDebugPause            Operation = "debug.pause"
	OpDebugTerminate        Operation = "debug.terminate"
	OpDebugRestart          Operation = "debug.restart"
	OpDebugRunLast          Operation = "debug.runLast"
	OpDebugStepOver         Operation = "debug.stepOver"
	OpDebugStepIn           Operation = "debug.stepIn"
	OpDebugStepOut          Operation = "debug.stepOut"
	OpDebugToggleUI         Operation = "debug.toggleUI"
)

// Operations は、この版の API の知っている操作の一覧である（基本設計 5.5 の表の順）。
var Operations = []Operation{
	OpDefinition, OpImplementation, OpTypeDefinition, OpHover,
	OpReferences, OpDocumentSymbols, OpWorkspaceSymbols,
	OpFormat, OpRename, OpCodeAction, OpCompletion,
	OpBreakpointToggle, OpBreakpointConditional, OpBreakpointClear,
	OpDebugContinue, OpDebugPause, OpDebugTerminate, OpDebugRestart, OpDebugRunLast,
	OpDebugStepOver, OpDebugStepIn, OpDebugStepOut, OpDebugToggleUI,
}

// Known は、o がこの版の API の知っている操作かどうかを返す。
func (o Operation) Known() bool {
	for _, k := range Operations {
		if o == k {
			return true
		}
	}
	return false
}

// ProviderInvokeParams は、provider/invoke の params である。
// 項目の意味は、Operation と Range を除いて CommandExecuteParams と同じである。
type ProviderInvokeParams struct {
	// Operation は、問い合わせる本体の操作である。提供者が provides で名乗ったものに限る。
	Operation Operation `json:"operation"`
	Buffer    BufferID  `json:"buffer"`
	// Version は、問い合わせた時点のバージョン番号である。
	Version Version `json:"version"`
	// Cursor は、問い合わせた時点のカーソル位置である。
	Cursor Position `json:"cursor"`
	// Range は、範囲を取る操作（gq{モーション} の OpFormat）の範囲である。範囲の無い呼び出しでは省く。
	Range *Range `json:"range,omitempty"`
	// Count は、回数である。0 は省略を表す。
	Count int `json:"count"`
	// Args は、操作の引数である。無ければ省く。形は Operation の定数の doc にある（{"peek":true}、{"text":"..."}）。
	Args json.RawMessage `json:"args,omitempty"`
	// Sync は、CommandExecuteParams.Sync と同じく、本体がマクロかドットリピートを再生していることを表す（FR-19）。
	Sync bool `json:"sync,omitempty"`
	// Withheld は、CommandExecuteParams.Withheld と同じく、本体が Buffer を拡張機能へ送っていないことを表す。
	// デバッグの続行のように、バッファを使わない操作はそのまま行える。
	Withheld bool `json:"withheld,omitempty"`
}

// ProviderInvokeResult は、provider/invoke の result である。形と扱いは command/execute の result と同じである。
type ProviderInvokeResult = CommandExecuteResult

// ProviderArgs は、provider/invoke の Args の形である（仮）。
type ProviderArgs struct {
	// Peek は、移動の操作で、移らずにポップアップで見ることを表す。
	// 今は提供者が移動先の行を popup/show で出す。ポップアップの部品（FR-110）ができたら、#177 の裁定のとおり本体が写しを
	// 編集を禁止したバッファとして出す形へ移り、提供者は場所だけを返すことになる（このとき Peek の意味が変わる。仮）。
	Peek bool `json:"peek,omitempty"`
	// Text は、コマンドラインから呼んだときの、操作の名前の後の引数の文字列である（:rename 新しい名前）。
	Text string `json:"text,omitempty"`
}
