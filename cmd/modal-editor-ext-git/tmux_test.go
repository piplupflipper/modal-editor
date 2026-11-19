package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testTmux は、テスト用に tmux -L で立てた別の tmux のサーバーである。利用者の tmux には触らない。
type testTmux struct {
	t    *testing.T
	bin  string
	name string
	env  []string
}

func (ts *testTmux) run(args ...string) string {
	ts.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ts.bin, append([]string{"-L", ts.name, "-f", "/dev/null"}, args...)...) //nolint:gosec // テスト用の tmux
	cmd.Env = ts.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		ts.t.Fatalf("tmux %v: %v: %s", args, err, out.String())
	}
	return strings.TrimSpace(out.String())
}

// waitPane は、ペインの画面が cond を満たすまで待ち、その画面を返す。
func (ts *testTmux) waitPane(pane, what string, cond func(screen string) bool) string {
	ts.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		s := ts.run("capture-pane", "-p", "-t", pane)
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			ts.t.Fatalf("ペイン %s が %s にならない。画面:\n%s", pane, what, s)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (ts *testTmux) panes() []string {
	ts.t.Helper()
	return strings.Fields(ts.run("list-panes", "-a", "-F", "#{pane_id}"))
}

// buildDist は、本体と git の拡張機能を、配布物と同じ並び（<bin>/modal-editor と <bin>/extensions/git/）でビルドし、本体のパスを返す。
func buildDist(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	ext := filepath.Join(bin, "extensions", "git")
	for _, b := range []struct{ out, pkg string }{
		{filepath.Join(bin, "modal-editor"), "../modal-editor"},
		{filepath.Join(ext, "modal-editor-ext-git"), "."},
	} {
		out, err := exec.Command("go", "build", "-o", b.out, b.pkg).CombinedOutput() //nolint:gosec // テストで使う実行ファイルを一時ディレクトリへビルドする
		if err != nil {
			t.Fatalf("build %s: %v\n%s", b.pkg, err, out)
		}
	}
	manifest, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ext, "manifest.json"), manifest, 0o600); err != nil { //nolint:gosec // テストの一時ディレクトリへ、リポジトリの宣言ファイルを写す
		t.Fatal(err)
	}
	return filepath.Join(bin, "modal-editor")
}

