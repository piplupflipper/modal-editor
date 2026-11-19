package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

// コマンドの ID。宣言ファイル manifest.json の contributes.commands と揃える（基本設計 6.5。名前は仮）。
const (
	cmdPrompt = "acp.prompt"
	cmdCancel = "acp.cancel"
)

// outputChannel は、エージェントの返答と進み具合を書き足す output/append の欄の名前である（基本設計 6.5。仮）。
const outputChannel = "acp"

// envMCPSocket は、本体が拡張機能へ MCP のソケットのパスを渡す環境変数の名前である（基本設計 6.2。仮）。
// MCP の中継コマンド（cmd/vi-imitation-mcp-bridge）が読む名前と同じにする。
// 本体（internal/editor）が起動ごとにパスを決め、MCP の拡張機能はそのパスで待ち受ける。
const envMCPSocket = ext.EnvMCPSocket

// mcpBridgeName は、MCP の中継コマンドの実行ファイルの名前である。
const mcpBridgeName = "vi-imitation-mcp-bridge"

// mcpServerName は、session/new で渡す MCP サーバーの名前である（仮）。
const mcpServerName = "vi-imitation"

// clientName は、initialize の clientInfo の name である。
const clientName = "vi-imitation"

// settings は、利用者の設定のうち、この拡張機能に向けた部分（initialize の settings）である。項目の名前は仮である。
//
//	{
//	  "agent": { "command": "my-acp-agent", "args": ["--acp"], "env": { "KEY": "value" } },
//	  "mcpBridge": "/opt/vi-imitation/vi-imitation-mcp-bridge"
//	}
//
// agent.command には既定の値が無い。省くと acp.prompt は失敗し、設定を求めるメッセージを出す。
// mcpBridge は MCP の中継コマンドの絶対パスで、省くと mcpBridgePath の規則で探す。
type settings struct {
	Agent     agentConfig `json:"agent"`
	MCPBridge string      `json:"mcpBridge,omitempty"`
}

// timeouts は、待つ時間の上限である。値は仮で、テストが短くする。
type timeouts struct {
	// start は、エージェントの起動から session/new の応答までを待つ時間である。
	start time.Duration
	// request は、fs/* の処理で本体の応答と複製を待つ時間である。
	request time.Duration
	// stopGrace は、エージェントを終わらせるときの、標準入力を閉じてから SIGTERM まで、SIGTERM から SIGKILL まで、
	// SIGKILL から待つのをやめるまでの、それぞれの猶予である（agentProcess.stop）。
	// 本体は shutdown を送ってから期限（仮に 3 秒。internal/exthost の DefaultShutdownTimeout。基本設計 5.2）を過ぎると
	// 拡張機能のプロセスグループへ SIGTERM を送るので、3 倍してもその期限より短くする。
	stopGrace time.Duration
	// cancel は、acp.cancel で session/cancel を送ってから、エージェントがプロンプトに応答するのを待つ時間である。
	// 過ぎたら、取り消しに応じないエージェントとして止め、プロンプトを終える（次の acp.prompt で起動し直す）。
	cancel time.Duration
}

func defaultTimeouts() timeouts {
	return timeouts{start: 60 * time.Second, request: 30 * time.Second, stopGrace: 800 * time.Millisecond, cancel: 5 * time.Second}
}

// client は、ACP の拡張機能の状態である。ACP のクライアントとして、設定のエージェントを子プロセスで起動して話す。
//
// 状態は mu で守る。Server のループ（コマンドのハンドラーとフック）、プロンプトの goroutine、
// エージェントとの接続の goroutine（lineConn）の 3 つから触る。mu を持ったまま、応答を待つ処理を行わない。
// output/append は応答を待たない通知なので mu を持ったまま送り、書き足す順序を保つ。
//
// エージェントは最初の acp.prompt で起動し、異常終了したら次の acp.prompt で起動し直す。
// 1 つのセッションでは一度に 1 つのプロンプトだけを扱い（ACP の Prompt Turn）、処理中の acp.prompt は失敗させる。
type client struct {
	srv      *server.Server
	settings settings
	timeouts timeouts
	logger   *log.Logger
	// unopened は、開いていないファイルへの fs/write_text_file の書き方である（#43 の裁定 3 の A で、本体にバッファとして開かせる。unopenedFileWriter）。
	unopened unopenedFileWriter
	// getenv と executable は、環境変数と自分の実行ファイルのパスを得る関数である。テストが差し替える。
	getenv     func(string) string
	executable func() (string, error)

	mu    sync.Mutex
	agent *agentProcess
	// turn は、処理中のプロンプトの ctx で、プロンプトが無ければ nil である。
	// acp.cancel で取り消し、処理中の権限の確認を cancelled で終わらせる（ACP の Prompt Turn の Cancellation）。
	turn       context.Context
	cancelTurn context.CancelFunc
	// turnDone は、処理中のプロンプトが終わったときに閉じる。acp.cancel の期限の監視が使う。
	turnDone chan struct{}
	// toolCalls は、処理中のプロンプトで session/update の tool_call と tool_call_update で届いた、
	// ツールの呼び出しの表示の項目である。権限の確認の toolCall に欠けた項目を補うのに使う（requestPermission）。
	toolCalls map[string]toolCallUpdate
	// paths は、開いているバッファのパスから番号への対応である（基本設計 6.7 のバッファのパス）。
	paths map[string]ext.BufferID
	// atLineStart は、欄へ最後に書き足したテキストが改行で終わっているか（何も書いていないか）である。
	atLineStart bool
	shutdown    bool
	// cwd は、session/new の cwd である。拡張機能の作業ディレクトリは本体のものである（基本設計 5.1）。
	cwd string
}

