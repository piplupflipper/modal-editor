package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// LockName は、server のディレクトリ（基本設計 1.2 の `<名前>/`）の中のロックのファイル名である。
const LockName = "server.lock"

// ErrLocked は、ロックのファイルを他のプロセス（起動の途中か、終わりの途中の server）が持っていることを表す。
var ErrLocked = errors.New("server: another server holds the lock")

// Lock は、server のディレクトリ dir のロックのファイル（LockName）を、無ければ権限 0600 で作り、排他の flock を待たずに掛ける（基本設計 1.2）。
//
// 同じ window の 2 つのペインでほぼ同時に起動した client が、それぞれ server を起動しても、ロックを取れた 1 つだけが server になる。
// 他のプロセスがロックを持っていれば ErrLocked を返す。
// ロックは、返したファイルを閉じるか、プロセスが終わると外れる（SIGKILL で終わっても外れるので、古いロックは残らない）。
// ファイル自体は消さない（消すと、消す前に開いた別のプロセスと、消した後に作り直したプロセスが、別のファイルに同時にロックを掛けられる）。
//
// dir は PrepareDir で検査したものを渡す。ロックのファイルがシンボリックリンクなら開かない。
func Lock(dir string) (*os.File, error) {
	path := filepath.Join(dir, LockName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("server: lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close() // ロックを掛けられなかったファイルを閉じる。誤りは扱えない
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("server: lock: %w", err)
	}
	return f, nil
}
