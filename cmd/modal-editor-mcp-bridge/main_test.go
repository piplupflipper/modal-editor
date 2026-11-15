package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperEnv が設定されたプロセスは、テストではなく main を実行する。
// 終了コードを確かめるために、テストの実行ファイルを中継コマンドとして起動し直す。
const helperEnv = "MODAL_EDITOR_MCP_BRIDGE_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// listenUnix は、一時ディレクトリに Unix ドメインソケットを立てる。
// t.TempDir のパスは長く、ソケットのパスの上限（108 バイト程度）を超えうるので、短い一時ディレクトリを使う。
func listenUnix(t *testing.T) (net.Listener, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mcpb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln, path
}

// accept は、相手（エディタ役）の接続を 1 つ受けて返す。
func accept(t *testing.T, ln net.Listener) <-chan net.Conn {
	t.Helper()
	ch := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(ch)
			return
		}
		ch <- conn
	}()
	return ch
}

// startBridge は、標準入出力を io.Pipe にして run を別の goroutine で動かす。
// 戻り値は、標準入力への書き込み口、標準出力の読み出し口、run の戻り値を受ける channel である。
func startBridge(t *testing.T, args []string, getenv func(string) string) (*io.PipeWriter, *io.PipeReader, <-chan error) {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	t.Cleanup(func() {
		_ = stdinW.Close()
		_ = stdoutR.Close()
	})

	done := make(chan error, 1)
	go func() {
		var stderr bytes.Buffer
		err := run(args, stdinR, stdoutW, &stderr, getenv)
		_ = stdoutW.Close()
		done <- err
	}()
	return stdinW, stdoutR, done
}

func noEnv(string) string { return "" }

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("中継が終わらない")
		return nil
	}
}

