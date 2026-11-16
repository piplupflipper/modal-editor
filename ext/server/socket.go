package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/piplupflipper/modal-editor/ext"
)

// InheritedSocket は、本体が起動の契機 socket（ext.ActivationSocket）で引き継いだ、待ち受けているソケットと、
// 本体が受け付けて渡した接続を返す（基本設計 6.2）。
//
// 環境変数 ext.EnvSocketFDs が空か無ければ、何も引き継いでいないので、ln も conns も nil で、err も nil である。
// 引き継いだファイル記述子（ext.SocketFDStart から）は、net.FileListener と net.FileConn で複製してから閉じる。
// 拡張機能がさらに起動する子プロセスへ番号を誤って渡さないように、環境変数 ext.EnvSocketFDs も消す。
// このため、2 回目以降の呼び出しは何も返さない。
//
// 接続は、本体が最初に受け付けたもの（拡張機能を起動した契機）と、拡張機能の再起動を待つ間に受け付けたものである。
// それより後の接続は、拡張機能が ln で受け付ける。
func InheritedSocket() (ln net.Listener, conns []net.Conn, err error) {
	v := os.Getenv(ext.EnvSocketFDs)
	if v == "" {
		return nil, nil, nil
	}
	_ = os.Unsetenv(ext.EnvSocketFDs)
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return nil, nil, fmt.Errorf("server: invalid %s %q", ext.EnvSocketFDs, v)
	}
	return inheritFDs(ext.SocketFDStart, n)
}

// inheritFDs は、ファイル記述子 start から n 個を、待ち受け 1 つと接続 n-1 本として受け取る。
// 受け取れなかったものは閉じ、誤りをまとめて返す。受け取れたものは返す。
func inheritFDs(start, n int) (ln net.Listener, conns []net.Conn, err error) {
	var errs []error
	for i := range n {
		fd := start + i
		f := os.NewFile(uintptr(fd), "inherited-socket-"+strconv.Itoa(fd))
		if f == nil {
			errs = append(errs, fmt.Errorf("server: inherited fd %d is invalid", fd))
			continue
		}
		if i == 0 {
			l, lerr := net.FileListener(f)
			if lerr != nil {
				errs = append(errs, fmt.Errorf("server: inherited listener (fd %d): %w", fd, lerr))
			}
			ln = l
		} else {
			c, cerr := net.FileConn(f)
			if cerr != nil {
				errs = append(errs, fmt.Errorf("server: inherited connection (fd %d): %w", fd, cerr))
			} else {
				conns = append(conns, c)
			}
		}
		_ = f.Close()
	}
	return ln, conns, errors.Join(errs...)
}
