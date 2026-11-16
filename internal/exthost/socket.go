package exthost

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// acceptRetryDelay は、Accept が一時的な誤り（ファイル記述子の不足など）で失敗したときに、次に受け付けるまで待つ時間である。
const acceptRetryDelay = 100 * time.Millisecond

// socketActivator は、起動の契機 socket（ext.ActivationSocket）のために本体が待ち受けるソケットである（基本設計 6.2）。
//
// 本体は、[Config.SocketPath] で起動時から待ち受け、拡張機能 e が次の状態の間だけ接続を受け付ける（acceptLoop）。
//
//   - 未起動: 最初の接続で e を起動し、待ち受けと受け付けた接続を子プロセスへ引き継ぐ
//   - 再起動待ち: 受け付けた接続を pending に溜め、再起動の待ち時間が過ぎたら、次の起動で引き継ぐ。
//     異常で終わった後は、待ち時間が過ぎても接続が来るまで起動しない（supervise）
//   - 無効: 受け付けてすぐに閉じる。中継コマンドは接続が閉じたことで終わる
//
// 起動中、稼働中、停止中は受け付けない。待ち受けは子プロセスと共有しているので、その間の接続は子プロセスが受け付ける。
// 子プロセスを起動する前に、本体は Accept を止め（takeSocketFiles）、本体と子プロセスが同じ接続を取り合わないようにする。
//
// フィールドは Host.mu が守る。
type socketActivator struct {
	e    *extension
	path string
	ln   *net.UnixListener
	// file は、子プロセスへ引き継ぐ待ち受けのファイル記述子である。ln とは別の記述子で、同じソケットを指す。
	// 非ブロックでない記述子から os.NewFile で作るので、os/exec が Fd で取り出してもブロックの設定を変えない（listenUnix）。
	file *os.File
	// idle は、acceptLoop が Accept を呼んでいる間だけ nil でなく、Accept が戻ると閉じる。
	idle chan struct{}
	// pending は、受け付けて、次の起動で引き継ぐ接続である。
	pending []*os.File
	// arrived は、再起動待ちの間に接続を受け付けたか、kick を立てたことを、supervise へ知らせる。
	arrived chan struct{}
	// waiting は、異常の後の再起動の待ち時間が過ぎ、supervise が接続を待っている間 true である（waitConnectionLocked）。
	waiting bool
	// kick は、再起動待ちの間に、利用者の起動し直し（[Host.Restart]）か問い合わせ（コマンドの呼び出し）があったことを表す。
	// 立っていれば、supervise は接続を待たずに起動する（基本設計 6.2）。
	kick bool
	// done は、acceptLoop が戻ると閉じる。
	done chan struct{}
}

// socketExtension は、specs のうち起動の契機 socket を宣言した最初の拡張機能を返す。無ければ nil。
// 2 つ目以降は記録して、この契機では起動しない。
func (h *Host) socketExtension() *extension {
	var found *extension
	for _, e := range h.order {
		for _, a := range e.spec.Manifest.KnownActivation() {
			if a.On != ext.ActivationSocket {
				continue
			}
			if found == nil {
				found = e
			} else {
				h.log.Warn("only one extension can use the socket activation; ignored", "extension", e.spec.Manifest.ID, "used_by", found.spec.Manifest.ID)
			}
			break
		}
	}
	return found
}

// startSocket は、起動の契機 socket の拡張機能があれば、[Config.SocketPath] で待ち受けを始める。
// 待ち受けられなければ記録だけし、その拡張機能はこの契機では起動しない（他の契機と問い合わせでは起動する）。
func (h *Host) startSocket() {
	e := h.socketExtension()
	if e == nil {
		return
	}
	id := e.spec.Manifest.ID
	if h.cfg.SocketPath == "" {
		h.log.Warn("no socket path for the socket activation", "extension", id)
		return
	}
	ln, file, err := listenUnix(h.cfg.SocketPath)
	if err != nil {
		h.log.Warn("cannot listen for the socket activation", "extension", id, "error", err)
		return
	}
	s := &socketActivator{e: e, path: h.cfg.SocketPath, ln: ln, file: file, arrived: make(chan struct{}, 1), done: make(chan struct{})}
	h.sock = s
	go h.acceptLoop(s)
}

