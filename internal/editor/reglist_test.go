package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/ui"
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

// 一覧の行は、名前の列と種類の列の幅をそろえ、2 行以上のテキストは種類の列に "l+2" の形で行数を書く。
// 履歴が 100 件あっても、番号の列の幅をそろえるのでテキストの始まりはずれない（PR #171 のレビュー）。
//
// req: FR-54
func TestRegisterListLabels(t *testing.T) {
	h := start(t, Config{})
	var got *ui.List
	h.e.do(func() {
		regs := h.e.cur.state.Registers()
		for i := range 100 {
			regs.Set(0, command.Register{Text: fmt.Sprint(i)})
		}
		regs.Set(0, command.Register{Text: "x\ny\nz", Linewise: true})
		regs.Set('a', command.Register{Text: "A"})
		if err := h.e.openRegisterList(h.e.cur); err != nil {
			t.Error(err)
			return
		}
		got = h.e.uiRegList()
	})
	if got == nil {
		t.Fatal("一覧を出さない")
	}
	want := map[int]string{
		0:   "  1  c    A",
		1:   "  2  l+2  x",
		99:  "100  c    2",
		100: ` "a  c    A`,
	}
	for i, w := range want {
		if got.Items[i] != w {
			t.Errorf("Items[%d] = %q, want %q", i, got.Items[i], w)
		}
	}
}

// 表示領域が 2 行に満たない画面では、:registers は窓を開かずに知らせる。開いた後に画面が小さくなれば、次のキーで閉じて知らせる。
//
// req: FR-54
func TestRegisterListNoRoom(t *testing.T) {
	h := start(t, Config{})
	resize := func(height int) {
		h.settle()
		h.term.in <- TermEvent{Resize: true, Width: 40, Height: height}
		h.eventually("画面の大きさ", func() bool { return h.e.renderer.Height == height })
	}
	h.keys("ix<Esc>yl")
	resize(3)
	h.keys(":reg<CR>")
	h.settle()
	if got := h.message(); got != errRegListNoRoom.Error() {
		t.Errorf("message = %q", got)
	}
	resize(10)
	h.keys(":reg<CR>")
	h.settle()
	h.e.do(func() {
		if h.e.regList == nil {
			t.Error("広い画面で窓を開かない")
		}
	})
	resize(3)
	h.keys("<CR>")
	h.settle()
	if got := h.text(); got != "x" {
		t.Errorf("見えない窓で貼った: %q", got)
	}
	if got := h.message(); got != errRegListNoRoom.Error() {
		t.Errorf("message = %q", got)
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
