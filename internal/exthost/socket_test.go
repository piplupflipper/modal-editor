package exthost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// socketSpec は、起動の契機 socket だけを宣言した、modeSocket のテスト用の拡張機能の定義を作る。
func socketSpec(t *testing.T, id string) Spec {
	t.Helper()
	m := ext.Manifest{ID: id, Command: fakeCommand(t, modeSocket), Activation: []ext.Activation{{On: ext.ActivationSocket}}}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return Spec{Manifest: m}
}

// socketPath は、ソケットを置くパスを返す。t.TempDir は長くなりうるので、短い一時ディレクトリに置く。
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "exs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "mcp.sock")
}

// dialLine は、path へ接続し、相手が書いた 1 行を返す。相手が何も書かずに閉じたら空文字列と io.EOF を返す。
func dialLine(t *testing.T, path string) (string, error) {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(waitTimeout))
	return bufio.NewReader(c).ReadString('\n')
}

// answer は、path へ接続し、modeSocket の拡張機能が接続へ書いた行を読む。
func answer(t *testing.T, path string) (pid, inherited int) {
	t.Helper()
	line, err := dialLine(t, path)
	if err != nil {
		t.Fatalf("read: %q, %v", line, err)
	}
	if _, err := fmt.Sscanf(line, "%d %d\n", &pid, &inherited); err != nil {
		t.Fatalf("answer %q: %v", line, err)
	}
	return pid, inherited
}

// 本体は起動時から待ち受け、最初の接続で socket の契機の拡張機能を起動し、待ち受けと接続を引き継ぐ（基本設計 6.2）。
// 稼働中の接続は拡張機能が受け付け、異常で終わった後は次の接続で再起動し、無効の間の接続はすぐに閉じる。
//
// req: FR-14
func TestSocketActivation(t *testing.T) {
	path := socketPath(t)
	t.Setenv(ext.EnvSocketFDs, "9")
	hs := newHarness(t, []Spec{socketSpec(t, "m"), fakeSpec(t, "x", modeNormal, "")}, func(c *Config) {
		c.SocketPath = path
		c.MaxFailures = 10
	}, true)

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("socket file mode = %v, want socket 0600", fi.Mode())
	}
	// 接続が無い間は起動しない。
	time.Sleep(200 * time.Millisecond)
	if st := hs.state("m"); st != StateNotStarted {
		t.Fatalf("state before any connection = %v, want not-started", st)
	}
	mark := hs.mark()

	// 最初の接続: 本体が受け付けて起動し、その接続を引き継ぐ。
	pid1, n := answer(t, path)
	if n != 1 {
		t.Errorf("first connection: inherited %d connections, want 1", n)
	}
	hs.waitState("m", StateRunning)
	// 稼働中の接続は、引き継いだ待ち受けで拡張機能が受け付ける。
	pid, n := answer(t, path)
	if pid != pid1 || n != 1 {
		t.Errorf("second connection answered by pid %d (inherited %d), want %d", pid, n, pid1)
	}
	if got := hs.statesSince(mark, "m"); len(got) != 2 || got[0] != StateStarting || got[1] != StateRunning {
		t.Errorf("states = %v, want [starting running]", got)
	}

	// 他の拡張機能へは、本体の環境の古い値を空で上書きして渡す。
	var env fakeEnv
	if err := hs.h.Call(ctx, "x", methodEnv, nil, callTimeout, &env); err != nil {
		t.Fatal(err)
	}
	if !env.SocketFDsSet || env.SocketFDs != "" {
		t.Errorf("%s for another extension = %q (set %v), want empty", ext.EnvSocketFDs, env.SocketFDs, env.SocketFDsSet)
	}

	// 異常で終わった後は、待ち時間が過ぎても、次の接続まで再起動しない。
	if err := hs.h.Call(ctx, "m", methodExit, nil, callTimeout, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("exit: %v", err)
	}
	hs.waitState("m", StateRestartWait)
	time.Sleep(300 * time.Millisecond)
	if st := hs.state("m"); st != StateRestartWait {
		t.Fatalf("state after the restart delay without a connection = %v, want restart-wait", st)
	}
	pid2, n := answer(t, path)
	if pid2 == pid1 || n != 1 {
		t.Errorf("after crash: pid %d (inherited %d), want a new process with 1 connection", pid2, n)
	}
	hs.waitState("m", StateRunning)

	// 接続を待つ再起動待ちの間でも、問い合わせ（コマンドの呼び出し）と利用者の起動し直しでは、接続を待たずに起動する。
	crashAndWait := func() {
		t.Helper()
		if err := hs.h.Call(ctx, "m", methodExit, nil, callTimeout, nil); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("exit: %v", err)
		}
		hs.waitState("m", StateRestartWait)
		time.Sleep(300 * time.Millisecond)
		if st := hs.state("m"); st != StateRestartWait {
			t.Fatalf("state = %v, want restart-wait", st)
		}
	}
	crashAndWait()
	var st fakeState
	if err := hs.h.Call(ctx, "m", methodState, nil, callTimeout, &st); err != nil {
		t.Fatalf("query while waiting for a connection: %v", err)
	}
	if st.PID == pid2 {
		t.Error("query did not restart the extension")
	}
	hs.waitState("m", StateRunning)
	crashAndWait()
	if err := hs.h.Restart("m"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("m", StateRunning)
	if _, n = answer(t, path); n != 0 {
		t.Errorf("after Restart while waiting: inherited %d connections, want 0", n)
	}

	// 無効の間の接続は、本体が受け付けてすぐに閉じる。
	if err := hs.h.Disable("m"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("m", StateDisabled)
	if line, err := dialLine(t, path); !errors.Is(err, io.EOF) || line != "" {
		t.Errorf("connection while disabled = %q, %v; want EOF", line, err)
	}

	// 利用者が起動し直すと、接続が無くても起動し、待ち受けだけを引き継ぐ。
	if err := hs.h.Restart("m"); err != nil {
		t.Fatal(err)
	}
	hs.waitState("m", StateRunning)
	pid3, n := answer(t, path)
	if pid3 == pid2 || n != 0 {
		t.Errorf("after restart: pid %d (inherited %d), want a new process with 0 connections", pid3, n)
	}

	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file remains after Close: %v", err)
	}
}