// listenUnix は、path に Unix ドメインソケットを作って待ち受け、本体が使う待ち受けと、子プロセスへ渡すファイルを返す。
//
// path に前回の起動で残ったソケットがあり、接続を受け付けるものがいなければ消してから作る。
// 他のプロセスが待ち受けているソケットと、ソケットでないファイルは消さずに誤りを返す。
// ソケットのファイルは所有者だけが読み書きできるようにする（接続した相手はバッファを読み書きできる）。
// [NewMCPSocket] のディレクトリは権限 0700 なので、権限を変えるまでの間も他の利用者は接続できない。
//
// ソケットは syscall で作り、ブロックする記述子のまま os.NewFile で包む。
// net.Listener の File で得たファイルは、os/exec が子プロセスへ渡すときにブロックする設定へ変え（os.File.Fd）、
// 設定はソケットを共有する本体の待ち受けにも及ぶので、本体の Accept が期限で止まらなくなるためである。
// 本体の待ち受け（ln）は net.FileListener が複製した記述子で、非ブロックの設定は net が行う。
func listenUnix(path string) (ln *net.UnixListener, file *os.File, err error) {
	if fi, serr := os.Lstat(path); serr == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, nil, fmt.Errorf("exthost: socket: %s exists and is not a socket", path)
		}
		if c, derr := net.DialTimeout("unix", path, time.Second); derr == nil {
			_ = c.Close()
			return nil, nil, fmt.Errorf("exthost: socket: another process is listening on %s", path)
		}
		if rerr := os.Remove(path); rerr != nil {
			return nil, nil, fmt.Errorf("exthost: socket: %w", rerr)
		}
	}
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, nil, fmt.Errorf("exthost: socket: %w", err)
	}
	file = os.NewFile(uintptr(fd), path)
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	if err = syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		return nil, nil, fmt.Errorf("exthost: socket: bind %s: %w", path, err)
	}
	if err = os.Chmod(path, 0o600); err != nil {
		_ = os.Remove(path)
		return nil, nil, fmt.Errorf("exthost: socket: %w", err)
	}
	if err = syscall.Listen(fd, syscall.SOMAXCONN); err != nil {
		_ = os.Remove(path)
		return nil, nil, fmt.Errorf("exthost: socket: listen %s: %w", path, err)
	}
	l, err := net.FileListener(file)
	if err != nil {
		_ = os.Remove(path)
		return nil, nil, fmt.Errorf("exthost: socket: %w", err)
	}
	ul, ok := l.(*net.UnixListener)
	if !ok {
		_ = l.Close()
		_ = os.Remove(path)
		err = fmt.Errorf("exthost: socket: unexpected listener %T", l)
		return nil, nil, err
	}
	return ul, file, nil
}

// acceptsLocked は、本体が接続を受け付ける状態かどうかを返す（socketActivator の doc）。
func (s *socketActivator) acceptsLocked() bool {
	switch s.e.state {
	case StateNotStarted, StateRestartWait, StateDisabled:
		return true
	case StateStarting, StateRunning, StateStopping:
	}
	return false
}

// acceptLoop は、拡張機能が接続を受け付ける状態の間、接続を受け付ける。Host が閉じると戻る。
//
// req: FR-14
func (h *Host) acceptLoop(s *socketActivator) {
	defer close(s.done)
	for {
		h.mu.Lock()
		for !h.closing && !s.acceptsLocked() {
			changed := s.e.changed
			h.mu.Unlock()
			select {
			case <-changed:
			case <-h.ctx.Done():
			}
			h.mu.Lock()
		}
		if h.closing {
			h.mu.Unlock()
			return
		}
		// 期限を外してから Accept を呼ぶ。takeSocketFiles は同じロックの中で期限を過去にして Accept を止める。
		_ = s.ln.SetDeadline(time.Time{})
		idle := make(chan struct{})
		s.idle = idle
		h.mu.Unlock()

		c, err := s.ln.AcceptUnix()

		h.mu.Lock()
		s.idle = nil
		close(idle)
		if err != nil {
			h.mu.Unlock()
			if errors.Is(err, net.ErrClosed) {
				return
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			h.log.Warn("socket accept failed", "error", err)
			t := time.NewTimer(acceptRetryDelay)
			select {
			case <-t.C:
			case <-h.ctx.Done():
				t.Stop()
			}
			continue
		}
		h.acceptedLocked(s, c)
		h.mu.Unlock()
	}
}

// acceptedLocked は、受け付けた接続 c を、拡張機能の状態に応じて引き継ぐか閉じる。
func (h *Host) acceptedLocked(s *socketActivator, c *net.UnixConn) {
	e := s.e
	id := e.spec.Manifest.ID
	if h.closing || e.state == StateDisabled {
		h.log.Info("socket connection closed: extension is disabled", "extension", id)
		_ = c.Close()
		return
	}
	f, err := c.File()
	_ = c.Close()
	if err != nil {
		h.log.Warn("socket connection dropped", "extension", id, "error", err)
		return
	}
	switch e.state {
	case StateNotStarted:
		s.pending = append(s.pending, f)
		h.log.Info("socket connection activates extension", "extension", id)
		h.startLocked(e)
	case StateRestartWait:
		s.pending = append(s.pending, f)
		select {
		case s.arrived <- struct{}{}:
		default:
		}
	case StateStarting:
		// Accept が接続を返した後、ロックを取るまでの間に、他の契機か問い合わせで起動が始まった。
		// runOnce は takeSocketFiles で Accept が戻るのを待ってから pending を取るので、この接続も引き継がれる。
		s.pending = append(s.pending, f)
	case StateRunning, StateStopping, StateDisabled:
		// takeSocketFiles が Accept を止めてから子プロセスを起動するので、ここへは来ない。
		_ = f.Close()
	}
}

// takeSocketFiles は、拡張機能 e が起動の契機 socket の拡張機能なら、本体の Accept を止め、
// 子プロセスへ引き継ぐファイル（待ち受けと、受け付けた接続）を返す。それ以外の拡張機能には nil を返す。
// 返した接続のファイルは、呼び出し側が子プロセスを起動した後に閉じる（待ち受けのファイルは Host が持ち続ける）。
// e の状態は起動中なので、Accept が戻った後の acceptLoop は、次に受け付けられる状態になるまで Accept を呼ばない。
func (h *Host) takeSocketFiles(e *extension) (listener *os.File, conns []*os.File) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sock
	if s == nil || s.e != e {
		return nil, nil
	}
	for s.idle != nil {
		_ = s.ln.SetDeadline(time.Now())
		idle := s.idle
		h.mu.Unlock()
		<-idle
		h.mu.Lock()
	}
	conns, s.pending = s.pending, nil
	s.kick = false
	select {
	case <-s.arrived:
	default:
	}
	return s.file, conns
}

