package editor

import (
	"fmt"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

func pos(line, b int) buffer.Position { return buffer.Position{Line: line, Byte: b} }

func edit(sl, sb, el, eb int, text string) buffer.Edit {
	return buffer.Edit{Range: buffer.Range{Start: pos(sl, sb), End: pos(el, eb)}, Text: text}
}

// 診断と印の位置は、基本設計 6.7 の規則で編集に合わせてずれる。
func TestShiftPos(t *testing.T) {
	tests := []struct {
		name string
		p    buffer.Position
		ed   buffer.Edit
		want buffer.Position
	}{
		{"範囲より前", pos(0, 1), edit(0, 3, 0, 5, "x"), pos(0, 1)},
		{"同じ行の後ろ", pos(0, 7), edit(0, 3, 0, 5, "x"), pos(0, 6)},
		{"範囲の終わりは後ろ", pos(0, 5), edit(0, 3, 0, 5, "x"), pos(0, 4)},
		{"重なる位置は開始へ", pos(0, 4), edit(0, 3, 0, 5, "x"), pos(0, 3)},
		{"範囲の開始は重なる", pos(0, 3), edit(0, 3, 0, 5, "x"), pos(0, 3)},
		{"行を足すと下の行はずれる", pos(3, 2), edit(1, 0, 1, 0, "a\nb\n"), pos(5, 2)},
		{"行の先頭への挿入で先頭の印は下へ", pos(1, 0), edit(1, 0, 1, 0, "new\n"), pos(2, 0)},
		{"行の削除で上へ", pos(4, 1), edit(1, 0, 3, 0, ""), pos(2, 1)},
		{"終わりの行の後ろは列もずれる", pos(2, 5), edit(0, 2, 2, 3, "XY"), pos(0, 6)},
		{"複数行の挿入の後の同じ行", pos(0, 6), edit(0, 4, 0, 4, "ab\ncde"), pos(1, 5)},
	}
	for _, tt := range tests {
		if got := shiftPos(tt.p, tt.ed); got != tt.want {
			t.Errorf("%s: shiftPos(%v) = %v, want %v", tt.name, tt.p, got, tt.want)
		}
	}
}

// TestEmptyDisplayDataOutOfHistory は、本体が覚えている編集の範囲より古い番号の空の一覧（診断、印、色）を受け付けて、
// その拡張機能の一覧を消すことを確かめる（基本設計 6.7 の表。#267）。
// 再現の手順は、LSP の拡張機能が空の diagnostic/set を送った版のあと、それが本体に届く前に、
// :set filetype=text<CR>50@b のようにマクロで maxHistory を超える編集が続く場合である。
// ここでは、一覧を付けた版を覚えてから、1 行足すマクロを maxHistory より多く再生し、その版で空の一覧を送る。
// 空でない一覧は、これまでどおり捨てる。
//
// req: FR-30
func TestEmptyDisplayDataOutOfHistory(t *testing.T) {
	h := start(t, Config{})
	rng := ext.Range{Start: ext.Position{Line: 0}, End: ext.Position{Line: 0, Byte: 1}}
	diags := []ext.Diagnostic{{Range: rng, Severity: ext.SeverityError, Message: "m"}}
	signs := []ext.Sign{{Line: 0, Kind: ext.SignBreakpoint}}
	decos := []ext.Decoration{{Range: rng, Style: "keyword"}}
	h.keys("ia<Esc>")
	h.settle()
	var v ext.Version
	h.e.do(func() {
		e := h.e
		bs := e.cur
		v = ext.Version(bs.buf.Version())
		e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: diags})
		e.setSigns("x", ext.SignSetParams{Buffer: bs.id, Version: v, Signs: signs})
		e.setDecorations("x", ext.DecorationSetParams{Buffer: bs.id, Version: v, Decorations: decos})
	})
	h.keys(fmt.Sprintf("qbo//<Esc>q%d@b", maxHistory+20))
	h.settle()
	h.e.do(func() {
		e := h.e
		bs := e.cur
		if _, ok := bs.editsSince(uint64(v)); ok {
			t.Fatalf("版 %d がまだ覚えている編集の範囲にある（現在 %d）", v, bs.buf.Version())
		}
		if e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: diags}) {
			t.Error("覚えていない番号の空でない診断を持った")
		}
		if e.setSigns("x", ext.SignSetParams{Buffer: bs.id, Version: v, Signs: signs}) {
			t.Error("覚えていない番号の空でない印を持った")
		}
		if len(bs.diagnostics["x"]) != 1 || len(bs.signs["x"]) != 1 {
			t.Fatalf("空の一覧の前に一覧が消えた: %+v %+v", bs.diagnostics["x"], bs.signs["x"])
		}
		if !e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: v}) || len(bs.diagnostics["x"]) != 0 {
			t.Errorf("覚えていない番号の空の診断で消えない: %+v", bs.diagnostics["x"])
		}
		if !e.setSigns("x", ext.SignSetParams{Buffer: bs.id, Version: v}) || len(bs.signs["x"]) != 0 {
			t.Errorf("覚えていない番号の空の印で消えない: %+v", bs.signs["x"])
		}
		e.setDecorations("x", ext.DecorationSetParams{Buffer: bs.id, Version: v})
		if _, ok := bs.decorations["x"]; ok {
			t.Errorf("覚えていない番号の空の色で消えない: %+v", bs.decorations["x"])
		}
	})
}

