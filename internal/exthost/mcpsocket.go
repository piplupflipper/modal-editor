package exthost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/piplupflipper/modal-editor/ext"
)

// mcpSocketName は、MCP のソケットのファイル名である。
const mcpSocketName = "mcp.sock"

// maxSocketPath は、Unix ドメインソケットのパスのバイト数の上限である。
// sockaddr_un の sun_path は Linux で 108 バイト、macOS で 104 バイトで、末尾の NUL の分を引いた短い方にする。
const maxSocketPath = 103

// MCPSocket は、本体が起動ごとに決める MCP のソケットのパスと、そのパスを置くディレクトリである（基本設計 6.2）。
// [NewMCPSocket] で作り、エディタの終了時に [MCPSocket.Remove] で消す。
//
// 本体はパスを決めて、[Config.Env] で環境変数 ext.EnvMCPSocket としてすべての拡張機能へ渡すだけで、自分では待ち受けない。
// 本体が待ち受けるか MCP の拡張機能が待ち受けるかは、起動の契機 socket の裁定待ちである（基本設計 10 章）。
// 当面は MCP の拡張機能が、起動したときにこのパスで待ち受ける。
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
//  1. 環境変数 XDG_RUNTIME_DIR が絶対パスなら、`$XDG_RUNTIME_DIR/modal-editor/<pid>-<乱数>/mcp.sock`
//  2. 1 が使えなければ（変数が無い、ディレクトリを作れない、パスが長すぎる）、`<一時ディレクトリ>/modal-editor-<pid>-<乱数>/mcp.sock`。
//     一時ディレクトリは os.TempDir（TMPDIR か /tmp）である
//
// ディレクトリの名前に乱数を足すのは、同じプロセスで複数のエディタを作る場合（テスト）と、
// 異常終了して残ったディレクトリとプロセス ID が重なった場合にも、一意にするためである。
// 乱数の部分は os.MkdirTemp が作り、既にあるディレクトリは使わない。
// 一時ディレクトリは他の利用者と共有されうるが、os.MkdirTemp は新しいディレクトリを 0700 で作るので、他の利用者は中に入れない。
//
// req: FR-33
func NewMCPSocket() (*MCPSocket, error) {
	return newMCPSocket(os.Getenv("XDG_RUNTIME_DIR"), os.TempDir(), os.Getpid())
}

func newMCPSocket(runtimeDir, tempDir string, pid int) (*MCPSocket, error) {
	var errs []error
	if filepath.IsAbs(runtimeDir) {
		base := filepath.Join(runtimeDir, "modal-editor")
		s, err := makeMCPSocket(base, strconv.Itoa(pid)+"-")
		if err == nil {
			return s, nil
		}
		errs = append(errs, err)
	}
	s, err := makeMCPSocket(tempDir, "modal-editor-"+strconv.Itoa(pid)+"-")
	if err == nil {
		return s, nil
	}
	return nil, errors.Join(append(errs, err)...)
}

// makeMCPSocket は、parent の下に pattern の名前のディレクトリを作り、その中のソケットのパスを返す。
// parent が無ければ権限 0700 で作る。
func makeMCPSocket(parent, pattern string) (*MCPSocket, error) {
	parent, err := filepath.Abs(parent)
	if err != nil {
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	// 乱数の部分（os.MkdirTemp は 10 桁以下の数字）を含めても上限に収まるかを、作る前に確かめる。
	if n := len(filepath.Join(parent, pattern+"0123456789", mcpSocketName)); n > maxSocketPath {
		return nil, fmt.Errorf("exthost: mcp socket: path under %s is too long (%d bytes)", parent, n)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	dir, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return nil, fmt.Errorf("exthost: mcp socket: %w", err)
	}
	return &MCPSocket{Path: filepath.Join(dir, mcpSocketName), dir: dir}, nil
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
