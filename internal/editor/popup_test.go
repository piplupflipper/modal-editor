package editor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// popupText は、読み込んでいる client のポップアップのバッファの中身を返す。ポップアップが無ければ空である。
// イベントループの goroutine で呼ぶ。
func popupText(e *Editor) string {
	if e.float == nil {
		return ""
	}
	return e.float.bs.buf.Text()
}

// floatInfo は、ポップアップの状態をテストで比べる形にしたものである。
type floatInfo struct {
	text            string
	focused, center bool
	kind            floatKind
}

func (h *harness) float() (floatInfo, bool) {
	var fi floatInfo
	var ok bool
	h.e.do(func() {
		h.e.expireFloat()
		if f := h.e.float; f != nil {
			ok = true
			fi = floatInfo{text: f.bs.buf.Text(), focused: f.focused && h.e.cur == f.bs, center: f.center, kind: f.kind}
		}
	})
	return fi, ok
}

// ホバーは、テキストのバッファの部品のポップアップとしてカーソルの近くに出す。フォーカスを入れていなければ次のキーで閉じ、
// <C-w>w でフォーカスを入れるとキーがポップアップのバッファへ入る（移動、ヤンク、q のマクロの記録）。編集は E21 で断る。
// <Esc> で閉じて元のタブへ戻る。ポップアップのために作ったバッファは :ls に出さず、閉じたら消える。
//
// req: FR-68, FR-71
func TestHoverPopupFocus(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.provft": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: providerSpecs(t), FileTypes: map[string]string{".provft": "provft"}}, "a.provft")
	h.keys("K")
	h.eventually("ホバー", func() bool { return popupText(h.e) == "hover-popup" })
	if fi, _ := h.float(); fi.focused || fi.center {
		t.Errorf("ホバー: %+v, want カーソルの近くでフォーカスなし", fi)
	}
	h.keys("j")
	h.settle()
	if _, ok := h.float(); ok {
		t.Errorf("j の後もホバーが残っている")
	}

	h.keys("kK")
	h.eventually("2 回目のホバー", func() bool { return popupText(h.e) == "hover-popup" })
	h.keys("<C-w>w")
	h.settle()
	if fi, ok := h.float(); !ok || !fi.focused {
		t.Fatalf("<C-w>w: %+v %v, want フォーカスあり", fi, ok)
	}
	var ls string
	h.keys("x")
	h.settle()
	if msg := h.message(); msg != "E21: 編集を禁止したバッファは変更できません" {
		t.Errorf("x in the hover: message %q", msg)
	}
	// q はポップアップの中でもマクロの記録である（#177 の裁定）。記録の後もポップアップは開いている。
	h.keys("qaywq")
	h.settle()
	var reg string
	h.e.do(func() {
		r, _ := h.e.cur.state.Registers().Get('a')
		reg = r.Text
		ls = h.e.bufferList()
	})
	if _, ok := h.float(); !ok || reg != "yw" {
		t.Errorf("qaywq in the hover: popup open %v, register a %q", ok, reg)
	}
	if strings.Contains(ls, "ポップアップ") || strings.Count(ls, "\n") != 0 {
		t.Errorf(":ls lists the popup buffer:\n%s", ls)
	}
	// <C-w>w は、フォーカスのあるポップアップから元のタブへ戻す（ポップアップは残す）。もう一度で入れ直す。
	h.keys("<C-w>w")
	h.settle()
	if fi, ok := h.float(); !ok || fi.focused {
		t.Errorf("2 回目の <C-w>w: %+v %v, want フォーカスなしで残る", fi, ok)
	}
	h.keys("<C-w>w<Esc>")
	h.settle()
	var bufs int
	var cur string
	h.e.do(func() { bufs, cur = len(h.e.bufs), filepath.Base(h.e.cur.path) })
	if _, ok := h.float(); ok || bufs != 1 || cur != "a.provft" {
		t.Errorf("<Esc>: popup open %v, buffers %d, cursor in %q", ok, bufs, cur)
	}
}

