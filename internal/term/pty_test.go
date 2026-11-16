package term

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPTY は、偽の端末（pty）の組を開く。master はテストが端末の側として読み書きし、tty はエディタの側の端末である。
// /dev/ptmx が無い環境ではテストを飛ばす。
// 外部のライブラリ（creack/pty など）は使わず、Linux の ioctl で開く。
func openPTY(t *testing.T, cols, rows uint16) (master, tty *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("pty を開けない: %v", err)
	}
	mfd, err := fdOf(m)
	if err != nil {
		t.Fatal(err)
	}
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
	setWinsize(t, m, cols, rows)
	t.Cleanup(func() {
		_ = s.Close() // テストの後始末
		_ = m.Close() // テストの後始末
	})
	return m, s
}

func setWinsize(t *testing.T, f *os.File, cols, rows uint16) {
	t.Helper()
	fd, err := fdOf(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols}); err != nil {
		t.Fatalf("TIOCSWINSZ: %v", err)
	}
}

func getTermios(t *testing.T, f *os.File) *unix.Termios {
	t.Helper()
	fd, err := fdOf(f)
	if err != nil {
		t.Fatal(err)
	}
	tio, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatalf("TCGETS: %v", err)
	}
	return tio
}

// capture は、master から読んだ出力を溜める。
type capture struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func startCapture(m *os.File) *capture {
	c := &capture{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		b := make([]byte, 4096)
		for {
			n, err := m.Read(b)
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

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// waitFor は、出力に want が現れるまで待つ。
func (c *capture) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !bytes.Contains([]byte(c.String()), []byte(want)) {
		if time.Now().After(deadline) {
			t.Fatalf("出力に %q が現れない。出力: %q", want, c.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
