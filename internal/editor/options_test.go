package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/docs"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 名前の無いバッファで :set filetype= を打つと、ステータス行のファイルの種類が変わり、拡張機能へ buffer/open を送り直し、
// 新しい種類を契機にする拡張機能を起動する。:option.get は今の値を出し、:option.set でも変えられる。
// 名前を付けて書き出しても、決めたファイルの種類は残る。
//
// req: FR-132, FR-134
func TestSetFileType(t *testing.T) {
	dir := t.TempDir()
	status := &ui.StatusLine{Left: []string{ui.StatusPartName}, Right: []string{ui.StatusPartFileType}}
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), StatusLine: status})
	h.keys(`\b`)
	h.eventually("拡張機能の起動", func() bool { return h.e.message.Text == "bufs: open 1 ." })
	if st := h.state("ftx"); st != exthost.StateNotStarted {
		t.Fatalf("ftx: state = %v", st)
	}

	h.keys(":set filetype=fakeft<CR>")
	h.eventually("ftx の起動", func() bool { return h.state("ftx") == exthost.StateRunning })
	h.settle()
	if s := h.term.screen(); !strings.Contains(s, "fakeft") {
		t.Errorf(":set filetype=fakeft の後の画面に種類が無い: %q", s)
	}
	h.keys(`\b`)
	h.eventually("buffer/open の送り直し", func() bool { return h.e.message.Text == "bufs: open 1 ., open 1 ." })
	h.keys(`\y`)
	h.eventually("拡張機能の複製の種類", func() bool { return h.e.message.Text == "b1 . ft=fakeft" })
	h.keys(":option.get filetype<CR>")
	h.eventually(":option.get", func() bool { return h.e.message.Text == "filetype=fakeft" })

	// 同じ値にしても送り直さない（exthost.Host.UpdateBuffer は属性が変わらなければ何もしない）。
	h.keys(":set filetype=fakeft<CR>")
	h.keys(":option.set filetype go<CR>")
	h.keys(`\b`)
	h.eventually(":option.set の後の記録", func() bool { return h.e.message.Text == "bufs: open 1 ., open 1 ., open 1 ." })
	h.keys(`\y`)
	h.eventually(":option.set の後の複製の種類", func() bool { return h.e.message.Text == "b1 . ft=go" })

	// 名前を付けて書き出すと、パスを知らせ直し、種類は残す。
	h.keys("ihello<Esc>:w a.txt<CR>")
	h.eventually("書き出し", func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "a.txt"))
		return err == nil && string(b) == "hello\n"
	})
	h.keys(`\y`)
	h.eventually(":w a.txt の後の複製", func() bool { return h.e.message.Text == "b1 a.txt ft=go" })
	h.e.do(func() {
		if got := h.e.cur.fileType(); got != "go" {
			t.Errorf(":w a.txt の後の種類 = %q, want go", got)
		}
	})
}

// 種類の決まっていない名前の無いバッファに名前を付けて書き出すと、新しい名前から種類を判定する。
//
// req: FR-134
func TestWriteDetectsFileType(t *testing.T) {
	dir := t.TempDir()
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), FileTypes: map[string]string{".fakeft": "fakeft"}})
	h.keys("ix<Esc>:w b.fakeft<CR>")
	h.eventually("ftx の起動", func() bool { return h.state("ftx") == exthost.StateRunning })
	h.e.do(func() {
		if got := h.e.cur.fileType(); got != "fakeft" {
			t.Errorf("種類 = %q, want fakeft", got)
		}
	})
}

// :e と :e! でファイルを開き直すと、バッファごとの項目を初めの値に戻し、拡張機能へ知らせ直す（Vim 9.1 も 'filetype' を判定し直す）。
// 名前の無いバッファを開いたファイルのバッファとして使い回すときも、名前の無いバッファで決めた値を引き継がない。
//
// req: FR-134
func TestReopenResetsBufferOptions(t *testing.T) {
	dir := writeFiles(t, map[string]string{"b.fakeft": "x\n", "a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), FileTypes: map[string]string{".fakeft": "fakeft"}})
	h.keys(":set filetype=go<CR>:e a.txt<CR>")
	h.settle()
	h.e.do(func() {
		if got, want := h.e.cur.fileType(), h.e.detectFileType(filepath.Join(dir, "a.txt")); h.e.cur.path == "" || got != want {
			t.Errorf("使い回した後の種類 = %q, want %q（path = %q）", got, want, h.e.cur.path)
		}
	})
	h.keys(":e b.fakeft<CR>")
	h.eventually("ftx の起動", func() bool { return h.state("ftx") == exthost.StateRunning })
	for _, cmd := range []string{":e<CR>", ":e!<CR>"} {
		h.keys(":set filetype=other<CR>")
		h.keys(`\y`)
		h.eventually(cmd+" の前の複製の種類", func() bool { return h.e.message.Text == "b2 b.fakeft ft=other" })
		h.keys(cmd)
		h.keys(`\y`)
		h.eventually(cmd+" の後の複製の種類", func() bool { return h.e.message.Text == "b2 b.fakeft ft=fakeft" })
		h.e.do(func() {
			if got := h.e.cur.fileType(); got != "fakeft" {
				t.Errorf("%s の後の種類 = %q, want fakeft", cmd, got)
			}
		})
	}
}

// バッファごとの項目は、バッファごとに値を持つ。別のバッファへ移ると、移った先の値を読む。
//
// req: FR-132
func TestBufferOptionPerBuffer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":set filetype=one<CR>:e b.txt<CR>:option.get filetype<CR>")
	h.eventually("b.txt の値", func() bool { return h.e.message.Text == "filetype=" })
	h.keys(":b1<CR>:set filetype?<CR>")
	h.eventually("a.txt の値", func() bool { return h.e.message.Text == "filetype=one" })
	h.keys(":set nosuch<CR>")
	h.eventually("知らない設定", func() bool { return strings.HasPrefix(h.e.message.Text, "E518: ") })
}