func receiveConn(t *testing.T, ch <-chan net.Conn) net.Conn {
	t.Helper()
	select {
	case conn, ok := <-ch:
		if !ok {
			t.Fatal("接続を受けられなかった")
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("中継コマンドが接続してこない")
		return nil
	}
}

// 標準入力からソケットへ、ソケットから標準出力へ、内容を変えずに転送する。
// 標準入力が閉じたら、ソケットを閉じて終わる。
//
// req: FR-33
func TestBridgeBothDirections(t *testing.T) {
	ln, path := listenUnix(t)
	conns := accept(t, ln)
	stdin, stdout, done := startBridge(t, []string{"--socket", path}, noEnv)
	conn := receiveConn(t, conns)

	// エディタ役は、受けたバイト列をそのまま送り返す。
	echoed := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, conn)
		echoed <- err
	}()

	// MCP のメッセージ（改行区切りの JSON）に限らず、任意のバイト列を解釈せずに通す。
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	payload = append(payload, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n")...)

	writeErr := make(chan error, 1)
	go func() {
		_, err := stdin.Write(payload)
		writeErr <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(stdout, got); err != nil {
		t.Fatalf("標準出力の読み出しに失敗: %v", err)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("標準入力への書き込みに失敗: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("送り返された内容が送った内容と違う")
	}

	// 標準入力を閉じると、中継はソケットを閉じて終わる。
	_ = stdin.Close()
	if err := waitDone(t, done); err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
	select {
	case err := <-echoed:
		if err != nil {
			t.Errorf("エディタ役の送り返しが誤りで終わった: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("エディタ役から見て、ソケットが閉じていない")
	}
}

// エディタ側がソケットを閉じたら、標準入力が開いたままでも、それまでの内容を標準出力へ書いて終わる。
//
// req: FR-33
func TestBridgeEndsWhenSocketCloses(t *testing.T) {
	ln, path := listenUnix(t)
	conns := accept(t, ln)
	_, stdout, done := startBridge(t, []string{"--socket", path}, noEnv)
	conn := receiveConn(t, conns)

	const msg = `{"jsonrpc":"2.0","method":"notifications/message"}` + "\n"
	go func() {
		_, _ = io.WriteString(conn, msg)
		_ = conn.Close()
	}()

	got, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("標準出力の読み出しに失敗: %v", err)
	}
	if string(got) != msg {
		t.Errorf("stdout = %q, want %q", got, msg)
	}
	if err := waitDone(t, done); err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
}

// --socket が無ければ、環境変数のパスへ接続する。両方あれば --socket を優先する。
//
// req: FR-33
func TestRunSocketSelection(t *testing.T) {
	const (
		none    = iota // 渡さない
		good           // 待ち受けているソケットのパスを渡す
		missing        // 存在しないパスを渡す
	)
	tests := []struct {
		name    string
		flag    int
		env     int
		wantErr bool
	}{
		{name: "--socket だけ", flag: good, env: none},
		{name: "環境変数だけ", flag: none, env: good},
		{name: "両方あれば --socket を優先", flag: good, env: missing},
		{name: "--socket が誤りなら環境変数が正しくても失敗", flag: missing, env: good, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, path := listenUnix(t)
			conns := accept(t, ln)
			value := func(kind int) string {
				switch kind {
				case good:
					return path
				case missing:
					return path + ".missing"
				default:
					return ""
				}
			}

			var args []string
			if tt.flag != none {
				args = []string{"--socket", value(tt.flag)}
			}
			getenv := func(key string) string {
				if key != socketEnv {
					return ""
				}
				return value(tt.env)
			}

			stdin, _, done := startBridge(t, args, getenv)
			if tt.wantErr {
				if err := waitDone(t, done); err == nil {
					t.Fatal("run() error = nil, want error")
				}
				return
			}
			receiveConn(t, conns)
			_ = stdin.Close()
			if err := waitDone(t, done); err != nil {
				t.Fatalf("run() error = %v, want nil", err)
			}
		})
	}
}

// 接続先が無いときは、理由を標準エラー出力に書いて終了コード 1 で終わる。
//
// req: FR-33
func TestExitCodeWithoutSocket(t *testing.T) {
	ln, path := listenUnix(t)
	_ = ln.Close() // ソケットのファイルを消し、接続先が無い状態にする。

	tests := []struct {
		name       string
		args       []string
		env        string
		wantStderr string
	}{
		{name: "指定が無い", args: nil, wantStderr: "接続先のソケットが指定されていない"},
		{name: "--socket の先にエディタがいない", args: []string{"--socket", path}, wantStderr: "接続に失敗"},
		{name: "環境変数の先にエディタがいない", env: path, wantStderr: "接続に失敗"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], tt.args...) //nolint:gosec // テストの実行ファイル自身を起動し直す
			cmd.Env = append(filterEnv(os.Environ(), socketEnv), helperEnv+"=1")
			if tt.env != "" {
				cmd.Env = append(cmd.Env, socketEnv+"="+tt.env)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr

			err := cmd.Run()

			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				t.Fatalf("Run() error = %v, want ExitError", err)
			}
			if code := exitErr.ExitCode(); code != 1 {
				t.Errorf("終了コード = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want containing %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

// filterEnv は、env から key の変数を除いたものを返す。
func filterEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func TestRunFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantErr    error
		anyErr     bool
	}{
		{name: "version フラグ", args: []string{"--version"}, wantStdout: "dev\n"},
		{name: "help フラグ", args: []string{"--help"}, wantErr: flag.ErrHelp},
		{name: "未知のフラグはエラー", args: []string{"--unknown"}, anyErr: true},
		{name: "余分な引数はエラー", args: []string{"extra"}, anyErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := run(tt.args, strings.NewReader(""), &stdout, &stderr, noEnv)

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("run() error = %v, want %v", err, tt.wantErr)
				}
			case tt.anyErr:
				if err == nil {
					t.Fatal("run() error = nil, want error")
				}
			default:
				if err != nil {
					t.Fatalf("run() error = %v", err)
				}
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}