func newClient() *client {
	c := &client{
		timeouts:    defaultTimeouts(),
		logger:      log.New(os.Stderr, "vi-imitation-ext-acp: ", log.LstdFlags),
		getenv:      os.Getenv,
		executable:  os.Executable,
		paths:       make(map[string]ext.BufferID),
		toolCalls:   make(map[string]toolCallUpdate),
		atLineStart: true,
	}
	c.unopened = openAsBuffer{c: c}
	c.srv = c.newServer()
	return c
}

// newServer は、コマンドとフックを登録した Server を作る。
func (c *client) newServer() *server.Server {
	s := server.New()
	s.ErrorLog = c.logger
	s.OnInitialize(c.onInitialize)
	s.OnBufferOpen(c.onBufferOpen)
	s.OnBufferClose(c.onBufferClose)
	s.Command(cmdPrompt, c.prompt)
	s.Command(cmdCancel, c.noArgs(cmdCancel, c.cancel))
	s.OnShutdown(func(context.Context, *server.ShutdownEvent) { c.stop() })
	return s
}

func (c *client) onInitialize(_ context.Context, e *server.InitializeEvent) error {
	if wd, err := os.Getwd(); err == nil {
		c.cwd = wd
	}
	if len(e.Settings) == 0 || string(e.Settings) == "null" {
		return nil
	}
	var s settings
	if err := json.Unmarshal(e.Settings, &s); err != nil {
		return fmt.Errorf("invalid settings: %w", err)
	}
	c.settings = s
	return nil
}

func (c *client) onBufferOpen(_ context.Context, e *server.BufferOpenEvent) {
	if path := e.Buffer.Path(); path != "" {
		c.mu.Lock()
		c.paths[path] = e.Buffer.ID()
		c.mu.Unlock()
	}
}

func (c *client) onBufferClose(_ context.Context, e *server.BufferCloseEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p, id := range c.paths {
		if id == e.Buffer {
			delete(c.paths, p)
		}
	}
}

// bufferFor は、path のバッファの複製を持っていれば、その番号を返す。
func (c *client) bufferFor(path string) (ext.BufferID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.paths[path]
	return id, ok
}

// stop は、拡張機能が終わるときに、処理中のプロンプトを取り消し、エージェントを終わらせる。
// 本体から shutdown を受け取ったとき（OnShutdown のフック）と、Serve が戻った後（main の run）に呼ぶ。
// 本体の強制終了（拡張機能のプロセスグループへの SIGKILL）では走らず、エージェントは Pdeathsig の SIGTERM で終わる（startAgent）。
//
// req: FR-34
func (c *client) stop() {
	c.mu.Lock()
	c.shutdown = true
	ag := c.agent
	if c.cancelTurn != nil {
		c.cancelTurn()
	}
	c.mu.Unlock()
	if ag != nil {
		ag.stop(c.timeouts.stopGrace)
	}
}

func (c *client) logf(format string, args ...any) { c.logger.Printf(format, args...) }

func (c *client) showError(text string) {
	if err := c.srv.ShowMessage(ext.MessageError, text); err != nil {
		c.logf("message/show: %v", err)
	}
}

// fail は、コマンドを失敗させ、理由を利用者へ表示する。
func (c *client) fail(text string) (ext.CommandExecuteResult, error) {
	c.showError(text)
	return ext.CommandExecuteResult{OK: false}, nil
}

