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
// path を置くディレクトリは、無ければ権限 0700 で作る。
// 既にあれば、シンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、他の利用者の権限が無いことを確かめ、満たさなければ待ち受けない。
// path に繋げない古いソケット（server が SIGKILL で終わった後に残ったもの）があれば消してから待ち受け、繋げるソケットがあれば誤りにする。
// 返す net.Listener は、Close でソケットのファイルを消す。
func Listen(path string) (net.Listener, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	if len(path) > maxSocketPath {
		return nil, fmt.Errorf("server: listen: socket path %s is too long (%d bytes, max %d)", path, len(path), maxSocketPath)
	}
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("server: listen: %w", err)
	}
	if err = checkPrivateDir(dir); err != nil {
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
		return fmt.Errorf("server: socket directory: %s is accessible by other users (mode %v)", path, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("server: socket directory: %s is owned by uid %d, not %d", path, st.Uid, os.Geteuid())
	}
	return nil
}
