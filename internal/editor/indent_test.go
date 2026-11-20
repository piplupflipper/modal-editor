package editor

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editorconfig"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// indentValues は、バッファ bs の字下げの 3 つの項目を "useSpaces width tab" の形で返す。
func indentValues(bs *bufferState) string {
	return bs.option(option.IndentUseSpaces).String() + " " + bs.option(option.IndentWidth).String() + " " +
		bs.option(option.TabWidth).String()
}

// bufferAt は、パスの最後の要素が name のバッファを返す。
func (h *harness) bufferAt(name string) *bufferState {
	h.t.Helper()
	for _, bs := range h.e.bufs {
		if filepath.Base(bs.path) == name {
			return bs
		}
	}
	h.t.Fatalf("%s のバッファが無い", name)
	return nil
}

// バッファごとの項目の初めの値は、表の既定値、設定ファイルの最上位、本体のファイルの種類ごとの既定（go と make）、
// 設定ファイルの fileTypeOptions、.editorconfig の順に当て、後に当てた値が勝つ。
//
// req: FR-163, FR-164
func TestOptionLayers(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		".editorconfig": "root = true\n[*.md]\nindent_size = 2\n[sub/*.go]\nindent_style = space\n",
		"a.txt":         "", "a.go": "", "Makefile": "", "a.py": "", "a.md": "", "sub/b.go": "", "a.rs": "",
	})
	cfg := Config{
		WorkDir: dir,
		Options: map[string]option.Value{
			option.IndentUseSpaces: option.BoolValue(true), option.IndentWidth: option.IntValue(4), option.TabWidth: option.IntValue(4),
			option.IndentAuto: option.BoolValue(true),
		},
		FileTypeOptions: map[string]map[string]option.Value{
			"python": {option.IndentWidth: option.IntValue(3)},
			"go":     {option.TabWidth: option.IntValue(2)},
			"rust":   {option.IndentUseSpaces: option.BoolValue(false)},
		},
	}
	h := start(t, cfg, "a.txt", "a.go", "Makefile", "a.py", "a.md", "sub/b.go", "a.rs")
	h.settle()
	tests := []struct{ name, want string }{
		{"a.txt", "true 4 4"},     // 設定ファイルの最上位
		{"a.go", "false 0 2"},     // 本体の go の既定（タブ文字、tab.width と同じ幅）の上に、設定ファイルの go の tab.width
		{"Makefile", "false 0 4"}, // 本体の make の既定は、設定ファイルの最上位より後に当てる
		{"a.py", "true 3 4"},      // 設定ファイルの python
		{"a.md", "true 2 2"},      // .editorconfig の indent_size は、tab_width が無ければ tab.width にもなる
		{"sub/b.go", "true 0 2"},  // .editorconfig は本体と設定ファイルのファイルの種類ごとの値より後に当てる
		{"a.rs", "false 4 4"},     // 設定ファイルの rust
	}
	h.e.do(func() {
		for _, tt := range tests {
			bs := h.bufferAt(filepath.Base(tt.name))
			if got := indentValues(bs); got != tt.want {
				t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
			}
			if !bs.option(option.IndentAuto).Bool() {
				t.Errorf("%s: indent.auto が設定ファイルの値になっていない", tt.name)
			}
		}
	})
}

