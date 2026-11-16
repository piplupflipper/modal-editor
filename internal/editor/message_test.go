package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
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
	h.keys("iabc def<CR>ghi<CR>f()<Esc>gg0")
	h.settle()
	h.e.do(func() {
		bs := h.e.cur
		rng := func(l, b0, b1 int) ext.Range {
			return ext.Range{Start: ext.Position{Line: l, Byte: b0}, End: ext.Position{Line: l, Byte: b1}}
		}
		h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Diagnostics: []ext.Diagnostic{
			{Range: rng(0, 4, 7), Severity: ext.SeverityError, Message: "undefined: def", Source: "gopls"},
			{Range: rng(0, 0, 3), Severity: ext.SeverityHint, Message: "unused abc"},
			// gopls v0.24.0 の型検査の診断の形（PR #134 のレビューで実物から写したもの）。本文に改行とタブを含む。
			{Range: rng(2, 0, 3), Severity: ext.SeverityError, Message: "not enough arguments in call to f\n\thave ()\n\twant (int)", Source: "compiler"},
		}})
	})
	h.keys("<Esc>")
	h.screenHas("カーソルの位置を含む診断", "H unused abc")
	h.keys("w")
	h.screenHas("重い診断", "E gopls: undefined: def")
	h.keys("j")
	h.eventually("診断の無い行", func() bool { return !strings.Contains(h.term.screen(), "undefined") })
	// 改行を含む本文は 1 行に詰め、メッセージの行を上へ広げない（ステータス行とバッファの行を覆わない）。
	h.keys("j")
	h.screenHas("改行を含む診断", "E compiler: not enough arguments in call to f have () want (int)")
	if scr := h.trimmedScreen(); !strings.Contains(scr, "f()\n") || !strings.Contains(scr, "NORMAL") {
		t.Errorf("診断の本文がバッファの行かステータス行を覆った:\n%s", scr)
	}
	h.keys("gg:foo<CR>")
	h.settle()
	if scr := h.term.screen(); strings.Contains(scr, "undefined") || !strings.Contains(scr, "E492") {
		t.Errorf("メッセージがあれば診断の本文は出さない:\n%s", scr)
	}
}

// :w の後のコマンドの誤りは、:w の「書き込みました」に上書きされずに見える（Vim 9.1 の :w|foo と同じく、書いた後に E492 を出す）。
// :w が失敗したら、その誤りを知らせ、右のコマンドの誤りは知らせない。
func TestMessageOrderAfterWrite(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys("x:w|foo<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf(":w|foo: message = %q, want E492", msg)
	}
	if disk, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(disk) != "bc\ndef\n" {
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
	// :w が失敗したら、右のコマンドの確認の問い（逆順の範囲）も出さない（Vim 9.1 は :w! dir|foo で E502 を出し、右を実行しない）。
	h.keys(":w! " + dir + "|2,1d<CR>")
	h.settle()
	var asked bool
	h.e.do(func() { asked = h.e.confirm != nil })
	if msg := h.message(); asked || !strings.HasPrefix(msg, "E212") {
		t.Errorf(":w! dir|2,1d: 問い = %v, message = %q, want 問いなしの E212", asked, msg)
	}
	h.keys("y")
	h.settle()
	if got := h.text(); got != "bc\ndef" {
		t.Errorf(":w! dir|2,1d の後の y: text = %q（消さない）", got)
	}
}

// 改行を含むメッセージの間は、Vim の hit-enter の問いと同じく、メッセージの下に問いを出してカーソルを問いの末尾に置き、
// <CR> と <Space> はメッセージを消すだけにする。他のキーは、メッセージを消してからコマンドとして使う。
// インサートモードでも同じで、コマンドラインモードでは問いを出さない（#135）。
//
// 期待値は Vim 9.1 で確かめた（:echo "aaa\nbbb" の後の <CR>、<Space>、<Esc> はカーソルを動かさず、j と <BS> は動かす。
// インサートモードの間にタイマーで出した 2 行の :echo の後の <CR> は、改行を入れない）。
func TestHitEnterPrompt(t *testing.T) {
	h := start(t, Config{})
	h.keys("iabc<CR>def<CR>ghi<Esc>k")
	h.settle()
	show := func(text string) {
		h.e.do(func() {
			h.e.handleEvent(&exthost.MessageShow{Params: ext.MessageShowParams{Level: ext.MessageInfo, Text: text}})
		})
	}
	cursor := func() (line, off int) {
		h.e.do(func() {
			c := h.e.cur.state.Cursor()
			line, off = c.Line, c.Byte
		})
		return line, off
	}
	screenCursor := func() ui.Cursor {
		h.term.mu.Lock()
		defer h.term.mu.Unlock()
		return h.term.last.Cursor
	}

	show("aaa\nbbb")
	h.screenHas("問い", "aaa\nbbb\n"+ui.HitEnterPrompt)
	want := ui.Cursor{Row: 5, Col: ui.StringWidth(ui.HitEnterPrompt, 0), Shape: ui.CursorBlock, Visible: true}
	if c := screenCursor(); c != want {
		t.Errorf("問いの間のカーソル = %+v, want %+v（問いの末尾）", c, want)
	}
	for _, k := range []string{"<CR>", "<Space>"} {
		show("aaa\nbbb")
		h.screenHas(k+" の前の問い", ui.HitEnterPrompt)
		h.keys(k)
		h.settle()
		if l, b := cursor(); l != 1 || b != 2 || h.message() != "" {
			t.Errorf("%s: cursor = %d:%d, message = %q, want 1:2 で消える（コマンドとして使わない）", k, l, b, h.message())
		}
	}
	if scr := h.trimmedScreen(); strings.Contains(scr, ui.HitEnterPrompt) {
		t.Errorf("消した後も問いが残る:\n%s", scr)
	}
	show("aaa\nbbb")
	h.screenHas("j の前の問い", ui.HitEnterPrompt)
	h.keys("j")
	h.settle()
	if l, _ := cursor(); l != 2 || h.message() != "" {
		t.Errorf("j: cursor の行 = %d, message = %q, want 2 で消える（コマンドとして使う）", l, h.message())
	}
	// 1 行のメッセージは問いを出さず、<CR> はそのまま使う（インサートモードなら改行を入れる）。
	h.keys("A")
	h.settle()
	show("one line")
	h.screenHas("1 行のメッセージ", "one line")
	h.keys("<CR>x<Esc>")
	h.settle()
	if got := h.text(); got != "abc\ndef\nghi\nx" {
		t.Errorf("1 行のメッセージの後の <CR>: text = %q, want 改行を入れる", got)
	}
	// インサートモードでも、改行を含むメッセージの間の <CR> は、改行を入れずにメッセージを消す。
	h.keys("A")
	h.settle()
	show("aaa\nbbb")
	h.screenHas("インサートモードの問い", ui.HitEnterPrompt)
	h.keys("<CR>y<Esc>")
	h.settle()
	if got := h.text(); got != "abc\ndef\nghi\nxy" {
		t.Errorf("インサートモードの <CR>: text = %q, want 改行を入れない", got)
	}
	// コマンドラインモードでは問いを出さず、<CR> はコマンドラインを実行する。
	h.keys(":1")
	h.settle()
	show("aaa\nbbb")
	h.keys("<CR>")
	h.settle()
	if l, _ := cursor(); l != 0 {
		t.Errorf("コマンドラインモードの <CR>: cursor の行 = %d, want 0（:1 を実行する）", l)
	}
}
