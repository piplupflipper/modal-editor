package editor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command"
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

// TestRegistersConfig は、Config.Registers をバッファのレジスターに設定し、切り替えた先のバッファにも引き継ぐことを確かめる。
// maxEntryBytes を超えたヤンクは、履歴に積まなかったことをメッセージの行に出す。
//
// req: FR-52, FR-53
func TestRegistersConfig(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abcdef\n", "b.txt": "xyz\n"})
	h := start(t, Config{WorkDir: dir, Registers: command.RegisterConfig{MaxEntryBytes: 3, DeleteToHistory: new(false)}}, "a.txt")
	h.keys("yw")
	h.settle()
	if got := h.message(); !strings.Contains(got, "積みませんでした") {
		t.Errorf("message = %q", got)
	}
	h.keys(":e b.txt<CR>")
	h.settle()
	var cfg command.RegisterConfig
	var history int
	h.e.do(func() {
		cfg = h.e.cur.state.Registers().Config()
		history = len(h.e.cur.state.Registers().History())
	})
	if cfg.MaxEntryBytes != 3 || cfg.DeleteToHistory == nil || *cfg.DeleteToHistory {
		t.Errorf("切り替えた先の設定 = %+v", cfg)
	}
	h.keys("x")
	h.settle()
	h.e.do(func() { history = len(h.e.cur.state.Registers().History()) })
	if history != 0 {
		t.Errorf("deleteToHistory が偽なのに x が履歴に積んだ: %d 件", history)
	}
}

// TestInvalidRegisterMessage は、無い名前のレジスターを指定した操作を失敗させ、メッセージの行に出すことを確かめる。
//
// req: FR-50
func TestInvalidRegisterMessage(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	for _, keys := range []string{`"0p`, `"+yy`, "q1", "@-"} {
		h.keys("<Esc>")
		h.keys(keys)
		h.settle()
		if got := h.message(); !strings.HasPrefix(got, "E354: ") {
			t.Errorf("%s: message = %q", keys, got)
		}
	}
}

// TestConfigProblems は、Config.Problems の最初の 1 件を、起動後のメッセージで知らせることを確かめる（基本設計 8 章）。
func TestConfigProblems(t *testing.T) {
	h := start(t, Config{
		Problems: []error{errors.New("config.jsonc:3:5: 未知の項目 foo"), errors.New("2 件目")},
		Keymap:   map[string]map[string]string{"normal": {"<Foo>": "x"}},
	})
	// 2 件目と、キーマップの誤り（<Foo>）の 2 件を、件数で知らせる。
	if got := h.message(); got != "config.jsonc:3:5: 未知の項目 foo（他 2 件）" {
		t.Errorf("message = %q", got)
	}
}
