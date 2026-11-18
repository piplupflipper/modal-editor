package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 起動の引数のファイル（Open）は、ReadOnlyArgs（-R）なら編集を禁止して開く。:e で後から開くファイルは禁止しない。
// 禁止中の x は E21 を 1 行で出し、:w は断らずに書き出す。:readonly で解けば編集できる。
//
// req: FR-85, FR-86, FR-119
func TestReadOnlyArgs(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "def\n"})
	h := start(t, Config{WorkDir: dir, ReadOnlyArgs: true}, "a.txt")
	h.keys("x")
	h.settle()
	if got, msg := h.text(), h.message(); got != "abc" || msg != "E21: 編集を禁止したバッファは変更できません" {
		t.Errorf("x: text %q, message %q", got, msg)
	}
	h.keys(":w<CR>")
	h.settle()
	if strings.HasPrefix(h.message(), "E") {
		t.Errorf(":w on a read-only buffer: %q", h.message())
	}

	h.keys(":e b.txt<CR>x")
	h.settle()
	if got := h.text(); got != "ef" {
		t.Errorf(":e b.txt then x: text %q, message %q", got, h.message())
	}

	h.keys(":w<CR>:b 1<CR>:readonly<CR>")
	h.settle()
	if msg := h.message(); msg != "編集の禁止を解きました" {
		t.Errorf(":readonly message = %q", msg)
	}
	h.keys("x")
	h.settle()
	if got := h.text(); got != "bc" {
		t.Errorf("x after :readonly: text %q, message %q", got, h.message())
	}
}

// 設定の readonlyPaths に並べたディレクトリの下のファイルは、編集を禁止して開く。外のファイルは禁止しない。
//
// req: FR-120
func TestReadOnlyPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"mod/x.go", "modx/y.go"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("abc\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := start(t, Config{WorkDir: dir, ReadOnlyPaths: []string{filepath.Join(dir, "mod")}}, "mod/x.go", "modx/y.go")
	h.keys(":b 1<CR>x")
	h.settle()
	if got := h.text(); got != "abc" {
		t.Errorf("mod/x.go: x changed the text to %q", got)
	}
	h.keys(":b 2<CR>x")
	h.settle()
	if got := h.text(); got != "bc" {
		t.Errorf("modx/y.go: text %q, message %q", got, h.message())
	}
}

// 編集を禁止したバッファへの拡張機能の buffer/edit は、E21 の文言の誤りの応答で断る。編集の無い（noLines だけの）要求も断る。
//
// req: FR-118
func TestReadOnlyExtensionEdit(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), ReadOnlyArgs: true}, "a.txt")
	for _, k := range []string{`\z`, `\n`} {
		h.keys(k)
		h.eventually("buffer/edit の応答 "+k, func() bool { return strings.Contains(h.e.message.Text, "applied=false v0 err=") })
		if msg := h.message(); !strings.Contains(msg, "E21: 編集を禁止したバッファは変更できません") {
			t.Errorf("%s: message = %q", k, msg)
		}
		h.e.do(func() { h.e.message.Text = "" })
	}
	if got := h.text(); got != "x" {
		t.Errorf("text = %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "x\n" {
		t.Errorf("a.txt = %q", b)
	}
}

func TestUnderReadOnlyPath(t *testing.T) {
	dirs := []string{"/a/b", "/c/"}
	for path, want := range map[string]bool{
		"/a/b": true, "/a/b/x": true, "/a/b/x/y": true, "/a/bc/x": false, "/a": false, "/c/d": true, "/x": false,
	} {
		if got := underReadOnlyPath(path, dirs); got != want {
			t.Errorf("underReadOnlyPath(%q) = %v, want %v", path, got, want)
		}
	}
	if !underReadOnlyPath("/x", []string{"/"}) {
		t.Error(`"/" does not cover "/x"`)
	}
}
