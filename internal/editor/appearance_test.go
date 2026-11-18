package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/ui"
)

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
