package editor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// New は、起動ごとに一意な MCP のソケットのパスを決め、すべての拡張機能へ環境変数 ext.EnvMCPSocket で渡す（基本設計 6.2）。
// 本体の環境に同じ名前の変数があっても、決めたパスで上書きする。
func TestMCPSocketToExtensions(t *testing.T) {
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv(ext.EnvMCPSocket, "/stale")
	h := start(t, Config{Extensions: fakeSpecs(t)})
	sock := h.e.MCPSocket()
	if filepath.Base(sock) != "mcp.sock" || filepath.Dir(filepath.Dir(sock)) != filepath.Join(rt, "modal-editor") {
		t.Fatalf("MCPSocket = %q, want %s/modal-editor/*/mcp.sock", sock, rt)
	}
	fi, err := os.Stat(filepath.Dir(sock))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("socket directory mode = %v, want 0700", fi.Mode().Perm())
	}
	h.keys(`\v`)
	want := fmt.Sprintf("mcp=%q set=true", sock)
	h.eventually("拡張機能が受け取ったパス", func() bool { return h.e.message.Text == want })
}

// Close は、New が決めた MCP のソケットのディレクトリを消す。Config.MCPSocket で指定したパスはそのまま渡し、消さない。
func TestMCPSocketCleanup(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	newEditor := func(cfg Config) *Editor {
		t.Helper()
		cfg.WorkDir = t.TempDir()
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		e, err := New(newFakeTerm(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	closeEditor := func(e *Editor) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := e.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}

	a, b := newEditor(Config{}), newEditor(Config{})
	if a.MCPSocket() == b.MCPSocket() {
		t.Errorf("two editors share %s", a.MCPSocket())
	}
	dir := filepath.Dir(a.MCPSocket())
	closeEditor(a)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("%s remains after Close: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Dir(b.MCPSocket())); err != nil {
		t.Errorf("closing one editor removed the other's directory: %v", err)
	}
	closeEditor(b)

	given := filepath.Join(t.TempDir(), "given.sock")
	c := newEditor(Config{MCPSocket: given})
	if c.MCPSocket() != given {
		t.Errorf("MCPSocket = %q, want %q", c.MCPSocket(), given)
	}
	closeEditor(c)
	if _, err := os.Stat(filepath.Dir(given)); err != nil {
		t.Errorf("the given path's directory was removed: %v", err)
	}
}
