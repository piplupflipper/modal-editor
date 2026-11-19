package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/extmgr"
)

// extSyncEnv は、:ext sync のテストの git と go の子プロセスに渡す環境変数を返す。
// ネットワークに出ず、利用者の Go のキャッシュと git の設定を使わない（internal/extmgr の package の doc の「テストの環境」と同じ）。
func extSyncEnv(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	return []string{
		"GOCACHE=" + filepath.Join(root, "gocache"),
		"GOMODCACHE=" + filepath.Join(root, "gomodcache"),
		"GOPATH=" + filepath.Join(root, "gopath"),
		"GOFLAGS=-modcacherw",
		"GOENV=off",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"CGO_ENABLED=0",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	}
}

const extSyncCommand = "modal-editor-ext-hello"

// extSyncRepo は、実行すると版の名前を出す拡張機能（ID は hello）の v1 と v2 のタグを持つ git のリポジトリを作り、パスを返す。
func extSyncRepo(t *testing.T, env []string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // G204: テストの取得元を作る git の引数はテストが組み立てたもの
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	for _, v := range []string{"v1", "v2", "v3"} {
		cmds := `[{"id": "hello.run"}]`
		if v == "v3" {
			// v3 は宣言ファイルを変える（コマンドを足す）。
			cmds = `[{"id": "hello.run"}, {"id": "hello.more"}]`
		}
		files := map[string]string{
			"go.mod":  "module example.com/hello\n\ngo 1.21\n",
			"main.go": fmt.Sprintf("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(%q) }\n", v),
			extmgr.ManifestFileName: `{"id": "hello", "command": "./` + extSyncCommand + `",
  "activation": [{"on": "command", "id": "hello.run"}], "contributes": {"commands": ` + cmds + `}}
`,
		}
		for name, body := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "--quiet", "-m", v)
		git("tag", v)
	}
	return dir
}

