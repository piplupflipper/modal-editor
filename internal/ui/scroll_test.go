package ui

import "testing"

// Botline は、直前の描画で最後まで描いた行の次の行を返す。折り返すときは見た目の行をすべて描いた行だけを数え、
// 表示領域より高い最初の行しか描けないときは、最初の行の次の行にする（PR #338 のレビューの推奨 1）。
//
// req: FR-174
func TestRendererBotline(t *testing.T) {
	b := wrapText()
	r := newRenderer(41, 9) // 表示領域は 7 行
	r.Render(&Frame{Text: b, Cursor: pos(0, 0)})
	if got := r.Botline(); got != 7 {
		t.Errorf("折り返さない: Botline = %d, want 7", got)
	}
	// 1 行目から 1、3、2 行の高さの 3 行で 6 行を使い、2 行の高さの 4 行目は入らない。
	r = newRenderer(41, 9)
	r.Render(&Frame{Text: b, Cursor: pos(0, 0), Wrap: true})
	if got := r.Botline(); got != 3 {
		t.Errorf("折り返す: Botline = %d, want 3", got)
	}
	// 10 行の高さの 7 行目にカーソルを置くと、7 行目だけを途中まで描く。
	r.Render(&Frame{Text: b, Cursor: pos(6, 0), Wrap: true})
	if top, got := r.Top(), r.Botline(); top != 6 || got != 7 {
		t.Errorf("表示領域より高い行: Top = %d、Botline = %d, want 6、7", top, got)
	}
}