// 未起動のまま閉じても、ソケットのファイルを消す。待ち受けられないパスでは起動の契機にならないが、Host は動く。
func TestSocketActivationUnavailable(t *testing.T) {
	path := socketPath(t)
	hs := newHarness(t, []Spec{socketSpec(t, "m")}, func(c *Config) { c.SocketPath = path }, true)
	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket file remains after Close: %v", err)
	}

	// ソケットでないファイルは消さず、待ち受けない。
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	hs = newHarness(t, []Spec{socketSpec(t, "m")}, func(c *Config) { c.SocketPath = path }, true)
	hs.waitLog("cannot listen for the socket activation")
	if err := hs.h.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("non-socket file was removed: %v", err)
	}
}

// 起動を含む問い合わせだけ、StartTimeout の長い期限で待つ（基本設計 5.2、#43 の裁定 1-b）。
// 稼働中の拡張機能への問い合わせは、Timeout で打ち切る。
//
// req: FR-17
func TestSendStartTimeout(t *testing.T) {
	short := 50 * time.Millisecond
	p := ext.TextObjectResolveParams{Version: 1, ID: "x.obj", Count: 1}
	result := func(hs *harness, tag string) *QueryResult {
		t.Helper()
		ev := hs.waitEvent("query result "+tag, func(ev Event) bool {
			r, ok := ev.(*QueryResult)
			return ok && r.Tag == tag
		})
		return ev.(*QueryResult)
	}

	// StartTimeout が無ければ、起動に slowInitDelay かかる拡張機能への最初の問い合わせは期限を超える。
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeSlowInit, "")}, nil, true)
	if _, err := hs.h.Send("x", ext.MethodTextObjectResolve, p, SendOptions{Tag: "a", Timeout: short}); err != nil {
		t.Fatal(err)
	}
	if r := result(hs, "a"); !errors.Is(r.Err, ErrTimeout) {
		t.Fatalf("without StartTimeout: %v, want ErrTimeout", r.Err)
	}

	hs = newHarness(t, []Spec{fakeSpec(t, "x", modeSlowInit, "")}, nil, true)
	opts := SendOptions{Tag: "b", Timeout: short, StartTimeout: callTimeout}
	if _, err := hs.h.Send("x", ext.MethodTextObjectResolve, p, opts); err != nil {
		t.Fatal(err)
	}
	if r := result(hs, "b"); r.Err != nil {
		t.Fatalf("with StartTimeout: %v", r.Err)
	}
	// 稼働中なら StartTimeout は使わない。
	slow := p
	slow.Args = json.RawMessage(`{"delayMs":400}`)
	opts.Tag = "c"
	if _, err := hs.h.Send("x", ext.MethodTextObjectResolve, slow, opts); err != nil {
		t.Fatal(err)
	}
	if r := result(hs, "c"); !errors.Is(r.Err, ErrTimeout) {
		t.Fatalf("running extension with StartTimeout: %v, want ErrTimeout", r.Err)
	}
}
