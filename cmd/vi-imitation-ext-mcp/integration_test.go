package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/editor"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// diagExtFlag の引数で起動したテストの実行ファイルは、テストを実行せずに、診断を送る拡張機能として動く（runDiagExt）。
const diagExtFlag = "-mcp-test-diag-ext"

// 結合のテストで使う実行ファイル。TestMain が一時ディレクトリへビルドする。
var (
	extBin    string
	bridgeBin string
)

func TestMain(m *testing.M) {
	for _, a := range os.Args[1:] {
		if a == diagExtFlag {
			os.Exit(runDiagExt())
		}
	}
	dir, err := os.MkdirTemp("", "mcpext")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	extBin = filepath.Join(dir, "vi-imitation-ext-mcp")
	bridgeBin = filepath.Join(dir, "vi-imitation-mcp-bridge")
	for _, b := range []struct{ out, pkg string }{{extBin, "."}, {bridgeBin, "../vi-imitation-mcp-bridge"}} {
		out, err := exec.Command("go", "build", "-o", b.out, b.pkg).CombinedOutput() //nolint:gosec // テストで使う実行ファイルを一時ディレクトリへビルドする
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", b.pkg, err, out)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// diagMessage は、診断を送る拡張機能が、開いたバッファごとに送る診断の本文である。
const diagMessage = "fake diagnostic"

// runDiagExt は、バッファを受け取るたびに、最初の行の先頭から 7 バイトに診断を 1 つ送る拡張機能である。
// get_diagnostics が本体の受け取り済みの診断を返すことを確かめるのに使う。
func runDiagExt() int {
	s := server.New()
	s.OnBufferOpen(func(_ context.Context, e *server.BufferOpenEvent) {
		_ = s.SetDiagnostics(ext.DiagnosticSetParams{
			Buffer: e.Buffer.ID(), Version: e.Buffer.Version(),
			Diagnostics: []ext.Diagnostic{{
				Range:    ext.Range{End: ext.Position{Byte: 7}},
				Severity: ext.SeverityWarning, Message: diagMessage, Source: "diagext",
			}},
		})
	})
	if err := s.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// fakeTerm は、テスト用の端末である。キーを送るだけで、画面は捨てる。
type fakeTerm struct{ in chan editor.TermEvent }

func (f *fakeTerm) Input() <-chan editor.TermEvent { return f.in }
func (f *fakeTerm) Size() (int, int)               { return 80, 24 }
func (f *fakeTerm) Draw(ui.Screen) error           { return nil }
func (f *fakeTerm) SetClipboard(string) error      { return nil }

func (f *fakeTerm) keys(s string) {
	for _, k := range editcmd.MustParseKeys(s) {
		f.in <- editor.TermEvent{Key: k}
	}
}

// specs は、MCP の拡張機能（宣言ファイルは manifest.json のもの）と、診断を送る拡張機能の定義を返す。
// mcp.start には、テストから押すキー \m を割り当てる。
func specs(t *testing.T) []exthost.Spec {
	t.Helper()
	b, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Contributes.Commands[0].Keys = `\m`
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	diag := ext.Manifest{
		ID: "diagext", Command: exe, Args: []string{diagExtFlag},
		Activation: []ext.Activation{{On: ext.ActivationFileType, Pattern: "go"}},
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m, diag}); err != nil {
		t.Fatal(err)
	}
	return []exthost.Spec{{Manifest: m, Dir: filepath.Dir(extBin)}, {Manifest: diag, Dir: t.TempDir()}}
}

// syncBuffer は、複数の goroutine から書いてよい strings.Builder である。本体の記録を集めるのに使う。
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// startEditor は、internal/editor のエディタを偽の端末で動かす。files を開き、テストの終わりに止める。
// 本体が拡張機能へ渡す MCP のソケットのパスと、本体の記録（拡張機能の状態の変化を含む）を一緒に返す。
func startEditor(t *testing.T, workDir string, files ...string) (*fakeTerm, string, *syncBuffer) {
	t.Helper()
	term := &fakeTerm{in: make(chan editor.TermEvent, 256)}
	logs := &syncBuffer{}
	e, err := editor.New(term, editor.Config{
		Extensions: specs(t), WorkDir: workDir, Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := e.Open(f); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("Run = %v", err)
			}
		case <-time.After(testTimeout):
			t.Error("Run が終わらない")
		}
		cctx, stop := context.WithTimeout(context.Background(), testTimeout)
		defer stop()
		if err := e.Close(cctx); err != nil {
			t.Errorf("Close = %v", err)
		}
	})
	return term, e.MCPSocket(), logs
}

