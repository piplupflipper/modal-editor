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
	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
	"github.com/piplupflipper/modal-editor/internal/ui"
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
	master, tty := ptytest.Open(t, 80, 24)
	before := ptytest.Termios(t, tty)
	out := ptytest.StartCapture(master)

	// 続きを待つ時間は、負荷の高い CI でも「ESC [ と B を別々に書く」が時間内に収まるように長めにする。
	tm, err := Open(tty, tty, Options{EscTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	raw := ptytest.Termios(t, tty)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG) != 0 {
		t.Errorf("raw モードになっていない: lflag=%#x", raw.Lflag)
	}
	out.WaitForString(t, seqEnter)
	if w, h := tm.Size(); w != 80 || h != 24 {
		t.Errorf("Size() = %d, %d, want 80, 24", w, h)
	}

	write := func(s string) {
		t.Helper()
		if _, err := master.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	write("aあ\x1b[1;5A\x1b[15~\x1b[2~\x03")
	wantKeys(t, tm, "aあ<C-Up><F5><Insert><C-c>")

	// ESC の単独は、時間切れの後に <Esc> になる。
	write("\x1b")
	wantKeys(t, tm, "<Esc>")
	// ESC と続きが別々に届いても、時間内なら 1 つの列として読む。
	write("\x1b[")
	time.Sleep(2 * time.Millisecond)
	write("B")
	wantKeys(t, tm, "<Down>")

	write("\x1b[200~x\x1by\r\n\x1b[201~")
	if ev := nextEvent(t, tm); ev.Paste != "xy\n" {
		t.Errorf("貼り付けのイベント = %+v, want Paste %q", ev, "xy\n")
	}

	ptytest.SetSize(t, master, 100, 30)
	// テストのプロセスは pty の前面のプロセスグループではないので、SIGWINCH を自分へ送る。
	if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatal(err)
	}
	if ev := nextEvent(t, tm); !ev.Resize || ev.Width != 100 || ev.Height != 30 {
		t.Errorf("リサイズのイベント = %+v, want 100x30", ev)
	}

	r := &ui.Renderer{Width: 100, Height: 30}
	s := r.Render(&ui.Frame{Text: lines{"hello", "日本語"}, Mode: ui.ModeInsert})
	if err := tm.Draw(s); err != nil {
		t.Fatal(err)
	}
	out.WaitForString(t, "\x1b[?25h")
	v := newVT(100, 30)
	v.write(t, out.String())
	v.check(t, s)
	if !v.modes[1049] || !v.modes[2004] || v.modes[7] {
		t.Errorf("代替画面、bracketed paste、折り返しの止めのモードが違う: %v", v.modes)
	}

	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	if err := tm.Draw(s); err != nil {
		t.Errorf("Restore の後の Draw: %v", err)
	}
	if err := tm.Restore(); err != nil {
		t.Errorf("2 回目の Restore: %v", err)
	}
	after := ptytest.Termios(t, tty)
	if after != before {
		t.Errorf("端末の設定が戻っていない:\n before %+v\n after  %+v", before, after)
	}
	out.WaitForString(t, seqLeave)
	if o := out.String(); !strings.HasSuffix(o, seqLeave) {
		t.Errorf("出力の最後が seqLeave でない: %q", o[max(0, len(o)-64):])
	}
	o := out.String()
	v.write(t, o[strings.LastIndex(o, seqLeave):])
	if v.modes[1049] || v.modes[2004] || !v.modes[7] || !v.cursorVisible || v.shape != 0 {
		t.Errorf("Restore の後のモードが戻っていない: %v, カーソルの表示 %v, 形 %d", v.modes, v.cursorVisible, v.shape)
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

// TestSetClipboard は、SetClipboard がテキストを base64 にした OSC 52 の制御シーケンス（BEL で終わる）を書き、
// Restore の後は何も書かないことを確かめる（FR-56）。
//
// req: FR-56
func TestSetClipboard(t *testing.T) {
	if got, want := string(osc52("")), "\x1b]52;c;\a"; got != want {
		t.Errorf("osc52(\"\") = %q, want %q", got, want)
	}
	master, tty := ptytest.Open(t, 80, 24)
	out := ptytest.StartCapture(master)
	tm, err := Open(tty, tty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	out.WaitForString(t, seqEnter)

	// "日本語\n" の base64 は 5pel5pys6KqeCg== である。
	if err := tm.SetClipboard("日本語\n"); err != nil {
		t.Fatal(err)
	}
	const want = "\x1b]52;c;5pel5pys6KqeCg==\a"
	out.WaitForString(t, want)

	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	out.WaitForString(t, seqLeave)
	if err := tm.SetClipboard("after"); err != nil {
		t.Errorf("Restore の後の SetClipboard: %v", err)
	}
	// Restore の後に書いていないことは、出力の最後が seqLeave のままであることで確かめる。
	time.Sleep(50 * time.Millisecond)
	if o := out.String(); !strings.HasSuffix(o, seqLeave) || strings.Count(o, "\x1b]52;") != 1 {
		t.Errorf("Restore の後に書いた: %q", o[max(0, len(o)-64):])
	}
}

// TestRestoreOnPanic は、RestoreOnPanic を defer した関数が panic すると、端末を戻してから同じ値で panic を続けることを確かめる。
// 実際の goroutine で panic するとテストのプロセスが終わるので、同じ goroutine の関数で確かめる。
func TestRestoreOnPanic(t *testing.T) {
	master, tty := ptytest.Open(t, 80, 24)
	ptytest.StartCapture(master)
	before := ptytest.Termios(t, tty)
	tm, err := Open(tty, tty, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tm.Close() })
	func() {
		defer tm.RestoreOnPanic() // panic していなければ何もしない
	}()
	if ptytest.Termios(t, tty) == before {
		t.Fatal("panic していないのに端末が戻った")
	}
	var got any
	func() {
		defer func() { got = recover() }()
		func() {
			defer tm.RestoreOnPanic()
			panic("boom")
		}()
	}()
	if got != "boom" {
		t.Errorf("recover() = %v, want boom", got)
	}
	if ptytest.Termios(t, tty) != before {
		t.Error("panic の後に端末が戻っていない")
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
