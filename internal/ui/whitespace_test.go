package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// whitespaceText は、空白の記号の確かめに使うテキストである。2026-11-21 に同じテキストを nvim 0.12.5（--clean）と Vim 9.1（-Nu NONE）で
// tmux の画面に開き、:set list と 'listchars' を変えて capture-pane -e -p で読んだ画面を期待値にした（どの場合も nvim と Vim の画面は同じだった）。
//
//	1 行目: 行頭のタブ文字
//	2 行目: 全角の文字の後のタブ文字（タブ文字の幅が 6 列になる）
//	3 行目: 行頭、行の途中、行末の空白（20 文字。20 列の画面ではちょうど本文の幅で終わる）
//	4 行目: 空白だけの行
//	5 行目: 空の行
//	6 行目: ノーブレークスペース（U+00A0 と U+202F）と、ノーブレークスペースの前の空白（行末の空白でない）
//	7 行目: 行頭の空白とタブ文字、行の途中と行末のタブ文字と空白
//	8 行目: 40 文字の後のタブ文字（20 列の画面では見た目の行の頭から始まる）
//	9 行目: 19 文字の行、10 行目: 20 文字の行
func whitespaceText() *buffer.Buffer {
	return buffer.New(strings.Join([]string{
		"\tx",
		"あ\tx",
		"  lead  mid  trail  ",
		"    ",
		"",
		"a b c  ",
		" \t x\t ",
		"abcdefghijklmnopqrstuvwxyzabcdefghijklmn\topq",
		"0123456789012345678",
		"01234567890123456789",
	}, "\n") + "\n")
}

// lcs は、Vim の 'listchars' の項目（tab、space、lead、trail、nbsp、eol）を、空白の記号の対応にする。書かない項目は空にする
// （'listchars' を書き直したときに、書かない項目が無くなるのと同じ）。
func lcs(items map[string]string) Symbols {
	s := Symbols{}
	for _, n := range []string{SymbolWhitespaceTab, SymbolWhitespaceSpace, SymbolWhitespaceLead, SymbolWhitespaceTrail, SymbolWhitespaceNbsp, SymbolWhitespaceEOL} {
		s[n] = items[strings.TrimPrefix(n, "whitespace.")]
	}
	return s
}

// checkRows は、画面の上から len(want) 行の文字（右の空白を除く。diff_test.go の rowTexts）が want と同じかを確かめる。
func checkRows(t *testing.T, name string, s Screen, want []string) {
	t.Helper()
	got := rowTexts(s, len(want))
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: %d 行目 = %q, want %q", name, i+1, got[i], want[i])
		}
	}
}

// 空白の記号の描き方を、nvim 0.12.5 と Vim 9.1 の :set list の画面と比べる（期待値の取り方は whitespaceText の doc）。
//
//   - 既定の記号（Neovim の 'listchars' の既定の tab:> ,trail:-,nbsp:+）。空白だけの行は行末の空白として描く
//   - タブ文字の記号が空なら ^I の 2 列で描く（行の幅が変わる）。行末の記号は空の行にも描く
//   - 空白の記号の優先（lead と trail は space より優先し、空なら space を使う。空白だけの行には lead を使わない）
//   - タブ文字の記号の 3 文字の形（tab:<->）と 2 文字の形（tab:xy）
//
// req: FR-170
func TestWhitespaceSymbols(t *testing.T) {
	b := whitespaceText()
	render := func(w, h int, sym Symbols) Screen {
		r := newRenderer(w, h)
		r.Symbols = sym
		return r.Render(&Frame{Text: b, Whitespace: true})
	}
	nbspLine := "a b c  "
	tests := []struct {
		name string
		w    int
		sym  Symbols
		want []string
	}{
		{"既定の記号", 30, nil, []string{
			">       x", "あ>     x", "  lead  mid  trail--", "----", "", "a+b+c +", " >       x>     -",
			"abcdefghijklmnopqrstuvwxyzabcd", "0123456789012345678", "01234567890123456789",
		}},
		{"eol:$", 30, lcs(map[string]string{"eol": "$"}), []string{
			"^Ix$", "あ^Ix$", "  lead  mid  trail  $", "    $", "$", nbspLine + "$", " ^I x^I $",
			"abcdefghijklmnopqrstuvwxyzabcd", "0123456789012345678$", "01234567890123456789$",
		}},
		{"space:S,lead:L,trail:T,nbsp:N", 40, lcs(map[string]string{"space": "S", "lead": "L", "trail": "T", "nbsp": "N"}), []string{
			"^Ix", "あ^Ix", "LLleadSSmidSStrailTT", "TTTT", "", "aNbNcSN", "L^ILx^IT",
		}},
		{"lead:L", 40, lcs(map[string]string{"lead": "L"}), []string{
			"^Ix", "あ^Ix", "LLlead  mid  trail", "", "", nbspLine, "L^ILx^I",
		}},
		{"space:S", 40, lcs(map[string]string{"space": "S"}), []string{
			"^Ix", "あ^Ix", "SSleadSSmidSStrailSS", "SSSS", "", "a b cS ", "S^ISx^IS",
		}},
		{"space:S,trail:T", 40, lcs(map[string]string{"space": "S", "trail": "T"}), []string{
			"^Ix", "あ^Ix", "SSleadSSmidSStrailTT", "TTTT", "", "a b cS ", "S^ISx^IT",
		}},
		{"tab:<->", 40, lcs(map[string]string{"tab": "<->"}), []string{
			"<------>x", "あ<---->x", "  lead  mid  trail", "", "", nbspLine, " <-----> x<---->",
		}},
		{"tab:xy", 40, lcs(map[string]string{"tab": "xy"}), []string{
			"xyyyyyyyx", "あxyyyyyx", "  lead  mid  trail", "", "", nbspLine, " xyyyyyy xxyyyyy",
		}},
	}
	for _, tt := range tests {
		checkRows(t, tt.name, render(tt.w, 14, tt.sym), tt.want)
	}
}

