package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 設定の色（Config.Colors）は、描画に渡り、名前ごとに既定の属性を置き換える。
//
// req: FR-91
func TestColorsReachRenderer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	want := ui.Attr{Fg: ui.RGB(0xc6, 0xc8, 0xd1), Bg: ui.RGB(0x0f, 0x11, 0x17)}
	h := start(t, Config{WorkDir: dir, Colors: map[string]ui.Attr{ui.StyleStatusLine: want}}, "a.txt")
	h.eventually("ステータス行の色", func() bool {
		h.term.mu.Lock()
		defer h.term.mu.Unlock()
		g := h.term.last.Grid
		// fakeTerm は 6 行なので、ステータス行は 5 行目（0 から数えて 4）。
		return g != nil && g.RowText(4) != "" && g.At(4, 1).Text == "N" && g.At(4, 1).Attr == want
	})
}

// 編集を禁止したバッファは、ステータス行に禁止の印を出し、:readonly で解くと消える。
// 印の文字（Config.Symbols）とステータス行の並び（Config.StatusLine）は設定で変えられる。
//
// req: FR-87, FR-92, FR-93
func TestStatusLineReadOnlyMark(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, ReadOnlyArgs: true}, "a.txt")
	h.eventually("[RO] を出す", func() bool { return strings.Contains(h.term.screen(), "a.txt [RO]") })
	h.keys(":readonly<CR>")
	h.eventually(":readonly で消す", func() bool { return !strings.Contains(h.term.screen(), "[RO]") })

	h2 := start(t, Config{
		WorkDir: dir, ReadOnlyArgs: true,
		Symbols:    ui.Symbols{ui.SymbolReadOnly: "禁止"},
		StatusLine: &ui.StatusLine{Left: []string{ui.StatusPartReadOnly, ui.StatusPartName}},
	}, "a.txt")
	h2.eventually("設定の並びと記号", func() bool {
		lines := strings.Split(h2.term.screen(), "\n")
		return len(lines) >= 2 && strings.HasPrefix(lines[len(lines)-2], " 禁止  ") && strings.Contains(lines[len(lines)-2], "a.txt ")
	})
	if strings.Contains(h2.term.screen(), "NORMAL") {
		t.Errorf("並べなかったモードを出した:\n%s", h2.term.screen())
	}
}