// 「ポップアップで見る」（<Space>ld）は、移動先の周りの行の写しを、編集を禁止したバッファとしてカーソルの近くに出し、移らない。
// :tabopen で広げると、本物のファイルの移動先の位置へ移る。開いていないファイルの写しを広げると、新しいタブで開く。
//
// req: FR-110, FR-70
func TestPeekDefinition(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.provft": "l0\nl1\nl2\nl3\n", "b.txt": "b0\nb1\nb2\nb3\nb4\nb5\n"})
	h := start(t, Config{WorkDir: dir, Extensions: providerSpecs(t), FileTypes: map[string]string{".provft": "provft"}}, "a.provft")
	h.keys("j<Space>ld")
	h.eventually("写しのポップアップ", func() bool { return h.e.float != nil && h.e.float.peek != nil })
	var cursor, base buffer.Position
	var readOnly bool
	var title string
	h.e.do(func() {
		f := h.e.float
		cursor, base, readOnly, title = f.bs.state.Cursor(), h.e.cur.state.Cursor(), f.bs.buf.ReadOnly(), f.title
	})
	if fi, _ := h.float(); fi.text != "l0\nl1\nl2\nl3" || fi.focused || cursor.Line != 2 || base.Line != 1 || !readOnly || title != "a.provft:3" {
		t.Errorf("<Space>ld: %+v, popup cursor %v, base cursor %v, read-only %v, title %q", fi, cursor, base, readOnly, title)
	}
	h.keys("<C-w>w:tabopen<CR>")
	h.settle()
	var tabs int
	h.e.do(func() { base, tabs = h.e.cur.state.Cursor(), len(h.e.view.tabs) })
	if _, ok := h.float(); ok || base.Line != 2 || tabs != 1 {
		t.Errorf(":tabopen: popup open %v, cursor %v, tabs %d, message %q", ok, base, tabs, h.message())
	}

	// 開いていないファイルの写し。
	b := filepath.Join(dir, "b.txt")
	h.e.do(func() {
		if _, _, err := h.e.editorOpen(ext.EditorOpenParams{Path: b, Position: &ext.Position{Line: 4, Byte: 1}, Peek: true}); err != nil {
			t.Errorf("editor/open peek: %v", err)
		}
	})
	if fi, _ := h.float(); fi.text != "b1\nb2\nb3\nb4\nb5" {
		t.Errorf("b.txt の写し: %q", fi.text)
	}
	h.keys("<C-w>w:tabopen<CR>")
	h.settle()
	var path string
	h.e.do(func() { path, base, tabs = h.e.cur.path, h.e.cur.state.Cursor(), len(h.e.view.tabs) })
	if path != b || base != (buffer.Position{Line: 4, Byte: 1}) || tabs != 2 {
		t.Errorf(":tabopen of b.txt: path %q, cursor %v, tabs %d, message %q", path, base, tabs, h.message())
	}
}

// :popup は、カーソルのあるタブを中央のポップアップへ縮め、:tabopen で広げる。バッファの番号、内容、未保存の変更は変わらず、
// ポップアップの中でも通常の編集のキーが効く。<Esc> で閉じるとタブの並びへ戻る。タブが 1 つなら縮めない。
//
// req: FR-70, FR-71
func TestShrinkExpandTab(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":popup<CR>")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "タブが 1 つだけ") {
		t.Errorf(":popup with one tab: message %q", msg)
	}
	h.keys(":e b.txt<CR>ix<Esc>:popup<CR>")
	h.settle()
	var tabs int
	var id ext.BufferID
	h.e.do(func() { tabs, id = len(h.e.view.tabs), h.e.cur.id })
	fi, ok := h.float()
	if !ok || !fi.focused || !fi.center || fi.text != "xb" || tabs != 1 {
		t.Fatalf(":popup: %+v %v, tabs %d", fi, ok, tabs)
	}
	h.keys("Ay<Esc>")
	h.settle()
	if fi, _ := h.float(); fi.text != "xby" {
		t.Errorf("A in the popup: %q", fi.text)
	}
	h.keys(":tabopen<CR>")
	h.settle()
	var modified bool
	var cur ext.BufferID
	h.e.do(func() { tabs, cur, modified = len(h.e.view.tabs), h.e.cur.id, h.e.cur.buf.Modified() })
	if _, ok := h.float(); ok || tabs != 2 || cur != id || !modified || h.text() != "xby" {
		t.Errorf(":tabopen: popup %v, tabs %d, buffer %d (want %d), modified %v, text %q", ok, tabs, cur, id, modified, h.text())
	}
	// <Esc> で閉じると、タブの並びへ戻り、カーソルのあるタブは縮めたときに移ったタブのまま。
	h.keys(":popup<CR><Esc>")
	h.settle()
	var name string
	h.e.do(func() { tabs, name = len(h.e.view.tabs), filepath.Base(h.e.cur.path) })
	if _, ok := h.float(); ok || tabs != 2 || name != "a.txt" {
		t.Errorf("<Esc> after :popup: popup %v, tabs %d, cursor in %q", ok, tabs, name)
	}
}

