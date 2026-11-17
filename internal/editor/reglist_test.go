package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command"
)

// :registers は、履歴と名前付きのレジスターの一覧を浮かべる窓に出し、j と k で選び、<CR> で選んだ項目をカーソルの後に貼る。
// 貼った履歴の項目は履歴の先頭へ移り、直後の <C-p> は 1 つ古い項目に置き換える。<Esc> は貼らずに閉じ、他のキーは捨てる。
//
// req: FR-54, FR-55
func TestRegisterList(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one two\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")

	h.keys(`yiwwyiw"ayl$:reg<CR>`)
	// 画面は 6 行なので、窓には見出しと 3 項目が収まる（4 項目目の "a は、選ぶと見える位置までずらす）。
	h.screenHas("一覧の窓", " 3  c  one")
	for _, want := range []string{regListTitle, " 1  c  t", " 2  c  two"} {
		if !strings.Contains(h.term.screen(), want) {
			t.Errorf("一覧に %q が無い:\n%s", want, h.term.screen())
		}
	}
	// 一覧の間の x と : は捨て、バッファを変えない。
	h.keys("x:jjj")
	h.screenHas("ずらした一覧", `"a  c  t`)
	h.keys("kk")
	h.settle()
	if got := h.text(); got != "one two" {
		t.Fatalf("一覧の間のキーで変わった: %q", got)
	}
	h.keys("<CR>")
	h.settle()
	if got := h.text(); got != "one twotwo" {
		t.Fatalf("<CR> の後 = %q", got)
	}
	var hist []string
	h.e.do(func() {
		for _, r := range h.e.cur.state.Registers().History() {
			hist = append(hist, r.Text)
		}
	})
	if strings.Join(hist, ",") != "two,t,one" {
		t.Errorf("履歴 = %q（選んだ項目を先頭へ移していない）", hist)
	}
	h.keys("<C-p>")
	h.settle()
	if got := h.text(); got != "one twot" {
		t.Errorf("<C-p> の後 = %q", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "one two" {
		t.Errorf("u の後 = %q", got)
	}

	// <Esc> は貼らずに閉じ、その後のキーはふつうのコマンドになる。
	h.keys(":registers<CR>")
	h.screenHas("一覧の窓", regListTitle)
	h.keys("<Esc>x")
	h.settle()
	h.eventually("ノーマルモード", func() bool { return h.e.regList == nil && h.e.cur.state.Mode() == command.ModeNormal })
	if got := h.text(); got != "one tw" {
		t.Errorf("<Esc>x の後 = %q", got)
	}
}

// レジスターも履歴も空なら、:registers は窓を出さずに知らせる。
//
// req: FR-54
func TestRegisterListEmpty(t *testing.T) {
	h := start(t, Config{})
	h.keys(":reg<CR>")
	h.settle()
	if got := h.message(); got != errNoRegisters.Error() {
		t.Errorf("message = %q", got)
	}
	h.e.do(func() {
		if h.e.regList != nil {
			t.Error("窓を出した")
		}
	})
}