// .editorconfig の indent_style、indent_size、tab_width は、Neovim 0.12.5 の runtime/lua/editorconfig.lua と同じに写す。
// unset は書かなかったものとし、正しくない値の項目は使わない。
//
// req: FR-164
func TestEditorConfigOptions(t *testing.T) {
	e := &Editor{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tests := []struct {
		props editorconfig.Properties
		want  string
	}{
		{editorconfig.Properties{"indent_style": "space"}, "useSpaces=true"},
		{editorconfig.Properties{"indent_style": "tab"}, "useSpaces=false width=0"},
		{editorconfig.Properties{"indent_style": "tab", "indent_size": "4"}, "useSpaces=false width=4 tab=4"},
		{editorconfig.Properties{"indent_size": "tab", "tab_width": "3"}, "width=0 tab=3"},
		{editorconfig.Properties{"indent_size": "2", "tab_width": "8"}, "width=2 tab=8"},
		{editorconfig.Properties{"indent_size": "unset", "tab_width": "unset", "indent_style": "unset"}, ""},
		{editorconfig.Properties{"indent_style": "tabs", "indent_size": "x", "tab_width": "0"}, ""},
		{editorconfig.Properties{"indent_size": "-1"}, ""},
		{editorconfig.Properties{"end_of_line": "crlf", "charset": "latin1"}, ""},
	}
	for _, tt := range tests {
		got := e.editorConfigOptions(tt.props)
		var parts []string
		for _, n := range []struct{ name, label string }{
			{option.IndentUseSpaces, "useSpaces"}, {option.IndentWidth, "width"}, {option.TabWidth, "tab"},
		} {
			if v, ok := got[n.name]; ok {
				parts = append(parts, n.label+"="+v.String())
			}
		}
		if s := strings.Join(parts, " "); s != tt.want || len(got) != len(parts) {
			t.Errorf("%v: %q（%d 項目）, want %q", tt.props, s, len(got), tt.want)
		}
	}
}

// 後で拡張機能から設定を変える口を作るときは、optionLayers の .editorconfig の後に段を 1 つ足し、値が届いたら refreshOptionLayers を呼ぶ。
// ここでは、その段を Editor.layers の末尾に足すだけで（ほかの箇所は直さずに）、次のとおりになることを確かめる（PR #324 のレビューの必須 2）。
//
//   - 足した段の値が .editorconfig の値に勝つ
//   - 利用者が :set で変えた値は、足した段の値が後から届いても（buffer/open を送り直した後でも）上書きされない
//   - :set filetype=go で段を当て直すと、go の段が値を持つ indent.width は :set の値を隠し、足した段の値になる
//   - 名前の無いバッファに名前を付けたときも、足した段が .editorconfig より後に当たり、:set で変えたほかの値は残る
//
// req: FR-163, FR-164
func TestOptionLayerAddedLater(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		".editorconfig": "root = true\n[*]\nindent_style = space\nindent_size = 2\n",
		"a.txt":         "x\n",
	})
	extWidth := 6
	h := startWith(t, Config{WorkDir: dir}, func(e *Editor) error {
		e.layers = append(e.layers, optionLayer{values: func(*Editor, *bufferState) map[string]option.Value {
			return map[string]option.Value{option.IndentWidth: option.IntValue(extWidth)}
		}})
		return e.Open("a.txt")
	})
	h.settle()
	want := func(what, values string) {
		t.Helper()
		h.e.do(func() {
			if got := indentValues(h.e.cur); got != values {
				t.Errorf("%s: %s, want %s", what, got, values)
			}
		})
	}
	want("開いたとき", "true 6 2")
	h.keys(":set sw=3<CR>")
	h.settle()
	want(":set sw=3 の後", "true 3 2")
	h.e.do(func() {
		extWidth = 7
		h.e.refreshOptionLayers(h.e.cur)
	})
	want("足した段の値が後から届いた後", "true 3 2")
	h.keys(":set filetype=go<CR>")
	h.settle()
	want(":set filetype=go の後", "true 7 2")
	h.keys(":tab.new<CR>:set ts=5<CR>:w b.txt<CR>")
	h.settle()
	want(":w b.txt の後", "true 7 5")
}

// editorConfig.enabled が false なら .editorconfig を読まない。
//
// req: FR-164
func TestEditorConfigDisabled(t *testing.T) {
	dir := writeFiles(t, map[string]string{".editorconfig": "root = true\n[*]\nindent_size = 2\n", "a.txt": ""})
	h := start(t, Config{WorkDir: dir, Options: map[string]option.Value{option.EditorConfigEnabled: option.BoolValue(false)}}, "a.txt")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 8 8" {
			t.Errorf("%s, want false 8 8（.editorconfig を読まない）", got)
		}
	})
}

// .editorconfig はバッファを開くときに読み、開いた後に書き換えても効かず、:e で開き直すと効く（Neovim 0.12.5 と同じ）。
// :e で開き直すと、:set で変えた値も初めの値に戻る。
//
// req: FR-164
func TestEditorConfigReadOnOpen(t *testing.T) {
	dir := writeFiles(t, map[string]string{".editorconfig": "root = true\n[*]\nindent_size = 2\n", "a.txt": "x\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.settle()
	if err := os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte("root = true\n[*]\nindent_style = space\ntab_width = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.keys(":set sw=5<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 5 2" {
			t.Errorf("書き換えた後: %s, want false 5 2", got)
		}
	})
	h.keys(":e<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "true 8 3" {
			t.Errorf(":e の後: %s, want true 8 3", got)
		}
	})
}

