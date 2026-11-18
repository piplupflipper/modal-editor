package extmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 設定の extensions.install に誤りがあったとき（KeepUnlisted）と、specs に誤りのある Spec があるときは、削除をしない。
// 誤りの項目の拡張機能は、入っている版のまま残る（PR #195 のレビューの必須 1）。
func TestSyncKeepUnlisted(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	e := newEnv(t)
	hello := Spec{Name: "hello", Repo: r.dir, Version: "main"}
	keep := Spec{Name: "keep", Repo: r.dir, Version: "main"}
	e.sync(t, hello, keep)

	// config が "version": "" の項目や、構文の誤りの設定から落とした結果の列を、KeepUnlisted で渡す。
	e.opts.KeepUnlisted = true
	wantOutcomes(t, e.sync(t, keep), Outcome{Name: "keep", Action: ActionUnchanged, Commit: c1})
	wantOutcomes(t, e.sync(t), nil...)
	e.wantRuns(t, "hello", "v1")
	e.wantLock(t, map[string]LockEntry{
		"hello": {Repo: r.dir, Version: "main", Commit: c1},
		"keep":  {Repo: r.dir, Version: "main", Commit: c1},
	})

	// KeepUnlisted でなくても、誤りのある Spec が 1 つでもあれば削除しない。
	e.opts.KeepUnlisted = false
	wantOutcomes(t, e.sync(t, keep, Spec{Name: "hello", Repo: r.dir, Version: ""}),
		Outcome{Name: "keep", Action: ActionUnchanged, Commit: c1},
		Outcome{Name: "hello", Action: ActionFailed})
	e.wantRuns(t, "hello", "v1")

	// 誤りが無ければ削除する。
	wantOutcomes(t, e.sync(t, keep),
		Outcome{Name: "keep", Action: ActionUnchanged, Commit: c1},
		Outcome{Name: "hello", Action: ActionRemoved})
	if got, want := eventsString(e.events), "1/2 keep 完了 変更なし hello, 2/2 hello 削除, 2/2 hello 完了 削除 hello"; got != want {
		t.Errorf("進み具合 = %s, want %s", got, want)
	}
}

// 本体の環境の GIT_DIR などは、git と go の子プロセスに渡さない（PR #195 のレビューの必須 2）。
// 渡すと、拡張機能の取得が、本体を起動した git commit のリポジトリを書き換える。
// t.Setenv を使うので並行にしない。
func TestSyncIgnoresRepoEnv(t *testing.T) {
	r := newRepo(t)
	c1 := r.commitExt("v1")
	r.git("tag", "v1")
	c2 := r.commitExt("v2")
	r.git("tag", "v2")

	victim := newRepo(t)
	vc := victim.commit(map[string]string{"a.txt": "a\n"})
	victim.git("remote", "add", "origin", "https://example.com/victim.git")
	victim.git("update-ref", "refs/remotes/origin/main", vc)
	e := newEnv(t)

	gitDir := filepath.Join(victim.dir, ".git")
	t.Setenv("GIT_DIR", gitDir)
	t.Setenv("GIT_WORK_TREE", victim.dir)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(gitDir, "index"))
	t.Setenv("GIT_COMMON_DIR", gitDir)

	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"}), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v2"}), Outcome{Name: "hello", Action: ActionUpdated, Commit: c2})
	e.wantRuns(t, "hello", "v2")

	// victim.git は、本体の環境の GIT_DIR などをそのまま使うので、victim のリポジトリを見る。
	if got := victim.git("symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("victim の HEAD = %s, want refs/heads/main", got)
	}
	if got := victim.git("rev-parse", "HEAD"); got != vc {
		t.Errorf("victim の HEAD のコミット = %s, want %s", got, vc)
	}
	if got := victim.git("remote", "get-url", "origin"); got != "https://example.com/victim.git" {
		t.Errorf("victim の origin = %s", got)
	}
	if got := victim.git("rev-parse", "refs/remotes/origin/main"); got != vc {
		t.Errorf("victim の origin/main = %s, want %s", got, vc)
	}
	if got := victim.git("status", "--porcelain"); got != "" {
		t.Errorf("victim の作業ツリーとインデックスが変わった:\n%s", got)
	}
}

// ロックファイルが symlink なら、symlink を残してリンク先を書き換える（PR #195 のレビューの必須 3）。
func TestSyncLockSymlink(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	e := newEnv(t)
	realLock := filepath.Join(t.TempDir(), "dotfiles", LockFileName)
	if err := os.MkdirAll(filepath.Dir(realLock), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realLock, []byte(`{"version": 1, "extensions": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realLock, e.opts.LockPath); err != nil {
		t.Fatal(err)
	}
	e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "main"})
	fi, err := os.Lstat(e.opts.LockPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ロックファイル = %v, %v; want symlink のまま", fi, err)
	}
	data, err := os.ReadFile(realLock)
	if err != nil || !strings.Contains(string(data), c1) {
		t.Errorf("リンク先 = %s, %v; want %s を含む", data, err, c1)
	}
	entries, err := os.ReadDir(filepath.Dir(e.opts.LockPath))
	if err != nil || len(entries) != 1 {
		t.Errorf("ロックファイルのディレクトリ = %v, %v; want symlink だけ", entries, err)
	}
}

// 取得したソースが壊れていたら、消して clone し直す。
func TestSyncBrokenSource(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	r.git("tag", "v1")
	c2 := r.commitExt("v2")
	r.git("tag", "v2")
	e := newEnv(t)
	e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"})
	head := filepath.Join(filepath.Dir(e.opts.ExtensionsDir), "extmgr", "src", "hello", ".git", "HEAD")
	if err := os.WriteFile(head, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v2"}), Outcome{Name: "hello", Action: ActionUpdated, Commit: c2})
	e.wantRuns(t, "hello", "v2")
}

// 同じ WorkDir で別の Sync が動いていれば ErrBusy を返す。前の Sync の途中のディレクトリ（tmp/）は始めに消す。
func TestSyncBusyAndCleanup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	work := filepath.Join(filepath.Dir(e.opts.ExtensionsDir), "extmgr")
	unlock, err := lockWorkDir(work)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), nil, e.opts); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
	unlock()

	leftover := filepath.Join(work, "tmp", "hello-123", "ext")
	if err := os.MkdirAll(leftover, 0o750); err != nil {
		t.Fatal(err)
	}
	e.sync(t)
	if _, err := os.Stat(filepath.Join(work, "tmp")); !os.IsNotExist(err) {
		t.Errorf("tmp が残っている: %v", err)
	}
}

// specs に並べたものどうしで宣言ファイルの ID が重なれば、本体が使わない方（名前の順で後）の Warning に書く。
func TestSyncDuplicateID(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	e := newEnv(t)
	out := e.sync(t, Spec{Name: "b", Repo: r.dir, Version: "main"}, Spec{Name: "a", Repo: r.dir, Version: "main"})
	if out[0].ID != "hello" || out[1].ID != "hello" {
		t.Errorf("ID = %q, %q; want hello", out[0].ID, out[1].ID)
	}
	if out[1].Warning != nil || out[0].Warning == nil || !strings.Contains(out[0].Warning.Error(), "本体は b を使わない") {
		t.Errorf("Warning = %v, %v; want b にだけ", out[0].Warning, out[1].Warning)
	}
}
