package editor

import (
	"slices"
	"strings"
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

// マクロの記録は送らず、再生の中で先頭が何度変わっても最後の先頭だけを送る。
// 大文字での追記（"A）は、追記した後の全体を送る。:g/pat/d は 1 件として、最後に消した行を送る。
//
// req: FR-56
func TestClipboardMacroAppendGlobal(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "x1\nab\ncd\nx2\nx3\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("qbylq")
	h.waitClipboard([]string{"x"})

	h.keys("qayyjylq")
	want := []string{"x", "x1\n", "a"}
	h.waitClipboard(want)

	// 再生は yy と yl で先頭を 2 回変えるが、1 回の結果なので最後の yl の "c" だけを送る。
	h.keys("@a")
	want = append(want, "c")
	h.waitClipboard(want)

	h.keys(`gg"cyl"Cyl`)
	want = append(want, "x", "xx")
	h.waitClipboard(want)

	h.keys(":g/x/d<CR>")
	want = append(want, "x3\n")
	h.waitClipboard(want)
	if got := h.text(); got != "ab\ncd" {
		t.Errorf(":g/x/d の後の text = %q", got)
	}
}

// base64 にした大きさが MaxClipboardEncoded ちょうどのテキストは送り、1 バイトでも超えたら送らずにメッセージの行に出す。
//
// req: FR-56
func TestClipboardSizeLimit(t *testing.T) {
	// 行単位のテキストは改行を足して送るので、元のテキストは 750,000 バイト（base64 で 1,000,000 バイト）になる。
	fit := strings.Repeat("a", MaxClipboardEncoded/4*3-1)
	over := strings.Repeat("b", MaxClipboardEncoded/4*3)
	dir := writeFiles(t, map[string]string{"a.txt": fit + "\n" + over + "\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("yy")
	h.waitClipboard([]string{fit + "\n"})

	h.keys("jyy")
	h.settle()
	if got := h.term.clipboard(); len(got) != 1 {
		t.Errorf("上限を超えたテキストを送った: %d 件", len(got))
	}
	if msg, want := h.message(), "クリップボードへ送りませんでした: 大きすぎます（750001 バイト）"; msg != want {
		t.Errorf("message = %q, want %q", msg, want)
	}
	// 送らなかった後も、次に先頭が変われば送る。
	h.keys("yl")
	h.waitClipboard([]string{fit + "\n", "b"})
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