// :set filetype= でファイルの種類を変えると、初めの値の段を新しい種類で当て直す。.editorconfig は、開いたときに読んだものを当てる。
// 新しい種類の段が値を持つ項目だけ :set の値を隠し、ほかの項目の :set の値（ここでは tab.width）は残す。
// 名前の無いバッファに名前を付けたときは、種類が変わらなければ、.editorconfig を読み直して段を作り直し、:set で変えた値はどの段よりも勝つ。
//
// req: FR-163, FR-164
func TestFileTypeChangeReappliesLayers(t *testing.T) {
	dir := writeFiles(t, map[string]string{".editorconfig": "root = true\n[*.txt]\ntab_width = 6\n[*.dat]\nindent_size = 3\n", "a.txt": ""})
	cfg := Config{
		WorkDir:         dir,
		Options:         map[string]option.Value{option.IndentUseSpaces: option.BoolValue(true), option.IndentWidth: option.IntValue(4)},
		FileTypeOptions: map[string]map[string]option.Value{"python": {option.IndentWidth: option.IntValue(2)}},
	}
	h := start(t, cfg, "a.txt")
	h.settle()
	h.keys(":set ts=2<CR>:set filetype=go<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 0 2" {
			t.Errorf(":set filetype=go の後: %s, want false 0 2", got)
		}
	})
	h.keys(":set filetype=python<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "true 2 2" {
			t.Errorf(":set filetype=python の後: %s, want true 2 2", got)
		}
	})

	// 名前の無いバッファ
	h.keys(":tab.new<CR>:set ts=5 noet<CR>:w b.dat<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 3 5" {
			t.Errorf(":w b.dat の後: %s, want false 3 5（.editorconfig の indent_size は当たり、:set で変えた noet と ts=5 は残る）", got)
		}
	})
}

// ファイルの種類が変わったときは、新しい種類の段（本体と設定ファイルのファイルの種類ごとの値）が値を持つ項目だけ :set の値を隠し、
// ほかの :set の値は残す。前の種類が隠した項目は、新しい種類の段が値を持たなければ :set の値に戻る。
// 期待値は、2026-11-21 に Neovim 0.12.5（nvim --clean）で、:enew、:set sw=3 et ts=5 ai の後に :w <名前> を打ち、
// 同じく :set ft=go の後に :set ft=c を打って取った（本体の設定は Vim と同じ既定のまま）。
//
// req: FR-163
func TestFileTypeChangeKeepsOtherSetValues(t *testing.T) {
	dir := t.TempDir()
	h := start(t, Config{WorkDir: dir})
	h.settle()
	tests := []struct{ name, want string }{
		{"n.c", "true 3 5"},       // Neovim: ft=c et=1 sw=3 ts=5
		{"n.go", "false 0 5"},     // Neovim: ft=go et=0 sw=0 ts=5
		{"Makefile", "false 0 5"}, // Neovim: ft=make et=0 sw=0 ts=5
		{"n.txt", "true 3 5"},     // Neovim: ft=text et=1 sw=3 ts=5
	}
	for _, tt := range tests {
		h.keys(":tab.new<CR>:set sw=3 et ts=5 ai<CR>:w " + tt.name + "<CR>")
		h.settle()
		h.e.do(func() {
			if got := indentValues(h.e.cur); got != tt.want || !h.e.cur.option(option.IndentAuto).Bool() {
				t.Errorf(":w %s の後: %s（indent.auto %v）, want %s", tt.name, got, h.e.cur.option(option.IndentAuto).Bool(), tt.want)
			}
		})
	}
	// 本体の :set は短い名前の ft を受け付けないので、filetype と書く。
	h.keys(":tab.new<CR>:set sw=3 et ts=5<CR>:set filetype=go<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 0 5" {
			t.Errorf(":set ft=go の後: %s, want false 0 5", got)
		}
	})
	h.keys(":set filetype=c<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "true 3 5" {
			t.Errorf(":set ft=c の後: %s, want true 3 5（Neovim は et=1 sw=3 ts=5）", got)
		}
	})
	// 隠した項目を :set で変えると、その値を使う。
	h.keys(":set filetype=go<CR>:set sw=6<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 6 5" {
			t.Errorf(":set ft=go の後の :set sw=6: %s, want false 6 5", got)
		}
	})
}

