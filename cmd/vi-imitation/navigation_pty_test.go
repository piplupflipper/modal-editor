package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/term/ptytest"
)

// TestNavigationKeysInPTY は、偽の端末の上で、矢印キー、<Home>、<End>、<PageDown> がカーソルを動かし、
// インサートモードの矢印キーも使えることを、保存した内容で確かめる（#112）。
// 端末は 80 列 24 行なので、表示領域は 22 行である。
func TestNavigationKeysInPTY(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 22 行の表示領域の <PageDown> を確かめるので、タブが 1 つのときはタブの行を出さない（設定の tabline。#215）。
	p := startEditorWithConfig(t, `{ "tabline": { "show": "multiple" } }`, path)
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "a.txt")

	const (
		up, down, right, left = "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D"
		home, end             = "\x1b[1~", "\x1b[4~"
		pageDown              = "\x1b[6~"
	)
	// <Esc> の直後に ESC で始まる列を続けると、<Esc> の続きと区別できないので、<Esc> の後には文字のキーを置く。
	keys := down + down + right + "iX\x1b" + // 3 行目の 2 文字目の前に X
		"j" + home + "iH\x1b" + // 4 行目の先頭に H
		"j" + pageDown + end + "aY" + left + "Z\x1b" + // 22 行の画面の <PageDown> で 21 行目へ。行末に Y、その前に Z
		"k" + up + home + right + "iW\x1b" + // 19 行目の 2 文字目の前に W
		":wq\r"
	p.write(t, keys)
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v。出力: %q", err, p.out.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := slicesReplace(lines, map[int]string{2: "lXine 3", 3: "Hline 4", 18: "lWine 19", 20: "line 21ZY"})
	if string(got) != strings.Join(want, "\n")+"\n" {
		t.Errorf("保存した内容 = %q, want %q", got, strings.Join(want, "\n")+"\n")
	}
	p.checkRestored(t, before)
}

// slicesReplace は、lines の写しの、repl のキーの番号の行を値に置き換えたものを返す。
func slicesReplace(lines []string, repl map[int]string) []string {
	out := append([]string(nil), lines...)
	for i, s := range repl {
		out[i] = s
	}
	return out
}
