package term

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	xterm "golang.org/x/term"
)

// このファイルは、端末を子プロセスへ渡して取り戻す操作である（基本設計 4.7、4.9。FR-79、FR-80、FR-117）。
//
// 渡す（Release）ときは、in の読み取りを止め、代替画面から戻り、raw モードを解く。子プロセスは同じ端末を読み書きする。
// 取り戻す（Reacquire）ときは、端末の前面のプロセスグループを自分へ移し、raw モードに入り、代替画面に入り、in の読み取りを再開する。
//
// # 前面のプロセスグループ
//
// :! の子プロセスは、Vim と同じく本体と同じプロセスグループで動かす（端末の <C-c> が子プロセスに届くように）。
// ただし、子プロセスが対話のシェル（:!sh）なら、シェルは自分のプロセスグループを作り、端末の前面をそれへ移す。
// その中で起動した modal-editor（待つだけの client。FR-83）のために端末を取り戻すとき（FR-117）、本体は背面のプロセスグループにいて、
// 端末の設定を変えると SIGTTOU で止まり、読むと SIGTTIN で止まる。
// そのため Reacquire は、前面が自分のプロセスグループでなければ、SIGTTOU を無視して前面を自分へ移し、元の前面を覚える。
// 次の Release で、覚えた前面へ返す（待つだけの client の終わりを待つ git などのプロセスグループ）。

// TTY は、端末の入力と出力のファイルを返す。子プロセスの標準入出力にする（Release の後）。
func (t *Terminal) TTY() (in, out *os.File) { return t.in, t.out }

// Release は、端末を子プロセスへ渡す（FR-80）。in の読み取りを止め（読みかけのキーは Input へ送り終える）、
// 代替画面から戻り（画面は代替画面に入る前のものに戻る）、raw モードを解く。
// Reacquire が前面のプロセスグループを移していれば、元の前面へ返す。
// Restore の後と、既に渡しているときは何もしない。渡している間の Draw と SetClipboard は何も書かない。
func (t *Terminal) Release() error {
	t.gate.pause()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.restored || t.away {
		return nil
	}
	var werr error
	if !t.noScreen {
		_, werr = t.out.WriteString(seqLeave)
	}
	rerr := xterm.Restore(t.inFd, t.state)
	t.away, t.noScreen = true, false
	if t.prevFg != 0 {
		setForeground(t.inFd, t.prevFg) // 返せなくても続ける。子プロセスが端末を読むときに分かる
		t.prevFg = 0
	}
	if rerr != nil {
		return fmt.Errorf("term: restore: %w", rerr)
	}
	if werr != nil {
		return fmt.Errorf("term: write: %w", werr)
	}
	return nil
}

// Reacquire は、Release で渡した端末を取り戻す。前面のプロセスグループが自分でなければ自分へ移し（ファイルの doc）、
// raw モードに入り、in の読み取りを再開する。
// screen が true なら代替画面にも入り、次の Draw で全体を書き直させる。
// false なら代替画面には入らず、子プロセスの出力を残したまま、キーを 1 つ読む（「続けるには ENTER」の問い）。
// この間の Draw は何も書かない。後で Reacquire(true) を呼ぶと代替画面に入る。
// Restore の後は何もしない。
func (t *Terminal) Reacquire(screen bool) error {
	t.mu.Lock()
	if t.restored {
		t.mu.Unlock()
		return nil
	}
	var err error
	if t.away {
		if pg := takeForeground(t.inFd); pg != 0 {
			t.prevFg = pg
		}
		if _, rerr := xterm.MakeRaw(t.inFd); rerr != nil {
			err = fmt.Errorf("term: make raw: %w", rerr)
		}
		t.away, t.noScreen = false, true
	}
	if screen && t.noScreen {
		if _, werr := t.out.WriteString(seqEnter); werr != nil && err == nil {
			err = fmt.Errorf("term: write: %w", werr)
		}
		t.painter.Invalidate()
		t.noScreen = false
	}
	t.mu.Unlock()
	t.gate.resume()
	return err
}

// HandedOver は、端末を子プロセスへ渡しているか（Release の後、Reacquire の前）を返す。
// 呼び出し側は、渡している間に端末から届いた SIGINT と SIGQUIT（子プロセスへの <C-c> と <C-\>）で終わらないために使う。
func (t *Terminal) HandedOver() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.away
}

// screenOn は、Draw と SetClipboard が端末へ書いてよいかを返す。呼び出し側が mu を持つ。
func (t *Terminal) screenOn() bool {
	return !t.restored && !t.suspended && !t.away && !t.noScreen
}

// takeForeground は、端末 fd の前面のプロセスグループが自分のものでなければ、自分のものへ移し、元の前面を返す。
// 移さなかったとき（もう自分が前面、端末を制御端末にしていない、移せない）は 0 を返す。
// 背面のプロセスグループから前面を移すと SIGTTOU が届くので、その間は SIGTTOU を無視する（tcsetpgrp(3) の決まり）。
func takeForeground(fd int) int {
	pg, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	own := unix.Getpgrp()
	if err != nil || pg == own || pg <= 0 {
		return 0
	}
	if !setForeground(fd, own) {
		return 0
	}
	return pg
}

