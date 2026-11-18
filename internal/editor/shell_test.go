package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// :{range}! と :r ! は、シェルの子プロセスとパイプでつないで実行し、作業ディレクトリと Config.ChildEnv を渡す。
// 結果の細かい形（カーソル、アンドゥ、通知）は internal/command の TestFilterVim で Vim 9.1 と突き合わせている。
//
// req: FR-81
func TestShellFilterAndRead(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "c\nbb\na\n"})
	h := start(t, Config{WorkDir: dir, ChildEnv: []string{"MODAL_EDITOR_TEST_VALUE=xyz"}}, "a.txt")
	h.keys(":2,3!tr a-z A-Z<CR>")
	h.settle()
	if got := h.text(); got != "c\nBB\nA" {
		t.Fatalf(":2,3!tr: text = %q", got)
	}
	h.keys(":$r !pwd; echo $MODAL_EDITOR_TEST_VALUE<CR>")
	h.settle()
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
	h.settle()
	if got := h.message(); got != "シェルが 4 を返しました" {
		t.Errorf(":1!exit 4: message = %q", got)
	}
}

// <C-c> は、待っている外部のコマンドを終わらせ、バッファを変えない。上限の時間を超えたときも同じである。
//
// req: FR-81
func TestShellInterruptAndTimeout(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	begin := time.Now()
	h.keys(":r !echo x; touch started; sleep 30<CR>")
	// <C-c> を先に送ると、:r の前のキーと一緒に捨てられる（afterKey）。子プロセスが動き出してから送る。
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Since(begin) > 10*time.Second {
			t.Fatal("子プロセスが動き出さない")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.keys("<C-c>")
	h.settle()
	if d := time.Since(begin); d > 10*time.Second {
		t.Errorf("<C-c> で終わるまでに %v かかった", d)
	}
	if got, msg := h.text(), h.message(); got != "a" || !strings.Contains(msg, "中断しました") {
		t.Errorf("<C-c>: text = %q, message = %q", got, msg)
	}

	old := shellTimeout
	shellTimeout = 200 * time.Millisecond
	defer func() { shellTimeout = old }()
	h.keys(":1!sleep 30<CR>")
	h.settle()
	if got, msg := h.text(), h.message(); got != "a" || !strings.Contains(msg, "を過ぎても終わりません") {
		t.Errorf("timeout: text = %q, message = %q", got, msg)
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
	if msg := h.message(); !strings.HasPrefix(msg, "E212: ディレクトリ") {
		t.Errorf("message = %q, want E212", msg)
	}
	if _, err := os.Stat(filepath.Join(ro, "sub")); err == nil {
		t.Errorf("ディレクトリが作られた")
	}
}
