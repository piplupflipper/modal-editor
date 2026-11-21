package ui

import (
	"fmt"
	"strings"
	"testing"
)

// completionTheme は、補完の候補の一覧の試験で使う色である。
var completionTheme = Theme{
	StylePopup: {Bg: Indexed(1)}, StylePopupSelected: {Bg: Indexed(2)}, StylePopupMatch: {Bold: true},
	StylePopupScrollbar: {Bg: Indexed(3)}, StylePopupThumb: {Bg: Indexed(4)},
}

// renderCompletion は、width 列 height 行の画面に、コマンドライン line と一覧 m を描く。r が nil なら新しい Renderer で描く。
func renderCompletion(r *Renderer, width, height int, line string, m *CompletionMenu) Screen {
	if r == nil {
		r = &Renderer{Width: width, Height: height, Theme: completionTheme}
	}
	return r.Render(&Frame{Mode: ModeCommandLine, CommandLine: line, Completion: m})
}

// 一覧は、コマンドラインの行の真上に、項目を補う語の列から並べ、その 1 列左に空白を置く。欄の幅は項目の幅と 15 の大きい方である。
// 選んでいる項目は popup.selected、ほかは popup の色で描き、打った語に当たる先頭は popup.match を重ねる（Neovim 0.12.5 と同じ）。
//
// req: FR-197
func TestRenderCompletion(t *testing.T) {
	m := &CompletionMenu{ID: 1, Items: []string{"Alpha.txt", "alpine.md"}, Selected: 0, Start: 2, Match: "al"}
	s := renderCompletion(nil, 40, 10, "e al", m)
	got := rowTexts(s, 10)
	if got[7][2:] != " Alpha.txt" || !strings.HasPrefix(got[8][2:], " alpine.md      ") || got[9] != ":e al" {
		t.Errorf("画面 = %q", got)
	}
	sel, normal := completionTheme[StylePopupSelected], completionTheme[StylePopup]
	for _, tt := range []struct {
		row, col int
		want     Attr
	}{
		{7, 2, sel},
		{7, 3, sel.Over(Attr{Bold: true})},
		{7, 4, sel.Over(Attr{Bold: true})},
		{7, 5, sel},
		{7, 17, sel},
		{7, 18, Attr{}},
		{8, 2, normal},
		{8, 3, normal.Over(Attr{Bold: true})},
		{8, 17, normal},
	} {
		if a := s.Grid.At(tt.row, tt.col).Attr; a != tt.want {
			t.Errorf("(%d, %d) の色 = %+v, want %+v", tt.row, tt.col, a, tt.want)
		}
	}
	// 項目が 15 列より広ければ、項目の幅に 1 列を足した幅にする。
	m = &CompletionMenu{ID: 2, Items: []string{"search.clearHighlight", "set"}, Selected: -1, Start: 0}
	s = renderCompletion(nil, 40, 10, "s", m)
	if got := s.Grid.RowText(7)[:24]; got != " search.clearHighlight  " {
		t.Errorf("広い項目の行 = %q", got)
	}
	if a, b := s.Grid.At(7, 22).Attr, s.Grid.At(7, 23).Attr; a != normal || b == normal {
		t.Errorf("項目の後ろの 1 列の色 = %+v、その右の色 = %+v", a, b)
	}
}