// appendText は、text を acp の欄へそのまま書き足す。
func (c *client) appendText(text string) {
	if text == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.appendLocked(text)
}

// appendLine は、text を 1 行として acp の欄へ書き足す。直前の書き足しが行の途中で終わっていれば、改行してから書く。
func (c *client) appendLine(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.atLineStart {
		text = "\n" + text
	}
	c.appendLocked(text + "\n")
}

func (c *client) appendLocked(text string) {
	if err := c.srv.AppendOutput(outputChannel, text); err != nil {
		c.logf("output/append: %v", err)
		return
	}
	c.atLineStart = strings.HasSuffix(text, "\n")
}

// promptArgs は、acp.prompt の command/execute の args である（基本設計 6.5。仮）。
// 本体は、コマンドラインの :acp.prompt の後の引数の文字列を Text に入れて送る（ext の package の doc）。
type promptArgs struct {
	Text string `json:"text"`
}

// noArgs は、引数を取らないコマンド（acp.cancel）のハンドラー h を包む。
// コマンドラインから引数を付けて呼んだ（:acp.cancel foo）ら、h を呼ばずに、引数を取らないことを知らせて失敗させる。
// DAP の拡張機能の引数を取らないコマンドと同じ扱いである。
func (c *client) noArgs(id string, h server.CommandHandler) server.CommandHandler {
	return func(ctx context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		var a promptArgs
		if len(p.Args) > 0 {
			if err := json.Unmarshal(p.Args, &a); err != nil {
				return c.fail(fmt.Sprintf("%s: 引数を読めない: %v", id, err))
			}
		}
		if strings.TrimSpace(a.Text) != "" {
			return c.fail(id + ": 引数を取らない")
		}
		return h(ctx, b, p)
	}
}

// prompt は、acp.prompt のハンドラーである。受け付けたことだけを ok で返し、エージェントの起動とプロンプトの送信は別の goroutine で行う。
// 結果は output/append と message/show で後から届ける（基本設計 6 章の冒頭）。
//
// params の sync（マクロとドットリピートの再生）は見ない。プロンプトの結果は表示とファイルの書き込みで、
// command/execute の応答の transaction と open で返す編集やカーソルの移動を持たないためである。
//
// req: FR-34
func (c *client) prompt(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
	var a promptArgs
	if len(p.Args) > 0 {
		if err := json.Unmarshal(p.Args, &a); err != nil {
			return c.fail(fmt.Sprintf("%s: 引数を読めない: %v", cmdPrompt, err))
		}
	}
	if strings.TrimSpace(a.Text) == "" {
		return c.fail(cmdPrompt + ": プロンプトの本文が空である")
	}
	if c.settings.Agent.Command == "" {
		return c.fail(cmdPrompt + ": エージェントが設定されていない（設定の agent.command に書く）")
	}
	c.mu.Lock()
	switch {
	case c.shutdown:
		c.mu.Unlock()
		return c.fail(cmdPrompt + ": 拡張機能は終了の処理中である")
	case c.turn != nil:
		c.mu.Unlock()
		return c.fail(cmdPrompt + ": 前のプロンプトを処理している（" + cmdCancel + " で取り消せる）")
	}
	turn, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.turn, c.cancelTurn, c.turnDone = turn, cancel, done
	c.mu.Unlock()
	go c.runPrompt(turn, done, a.Text) //nolint:gosec // G118: ハンドラーの ctx は戻ると無効になるので引き継がない
	return ext.CommandExecuteResult{OK: true}, nil
}

// runPrompt は、セッションが無ければエージェントを起動してセッションを作り、session/prompt を送って応答を待つ。
// 終わったら done を閉じる。
//
// req: FR-34
func (c *client) runPrompt(turn context.Context, done chan struct{}, text string) {
	defer func() {
		c.mu.Lock()
		c.cancelTurn()
		c.turn, c.cancelTurn, c.turnDone = nil, nil, nil
		clear(c.toolCalls)
		c.mu.Unlock()
		close(done)
	}()
	c.appendLine("> " + text)
	ag, err := c.ensureSession(turn)
	if err != nil {
		if turn.Err() != nil {
			c.appendLine("[" + stopCancelled + "]")
			return
		}
		c.showError("ACP: " + err.Error())
		c.appendLine("[error] " + err.Error())
		return
	}
	c.mu.Lock()
	sid := ag.sessionID
	c.mu.Unlock()
	var res sessionPromptResult
	// 取り消しは session/cancel で伝え、エージェントが cancelled の stopReason で応答するのを待つ（ACP の Prompt Turn）。
	// このため、応答を待つ ctx に turn を使わない。エージェントが終われば、接続が閉じて待つのが終わる。
	err = ag.conn.call(context.Background(), methodSessionPrompt, sessionPromptParams{
		SessionID: sid,
		Prompt:    []contentBlock{{Type: "text", Text: text}},
	}, &res)
	switch {
	case errors.Is(err, errConnClosed) && ag.stopped() && turn.Err() != nil:
		// 取り消しに応じないので止めた（cancel の期限）。知らせは cancel が出している。
		c.appendLine("[" + stopCancelled + "]")
	case errors.Is(err, errConnClosed):
		// エージェントの終了そのものは onAgentExit が message/show で知らせる。
		// 欄には、このプロンプトが終わらなかったことを書く。
		c.appendLine("[error] エージェントとの接続が切れたので、このプロンプトは終わらなかった")
	case err != nil:
		c.showError("ACP: session/prompt: " + err.Error())
		c.appendLine("[error] " + err.Error())
	default:
		c.appendLine("[" + res.StopReason + "]")
	}
}

