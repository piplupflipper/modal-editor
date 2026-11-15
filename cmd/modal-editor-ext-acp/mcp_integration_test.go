package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// buildMCP は、MCP の拡張機能と中継コマンドを dir へビルドし、それぞれのパスを返す。
func buildMCP(t *testing.T, dir string) (extPath, bridgePath string) {
	t.Helper()
	extPath = filepath.Join(dir, "modal-editor-ext-mcp")
	bridgePath = filepath.Join(dir, "modal-editor-mcp-bridge")
	for _, b := range []struct{ out, pkg string }{{extPath, "../modal-editor-ext-mcp"}, {bridgePath, "../modal-editor-mcp-bridge"}} {
		out, err := exec.Command("go", "build", "-o", b.out, b.pkg).CombinedOutput() //nolint:gosec // テストで使う実行ファイルを一時ディレクトリへビルドする
		if err != nil {
			t.Fatalf("build %s: %v\n%s", b.pkg, err, out)
		}
	}
	return extPath, bridgePath
}

// readManifest は、宣言ファイルを読む。
func readManifest(t *testing.T, path string) ext.Manifest {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// outputCollector は、Host のイベントを受け取り、acp の欄へ書き足された本文を集める。
// 拡張機能からのリクエストには失敗で答える（この結合のテストでは使わない）。
type outputCollector struct {
	mu   sync.Mutex
	text strings.Builder
}

func (o *outputCollector) run(events <-chan exthost.Event) {
	for ev := range events {
		switch e := ev.(type) {
		case *exthost.OutputAppend:
			if e.Params.Channel == outputChannel {
				o.mu.Lock()
				o.text.WriteString(e.Params.Text)
				o.mu.Unlock()
			}
		case interface{ ReplyError(error) error }:
			_ = e.ReplyError(errors.New("not supported in this test"))
		}
	}
}

func (o *outputCollector) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.text.String()
}

// 本体（exthost）が決めた MCP のソケットのパスを、MCP の拡張機能と ACP の拡張機能の両方が環境変数で受け取る。
// ACP の拡張機能が session/new でエージェントへ渡す MCP サーバー（中継コマンド）の引数のパスは、MCP の拡張機能が待ち受けるパスと一致し、
// その設定のとおりに中継コマンドを起動すると、MCP の拡張機能と initialize をやり取りできる（基本設計 6.2、6.5）。
//
// 本体の側は internal/editor の New と同じく、exthost.NewMCPSocket で決めたパスを Config.Env で渡す。
// エディタを通さないのは、ACP のプロンプトの本文を command/execute の引数で渡す書き方がエディタに無いため（基本設計 10 章）である。
//
// req: FR-33, FR-34
func TestMCPSocketSharedWithACP(t *testing.T) {
	bin := t.TempDir()
	mcpExt, bridge := buildMCP(t, bin)

	// ソケットのパスの長さの上限に収めるため、短い名前のディレクトリを XDG_RUNTIME_DIR にする。
	rt, err := os.MkdirTemp("", "acpm")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv(envMCPSocket, filepath.Join(rt, "stale"))
	sock, err := exthost.NewMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sock.Remove() })

	mcpManifest := readManifest(t, "../modal-editor-ext-mcp/manifest.json")
	acpManifest := readManifest(t, "manifest.json")
	if _, err = ext.ValidateManifests([]ext.Manifest{mcpManifest, acpManifest}); err != nil {
		t.Fatal(err)
	}
	acpSettings, err := json.Marshal(settings{Agent: agentConfig{Command: fakeAgent}, MCPBridge: bridge})
	if err != nil {
		t.Fatal(err)
	}
	specs := []exthost.Spec{
		{Manifest: mcpManifest, Dir: filepath.Dir(mcpExt)},
		{Manifest: acpManifest, Dir: filepath.Dir(extBinary), Settings: acpSettings},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		logger = slog.Default()
	}
	host, err := exthost.New(specs, exthost.Config{Env: []string{sock.Env()}, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	var out outputCollector
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		out.run(host.Events())
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		defer cancel()
		if cerr := host.CloseContext(ctx); cerr != nil {
			t.Errorf("Close = %v", cerr)
		}
		<-collected
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	// バッファを開かずに呼ぶので、Withheld でバッファの複製が無いことを伝える（どちらのコマンドもバッファを使わない）。
	// MCP の拡張機能を起動する。起動すると本体が決めたパスで待ち受ける。
	start, err := host.ExecuteCommand(ctx, "mcp", ext.CommandExecuteParams{ID: "mcp.start", Withheld: true}, testTimeout)
	if err != nil || !start.OK {
		t.Fatalf("mcp.start = %+v, %v", start, err)
	}
	// ACP の拡張機能にプロンプトを送り、テスト用のエージェントが session/new で受け取った内容を欄へ書かせる。
	args, err := json.Marshal(map[string]string{"text": `{"op":"session"}`})
	if err != nil {
		t.Fatal(err)
	}
	res, err := host.ExecuteCommand(ctx, "acp", ext.CommandExecuteParams{ID: cmdPrompt, Args: args, Withheld: true}, testTimeout)
	if err != nil || !res.OK {
		t.Fatalf("acp.prompt = %+v, %v", res, err)
	}
	var line string
	for line == "" {
		for l := range strings.SplitSeq(out.String(), "\n") {
			if s, ok := strings.CutPrefix(l, "session="); ok {
				line = s
			}
		}
		if line != "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("session/new の内容が欄に書かれない: %q", out.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	var got struct {
		Session sessionNewParams `json:"session"`
	}
	if err = json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("%v: %s", err, line)
	}
	if len(got.Session.MCPServers) != 1 {
		t.Fatalf("mcpServers = %+v, want one", got.Session.MCPServers)
	}
	srv := got.Session.MCPServers[0]
	if srv.Command != bridge || len(srv.Args) != 2 || srv.Args[0] != "--socket" || srv.Args[1] != sock.Path {
		t.Fatalf("mcpServers[0] = %+v, want %s --socket %s", srv, bridge, sock.Path)
	}

	// MCP の拡張機能は、同じパスで待ち受けている。
	conn, err := net.Dial("unix", sock.Path)
	if err != nil {
		t.Fatalf("MCP の拡張機能が %s で待ち受けていない: %v", sock.Path, err)
	}
	_ = conn.Close()

	// エージェントと同じく、受け取った設定のとおりに中継コマンドを起動し、MCP の initialize をやり取りする。
	cmd := exec.CommandContext(ctx, srv.Command, srv.Args...) //nolint:gosec // テストでビルドした中継コマンドを、ACP の拡張機能が渡した引数で起動する
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}` + "\n"
	if _, err = io.WriteString(stdin, req); err != nil {
		t.Fatal(err)
	}
	resp, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("中継コマンドの応答を読めない: %v", err)
	}
	var r struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil || r.ID != 1 || r.Result.ProtocolVersion != "2025-11-25" {
		t.Errorf("initialize の応答 = %q (%v)", resp, err)
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Errorf("中継コマンドの終了 = %v", err)
	}
}