// testEnv は、tmux のサーバーとエディタに渡す環境である。
// 利用者の端末多重化ソフト、エディタの server、git のリポジトリと設定、エディタの設定と拡張機能の置き場所を引き継がない。
func testEnv(t *testing.T) []string {
	t.Helper()
	// XDG_RUNTIME_DIR には server のソケットを置くので、ソケットのパスの長さの上限（基本設計 1.2）に収まる短いパスにする。
	// os.MkdirTemp は権限 0700 で作る（server のディレクトリの検査に通る）。
	runtime, err := os.MkdirTemp("", "meg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	home := t.TempDir()
	drop := []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_SOCKET_PATH", "MODAL_EDITOR_SOCKET", "HOME", "TERM"}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "XDG_") || slices.Contains(drop, name)
	})
	return append(env, "HOME="+home, "TERM=xterm-256color", "XDG_RUNTIME_DIR="+runtime,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_DATA_HOME="+filepath.Join(home, "data"),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

// TestDiffHeadInTmux は、tmux（tmux -L の別のサーバー）の中で、配布物と同じ並びの本体と git の拡張機能を起動し、
// 一時の git のリポジトリのファイルで <Leader>gd（git.diffHead）を打つと、HEAD の版が、編集を禁止したファイルでないバッファとして
// 右に分割したペインの新しい編集の client のタブに開き、ファイルのタブと見比べられることを確かめる（FR-104）。
//
// req: FR-104
func TestDiffHeadInTmux(t *testing.T) {
	if testing.Short() {
		t.Skip("-short では実物の tmux を使うテストを飛ばす")
	}
	isolateGit(t)
	tmuxBin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux が無い")
	}
	editor := buildDist(t)
	repo := newRepo(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	writeTestFiles(t, repo, map[string]string{"a.txt": "one\nTWO\nthree\nfour\n"})

	ts := &testTmux{t: t, bin: tmuxBin, name: fmt.Sprintf("modal-editor-git-test-%d-%d", os.Getpid(), time.Now().UnixNano()), env: testEnv(t)}
	t.Cleanup(func() {
		q := exec.Command(tmuxBin, "-L", ts.name, "display-message", "-p", "#{socket_path}") //nolint:gosec // テスト用の tmux
		q.Env = ts.env
		sock, _ := q.Output()
		k := exec.Command(tmuxBin, "-L", ts.name, "kill-server") //nolint:gosec // テスト用の tmux
		k.Env = ts.env
		_ = k.Run()
		if s := strings.TrimSpace(string(sock)); s != "" {
			_ = os.Remove(s)
		}
	})
	// 最初のペインは、エディタが終わっても window を残すための待つだけのもの。
	p0 := ts.run("new-session", "-d", "-s", "t", "-x", "200", "-y", "30", "-c", repo, "-P", "-F", "#{pane_id}", "sleep 600")
	p1 := ts.run("split-window", "-d", "-h", "-t", p0, "-c", repo, "-P", "-F", "#{pane_id}", fmt.Sprintf("'%s' a.txt", editor))
	ts.waitPane(p1, "a.txt を開いた画面", func(s string) bool { return strings.Contains(s, "TWO") && strings.Contains(s, "a.txt") })

	ts.run("send-keys", "-t", p1, "-l", " gd")
	var p2 string
	deadline := time.Now().Add(15 * time.Second)
	for p2 == "" {
		for _, p := range ts.panes() {
			if p != p0 && p != p1 {
				p2 = p
			}
		}
		if p2 == "" {
			if time.Now().After(deadline) {
				t.Fatalf("HEAD の版のペインが開かない。画面:\n%s", ts.run("capture-pane", "-p", "-t", p1))
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// HEAD の版は、名前が HEAD:a.txt の、編集を禁止したバッファ（[RO]）になる。
	// ファイルにだけある four の行の位置には、詰め物の行（- で埋めた行）を描く。
	head := ts.waitPane(p2, "HEAD の版の画面", func(s string) bool {
		return strings.Contains(s, "HEAD:a.txt") && strings.Contains(s, "[RO]") && strings.Contains(s, "two") && hasFillerLine(s)
	})
	if strings.Contains(head, "TWO") || strings.Contains(head, "four") {
		t.Errorf("HEAD の版のペインに作業ツリーの行が出た:\n%s", head)
	}
	ts.waitPane(p1, "見比べを始めた知らせ", func(s string) bool { return strings.Contains(s, "見比べます") })

	// 2 行目で、HEAD の版へは dp で送れず（E21）、do で HEAD の内容を取り込める（基本設計 6.8）。
	ts.run("send-keys", "-t", p1, "-l", "2Gdp")
	ts.waitPane(p1, "dp を断った画面", func(s string) bool { return strings.Contains(s, "E21") })
	ts.run("send-keys", "-t", p1, "-l", "do")
	ts.waitPane(p1, "do で取り込んだ画面", func(s string) bool { return strings.Contains(s, "two") && !strings.Contains(s, "TWO") })

	// HEAD の版のタブを閉じると、その client は終わり、ファイルの側には見比べが終わったことを知らせる。
	ts.run("send-keys", "-t", p2, ":q", "Enter")
	ts.waitPane(p1, "見比べの終わりの知らせ", func(s string) bool { return strings.Contains(s, "見比べが終わりました") })
	ts.run("send-keys", "-t", p1, ":q!", "Enter")
	deadline = time.Now().Add(15 * time.Second)
	for len(ts.panes()) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("エディタのペインが閉じない: %v", ts.panes())
		}
		time.Sleep(50 * time.Millisecond)
	}
	// ファイルは書き出していないので、作業ツリーの変更はそのまま残る。
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "one\nTWO\nthree\nfour\n" {
		t.Errorf("a.txt = %q", b)
	}
}

// hasFillerLine は、画面に、- だけで埋めた行（見比べの詰め物の行）があるかを返す。
func hasFillerLine(screen string) bool {
	for l := range strings.Lines(screen) {
		l = strings.TrimSpace(l)
		if len(l) >= 10 && strings.Trim(l, "-") == "" {
			return true
		}
	}
	return false
}