// waitMessage は、メッセージの行が substr を含むまで待つ。ビルドがあるので、harness.eventually より長く待つ。
func (h *harness) waitMessage(substr string) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for !strings.Contains(h.message(), substr) {
		if time.Now().After(deadline) {
			h.t.Fatalf("メッセージ %q を待ちきれない（message=%q）", substr, h.message())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// :ext sync は、設定ファイルの extensions.install の拡張機能を取得してビルドし、拡張機能のディレクトリに入れ、
// ロックファイルに記録する。版を変えれば入れ替え、本体が使っている拡張機能を止めて次の契機で起動し直す。
// 設定から外せば削除し、本体が使っている拡張機能を止める。設定の書き損じでは削除しない。
//
// req: FR-102, FR-103, FR-121, FR-122
func TestExtSync(t *testing.T) {
	env := extSyncEnv(t)
	repo := extSyncRepo(t, env)
	confDir := t.TempDir()
	confPath := filepath.Join(confDir, "config.jsonc")
	lockPath := filepath.Join(confDir, extmgr.LockFileName)
	extDir := filepath.Join(t.TempDir(), "extensions")
	writeConfig := func(install string) {
		t.Helper()
		if err := os.WriteFile(confPath, []byte(`{"extensions": {"install": [`+install+`]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(version string) string {
		return fmt.Sprintf(`{"name": "hello", "repo": %q, "version": %q}`, repo, version)
	}
	runs := func(want string) {
		t.Helper()
		out, err := exec.Command(filepath.Join(extDir, "hello", extSyncCommand)).Output() //nolint:gosec // G204: テストで入れた実行ファイル
		if err != nil || string(out) != want {
			t.Errorf("入れた実行ファイルの出力 = %q, %v; want %q", out, err, want)
		}
	}
	lockHas := func(want bool) {
		t.Helper()
		l, err := extmgr.ReadLock(lockPath)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := l.Extensions["hello"]; ok != want {
			t.Errorf("ロックファイルの hello の記録 = %v, want %v（%+v）", ok, want, l.Extensions)
		}
	}

	// 本体が使っている拡張機能 hello は、入れるディレクトリの宣言ファイルを読んだものとして作る（実行ファイルはテストの偽の拡張機能）。
	spec := fakeSpecs(t)[0]
	spec.Manifest.ID = "hello"
	spec.Dir = filepath.Join(extDir, "hello")
	h := start(t, Config{
		Extensions: []exthost.Spec{spec},
		ExtManager: ExtManagerConfig{ConfigPath: confPath, ExtensionDirs: []string{extDir}, Env: env},
	})

	writeConfig(entry("v1"))
	h.keys(":ext sync<CR>")
	h.waitMessage("拡張機能: 追加 1、更新 0、削除 0、変更なし 0、失敗 0。本体を起動し直すと反映します")
	runs("v1")
	lockHas(true)

	h.keys(":fake.upper<CR>")
	h.eventually("hello の起動", func() bool { return h.state("hello") == exthost.StateRunning })
	writeConfig(entry("v2"))
	h.keys(":ext sync<CR>")
	h.waitMessage("拡張機能: 追加 0、更新 1")
	runs("v2")
	h.eventually("入れ替えた hello を止めて未起動へ戻す", func() bool { return h.state("hello") == exthost.StateNotStarted })
	if msg := h.message(); strings.Contains(msg, "起動し直す") {
		t.Errorf("宣言ファイルが同じなのに起動し直しを求めた: %q", msg)
	}

	// 宣言ファイルが変わったら、前の版を止めずに動かし続け、起動し直しを求める。
	h.keys(":fake.upper<CR>")
	h.eventually("hello の起動", func() bool { return h.state("hello") == exthost.StateRunning })
	writeConfig(entry("v3"))
	h.keys(":ext sync<CR>")
	h.waitMessage("拡張機能: 追加 0、更新 1、削除 0、変更なし 0、失敗 0。本体を起動し直すと反映します")
	runs("v3")
	if st := h.state("hello"); st != exthost.StateRunning {
		t.Errorf("宣言ファイルが変わった hello の状態 = %v, want 稼働中のまま", st)
	}

	// 書き損じ（version が空）では、設定から外れても削除しない。
	writeConfig(fmt.Sprintf(`{"name": "hello", "repo": %q, "version": ""}`, repo))
	h.keys(":ext sync<CR>")
	h.waitMessage("拡張機能: 追加 0、更新 0、削除 0、変更なし 0、失敗 0")
	runs("v3")
	lockHas(true)
	h.e.do(func() {
		if f := h.e.float; f == nil || !strings.Contains(popupText(h.e), "削除しなかった") || !f.center {
			t.Errorf("ポップアップ = %q; want 削除しなかったことを画面の中央に知らせる", popupText(h.e))
		}
	})

	h.keys(":fake.upper<CR>")
	h.eventually("hello の起動", func() bool { return h.state("hello") == exthost.StateRunning })
	writeConfig("")
	h.keys(":ext sync<CR>")
	h.waitMessage("拡張機能: 追加 0、更新 0、削除 1")
	if _, err := os.Stat(filepath.Join(extDir, "hello")); !os.IsNotExist(err) {
		t.Errorf("削除した拡張機能のディレクトリが残っている: %v", err)
	}
	lockHas(false)
	h.eventually("削除した hello を止める", func() bool { return h.state("hello") == exthost.StateDisabled })
	if msg := h.message(); !strings.Contains(msg, "削除 1") {
		t.Errorf("止めたことで結果のメッセージを上書きした: %q", msg)
	}

	h.keys(":ext foo<CR>")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "E475") {
		t.Errorf(":ext foo のメッセージ = %q", msg)
	}
}

// 拡張機能が :ext と同じ ID のコマンドを宣言していれば、本体の :ext を優先し、誤りを返す（New が起動時に知らせる）。
func TestExtCommandShadowed(t *testing.T) {
	cmds := map[string]string{"ext": "fake"}
	if err := addExtManagerCommand(cmds); err == nil || cmds["ext"] != extManagerExtension {
		t.Errorf("err = %v, 表 = %v", err, cmds)
	}
}
