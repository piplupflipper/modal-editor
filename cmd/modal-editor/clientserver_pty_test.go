package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/server"
	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// serverProc は、-listen で端末を使わずに起動した server のプロセスである。
type serverProc struct {
	cmd  *exec.Cmd
	out  *bytes.Buffer
	done chan error
}

// startServer は、テストの実行ファイルを -listen sock で server として起動し、ソケットのファイルができるまで待つ。
// 利用者の設定と拡張機能を読まないように、XDG_CONFIG_HOME と XDG_DATA_HOME を空のディレクトリにする。
func startServer(t *testing.T, bin, sock string, files ...string) *serverProc {
	t.Helper()
	xdg := t.TempDir()
	cmd := exec.Command(bin, append([]string{"-listen", sock}, files...)...) //nolint:gosec // テストの実行ファイルか make build の本体を server として起動する
	cmd.Env = append(os.Environ(), envRunMain+"=1",
		"XDG_CONFIG_HOME="+filepath.Join(xdg, "config"), "XDG_DATA_HOME="+filepath.Join(xdg, "data"))
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	p := &serverProc{cmd: cmd, out: out, done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill() // 失敗したテストの後始末
			if err := <-p.done; err != nil {
				t.Logf("後始末で止めた server: %v", err)
			}
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			return p
		}
		select {
		case err := <-p.done:
			t.Fatalf("server が待ち受ける前に終わった: %v。出力: %q", err, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server のソケット %s ができない", sock)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *serverProc) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("server が終わらない")
	}
	return nil
}

// TestClientServerProcessesInPTY は、server（-listen）と編集の client（-connect）を別のプロセスで起動し、
// client の端末で打ったキーで server のバッファを編集して保存できることを確かめる。
// :wq で、server と client のどちらも正常に終わり、client は端末を戻す。
//
// req: FR-58
func TestClientServerProcessesInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	sock := socketPath(t)
	srv := startServer(t, os.Args[0], sock, path)

	p := startEditor(t, "-connect", sock)
	if p.cmd.Process.Pid == srv.cmd.Process.Pid {
		t.Fatal("server と client が同じプロセスである")
	}
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	p.write(t, "isplit\x1b")
	p.out.WaitForString(t, "NORMAL")
	p.write(t, ":wq\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("client の終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
	if err := srv.wait(t); err != nil {
		t.Fatalf("server の終了 = %v, want 正常終了。出力: %q", err, srv.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "split\n" {
		t.Errorf("保存した内容 = %q, want %q", got, "split\n")
	}
	if _, err := os.Lstat(sock); err == nil {
		t.Errorf("server の終了の後にソケット %s が残った", sock)
	}
	p.checkRestored(t, before)
}

// TestServerKilledInPTY は、server が SIGKILL で終わったら、client は端末を戻し、そのことを 1 行出して、0 でない終了コードで終わることを確かめる
// （PR #200 のレビューの推奨 2）。
func TestServerKilledInPTY(t *testing.T) {
	sock := socketPath(t)
	srv := startServer(t, os.Args[0], sock)
	p := startEditor(t, "-connect", sock)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "NORMAL")
	if err := srv.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := srv.wait(t); err == nil {
		t.Fatal("SIGKILL の server が正常終了になった")
	}
	err := p.wait(t)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() == 0 {
		t.Fatalf("client の終了 = %v, want 0 でない終了コード", err)
	}
	p.checkRestored(t, before)
	out := p.out.String()
	if i := strings.LastIndex(out, "\x1b[?1049l"); !strings.Contains(out[i:], "server との接続が切れた") {
		t.Errorf("端末を戻した後に、server の異常終了の知らせが無い: %q", out[i:])
	}
}

// TestLargeTerminalInPTY は、1 つのプロセスの構成でも分けた構成でも、2100 列の大きな端末で起動でき、
// さらに広げると新しい幅で描き直すことを確かめる（PR #200 の再レビューの必須 2）。
// ステータス行（反転の属性で、行の全体を 1 続きに書く）の長さで、描いた幅を見る。
func TestLargeTerminalInPTY(t *testing.T) {
	status := regexp.MustCompile(`NORMAL[^\x1b]*`)
	maxCol := func(out string) int {
		m := 0
		for _, s := range status.FindAllString(out, -1) {
			m = max(m, len(s))
		}
		return m
	}
	for _, split := range []bool{false, true} {
		t.Run(configName(split), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "a.txt")
			args := []string{path}
			var srv *serverProc
			if split {
				sock := socketPath(t)
				srv = startServer(t, os.Args[0], sock, path)
				args = []string{"-connect", sock}
			}
			p := startEditorSized(t, 2100, 20, t.TempDir(), nil, args...)
			p.out.WaitFor(t, "2000 列より右に描く", func(out string) bool {
				return strings.Contains(out, "NORMAL") && maxCol(out) > 2000
			})
			start := len(p.out.String())
			ptytest.SetSize(t, p.master, 2300, 30)
			p.out.WaitFor(t, "広げた後に 2200 列より右に描く", func(out string) bool {
				return maxCol(out[start:]) > 2200
			})
			p.write(t, ":q\r")
			if err := p.wait(t); err != nil {
				t.Fatalf("エディタの終了 = %v。出力の末尾: %q", err, tail(p.out.String()))
			}
			if srv != nil {
				if err := srv.wait(t); err != nil {
					t.Fatalf("server の終了 = %v。出力: %q", err, srv.out.String())
				}
			}
		})
	}
}