// setForeground は、端末 fd の前面のプロセスグループを pg にする。移せたかを返す。
func setForeground(fd, pg int) bool {
	signal.Ignore(syscall.SIGTTOU)
	defer signal.Reset(syscall.SIGTTOU)
	return unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pg) == nil
}

// EnsureForeground は、端末の前面のプロセスグループを自分のものへ戻す。
// 子プロセス（対話のシェル）が前面を移したまま異常終了したときに、raw モードに入る前に呼ぶ（入れないと SIGTTOU で止まる）。
func (t *Terminal) EnsureForeground() {
	_ = takeForeground(t.inFd) // 移さなかったときは何もしない
}

func isAgain(err error) bool {
	return errors.Is(err, syscall.EAGAIN)
}

// readGate は、端末の読み取りの goroutine を、in が読めるまで poll(2) で待たせ、止めたり再開したりする仕組みである。
//
// read(2) で待つと、端末を子プロセスへ渡すときに、待っている読み取りを戻す手段が無い（標準入力は読み取りの期限を付けられない）。
// 戻せないと、子プロセスへ打った最初のキーをこちらが読んでしまう。
// poll(2) で in と起こすためのパイプの両方を待てば、パイプへ 1 バイト書くことで待ちを戻せる。
type readGate struct {
	// wakeR と wakeW は、起こすためのパイプである。wakeRfd は poll に渡す wakeR のファイル記述子である。
	wakeR, wakeW *os.File
	wakeRfd      int

	mu   sync.Mutex
	cond *sync.Cond
	// paused は、読み取りを止めるよう頼まれていることを、parked は読み取りの goroutine が止まっていることを表す。
	paused, parked bool
	// stopped は、Close で止められたことを、done は読み取りの goroutine が終わったことを表す。
	stopped, done bool
}

func (g *readGate) init() error {
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("term: pipe: %w", err)
	}
	fd, err := fdOf(r)
	if err != nil {
		_ = r.Close()
		_ = w.Close()
		return err
	}
	g.wakeR, g.wakeW, g.wakeRfd = r, w, fd
	g.cond = sync.NewCond(&g.mu)
	return nil
}

// close は、起こすためのパイプを閉じる。読み取りの goroutine を起こしていない（Open の途中の誤り）ときに使う。
func (g *readGate) close() {
	_ = g.wakeR.Close() // 後始末の誤りは扱えない
	_ = g.wakeW.Close()
}

// wake は、poll で待つ読み取りの goroutine を起こす。
func (g *readGate) wake() {
	_, _ = g.wakeW.Write([]byte{0}) // パイプが一杯なら、既に起こしてある
}

// wait は、fd が読めるようになるまで待ち、true を返す。止めるよう頼まれていれば、再開されるまで待つ。
// 止められた（stop）ら false を返す。
func (g *readGate) wait(fd int) bool {
	for {
		g.mu.Lock()
		for g.paused && !g.stopped {
			if !g.parked {
				g.parked = true
				g.cond.Broadcast()
			}
			g.cond.Wait()
		}
		g.parked = false
		stopped := g.stopped
		g.mu.Unlock()
		if stopped {
			return false
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}, {Fd: int32(g.wakeRfd), Events: unix.POLLIN}} //nolint:gosec // ファイル記述子は int32 に収まる
		_, err := unix.Poll(fds, -1)
		switch {
		case errors.Is(err, unix.EINTR):
			continue
		case err != nil:
			return true // poll を使えなければ、read の誤りに任せる
		}
		if fds[1].Revents != 0 {
			g.drain()
			continue // 止めるか終わるかを確かめ直す
		}
		if fds[0].Revents != 0 {
			return true
		}
	}
}

// drain は、起こすためのパイプに溜まったバイトを読み捨てる。
func (g *readGate) drain() {
	var b [64]byte
	for {
		n, err := unix.Read(g.wakeRfd, b[:])
		if n <= 0 || err != nil {
			return
		}
	}
}

// pause は、読み取りの goroutine を止め、止まるまで待つ。止まったときには、読みかけのバイト列は送り終えている。
func (g *readGate) pause() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.done {
		return
	}
	g.paused = true
	g.wake()
	for !g.parked && !g.done && !g.stopped {
		g.cond.Wait()
	}
}

// resume は、pause で止めた読み取りの goroutine を再開させる。
func (g *readGate) resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.paused = false
	g.cond.Broadcast()
}

// stop は、読み取りの goroutine を終わらせる（Close）。
func (g *readGate) stop() {
	g.mu.Lock()
	g.stopped = true
	g.cond.Broadcast()
	g.mu.Unlock()
	g.wake()
}

// exited は、読み取りの goroutine が終わるときに呼ぶ。pause の待ちを戻す。
func (g *readGate) exited() {
	g.mu.Lock()
	g.done = true
	g.cond.Broadcast()
	g.mu.Unlock()
}
