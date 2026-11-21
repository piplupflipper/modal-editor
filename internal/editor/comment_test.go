package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// comment.string の初めの値は、ほかのバッファごとの項目と同じ段で決まる。本体のファイルの種類ごとの既定（Neovim 0.12.5 の ftplugin の値）は
// 設定ファイルの最上位の値より後に当て、設定ファイルの fileTypeOptions で上書きできる。json は ftplugin と同じく空になる。
// lua は本体が判定しないので、設定の fileTypes で足したときに既定の値になる。
//
// req: FR-163, FR-169
func TestCommentStringLayers(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"a.go": "x\n", "a.py": "", "a.sh": "", "a.json": "", "a.txt": "", "a.lua": "", "a.rs": "",
	})
	cfg := Config{
		WorkDir:   dir,
		FileTypes: map[string]string{".lua": "lua"},
		Options:   map[string]option.Value{option.CommentString: option.StringValue("; %s")},
		FileTypeOptions: map[string]map[string]option.Value{
			"rust": {option.CommentString: option.StringValue("//%s")},
		},
	}
	h := start(t, cfg, "a.go", "a.py", "a.sh", "a.json", "a.txt", "a.lua", "a.rs")
	h.settle()
	h.e.do(func() {
		for name, want := range map[string]string{
			"a.go": "// %s", "a.py": "# %s", "a.sh": "# %s", "a.json": "", "a.txt": "; %s", "a.lua": "-- %s", "a.rs": "//%s",
		} {
			if got := h.bufferAt(name).option(option.CommentString).String(); got != want {
				t.Errorf("%s: comment.string = %q, want %q", name, got, want)
			}
		}
	})
}

// 端末から Ctrl-/ は <C-_>（0x1f）で届く。Go のファイルでは <C-_> が gcc と同じく行をコメントにし、
// :set cms= で変えた形が次の gc から効く。空でなく %s を含まない形は E537 にする。
//
// req: FR-169
func TestCommentKeysInEditor(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "\tx := 1\n\ty := 2\n"})
	h := start(t, Config{WorkDir: dir}, "a.go")
	h.keys("<C-_>")
	h.settle()
	if got, want := h.text(), "\t// x := 1\n\ty := 2"; got != want {
		t.Errorf("<C-_> = %q, want %q", got, want)
	}
	h.keys("Vj<C-_>")
	h.settle()
	if got, want := h.text(), "\t// // x := 1\n\t// y := 2"; got != want {
		t.Errorf("Vj<C-_> = %q, want %q", got, want)
	}
	h.keys("uu:set cms=#\\ %s<CR>gcj")
	h.settle()
	if got, want := h.text(), "\t# x := 1\n\t# y := 2"; got != want {
		t.Errorf(":set cms=#\\ %%s の後の gcj = %q, want %q", got, want)
	}
	h.keys(":set cms=#<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E537") {
		t.Errorf(":set cms=# の message = %q", msg)
	}
	h.keys(":set cms=<CR>gcc")
	h.settle()
	if msg := h.message(); msg != "comment.string が空です" {
		t.Errorf("空の comment.string の gcc の message = %q", msg)
	}
}
