package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/config"
)

// viewTabs は、読み込んでいる client のタブのファイル名を、並びの順に空白で区切って返す。
// 名前の無いバッファは filepath.Base("") の "." になる（offscreen_test.go の tabNames と同じ形）。
func (h *harness) viewTabs() string {
	var names []string
	h.e.do(func() {
		for _, bs := range h.e.view.tabs {
			names = append(names, filepath.Base(bs.path))
		}
	})
	return strings.Join(names, " ")
}

// :tab.new は、名前の無い空のバッファを、カーソルのあるタブの右の新しいタブで開いて移る。
// 名前の無いバッファは :w ファイル で名前を付けられる。引数にファイルを書くと :e ファイル と同じく開く。
//
// req: FR-151
func TestTabNewCommand(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "bee\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt", "b.txt")
	h.keys(":b a.txt<CR>:tab.new<CR>")
	h.settle()
	if got := h.viewTabs(); got != "a.txt . b.txt" {
		t.Fatalf(":tab.new: tabs = %q (message %q)", got, h.message())
	}
	var path, fileType string
	var modified bool
	h.e.do(func() { path, fileType, modified = h.e.cur.path, h.e.cur.fileType(), h.e.cur.buf.Modified() })
	if path != "" || fileType != "" || modified || h.text() != "" {
		t.Errorf(":tab.new: path %q, filetype %q, modified %v, text %q", path, fileType, modified, h.text())
	}
	// 移る前のタブは裏のバッファになる（<C-^> で戻る）。
	h.keys("<C-^>")
	h.settle()
	h.e.do(func() { path = h.e.cur.path })
	if filepath.Base(path) != "a.txt" {
		t.Errorf("<C-^>: path = %q", path)
	}
	h.keys("<C-^>ihello<Esc>:w c.txt<CR>")
	h.settle()
	if disk, err := os.ReadFile(filepath.Join(dir, "c.txt")); err != nil || string(disk) != "hello\n" {
		t.Errorf(":w c.txt: disk = %q, %v (message %q)", disk, err, h.message())
	}
	if got := h.viewTabs(); got != "a.txt c.txt b.txt" {
		t.Errorf(":w c.txt: tabs = %q", got)
	}
	// ファイルを書けば :e と同じく開く。開いているファイルはそのタブへ移り、無いファイルは右の新しいタブで開く。
	h.keys(":tab.new b.txt<CR>")
	h.settle()
	h.e.do(func() { path = h.e.cur.path })
	if got := h.viewTabs(); filepath.Base(path) != "b.txt" || got != "a.txt c.txt b.txt" {
		t.Errorf(":tab.new b.txt: cur %q, tabs %q", path, got)
	}
	h.keys(":tab.new d.txt<CR>")
	h.settle()
	if got := h.viewTabs(); got != "a.txt c.txt b.txt d.txt" {
		t.Errorf(":tab.new d.txt: tabs = %q", got)
	}
	h.keys(":tab.ne<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf(":tab.ne: message = %q", msg)
	}
}

// 起動したときの名前の無い空のタブでも、:tab.new は使い直さずに新しいタブを足す（Vim 9.1 の :tabnew と同じ）。
//
// req: FR-151
func TestTabNewFromEmpty(t *testing.T) {
	h := start(t, Config{})
	h.keys(":tab.new<CR>")
	h.settle()
	if got := h.viewTabs(); got != ". ." {
		t.Errorf("tabs = %q", got)
	}
}

// 設定ファイルの別名（commandAliases）は、本体の別名と同じ表で引き、本体の Ex コマンドと拡張機能のコマンドへ付け替えられる。
// 足した別名は、コマンドの一覧（editor/list の commands。:finder.commands）と :help に出る。
//
// req: FR-150
func TestConfigCommandAliases(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "bee\n"})
	specs := fakeSpecs(t)
	aliases, add := CommandAliases(specs)
	for _, a := range []config.CommandAlias{
		{Name: "tabedit", Min: 4, Command: "edit"},
		{Name: "tabnew", Min: 6, Command: "tab.new"},
		{Name: "echo", Min: 2, Command: "fake.echo"},
		{Name: "manual", Min: 3, Command: "help"},
	} {
		if err := add(a); err != nil {
			t.Fatalf("%s: %v", a.Name, err)
		}
	}
	// 付け替える先は、宣言ファイルのコマンドと、本体が同じ口から呼ぶコマンド（:help など）でなければ足せない。
	for _, a := range []config.CommandAlias{
		{Name: "nosuch", Min: 2, Command: "fake.nosuch"},
		{Name: "helper", Min: 4, Command: "edit"}, // :help が呼べなくなる
		{Name: "tabedits", Min: 4, Command: "edit"},
	} {
		if err := add(a); err == nil {
			t.Errorf("%s: 足せた", a.Name)
		}
	}
	h := start(t, Config{WorkDir: dir, Extensions: specs, CommandAliases: aliases}, "a.txt")
	h.keys(":tabe b.txt<CR>")
	h.settle()
	if got := h.viewTabs(); got != "a.txt b.txt" {
		t.Fatalf(":tabe b.txt: tabs = %q (message %q)", got, h.message())
	}
	h.keys(":tabnew<CR>")
	h.settle()
	if got := h.viewTabs(); got != "a.txt b.txt ." {
		t.Errorf(":tabnew: tabs = %q (message %q)", got, h.message())
	}
	h.keys(":ec こんにちは|x<CR>")
	h.eventually("拡張機能のコマンドへの別名", func() bool { return h.e.message.Text == `args={"text":"こんにちは|x"}` })

	h.keys(":fake.list commands<CR>")
	h.waitMessage("list {")
	msg := h.message()
	for _, w := range []string{`{"name":"tab.new","short":"tab.new"}`, `{"name":"set","short":"se"}`, `{"name":"tabedit","short":"tabe"}`, `{"name":"tabnew","short":"tabnew"}`, `{"name":"echo","short":"ec"}`} {
		if !strings.Contains(msg, w) {
			t.Errorf("commands %s does not contain %s", msg, w)
		}
	}
	h.keys("<Esc>")
	h.settle()

	for _, tt := range []struct{ word, want string }{
		{"tabedit", "- `:tabedit`: `:edit` の別名（`:tabe`）"},
		{":tabnew", "- `:tabnew`: `:tab.new` の別名（`:tabnew`）"},
		{"echo", "- `:echo`: `:fake.echo` の別名（`:ec`）"},
	} {
		h.keys(":h " + tt.word + "<CR>")
		h.settle()
		if got := h.cursorLine(); !strings.Contains(got, tt.want) {
			t.Errorf(":h %s: cursor line %q does not contain %q (message %q)", tt.word, got, tt.want, h.message())
		}
	}
	h.keys(":q<CR>")
	h.settle()
	// :manual は :help に付け替えた別名で、本体が拡張機能のコマンドの口から呼ぶコマンドへも付け替えられる。
	h.keys(":man<CR>")
	h.settle()
	var title string
	h.e.do(func() { title = h.e.cur.title })
	if title != helpTitle {
		t.Errorf(":man: title = %q (message %q)", title, h.message())
	}
}
