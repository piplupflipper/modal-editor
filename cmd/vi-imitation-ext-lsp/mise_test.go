//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// missingCommand は、PATH に無い言語サーバーの名前である。偽の mise は、mise exec のときだけ、この名前で fakels を見せる。
const missingCommand = "fakels-mise-278"

// testTool は、テストで使う mise の道具の ID である。
const testTool = "test:fakels@1.2.3"

// fakeMiseScript は、偽の mise である。%[1]s は状態のディレクトリで、次のとおりに動く。
//
//   - mise の名前でなく呼ばれたら（shim へのシンボリックリンクから）、"shim <名前> <引数>" を calls に書き足す。
//     versionset があれば bin の同じ名前のコマンドに置き換わり、無ければ本物の shim と同じく "No version is set" で終了コード 1
//   - それ以外は、呼ばれた引数を calls に 1 行で書き足す
//   - which <コマンド>: versionset があれば終了コード 0、無ければ終了コード 1
//   - where: installed があれば終了コード 0、無ければ終了コード 1
//   - install: fail があれば、その中身を標準エラー出力に書いて終了コード 1。
//     無ければ、進み具合の行を書き、release ができるまで待ってから installed を作る。待っている間、自分の pid を install.pid に書いておく。
//     待つ前に、本物の mise が npm を起動するのと同じく、別のプロセスグループ（setsid）で孫プロセスを起こし、その pid を grandchild.pid に書く。
//     SIGINT を受け取ると、本物の mise と同じく孫プロセスを終わらせてから終わる
//   - exec <道具の ID> -- <コマンド>...: bin を PATH の先頭に足し、コマンドに置き換わる（本物の mise exec と同じく exec する）
const fakeMiseScript = `#!/bin/sh
state='%[1]s'
name=$(basename "$0")
if [ "$name" != mise ]; then
	echo "shim $name $*" >> "$state/calls"
	if [ -f "$state/versionset" ]; then exec "$state/bin/$name" "$@"; fi
	echo "mise ERROR No version is set for shim: $name" >&2
	exit 1
fi
echo "$*" >> "$state/calls"
case "$1" in
which)
	if [ -f "$state/versionset" ]; then echo "$state/bin/$2"; exit 0; fi
	echo "mise ERROR No version is set for shim: $2" >&2
	exit 1
	;;
where)
	if [ -f "$state/installed" ]; then echo "$state/installs/$2"; exit 0; fi
	echo "mise ERROR $2 not installed" >&2
	exit 1
	;;
install)
	if [ -f "$state/fail" ]; then cat "$state/fail" >&2; exit 1; fi
	setsid sleep 300 < /dev/null > /dev/null 2>&1 &
	child=$!
	trap 'kill -9 $child; exit 130' INT
	echo $child > "$state/grandchild.pid"
	echo $$ > "$state/install.pid"
	echo "mise $2 [1/2] download" >&2
	while [ ! -f "$state/release" ]; do sleep 0.02; done
	kill -9 $child
	echo "mise $2 [2/2] extract" >&2
	: > "$state/installed"
	exit 0
	;;
exec)
	shift 3
	PATH="$state/bin:$PATH" exec "$@"
	;;
esac
exit 2
`

// fakeMise は、偽の mise の実行ファイルと状態のディレクトリである。
type fakeMise struct {
	t *testing.T
	// path は、偽の mise の実行ファイル（名前は mise）である。
	path  string
	state string
}

