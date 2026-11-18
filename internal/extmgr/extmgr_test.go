package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testEnv は、テストの git と go の子プロセスに渡す環境変数である（package の doc の「テストの環境」）。
var testEnv []string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	root, err := os.MkdirTemp("", "extmgr-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(root) //nolint:errcheck // テストの一時ディレクトリの後始末
	testEnv = []string{
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
	return m.Run()
}

// repo は、テストの取得元の git のリポジトリである。
type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	r := &repo{t: t, dir: t.TempDir()}
	r.git("init", "--quiet", "--initial-branch=main")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: テストの取得元を作る git の引数はテストが組み立てたもの
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), testEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit は、files を書いてコミットし、コミットのハッシュを返す。
func (r *repo) commit(files map[string]string) string {
	r.t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(r.dir, name), []byte(body), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", "commit")
	return r.git("rev-parse", "HEAD")
}

// commitExt は、実行すると greeting を出す拡張機能をコミットする。
func (r *repo) commitExt(greeting string) string {
	r.t.Helper()
	return r.commit(extFiles(greeting))
}

const testCommand = "modal-editor-ext-hello"

func extFiles(greeting string) map[string]string {
	return map[string]string{
		"go.mod":  "module example.com/hello\n\ngo 1.21\n",
		"main.go": fmt.Sprintf("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Print(%q) }\n", greeting),
		ManifestFileName: `{
  // JSONC で書いてよい
  "id": "hello",
  "command": "./` + testCommand + `",
  "activation": [{ "on": "command", "id": "hello.run" }],
  "contributes": { "commands": [{ "id": "hello.run" }] },
}
`,
	}
}

type env struct {
	opts   Options
	events []Event
}

func newEnv(t *testing.T) *env {
	t.Helper()
	data := t.TempDir()
	e := &env{opts: Options{
		ExtensionsDir: filepath.Join(data, "extensions"),
		LockPath:      filepath.Join(t.TempDir(), LockFileName),
		Env:           testEnv,
	}}
	e.opts.Progress = func(ev Event) { e.events = append(e.events, ev) }
	return e
}

func (e *env) sync(t *testing.T, specs ...Spec) []Outcome {
	t.Helper()
	e.events = nil
	out, err := Sync(context.Background(), specs, e.opts)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	return out
}

// wantOutcomes は、結果の名前と Action と Commit を比べる。commit が空なら Commit を比べない。
func wantOutcomes(t *testing.T, got []Outcome, want ...Outcome) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("結果 = %+v, want %+v", got, want)
	}
	for i, w := range want {
		g := got[i]
		if g.Name != w.Name || g.Action != w.Action || (w.Commit != "" && g.Commit != w.Commit) {
			t.Errorf("結果 %d = {%s %v %s %v}, want {%s %v %s}", i, g.Name, g.Action, g.Commit, g.Err, w.Name, w.Action, w.Commit)
		}
		if (g.Err != nil) != (w.Action == ActionFailed) {
			t.Errorf("結果 %d の Err = %v", i, g.Err)
		}
	}
}

// wantRuns は、入れた拡張機能の実行ファイルを実行し、出力が greeting であることを確かめる。
func (e *env) wantRuns(t *testing.T, name, greeting string) {
	t.Helper()
	out, err := exec.Command(filepath.Join(e.opts.ExtensionsDir, name, testCommand)).Output() //nolint:gosec // G204: テストで入れた実行ファイル
	if err != nil {
		t.Fatalf("%s を実行できない: %v", name, err)
	}
	if string(out) != greeting {
		t.Errorf("%s の出力 = %q, want %q", name, out, greeting)
	}
}