// mcpStarting は、本体の記録のうち、MCP の拡張機能を起動し始めたことを表す部分である（exthost の状態の変化の記録）。
const mcpStarting = "extension=mcp state=starting"

// startBridge は、中継コマンドを子プロセスとして起動し、その標準入出力に話すクライアントを返す。
// 返した関数は、標準入力を閉じ、中継コマンドが終了コード 0 で終わることを確かめる。
func startBridge(t *testing.T, socket string) (*mcpClient, func()) {
	t.Helper()
	cmd := exec.Command(bridgeBin, "--socket", socket) //nolint:gosec // テストでビルドした中継コマンドを起動する
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := newClient(t, stdout, stdin)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			_ = stdin.Close()
			select {
			case <-c.done:
			case <-time.After(testTimeout):
				t.Error("中継コマンドの標準出力が閉じない")
				_ = cmd.Process.Kill()
			}
			if err := cmd.Wait(); err != nil {
				t.Errorf("中継コマンドの終了 = %v", err)
			}
		})
	}
	t.Cleanup(finish)
	return c, finish
}

// 中継コマンドを通して、MCP のクライアントから全ツールを呼び、本体（internal/editor と exthost）の状態を読み、編集し、保存する。
// MCP の拡張機能は、manifest.json の起動の契機 socket で、本体が最初の接続を受け付けたときに起動したものである（基本設計 6.2）。
// 最初の接続より前には起動していない。
//
// req: FR-14, FR-32, FR-33
func TestThroughBridge(t *testing.T) {
	work := t.TempDir()
	aPath := filepath.Join(work, "a.go")
	bPath := filepath.Join(work, "b.txt")
	if err := os.WriteFile(aPath, []byte("package a\n\nfunc f() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("héllo\nworld\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// ソケットのパスは本体が XDG_RUNTIME_DIR の下に決めて、拡張機能へ環境変数で渡す（基本設計 6.2）。
	// 本体の環境に残っている変数は使われない。
	rt, err := os.MkdirTemp("", "mcps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv(socketEnv, filepath.Join(rt, "stale"))

	term, sock, logs := startEditor(t, work, aPath)
	if !strings.HasPrefix(sock, filepath.Join(rt, "vi-imitation")+string(filepath.Separator)) {
		t.Fatalf("ソケットのパス = %q, want under %s", sock, rt)
	}
	// 本体は起動時から、決めたパスで待ち受けている。
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("ソケットのファイル = %v, want socket 0600", fi.Mode())
	}
	// 接続が無い間は、MCP の拡張機能を起動しない（FR-14）。
	// 診断の拡張機能（ファイルの種類 go で起動する）が稼働中になるまで待ち、その間に MCP の拡張機能が起動していないことを確かめる。
	deadline := time.Now().Add(testTimeout)
	for !strings.Contains(logs.String(), "extension=diagext state=running") {
		if time.Now().After(deadline) {
			t.Fatalf("診断の拡張機能が起動しない:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(logs.String(), mcpStarting) {
		t.Fatalf("接続の前に MCP の拡張機能が起動した:\n%s", logs.String())
	}

	// 最初の接続で、本体が MCP の拡張機能を起動し、受け付けた接続を引き継ぐ。
	c, finish := startBridge(t, sock)
	r := c.result("server/discover", withMeta(nil))
	if r["resultType"] != "complete" {
		t.Errorf("server/discover = %v", r)
	}
	if n := strings.Count(logs.String(), mcpStarting); n != 1 {
		t.Errorf("MCP の拡張機能を %d 回起動した, want 1", n)
	}

	// list_buffers
	var lb struct {
		Buffers []struct {
			Buffer   ext.BufferID `json:"buffer"`
			Path     string       `json:"path"`
			FileType string       `json:"fileType"`
			Version  ext.Version  `json:"version"`
			Modified bool         `json:"modified"`
			Current  bool         `json:"current"`
		} `json:"buffers"`
	}
	c.toolOK("list_buffers", map[string]any{}, &lb)
	if len(lb.Buffers) != 1 || lb.Buffers[0].Path != aPath || lb.Buffers[0].FileType != "go" || !lb.Buffers[0].Current || lb.Buffers[0].Modified {
		t.Fatalf("list_buffers = %+v", lb)
	}
	aID := lb.Buffers[0].Buffer

	// read_buffer
	type readResult struct {
		Buffer  ext.BufferID `json:"buffer"`
		Version ext.Version  `json:"version"`
		Text    string       `json:"text"`
	}
	var rb readResult
	c.toolOK("read_buffer", map[string]any{"path": aPath}, &rb)
	if rb.Buffer != aID || rb.Text != "package a\n\nfunc f() {}" {
		t.Fatalf("read_buffer = %+v", rb)
	}

	// get_diagnostics: 診断を送る拡張機能はファイルの種類 go で起動するので、届くまで待つ。
	var diags struct {
		Entries []struct {
			Buffer      ext.BufferID `json:"buffer"`
			Path        string       `json:"path"`
			Extension   string       `json:"extension"`
			Diagnostics []struct {
				Range   mcpRange `json:"range"`
				Message string   `json:"message"`
			} `json:"diagnostics"`
		} `json:"entries"`
	}
	deadline = time.Now().Add(testTimeout)
	for {
		c.toolOK("get_diagnostics", map[string]any{"buffer": aID}, &diags)
		if len(diags.Entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("get_diagnostics に診断が届かない")
		}
		time.Sleep(20 * time.Millisecond)
	}
	d := diags.Entries[0]
	if d.Extension != "diagext" || d.Path != aPath || len(d.Diagnostics) != 1 || d.Diagnostics[0].Message != diagMessage ||
		d.Diagnostics[0].Range != (mcpRange{Start: mcpPosition{1, 1}, End: mcpPosition{1, 8}}) {
		t.Errorf("get_diagnostics = %+v", d)
	}

	// edit_buffer: 位置はすべて読んだ版のテキストで数える。
	var eb struct {
		Applied bool        `json:"applied"`
		Version ext.Version `json:"version"`
	}
	c.toolOK("edit_buffer", map[string]any{"buffer": aID, "version": rb.Version, "edits": []any{
		map[string]any{"range": rng(3, 6, 3, 7), "text": "g"},
		map[string]any{"range": rng(1, 1, 1, 1), "text": "// edited\n"},
	}}, &eb)
	if !eb.Applied || eb.Version <= rb.Version {
		t.Fatalf("edit_buffer = %+v", eb)
	}
	const edited = "// edited\npackage a\n\nfunc g() {}"
	c.toolOK("read_buffer", map[string]any{"buffer": aID}, &rb)
	if rb.Text != edited || rb.Version != eb.Version {
		t.Errorf("編集の後の read_buffer = %+v", rb)
	}
	// 古い版の編集は、本体が適用しない（FR-15）。
	c.toolErr("edit_buffer", map[string]any{"buffer": aID, "version": rb.Version - 1, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 1), "text": "x"},
	}}, "read_buffer again")
	// バッファの外の位置は、送る前にツールの数え方（1 始まり）の本文で拒む。
	c.toolErr("edit_buffer", map[string]any{"buffer": aID, "version": rb.Version, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 1), "text": "x"},
		map[string]any{"range": rng(9, 1, 9, 1), "text": "x"},
	}}, "edits[1]: line 9 is out of range: the buffer has lines 1 to 4")
	c.toolErr("edit_buffer", map[string]any{"path": work + "/./a.go", "version": rb.Version, "edits": []any{
		map[string]any{"range": rng(2, 1, 2, 50), "text": "x"},
	}}, "edits[0]: column 50 of line 2 is out of range: the line has 9 bytes, so the column is 1 to 10")

	// get_editor_state: 利用者がビジュアルモードで選んだ範囲を読む。
	type stateResult struct {
		Buffer     ext.BufferID `json:"buffer"`
		Cursor     mcpPosition  `json:"cursor"`
		Mode       string       `json:"mode"`
		Selections []mcpRange   `json:"selections"`
	}
	var st stateResult
	term.keys("2Gvl")
	deadline = time.Now().Add(testTimeout)
	for {
		c.toolOK("get_editor_state", map[string]any{}, &st)
		if st.Mode == "visual" && st.Cursor.Column == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ビジュアルモードにならない: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Buffer != aID || st.Cursor != (mcpPosition{2, 2}) || len(st.Selections) != 1 ||
		st.Selections[0] != (mcpRange{Start: mcpPosition{2, 1}, End: mcpPosition{2, 3}}) {
		t.Errorf("get_editor_state = %+v", st)
	}
	term.keys("<Esc>")

	// open_file: 開いてカーソルを動かす。列はバイトで数える（é は 2 バイト）。
	var of struct {
		Buffer  ext.BufferID `json:"buffer"`
		Version ext.Version  `json:"version"`
		Moved   bool         `json:"moved"`
	}
	c.toolOK("open_file", map[string]any{"path": bPath, "line": 1, "column": 4}, &of)
	if of.Buffer == aID || !of.Moved {
		t.Errorf("open_file = %+v", of)
	}
	deadline = time.Now().Add(testTimeout)
	for {
		c.toolOK("get_editor_state", map[string]any{}, &st)
		if st.Mode == "normal" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ノーマルモードに戻らない: %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Buffer != of.Buffer || st.Cursor != (mcpPosition{1, 4}) {
		t.Errorf("open_file の後の get_editor_state = %+v", st)
	}
	// 開いた直後のバッファも読める（複製ができるのを待つ）。
	c.toolOK("read_buffer", map[string]any{"buffer": of.Buffer, "startLine": 2}, &rb)
	if rb.Text != "world" {
		t.Errorf("open_file の後の read_buffer = %+v", rb)
	}
	// 列はバイトで数えるので、é（列 2 から 2 バイト）の途中の列 3 は拒む。
	c.toolErr("edit_buffer", map[string]any{"buffer": of.Buffer, "version": rb.Version, "edits": []any{
		map[string]any{"range": rng(1, 3, 1, 3), "text": "x"},
	}}, "column 3 of line 1 is inside a multi-byte character that starts at column 2")

	// save_buffer: 編集した a.go を書き出す。
	var sb struct {
		Saved bool `json:"saved"`
	}
	c.toolOK("save_buffer", map[string]any{"path": aPath, "version": eb.Version}, &sb)
	if !sb.Saved {
		t.Errorf("save_buffer = %+v", sb)
	}
	got, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != edited+"\n" {
		t.Errorf("保存したファイル = %q", got)
	}
	c.toolOK("list_buffers", map[string]any{}, &lb)
	for _, b := range lb.Buffers {
		if b.Buffer == aID && b.Modified {
			t.Errorf("保存の後も未保存の変更がある: %+v", b)
		}
	}
	finish()

	// 2 本目の接続: 2025-11-25 の版のクライアント。本体から引き継いだ待ち受けで、稼働中の MCP の拡張機能が受け付ける。
	c2, finish2 := startBridge(t, sock)
	ir := c2.result("initialize", map[string]any{"protocolVersion": legacyVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "0"}})
	if ir["protocolVersion"] != legacyVersion {
		t.Errorf("initialize = %v", ir)
	}
	c2.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	isErr, text := c2.tool(false, "list_buffers", map[string]any{})
	if isErr || !strings.Contains(text, filepath.Base(bPath)) {
		t.Errorf("2025-11-25 の list_buffers = %v %s", isErr, text)
	}
	finish2()
	if n := strings.Count(logs.String(), mcpStarting); n != 1 {
		t.Errorf("MCP の拡張機能を %d 回起動した, want 1", n)
	}
}

