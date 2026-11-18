package extmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #213 の拡張機能の管理の推奨（PR #195 の再レビュー）と、PR #195 の未確認事項（同梱の拡張機能を隠すこと）を確かめる。

// ロックファイルが切れた symlink なら、symlink を残してリンクの指す先に作る。
func TestSyncLockBrokenSymlink(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	e := newEnv(t)
	dotfiles := t.TempDir()
	// 相対パスのリンクを 2 段にする（lock → link2 → 無いファイル）。
	if err := os.Symlink(filepath.Join(dotfiles, "link2"), e.opts.LockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(LockFileName, filepath.Join(dotfiles, "link2")); err != nil {
		t.Fatal(err)
	}
	e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "main"})
	if fi, err := os.Lstat(e.opts.LockPath); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("ロックファイル = %v, %v; want symlink のまま", fi, err)
	}
	data, err := os.ReadFile(filepath.Join(dotfiles, LockFileName))
	if err != nil || !strings.Contains(string(data), c1) {
		t.Errorf("リンク先 = %s, %v; want %s を含む", data, err, c1)
	}
	// 2 回目は、作ったリンク先を読んで「変更なし」にする。
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "main"}), Outcome{Name: "hello", Action: ActionUnchanged, Commit: c1})
}

// 取得元に届かないだけなら、取得したソースを消さない。入れてあるものとロックファイルの記録も残す。
func TestSyncUnreachableKeepsSource(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	r.git("tag", "v1")
	r.commitExt("v2")
	r.git("tag", "v2")
	e := newEnv(t)
	e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"})
	moved := r.dir + "-moved"
	if err := os.Rename(r.dir, moved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(moved) })
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v2"}), Outcome{Name: "hello", Action: ActionFailed, Commit: c1})
	src := filepath.Join(filepath.Dir(e.opts.ExtensionsDir), "extmgr", "src", "hello", ".git")
	if _, err := os.Stat(src); err != nil {
		t.Errorf("取得したソースが消えた: %v", err)
	}
	e.wantRuns(t, "hello", "v1")
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "v1", Commit: c1}})
}

// 版を上げて宣言ファイルの ID が変わったら、PreviousID に前の版の ID を返す。
func TestSyncPreviousID(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	r.git("tag", "v1")
	files := extFiles("v2")
	files[ManifestFileName] = strings.Replace(files[ManifestFileName], `"id": "hello"`, `"id": "hello2"`, 1)
	r.commit(files)
	r.git("tag", "v2")
	e := newEnv(t)
	out := e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"})
	if out[0].ID != "hello" || out[0].PreviousID != "" {
		t.Errorf("v1: ID = %q, PreviousID = %q; want hello と空", out[0].ID, out[0].PreviousID)
	}
	out = e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v2"})
	if out[0].Action != ActionUpdated || out[0].ID != "hello2" || out[0].PreviousID != "hello" {
		t.Errorf("v2: %v ID = %q, PreviousID = %q; want 更新 hello2 と hello", out[0].Action, out[0].ID, out[0].PreviousID)
	}
	out = e.sync(t)
	if len(out) != 1 || out[0].Action != ActionRemoved || out[0].ID != "hello2" {
		t.Errorf("削除: %+v; want 削除 hello2", out)
	}
}

// ID の重なりの Warning は、PhaseDone の後に PhaseWarning の Event でも知らせる。
// 手で置いたものとの重なりと、同梱の拡張機能を隠すことも知らせる。
func TestSyncShadowWarnings(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	e := newEnv(t)
	bundled := t.TempDir()
	writeManifest(t, filepath.Join(bundled, "hello"), "hello")
	e.opts.BundledDirs = []string{bundled, filepath.Join(t.TempDir(), "none")}

	out := e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "main"})
	if out[0].Warning == nil || !strings.Contains(out[0].Warning.Error(), "同梱のものの代わりに hello を使う") {
		t.Errorf("Warning = %v; want 同梱の拡張機能を隠す", out[0].Warning)
	}
	if got, want := eventsString(e.events), "1/1 hello 取得, 1/1 hello ビルド, 1/1 hello 入れ替え, 1/1 hello 完了 追加 hello, 1/1 hello 警告 追加 hello"; got != want {
		t.Errorf("進み具合 = %s, want %s", got, want)
	}
	if e.events[3].Outcome.Warning != nil || e.events[4].Outcome.Warning == nil {
		t.Errorf("Event の Warning = %v, %v; want PhaseWarning にだけ", e.events[3].Outcome.Warning, e.events[4].Outcome.Warning)
	}

	// 手で置いた 0hand が名前の順で先なので、本体は hello を使わない。
	writeManifest(t, filepath.Join(e.opts.ExtensionsDir, "0hand"), "hello")
	out = e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "main"})
	if out[0].Action != ActionUnchanged || out[0].Warning == nil || !strings.Contains(out[0].Warning.Error(), "拡張機能 0hand と重なるので、本体は hello を使わない") {
		t.Errorf("%v Warning = %v; want 0hand との重なり", out[0].Action, out[0].Warning)
	}
}

func writeManifest(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"id": "` + id + `", "command": "./x", "activation": [{"on": "filetype", "pattern": "go"}]}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
