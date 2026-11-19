package editor

import (
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
