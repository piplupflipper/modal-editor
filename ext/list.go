package ext

import "fmt"

// 本体が持つ一覧の問い合わせ（基本設計 5.3 の段階 3 の表、6.9。FR-107）。
//
// 拡張機能は、editor/list（拡張機能 → 本体、リクエスト）で、本体が持つ一覧を 1 つずつ問い合わせる。
// ファジーファインダーの拡張機能（FR-106）が、タブ、コマンド、キーの一覧、レジスターの履歴、ジャンプリスト、ヘルプの語を候補にするために使う。
// 応答は問い合わせた時点の写しで、後から変わっても知らせない。足したのは API 0.9.0 で、ヘルプの語（ListHelp）は API 0.13.0 で足した。
const (
	// MethodEditorList は、拡張機能から本体へのリクエストである。params は EditorListParams、result は EditorListResult。
	MethodEditorList = "editor/list"
	// MethodEditorExecute は、拡張機能から本体へのリクエストである。params は EditorExecuteParams、result は EditorExecuteResult。
	// editor/list の keys と commands の項目を、利用者が打ったのと同じく実行させる（ファインダーで選んだキーとコマンド。PR #249 のレビューの必須 1）。
	// help の項目は、その語の :help を開かせる（API 0.13.0。FR-148）。
	MethodEditorExecute = "editor/execute"
)

// EditorExecuteParams は、editor/execute の params である。Keys、Command、Help のどれか 1 つを指定する。
//
// 呼べる条件（満たさなければ、本体は実行せずに JSON-RPC の誤りで応答し、利用者のメッセージの行にも理由を出す）:
//
//   - その拡張機能が popup/open で開いた一覧、木、ファインダーで、利用者が項目を選んだ（view/select）ことへの応答であること。
//     view/select の Selection と同じ値を Selection に入れる。1 回の選択に 1 回だけで、後の選択があったとき、選んだ client が
//     次のキーを打ったとき、ポップアップを利用者か本体が閉じたとき（拡張機能の view/close を除く）は、その番号は使えない
//   - 選んだ client が、ノーマルモードで入力の途中でないこと（操作の途中、キーマップの展開の途中、hit-enter の問い、フォーカスのある
//     ポップアップ、外部のコマンドや再生中の拡張機能の結果の待ちが無いこと）。ポップアップは、送る前に view/close で閉じておく
//   - Command は、コマンドの一覧（ListCommand）の Name か Short と完全に一致すること。範囲、引数、"|" は付けられない
//
// 本体は、選んだ client で、利用者が打ったのと同じく実行する。応答は実行の前に返す。
// 実行はマクロに記録しない（選んだ <CR> などが記録されている。キーの一覧で選んだキーと同じ扱い。基本設計 7.2）。
// 打ったのと同じなので、ドットリピートの値（.）は実行した操作に替わる。
type EditorExecuteParams struct {
	// Selection は、この実行が応える view/select の Selection である。
	Selection uint64 `json:"selection"`
	// Keys は、キーの一覧の項目の Keys（ListKey.Keys）である。本体はキーの一覧からその表記の行を引き直し、
	// 利用者のキーマップを通して打ったのと同じく実行する（設定の keymap が上書きした行なら、上書きした方が動く）。
	// ノーマルモードの行でないもの（インサートモードだけのキー）と、回数やマクロのように操作の表に無いキーは、理由を出して実行しない。
	Keys string `json:"keys,omitempty"`
	// Command は、コマンドの一覧の項目の Name（ListCommand.Name）である。本体は :Name<CR> を打ったのと同じく実行する。
	// 引数が要るコマンドは、打ったときと同じ誤りになる。
	Command string `json:"command,omitempty"`
	// Help は、ヘルプの語の一覧の項目の Word（ListHelpWord.Word）である。本体は :help Word を打ったのと同じく、
	// その語の説明の行を開いたヘルプを出す（API 0.13.0。FR-148）。説明が見つからない語は、:help と同じ誤り（E149）を出す。
	Help string `json:"help,omitempty"`
}