// newFakeMise は、偽の mise を作る。mise exec のときは、missingCommand の名前で fakels を起動できる。
func newFakeMise(t *testing.T) *fakeMise {
	t.Helper()
	dir := t.TempDir()
	m := &fakeMise{t: t, path: filepath.Join(dir, "mise"), state: filepath.Join(dir, "state")}
	if err := os.MkdirAll(filepath.Join(m.state, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeLS, filepath.Join(m.state, "bin", missingCommand)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.path, fmt.Appendf(nil, fakeMiseScript, m.state), 0o700); err != nil { //nolint:gosec // テストの一時ディレクトリに置く偽の mise は実行できなければならない
		t.Fatal(err)
	}
	return m
}

// shimDir は、missingCommand の名前の mise の shim（偽の mise へのシンボリックリンク）を置いたディレクトリを作って返す。
func (m *fakeMise) shimDir() string {
	m.t.Helper()
	dir := filepath.Join(filepath.Dir(m.path), "shims")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.t.Fatal(err)
	}
	if err := os.Symlink(m.path, filepath.Join(dir, missingCommand)); err != nil {
		m.t.Fatal(err)
	}
	return dir
}

// pid は、状態のディレクトリのファイル name ができるまで待ち、そこに書かれた pid を返す。
func (m *fakeMise) pid(name string) int {
	m.t.Helper()
	pid, err := strconv.Atoi(strings.TrimSpace(m.waitFile(name)))
	if err != nil {
		m.t.Fatal(err)
	}
	return pid
}

// touch は、状態のディレクトリにファイル name を作る。
func (m *fakeMise) touch(name, text string) {
	m.t.Helper()
	if err := os.WriteFile(filepath.Join(m.state, name), []byte(text), 0o600); err != nil {
		m.t.Fatal(err)
	}
}

// calls は、偽の mise が呼ばれた引数の列を返す。
func (m *fakeMise) calls() []string {
	b, err := os.ReadFile(filepath.Join(m.state, "calls"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// waitFile は、状態のディレクトリにファイル name ができるまで待ち、中身を返す。
func (m *fakeMise) waitFile(name string) string {
	m.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(filepath.Join(m.state, name)); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(10 * time.Millisecond)
	}
	m.t.Fatalf("%s did not appear", name)
	return ""
}

// noMessage は、wait の間に message/show が届かないことを確かめる。
func (h *host) noMessage(wait time.Duration) {
	h.t.Helper()
	select {
	case m := <-h.msgs:
		h.t.Errorf("unexpected message/show: %+v", m)
	case <-time.After(wait):
	}
}

// waitMessage は、cond を満たす message/show が届くまで待ち、それまでに届いたものを含めて返す。
func (h *host) waitMessage(what string, cond func(ext.MessageShowParams) bool) []ext.MessageShowParams {
	h.t.Helper()
	var seen []ext.MessageShowParams
	for {
		m := receive(h.t, h.msgs, what)
		seen = append(seen, m)
		if cond(m) {
			return seen
		}
	}
}

// startLog は、fakels のログの start の行（自分と親の pid）を読む。
func (h *host) startLog() (pid, ppid int) {
	h.t.Helper()
	e := h.waitLog("start", func(e logEntry) bool { return e.Kind == "start" })
	v := decode[map[string]int](h.t, e.Value)
	return v["pid"], v["ppid"]
}

// TestMiseCommandInPath は、コマンドが PATH にあれば、mise の道具の ID と自動で入れる設定があっても、mise を呼ばずにそのまま起動することを確かめる。
//
// req: FR-143
func TestMiseCommandInPath(t *testing.T) {
	m := newFakeMise(t)
	h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"mise": testTool, "autoInstall": true}})
	h.open(1, "a.go", "a ERR")
	checkDiagnostics(t, h.diagnosticsAt(1, 1), "a ERR")
	if c := m.calls(); c != nil {
		t.Errorf("mise calls = %q, want none", c)
	}
	h.noMessage(100 * time.Millisecond)
}

