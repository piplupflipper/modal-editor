package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitShell は、送ったキーを処理し、外部のコマンドが終わって結果を入れ終えるまで待つ。
func (h *harness) waitShell() {
	h.t.Helper()
	h.eventually("外部のコマンドの終わり", func() bool {
		return h.e.shell == nil && h.e.forwarded.Load() == h.sent && h.e.keys.empty()
	})
}

// waitFile は、子プロセスが path を作るまで待つ（子が動き出したことを知るため）。
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("子プロセスが %s を作らない", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// :{range}! と :r ! は、シェルの子プロセスとパイプでつないで実行し、作業ディレクトリと Config.ChildEnv を渡す。
// 結果の細かい形（カーソル、アンドゥ、通知）は internal/command の TestFilterVim で Vim 9.1 と突き合わせている。
//
// req: FR-81
func TestShellFilterAndRead(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "c\nbb\na\n"})
	h := start(t, Config{WorkDir: dir, ChildEnv: []string{"MODAL_EDITOR_TEST_VALUE=xyz"}}, "a.txt")
	h.keys(":2,3!tr a-z A-Z<CR>")
	h.waitShell()
	if got := h.text(); got != "c\nBB\nA" {
		t.Fatalf(":2,3!tr: text = %q", got)
	}
	h.keys(":$r !pwd; echo $MODAL_EDITOR_TEST_VALUE<CR>")
	h.waitShell()
	if got, want := h.text(), "c\nBB\nA\n"+dir+"\nxyz"; got != want {
		t.Errorf(":r !pwd: text = %q, want %q", got, want)
	}
	// 2 回の変更は、それぞれアンドゥの 1 単位である。
	h.keys("u")
	h.settle()
	if got := h.text(); got != "c\nBB\nA" {
		t.Errorf("u: text = %q", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "c\nbb\na" {
		t.Errorf("uu: text = %q", got)
	}
	h.keys(":1!exit 4<CR>")
	h.waitShell()
	if got := h.message(); got != "シェルが 4 を返しました" {
		t.Errorf(":1!exit 4: message = %q", got)
	}
}

// 外部のコマンドの実行中もイベントループは動き、キーは終わるまで処理せずに残して、終わってから処理する。
//
// req: FR-81
func TestShellDoesNotBlockLoop(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":r !touch started; while [ ! -e go ]; do sleep 0.01; done; echo x<CR>")
	waitFile(t, filepath.Join(dir, "started"))
	// イベントループが止まっていれば、do（inspect）は戻らない。
	var running bool
	h.e.do(func() { running = h.e.shell != nil })
	if !running {
		t.Fatal("実行中の印が無い")
	}
	h.keys("dd")
	time.Sleep(50 * time.Millisecond)
	if got := h.text(); got != "a" {
		t.Errorf("実行中のキーが処理された: text = %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.waitShell()
	// :r で x が入ってカーソルが 2 行目へ移り、その後に残しておいた dd が x の行を消す。
	if got := h.text(); got != "a" {
		t.Errorf("text = %q, want %q", got, "a")
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "a\nx" {
		t.Errorf("u: text = %q", got)
	}
}

// <C-c> は、外部のコマンドを終わらせ、バッファを変えない。プロセスグループから抜けた子孫がパイプを持っていても戻り、
// <C-c> より前に打ったキーは捨てる（Vim と同じ）。
//
// req: FR-81
func TestShellInterrupt(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	begin := time.Now()
	h.keys(":r !setsid sleep 4 & touch started; sleep 30<CR>")
	waitFile(t, filepath.Join(dir, "started"))
	h.keys("x<C-c>")
	h.waitShell()
	if d := time.Since(begin); d > 3*time.Second {
		t.Errorf("<C-c> で終わるまでに %v かかった（パイプを持つ子孫を待った）", d)
	}
	if got, msg := h.text(), h.message(); got != "a" || !strings.Contains(msg, "中断しました") {
		t.Errorf("<C-c>: text = %q, message = %q", got, msg)
	}
}

// シェルが終われば、背景に回してパイプを持ったままの子孫を待たずに（shellWaitDelay の後に）出力を入れる。
//
// req: FR-81
func TestShellBackgroundChild(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	begin := time.Now()
	h.keys(":r !(sleep 5 &); echo hi<CR>")
	h.waitShell()
	if d := time.Since(begin); d > 3*time.Second {
		t.Errorf("背景の子孫を %v 待った", d)
	}
	if got := h.text(); got != "a\nhi" {
		t.Errorf("text = %q", got)
	}
}

// キーマップの展開の中の :{range}! は、展開の残りのキーを外部のコマンドが終わるまで保留し、出力を入れてから続ける。
// 期待値は Vim 9.1（nnoremap Q :%!sort<CR>:w<CR>、nnoremap Z :%!sort<CR>dd。本文 c/b/a）で、Q は並べてから保存し、Z は b/c になる。
//
// req: FR-81
func TestShellInMapping(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "c\nb\na\n"})
	keymap := map[string]map[string]string{"normal": {
		"Q": ":%!sleep 0.3; LC_ALL=C sort<CR>:w<CR>",
		"Z": ":%!sleep 0.3; LC_ALL=C sort<CR>dd",
	}}
	h := start(t, Config{WorkDir: dir, Keymap: keymap}, "a.txt")
	h.keys("Q")
	h.waitShell()
	if got := h.text(); got != "a\nb\nc" {
		t.Errorf("Q: text = %q", got)
	}
	if disk, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(disk) != "a\nb\nc\n" {
		t.Errorf("Q: disk = %q, %v（並べてから保存する）", disk, err)
	}
	h.keys("uuZ")
	h.waitShell()
	if got := h.text(); got != "b\nc" {
		t.Errorf("Z: text = %q, want %q", got, "b\nc")
	}
}

// マクロの再生の中の :{range}! は、再生を止めて外部のコマンドを動かし、出力を入れてから再生の残りを続ける。
// 期待値は Vim 9.1 で、本文 ab/cd/ef/gh の 1 行目で qa:.!tr a-z A-Z<CR>jq の後に 2@a を打った結果である。
// ドットリピートで再生し直したときも同じく続ける。
//
// req: FR-81
func TestShellInMacro(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "ab\ncd\nef\ngh\nij\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("qa:.!tr a-z A-Z<CR>")
	h.waitShell()
	h.keys("jq2@a")
	h.waitShell()
	if got := h.text(); got != "AB\nCD\nEF\ngh\nij" {
		t.Errorf("2@a: text = %q", got)
	}
	var cur string
	h.e.do(func() { cur = h.e.cur.state.Cursor().String() })
	if cur != "3:0" {
		t.Errorf("2@a: cursor = %s, want 3:0", cur)
	}
	// . は 2@a を繰り返す（FR-57。Vim の . はマクロの再生を繰り返さないので、ここは Vim と比べていない）。
	// gh と ij を変え、2 回目の j は最後の行で失敗して止まる。
	h.keys(".")
	h.waitShell()
	if got := h.text(); got != "AB\nCD\nEF\nGH\nIJ" {
		t.Errorf(".: text = %q", got)
	}
}

// 出力が上限を超えたら、子を止めてバッファを変えずに誤りを出す。
//
// req: FR-81
func TestShellOutputLimit(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	old := shellOutputLimit
	shellOutputLimit = 1 << 20
	defer func() { shellOutputLimit = old }()
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":r !yes<CR>")
	h.waitShell()
	if got, msg := h.text(), h.message(); got != "a" || !strings.Contains(msg, "上限") {
		t.Errorf("text = %q, message = %q", got, msg)
	}
}

