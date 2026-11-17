package editor

import (
	"slices"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command"
)

// waitClipboard は、偽の端末へ送られたクリップボードのテキストが want になるまで待つ。
func (h *harness) waitClipboard(want []string) {
	h.t.Helper()
	h.settle()
	h.eventually("クリップボードへの送り出し", func() bool { return slices.Equal(h.term.clipboard(), want) })
}

// 履歴の先頭が変わるたびに、その内容をホストのクリップボードへ送る。行単位のテキストは最後に改行を付ける。
// 貼り付けと _ への書き込みは履歴の先頭を変えないので送らない。同じテキストをもう一度ヤンクしても、先頭に積んだので送る。
// 1 つの Ex コマンドの行の中の削除（:1d|1d）は、最後の先頭だけを送る。
//
// req: FR-56
func TestClipboardFollowsHistoryHead(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one two\nthree\nfour\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("yy")
	want := []string{"one two\n"}
	h.waitClipboard(want)

	h.keys("yiw")
	want = append(want, "one")
	h.waitClipboard(want)

	h.keys(`p"_yyP"_dd`)
	h.waitClipboard(want)

	h.keys("uuugg0yiw")
	want = append(want, "one")
	h.waitClipboard(want)
	if got := h.text(); got != "one two\nthree\nfour" {
		t.Errorf("アンドゥの後の text = %q", got)
	}

	h.keys(`"ayj`)
	want = append(want, "one two\nthree\n")
	h.waitClipboard(want)

	h.keys(":1d|1d<CR>")
	want = append(want, "three\n")
	h.waitClipboard(want)
	if got := h.text(); got != "four" {
		t.Errorf(":1d|1d の後の text = %q", got)
	}
}

// 上限（maxEntryBytes）を超えて上限を超えた無名のレジスターに置いたテキストは送らない。
// 名前付きのレジスターに書いたときも、履歴に積まなかったら送らない。
//
// req: FR-56
func TestClipboardSkipsOversize(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abcdef\nxy\n"})
	h := start(t, Config{WorkDir: dir, Registers: command.RegisterConfig{MaxEntryBytes: 4}}, "a.txt")
	h.keys(`yy"ayyj`)
	h.settle()
	h.keys("yiw")
	h.waitClipboard([]string{"xy"})
	h.keys("kyy")
	h.settle()
	if got := h.term.clipboard(); !slices.Equal(got, []string{"xy"}) {
		t.Errorf("上限を超えたヤンクの後の送り出し = %q, want [xy]", got)
	}
}

// バッファを切り替えても、レジスターは引き継ぐので送り直さない。切り替えた先のヤンクは送る。
//
// req: FR-56
func TestClipboardAcrossBuffers(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "A\n", "b.txt": "B\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("yy")
	h.waitClipboard([]string{"A\n"})
	h.keys(":e b.txt<CR>")
	h.keys(":e a.txt<CR>")
	h.keys(":e b.txt<CR>yl")
	h.waitClipboard([]string{"A\n", "B"})
}

// registers.clipboard が "off"（Config.NoClipboard）なら送らない。
//
// req: FR-56
func TestClipboardOff(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "A\n"})
	h := start(t, Config{WorkDir: dir, NoClipboard: true}, "a.txt")
	h.keys("yyp")
	h.settle()
	if got := h.text(); got != "A\nA" {
		t.Fatalf("text = %q", got)
	}
	if got := h.term.clipboard(); len(got) != 0 {
		t.Errorf("送り出し = %q, want なし", got)
	}
}