// TestMiseGuideOnce は、自動で入れない設定（既定）では、コマンドが見つからないときに入れ方を一度だけ案内し、mise を呼ばないこと、
// 同じコマンドと道具の別のファイルの種類でも案内を繰り返さないこと、利用者が入れた後に次のファイルを開くと起動することを確かめる。
//
// req: FR-143
func TestMiseGuideOnce(t *testing.T) {
	m := newFakeMise(t)
	same := map[string]any{"command": missingCommand, "mise": testTool}
	h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: same, secondFileType: "go2", secondExtra: same})
	h.open(1, "a.go", "a ERR")
	msg := receive(t, h.msgs, "message/show")
	want := fmt.Sprintf("言語サーバー %s が見つからない。mise use -g %s で入れられる", missingCommand, testTool)
	if msg.Level != ext.MessageWarning || msg.Text != want {
		t.Errorf("guide = %+v, want warning %q", msg, want)
	}
	// 同じファイルの種類の別のファイルと、同じ言語サーバーの別のファイルの種類では、探し直すが案内しない。
	h.open(2, "b.go", "b")
	if err := h.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 3, Path: filepath.Join(h.dir, "c.go2"), FileType: "go2", Version: 1, Text: "c"}); err != nil {
		t.Fatal(err)
	}
	h.noMessage(300 * time.Millisecond)
	if c := m.calls(); c != nil {
		t.Errorf("mise calls = %q, want none", c)
	}

	// 利用者が入れた（PATH に置いた）後は、次にファイルを開いたときに起動し、開いている文書をすべて送る。
	t.Setenv("PATH", filepath.Join(m.state, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	h.open(4, "d.go", "d ERR")
	// 開いている文書の didOpen の順は決まっていないので、届いた順に確かめる。
	got := map[ext.BufferID]bool{}
	for len(got) < 2 {
		p := receive(t, h.diags, "diagnostic/set")
		switch p.Buffer {
		case 1:
			checkDiagnostics(t, p, "a ERR")
		case 4:
			checkDiagnostics(t, p, "d ERR")
		default:
			continue
		}
		got[p.Buffer] = true
	}
}

// TestMiseAutoInstall は、自動で入れる設定（settings の autoInstall）で、mise where で入っていないことを確かめ、mise install で入れ、
// 入れている間も ping に答え、進み具合を知らせ、入れた後に mise exec で起動することを確かめる。
//
// req: FR-143
func TestMiseAutoInstall(t *testing.T) {
	m := newFakeMise(t)
	h := startHost(t, hostOptions{
		maxRestarts: -1, mise: m.path,
		extra:    map[string]any{"command": missingCommand, "mise": testTool},
		settings: map[string]any{"autoInstall": true},
	})
	h.open(1, "a.go", "a ERR")
	first := receive(t, h.msgs, "message/show")
	if first.Level != ext.MessageInfo || !strings.Contains(first.Text, "mise で "+testTool+" を入れている") {
		t.Errorf("first message = %+v", first)
	}
	m.waitFile("install.pid")
	// 入れている間（mise install が release を待つ間）も、ping に答え、文書の変更を受け付ける。
	for range 5 {
		h.ping(time.Second)
	}
	h.change(1, edit(0, 0, 0, 0, "x "))
	progress := receive(t, h.msgs, "message/show")
	if progress.Level != ext.MessageInfo || progress.Text != "mise: mise "+testTool+" [1/2] download" {
		t.Errorf("progress = %+v", progress)
	}
	m.touch("release", "")
	seen := h.waitMessage("installed", func(p ext.MessageShowParams) bool { return strings.Contains(p.Text, "を入れた") })
	for _, p := range seen {
		if p.Level != ext.MessageInfo {
			t.Errorf("message while installing = %+v, want info", p)
		}
	}
	checkDiagnostics(t, h.diagnosticsAt(1, 2), "x a ERR")
	calls := m.calls()
	wantExec := fmt.Sprintf("exec %s -- %s -log %s", testTool, missingCommand, h.logPath)
	if len(calls) != 3 || calls[0] != "where "+testTool || calls[1] != "install "+testTool || !strings.HasPrefix(calls[2], wantExec) {
		t.Errorf("mise calls = %q, want where, install, %q", calls, wantExec)
	}
	// mise exec は言語サーバーに置き換わるので、言語サーバーの親は拡張機能である。
	if _, ppid := h.startLog(); ppid != os.Getpid() {
		t.Errorf("parent of the language server = %d, want the extension %d", ppid, os.Getpid())
	}
}

// TestMiseAlreadyInstalled は、自動で入れる設定（servers の autoInstall）で、道具がもう入っていれば mise install を呼ばず、
// 進み具合も知らせずに mise exec で起動すること、言語サーバーが異常終了したら mise exec で起動し直すことを確かめる。
//
// req: FR-143
func TestMiseAlreadyInstalled(t *testing.T) {
	m := newFakeMise(t)
	m.touch("installed", "")
	h := startHost(t, hostOptions{maxRestarts: 1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}})
	h.open(1, "a.go", "a ERR")
	checkDiagnostics(t, h.diagnosticsAt(1, 1), "a ERR")
	h.change(1, edit(0, 0, 0, 0, "CRASH "))
	h.diagnosticsAt(1, 2) // 消した診断
	if msg := receive(t, h.msgs, "message/show"); !strings.Contains(msg.Text, "再起動する") {
		t.Errorf("message = %+v, want a restart", msg)
	}
	h.diagnosticsAt(1, 2) // 再起動した言語サーバーの診断
	var kinds []string
	for _, c := range m.calls() {
		kinds = append(kinds, strings.Fields(c)[0])
	}
	if !slices.Equal(kinds, []string{"where", "exec", "where", "exec"}) {
		t.Errorf("mise calls = %q", m.calls())
	}
}