// 拡張機能の popup/open は、部品のデータ（一覧、木、入力の行、テキスト）で開き、色を付ける範囲を描く。
// 一覧の <CR> は view/select で項目の ID を、contributes.popupKeys のキーは view/key で ID を知らせ、<Esc> は閉じて view/closed を送る。
// view/update で中身を置き換え、view/close で閉じる。
//
// req: FR-68, FR-69, FR-72
func TestExtensionPopup(t *testing.T) {
	h := start(t, Config{Extensions: fakeSpecs(t)})
	open := func(kind string) {
		t.Helper()
		var id uint64
		h.e.do(func() {
			if h.e.float != nil {
				id = h.e.float.id
			}
		})
		h.keys(":fake.popup " + kind + "<CR>")
		h.eventually(kind+" のポップアップ", func() bool { return h.e.float != nil && h.e.float.id != id && h.e.float.ext == "fake" })
	}
	open("list")
	fi, _ := h.float()
	if fi.text != "first\nclose me\nupdate me" || !fi.focused || !fi.center || fi.kind != floatList {
		t.Fatalf("list: %+v", fi)
	}
	var decorated bool
	h.e.do(func() { _, decorated = h.e.float.bs.decorations[popupDecorationOwner] })
	if !decorated {
		t.Errorf("list: highlights are not set")
	}
	var view uint64
	h.e.do(func() { view = h.e.float.id })

	// popupKeys の x は、本体の x（文字を消す）より先に効き、カーソルの行の項目と一緒に知らせる（FR-69）。2 つのキーの列も引く。
	h.keys("x")
	h.waitMessage("kind=3 item=one key=fake.pick")
	h.keys("jzq")
	h.waitMessage("kind=3 item=close key=fake.two")
	// 選んだ項目（close）に、拡張機能は view/close で応える。
	h.keys("<CR>")
	h.waitMessage("kind=1 item=close")
	h.eventually("view/close", func() bool { return h.e.float == nil })

	open("list")
	h.keys("jj<CR>")
	h.eventually("view/update", func() bool { return popupText(h.e) == "updated" })
	h.e.do(func() { view = h.e.float.id })
	h.keys("<Esc>")
	h.waitMessage("kind=0 item= key=")
	if _, ok := h.float(); ok {
		t.Errorf("<Esc> did not close the popup %d", view)
	}

	// 木は、子を持つ項目の <CR> で開閉し、子の無い項目の <CR> で選ぶ。
	open("tree")
	if fi, _ := h.float(); fi.text != "▸ dir\n  top" {
		t.Errorf("tree: %q", fi.text)
	}
	h.keys("<CR>")
	h.settle()
	if fi, _ := h.float(); fi.text != "▾ dir\n    a\n    b\n  top" {
		t.Errorf("tree after <CR>: %q", fi.text)
	}
	h.keys("j<CR>")
	h.waitMessage("kind=1 item=dir/a")

	// 入力の行は編集でき、中身が変わるたびに input/change で、<CR> で view/select で知らせる。
	open("input")
	h.keys("A!")
	h.waitMessage("kind=2 item=old!")
	h.keys("<CR>")
	h.waitMessage("kind=1 item=old!")
	h.keys("<Esc><Esc>")
	h.settle()

	// placement が cursor のテキストは、フォーカスを入れずにカーソルの近くに出し、次のキーで閉じる。
	open("text")
	if fi, _ := h.float(); fi.text != "alpha beta\ngamma" || fi.focused || fi.center {
		t.Errorf("text: %+v", fi)
	}
	h.keys("l")
	h.waitMessage("kind=0")
	if _, ok := h.float(); ok {
		t.Errorf("text popup is still open after l")
	}
}

// 中央のポップアップは、開いている間はそれだけを操作し、<C-w>w で外へ出ない（FR-68）。
//
// req: FR-68
func TestCenterPopupKeepsFocus(t *testing.T) {
	h := start(t, Config{})
	h.keys(":help<CR><C-w>w")
	h.settle()
	if fi, ok := h.float(); !ok || !fi.focused {
		t.Errorf("<C-w>w in the help: %+v %v", fi, ok)
	}
	if msg := h.message(); !strings.Contains(msg, "中央のポップアップ") {
		t.Errorf("<C-w>w in the help: message %q", msg)
	}
}
