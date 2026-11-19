package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// TestDefinitionJumpBackInPTY は、LSP の拡張機能とテスト用の言語サーバー（cmd/modal-editor-ext-lsp/testdata/fakels）を実際に起動し、
// 定義へ移動（既定のキー <C-]>。#203）で別のファイルへ移った後に、<C-o> で元のファイルの元の位置へ戻れることを確かめる（#142）。
// <C-^> で定義のファイルへ移り直し、もう一度 <C-^> で戻れることと、:ls の一覧も確かめる。
//
// req: FR-95
func TestDefinitionJumpBackInPTY(t *testing.T) {
	xdg := t.TempDir()
	installExtension(t, xdg, "lsp")
	fakels := filepath.Join(t.TempDir(), "fakels")
	goBuild(t, fakels, "./"+filepath.ToSlash(filepath.Join("..", "modal-editor-ext-lsp", "testdata", "fakels")))
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	if err := os.WriteFile(a, []byte("package a\n\nfunc f() { g() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("package a\n\nfunc TARGET() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// fakels は、-defpath のファイルの中の "TARGET" の位置を定義として返す。
	writeConfig(t, xdg, `{ "extensions": { "settings": { "lsp": { "servers": { "go": {
		"command": `+strconv.Quote(fakels)+`, "args": ["-defpath", `+strconv.Quote(b)+`] } } } } } }`)
	p := startEditorIn(t, xdg, nil, a)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.go")

	// 3 行目の g へ移り（jjw の後の 3 つ目の語）、定義へ移動で b.go の TARGET へ移る。
	// 言語サーバーの起動を待つ間は「稼働していない」と知らせて何もしないので、b.go へ移るまで打ち直す。
	p.write(t, "jjwwww")
	// 描画は前の画面との差分なので、ステータス行のファイル名は変わった文字だけが出る。移った先の本文の TARGET を見る。
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(p.out.String(), "TARGET"); {
		if time.Now().After(deadline) {
			t.Fatalf("定義へ移らない。出力: %q", p.out.String())
		}
		p.write(t, "\x1d") // <C-]>
		time.Sleep(200 * time.Millisecond)
	}
	// <C-o> で a.go の g へ戻り、x で g を消して書き出す。
	written := func(n int) func(string) bool {
		return func(out string) bool { return strings.Count(out, "を書き込みました") >= n }
	}
	p.write(t, "\x0fx:w\r")
	p.out.WaitFor(t, "a.go の書き出し", written(1))
	// :ls の一覧の 2 行目に、裏のタブ（#h）として b.go が出る。
	p.write(t, ":ls\r")
	p.out.WaitForString(t, `#h   "`)
	// <C-^> で b.go の TARGET へ移り、x で T を消して書き出し、<C-^> で a.go へ戻り、:qa で 2 つのタブを閉じて終わる（FR-114）。
	p.write(t, "\r\x1ex:w\r")
	p.out.WaitFor(t, "b.go の書き出し", written(2))
	p.write(t, "\x1e:qa\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	for path, want := range map[string]string{a: "package a\n\nfunc f() { () }\n", b: "package a\n\nfunc ARGET() {}\n"} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
		}
	}
	p.checkRestored(t, before)
}