// 本体から待ち受けを引き継がなかったときは、何も待ち受けず、mcp.start は失敗して理由を利用者へ知らせる。
func TestStartWithoutInheritedSocket(t *testing.T) {
	x := newExtension("", nil, nil, nil)
	defer x.stop()
	x.start()
	if res := x.startCommand(); res.OK {
		t.Error("待ち受けを引き継いでいないのに mcp.start が成功した")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if !errors.Is(x.listenErr, errNotInherited) {
		t.Errorf("listenErr = %v", x.listenErr)
	}
}

// 本体がソケットのパスを決められなかったときは、待ち受けず、MCP の拡張機能は socket の契機では起動しない。
// mcp.start で起動すると、引き継げなかったことを利用者へ知らせる。
func TestWithoutSocketPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
	t.Setenv("XDG_RUNTIME_DIR", long)
	t.Setenv("TMPDIR", long)
	term, sock, logs := startEditor(t, t.TempDir())
	if sock != "" {
		t.Fatalf("ソケットのパス = %q, want empty", sock)
	}
	term.keys(`\m`)
	deadline := time.Now().Add(testTimeout)
	for !strings.Contains(logs.String(), "extension=mcp state=running") {
		if time.Now().After(deadline) {
			t.Fatalf("mcp.start で起動しない:\n%s", logs.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