// 保存の先のディレクトリが無ければ、聞かずに作ってから保存し、作ったことをメッセージの行に 1 行で出す。
//
// req: FR-78
func TestWriteMakesDirectory(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt", "new/sub/b.txt")
	// 別のファイルへの書き出し（:w 名前）。
	h.keys(":b 1<CR>:w out/c.txt<CR>")
	h.settle()
	if disk, err := os.ReadFile(filepath.Join(dir, "out", "c.txt")); err != nil || string(disk) != "abc\n" {
		t.Errorf(":w out/c.txt: disk = %q, %v", disk, err)
	}
	if msg := h.message(); !strings.Contains(msg, "ディレクトリ \""+filepath.Join(dir, "out")+"\" を作り") || strings.Contains(msg, "\n") {
		t.Errorf(":w out/c.txt: message = %q", msg)
	}
	// バッファのファイル（保存の流れ）。
	h.keys(":b 2<CR>ixy<Esc>:w<CR>")
	h.settle()
	path := filepath.Join(dir, "new", "sub", "b.txt")
	if disk, err := os.ReadFile(path); err != nil || string(disk) != "xy\n" {
		t.Errorf(":w new/sub/b.txt: disk = %q, %v", disk, err)
	}
	if msg := h.message(); msg != "ディレクトリ \""+filepath.Dir(path)+"\" を作り、\""+path+"\" を書き込みました" {
		t.Errorf(":w new/sub/b.txt: message = %q", msg)
	}
	// ディレクトリがあれば、作ったとは言わない。
	h.keys("ia<Esc>:w<CR>")
	h.settle()
	if msg := h.message(); strings.Contains(msg, "ディレクトリ") {
		t.Errorf("2 回目の :w: message = %q", msg)
	}
	// 作った後に書き出しが失敗したら、誤りの中で作ったことも知らせる。
	h.keys(":w made/" + strings.Repeat("x", 300) + "<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E212") || !strings.Contains(msg, "ディレクトリ \""+filepath.Join(dir, "made")+"\" は作りました") {
		t.Errorf("作った後の失敗: message = %q", msg)
	}
}

// 権限が足りずにディレクトリを作れないときは、誤りを知らせて書き出さない（権限を上げて作る FR-116 はまだ無い）。
//
// req: FR-78
func TestWriteMakeDirectoryDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root は権限の不足にならない")
	}
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // 一時ディレクトリを消せるように書き込みを戻すだけである
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":w ro/sub/c.txt<CR>")
	h.settle()
	// 権限の不足なので、権限を上げて作るかを問う（FR-116）。n で断ると E212 を出す。
	h.eventually("権限を上げる問い", func() bool { return h.e.prompt != nil && strings.Contains(h.e.prompt.text, "権限が足りない") })
	h.keys("n")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E212: ディレクトリ") {
		t.Errorf("message = %q, want E212", msg)
	}
	if _, err := os.Stat(filepath.Join(ro, "sub")); err == nil {
		t.Errorf("ディレクトリが作られた")
	}
}