// 項目が行の数より多ければ、右の端の列にスクロールバーを描き、選んでいる項目の上と下に 2 行（行の数の半分と 3 の小さい方）を残して送る。
// completionMenu.height で行の数を限る。
//
// req: FR-197
func TestRenderCompletionScroll(t *testing.T) {
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf("i%02d", i)
	}
	r := &Renderer{Width: 40, Height: 10, Theme: completionTheme}
	m := &CompletionMenu{ID: 1, Items: items, Selected: 0, Start: 2, MaxHeight: 5}
	s := renderCompletion(r, 40, 10, "e i", m)
	got := rowTexts(s, 10)
	for i, want := range []string{"i00", "i01", "i02", "i03", "i04"} {
		if row := got[4+i]; len(row) < 6 || row[3:6] != want {
			t.Errorf("%d 行目 = %q, want %s", 4+i, row, want)
		}
	}
	if !strings.HasPrefix(got[3], "~") || r.CompletionRows() != 5 {
		t.Errorf("一覧の上の行 = %q、行の数 = %d", got[3], r.CompletionRows())
	}
	thumb, bar := completionTheme[StylePopupThumb], completionTheme[StylePopupScrollbar]
	if a, b := s.Grid.At(4, 18).Attr, s.Grid.At(5, 18).Attr; a != thumb || b != bar {
		t.Errorf("スクロールバー = %+v, %+v", a, b)
	}
	m.Selected = 10
	s = renderCompletion(r, 40, 10, "e i10", m)
	got = rowTexts(s, 10)
	if got[4][3:6] != "i08" || got[6][3:6] != "i10" || got[8][3:6] != "i12" {
		t.Errorf("送った後 = %q", got[4:9])
	}
	if a := s.Grid.At(5, 18).Attr; a != thumb {
		t.Errorf("送った後のつまみ = %+v", a)
	}
	// ID が替われば先頭から出す。
	m = &CompletionMenu{ID: 2, Items: items, Selected: -1, Start: 2, MaxHeight: 5}
	if got := rowTexts(renderCompletion(r, 40, 10, "e i", m), 10); got[4][3:6] != "i00" {
		t.Errorf("新しい一覧 = %q", got[4:9])
	}
}

// 欄が右の端に収まらなければ、15 列より広く取れる限り右の端までの幅にして、収まらない項目の右の端に ">" を描く。
// 15 列より狭くなるなら、16 列の幅にして右の端に寄せる（Neovim 0.12.5 と同じ）。
//
// req: FR-197
func TestRenderCompletionNarrow(t *testing.T) {
	m := &CompletionMenu{ID: 1, Items: []string{"abcdefghijklmnopqrstuvwxyz"}, Selected: -1, Start: 2}
	s := renderCompletion(nil, 20, 6, "e x", m)
	if got := s.Grid.RowText(4); got[2:] != " abcdefghijklmnop>" {
		t.Errorf("切った項目 = %q", got)
	}
	line := "e " + strings.Repeat("x", 22)
	m = &CompletionMenu{ID: 1, Items: []string{"abc"}, Selected: 0, Start: len(line)}
	s = renderCompletion(nil, 30, 6, line, m)
	if got := s.Grid.RowText(4); got[13:] != " abc"+strings.Repeat(" ", 13) {
		t.Errorf("右に寄せた一覧 = %q", got)
	}
	// 1 行しか取れないのに 2 項目以上なら出さない。
	m = &CompletionMenu{ID: 1, Items: []string{"a", "b"}, Selected: 0, Start: 0}
	if got := renderCompletion(nil, 20, 2, "", m).Grid.RowText(0); strings.Contains(got, "a") {
		t.Errorf("1 行の画面 = %q", got)
	}
}

// 一番上の行の項目は、Neovim 0.12.5 の pum_set_selected と同じく、選んでいる項目の上と下に行を残すように決める。
//
// req: FR-197
func TestScrollCompletion(t *testing.T) {
	// 1 つずつ下へ送ると、下に 3 行を残す。1 つずつ上へ送ると、上に 3 行を残す。
	first := 0
	for sel := range 20 {
		first = scrollCompletion(first, sel, 12, 20)
		if want := max(0, min(sel-8, 8)); first != want {
			t.Errorf("下へ送って %d を選んだときの一番上 = %d, want %d", sel, first, want)
		}
	}
	for sel := 18; sel >= 0; sel-- {
		first = scrollCompletion(first, sel, 12, 20)
		if want := min(8, max(sel-3, 0)); first != want {
			t.Errorf("上へ送って %d を選んだときの一番上 = %d, want %d", sel, first, want)
		}
	}
	// 跳んだとき（<PageDown>、打った語から最後の候補へ戻るとき）は、Neovim と同じく 1 画面ぶん送る。選んでいなければ動かさない。
	for _, tt := range []struct{ first, sel, want int }{
		{2, 13, 8}, {0, 19, 8}, {8, -1, 8}, {8, 0, 0},
	} {
		if got := scrollCompletion(tt.first, tt.sel, 12, 20); got != tt.want {
			t.Errorf("一番上 %d から %d を選んだときの一番上 = %d, want %d", tt.first, tt.sel, got, tt.want)
		}
	}
}
