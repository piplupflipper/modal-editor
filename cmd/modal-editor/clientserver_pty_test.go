package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
func startServer(t *testing.T, bin, sock string, env []string, files ...string) *serverProc {
	t.Helper()
	xdg := t.TempDir()
	cmd := exec.Command(bin, append([]string{"-listen", sock}, files...)...) //nolint:gosec // テストの実行ファイルか make build の本体を server として起動する
	cmd.Env = append(os.Environ(), envRunMain+"=1",
		"XDG_CONFIG_HOME="+filepath.Join(xdg, "config"), "XDG_DATA_HOME="+filepath.Join(xdg, "data"))
	cmd.Env = append(cmd.Env, env...)
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
	sock := filepath.Join(t.TempDir(), "srv", server.SocketName)
	srv := startServer(t, os.Args[0], sock, nil, path)

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

// TestClientKilledEndsServerInPTY は、編集の client が SIGKILL で終わって接続が切れたら、server も終わることを確かめる（基本設計 1.2）。
func TestClientKilledEndsServerInPTY(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "srv", server.SocketName)
	srv := startServer(t, os.Args[0], sock, nil)
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
