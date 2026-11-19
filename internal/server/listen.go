package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// SocketName は、server のディレクトリ（基本設計 1.2 の `<名前>/`）の中の server のソケットのファイル名である。
const SocketName = "server.sock"

// maxSocketPath は、Unix ドメインソケットのパスのバイト数の上限である（internal/exthost の MCP のソケットと同じ）。
// sockaddr_un の sun_path は Linux で 108 バイト、macOS で 104 バイトで、末尾の NUL の分を引いた短い方にする。
const maxSocketPath = 103

// staleDialTimeout は、既にあるソケットのファイルが生きた server のものかを確かめるために繋ぐ時間の上限である。
const staleDialTimeout = 500 * time.Millisecond

// Listen は、path で server のソケットを待ち受ける（基本設計 1.2）。
//
// path を置くディレクトリ（基本設計 1.2 の `<名前>/`）とその親（`$XDG_RUNTIME_DIR/vi-imitation/` か `vi-imitation-<利用者 ID>/`）は、
// 無ければ権限 0700 で作る。
// どちらも、シンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、他の利用者の権限が無いことを確かめ、満たさなければ待ち受けない。
// 親を他の利用者が書けると、検査の後に `<名前>/` を入れ替えられるためである。
// path に繋げない古いソケット（server が SIGKILL で終わった後に残ったもの）があれば消してから待ち受け、繋げるソケットがあれば誤りにする。
// 返す net.Listener は、Close でソケットのファイルを消す。
func Listen(path string) (net.Listener, error) {
	return listen(path, true)
}

// ListenInOwnDir は、呼び出し側が os.MkdirTemp で排他的に作ったディレクトリ（権限 0700）の中の path で待ち受ける。
// そのディレクトリは他の利用者が先に作れず、入れ替えもできないので、親のディレクトリは検査しない
// （1 つのプロセスの構成で、MCP のソケットと同じディレクトリに置く場合。親は一時ディレクトリでありうる）。
// それ以外は Listen と同じである。
func ListenInOwnDir(path string) (net.Listener, error) {
	return listen(path, false)
}

func listen(path string, checkParent bool) (net.Listener, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("server: listen: socket path %s is too long (%d bytes, max %d)", path, len(path), maxSocketPath)
	}
	if err = prepareDir(filepath.Dir(path), checkParent); err != nil {
		return nil, err
	}
	if err = removeStaleSocket(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	return ln, nil
}

// PrepareDir は、server のディレクトリ dir（基本設計 1.2 の `<名前>/`）とその親を、無ければ権限 0700 で作り、
// どちらもシンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、他の利用者の権限が無いことを確かめる（Listen と同じ検査）。
// ロックのファイル（Lock）を置く前と、client がソケットのパスを決めるとき（SocketPathForUnit）に使う。
func PrepareDir(dir string) error {
	return prepareDir(dir, true)
}

func prepareDir(dir string, checkParent bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("server: socket directory: %w", err)
	}
	if checkParent {
		if err := checkPrivateDir(filepath.Dir(dir)); err != nil {
			return err
		}
	}
	return checkPrivateDir(dir)
}

// removeStaleSocket は、path に繋げないソケットのファイルがあれば消す。繋げるソケットか、ソケットでないファイルがあれば誤りを返す。
func removeStaleSocket(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("server: listen: %s exists and is not a socket", path)
	}
	if c, err := net.DialTimeout("unix", path, staleDialTimeout); err == nil {
		_ = c.Close() // 確かめるために繋いだだけ
		return fmt.Errorf("server: listen: another server is listening on %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	return nil
}

// checkPrivateDir は、path がシンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、
// 他の利用者の権限（0o077）が無いことを確かめる（基本設計 1.2。internal/exthost の MCP のソケットのディレクトリの検査と同じ）。
func checkPrivateDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("server: socket directory: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("server: socket directory: %s is not a directory (mode %v)", path, fi.Mode())
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("server: socket directory: %s is accessible by other users (mode %v)", path, fi.Mode())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("server: socket directory: %s is owned by uid %d, not %d", path, st.Uid, os.Geteuid())
	}
	return nil
}
