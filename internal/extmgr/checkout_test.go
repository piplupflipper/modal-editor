package extmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Checkout は、取得元を clone して版を解決し、作業ツリーをそのコミットにする。
// 2 回目は fetch して、記録したコミット（pinned）があればそれを、無ければ版を解決し直したコミットを取り出す。
func TestCheckout(t *testing.T) {
	r := newRepo(t)
	first := r.commit(map[string]string{"a.txt": "1"})
	r.git("tag", "v1")
	second := r.commit(map[string]string{"a.txt": "2"})
	src := filepath.Join(t.TempDir(), "src", "p")
	opts := CheckoutOptions{Env: testEnv}
	ctx := context.Background()

	read := func() string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(src, "a.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got, err := Checkout(ctx, src, r.dir, "v1", "", opts); err != nil || got != first || read() != "1" {
		t.Fatalf("Checkout(v1) = %q, %v; a.txt = %q; want %q", got, err, read(), first)
	}
	if got, err := Checkout(ctx, src, r.dir, "main", "", opts); err != nil || got != second || read() != "2" {
		t.Fatalf("Checkout(main) = %q, %v; want %q", got, err, second)
	}
	// 記録したコミットがあれば、版がブランチでもそのコミットを取り出す。
	if got, err := Checkout(ctx, src, r.dir, "main", first, opts); err != nil || got != first || read() != "1" {
		t.Fatalf("Checkout(main, pinned) = %q, %v; want %q", got, err, first)
	}
	// 追跡していないファイルは消す。
	if err := os.WriteFile(filepath.Join(src, "build.o"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Checkout(ctx, src, r.dir, "v1", "", opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "build.o")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("追跡していないファイルが残った: %v", err)
	}
	if _, err := Checkout(ctx, src, r.dir, "v9", "", opts); err == nil || !strings.Contains(err.Error(), `"v9"`) {
		t.Errorf("Checkout(v9) = %v, want an error naming the version", err)
	}
}

// Checkout は、git の引数に紛れる値と相対パスの取得元を、git を起動する前に拒む。
func TestCheckoutRejects(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	for _, tt := range []struct{ repo, version, pinned string }{
		{"", "v1", ""},
		{"-oops", "v1", ""},
		{"relative/repo", "v1", ""},
		{"/abs", "", ""},
		{"/abs", "--force", ""},
		{"/abs", "v1", "abc"},
	} {
		if _, err := Checkout(context.Background(), src, tt.repo, tt.version, tt.pinned, CheckoutOptions{Env: testEnv}); err == nil {
			t.Errorf("Checkout(%q, %q, %q) = nil, want an error", tt.repo, tt.version, tt.pinned)
		}
	}
	if _, err := Checkout(context.Background(), src, "/abs", "v1", "", CheckoutOptions{Git: "no-such-git-command"}); !errors.Is(err, ErrGitNotFound) {
		t.Errorf("Checkout with a missing git = %v, want ErrGitNotFound", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("拒んだのに %s を作った: %v", src, err)
	}
}
