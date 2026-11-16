package term

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// nextEvent は、Input から次のイベントを待って返す。
func nextEvent(t *testing.T, tm *Terminal) Event {
	t.Helper()
	select {
	case ev, ok := <-tm.Input():
		if !ok {
			t.Fatal("Input が閉じた")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("イベントが届かない")
	}
	return Event{}
}

func wantKeys(t *testing.T, tm *Terminal, want string) {
	t.Helper()
	var keys []editcmd.Key
	for range len(editcmd.MustParseKeys(want)) {
		ev := nextEvent(t, tm)
		if ev.Resize {
			t.Fatalf("キーの代わりにリサイズ %+v が届いた", ev)
		}
		keys = append(keys, ev.Key)
	}
	if got := editcmd.KeysString(keys); got != want {
		t.Errorf("キー = %q, want %q", got, want)
	}
}

// TestTerminalPTY は、偽の端末の上で、raw モードへの切り替えと復元、キーの解読、ESC の時間切れ、
// 貼り付け、リサイズ、Close で Input が閉じることを確かめる。
func TestTerminalPTY(t *testing.T) {
	master, tty := openPTY(t, 80, 24)
	before := getTermios(t, tty)
	out := startCapture(master)

	tm, err := Open(tty, tty, Options{EscTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	raw := getTermios(t, tty)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 {
		t.Errorf("raw モードになっていない: lflag=%#x", raw.Lflag)
	}
	out.waitFor(t, seqEnter)
	if w, h := tm.Size(); w != 80 || h != 24 {
		t.Errorf("Size() = %d, %d, want 80, 24", w, h)
	}

	write := func(s string) {
		t.Helper()
		if _, err := master.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	write("aあ\x1b[1;5A\x1b[15~\x03")
	wantKeys(t, tm, "aあ<C-Up><C-c>")

	// ESC の単独は、時間切れの後に <Esc> になる。
	write("\x1b")
	wantKeys(t, tm, "<Esc>")
	// ESC と続きが別々に届いても、時間内なら 1 つの列として読む。
	write("\x1b[")
	time.Sleep(2 * time.Millisecond)
	write("B")
	wantKeys(t, tm, "<Down>")

	write("\x1b[200~x\x1by\r\n\x1b[201~")
	wantKeys(t, tm, "xy<CR>")

	setWinsize(t, master, 100, 30)
	// テストのプロセスは pty の前面のプロセスグループではないので、SIGWINCH を自分へ送る。
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, tm); !ev.Resize || ev.Width != 100 || ev.Height != 30 {
		t.Errorf("リサイズのイベント = %+v, want 100x30", ev)
	}

	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Restore(); err != nil {
		t.Errorf("2 回目の Restore: %v", err)
	}
	after := getTermios(t, tty)
	if *after != *before {
		t.Errorf("端末の設定が戻っていない:\n before %+v\n after  %+v", before, after)
	}
	out.waitFor(t, seqLeave)
	if s := out.String(); !strings.HasSuffix(s, seqLeave) {
		t.Errorf("出力の最後が seqLeave でない: %q", s[max(0, len(s)-64):])
	}

	if err := tm.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-tm.Input():
		for ok {
			_, ok = <-tm.Input()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close の後も Input が閉じない")
	}
	select {
	case <-tm.readerDone:
	case <-time.After(5 * time.Second):
		t.Error("Close の後も読み取りの goroutine が残る")
	}
}

// TestOpenNotTerminal は、端末でないファイルでは ErrNotTerminal を返し、何も書かないことを確かめる。
func TestOpenNotTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close() // テストの後始末
		_ = w.Close() // テストの後始末
	})
	if _, err := Open(r, w, Options{}); !errors.Is(err, ErrNotTerminal) {
		t.Errorf("Open(pipe) = %v, want ErrNotTerminal", err)
	}
}
