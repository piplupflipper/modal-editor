package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// popup/show の本文は、2 行目以降もカーソルの下に浮かべて出し、次のキー入力で消す。
//
// req: FR-30
func TestPopupLines(t *testing.T) {
	h := start(t, Config{})
	h.keys("iabc<CR>def<Esc>gg")
	h.settle()
	h.e.do(func() {
		bs := h.e.cur
		h.e.showPopup(ext.PopupShowParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Text: "func f()\n\n説明の 2 行目", Format: "markdown"})
	})
	// キーを送ると消えるので、送らずに待つ（イベントループは do の関数の後にも描く）。
	h.screenHas("ポップアップの 3 行目", " 説明の 2 行目")
	if scr := h.trimmedScreen(); !strings.Contains(scr, "abc\n func f()") {
		t.Errorf("1 行目の下に出ていない:\n%s", scr)
	}
	h.keys("j")
	h.eventually("キーで消える", func() bool { return !strings.Contains(h.term.screen(), "説明の") })
}

// カーソルの行に診断があれば、他に出すメッセージが無いときに、その本文をメッセージの行に出す。
// 行に複数あれば、カーソルの位置を含むものを先に、その中で重いものを選ぶ。メッセージがあれば、そちらを出す。
//
// req: FR-30
func TestCursorDiagnosticMessage(t *testing.T) {
	h := start(t, Config{})
	h.keys("iabc def<CR>ghi<Esc>gg0")
	h.settle()
	h.e.do(func() {
		bs := h.e.cur
		rng := func(l, b0, b1 int) ext.Range {
			return ext.Range{Start: ext.Position{Line: l, Byte: b0}, End: ext.Position{Line: l, Byte: b1}}
		}
		h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
			{Range: rng(0, 4, 7), Severity: ext.SeverityError, Message: "undefined: def", Source: "gopls"},
			{Range: rng(0, 0, 3), Severity: ext.SeverityHint, Message: "unused abc"},
		}})
	})
	h.keys("<Esc>")
	h.screenHas("カーソルの位置を含む診断", "H unused abc")
	h.keys("w")
	h.screenHas("重い診断", "E gopls: undefined: def")
	h.keys("j")
	h.eventually("診断の無い行", func() bool { return !strings.Contains(h.term.screen(), "undefined") })
	h.keys("k:foo<CR>")
	h.settle()
	if scr := h.term.screen(); strings.Contains(scr, "undefined") || !strings.Contains(scr, "E492") {
		t.Errorf("メッセージがあれば診断の本文は出さない:\n%s", scr)
	}
}

// :w の後のコマンドの誤りは、:w の「書き込みました」に上書きされずに見える（Vim 9.1 の :w|foo と同じく、書いた後に E492 を出す）。
// :w が失敗したら、その誤りを知らせ、右のコマンドの誤りは知らせない。
func TestMessageOrderAfterWrite(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys("x:w|foo<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf(":w|foo: message = %q, want E492", msg)
	}
	if disk, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(disk) != "bc\n" {
		t.Errorf(":w|foo: disk = %q, %v（:w は行う）", disk, err)
	}
	h.keys(":w|fake.upper<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E319") {
		t.Errorf(":w|fake.upper: message = %q, want E319", msg)
	}
	// 書けないファイル（ディレクトリ）への :w は E212 で止まり、右の誤りは知らせない。
	h.keys(":w! " + dir + "|foo<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E212") {
		t.Errorf(":w! dir|foo: message = %q, want E212", msg)
	}
}
