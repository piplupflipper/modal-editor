package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// TestPasteInPTY は、ノーマルモードとビジュアルモードの bracketed paste が、貼った文字列をコマンドとして実行せずに入れることを確かめる
// （#114。Vim 9.1 と同じ結果）。貼った dd で行が消えず、:q! で終わらない。
func TestPasteInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("abc\ndef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditor(t, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")
	const start, end = "\x1b[200~", "\x1b[201~"
	// 1 行目の先頭に "dd:q!\r" を貼る（行の先頭なので i の位置）。カーソルは貼った最後の文字（2 行目の先頭）に来る。
	// 続けて j で 3 行目（def）へ移り、vl で "de" を選んで Z を貼る。
	p.write(t, start+"dd:q!\r"+end+"jvl"+start+"Z"+end+":wq\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "dd:q!\nabc\nZf\n"; string(got) != want {
		t.Errorf("保存した内容 = %q, want %q", got, want)
	}
	p.checkRestored(t, before)
}

// TestExtensionPanicRestoresTerminal は、拡張機能を扱う goroutine（internal/exthost の標準エラー出力の記録）が panic しても、
// 端末を元に戻してから panic で終わることを確かめる（#114、基本設計 8 章）。
// panic は、テストが差し込んだロガー（panicHandler）が、拡張機能が標準エラー出力に書いた印の行を記録するときに起こす。
func TestExtensionPanicRestoresTerminal(t *testing.T) {
	const marker = "modal-editor-test-panic"
	xdg := t.TempDir()
	dir := filepath.Join(xdg, "data", "modal-editor", "extensions", "boom")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "id": "boom",
  "command": "/bin/sh",
  "args": ["-c", "echo ` + marker + ` >&2; exec sleep 30"],
  "activation": [{ "on": "filetype", "pattern": "go" }]
}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditorIn(t, xdg, []string{envPanicOnStderr + "=" + marker}, path)
	before := ptytest.Termios(t, p.tty)
	err := p.wait(t)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("エディタの終了 = %v, want panic の終了コード 2。出力: %q", err, p.out.String())
	}
	p.checkRestored(t, before)
	// panic の出力は、端末を戻した後（代替画面から戻る列の後）に出る。
	p.out.WaitForString(t, "テストが起こした panic")
	out := p.out.String()
	if i, j := strings.LastIndex(out, "\x1b[?1049l"), strings.Index(out, "テストが起こした panic"); j < i {
		t.Errorf("panic の出力が、代替画面から戻る列より前にある: %q", out)
	}
	if !strings.Contains(out, "logStderr") {
		t.Errorf("panic が拡張機能の標準エラー出力を読む goroutine で起きていない: %q", out)
	}
}
