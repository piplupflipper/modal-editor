package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// TestSearchInPTY は、偽の端末の上で、/ のパターンの入力がコマンドラインの行に "/" と一緒に出ること、
// <CR> で一致へ移ること、n が次の一致へ移り、バッファの端で折り返したら警告を出すこと、
// d/ と * が働くことを、画面と保存した内容で確かめる（#139）。
func TestSearchInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo foo\nthree\nfoo four\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditor(t, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")

	// 入力の途中は、コマンドラインの行（24 行目）の先頭に "/" が出る。
	// 描画は前の画面との差分なので、"/" とパターンが続けて出るとは限らない。"/" が行の先頭に描かれたことだけを見る。
	p.write(t, "/fo")
	p.out.WaitForString(t, "\x1b[24;1H\x1b[0m/")
	// <CR> で 2 行目の foo へ移り、n で 4 行目の foo へ移る。もう一度の n は先頭へ折り返して 2 行目へ戻り、警告を出す。
	p.write(t, "o\rnn")
	p.out.WaitForString(t, "末尾まで検索したので先頭から続けました")
	// * で 2 行目の foo から 4 行目の foo へ移って Y を入れる。
	// 最後に 1 行目へ戻り、d/thr<CR> で 1 行目と 2 行目を消す（行の先頭から行の先頭までの d は行単位になる）。
	p.write(t, "*iY")
	p.out.WaitForString(t, "INSERT")
	p.write(t, "\x1bggd/thr\r:wq\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "three\nYfoo four\n"
	if string(got) != want {
		t.Errorf("保存した内容 = %q, want %q", got, want)
	}
	p.checkRestored(t, before)
}
