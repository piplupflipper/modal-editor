package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// writeExternal は、エディタの外から path を text に書き換え、更新時刻を 2 秒進める（更新時刻の刻みによらず変更として見えるように）。
func writeExternal(t *testing.T, path, text string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime := fi.ModTime().Add(2 * time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// TestExternalChangeInPTY は、偽の端末の上で、外でのファイルの変更の扱いを確かめる（#143）。
// 未変更のバッファは問わずに読み直し（既定の autoread）、変更のあるバッファでは W12 を問い、
// その後の :w は書き込むかを問い、y で書く。
func TestExternalChangeInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditor(t, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")

	writeExternal(t, path, "agent\n")
	// メッセージは絶対パスを含み、80 桁に収まらないことがあるので、行の始めのほうだけを見る。
	p.out.WaitForString(t, "gent")
	p.out.WaitForString(t, "は外で変更されたので")

	p.write(t, "x")
	p.out.WaitForString(t, "[+]")
	writeExternal(t, path, "Q\n")
	p.out.WaitForString(t, "W12:")
	p.write(t, "o:w\r")
	// 描画は前の画面との差分なので、問いの文が続けて出るとは限らない。末尾の "(y/n)?" だけを見る。
	// y で書けていなければ、続く :q は E37 で失敗し、エディタは終わらない。
	p.out.WaitForString(t, "(y/n)?")
	p.write(t, "y:q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "gent\n"; string(got) != want {
		t.Errorf("保存した内容 = %q, want %q", got, want)
	}
	p.checkRestored(t, before)
}
