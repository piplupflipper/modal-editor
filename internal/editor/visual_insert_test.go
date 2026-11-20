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
	const text = "abc\ndef\nghi\n"
	tests := []struct {
		name     string
		edit     buffer.Edit
		wantText string
	}{
		{"先頭に行を足す", buffer.Edit{Text: "Z\n"}, "Z\nabcXY\ndefXY\nghiXY"},
		{"途中の行を消す", buffer.Edit{Range: buffer.Range{Start: buffer.Position{Line: 1}, End: buffer.Position{Line: 2}}}, "abcXY\nghiXY"},
		{"末尾の行を消す", buffer.Edit{Range: buffer.Range{Start: buffer.Position{Byte: 4}, End: buffer.Position{Line: 2, Byte: 4}}}, "abcXY"},
		{"位置の前に文字を足す", buffer.Edit{Range: buffer.Range{Start: buffer.Position{Line: 1}, End: buffer.Position{Line: 1}}, Text: "Q"}, "abcXY\nQdefXY\nghiXY"},
		{"途中に行を足す", buffer.Edit{Range: buffer.Range{Start: buffer.Position{Line: 1}, End: buffer.Position{Line: 1}}, Text: "M\n"}, "abcXY\nM\ndefXY\nghiXY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": text})
			h := start(t, Config{WorkDir: dir}, "a.txt")
			h.keys("VjjAX")
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
