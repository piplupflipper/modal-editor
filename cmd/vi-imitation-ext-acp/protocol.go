package main

import "encoding/json"

// この節は、拡張機能が使う ACP のメッセージの型である。
// 2026-11-16 に https://agentclientprotocol.com の protocol の各ページ（Overview、Initialization、Session Setup、
// Prompt Turn、Tool Calls、File System、Transports）で確かめた、プロトコルの版 1（protocolVersion が 1。ページの URL の
// /protocol/v1/）の形に合わせる。拡張機能が読まない項目は型に書かない。

// protocolVersion は、initialize で申し出る ACP のプロトコルの版である（Initialization。整数の MAJOR の版）。
const protocolVersion = 1

// ACP のメソッドの名前。
const (
	methodInitialize        = "initialize"
	methodSessionNew        = "session/new"
	methodSessionPrompt     = "session/prompt"
	methodSessionCancel     = "session/cancel"
	methodSessionUpdate     = "session/update"
	methodRequestPermission = "session/request_permission"
	methodReadTextFile      = "fs/read_text_file"
	methodWriteTextFile     = "fs/write_text_file"
)

// initializeParams は、クライアントからエージェントへの initialize の params である。
type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         implementation     `json:"clientInfo"`
}

// clientCapabilities は、クライアントが申し出る能力である。
// ファイルの読み書きは両方を申し出て、端末（terminal/*）は申し出ない（基本設計 6.5）。
type clientCapabilities struct {
	FS       fsCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type fsCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// implementation は、clientInfo と agentInfo の形である。
type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initializeResult は、initialize の result である。
type initializeResult struct {
	// ProtocolVersion は、エージェントが選んだ版である。申し出た版に対応していなければ、エージェントが対応する最新の版を返す。
	ProtocolVersion int             `json:"protocolVersion"`
	AgentInfo       *implementation `json:"agentInfo,omitempty"`
	// AuthMethods は、エージェントが求める認証の方法である。最初の範囲では authenticate を送らない。
	AuthMethods []json.RawMessage `json:"authMethods,omitempty"`
}

// envVariable は、MCP サーバーへ渡す環境変数である。
type envVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// mcpServer は、session/new で渡す標準入出力の MCP サーバーである（Session Setup）。
// command は実行ファイルの絶対パスで、name、command、args は必須である。
type mcpServer struct {
	Name    string        `json:"name"`
	Command string        `json:"command"`
	Args    []string      `json:"args"`
	Env     []envVariable `json:"env"`
}

// sessionNewParams は、session/new の params である。cwd は絶対パスである。
type sessionNewParams struct {
	Cwd        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

type sessionNewResult struct {
	SessionID string `json:"sessionId"`
}

// contentBlock は、プロンプトと更新の本文の 1 つの塊である。拡張機能が作るのは text だけである。
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type sessionPromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// sessionPromptResult は、session/prompt の result である。
// stopReason は end_turn、max_tokens、max_turn_requests、refusal、cancelled のいずれかである（Prompt Turn）。
type sessionPromptResult struct {
	StopReason string `json:"stopReason"`
}

// stopCancelled は、取り消されたプロンプトの stopReason である。
const stopCancelled = "cancelled"

type sessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// sessionUpdateParams は、エージェントからの session/update の params である。
type sessionUpdateParams struct {
	SessionID string        `json:"sessionId"`
	Update    sessionUpdate `json:"update"`
}

// sessionUpdate は、更新の 1 件である。sessionUpdate の値で種類を分ける（Prompt Turn）。
// 種類ごとに使う項目だけを持つ。
type sessionUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	// Content は、agent_message_chunk では 1 つの contentBlock、tool_call と tool_call_update では配列である。
	Content json.RawMessage `json:"content,omitempty"`

	// 以下は tool_call と tool_call_update の項目である。tool_call_update では、変わった項目だけが届く。
	ToolCallID string             `json:"toolCallId,omitempty"`
	Title      string             `json:"title,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Status     string             `json:"status,omitempty"`
	Locations  []toolCallLocation `json:"locations,omitempty"`

	// Entries は、plan の項目である。
	Entries []planEntry `json:"entries,omitempty"`
}

// 拡張機能が表示する更新の種類（Prompt Turn）。
const (
	updateAgentMessageChunk = "agent_message_chunk"
	updateToolCall          = "tool_call"
	updateToolCallUpdate    = "tool_call_update"
	updatePlan              = "plan"
)

// toolCallLocation は、ツールの呼び出しが読み書きしているファイルの絶対パスと、1 から数えた行である（Tool Calls）。
type toolCallLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

type planEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"`
}

// toolCallUpdate は、session/request_permission の toolCall である。tool_call_update と同じ形である。
type toolCallUpdate struct {
	ToolCallID string             `json:"toolCallId"`
	Title      string             `json:"title,omitempty"`
	Kind       string             `json:"kind,omitempty"`
	Locations  []toolCallLocation `json:"locations,omitempty"`
}

// permissionOption は、権限の確認の選択肢である。kind は allow_once、allow_always、reject_once、reject_always のいずれかである。
type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type requestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  toolCallUpdate     `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

// requestPermissionResult は、session/request_permission の result である。
type requestPermissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

// permissionOutcome は、選んだ選択肢（outcome が selected で optionId を持つ）か、取り消し（outcome が cancelled）である。
type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

type readTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	// Line は、読み始める行で、1 から数える。
	Line *int `json:"line,omitempty"`
	// Limit は、読む行数の上限である。
	Limit *int `json:"limit,omitempty"`
}

type readTextFileResult struct {
	Content string `json:"content"`
}

type writeTextFileParams struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

// writeTextFileResult は、fs/write_text_file の result で、空のオブジェクトである。
type writeTextFileResult struct{}
