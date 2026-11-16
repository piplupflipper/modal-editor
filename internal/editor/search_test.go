package editor

import (
	"strings"
	"testing"
)

// / と ? のパターンの入力は、コマンドラインの行に "/" か "?" を付けて出し、Ex コマンドの入力と同じ行の編集を使う。
// 検索の後は、検索したパターンをメッセージの行に出し、バッファの端で折り返したら警告を出す。
//
// req: FR-01
func TestSearchCommandLine(t *testing.T) {
	h := start(t, Config{})
	h.keys("ione<CR>foo<CR>two<Esc>gg")
	h.settle()
	h.keys("?fx<BS>o")
	h.screenHas("後ろ向きの検索の入力", "?fo")
	h.keys("<CR>")
	h.screenHas("折り返しの警告", "先頭まで検索したので末尾から続けました")
	h.keys("/")
	h.eventually("前向きの検索の入力", func() bool { return strings.HasSuffix(strings.TrimRight(h.term.screen(), " \n"), "/") })
	// ? の後の N は前向きに検索するので、"/" を付けて出す。
	h.keys("<Esc>ggN")
	h.screenHas("N の後のパターン", "/fo")
}
