package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/server"
)

// shortRuntimeDir は、XDG_RUNTIME_DIR に使う、権限 0700 の短いパスのディレクトリを作る。
// server のソケットのパスは 103 バイトに収める必要があり、t.TempDir はテストの名前を含んで長くなりうる。
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "me")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// unitSocket は、XDG_RUNTIME_DIR が runtime のときの、server の単位 unit の server のソケットのパスである（基本設計 1.2）。
func unitSocket(runtime, unit string) string {
	return filepath.Join(runtime, "vi-imitation", server.UnitDirName(unit), server.SocketName)
}

// serverPIDs は、sock を -server に渡して起動した server のプロセスの ID を、/proc から探して返す。
func serverPIDs(t *testing.T, sock string) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skipf("/proc を読めない: %v", err)
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(string(b), "\x00")
		if i := slices.Index(args, "-server"); i >= 0 && i+1 < len(args) && args[i+1] == sock {
			pids = append(pids, pid)
		}
	}
	return pids
}

// killServersAtCleanup は、テストの終わりに、sock の server が残っていれば SIGKILL で止める（失敗したテストの後始末）。
func killServersAtCleanup(t *testing.T, sock string) {
	t.Helper()
	t.Cleanup(func() {
		for _, pid := range serverPIDs(t, sock) {
			t.Logf("後始末で server（pid %d）を止める", pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// waitServers は、sock の server のプロセスがちょうど want 個になるまで待ち、その ID を返す。
// ロックを取れずに終わる server が一瞬いることがあるので、数が want で 100ms 落ち着くまで待つ。
func waitServers(t *testing.T, sock string, want int) []int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var stableSince time.Time
	for {
		pids := serverPIDs(t, sock)
		switch {
		case len(pids) != want:
			stableSince = time.Time{}
		case stableSince.IsZero():
			stableSince = time.Now()
		case time.Since(stableSince) > 100*time.Millisecond:
			return pids
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s の server の数 = %d（%v）, want %d", sock, len(pids), pids, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitServerGone は、sock の server のプロセスが終わり、ソケットのファイルが消えるのを待つ（FR-61）。
func waitServerGone(t *testing.T, sock string) {
	t.Helper()
	waitServers(t, sock, 0)
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("server の終了の後にソケット %s が残った: %v", sock, err)
	}
}

// fakeHerdrPanes は、pane.get だけに答える herdr の socket API の偽物である。panes はペインの ID からタブの ID への表である。
func fakeHerdrPanes(t *testing.T, panes map[string]string) string {
	t.Helper()
	dir := shortRuntimeDir(t)
	sock := filepath.Join(dir, "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						PaneID string `json:"pane_id"`
					} `json:"params"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				resp := map[string]any{"id": req.ID}
				if tab, ok := panes[req.Params.PaneID]; ok && req.Method == "pane.get" {
					resp["result"] = map[string]any{"pane": map[string]any{"pane_id": req.Params.PaneID, "tab_id": tab}}
				} else {
					resp["error"] = map[string]any{"code": "pane_not_found", "message": "no such pane"}
				}
				b, _ := json.Marshal(resp)
				_, _ = conn.Write(append(b, '\n'))
			}()
		}
	}()
	return sock
}

// herdrEnv は、herdr のペイン pane の中で起動したときの環境変数である。
func herdrEnv(runtime, herdrSock, pane string) []string {
	return []string{"XDG_RUNTIME_DIR=" + runtime, "HERDR_ENV=1", "HERDR_SOCKET_PATH=" + herdrSock, "HERDR_PANE_ID=" + pane}
}

// TestAutostartHerdrInPTY は、herdr の中（偽の socket API）で起動した vi-imitation が、
// タブの server を自分の子として別のプロセスで起動して繋ぐこと、同じタブの 2 つ目は同じ server に繋ぐこと、
// 別のタブでは別の server になること、最後の編集の client が終わると server も終わってソケットを消すことを確かめる。
//
// 同じタブの 2 つ目は、同じ server の 2 つ目の編集の client になる（FR-62）。
//
// req: FR-59, FR-61, FR-62
func TestAutostartHerdrInPTY(t *testing.T) {
	runtime := shortRuntimeDir(t)
	herdrSock := fakeHerdrPanes(t, map[string]string{"wA:p1": "wA:t1", "wA:p2": "wA:t1", "wA:p3": "wA:t2"})
	sock1 := unitSocket(runtime, herdrSock+":wA:t1")
	sock2 := unitSocket(runtime, herdrSock+":wA:t2")
	killServersAtCleanup(t, sock1)
	killServersAtCleanup(t, sock2)
	path := filepath.Join(t.TempDir(), "a.txt")

	a := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p1"), path)
	a.out.WaitForString(t, "a.txt")
	a.out.WaitForString(t, "NORMAL")
	pids := waitServers(t, sock1, 1)
	if pids[0] == a.cmd.Process.Pid {
		t.Fatal("server が編集の client と同じプロセスである")
	}
	if err := syscall.Kill(pids[0], 0); err != nil {
		t.Fatal(err)
	}
	if pgid, err := syscall.Getpgid(pids[0]); err != nil || pgid == a.cmd.Process.Pid {
		t.Errorf("server のプロセスグループ = %d, %v; want client（%d）のプロセスグループの外", pgid, err, a.cmd.Process.Pid)
	}

	// 同じタブの 2 つ目は、同じ server に 2 つ目の編集の client として繋ぎ、自分の引数のファイルをタブに開く（FR-62）。
	// server は増えない。2 つ目が :wq で終わっても、server は 1 つ目のために残る
	pathB := filepath.Join(t.TempDir(), "b.txt")
	b := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p2"), pathB)
	b.out.WaitForString(t, "b.txt")
	b.out.WaitForString(t, "NORMAL")
	if strings.Contains(b.out.String(), "1 つのプロセス") {
		t.Errorf("同じタブの 2 つ目が 1 つのプロセスで動いた。出力: %q", tail(b.out.String()))
	}
	if got := waitServers(t, sock1, 1); got[0] != pids[0] {
		t.Errorf("同じタブの server = %v, want %v のまま", got, pids)
	}
	b.write(t, "ibee\x1b:wq\r")
	if err := b.wait(t); err != nil {
		t.Fatalf("同じタブの 2 つ目の終了 = %v, want 正常終了。出力: %q", err, tail(b.out.String()))
	}
	if got, err := os.ReadFile(pathB); err != nil || string(got) != "bee\n" {
		t.Errorf("b.txt = %q, %v; want %q", got, err, "bee\n")
	}
	if got := serverPIDs(t, sock1); len(got) != 1 || got[0] != pids[0] {
		t.Errorf("2 つ目の client の後の server = %v, want %v のまま", got, pids)
	}

	// 別のタブでは、別の server になる
	c := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p3"))
	c.out.WaitForString(t, "NORMAL")
	pids2 := waitServers(t, sock2, 1)
	if pids2[0] == pids[0] {
		t.Fatal("別のタブの server が同じプロセスである")
	}

	// 最後の編集の client が終わると、server も終わる（FR-61）
	a.write(t, "ihello\x1b:wq\r")
	if err := a.wait(t); err != nil {
		t.Fatalf("client の終了 = %v, want 正常終了。出力: %q", err, tail(a.out.String()))
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "hello\n" {
		t.Errorf("保存した内容 = %q, %v; want %q", got, err, "hello\n")
	}
	waitServerGone(t, sock1)
	if got := serverPIDs(t, sock2); len(got) != 1 {
		t.Errorf("別のタブの server = %v, want 動いたまま", got)
	}
	c.write(t, ":q\r")
	if err := c.wait(t); err != nil {
		t.Fatalf("別のタブの client の終了 = %v。出力: %q", err, tail(c.out.String()))
	}
	waitServerGone(t, sock2)
}

// TestAutostartRaceInPTY は、同じタブの 2 つのペインでほぼ同時に起動しても、server のロックで server が 1 つになることを確かめる（基本設計 1.2）。
// どちらも同じ server の編集の client になる（FR-62）。
// どちらのペインにも、そのペインで打った引数のファイルが開く（server を起動した側の引数を、先に繋いだ他方のペインに開かない。PR #207 のレビューの推奨 1）。
//
// req: FR-59
func TestAutostartRaceInPTY(t *testing.T) {
	for i := range 5 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			runtime := shortRuntimeDir(t)
			herdrSock := fakeHerdrPanes(t, map[string]string{"wA:p1": "wA:t1", "wA:p2": "wA:t1"})
			sock := unitSocket(runtime, herdrSock+":wA:t1")
			killServersAtCleanup(t, sock)
			dir := t.TempDir()
			pathA, pathB := filepath.Join(dir, "aaa.txt"), filepath.Join(dir, "bbb.txt")
			p1 := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p1"), pathA)
			p2 := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p2"), pathB)
			p1.out.WaitForString(t, "aaa.txt")
			p2.out.WaitForString(t, "bbb.txt")
			for _, p := range []*editorProc{p1, p2} {
				p.out.WaitForString(t, "NORMAL")
			}
			for _, p := range []*editorProc{p1, p2} {
				if strings.Contains(p.out.String(), "1 つのプロセス") {
					t.Errorf("1 つのプロセスで動いた。出力: %q", tail(p.out.String()))
				}
			}
			for _, tc := range []struct {
				p     *editorProc
				other string
			}{{p1, "bbb.txt"}, {p2, "aaa.txt"}} {
				if strings.Contains(tc.p.out.String(), tc.other) {
					t.Errorf("他方のペインで打った %s が開いた。出力: %q", tc.other, tail(tc.p.out.String()))
				}
			}
			waitServers(t, sock, 1)
			for _, p := range []*editorProc{p1, p2} {
				p.write(t, ":q\r")
				if err := p.wait(t); err != nil {
					t.Fatalf("エディタの終了 = %v。出力: %q", err, tail(p.out.String()))
				}
			}
			waitServerGone(t, sock)
		})
	}
}

// TestAutostartClientKilledInPTY は、server を起動した client が、server の起動の途中（繋ぐ前）に kill -9 で終わっても、
// server が誰も繋がないまま残らないことを確かめる（PR #207 のレビューの必須 2）。
// 最初の編集の client を待つ上限（firstClientTimeout）は、テストの間だけ 500ms にする。
// kill -9 が client の接続の後になった回でも、server は接続が切れて終わるので、どちらでも server は残らない。
//
// req: FR-61
func TestAutostartClientKilledInPTY(t *testing.T) {
	runtime := shortRuntimeDir(t)
	herdrSock := fakeHerdrPanes(t, map[string]string{"wA:p1": "wA:t1"})
	sock := unitSocket(runtime, herdrSock+":wA:t1")
	killServersAtCleanup(t, sock)
	p := startEditorIn(t, t.TempDir(), append(herdrEnv(runtime, herdrSock, "wA:p1"), envFirstClientTimeout+"=500ms"))
	deadline := time.Now().Add(10 * time.Second)
	for len(serverPIDs(t, sock)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("server が起動しない。出力: %q", tail(p.out.String()))
		}
	}
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.wait(t) // SIGKILL で終わった誤りが返る
	waitServerGone(t, sock)
}

// TestServeAutoWithoutClient は、-server の server が、firstClientTimeout の間に編集の client が繋がなければ、
// 正常に終わってソケットを消すことを確かめる（起動した client が、繋ぐ前に終わった場合）。
//
// req: FR-61
func TestServeAutoWithoutClient(t *testing.T) {
	runtime := shortRuntimeDir(t)
	sock := filepath.Join(runtime, "vi-imitation", "u-test", server.SocketName)
	killServersAtCleanup(t, sock)
	xdg := t.TempDir()
	cmd := exec.Command(os.Args[0], "-server", sock) //nolint:gosec // テストの実行ファイルを server として起動する
	cmd.Env = append(muxFreeEnv(), envRunMain+"=1", envFirstClientTimeout+"=300ms",
		"XDG_CONFIG_HOME="+filepath.Join(xdg, "config"), "XDG_DATA_HOME="+filepath.Join(xdg, "data"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server の終了 = %v, want 正常終了。出力: %q", err, out.String())
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("client が繋がない server が終わらない")
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("server が %v で終わった, want 300ms 待ってから", d)
	}
	if _, err := os.Lstat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ソケット %s が残った: %v", sock, err)
	}
}

// TestAutostartStaleSocketInPTY は、SIGKILL で終わった server が残したソケットのファイルがあっても、
// 次に起動した client が新しい server を起動して繋ぐことを確かめる（基本設計 1.2）。
// また、VI_IMITATION_SOCKET の指す server がもう無ければ、規則 2 以降でソケットを決めることを確かめる。
//
// req: FR-59
func TestAutostartStaleSocketInPTY(t *testing.T) {
	runtime := shortRuntimeDir(t)
	herdrSock := fakeHerdrPanes(t, map[string]string{"wA:p1": "wA:t1"})
	sock := unitSocket(runtime, herdrSock+":wA:t1")
	killServersAtCleanup(t, sock)
	env := herdrEnv(runtime, herdrSock, "wA:p1")

	a := startEditorIn(t, t.TempDir(), env)
	a.out.WaitForString(t, "NORMAL")
	pids := waitServers(t, sock, 1)
	if err := syscall.Kill(pids[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if err := a.wait(t); err == nil {
		t.Fatal("server が SIGKILL で終わったのに client が正常終了した")
	}
	waitServers(t, sock, 0)
	if _, err := os.Lstat(sock); err != nil {
		t.Fatalf("SIGKILL の server のソケットのファイルが残っていない（テストの前提）: %v", err)
	}

	gone := filepath.Join(runtime, "gone", server.SocketName)
	b := startEditorIn(t, t.TempDir(), append(env, envSocket+"="+gone))
	b.out.WaitForString(t, "NORMAL")
	if got := waitServers(t, sock, 1); got[0] == pids[0] {
		t.Fatal("新しい server が起動していない")
	}
	b.write(t, ":q\r")
	if err := b.wait(t); err != nil {
		t.Fatalf("client の終了 = %v。出力: %q", err, tail(b.out.String()))
	}
	waitServerGone(t, sock)
}

// TestAutostartUnitErrorInPTY は、herdr の中なのにタブを引けないときは、1 つのプロセスで動かし、そのことをメッセージで知らせることを確かめる（基本設計 1.2）。
func TestAutostartUnitErrorInPTY(t *testing.T) {
	runtime := shortRuntimeDir(t)
	herdrSock := fakeHerdrPanes(t, map[string]string{})
	p := startEditorIn(t, t.TempDir(), herdrEnv(runtime, herdrSock, "wA:p9"))
	p.out.WaitForString(t, "1 つのプロセスで動かす")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, tail(p.out.String()))
	}
	if m, _ := filepath.Glob(filepath.Join(runtime, "vi-imitation", "u-*")); len(m) != 0 {
		t.Errorf("server のディレクトリができた: %q", m)
	}
}

// testTmuxServer は、テスト用に tmux -L で立てた別の tmux のサーバーである。
type testTmuxServer struct {
	bin, name string
	env       []string
}

// newTestTmuxServer は、テスト用の tmux のサーバーを立てる準備をし、テストの終わりに kill-server で消す。tmux が無ければ skip する。
// サーバーの環境（ペインに渡る）は env にする。
func newTestTmuxServer(t *testing.T, env []string) *testTmuxServer {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux が無い")
	}
	ts := &testTmuxServer{bin: bin, name: fmt.Sprintf("vi-imitation-autostart-test-%d-%d", os.Getpid(), time.Now().UnixNano()), env: env}
	t.Cleanup(func() {
		q := exec.Command(bin, "-L", ts.name, "display-message", "-p", "#{socket_path}") //nolint:gosec // テスト用の tmux
		q.Env = env
		sock, _ := q.Output()
		cmd := exec.Command(bin, "-L", ts.name, "kill-server") //nolint:gosec // テスト用の tmux
		cmd.Env = env
		_ = cmd.Run()
		// kill-server はソケットのファイルを残すことがあるので消す。
		if s := strings.TrimSpace(string(sock)); s != "" {
			_ = os.Remove(s)
		}
	})
	return ts
}

func (ts *testTmuxServer) run(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ts.bin, append([]string{"-L", ts.name, "-f", "/dev/null"}, args...)...) //nolint:gosec // テスト用の tmux
	cmd.Env = ts.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// tmuxEditor は、tmux のペインで起動したエディタの、標準エラー出力と終了コードを書くファイルである。
//
// エディタの終わりと終了コードは、ペインの画面からは読まない。
// tmux 3.4 では、書式の pane_dead_status が終わった直後のペインに空を返すことがあり、
// エディタの後にシェルが画面に出した行や remain-on-exit の「Pane is dead」の行が、capture-pane に出ないことがあった（2026-11-18）。
type tmuxEditor struct {
	errPath, exitPath string
}

// editorCommand は、エディタを files で起動し、標準エラー出力（終わりの誤りの 1 行）を e.errPath に、終了コードを e.exitPath に書くシェルのコマンドである。
// エディタは端末を標準入力と標準出力で使うので、標準エラー出力をファイルにしても画面は変わらない。
func (e tmuxEditor) command(files ...string) string {
	return shellQuoteArgs(append([]string{os.Args[0]}, files...)) + " 2>" + shellQuoteArgs([]string{e.errPath}) +
		`; echo "$?" >` + shellQuoteArgs([]string{e.exitPath + ".tmp"}) + " && mv " + shellQuoteArgs([]string{e.exitPath + ".tmp", e.exitPath})
}

func newTmuxEditor(t *testing.T, dir, name string) tmuxEditor {
	t.Helper()
	return tmuxEditor{errPath: filepath.Join(dir, name+".err"), exitPath: filepath.Join(dir, name+".exit")}
}

// exited は、エディタが終わっていれば true と終了コードを返す。
func (e tmuxEditor) exited() (bool, int) {
	b, err := os.ReadFile(e.exitPath)
	if err != nil {
		return false, 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return true, n
}

// wait は、エディタが終わるのを待ち、終了コードを返す。
func (e tmuxEditor) wait(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if done, n := e.exited(); done {
			return n
		}
		if time.Now().After(deadline) {
			t.Fatalf("エディタ（%s）が終わらない", e.exitPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stderr は、エディタが標準エラー出力に書いたものを返す。
func (e tmuxEditor) stderr() string {
	b, _ := os.ReadFile(e.errPath)
	return string(b)
}

// waitPane は、ペインの画面に want が出るまで待つ。折り返した行はつないで探す。
func (ts *testTmuxServer) waitPane(t *testing.T, pane, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		out := ts.run(t, "capture-pane", "-p", "-J", "-t", pane)
		if strings.Contains(out, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ペイン %s に %q が出ない。画面:\n%s", pane, want, out)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAutostartTmux は、tmux（tmux -L の別のサーバー）の中で、次を確かめる。
//
//   - 同じ window の 2 つのペインからほぼ同時に起動しても、server は 1 つになり、どちらも同じ server の編集の client になる（FR-62）。
//     どちらのペインにも、そのペインで打ったファイルが開く
//   - 別の client のタブのファイルを :e で開こうとすると、そのペインへフォーカスを移す（FR-63）
//   - 別の window では、別の server になる
//   - ペインを別の window へ動かした client は、起動したときの server に繋いだまま編集を続けられる（FR-112）
//   - 最後の編集の client を閉じると、server が終わり、ソケットのファイルを消す（FR-61）
//
// req: FR-59, FR-61, FR-62, FR-63, FR-112
func TestAutostartTmux(t *testing.T) {
	runtime := shortRuntimeDir(t)
	xdg := t.TempDir()
	env := append(muxFreeEnv(), envRunMain+"=1", "XDG_RUNTIME_DIR="+runtime,
		"XDG_CONFIG_HOME="+filepath.Join(xdg, "config"), "XDG_DATA_HOME="+filepath.Join(xdg, "data"))
	ts := newTestTmuxServer(t, env)
	dir := t.TempDir()
	pathA, pathB := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	// 同じ window の 2 つのペインで、ほぼ同時に起動する。最初のペインは、window を残すための待つだけのもの
	e1, e2, e3 := newTmuxEditor(t, dir, "e1"), newTmuxEditor(t, dir, "e2"), newTmuxEditor(t, dir, "e3")
	p0 := ts.run(t, "new-session", "-d", "-s", "t", "-x", "300", "-y", "50", "-P", "-F", "#{pane_id}", "sleep 600")
	pathC := filepath.Join(dir, "c.txt")
	p1 := ts.run(t, "split-window", "-d", "-t", p0, "-P", "-F", "#{pane_id}", e1.command(pathA))
	p2 := ts.run(t, "split-window", "-d", "-h", "-t", p0, "-P", "-F", "#{pane_id}", e2.command(pathC))
	unit1 := ts.run(t, "display-message", "-p", "-t", p0, "#{socket_path}:#{window_id}")
	sock1 := unitSocket(runtime, unit1)
	killServersAtCleanup(t, sock1)

	// どちらも同じ server の編集の client になり（FR-62）、どちらのペインにも、そのペインで打ったファイルが開く
	ts.waitPane(t, p1, "a.txt")
	ts.waitPane(t, p2, "c.txt")
	ts.waitPane(t, p1, "NORMAL")
	ts.waitPane(t, p2, "NORMAL")
	alive, aliveEditor, alivePath := p1, e1, pathA
	pids1 := waitServers(t, sock1, 1)
	for _, p := range []string{p1, p2} {
		if s := ts.run(t, "capture-pane", "-p", "-J", "-t", p); strings.Contains(s, "1 つのプロセス") {
			t.Errorf("ペイン %s が 1 つのプロセスで動いた。画面:\n%s", p, s)
		}
	}

	// p1 で、p2 のタブの c.txt を開こうとすると、開かずに p2 のペインへフォーカスを移す（FR-63）
	ts.run(t, "send-keys", "-t", p1, ":e "+pathC, "Enter")
	ts.waitPane(t, p1, "client")
	deadline := time.Now().Add(10 * time.Second)
	for ts.run(t, "display-message", "-p", "-t", p2, "#{pane_active}") != "1" {
		if time.Now().After(deadline) {
			t.Fatalf("p2 のペインへフォーカスが移らない。p1 の画面:\n%s", ts.run(t, "capture-pane", "-p", "-J", "-t", p1))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// p2 が :q で終わっても、server は p1 のために残る
	ts.run(t, "send-keys", "-t", p2, ":q", "Enter")
	if status := e2.wait(t); status != 0 {
		t.Errorf("2 つ目の client の終了コード = %d。標準エラー出力: %q", status, e2.stderr())
	}
	if got := serverPIDs(t, sock1); len(got) != 1 || got[0] != pids1[0] {
		t.Errorf("2 つ目の client の後の server = %v, want %v のまま", got, pids1)
	}

	// 別の window では、別の server になる
	// -t は「t:」（セッション t の、空いている次の番号）にする。「t」だけでは、tmux の版によって番号 0 の window を指し、
	// index 0 in use で失敗した（CI の run 44334802948）
	p3 := ts.run(t, "new-window", "-d", "-t", "t:", "-P", "-F", "#{pane_id}", e3.command(pathB))
	unit2 := ts.run(t, "display-message", "-p", "-t", p3, "#{socket_path}:#{window_id}")
	if unit2 == unit1 {
		t.Fatalf("別の window の単位 %q が同じ", unit2)
	}
	sock2 := unitSocket(runtime, unit2)
	killServersAtCleanup(t, sock2)
	ts.waitPane(t, p3, "b.txt")
	pids2 := waitServers(t, sock2, 1)
	if pids2[0] == pids1[0] {
		t.Fatal("別の window の server が同じプロセスである")
	}

	// 編集の client のペインを、別の server のある window へ動かしても、起動したときの server（a.txt を開いた方）に繋いだままである（FR-112）
	ts.run(t, "join-pane", "-d", "-s", alive, "-t", p3)
	if got := ts.run(t, "display-message", "-p", "-t", alive, "#{socket_path}:#{window_id}"); got != unit2 {
		t.Fatalf("動かした後のペインの単位 = %q, want %q（テストの前提）", got, unit2)
	}
	ts.run(t, "send-keys", "-t", alive, "imoved", "Escape")
	ts.waitPane(t, alive, "moved")
	ts.run(t, "send-keys", "-t", alive, ":wq", "Enter")
	if status := aliveEditor.wait(t); status != 0 {
		t.Errorf("動かした client の終了コード = %d, want 0。標準エラー出力: %q", status, aliveEditor.stderr())
	}
	if got, err := os.ReadFile(alivePath); err != nil || string(got) != "moved\n" {
		t.Errorf("%s = %q, %v; want %q（起動したときの server で保存する）", alivePath, got, err, "moved\n")
	}
	// 最初の window の server の最後の編集の client が終わったので、その server は終わる（FR-61）。別の window の server は動いたまま
	waitServerGone(t, sock1)
	if got := serverPIDs(t, sock2); len(got) != 1 || got[0] != pids2[0] {
		t.Errorf("別の window の server = %v, want %v のまま", got, pids2)
	}

	ts.run(t, "send-keys", "-t", p3, ":q", "Enter")
	if status := e3.wait(t); status != 0 {
		t.Errorf("別の window の client の終了コード = %d, want 0。標準エラー出力: %q", status, e3.stderr())
	}
	waitServerGone(t, sock2)
}

// shellQuoteArgs は、args を POSIX のシェルの単一引用で囲んでつなぐ（tmux はペインのコマンドをシェルで起動する）。
func shellQuoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
