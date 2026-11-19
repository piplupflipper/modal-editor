package exthost

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
)

// checkMCPSocket は、s のパスが parent の直下の prefix で始まるディレクトリの mcp.sock で、
// ディレクトリが権限 0700 で作られていることを確かめる。
func checkMCPSocket(t *testing.T, s *MCPSocket, parent, prefix string) {
	t.Helper()
	dir := filepath.Dir(s.Path)
	if filepath.Base(s.Path) != "mcp.sock" || filepath.Dir(dir) != parent || !strings.HasPrefix(filepath.Base(dir), prefix) {
		t.Errorf("Path = %q, want %s/%s*/mcp.sock", s.Path, parent, prefix)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("%s: mode = %v, want a directory with 0700", dir, fi.Mode())
	}
	if _, err := os.Lstat(s.Path); !os.IsNotExist(err) {
		t.Errorf("socket file exists before anyone listens: %v", err)
	}
}

// MCP のソケットのパスは、XDG_RUNTIME_DIR があればその下の vi-imitation/<pid>-<乱数>/mcp.sock にし、
// 使えなければ一時ディレクトリの下の vi-imitation-<pid>-<乱数>/mcp.sock にする（基本設計 6.2）。
func TestNewMCPSocket(t *testing.T) {
	const pid = 4242
	prefix := strconv.Itoa(pid) + "-"
	t.Run("runtime dir", func(t *testing.T) {
		rt, tmp := t.TempDir(), t.TempDir()
		s, err := newMCPSocket(rt, tmp, pid)
		if err != nil {
			t.Fatal(err)
		}
		checkMCPSocket(t, s, filepath.Join(rt, "vi-imitation"), prefix)
		fi, err := os.Stat(filepath.Join(rt, "vi-imitation"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("vi-imitation: mode = %v, want 0700", fi.Mode().Perm())
		}
		// 同じプロセスで 2 つ作っても、別のパスになる。
		s2, err := newMCPSocket(rt, tmp, pid)
		if err != nil {
			t.Fatal(err)
		}
		if s2.Path == s.Path {
			t.Errorf("same path twice: %s", s.Path)
		}
		dir := filepath.Dir(s.Path)
		// ソケットのファイルができていても、中身ごと消す。2 回目の Remove は何もしない。
		if err := os.WriteFile(s.Path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.Remove(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s remains after Remove: %v", dir, err)
		}
		if err := s.Remove(); err != nil {
			t.Errorf("second Remove = %v", err)
		}
		if _, err := os.Stat(filepath.Dir(s2.Path)); err != nil {
			t.Errorf("Remove of one socket removed the other: %v", err)
		}
	})
	t.Run("no runtime dir", func(t *testing.T) {
		tmp := t.TempDir()
		for _, rt := range []string{"", "relative/dir"} {
			s, err := newMCPSocket(rt, tmp, pid)
			if err != nil {
				t.Fatal(err)
			}
			checkMCPSocket(t, s, tmp, "vi-imitation-"+prefix)
		}
	})
	t.Run("runtime dir unusable", func(t *testing.T) {
		tmp := t.TempDir()
		// ディレクトリを作れない（同じ名前のファイルがある）。
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := newMCPSocket(file, tmp, pid)
		if err != nil {
			t.Fatal(err)
		}
		checkMCPSocket(t, s, tmp, "vi-imitation-"+prefix)
		// パスが Unix ドメインソケットの上限を超える。
		long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
		s, err = newMCPSocket(long, tmp, pid)
		if err != nil {
			t.Fatal(err)
		}
		checkMCPSocket(t, s, tmp, "vi-imitation-"+prefix)
		if _, err := os.Stat(long); !os.IsNotExist(err) {
			t.Errorf("created a directory under the long path: %v", err)
		}
	})
	t.Run("runtime dir not private", func(t *testing.T) {
		tmp := t.TempDir()
		// 既にある vi-imitation が、他の利用者に開いている。
		open := t.TempDir()
		if err := os.Mkdir(filepath.Join(open, "vi-imitation"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(open, "vi-imitation"), 0o755); err != nil { //nolint:gosec // 他の利用者に開いたディレクトリを作るのがこのケースである。
			t.Fatal(err)
		}
		// 既にある vi-imitation が、私用のディレクトリへのシンボリックリンクである。
		link := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(link, "vi-imitation")); err != nil {
			t.Fatal(err)
		}
		for _, rt := range []string{open, link} {
			s, err := newMCPSocket(rt, tmp, pid)
			if err != nil {
				t.Fatal(err)
			}
			checkMCPSocket(t, s, tmp, "vi-imitation-"+prefix)
		}
		private := filepath.Join(t.TempDir(), "private")
		if err := os.Mkdir(private, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := checkPrivateDir(private); err != nil {
			t.Errorf("checkPrivateDir of a private directory = %v", err)
		}
	})
	t.Run("both unusable", func(t *testing.T) {
		long := filepath.Join(t.TempDir(), strings.Repeat("d", 100))
		if _, err := newMCPSocket(long, long, pid); err == nil {
			t.Error("newMCPSocket succeeded with a too long path")
		}
	})
	t.Run("from the environment", func(t *testing.T) {
		rt := t.TempDir()
		t.Setenv("XDG_RUNTIME_DIR", rt)
		s, err := NewMCPSocket()
		if err != nil {
			t.Fatal(err)
		}
		defer s.Remove() //nolint:errcheck // テストの後始末
		checkMCPSocket(t, s, filepath.Join(rt, "vi-imitation"), strconv.Itoa(os.Getpid())+"-")
		if want := ext.EnvMCPSocket + "=" + s.Path; s.Env() != want {
			t.Errorf("Env = %q, want %q", s.Env(), want)
		}
	})
}

// Config.Env の変数は、すべての拡張機能の子プロセスに渡り、本体の環境の同じ名前の変数より優先する（基本設計 6.2）。
func TestConfigEnv(t *testing.T) {
	t.Setenv(ext.EnvMCPSocket, "/stale")
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"set", []string{ext.EnvMCPSocket + "=/run/x/mcp.sock"}, "/run/x/mcp.sock"},
		{"inherited", nil, "/stale"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hs := newHarness(t, []Spec{fakeSpec(t, "x", modeNormal, ""), fakeSpec(t, "y", modeNormal, "")},
				func(c *Config) { c.Env = tc.env }, true)
			for _, id := range []string{"x", "y"} {
				var got fakeEnv
				if err := hs.h.Call(ctx, id, methodEnv, nil, callTimeout, &got); err != nil {
					t.Fatal(err)
				}
				if !got.MCPSet || got.MCP != tc.want {
					t.Errorf("%s: %s = %q (set %v), want %q", id, ext.EnvMCPSocket, got.MCP, got.MCPSet, tc.want)
				}
			}
		})
	}
}