func (e *env) lock(t *testing.T) Lock {
	t.Helper()
	l, err := ReadLock(e.opts.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func (e *env) wantLock(t *testing.T, want map[string]LockEntry) {
	t.Helper()
	got := e.lock(t).Extensions
	if len(got) != len(want) {
		t.Fatalf("ロックファイル = %+v, want %+v", got, want)
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("ロックファイルの %s = %+v, want %+v", name, got[name], w)
		}
	}
}

// 取得してビルドし、宣言ファイルと実行ファイルを入れ、ロックファイルに記録する。
// 2 回目は、ロックファイルの記録と入れてあるものが同じなので、git も go も起動しない。
func TestSyncInstall(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	r.git("tag", "-a", "-m", "v1", "v1")
	e := newEnv(t)
	spec := Spec{Name: "hello", Repo: r.dir, Version: "v1"}

	wantOutcomes(t, e.sync(t, spec), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})
	e.wantRuns(t, "hello", "v1")
	manifest, err := os.ReadFile(filepath.Join(e.opts.ExtensionsDir, "hello", ManifestFileName))
	if err != nil || string(manifest) != extFiles("")[ManifestFileName] {
		t.Errorf("宣言ファイル = %q, %v", manifest, err)
	}
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "v1", Commit: c1}})
	if want := []Event{{"hello", PhaseFetch}, {"hello", PhaseBuild}, {"hello", PhaseInstall}}; fmt.Sprint(e.events) != fmt.Sprint(want) {
		t.Errorf("進み具合 = %v, want %v", e.events, want)
	}
	data, err := os.ReadFile(e.opts.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	wantText := fmt.Sprintf("{\n  \"version\": 1,\n  \"extensions\": {\n    \"hello\": {\n      \"repo\": %q,\n      \"version\": \"v1\",\n      \"commit\": %q\n    }\n  }\n}\n", r.dir, c1)
	if string(data) != wantText {
		t.Errorf("ロックファイルの中身 = %s, want %s", data, wantText)
	}

	e.opts.Git, e.opts.Go = "/nonexistent/git", "/nonexistent/go"
	wantOutcomes(t, e.sync(t, spec), Outcome{Name: "hello", Action: ActionUnchanged, Commit: c1})
	if len(e.events) != 0 {
		t.Errorf("進み具合 = %v, want なし", e.events)
	}
}

// 版を変えたら取得し直して入れ替える（FR-121）。ビルドに失敗したら、前の版とロックファイルの記録を残す。
func TestSyncUpdate(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	r.git("tag", "v1")
	c2 := r.commitExt("v2")
	r.git("tag", "v2")
	r.commit(map[string]string{"main.go": "package main\n\nfunc main() { undefined() }\n"})
	r.git("tag", "broken")
	e := newEnv(t)

	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"}), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v2"}), Outcome{Name: "hello", Action: ActionUpdated, Commit: c2})
	e.wantRuns(t, "hello", "v2")
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "v2", Commit: c2}})

	out := e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "broken"})
	wantOutcomes(t, out, Outcome{Name: "hello", Action: ActionFailed, Commit: c2})
	if !strings.Contains(out[0].Err.Error(), "undefined") {
		t.Errorf("Err = %v, want コンパイルの誤りを含む", out[0].Err)
	}
	e.wantRuns(t, "hello", "v2")
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "v2", Commit: c2}})

	out = e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "nosuch"})
	wantOutcomes(t, out, Outcome{Name: "hello", Action: ActionFailed, Commit: c2})
	if !strings.Contains(out[0].Err.Error(), `版 "nosuch"`) {
		t.Errorf("Err = %v", out[0].Err)
	}
	e.wantRuns(t, "hello", "v2")
	assertNoLeftovers(t, e)
}

// assertNoLeftovers は、途中のディレクトリが残っていないことを確かめる。
func assertNoLeftovers(t *testing.T, e *env) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(e.opts.ExtensionsDir), "extmgr", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("途中のディレクトリが残っている: %v", entries)
	}
}