// ensureSession は、エージェントが起動していなければ起動して initialize を送り、セッションが無ければ session/new で作る。
// 途中で失敗したら、起動したエージェントを終わらせる。
//
// req: FR-34
func (c *client) ensureSession(turn context.Context) (*agentProcess, error) {
	c.mu.Lock()
	ag := c.agent
	if ag != nil && ag.conn.closed() {
		// 接続は閉じたが、onAgentExit がまだ呼ばれていない（プロセスの終了を待っている）。終わったものとして起動し直す。
		ag = nil
	}
	if ag != nil && ag.sessionID != "" {
		c.mu.Unlock()
		return ag, nil
	}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(turn, c.timeouts.start)
	defer cancel()
	if ag == nil {
		var err error
		ag, err = startAgentProcess(c.settings.Agent, c.cwd, c.logger, c.handleRequest, c.handleNotification, c.onAgentExit)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.shutdown {
			c.mu.Unlock()
			ag.stop(c.timeouts.stopGrace)
			return nil, errors.New("拡張機能は終了の処理中である")
		}
		c.agent = ag
		c.mu.Unlock()
	}
	sid, err := c.newSession(ctx, ag)
	if err != nil {
		c.mu.Lock()
		if c.agent == ag {
			c.agent = nil
		}
		c.mu.Unlock()
		ag.stop(c.timeouts.stopGrace)
		return nil, err
	}
	c.mu.Lock()
	ag.sessionID = sid
	c.mu.Unlock()
	return ag, nil
}

// newSession は、initialize と session/new を送り、セッションの ID を返す。
// クライアントの能力は、ファイルの読み書きを申し出て、端末を申し出ない（基本設計 6.5）。
func (c *client) newSession(ctx context.Context, ag *agentProcess) (string, error) {
	var ir initializeResult
	err := ag.conn.call(ctx, methodInitialize, initializeParams{
		ProtocolVersion:    protocolVersion,
		ClientCapabilities: clientCapabilities{FS: fsCapabilities{ReadTextFile: true, WriteTextFile: true}},
		ClientInfo:         implementation{Name: clientName, Version: ext.APIVersion},
	}, &ir)
	if err != nil {
		return "", fmt.Errorf("initialize: %w", err)
	}
	if ir.ProtocolVersion != protocolVersion {
		return "", fmt.Errorf("initialize: エージェントの ACP の版 %d に対応していない（対応する版は %d）", ir.ProtocolVersion, protocolVersion)
	}
	var sr sessionNewResult
	if err := ag.conn.call(ctx, methodSessionNew, sessionNewParams{Cwd: c.cwd, MCPServers: c.mcpServers()}, &sr); err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}
	if sr.SessionID == "" {
		return "", errors.New("session/new: sessionId が空である")
	}
	return sr.SessionID, nil
}

// mcpServers は、session/new でエージェントへ渡す MCP サーバーの一覧を返す（基本設計 6.2、6.5）。
// 本体が環境変数 VI_IMITATION_MCP_SOCKET でソケットのパスを渡していて、中継コマンドが見つかれば、
// 中継コマンドを `--socket <パス>` の引数で起動する標準入出力の MCP サーバーとして渡す。そうでなければ空の一覧を返す。
//
// req: FR-34
func (c *client) mcpServers() []mcpServer {
	sock := c.getenv(envMCPSocket)
	if sock == "" {
		return []mcpServer{}
	}
	bridge := c.mcpBridgePath()
	if bridge == "" {
		c.logf("%s not found; the agent is started without the editor's MCP server", mcpBridgeName)
		return []mcpServer{}
	}
	return []mcpServer{{Name: mcpServerName, Command: bridge, Args: []string{"--socket", sock}, Env: []envVariable{}}}
}