// TestMiseMissing は、自動で入れる設定でも、mise が見つからなければ、入れ方の案内に戻ることを確かめる。
//
// req: FR-143
func TestMiseMissing(t *testing.T) {
	h := startHost(t, hostOptions{
		maxRestarts: -1, mise: filepath.Join(t.TempDir(), "mise"),
		extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true},
	})
	h.open(1, "a.go", "a")
	msg := receive(t, h.msgs, "message/show")
	if msg.Level != ext.MessageWarning || !strings.Contains(msg.Text, "mise が見つからない") || !strings.Contains(msg.Text, "mise use -g "+testTool) {
		t.Errorf("guide = %+v", msg)
	}
	h.open(2, "b.go", "b")
	h.noMessage(300 * time.Millisecond)
}

// TestMiseInstallFails は、mise install が失敗したら（信頼していない mise.toml で止まったときなど）、mise の出力を含めて知らせ、
// 起動せず、次のファイルを開いても入れ直さないことを確かめる。
//
// req: FR-143
func TestMiseInstallFails(t *testing.T) {
	m := newFakeMise(t)
	// mise 2026.5.2 が、信頼していない mise.toml のあるディレクトリで出す行（パスは短くした）。
	untrusted := "mise ERROR error parsing config file: ~/p/mise.toml\n" +
		"mise ERROR Config files in ~/p/mise.toml are not trusted.\n" +
		"Trust them with `mise trust`.\n" +
		"mise ERROR Run with --verbose or MISE_VERBOSE=1 for more information\n"
	m.touch("fail", untrusted)
	h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}})
	h.open(1, "a.go", "a")
	seen := h.waitMessage("failure", func(p ext.MessageShowParams) bool { return p.Level == ext.MessageError })
	got := seen[len(seen)-1].Text
	want := "mise で " + testTool + " を入れられない: mise ERROR Config files in ~/p/mise.toml are not trusted. Trust them with `mise trust`."
	if got != want {
		t.Errorf("failure = %q, want %q", got, want)
	}
	h.open(2, "b.go", "b")
	h.noMessage(300 * time.Millisecond)
	if calls := m.calls(); !slices.Equal(calls, []string{"where " + testTool, "install " + testTool}) {
		t.Errorf("mise calls = %q", calls)
	}
}