// 入れ替えの途中（新しい版の名前の変更）で失敗したら、退けた前の版を戻す。
func TestSyncSwapFailureKeepsOld(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	r.git("tag", "v1")
	r.commitExt("v2")
	r.git("tag", "v2")
	e := newEnv(t)
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: "v1"}), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})

	s := newSyncer(e.opts)
	calls := 0
	s.rename = func(oldpath, newpath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected")
		}
		return os.Rename(oldpath, newpath)
	}
	out, err := s.sync(context.Background(), []Spec{{Name: "hello", Repo: r.dir, Version: "v2"}})
	if err != nil {
		t.Fatal(err)
	}
	wantOutcomes(t, out, Outcome{Name: "hello", Action: ActionFailed, Commit: c1})
	if calls != 3 {
		t.Errorf("名前の変更 = %d 回, want 3（退ける、入れる、戻す）", calls)
	}
	e.wantRuns(t, "hello", "v1")
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "v1", Commit: c1}})
	assertNoLeftovers(t, e)
}

// ブランチの版は、ロックファイルの記録のコミットを取り出す。Update なら取得元で解決し直す。
// 別の環境（ソースも入れたものも無い）でも、ロックファイルの記録のコミットを取り出す。コミットのハッシュも版にできる。
func TestSyncBranchAndLock(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("one")
	e := newEnv(t)
	spec := Spec{Name: "hello", Repo: r.dir, Version: "main"}
	wantOutcomes(t, e.sync(t, spec), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})

	c2 := r.commitExt("two")
	wantOutcomes(t, e.sync(t, spec), Outcome{Name: "hello", Action: ActionUnchanged, Commit: c1})
	e.wantRuns(t, "hello", "one")

	other := newEnv(t)
	other.opts.LockPath = e.opts.LockPath
	wantOutcomes(t, other.sync(t, spec), Outcome{Name: "hello", Action: ActionInstalled, Commit: c1})
	other.wantRuns(t, "hello", "one")

	e.opts.Update = true
	wantOutcomes(t, e.sync(t, spec), Outcome{Name: "hello", Action: ActionUpdated, Commit: c2})
	e.wantRuns(t, "hello", "two")
	e.wantLock(t, map[string]LockEntry{"hello": {Repo: r.dir, Version: "main", Commit: c2}})

	e.opts.Update = false
	wantOutcomes(t, e.sync(t, Spec{Name: "hello", Repo: r.dir, Version: c1[:12]}), Outcome{Name: "hello", Action: ActionUpdated, Commit: c1})
	e.wantRuns(t, "hello", "one")
}