// waitConnectionLocked は、起動の契機 socket の拡張機能 e が異常で終わった後、再起動の前に、接続が来るまで待つ（基本設計 6.2）。
// 接続が来たか、利用者の起動し直しか問い合わせがあった（kickLocked）か、e が socket の拡張機能でないか、待ち受けていなければ true を返す。
// stop が閉じるか Host が閉じたら false を返す。Host.mu を持って呼び、持ったまま戻る。
func (h *Host) waitConnectionLocked(e *extension, stop <-chan struct{}) bool {
	s := h.sock
	if s == nil || s.e != e {
		return true
	}
	s.waiting = true
	defer func() {
		s.waiting = false
		s.kick = false
	}()
	for len(s.pending) == 0 && !s.kick {
		h.mu.Unlock()
		select {
		case <-s.arrived:
		case <-stop:
			h.mu.Lock()
			return false
		case <-h.ctx.Done():
			h.mu.Lock()
			return false
		}
		h.mu.Lock()
	}
	return true
}

// kickLocked は、起動の契機 socket の拡張機能 e が再起動待ちのとき、接続を待たずに起動するよう supervise へ知らせる。
// 再起動の待ち時間の中なら、待ち時間が過ぎた時点で起動する。ただし onlyWaiting なら、待ち時間の中では何もしない。
// supervise が接続を待っている（待ち時間が過ぎた）なら true を返す。e が socket の拡張機能でなければ何もせず false を返す。
func (h *Host) kickLocked(e *extension, onlyWaiting bool) bool {
	s := h.sock
	if s == nil || s.e != e || e.state != StateRestartWait || (onlyWaiting && !s.waiting) {
		return false
	}
	s.kick = true
	select {
	case s.arrived <- struct{}{}:
	default:
	}
	return s.waiting
}

// kickedLocked は、e が kickLocked で起動を待たされている（再起動待ちだが、まもなく起動する）かどうかを返す。
func (h *Host) kickedLocked(e *extension) bool {
	s := h.sock
	return s != nil && s.e == e && e.state == StateRestartWait && s.kick
}

// dropPendingLocked は、引き継ぐ前の接続を閉じる。拡張機能を無効にしたときと Host を閉じるときに呼ぶ。
func (h *Host) dropPendingLocked(e *extension) {
	s := h.sock
	if s == nil || s.e != e {
		return
	}
	for _, f := range s.pending {
		_ = f.Close()
	}
	s.pending = nil
	s.kick = false
}

// closeSocket は、待ち受けをやめ、ソケットのファイルを消す。拡張機能のプロセスがすべて終わった後に呼ぶ。
func (h *Host) closeSocket() {
	h.mu.Lock()
	s := h.sock
	if s == nil {
		h.mu.Unlock()
		return
	}
	_ = s.ln.Close()
	h.dropPendingLocked(s.e)
	h.mu.Unlock()
	<-s.done
	_ = s.file.Close()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		h.log.Warn("cannot remove socket", "path", s.path, "error", err)
	}
}

// socketEnv は、子プロセスの環境に足す ext.EnvSocketFDs の要素を返す。
// 引き継ぐファイルが無ければ空の値にして、本体の環境の古い値を引き継がない。
func socketEnv(n int) string {
	if n == 0 {
		return ext.EnvSocketFDs + "="
	}
	return ext.EnvSocketFDs + "=" + strconv.Itoa(n)
}