// TestMiseInstallStop は、mise install の途中で拡張機能が終わるとき、mise に SIGINT を送り、mise が別のプロセスグループの孫プロセス
// （本物の mise の npm に当たる）を終わらせてから終わるのを待って止まること、本体の期限の中で止まること、失敗を知らせないことを確かめる。
//
// req: FR-143
func TestMiseInstallStop(t *testing.T) {
	m := newFakeMise(t)
	h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}})
	h.open(1, "a.go", "a")
	grandchild := m.pid("grandchild.pid")
	pid := m.pid("install.pid")
	start := time.Now()
	h.x.stop()
	if d := time.Since(start); d >= stopTimeout {
		t.Errorf("stop took %s while installing", d)
	}
	// stop は mise が終わるまで待つので、戻った時点で mise も孫プロセスも終わっている。
	if !gone(pid) || !gone(grandchild) {
		t.Errorf("after stop: mise gone = %v, grandchild gone = %v; want both gone", gone(pid), gone(grandchild))
	}
	waitGone(t, "mise install", pid)
	waitGone(t, "the grandchild of mise install", grandchild)
	for {
		select {
		case msg := <-h.msgs:
			if msg.Level == ext.MessageError {
				t.Errorf("message after stop = %+v", msg)
			}
			continue
		case <-time.After(300 * time.Millisecond):
		}
		break
	}
}

// gone は、pid のプロセスが無いか、終わってゾンビになっていることを返す。
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// 3 つ目の欄が状態である。2 つ目の欄（実行ファイルの名前）は括弧で囲まれ、空白を含みうるので、最後の ')' の後から読む。
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

// waitGone は、pid のプロセスが終わるまで待つ。
func waitGone(t *testing.T, what string, pid int) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if gone(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("%s (pid %d) remains", what, pid)
}

// TestKillLeavesNoServer は、拡張機能のプロセスだけが SIGKILL で終わっても、言語サーバーが残らないことを確かめる（Pdeathsig）。
// 言語サーバーは、接続が閉じても終わらないもの（fakels の -linger）にする。
// PATH のコマンドをそのまま起動したときと、mise exec で起動したときの両方を確かめる。
//
// req: FR-30
func TestKillLeavesNoServer(t *testing.T) {
	for _, viaMise := range []bool{false, true} {
		t.Run(fmt.Sprintf("viaMise=%v", viaMise), func(t *testing.T) {
			m := newFakeMise(t)
			m.touch("installed", "")
			dir := t.TempDir()
			logPath := filepath.Join(dir, "fakels.log")
			server := map[string]any{"command": fakeLS, "args": []string{"-log", logPath, "-linger"}}
			if viaMise {
				server["command"] = missingCommand
				server["mise"] = testTool
				server["autoInstall"] = true
			}
			cmd := startExtensionProcess(t, m, dir, server)
			e := (&host{t: t}).waitLogFile(logPath, "start", func(e logEntry) bool { return e.Kind == "start" })
			v := decode[map[string]int](t, e.Value)
			if v["ppid"] != cmd.Process.Pid {
				t.Errorf("parent of the language server = %d, want the extension %d", v["ppid"], cmd.Process.Pid)
			}
			// 本体のプロセスグループへの SIGKILL ではなく、拡張機能のプロセスだけを終わらせる。
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			waitGone(t, "language server", v["pid"])
		})
	}
}