// 行を折り返すときの空白の記号を、nvim 0.12.5 と Vim 9.1 の :set list wrap の 20 列の画面と比べる。
//
//   - オーナーの nvim の値（tab:→ ,lead:·,trail:·,eol:↲）。ちょうど本文の幅で終わる行（3 行目と 10 行目）は、行末の記号を次の見た目の行に描き、
//     その行を見た目の行の数に数える
//   - 見た目の行の右の端をまたぐタブ文字（tab:<->）は、記号を続けて次の見た目の行に描く
//
// req: FR-170
func TestWhitespaceWrap(t *testing.T) {
	r := newRenderer(20, 22)
	r.Symbols = lcs(map[string]string{"tab": "→ ", "lead": "·", "trail": "·", "eol": "↲"})
	s := r.Render(&Frame{Text: whitespaceText(), Whitespace: true, Wrap: true})
	checkRows(t, "オーナーの値", s, []string{
		"→       x↲", "あ→     x↲", "··lead  mid  trail··", "↲", "····↲", "↲", "a b c  ↲", "·→      ·x→     ·↲",
		"abcdefghijklmnopqrst", "uvwxyzabcdefghijklmn", "→       opq↲", "0123456789012345678↲", "01234567890123456789", "↲", "~",
	})
	if got := r.WrapLines(DefaultTabStop, true).Rows("01234567890123456789"); got != 2 {
		t.Errorf("行末の記号を描くときの 20 文字の行の見た目の行の数 = %d, want 2", got)
	}
	if got := r.WrapLines(DefaultTabStop, false).Rows("01234567890123456789"); got != 1 {
		t.Errorf("空白の記号を描かないときの 20 文字の行の見た目の行の数 = %d, want 1", got)
	}

	r = newRenderer(20, 12)
	r.Symbols = lcs(map[string]string{"tab": "<->"})
	b := buffer.New("\tabc\n0123456789\n0123456789012345\tX\nab\tc\n\t\n")
	checkRows(t, "tab:<->", r.Render(&Frame{Text: b, Whitespace: true, Wrap: true}), []string{
		"<------>abc", "0123456789", "0123456789012345<---", "--->X", "ab<---->c", "<------>",
	})
}