// Validate は、Selection を指定し、Keys、Command、Help のどれか 1 つだけを指定し、どれも改行を含まないことを検査する。
// 一覧の項目と一致するかと、呼べる条件は本体が確かめる。
func (p *EditorExecuteParams) Validate() error {
	given := 0
	for _, s := range []string{p.Keys, p.Command, p.Help} {
		if s != "" {
			given++
		}
	}
	switch {
	case p.Selection == 0:
		return fmt.Errorf("selection を指定していない（view/select への応答としてだけ使える）")
	case given != 1:
		return fmt.Errorf("keys、command、help のどれか 1 つを指定する")
	case containsNewline(p.Command) || containsNewline(p.Keys) || containsNewline(p.Help):
		return fmt.Errorf("keys、command、help のどれかが改行を含む")
	}
	return nil
}

// EditorExecuteResult は、editor/execute の result である。受け付けたことだけを表す（実行の失敗はメッセージの行に出る）。
type EditorExecuteResult struct{}

// EditorListKind は、editor/list で問い合わせる一覧の種類である。
type EditorListKind string

// editor/list で問い合わせられる一覧（FR-107）。
const (
	// ListTabs は、すべての編集の client のタブである（EditorListResult.Tabs）。
	ListTabs EditorListKind = "tabs"
	// ListCommands は、コマンドラインから呼べるコマンド（本体の Ex コマンドと拡張機能のコマンド）である（EditorListResult.Commands）。
	ListCommands EditorListKind = "commands"
	// ListKeys は、キーの一覧（基本設計 7.2。FR-90 の一覧のすべての行）である（EditorListResult.Keys）。
	ListKeys EditorListKind = "keys"
	// ListRegisters は、レジスターと履歴（:registers の一覧。基本設計 4.5）である（EditorListResult.Registers）。
	ListRegisters EditorListKind = "registers"
	// ListJumps は、問い合わせを始めた client のジャンプリスト（基本設計 4.4）である（EditorListResult.Jumps）。
	ListJumps EditorListKind = "jumps"
	// ListHelp は、:help で引ける語（キー、Ex コマンド、設定の名前、操作の ID）である（EditorListResult.Help。API 0.13.0。FR-148）。
	ListHelp EditorListKind = "help"
)

// Known は、k がこの版の API の知っている一覧の種類かを返す。
func (k EditorListKind) Known() bool {
	switch k {
	case ListTabs, ListCommands, ListKeys, ListRegisters, ListJumps, ListHelp:
		return true
	}
	return false
}

// EditorListParams は、editor/list の params である。
type EditorListParams struct {
	List EditorListKind `json:"list"`
}

// Validate は、List が知っている種類かを検査する。本体は、誤りのある params に JSON-RPC のエラーで応答する。
func (p *EditorListParams) Validate() error {
	if !p.List.Known() {
		return fmt.Errorf("list %q は知らない一覧である", p.List)
	}
	return nil
}

// EditorListResult は、editor/list の result である。問い合わせた List の欄だけを埋め、他の欄は省く。
type EditorListResult struct {
	Tabs      []ListTab      `json:"tabs,omitempty"`
	Commands  []ListCommand  `json:"commands,omitempty"`
	Keys      []ListKey      `json:"keys,omitempty"`
	Registers []ListRegister `json:"registers,omitempty"`
	Jumps     []ListJump     `json:"jumps,omitempty"`
	Help      []ListHelpWord `json:"help,omitempty"`
}

// ListHelpWord の Kind の値。
const (
	HelpKindKey     = "key"
	HelpKindCommand = "command"
	HelpKindOption  = "option"
	HelpKindAction  = "action"
	// HelpKindOperation は、読み込んだ拡張機能のオペレーター、モーション、テキストオブジェクトの ID である（宣言ファイルの contributes）。
	HelpKindOperation = "operation"
)

// ListHelpWord は、:help で引ける語 1 つである（FR-148）。本体は、:help でその語の説明の行が見つかる語だけを返す。
// 並びは、種類の順（キー、Ex コマンド、設定の名前、操作の ID）で、同じ種類の中は本体の一覧の順である。同じ語は 1 つにまとめる。
type ListHelpWord struct {
	// Word は、:help に渡す語（"w"、"<C-o>"、":write"、"lineNumber.show"、"page-down"）である。
	// editor/execute の help に渡すと、その語の :help を開く。
	Word string `json:"word"`
	// Kind は、語の種類である。HelpKindKey（キー）、HelpKindCommand（Ex コマンドと拡張機能のコマンド。Word は : で始まる）、
	// HelpKindOption（:option.set で変える設定の名前と、設定ファイルの最上位の項目の名前）、HelpKindAction（設定の keymap に書く操作の ID）、
	// HelpKindOperation（拡張機能のオペレーター、モーション、テキストオブジェクトの ID）のどれかである。
	Kind string `json:"kind"`
	// Description は、語の 1 行の説明である。キー、操作の ID、拡張機能のコマンドと操作は、それを定めた所の説明文
	// （キーの一覧、操作の表、宣言ファイルの description）で、それが無い語は、:help でその語を引いたときの説明の行から
	// Markdown の記号と語そのものを除いた 1 行である。無ければ空。
	Description string `json:"description,omitempty"`
}

