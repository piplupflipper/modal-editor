package exthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// 段階 2 の確認（要件定義 8 章「拡張機能の起動、監視、再起動の仕組みが、実際の拡張機能で機能するか」）として、
// 実際にビルドした modal-editor-ext-lsp を Host から起動し、言語サーバーの代わりに LSP を話すテスト用のサーバー
// （cmd/modal-editor-ext-lsp/testdata/fakels）を使って、次を確かめる。
//
//   - ファイルの種類の契機で起動し、initialize の settings が届く（settings に書いた言語サーバーが起動する）
//   - 開いたバッファの診断が DiagnosticSet で届く
//   - 言語サーバーの応答を待つ間も ping に答え、異常と判断されない
//   - 拡張機能を強制終了すると、Host が再起動し、buffer/open の送り直しで言語サーバーに現在の全文が届き、診断が戻る
//   - 強制終了した拡張機能の言語サーバーの子プロセスが残らない。Host を閉じた後も残らない
//
// 2 つの実行ファイルを go のツールチェーンでビルドするので、-short では読み飛ばす。

func TestStage2LSP(t *testing.T) {
	if testing.Short() {
		t.Skip("builds modal-editor-ext-lsp and its fake language server")
	}
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "modal-editor-ext-lsp")
	for _, b := range []struct{ out, pkg string }{
		{"modal-editor-ext-lsp", src},
		{"fakels", filepath.Join(src, "testdata", "fakels")},
	} {
		//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
		cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, b.out), "./"+filepath.ToSlash(b.pkg))
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", b.out, err, out)
		}
	}
	data, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "fakels.log")
	settings, err := json.Marshal(map[string]any{"servers": map[string]any{"go": map[string]any{
		"command": filepath.Join(dir, "fakels"),
		"args":    []string{"-log", logPath, "-fmt-delay", "1m"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := New([]Spec{{Manifest: m, Dir: dir, Settings: settings}}, Config{
		PingInterval: 50 * time.Millisecond,
		PingTimeout:  time.Second,
		RestartDelay: 50 * time.Millisecond,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })

	diags := make(chan ext.DiagnosticSetParams, 100)
	states := make(chan *StateChange, 100)
	go func() {
		for ev := range h.Events() {
			switch ev := ev.(type) {
			case *DiagnosticSet:
				diags <- ev.Params
			case *StateChange:
				states <- ev
			}
		}
	}()
	waitState := func(want State) {
		t.Helper()
		for {
			select {
			case sc := <-states:
				if sc.State == want {
					return
				}
				if want == StateRunning && sc.State != StateStarting {
					t.Fatalf("state = %v (%v), want %v", sc.State, sc.Err, want)
				}
			case <-time.After(waitTimeout):
				t.Fatalf("state %v did not come", want)
			}
		}
	}
	waitDiags := func(v ext.Version, n int) {
		t.Helper()
		for {
			select {
			case p := <-diags:
				if p.Version == v && len(p.Diagnostics) == n {
					return
				}
			case <-time.After(waitTimeout):
				t.Fatalf("diagnostics of version %d did not come", v)
			}
		}
	}

	path := filepath.Join(dir, "a.go")
	if err = h.OpenBuffer(BufferSnapshot{ID: 1, Path: path, FileType: "go", Version: 1, Text: "package a // ERR"}); err != nil {
		t.Fatal(err)
	}
	waitState(StateRunning)
	waitDiags(1, 1)
	first := fakeStarts(t, logPath)
	if len(first) != 1 {
		t.Fatalf("fakels started %d times, want 1", len(first))
	}

	// 言語サーバーの応答を待つ間（整形を 1 分遅らせる）も ping に答える。ping の期限は 1 秒、間隔は 50 ms。
	var res ext.ProviderInvokeResult
	err = h.Call(context.Background(), m.ID, ext.MethodProviderInvoke, ext.ProviderInvokeParams{Operation: ext.OpFormat, Buffer: 1, Version: 1}, waitTimeout, &res)
	if err != nil || !res.OK {
		t.Fatalf("format = %+v, %v", res, err)
	}
	if _, err := h.WillSave(context.Background(), m.ID, ext.BufferWillSaveParams{Buffer: 1, Version: 1}, 200*time.Millisecond); !errors.Is(err, ErrTimeout) {
		t.Fatalf("willSave = %v, want ErrTimeout", err)
	}
	time.Sleep(time.Second)
	select {
	case sc := <-states:
		t.Fatalf("state changed while the language server was slow: %v (%v)", sc.State, sc.Err)
	default:
	}

	// 版 2 にしてから拡張機能を強制終了する。
	at := func(b int) buffer.Position { return buffer.Position{Line: 0, Byte: b} }
	if err := h.ChangeBuffer(1, buffer.Change{Before: 1, After: 2, Edits: []buffer.Edit{{Range: buffer.Range{Start: at(0), End: at(0)}, Text: "ERR\n"}}}); err != nil {
		t.Fatal(err)
	}
	waitDiags(2, 2)
	if err := syscall.Kill(first[0].PPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitState(StateRestartWait)
	waitState(StateRunning)
	waitDiags(2, 2)
	if !processGone(first[0].PID) {
		t.Errorf("language server %d of the killed extension is still alive", first[0].PID)
	}
	starts := fakeStarts(t, logPath)
	if len(starts) != 2 || starts[1].PPID == first[0].PPID {
		t.Fatalf("fakels starts = %+v, want a second one under a new extension process", starts)
	}
	if got := lastText(t, logPath); got != "ERR\npackage a // ERR\n" {
		t.Errorf("text resent to the restarted language server = %q", got)
	}

	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if !processGone(starts[1].PID) {
		t.Errorf("language server %d is still alive after Close", starts[1].PID)
	}
}

// fakeStart は、fakels のログの "start" の行である。
type fakeStart struct {
	PID  int `json:"pid"`
	PPID int `json:"ppid"`
}

// fakeLog は、fakels のログを読み、kind の行の value を返す。
func fakeLog(t *testing.T, path, kind string) []json.RawMessage {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var vs []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e struct {
			Kind  string          `json:"kind"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Kind == kind {
			vs = append(vs, e.Value)
		}
	}
	return vs
}

func fakeStarts(t *testing.T, path string) []fakeStart {
	t.Helper()
	var ss []fakeStart
	for _, v := range fakeLog(t, path, "start") {
		var s fakeStart
		if err := json.Unmarshal(v, &s); err != nil {
			t.Fatal(err)
		}
		ss = append(ss, s)
	}
	return ss
}

// lastText は、fakels が最後に持った文書の全文を返す。
func lastText(t *testing.T, path string) string {
	t.Helper()
	vs := fakeLog(t, path, "text")
	if len(vs) == 0 {
		t.Fatal("no text in the fakels log")
	}
	var lt struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(vs[len(vs)-1], &lt); err != nil {
		t.Fatal(err)
	}
	return lt.Text
}