// 空白の記号を描くときは、タブ文字の上のカーソルを、ノーマルモードでもタブの最初の列に置き、望みの列とルーラーの列もその列にする。
// nvim 0.12.5 と Vim 9.1 で、"\tabc" の 1 文字目のカーソルが画面の 0 列目に出て、j で下の行の 1 列目へ移ることを確かめた。
// タブ文字を ^I で描くときは、タブ文字を 2 列として数える（nvim のルーラーは "あ\tx" のタブ文字の上で 2,4-3）。
//
// req: FR-170
func TestWhitespaceCursor(t *testing.T) {
	b := whitespaceText()
	r := newRenderer(30, 14)
	for _, ws := range []bool{false, true} {
		for _, mode := range []Mode{ModeNormal, ModeInsert} {
			s := r.Render(&Frame{Text: b, Mode: mode, Whitespace: ws})
			want := 0
			if !ws && mode == ModeNormal {
				want = 7
			}
			if s.Cursor.Col != want {
				t.Errorf("Whitespace=%v モード %v のタブ文字の上のカーソルの列 = %d, want %d", ws, mode, s.Cursor.Col, want)
			}
		}
	}

	r.Symbols = lcs(map[string]string{"eol": "$"})
	s := r.Render(&Frame{Text: b, Cursor: pos(1, 3), Whitespace: true})
	if s.Cursor.Col != 2 || !strings.Contains(s.Grid.RowText(r.Height-2), "2,4-3") {
		t.Errorf("^I の上のカーソル = 列 %d、ステータス行 %q, want 列 2 と 2,4-3", s.Cursor.Col, s.Grid.RowText(r.Height-2))
	}

	tests := []struct {
		cols       Columns
		line       string
		byteOff    int
		col, byteA int // Column の結果と、ByteAt(line, col+1) の結果
	}{
		{Columns{}, "\tabc", 0, 7, 1},
		{Columns{Whitespace: true}, "\tabc", 0, 0, 0},
		{Columns{Whitespace: true}, "\tabc", 1, 8, 2},
		{Columns{Whitespace: true, Symbols: lcs(nil)}, "ab\tc", 3, 4, 3},
		{Columns{Whitespace: true, Symbols: lcs(nil)}, "ab\tc", 2, 2, 2},
	}
	for _, tt := range tests {
		if got := tt.cols.Column(tt.line, tt.byteOff); got != tt.col {
			t.Errorf("%+v.Column(%q, %d) = %d, want %d", tt.cols, tt.line, tt.byteOff, got, tt.col)
		}
		if got := tt.cols.ByteAt(tt.line, tt.col+1); got != tt.byteA {
			t.Errorf("%+v.ByteAt(%q, %d) = %d, want %d", tt.cols, tt.line, tt.col+1, got, tt.byteA)
		}
	}
}

// 空白の記号の色は、^A などの表記と同じく下地の色で、検索の一致と選択の色をその上に重ねる（layers.go）。
// 検索の一致の文字の色は、記号の色より上に出る（Vim 9.1 と同じ。nvim 0.12.5 は記号の色を一致の色の上に出す。docs/vim-differences.md）。
// 選択は背景の色だけを持つので、記号の文字の色の上に選択の背景の色が出る（nvim 0.12.5 と同じ）。行末の記号は nontext の色である。
//
// req: FR-170
func TestWhitespaceLayers(t *testing.T) {
	ws, nt := Attr{Fg: Indexed(1)}, Attr{Fg: Indexed(2)}
	search, visual := Attr{Fg: Indexed(0), Bg: Indexed(3)}, Attr{Bg: Indexed(4)}
	r := &Renderer{Width: 40, Height: 9, Theme: Theme{StyleWhitespace: ws, StyleNonText: nt, StyleSearch: search, StyleVisual: visual}}
	r.Symbols = lcs(map[string]string{"tab": ">-", "lead": "L", "trail": "T", "eol": "$"})
	b := whitespaceText()

	s := r.Render(&Frame{Text: b, Whitespace: true, Search: regexp.MustCompile(` +$|\t`)})
	at := func(row, col int) Attr { return s.Grid.At(row, col).Attr }
	checks := []struct {
		name     string
		row, col int
		want     Attr
	}{
		{"一致したタブ文字", 0, 0, search},
		{"一致した行末の空白", 2, 18, search},
		{"一致しない行頭の空白", 2, 0, ws},
		{"行末の記号", 2, 20, nt},
		{"ノーブレークスペースの前の空白（記号を描かない）", 5, 5, Attr{}},
	}
	for _, c := range checks {
		if got := at(c.row, c.col); got != c.want {
			t.Errorf("%s の色 = %+v, want %+v", c.name, got, c.want)
		}
	}

	sel := Selection{Active: true, Start: pos(2, 0), End: pos(4, 0), Linewise: true}
	s = r.Render(&Frame{Text: b, Mode: ModeVisualLine, Cursor: pos(4, 0), Selection: sel, Whitespace: true})
	checks = []struct {
		name     string
		row, col int
		want     Attr
	}{
		{"選択した行頭の空白", 2, 0, ws.Over(visual)},
		{"選択した行の行末の記号", 2, 20, nt.Over(visual)},
		{"選択した空白だけの行", 3, 1, ws.Over(visual)},
	}
	for _, c := range checks {
		if got := s.Grid.At(c.row, c.col).Attr; got != c.want {
			t.Errorf("%s の色 = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// 設定が偽（既定）なら、Frame.Whitespace を持つ前と同じに描く。:set list! で切り替えても、描くたびに Frame.Whitespace を読む。
//
// req: FR-170
func TestWhitespaceToggle(t *testing.T) {
	b := whitespaceText()
	r := newRenderer(30, 14)
	off := []string{"        x", "あ      x", "  lead  mid  trail", "", "", "a b c  ", "         x"}
	checkRows(t, "設定が偽", r.Render(&Frame{Text: b}), off)
	checkRows(t, "設定が真", r.Render(&Frame{Text: b, Whitespace: true}), []string{">       x"})
	checkRows(t, "偽に戻す", r.Render(&Frame{Text: b}), off)
}
