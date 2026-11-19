package exthost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/piplupflipper/vi-imitation/ext"
)

// mcpSocketName は、MCP のソケットのファイル名である。
const mcpSocketName = "mcp.sock"

// maxSocketPath は、Unix ドメインソケットのパスのバイト数の上限である。
// sockaddr_un の sun_path は Linux で 108 バイト、macOS で 104 バイトで、末尾の NUL の分を引いた短い方にする。
const maxSocketPath = 103

// MCPSocket は、本体が起動ごとに決める MCP のソケットのパスと、そのパスを置くディレクトリである（基本設計 6.2）。
// [NewMCPSocket] で作り、エディタの終了時に [MCPSocket.Remove] で消す。
//
// 本体はパスを決めて、[Config.Env] で環境変数 ext.EnvMCPSocket としてすべての拡張機能へ渡す。
// 起動の契機 socket の拡張機能（MCP）があれば、[Config.SocketPath] にもこのパスを入れ、[Host] が起動時から待ち受ける（基本設計 6.2）。
type MCPSocket struct {
	// Path は、ソケットの絶対パスである。ファイルはまだ無い。
	Path string
	// dir は、Path を置くために作った、権限 0700 のディレクトリである。Remove で中身ごと消す。
	dir string
}

// NewMCPSocket は、MCP のソケットのパスを決め、パスを置くディレクトリを権限 0700 で作る（基本設計 6.2）。
//
// パスは次の順に決める。
//
//  1. 環境変数 XDG_RUNTIME_DIR が絶対パスなら、`$XDG_RUNTIME_DIR/vi-imitation/<pid>-<乱数>/mcp.sock`
//  2. 1 が使えなければ（変数が無い、ディレクトリを作れない、パスが長すぎる、`$XDG_RUNTIME_DIR/vi-imitation` が私用のディレクトリでない）、
//     `<一時ディレクトリ>/vi-imitation-<pid>-<乱数>/mcp.sock`。一時ディレクトリは os.TempDir（TMPDIR か /tmp）である
//
// `$XDG_RUNTIME_DIR/vi-imitation` は、既にあれば、シンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、
// 他の利用者の権限（0o077）が無いことを確かめる。
// su（- 無し）や sudo -E で他の利用者の XDG_RUNTIME_DIR を引き継いで起動すると、元の利用者が中のディレクトリを作り直して
// ソケットを置く場所を握れるためである。
//
// ディレクトリの名前に乱数を足すのは、同じプロセスで複数のエディタを作る場合（テスト）と、
// 異常終了して残ったディレクトリとプロセス ID が重なった場合にも、一意にするためである。
// 乱数の部分は os.MkdirTemp が作り、既にあるディレクトリは使わない。
// 一時ディレクトリは他の利用者と共有されうるが、os.MkdirTemp は新しいディレクトリを 0700 で排他的に作るので、他の利用者は中に入れない。
//
// 本体が SIGKILL などで後始末をせずに終わると、ディレクトリ（と、待ち受けていればソケットのファイル）が残る。
// XDG_RUNTIME_DIR はログアウトで消えるが、一時ディレクトリへ落ちた場合は残り続ける。
// 残ったディレクトリを掃除する規則は、中継コマンドが起動中のエディタを探す規則（要件定義 7 章）と合わせて決める。
//
// req: FR-33
func NewMCPSocket() (*MCPSocket, error) {
	return newMCPSocket(os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(), os.Getpid())
}

func newMCPSocket(runtimeDir, tempDir string, pid int) (*MCPSocket, error) {
	var errs []error
	if filepath.IsAbs(runtimeDir) {
		base := filepath.Join(runtimeDir, "vi-imitation")
		s, err := makeMCPSocket(base, strconv.Itoa(pid)+"-", true)
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	s, err := makeMCPSocket(tempDir, "vi-imitation-"+strconv.Itoa(pid)+"-", false)
	if err == nil {
		return s, nil
	}
	return nil, errors.Join(append(errs, err)...)
}

// makeMCPSocket は、parent の下に pattern の名前のディレクトリを作り、その中のソケットのパスを返す。
// parent が無ければ権限 0700 で作る。private なら、parent が私用のディレクトリであること（checkPrivateDir）を確かめる。
func makeMCPSocket(parent, pattern string, private bool) (*MCPSocket, error) {
	parent, err := filepath.Abs(parent)
	if err != nil {
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	// 乱数の部分（os.MkdirTemp は 10 桁以下の数字）を含めても上限に収まるかを、作る前に確かめる。
	if n := len(filepath.Join(parent, pattern+"0123456789", mcpSocketName)); n > maxSocketPath {
		return nil, fmt.Errorf("exthost: mcp socket: path under %s is too long (%d bytes)", parent, n)
	}
	if err = os.MkdirAll(parent, 0o700); err != nil { //nolint:gosec // 利用者自身の XDG_RUNTIME_DIR か一時ディレクトリの下に作るのが、この関数の役目である。
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	if private {
		if err = checkPrivateDir(parent); err != nil {
			return nil, err
		}
	}
	dir, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	return &MCPSocket{Path: filepath.Join(dir, mcpSocketName), dir: dir}, nil
}

// checkPrivateDir は、path がシンボリックリンクでないディレクトリで、持ち主が実効ユーザー ID で、
// 他の利用者の権限（0o077）が無いことを確かめる。
func checkPrivateDir(path string) error {
	fi, err := os.Lstat(path) //nolint:gosec // 利用者自身の XDG_RUNTIME_DIR の下を調べるのが、この関数の役目である。
	if err != nil {
		return fmt.Errorf("exthost: mcp socket: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("exthost: mcp socket: %s is not a directory (mode %v)", path, fi.Mode())
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("exthost: mcp socket: %s is accessible by other users (mode %v)", path, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("exthost: mcp socket: %s is owned by uid %d, not %d", path, st.Uid, os.Geteuid())
	}
	return nil
}

// Env は、[Config.Env] に足す "ext.EnvMCPSocket=パス" の形の要素を返す。
func (s *MCPSocket) Env() string {
	return ext.EnvMCPSocket + "=" + s.Path
}

// Remove は、ソケットを置いたディレクトリを中身ごと消す。2 回目以降の呼び出しは何もしない。
// 待ち受けた拡張機能が終わる前に消すと、ソケットのファイルが先に消え、中継コマンドがそれ以降つなげなくなる。
// このため、拡張機能をすべて止めた後（[Host.Close] の後）に呼ぶ。
func (s *MCPSocket) Remove() error {
	if s == nil || s.dir == "" {
		return nil
	}
	dir := s.dir
	s.dir = ""
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("exthost: mcp socket: %w", err)
	}
	return nil
}
