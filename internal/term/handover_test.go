package term

import (
	"bytes"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// TestReleaseReacquire は、Release で端末を子プロセスへ渡すと、raw モードと代替画面を抜け、打ったキーを読まずに残し
// （子プロセスが読める）、Draw が何も書かないこと、Reacquire で raw モードと代替画面に戻り、キーをまた読むことを確かめる（FR-80）。
//
// req: FR-80
func TestReleaseReacquire(t *testing.T) {
	master, tty := ptytest.Open(t, 80, 24)
	before := ptytest.Termios(t, tty)
	out := ptytest.StartCapture(master)
	tm, err := Open(tty, tty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tm.Restore()
		_ = tm.Close()
	})
	out.WaitForString(t, seqEnter)
	write := func(s string) {
		t.Helper()
		if _, err := master.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	write("a")
	wantKeys(t, tm, "a")

	if err := tm.Release(); err != nil {
		t.Fatal(err)
	}
	if !tm.HandedOver() {
		t.Error("Release の後に HandedOver が false")
	}
	out.WaitForString(t, seqLeave)
	if got := ptytest.Termios(t, tty); got != before {
		t.Errorf("Release の後の端末の設定が元に戻っていない:\n before %+v\n after  %+v", before, got)
	}
	n := len(out.String())
	r := &ui.Renderer{Width: 80, Height: 24}
	if err := tm.Draw(r.Render(&ui.Frame{Text: lines{"x"}})); err != nil {
		t.Fatal(err)
	}

	// 渡している間に打ったキーは、エディタには届かず、子プロセスが端末から読める（ここではテストが tty から読む）。
	write("child\n")
	select {
	case ev := <-tm.Input():
		t.Fatalf("渡している間にイベントが届いた: %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
	got := readTTY(t, tty, "child\n")
	if got != "child\n" {
		t.Errorf("子プロセスが読んだもの = %q, want %q", got, "child\n")
	}
	if o := out.String(); len(o) > n && bytes.Contains([]byte(o[n:]), []byte("\x1b[")) {
		t.Errorf("渡している間に Draw が書いた: %q", o[n:])
	}

	// 問いの間（代替画面の外の raw モード）は、キーを読むが、Draw は書かない。
	if err := tm.Reacquire(false); err != nil {
		t.Fatal(err)
	}
	if raw := ptytest.Termios(t, tty); raw.Lflag&(unix.ICANON|unix.ECHO) != 0 {
		t.Errorf("Reacquire(false) の後に raw モードでない: lflag=%#x", raw.Lflag)
	}
	write("\r")
	wantKeys(t, tm, "<CR>")
	if err := tm.Reacquire(true); err != nil {
		t.Fatal(err)
	}
	if tm.HandedOver() {
		t.Error("Reacquire の後に HandedOver が true")
	}
	out.WaitFor(t, "代替画面に入り直す", func(o string) bool { return bytes.Count([]byte(o), []byte(seqEnter)) == 2 })
	write("b")
	wantKeys(t, tm, "b")
	// 取り戻した後の Draw は、全体を書き直す。
	if err := tm.Draw(r.Render(&ui.Frame{Text: lines{"hello"}})); err != nil {
		t.Fatal(err)
	}
	out.WaitForString(t, "hello")
}

// readTTY は、子プロセスの代わりに tty から want の長さだけ読む。端末は cooked のモードなので、行が揃ってから読める。
func readTTY(t *testing.T, tty *os.File, want string) string {
	t.Helper()
	if err := tty.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tty.SetReadDeadline(time.Time{}) }() // 読み終えたら期限を外す
	buf := make([]byte, len(want))
	var got []byte
	for len(got) < len(want) {
		n, err := tty.Read(buf[:len(want)-len(got)])
		got = append(got, buf[:n]...)
		if err != nil {
			t.Fatalf("tty から読めない（読んだもの %q）: %v", got, err)
		}
	}
	return string(got)
}
