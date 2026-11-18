package mux

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testTmux は、テスト用に tmux -L で立てた別の tmux のサーバーと、その最初のペインを指す Tmux である。
type testTmux struct {
	*Tmux
	session string
}

// newTestTmux は、テスト用の tmux のサーバーを立て、テストの終わりに kill-server で消す。
// tmux が無ければ skip する。利用者の tmux のサーバーには触らない。
func newTestTmux(t *testing.T) *testTmux {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux が無い")
	}
	name := fmt.Sprintf("modal-editor-mux-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	run := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, append([]string{"-L", name, "-f", "/dev/null"}, args...)...) //nolint:gosec // テスト用の tmux
		cmd.Env = tmuxFreeEnv()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("new-session", "-d", "-s", "mux", "-x", "200", "-y", "50", "sleep 600")
	info := run("display-message", "-p", "-t", "mux", "#{socket_path}\t#{pane_id}")
	socket, pane, _ := strings.Cut(info, "\t")
	t.Cleanup(func() {
		cmd := exec.Command(bin, "-L", name, "kill-server") //nolint:gosec // テスト用の tmux
		cmd.Env = tmuxFreeEnv()
		_ = cmd.Run()
		// kill-server はソケットのファイルを残すことがあるので消す。
		_ = os.Remove(socket)
	})
	return &testTmux{Tmux: &Tmux{Bin: bin, Socket: socket, Pane: pane}, session: "mux"}
}

