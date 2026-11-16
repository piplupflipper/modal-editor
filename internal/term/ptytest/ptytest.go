// Package ptytest は、偽の端末（pty）を使うテストの補助である。テストからだけ import する。
//
// 外部のライブラリ（github.com/creack/pty など）は使わず、Linux の /dev/ptmx を ioctl で開く。
// 要る操作（開く、大きさを変える、端末の設定を読む、出力を溜める）が少なく、依存を増やすほどではないためである。
package ptytest

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Open は、pty の組を開く。master はテストが端末の側として読み書きし、tty はエディタの側の端末である。
// 大きさを cols 列 rows 行にする。/dev/ptmx を開けない環境ではテストを飛ばす。
// どちらのファイルも、テストの終わりに閉じる。
func Open(t testing.TB, cols, rows uint16) (master, tty *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pty を開けない: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() }) // テストの後始末
	mfd := fd(t, m)
	if err = unix.IoctlSetPointerInt(mfd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("TIOCSPTLCK: %v", err)
	}
	n, err := unix.IoctlGetInt(mfd, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("TIOCGPTN: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("pts を開けない: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() }) // テストの後始末
	SetSize(t, m, cols, rows)
	return m, s
}

// fd は、f のファイル記述子を返す。os.File.Fd は f を blocking のモードにして読み取りの期限を効かなくするので使わない。
func fd(t testing.TB, f *os.File) int {
	t.Helper()
	rc, err := f.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rc.Control(func(p uintptr) { n = int(p) }); err != nil {
		t.Fatal(err)
	}
	return n
}

// SetSize は、端末の大きさを cols 列 rows 行にする。
// tty を制御端末にしたプロセスがあれば、カーネルがその前面のプロセスグループへ SIGWINCH を送る。
func SetSize(t testing.TB, f *os.File, cols, rows uint16) {
	t.Helper()
	if err := unix.IoctlSetWinsize(fd(t, f), unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatalf("TIOCSWINSZ: %v", err)
	}
}

// Termios は、端末の設定を返す。
func Termios(t testing.TB, f *os.File) unix.Termios {
	t.Helper()
	tio, err := unix.IoctlGetTermios(fd(t, f), unix.TCGETS)
	if err != nil {
		t.Fatalf("TCGETS: %v", err)
	}
	return *tio
}

// Capture は、master から読んだ出力を溜める。
type Capture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// StartCapture は、master を読み続けて出力を溜める goroutine を起こす。master が閉じるか読めなくなると終わる。
func StartCapture(master *os.File) *Capture {
	c := &Capture{}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := master.Read(b)
			c.mu.Lock()
			c.buf.Write(b[:n])
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return c
}

// String は、これまでに溜めた出力を返す。
func (c *Capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// WaitFor は、出力が cond を満たすまで待つ。5 秒で満たさなければテストを失敗させる。
func (c *Capture) WaitFor(t testing.TB, what string, cond func(out string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond(c.String()) {
		if time.Now().After(deadline) {
			t.Fatalf("出力が %s にならない。出力: %q", what, c.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// WaitForString は、出力に want が現れるまで待つ。
func (c *Capture) WaitForString(t testing.TB, want string) {
	t.Helper()
	c.WaitFor(t, fmt.Sprintf("%q を含む", want), func(out string) bool { return strings.Contains(out, want) })
}