// startExtensionProcess は、テストの実行ファイルを拡張機能として、本体と同じく新しいプロセスグループの先頭で起動する。
// PATH の先頭には偽の mise（m）のディレクトリを足す。go の言語サーバーを server の設定にして initialize し、
// dir の a.go を開く。拡張機能のプロセスは、テストの終わりにプロセスグループごと強制終了する。
func startExtensionProcess(t *testing.T, m *fakeMise, dir string, server map[string]any) *exec.Cmd {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"servers": map[string]any{"go": server}})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0]) //nolint:gosec // テストの実行ファイルを拡張機能として動かす
	cmd.Env = append(os.Environ(), envRunExtension+"=1",
		"PATH="+filepath.Dir(m.path)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	conn := rpc.NewConn(stdout, stdin, rpc.HandlerFuncs{})
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = conn.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var res ext.InitializeResult
	if err := conn.Call(ctx, ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion, Settings: raw}, &res); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	path := filepath.Join(dir, "a.go")
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Path: path, FileType: "go", Version: 1, Text: "a"}); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// TestKillWhileInstalling は、mise install の途中で、本体が拡張機能のプロセスグループへ SIGKILL を送っても（server への kill -9 や
// 本体の強制終了の形）、拡張機能のプロセスだけが SIGKILL で終わっても、mise が別のプロセスグループで起こした孫プロセス
// （本物の mise の npm に当たる）が孤児で残らないことを確かめる（PR #281 のレビューの必須 1）。
// mise は Pdeathsig の SIGINT を受け取り、孫プロセスを終わらせてから終わる。
//
// req: FR-143
func TestKillWhileInstalling(t *testing.T) {
	for _, group := range []bool{true, false} {
		t.Run(fmt.Sprintf("group=%v", group), func(t *testing.T) {
			m := newFakeMise(t)
			server := map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}
			cmd := startExtensionProcess(t, m, t.TempDir(), server)
			grandchild := m.pid("grandchild.pid")
			pid := m.pid("install.pid")
			target := cmd.Process.Pid
			if group {
				target = -target
			}
			if err := syscall.Kill(target, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			waitGone(t, "mise install", pid)
			waitGone(t, "the grandchild of mise install", grandchild)
		})
	}
}

// TestMiseShim は、PATH で見つかったコマンドが mise の shim で、版が決まっていない（mise which が失敗する）とき、
// PATH のものとして起動せず、mise の道具の ID の扱いへ進むことを確かめる（PR #281 のレビューの必須 2）。
// 自動で入れる設定なら mise exec で起動し、自動で入れない設定なら案内を出す。版が決まっていれば、shim をそのまま起動する。
//
// req: FR-143
func TestMiseShim(t *testing.T) {
	t.Run("autoInstall", func(t *testing.T) {
		m := newFakeMise(t)
		m.touch("installed", "")
		t.Setenv("PATH", m.shimDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}})
		h.open(1, "a.go", "a ERR")
		checkDiagnostics(t, h.diagnosticsAt(1, 1), "a ERR")
		var kinds []string
		for _, c := range m.calls() {
			kinds = append(kinds, strings.Fields(c)[0])
		}
		if !slices.Equal(kinds, []string{"which", "where", "exec"}) {
			t.Errorf("mise calls = %q, want which, where, exec (not the shim)", m.calls())
		}
	})
	t.Run("guide", func(t *testing.T) {
		m := newFakeMise(t)
		shims := m.shimDir()
		t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))
		h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool}})
		h.open(1, "a.go", "a")
		msg := receive(t, h.msgs, "message/show")
		// 入れ方を前に置き、長くなりうる shim のパスは後ろに置く（画面の幅で切れても入れ方が見えるように）。
		want := fmt.Sprintf("言語サーバー %s が見つからない。mise use -g %s で入れられる。PATH の %s は mise の shim で、版が決まっていない",
			missingCommand, testTool, filepath.Join(shims, missingCommand))
		if msg.Level != ext.MessageWarning || msg.Text != want {
			t.Errorf("guide = %+v, want %q", msg, want)
		}
		if calls := m.calls(); !slices.Equal(calls, []string{"which " + missingCommand}) {
			t.Errorf("mise calls = %q", calls)
		}
	})
	t.Run("versionSet", func(t *testing.T) {
		m := newFakeMise(t)
		m.touch("versionset", "")
		t.Setenv("PATH", m.shimDir()+string(os.PathListSeparator)+os.Getenv("PATH"))
		h := startHost(t, hostOptions{maxRestarts: -1, mise: m.path, extra: map[string]any{"command": missingCommand, "mise": testTool, "autoInstall": true}})
		h.open(1, "a.go", "a ERR")
		checkDiagnostics(t, h.diagnosticsAt(1, 1), "a ERR")
		calls := m.calls()
		if len(calls) != 2 || calls[0] != "which "+missingCommand || !strings.HasPrefix(calls[1], "shim "+missingCommand+" -log ") {
			t.Errorf("mise calls = %q, want which and the shim", calls)
		}
	})
}