// 設定に並べなくなったもののうち、Sync が入れたものを削除する（FR-122）。手で置いたものは消さず、上書きもしない。
func TestSyncRemove(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	c1 := r.commitExt("v1")
	e := newEnv(t)
	a := Spec{Name: "a", Repo: r.dir, Version: "main"}
	b := Spec{Name: "b", Repo: r.dir, Version: "main"}
	wantOutcomes(t, e.sync(t, a, b),
		Outcome{Name: "a", Action: ActionInstalled, Commit: c1},
		Outcome{Name: "b", Action: ActionInstalled, Commit: c1})
	e.wantRuns(t, "a", "v1")
	e.wantRuns(t, "b", "v1")

	manual := filepath.Join(e.opts.ExtensionsDir, "manual")
	if err := os.MkdirAll(manual, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manual, ManifestFileName), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 手で置いたものと同じ名前を並べても上書きしない。
	out := e.sync(t, a, Spec{Name: "manual", Repo: r.dir, Version: "main"})
	wantOutcomes(t, out,
		Outcome{Name: "a", Action: ActionUnchanged, Commit: c1},
		Outcome{Name: "manual", Action: ActionFailed},
		Outcome{Name: "b", Action: ActionRemoved})
	if _, err := os.Stat(filepath.Join(e.opts.ExtensionsDir, "b")); !os.IsNotExist(err) {
		t.Errorf("b が残っている: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.opts.ExtensionsDir), "extmgr", "src", "b")); !os.IsNotExist(err) {
		t.Errorf("b のソースが残っている: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(manual, ManifestFileName)); err != nil || string(data) != "{}" {
		t.Errorf("手で置いたものが変わった: %q, %v", data, err)
	}
	e.wantLock(t, map[string]LockEntry{"a": {Repo: r.dir, Version: "main", Commit: c1}})

	// ロックファイルに記録があっても、印の無いディレクトリは消さない。
	if err := os.Remove(filepath.Join(e.opts.ExtensionsDir, "a", MarkerFileName)); err != nil {
		t.Fatal(err)
	}
	wantOutcomes(t, e.sync(t), Outcome{Name: "a", Action: ActionKept})
	if _, err := os.Stat(filepath.Join(e.opts.ExtensionsDir, "a", testCommand)); err != nil {
		t.Errorf("a が消えた: %v", err)
	}
	e.wantLock(t, map[string]LockEntry{})
	if _, err := os.Stat(manual); err != nil {
		t.Errorf("手で置いたものが消えた: %v", err)
	}
}

// ビルドするものがあり、go が無ければ、何も変えずに ErrGoNotFound を返す。
func TestSyncGoNotFound(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	e := newEnv(t)
	e.opts.Go = filepath.Join(t.TempDir(), "go")
	_, err := Sync(context.Background(), []Spec{{Name: "hello", Repo: r.dir, Version: "main"}}, e.opts)
	if !errors.Is(err, ErrGoNotFound) {
		t.Fatalf("err = %v, want ErrGoNotFound", err)
	}
	if _, err := os.Stat(filepath.Dir(e.opts.ExtensionsDir) + "/extmgr"); !os.IsNotExist(err) {
		t.Errorf("作業のディレクトリを作った: %v", err)
	}
	if _, err := os.Stat(e.opts.LockPath); !os.IsNotExist(err) {
		t.Errorf("ロックファイルを作った: %v", err)
	}
}

// 誤りのある Spec は失敗にし、他は進める。読めないロックファイルは書き換えずに止める。
func TestSyncInvalid(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	out := e.sync(t,
		Spec{Name: "../x", Repo: "r", Version: "v"},
		Spec{Name: "x", Repo: "--upload-pack=touch", Version: "v"},
		Spec{Name: "y", Repo: "r", Version: "-v"})
	wantOutcomes(t, out,
		Outcome{Name: "../x", Action: ActionFailed},
		Outcome{Name: "x", Action: ActionFailed},
		Outcome{Name: "y", Action: ActionFailed})

	if err := os.WriteFile(e.opts.LockPath, []byte(`{"version": 2, "extensions": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), nil, e.opts); err == nil || !strings.Contains(err.Error(), "形式の版 2") {
		t.Errorf("err = %v", err)
	}
	if _, err := Sync(context.Background(), nil, Options{LockPath: e.opts.LockPath}); err == nil {
		t.Error("ExtensionsDir が空でも誤りにならない")
	}
}

// 取り消すと、残りを失敗にし、削除をせず、ctx.Err() を返す。
func TestSyncCanceled(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.commitExt("v1")
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := Sync(ctx, []Spec{{Name: "hello", Repo: r.dir, Version: "main"}}, e.opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	wantOutcomes(t, out, Outcome{Name: "hello", Action: ActionFailed})
}

func TestReadManifestCommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		command string
		ok      bool
	}{
		{"./x", true}, {"x", true}, {"../x", false}, {"bin/x", false}, {"/usr/bin/x", false}, {"./" + MarkerFileName, false},
	} {
		dir := t.TempDir()
		m := map[string]any{
			"id": "x", "command": tc.command,
			"activation":  []map[string]string{{"on": "filetype", "pattern": "go"}},
			"contributes": map[string]any{},
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, ManifestFileName), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err = readManifest(dir)
		if (err == nil) != tc.ok {
			t.Errorf("command %q: err = %v, want ok=%v", tc.command, err, tc.ok)
		}
	}
}