// 範囲がエディタ全体の項目の初めの値は Config.Options で渡し、範囲がバッファごとの項目と表に無い名前は使わない。
//
// req: FR-131
func TestEditorOptions(t *testing.T) {
	vs := editorOptions(map[string]option.Value{
		option.FileType: option.StringValue("go"),
		"nosuch":        option.BoolValue(true),
	})
	if got := vs.Get(option.FileType).String(); got != "" {
		t.Errorf("バッファごとの項目 = %q, want 既定値", got)
	}
	if got := vs.Get("nosuch"); got.Valid() {
		t.Errorf("表に無い名前 = %+v", got)
	}
}

// screenRow は、偽の端末に最後に描いた画面の row 行目を、右の空白を除いて返す。
func (h *harness) screenRow(row int) string {
	h.t.Helper()
	return strings.TrimRight(strings.Split(h.term.screen(), "\n")[row], " ")
}

// 設定ファイルの lineNumber.show と lineNumber.minWidth（Config.Options）で、起動したときから行番号の列を出す。
// :set number、:set nonumber、:set number!、:set numberwidth=N は、次の描画から効く（描画が描くたびに今の値を読む）。
// 範囲の外の :set numberwidth は誤りにして、値を変えない。
//
// req: FR-135, FR-136
func TestLineNumberOption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Config{WorkDir: dir, Options: map[string]option.Value{
		option.LineNumberShow: option.BoolValue(true), option.LineNumberMinWidth: option.IntValue(5),
	}}, path)
	// 画面の最初の行はタブの行なので、バッファの最初の行は 2 行目に描く。
	h.eventually("設定ファイルの値で出す", func() bool { return h.screenRow(1) == "   1 one" && h.screenRow(2) == "   2 two" })

	steps := []struct {
		keys string
		row1 string
	}{
		{":set nonumber<CR>", "one"},
		{":set number<CR>", "   1 one"},
		{":set numberwidth=3<CR>", " 1 one"},
		{":set number!<CR>", "one"},
		{":set number!<CR>", " 1 one"},
		{":option.set lineNumber.minWidth 1<CR>", "1 one"},
	}
	for _, s := range steps {
		h.keys(s.keys)
		h.settle()
		h.eventually(s.keys, func() bool { return h.screenRow(1) == s.row1 })
	}

	for _, s := range []struct{ keys, code string }{
		{":set numberwidth=0<CR>", "E487"},
		{":set numberwidth=21<CR>", "E474"},
	} {
		h.keys(s.keys)
		h.settle()
		h.e.do(func() {
			if !strings.HasPrefix(h.e.message.Text, s.code) {
				t.Errorf("%s: message = %q, want %s", s.keys, h.e.message.Text, s.code)
			}
			if got := h.e.options.Get(option.LineNumberMinWidth).Int(); got != 1 {
				t.Errorf("%s: minWidth = %d, want 1（変えない）", s.keys, got)
			}
		})
	}
}

// 利用者向けの文書（docs/usage.md）の設定の名前の一覧に、表のすべての項目がある。
func TestOptionNamesInUsage(t *testing.T) {
	_, list, ok := strings.Cut(docs.Usage, "\n### 設定の名前の一覧\n")
	if !ok {
		t.Fatal("docs/usage.md に「設定の名前の一覧」の節が無い")
	}
	list, _, _ = strings.Cut(list, "\n#")
	for _, d := range option.Defs() {
		if !strings.Contains(list, "| `"+d.Name+"` |") {
			t.Errorf("docs/usage.md の一覧に %s が無い", d.Name)
		}
	}
}