// diagnostic/set は、現在のバージョン番号ならそのまま、覚えている範囲の古い番号ならずらして持ち、
// それより古いか新しければ捨てる。持っている診断は、以後の編集でずれる。
func TestDiagnosticVersions(t *testing.T) {
	h := start(t, Config{})
	diag := func(line int) []ext.Diagnostic {
		return []ext.Diagnostic{{Range: ext.Range{Start: ext.Position{Line: line}, End: ext.Position{Line: line, Byte: 1}}, Severity: ext.SeverityError, Message: "m"}}
	}
	h.keys("ia<CR>b<CR>c<Esc>")
	h.settle()
	h.e.do(func() {
		e := h.e
		bs := e.cur
		v := bs.buf.Version()
		set := func(version uint64, line int) bool {
			return e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(version), Diagnostics: diag(line)})
		}
		if !set(v, 2) || bs.diagnostics["x"][0].Range.Start.Line != 2 {
			t.Errorf("現在の番号: %+v", bs.diagnostics["x"])
		}
		// 3 行目（c）を、3 行に増やす前（最初の <CR> の前）の番号で送ると、行はずれる。
		before := bs.history[0].Before
		if !set(before+1, 0) || bs.diagnostics["x"][0].Range.Start.Line != 0 {
			t.Errorf("古い番号: %+v", bs.diagnostics["x"])
		}
		if set(v+1, 0) {
			t.Error("新しい番号を持った")
		}
		// 覚えている変更より古い番号の一覧は捨て、持っている一覧を変えない。
		bs.history = bs.history[2:]
		if set(before+1, 1) || bs.diagnostics["x"][0].Range.Start.Line != 0 {
			t.Errorf("覚えていない番号を持った: %+v", bs.diagnostics["x"])
		}
	})
	// 空の一覧は、新しい番号なら捨て、覚えていない古い番号なら受け付けて消す（TestEmptyDisplayDataOutOfHistory）。
	h.e.do(func() {
		e := h.e
		bs := e.cur
		v := bs.buf.Version()
		if e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: bs.id, Version: ext.Version(v + 1)}) || len(bs.diagnostics["x"]) != 1 {
			t.Errorf("新しい番号の空の一覧を受け付けた: %+v", bs.diagnostics["x"])
		}
	})
	// 先頭に行を足すと、持っている診断は下へずれる。
	h.e.do(func() {
		h.e.setDiagnostics("x", ext.DiagnosticSetParams{Buffer: h.e.cur.id, Version: ext.Version(h.e.cur.buf.Version()), Diagnostics: diag(1)})
	})
	h.keys("ggOz<Esc>")
	h.settle()
	h.e.do(func() {
		if got := h.e.cur.diagnostics["x"][0].Range.Start.Line; got != 2 {
			t.Errorf("O の後の行 = %d", got)
		}
		res := h.e.diagnosticList(ext.DiagnosticListParams{})
		if len(res.Entries) != 1 || uint64(res.Entries[0].Version) != h.e.cur.buf.Version() {
			t.Errorf("diagnostic/list = %+v", res)
		}
	})
}
