package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// testGitEnv は、テストの git の環境である。利用者の環境の GIT_DIR などと、利用者の git の設定を使わない。
func testGitEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") })
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
}

// isolateGit は、このテストの間、readHead が起動する git が利用者の git の設定を読まないようにする。
func isolateGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// runGit は、テストの一時ディレクトリの dir で git を実行する。
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // テストの一時のリポジトリで git を実行する
	cmd.Dir = dir
	cmd.Env = testGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepo は、一時ディレクトリに git のリポジトリを作り、files を書いてコミットする。リポジトリの根を返す。
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	writeTestFiles(t, dir, files)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func writeTestFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// readHead は、ファイルを含むリポジトリの HEAD の版を、リポジトリの根からのパスと、末尾の改行を 1 つ除いた中身で返す。
// 作業ツリーの変更は読まない。
//
// req: FR-104
func TestReadHead(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t, map[string]string{"a.go": "package a\n", "sub/b.txt": "one\ntwo\n\n"})
	writeTestFiles(t, dir, map[string]string{"a.go": "package changed\n", "new.txt": "x\n"})

	for _, c := range []struct{ path, name, text string }{
		{"a.go", "a.go", "package a"},
		{"sub/b.txt", "sub/b.txt", "one\ntwo\n"},
	} {
		got, err := readHead(context.Background(), filepath.Join(dir, c.path))
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if got.Name != c.name || got.Text != c.text {
			t.Errorf("%s: %+v, want name %q text %q", c.path, got, c.name, c.text)
		}
	}

	// HEAD に無いファイルと、リポジトリの外のファイルは、日本語の誤りにする。
	if _, err := readHead(context.Background(), filepath.Join(dir, "new.txt")); err == nil || !strings.Contains(err.Error(), "HEAD の new.txt を読めません") {
		t.Errorf("HEAD に無いファイル: %v", err)
	}
	outside := t.TempDir()
	if _, err := readHead(context.Background(), filepath.Join(outside, "x.txt")); err == nil || !strings.Contains(err.Error(), "git のリポジトリの中のファイルではありません") {
		t.Errorf("リポジトリの外: %v", err)
	}
}

// 本体の環境に GIT_DIR などが残っていても（本体を git commit の GIT_EDITOR にしたとき）、ファイルを含むリポジトリの HEAD を読む。
//
// req: FR-104
func TestReadHeadIgnoresRepoEnv(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t, map[string]string{"a.txt": "mine\n"})
	other := newRepo(t, map[string]string{"a.txt": "other\n"})
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))
	got, err := readHead(context.Background(), filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "mine" {
		t.Errorf("Text = %q, want mine", got.Text)
	}
}

// 作業ツリーへ取り出すときの変換（.gitattributes の eol=crlf）を通した HEAD の版を読む。本体のバッファは CR を行の中のバイトとして持つので、
// 作業ツリーと同じ CRLF で読めば、変えていない行は同じ行になる（PR #247 のレビューの推奨 1）。
//
// req: FR-104
func TestReadHeadCRLF(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t, map[string]string{".gitattributes": "*.crlf text eol=crlf\n", "w.crlf": "one\ntwo\n"})
	got, err := readHead(context.Background(), filepath.Join(dir, "w.crlf"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "one\r\ntwo\r" {
		t.Errorf("Text = %q, want %q", got.Text, "one\r\ntwo\r")
	}
}

// シンボリックリンクのファイルは、リンクの先のファイルの HEAD の版を読む（PR #247 のレビューの推奨 2）。
//
// req: FR-104
func TestReadHeadSymlink(t *testing.T) {
	isolateGit(t)
	dir := newRepo(t, map[string]string{"sub/a.txt": "target\n"})
	if err := os.Symlink(filepath.Join("sub", "a.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "link.txt")
	runGit(t, dir, "commit", "-q", "-m", "link")
	got, err := readHead(context.Background(), filepath.Join(dir, "link.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "sub/a.txt" || got.Text != "target" {
		t.Errorf("readHead = %+v, want sub/a.txt の target", got)
	}
}

func TestGitEnv(t *testing.T) {
	got := gitEnv([]string{"PATH=/bin", "GIT_DIR=/x", "GIT_OPTIONAL_LOCKS=1", "GIT_AUTHOR_NAME=a", "GIT_COMMON_DIR=/y"})
	want := []string{"PATH=/bin", "GIT_AUTHOR_NAME=a", "GIT_OPTIONAL_LOCKS=0"}
	if !slices.Equal(got, want) {
		t.Errorf("gitEnv = %q, want %q", got, want)
	}
}
