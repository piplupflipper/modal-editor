package editor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestFixEndOfLine は、Config.FixEndOfLine を開いたバッファの書き出しに使うことを確かめる（基本設計 4.1）。
// 末尾に改行の無いファイルを編集して保存すると、既定では改行を補い、false なら補わない。
func TestFixEndOfLine(t *testing.T) {
	off := false
	for _, tt := range []struct {
		name string
		fix  *bool
		want string
	}{
		{"既定は補う", nil, "xab\n"},
		{"false なら補わない", &off, "xab"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": "ab"})
			h := start(t, Config{WorkDir: dir, FixEndOfLine: tt.fix}, "a.txt")
			h.keys("ix<Esc>:w<CR>")
			h.settle()
			want := tt.want
			got, err := os.ReadFile(filepath.Join(dir, "a.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Errorf("保存した内容 = %q, want %q", got, want)
			}
		})
	}
}

// TestConfigProblems は、Config.Problems の最初の 1 件を、起動後のメッセージで知らせることを確かめる（基本設計 8 章）。
func TestConfigProblems(t *testing.T) {
	h := start(t, Config{
		Problems: []error{errors.New("config.jsonc:3:5: 未知の項目 foo"), errors.New("2 件目")},
		Keymap:   map[string]map[string]string{"normal": {"<Foo>": "x"}},
	})
	if got := h.message(); got != "config.jsonc:3:5: 未知の項目 foo" {
		t.Errorf("message = %q", got)
	}
}
