package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestReadOnlyOptionInPTY は、-R で開いたファイルの編集が E21 で断られ、変更の無いまま :q で終われることを確かめる。
//
// req: FR-119
func TestReadOnlyOptionInPTY(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startEditor(t, "-R", path)
	p.out.WaitForString(t, "a.txt")
	p.write(t, "x")
	p.out.WaitForString(t, "E21")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
	if got, _ := os.ReadFile(path); string(got) != "abc\n" {
		t.Errorf("a.txt = %q", got)
	}
}

// TestReadOnlyPathsInPTY は、設定の readonlyPaths の下のファイルの編集が E21 で断られることを確かめる。
//
// req: FR-120
func TestReadOnlyPathsInPTY(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf, err := json.Marshal(map[string][]string{"readonlyPaths": {dir}})
	if err != nil {
		t.Fatal(err)
	}
	p := startEditorWithConfig(t, string(conf), path)
	p.out.WaitForString(t, "a.txt")
	p.write(t, "x")
	p.out.WaitForString(t, "E21")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
}
