package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// envRunMain は、テストの実行ファイルを、エディタ本体として起動させる環境変数である。
// 偽の端末の結合のテストは、テストの実行ファイル自身を子プロセスとして起動し、main を動かす（go build を待たないため）。
const envRunMain = "MODAL_EDITOR_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(envRunMain) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// editorProc は、偽の端末の上で起動したエディタのプロセスである。
type editorProc struct {
	cmd    *exec.Cmd
	master *os.File
	tty    *os.File
	out    *ptytest.Capture
	done   chan error
}

// startEditor は、偽の端末（80 列 24 行）を制御端末にして、エディタを args で起動する。
func startEditor(t *testing.T, args ...string) *editorProc {
	t.Helper()
	master, tty := ptytest.Open(t, 80, 24)
	cmd := exec.Command(os.Args[0], args...) //nolint:gosec // テストの実行ファイル自身をエディタとして起動する
	cmd.Env = append(os.Environ(), envRunMain+"=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// 新しいセッションを作り、tty を制御端末にする。端末の大きさを変えると、カーネルがエディタへ SIGWINCH を送る。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	p := &editorProc{cmd: cmd, master: master, tty: tty, out: ptytest.StartCapture(master), done: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill() // 失敗したテストの後始末
			if err := <-p.done; err != nil {
				t.Logf("後始末で止めたエディタ: %v", err)
			}
		}
	})
	return p
}

func (p *editorProc) write(t *testing.T, s string) {
	t.Helper()
	if _, err := p.master.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// wait は、エディタが終わるのを待ち、終了の誤り（終了コードが 0 なら nil）を返す。
func (p *editorProc) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-p.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("エディタが終わらない。出力: %q", p.out.String())
	}
	return nil
}

// checkRestored は、エディタの終了の後に、端末の設定が起動の前と同じで、代替画面から戻る列が出たことを確かめる。
func (p *editorProc) checkRestored(t *testing.T, before unix.Termios) {
	t.Helper()
	if after := ptytest.Termios(t, p.tty); after != before {
		t.Errorf("端末の設定が戻っていない:\n before %+v\n after  %+v", before, after)
	}
	p.out.WaitForString(t, "\x1b[?1049l")
	out := p.out.String()
	if i, j := strings.LastIndex(out, "\x1b[?1049h"), strings.LastIndex(out, "\x1b[?1049l"); j < i {
		t.Errorf("代替画面から戻る列が、切り替える列より前にある: %q", out)
	}
	for _, seq := range []string{"\x1b[?2004l", "\x1b[?25h", "\x1b[?7h", "\x1b[0 q"} {
		if !strings.Contains(out, seq) {
			t.Errorf("出力に %q が無い", seq)
		}
	}
}

// TestEditInPTY は、偽の端末の上で、起動、文字の入力、:wq での保存と終了、終了の後に端末が元に戻ることを確かめる。
func TestEditInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	p := startEditor(t, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")

	p.write(t, "ihello あ")
	p.out.WaitForString(t, "INSERT")
	// ESC の単独は、続きを待つ時間（50 ms）の後に Esc になる。:wq を続けて送ると <A-:> と読まれるので、間を空ける。
	p.write(t, "\x1b")
	p.out.WaitFor(t, "ノーマルモードの表示", func(out string) bool {
		return strings.Contains(out[strings.LastIndex(out, "INSERT"):], "NORMAL")
	})
	p.write(t, ":wq\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello あ\n" {
		t.Errorf("保存した内容 = %q, want %q", got, "hello あ\n")
	}
	p.checkRestored(t, before)
}

// TestEditResizeInPTY は、端末の大きさを変えると、新しい大きさで画面を描き直すことを確かめる。
func TestEditResizeInPTY(t *testing.T) {
	p := startEditor(t)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "[No Name]")
	ptytest.SetSize(t, p.master, 40, 10)
	// 大きさが変わると、画面を消して全体を書き直す。ステータス行は 9 行目（CUP の 9;1H）に来る。
	p.out.WaitFor(t, "10 行の画面の描き直し", func(out string) bool {
		i := strings.LastIndex(out, "\x1b[2J")
		return strings.Count(out, "\x1b[2J") >= 2 && strings.Contains(out[i:], "\x1b[9;1H")
	})
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v", err)
	}
	p.checkRestored(t, before)
}

// TestSignalRestoresTerminal は、SIGTERM と SIGHUP で終わるときも端末を元に戻すことを確かめる。
func TestSignalRestoresTerminal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			p := startEditor(t)
			before := ptytest.Termios(t, p.tty)
			p.out.WaitForString(t, "[No Name]")
			if err := p.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if err := p.wait(t); err == nil {
				t.Error("シグナルで終わったのに終了コードが 0")
			}
			p.checkRestored(t, before)
			if !strings.Contains(p.out.String(), "シグナルを受けて終了した") {
				t.Errorf("終了の理由が出ていない: %q", p.out.String())
			}
		})
	}
}
