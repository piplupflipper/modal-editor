package editor

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// ビジュアルモードの I と A で選んだ行ごとに同時に入力している途中に、外からの編集（拡張機能の buffer/edit。MCP の edit_buffer、
// ACP の書き込み）が入ると、applyExternal の ShiftPositions が同時の入力の位置もそれぞれずらす。
// 消した行の位置と、ほかの位置と同じ行へ寄った位置は落とし、残りの位置には続けて入力が入る。
//
// req: FR-141
func TestVisualInsertExternalEdit(t *testing.T) {
	const text, indented = "abc\ndef\nghi\n", "  a\n  b\n  c\n"
	at := func(l, b int) buffer.Position { return buffer.Position{Line: l, Byte: b} }
	tests := []struct {
		name, text, keys string
		edit             buffer.Edit
		wantText         string
	}{
		{"先頭に行を足す", text, "VjjAX", buffer.Edit{Text: "Z\n"}, "Z\nabcXY\ndefXY\nghiXY"},
		{"途中の行を消す", text, "VjjAX", buffer.Edit{Range: buffer.Range{Start: at(1, 0), End: at(2, 0)}}, "abcXY\nghiXY"},
		{"末尾の行を消す", text, "VjjAX", buffer.Edit{Range: buffer.Range{Start: at(0, 4), End: at(2, 4)}}, "abcXY"},
		{"位置の前に文字を足す", text, "VjjAX", buffer.Edit{Range: buffer.Range{Start: at(1, 0), End: at(1, 0)}, Text: "Q"}, "abcXY\nQdefXY\nghiXY"},
		{"途中に行を足す", text, "VjjAX", buffer.Edit{Range: buffer.Range{Start: at(1, 0), End: at(1, 0)}, Text: "M\n"}, "abcXY\nM\ndefXY\nghiXY"},
		// I の位置（字下げの後）で、最後の行を前の行の行末から消す（MCP の edit_buffer で最後の行を消す形。PR #274 のレビューの必須 1）。
		// 消した行の位置は前の行の行末へ寄るが、範囲の内側にあったので落とし、前の行の字下げの後の位置を残す。
		{"I で最後の行を消す", indented, "VjjIX", buffer.Edit{Range: buffer.Range{Start: at(1, 4), End: at(2, 4)}}, "  XYa\n  XYb"},
		// 列 0 の位置の行（beta）を消す。消した行の位置ではなく、下の行（gamma の字下げの後）の位置を残す（PR #274 の再レビューの必須）。
		{"I で列 0 の位置の行を消す", "  alpha\nbeta\n  gamma\n", "VjjI", buffer.Edit{Range: buffer.Range{Start: at(1, 0), End: at(2, 0)}}, "  Yalpha\n  Ygamma"},
		// 行を丸ごと書き換えると、その行の位置は範囲の内側なので落とす（行の先頭には入れない。レビューの推奨 3）。
		{"I で行を書き換える", indented, "VjjIX", buffer.Edit{Range: buffer.Range{Start: at(1, 0), End: at(1, 4)}, Text: "  Xbb"}, "  XYa\n  Xbb\n  XYc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": tt.text})
			h := start(t, Config{WorkDir: dir}, "a.txt")
			h.keys(tt.keys)
			h.settle()
			h.e.do(func() {
				bs := h.e.cur
				tx := buffer.Transaction{BaseVersion: bs.buf.Version(), Edits: []buffer.Edit{tt.edit}}
				if _, err := h.e.applyExternal(bs, tx); err != nil {
					t.Error(err)
				}
			})
			h.keys("Y<Esc>")
			h.settle()
			if got := h.text(); got != tt.wantText {
				t.Errorf("text = %q, want %q", got, tt.wantText)
			}
		})
	}
}