// :set は、字下げの 4 項目を Vim の名前と短い名前で受け付ける。範囲の外の値は Vim と同じ番号のエラーにする。
//
// req: FR-160, FR-161, FR-162
func TestSetIndentOptions(t *testing.T) {
	h := start(t, Config{})
	h.keys(":set et sw=2 ts=3 ai<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "true 2 3" || !h.e.cur.option(option.IndentAuto).Bool() {
			t.Errorf(":set et sw=2 ts=3 ai: %s", got)
		}
	})
	h.keys(":set noexpandtab shiftwidth=0 tabstop=4 noautoindent<CR>")
	h.settle()
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 0 4" || h.e.cur.option(option.IndentAuto).Bool() {
			t.Errorf(":set noexpandtab shiftwidth=0 tabstop=4 noautoindent: %s", got)
		}
	})
	for _, s := range []struct{ keys, code string }{
		{":set ts=0<CR>", "E487"},
		{":set ts=10000<CR>", "E474"},
		{":set sw=-1<CR>", "E487"},
		{":set sts=4<CR>", "E518"},
		{":set sta<CR>", "E518"},
	} {
		h.keys(s.keys)
		h.settle()
		if msg := h.message(); !strings.HasPrefix(msg, s.code) {
			t.Errorf("%s: message = %q, want %s", s.keys, msg, s.code)
		}
	}
}

// タブ文字は、描くたびにバッファの tab.width の幅で描き、カーソルの列と gj も同じ幅で数える。全角文字の後のタブ文字も同じ幅で数える。
// 範囲がバッファごとなので、:set tabstop= は、そのバッファにだけ効く。
//
// req: FR-161
func TestTabWidthDisplay(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "\tx\nあ\ty\nab\tcdefgh\tij\n", "b.txt": "\tz\n"})
	term := newFakeTerm()
	term.w, term.h = 10, 8
	h := startTerm(t, term, Config{WorkDir: dir, Options: map[string]option.Value{option.TabWidth: option.IntValue(4)}}, "a.txt", "b.txt")
	h.settle()
	h.keys(":b 1<CR>")
	h.settle()
	rows := func() []string { return strings.Split(h.term.screen(), "\n") }
	want := []string{"    x", "あ  y", "ab  cdefgh"}
	for i, w := range want {
		if got := strings.TrimRight(rows()[i+1], " "); !strings.HasPrefix(got, w) {
			t.Errorf("tab.width 4 の行 %d = %q, want %q で始まる", i, got, w)
		}
	}
	// 2 行目へ下りると、望みの列（タブ文字の最後の列の 3）から、全角文字の後のタブ文字（列 2 から 3）の上へ移る。
	h.keys("0jx")
	h.settle()
	if got := strings.Split(h.text(), "\n")[1]; got != "あy" {
		t.Errorf("ts=4 の 0jx の後の 2 行目 = %q, want %q", got, "あy")
	}
	h.keys("u")
	h.settle()
	h.keys(":set ts=2<CR>")
	h.settle()
	if got := strings.TrimRight(rows()[1], " "); got != "  x" {
		t.Errorf("ts=2 の後の 1 行目 = %q, want %q", got, "  x")
	}
	// ts=2 では、タブ文字の最後の列は 1 で、全角文字の上へ移る。
	h.keys("gg0jx")
	h.settle()
	if got := strings.Split(h.text(), "\n")[1]; got != "\ty" {
		t.Errorf("ts=2 の 0jx の後の 2 行目 = %q, want %q", got, "\ty")
	}
	h.keys("u")
	h.settle()
	// ほかのバッファの幅は変わらない。
	h.keys(":b 2<CR>")
	h.settle()
	if got := strings.TrimRight(rows()[1], " "); got != "    z" {
		t.Errorf("b.txt の 1 行目 = %q, want %q", got, "    z")
	}

	// 折り返すときも、タブ文字の幅は tab.width で数え、gj は見た目の行で動く。
	h.keys(":b 1<CR>:set wrap ts=4<CR>3G0")
	h.settle()
	// 10 列の画面で "ab" + タブ（2 列）+ "cdefgh"（6 列）で 1 行目が埋まり、2 行目にタブ（4 列。表示の列 10 から 12 の倍数まで 2 列）と ij。
	if got := strings.TrimRight(rows()[4], " "); got != "  ij" {
		t.Errorf("折り返した 2 番目の見た目の行 = %q, want %q", got, "  ij")
	}
	h.keys("gjx")
	h.settle()
	if got := strings.Split(h.text(), "\n")[2]; got != "ab\tcdefghij" {
		t.Errorf("gjx の後の 3 行目 = %q, want %q", got, "ab\tcdefghij")
	}
}
