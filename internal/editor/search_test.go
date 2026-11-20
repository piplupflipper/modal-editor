package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// searchMarks は、偽の端末 term に最後に描いた画面の row 行目の、属性が a のセルを "^"、それ以外を " " にした文字列を、右の空白を除いて返す。
func searchMarks(term *fakeTerm, row int, a ui.Attr) string {
	term.mu.Lock()
	defer term.mu.Unlock()
	g := term.last.Grid
	if g == nil {
		return ""
	}
	var b strings.Builder
	for c := range 20 {
		if g.At(row, c).Attr == a {
			b.WriteByte('^')
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// 設定ファイルの search.highlight（Config.Options）で、直前の検索のパターンの一致に色の名前 search の色を付ける。
// 描画は描くたびに設定の今の値と直前の検索を読むので、/、:nohlsearch、n、:set hlsearch!、:set hlsearch、* は次の描画から効く。
// :nohlsearch の後は、設定の値は変わらず、:set hlsearch を打ち直すか検索し直すと色が戻る。
// * はカーソルを動かさず、ジャンプリストにも記録しない。
//
// req: FR-137, FR-138, FR-139
func TestSearchHighlight(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "foo bar\nbar foo\nbaz\n"})
	hl := ui.Attr{Bg: ui.RGB(1, 2, 3)}
	h := start(t, Config{
		WorkDir: dir, Options: map[string]option.Value{option.SearchHighlight: option.BoolValue(true)},
		Colors: map[string]ui.Attr{ui.StyleSearch: hl},
	}, "a.txt")
	// 画面の最初の行はタブの行なので、バッファの最初の行は 1 行目に描く。
	rows := func() string { return searchMarks(h.term, 1, hl) + "|" + searchMarks(h.term, 2, hl) }
	h.settle()
	h.eventually("検索する前は色が無い", func() bool { return h.screenRow(1) == "foo bar" && rows() == "|" })

	steps := []struct {
		keys string
		want string
	}{
		{"/bar<CR>", "    ^^^|^^^"},
		{":nohlsearch<CR>", "|"},
		{"n", "    ^^^|^^^"},
		{":noh<CR>", "|"},
		{":set number<CR>:set nonumber<CR>", "|"},
		{":set hlsearch<CR>", "    ^^^|^^^"},
		{":set hlsearch!<CR>", "|"},
		{":set hlsearch!<CR>", "    ^^^|^^^"},
		{":search.clearHighlight<CR>gg0", "|"},
	}
	for _, s := range steps {
		h.keys(s.keys)
		h.settle()
		h.eventually(s.keys, func() bool { return rows() == s.want })
	}
	var before string
	h.e.do(func() {
		if got := h.e.options.Get(option.SearchHighlight).Bool(); !got {
			t.Errorf("search.highlight = %v（:nohlsearch は設定の値を変えない）", got)
		}
		before, _, _ = h.e.jumpList()
	})
	h.keys("*")
	h.settle()
	h.eventually("*", func() bool { return rows() == "^^^|    ^^^" })
	h.e.do(func() {
		if list, _, cur := h.e.jumpList(); list != before || cur != "a.txt:1.0" {
			t.Errorf("* の後の jumps = [%s] cur=%s, want [%s] cur=a.txt:1.0", list, cur, before)
		}
	})
	h.keys("n")
	h.settle()
	h.e.do(func() {
		if list, _, cur := h.e.jumpList(); cur != "a.txt:2.4" || list == before {
			t.Errorf("n の後の jumps = [%s] cur=%s, want a.txt:2.4 と記録", list, cur)
		}
	})
}

// 直前の検索は client の間で共有するので、ある client で検索すると、別の client の画面にも同じパターンの色が付く。
// :nohlsearch で消した状態も共有する。
//
// req: FR-137, FR-138
func TestSearchHighlightAcrossClients(t *testing.T) {
	hl := ui.Attr{Bg: ui.RGB(1, 2, 3)}
	h := start(t, Config{
		Options: map[string]option.Value{option.SearchHighlight: option.BoolValue(true)},
		Colors:  map[string]ui.Attr{ui.StyleSearch: hl},
	})
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys("ix foo<Esc>")
	h.keys("ifoo<Esc>/foo<CR>")
	h.settle()
	h.eventually("別の client の画面の色", func() bool { return searchMarks(peer.term.fakeTerm, 1, hl) == "  ^^^" })
	h.eventually("検索した client の画面の色", func() bool { return searchMarks(h.term, 1, hl) == "^^^" })
	peer.keys(":noh<CR>")
	h.eventually("別の client の :noh", func() bool {
		return searchMarks(peer.term.fakeTerm, 1, hl) == "" && searchMarks(h.term, 1, hl) == ""
	})
}

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