// tmuxFreeEnv は、利用者の tmux の環境変数を除いた環境を返す。
func tmuxFreeEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// query は、テスト用の tmux のサーバーで display-message -p を打った結果を返す。
func (tt *testTmux) query(t *testing.T, target, format string) string {
	t.Helper()
	out, err := tt.run(testCtx(t), "display-message", "-p", "-t", target, format)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// waitFile は、path のファイルが空でなくなるまで待ち、中身を返す。
func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 && strings.HasSuffix(string(b), "\n") {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s ができない", path)
	return ""
}

// writeCommand は、環境変数 V と作業ディレクトリと自分のプロセス ID を out に書いてから眠るコマンドである。
func writeCommand(out string) []string {
	return []string{"sh", "-c", `printf '%s\n%s\n%s\n' "$V" "$(pwd)" "$$" > "$1.tmp" && mv "$1.tmp" "$1"; exec sleep 600`, "sh", out}
}

func TestTmuxDetectFromEnv(t *testing.T) {
	tt := newTestTmux(t)
	m := Detect(envOf(map[string]string{"TMUX": tt.Socket + ",1,0", "TMUX_PANE": tt.Pane}))
	if m.Kind() != KindTmux {
		t.Fatalf("Kind = %v, want tmux", m.Kind())
	}
	ctx := testCtx(t)
	pane, err := m.CurrentPane(ctx)
	if err != nil || pane != tt.Pane {
		t.Errorf("CurrentPane = %q, %v; want %q", pane, err, tt.Pane)
	}
	unit, err := m.Unit(ctx)
	want := tt.Socket + ":" + tt.query(t, tt.Pane, "#{session_id}:#{window_id}")
	if err != nil || unit != want {
		t.Errorf("Unit = %q, %v; want %q", unit, err, want)
	}
}

// TestTmuxUnitFollowsPaneNotActiveWindow は、アクティブな window が別でも、TMUX_PANE のペインの window を返すことを確かめる（基本設計 1.2）。
func TestTmuxUnitFollowsPaneNotActiveWindow(t *testing.T) {
	tt := newTestTmux(t)
	ctx := testCtx(t)
	if _, err := tt.run(ctx, "new-window", "-t", tt.session, "sleep 600"); err != nil {
		t.Fatal(err)
	}
	unit, err := tt.Unit(ctx)
	want := tt.Socket + ":" + tt.query(t, tt.Pane, "#{session_id}:#{window_id}")
	if err != nil || unit != want {
		t.Errorf("Unit = %q, %v; want %q", unit, err, want)
	}
	if active := tt.query(t, tt.session, "#{window_id}"); strings.HasSuffix(unit, ":"+active) {
		t.Errorf("Unit がアクティブな window %s を返した", active)
	}
}

func TestTmuxSplitPane(t *testing.T) {
	tt := newTestTmux(t)
	ctx := testCtx(t)
	out := filepath.Join(t.TempDir(), "out")
	dir := t.TempDir()
	pane, err := tt.SplitPane(ctx, SplitOptions{
		Direction: Right,
		Size:      30,
		Command:   Command{Args: writeCommand(out), Env: map[string]string{"V": "it's $V"}, Dir: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pane, "%") || pane == tt.Pane {
		t.Fatalf("SplitPane = %q, want a new pane id", pane)
	}
	lines := strings.Split(waitFile(t, out), "\n")
	if lines[0] != "it's $V" {
		t.Errorf("環境変数 V = %q, want %q", lines[0], "it's $V")
	}
	if got, _ := filepath.EvalSymlinks(lines[1]); got != mustEval(t, dir) {
		t.Errorf("作業ディレクトリ = %q, want %q", lines[1], dir)
	}
	if got := tt.query(t, pane, "#{window_id}"); got != tt.query(t, tt.Pane, "#{window_id}") {
		t.Errorf("新しいペインの window = %s, want 元のペインと同じ", got)
	}
	// Focus が false なので、元のペインがアクティブのまま。
	if got := tt.query(t, tt.Pane, "#{pane_active}"); got != "1" {
		t.Errorf("元のペインの pane_active = %s, want 1", got)
	}
	// 右に開き、幅は window の幅のおよそ 30%。
	left, _ := strconv.Atoi(tt.query(t, pane, "#{pane_left}"))
	width, _ := strconv.Atoi(tt.query(t, pane, "#{pane_width}"))
	if left == 0 || width < 50 || width > 70 {
		t.Errorf("新しいペインの左端 = %d、幅 = %d, want 右に幅 60 前後", left, width)
	}

	if err = tt.ClosePane(ctx, pane); err != nil {
		t.Fatal(err)
	}
	panes, err := tt.run(ctx, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	for p := range strings.FieldsSeq(panes) {
		if p == pane {
			t.Errorf("ClosePane の後もペイン %s がある", pane)
		}
	}
}

func TestTmuxSplitPaneDownWithFocus(t *testing.T) {
	tt := newTestTmux(t)
	ctx := testCtx(t)
	pane, err := tt.SplitPane(ctx, SplitOptions{Target: tt.Pane, Direction: Down, Focus: true, Command: Command{Args: []string{"sleep", "600"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tt.query(t, pane, "#{pane_active}"); got != "1" {
		t.Errorf("新しいペインの pane_active = %s, want 1", got)
	}
	if top, _ := strconv.Atoi(tt.query(t, pane, "#{pane_top}")); top == 0 {
		t.Error("新しいペインが下に開かなかった")
	}
}

func TestTmuxFocusPaneInAnotherWindow(t *testing.T) {
	tt := newTestTmux(t)
	ctx := testCtx(t)
	// 別の window のペインを作り、元の window をアクティブのままにする。
	other, err := tt.run(ctx, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", tt.session, "sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	other = strings.TrimSpace(other)
	if _, err := tt.run(ctx, "split-window", "-d", "-t", other, "sleep 600"); err != nil {
		t.Fatal(err)
	}
	if err := tt.FocusPane(ctx, other); err != nil {
		t.Fatal(err)
	}
	if got := tt.query(t, other, "#{window_active}#{pane_active}"); got != "11" {
		t.Errorf("window_active と pane_active = %s, want 11", got)
	}
}

func TestTmuxClosePaneUnknown(t *testing.T) {
	tt := newTestTmux(t)
	if err := tt.ClosePane(testCtx(t), "%999"); err == nil {
		t.Error("無いペインを閉じて誤りにならなかった")
	}
}

func TestTmuxPopupWithoutClient(t *testing.T) {
	tt := newTestTmux(t)
	err := tt.OpenPopup(testCtx(t), PopupOptions{Command: Command{Args: []string{"true"}}})
	if err == nil || !strings.Contains(err.Error(), "no client") {
		t.Errorf("tmux の client が無いのに err = %v", err)
	}
}

// attachClient は、script(1) で疑似端末を作り、その中でテスト用の tmux のサーバーへ client を繋ぐ。
// script が無ければ skip する。
func (tt *testTmux) attachClient(t *testing.T) {
	t.Helper()
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script が無い")
	}
	attach := shellJoin([]string{tt.Bin, "-S", tt.Socket, "attach", "-t", tt.session})
	cmd := exec.Command(script, "-qfec", attach, "/dev/null") //nolint:gosec // テスト用の tmux の client
	cmd.Env = append(tmuxFreeEnv(), "TERM=xterm-256color")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if out, err := tt.run(testCtx(t), "list-clients", "-F", "#{client_name}"); err == nil && strings.TrimSpace(out) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Skip("script の中の tmux の client が繋がらない")
}

func TestTmuxPopup(t *testing.T) {
	tt := newTestTmux(t)
	tt.attachClient(t)
	ctx := testCtx(t)
	out := filepath.Join(t.TempDir(), "out")
	dir := t.TempDir()
	err := tt.OpenPopup(ctx, PopupOptions{
		Width: 50, Height: 40,
		Command: Command{Args: writeCommand(out), Env: map[string]string{"V": "popup value"}, Dir: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(waitFile(t, out), "\n")
	if lines[0] != "popup value" {
		t.Errorf("環境変数 V = %q, want %q", lines[0], "popup value")
	}
	if got, _ := filepath.EvalSymlinks(lines[1]); got != mustEval(t, dir) {
		t.Errorf("作業ディレクトリ = %q, want %q", lines[1], dir)
	}
	pid, err := strconv.Atoi(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	if err := tt.ClosePopup(ctx, ""); err != nil {
		t.Fatal(err)
	}
	// ポップアップを閉じると、中のコマンドも終わる。
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("ClosePopup の後もポップアップの中のプロセス %d が残っている", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