// ListTab は、タブ 1 つである。並びは、client の古い順に、各 client のタブの並びの順である。
type ListTab struct {
	Buffer BufferID `json:"buffer"`
	// Path は、ファイルの絶対パスである。ファイルでないバッファでは空。
	Path string `json:"path,omitempty"`
	// Name は、タブの行に出す名前（作業ディレクトリからの相対パス、ファイルでないバッファの見出しなど）である。
	Name string `json:"name"`
	// Client は、そのタブを持つ編集の client の番号である。本体がメッセージに出す「client N」の N と同じで、
	// server が接続ごとに割り当て、使い回さない（古い client が終わっても、残った client の番号は変わらない）。
	Client uint64 `json:"client"`
	// Own は、問い合わせを始めた client のタブかどうかである。
	Own bool `json:"own,omitempty"`
	// Current は、その client のカーソルのあるタブかどうかである。
	Current  bool `json:"current,omitempty"`
	Modified bool `json:"modified,omitempty"`
	ReadOnly bool `json:"readOnly,omitempty"`
}

// ListCommand は、コマンドラインから呼べるコマンド 1 つである。
type ListCommand struct {
	// Name は、省略しない名前である（"write"、"lsp.rename" など）。
	Name string `json:"name"`
	// Short は、本体の Ex コマンドの省略できる最短の形（"w"）である。拡張機能のコマンドでは空。
	Short string `json:"short,omitempty"`
	// Extension は、拡張機能のコマンドなら、その拡張機能の ID である。本体のコマンドでは空。
	Extension string `json:"extension,omitempty"`
	// Description は、宣言ファイルの説明文である。無ければ空。
	Description string `json:"description,omitempty"`
}

// ListKey は、キーの一覧の 1 行である（基本設計 7.2 の一覧の行の値。PR #230）。
type ListKey struct {
	// Keys は、キーの表記（"<Space>ff"）で、editor/execute の keys に渡すとその行を実行する。回数やレジスターのように Parser が先に読むキーでは、引数の名前を含む（"{回数}"）。
	Keys string `json:"keys"`
	// Modes は、キーが効くモードの文字（n、v、o、i、c）の列である。
	Modes string `json:"modes"`
	// Source は、出どころ（"builtin"、"user"、"extension"）である。
	Source string `json:"source"`
	// Extension は、Source が "extension" のときの拡張機能の ID である。
	Extension string `json:"extension,omitempty"`
	// Description は、説明文である。利用者のキーマップでは展開先のキーの列（"→ ..."）である。
	Description string `json:"description,omitempty"`
	// Overridden は、利用者のキーマップがこの行の操作を上書きしているモードの文字の列である。上書きされていなければ空。
	Overridden string `json:"overridden,omitempty"`
}

// ListRegister は、レジスターか履歴の 1 項目である。並びは :registers の一覧と同じである。
type ListRegister struct {
	// Name は、名前付きのレジスターの名前（"a"、"+" など）である。履歴の項目では空。
	Name string `json:"name,omitempty"`
	// Index は、履歴の項目の、新しい順の番号（0 から）である。名前付きのレジスターでは 0。
	Index    int    `json:"index,omitempty"`
	Text     string `json:"text"`
	Linewise bool   `json:"linewise,omitempty"`
}

// ListJump は、ジャンプリストの 1 項目である。並びは古い順で、:jumps と同じく同じ行の古い項目を除いた後のものである。
type ListJump struct {
	Buffer BufferID `json:"buffer"`
	// Path は、ファイルの絶対パスである。ファイルでないバッファでは空。
	Path     string   `json:"path,omitempty"`
	Name     string   `json:"name"`
	Position Position `json:"position"`
	// Text は、その行のテキストの、先頭の空白を除いたものである。バッファが閉じていれば空。
	Text string `json:"text,omitempty"`
	// Current は、<C-o> と <C-i> が次に読む位置の項目かどうかである。
	Current bool `json:"current,omitempty"`
}