func tail(s string) string { return s[max(len(s)-300, 0):] }

// socketPath は、親も権限 0700 のディレクトリの下の、server のソケットのパスを返す（server.Listen は親も検査する）。
func socketPath(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "p")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(d, "srv", server.SocketName)
}

// TestClientKilledEndsServerInPTY は、編集の client が SIGKILL で終わって接続が切れたら、server も終わることを確かめる（基本設計 1.2）。
func TestClientKilledEndsServerInPTY(t *testing.T) {
	sock := socketPath(t)
	srv := startServer(t, os.Args[0], sock)
	p := startEditor(t, "-connect", sock)
	p.out.WaitForString(t, "NORMAL")
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = p.wait(t) // SIGKILL で終わった誤りが返る
	if err := srv.wait(t); err != nil {
		t.Fatalf("server の終了 = %v, want 正常終了。出力: %q", err, srv.out.String())
	}
}

// TestSingleProcessListensInPTY は、1 つのプロセスの構成でも、MCP のソケットと同じディレクトリで server のソケットを待ち受けることを確かめる（FR-60。基本設計 1.2）。
// 今は 2 つ目の client に応えないので、繋いだ接続はすぐ閉じられ、編集は続く。終了でディレクトリは消える。
func TestSingleProcessListensInPTY(t *testing.T) {
	runtime := t.TempDir()
	p := startEditorIn(t, t.TempDir(), []string{"XDG_RUNTIME_DIR=" + runtime})
	p.out.WaitForString(t, "NORMAL")
	matches, err := filepath.Glob(filepath.Join(runtime, "modal-editor", strconv.Itoa(p.cmd.Process.Pid)+"-*", server.SocketName))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("server のソケット = %q, want 1 つ", matches)
	}
	sock := matches[0]
	// MCP のソケットは、起動の契機 socket の拡張機能があるときだけ待ち受ける。ここでは拡張機能が無いので無い
	if _, err = os.Stat(filepath.Join(filepath.Dir(sock), "mcp.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("拡張機能が無いのに mcp.sock がある: %v", err)
	}
	c, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 80, Height: 24}); err != nil && !errors.Is(err, protocol.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := c.Recv(); !errors.Is(err, io.EOF) {
		t.Errorf("2 つ目の client の Recv = %v, want io.EOF", err)
	}
	_ = c.Close()
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
	if _, err := os.Lstat(filepath.Dir(sock)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("終了の後にソケットのディレクトリが残った: %v", err)
	}
}