// mcpBridgePath は、MCP の中継コマンドの絶対パスを探す。見つからなければ空を返す。
//
// 探す順は、設定の mcpBridge、この拡張機能の実行ファイルと同じディレクトリ、その 2 つ上のディレクトリ（make dist の配布物では
// 拡張機能が extensions/acp/ に、中継コマンドが本体と同じ階層に置かれる）、PATH である。
// 拡張機能を探す規則（要件定義 7 章）が決まったら、この規則もそれに合わせる（基本設計 6.5）。
func (c *client) mcpBridgePath() string {
	if p := c.settings.MCPBridge; p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return ""
	}
	if exe, err := c.executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, mcpBridgeName),
			filepath.Join(dir, "..", "..", mcpBridgeName),
		} {
			if st, err := os.Stat(cand); err == nil && st.Mode().IsRegular() {
				return filepath.Clean(cand)
			}
		}
	}
	if p, err := exec.LookPath(mcpBridgeName); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return ""
}

// cancel は、acp.cancel のハンドラーである。処理中のプロンプトへ session/cancel を送り、
// 応答を待っている権限の確認を cancelled で終わらせる（ACP の Prompt Turn の Cancellation）。
// プロンプトの応答（stopReason が cancelled）は、runPrompt が受け取って欄へ書く。
//
// req: FR-34
func (c *client) cancel(_ context.Context, _ *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
	c.mu.Lock()
	if c.turn == nil {
		c.mu.Unlock()
		return c.fail(cmdCancel + ": 処理中のプロンプトが無い")
	}
	cancelTurn, done := c.cancelTurn, c.turnDone
	ag := c.agent
	var sid string
	if ag != nil {
		sid = ag.sessionID
	}
	c.mu.Unlock()
	if sid == "" {
		// セッションを作る前なら、作るのをやめさせる（runPrompt）。
		cancelTurn()
		return ext.CommandExecuteResult{OK: true}, nil
	}
	// session/cancel を、権限の確認の cancelled の応答より先に送る。エージェントが cancelled の応答を受け取った時点で
	// 取り消しを知っていれば、プロンプトを cancelled の stopReason で終えられる（ACP の Prompt Turn の Cancellation）。
	// エージェントが標準入力を読んでいないと書き込みが詰まりうるので、ループの上で書かない。
	go func() {
		if err := ag.conn.notify(methodSessionCancel, sessionCancelParams{SessionID: sid}); err != nil {
			c.logf("session/cancel: %v", err)
		}
		cancelTurn()
	}()
	go c.watchCancel(ag, done)
	return ext.CommandExecuteResult{OK: true}, nil
}

// watchCancel は、acp.cancel の後、期限（timeouts.cancel）までにプロンプトが終わらなければ、
// 取り消しに応じないエージェントとして止める。止めると接続が閉じ、runPrompt はプロンプトを cancelled として終える。
// 止めずにおくと、session/prompt の応答を待ち続け、次の acp.prompt が失敗し続ける。
//
// req: FR-34
func (c *client) watchCancel(ag *agentProcess, done <-chan struct{}) {
	t := time.NewTimer(c.timeouts.cancel)
	defer t.Stop()
	select {
	case <-done:
		return
	case <-t.C:
	}
	c.showError(fmt.Sprintf("ACP: エージェントが %v 以内に取り消しに応じなかったので止めた。次の %s で起動し直す", c.timeouts.cancel, cmdPrompt))
	ag.stop(c.timeouts.stopGrace)
}

// onAgentExit は、エージェントのプロセスが終わったときに呼ばれる。
// 拡張機能が終わらせたのでなければ（異常終了なら）、利用者へ知らせる。次の acp.prompt でエージェントを起動し直す。
// 処理中のプロンプトの session/prompt は、接続が閉じたことで終わる。
//
// req: FR-34
func (c *client) onAgentExit(ag *agentProcess) {
	c.mu.Lock()
	if c.agent == ag {
		c.agent = nil
	}
	c.mu.Unlock()
	if ag.stopped() {
		return
	}
	status := "終了コード 0"
	if ag.exitErr != nil {
		status = ag.exitErr.Error()
	}
	c.showError("ACP: エージェントが終了した（" + status + "）。次の " + cmdPrompt + " で起動し直す")
	c.appendLine("[エージェントが終了した: " + status + "]")
}
