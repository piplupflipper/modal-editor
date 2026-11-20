package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// プレビューは、窓の幅（Fill でなければ画面の幅の WindowWidthPercent %）が MinWindowWidth を超えるときだけ出し、
// 窓の幅の WidthPercent % をプレビューに使う。既定の値（100、60、90）では、200 列の画面で 180 列の窓の 108 列、
// 90 列の画面では出さない。端末多重化ソフトのポップアップ（Fill）では、その全体の幅で決める。
//
// req: FR-149
func TestPreviewLayout(t *testing.T) {
	rule := PreviewRule{MinWindowWidth: 100, WidthPercent: 60, WindowWidthPercent: 90}
	for _, tt := range []struct {
		width           int
		fill            bool
		window, preview int
		ok              bool
	}{
		{200, false, 180, 108, true},
		{90, false, 0, 0, false},
		{112, false, 0, 0, false}, // 112 の 90% は 100 で、境目を超えない
		{113, false, 101, 60, true},
		{160, true, 160, 96, true}, // tmux の 200 列の端末の 80% のポップアップ
		{72, true, 0, 0, false},
		{100, true, 0, 0, false},
	} {
		w, p, ok := rule.Layout(tt.width, tt.fill)
		if w != tt.window || p != tt.preview || ok != tt.ok {
			t.Errorf("Layout(%d, %v) = %d, %d, %v, want %d, %d, %v", tt.width, tt.fill, w, p, ok, tt.window, tt.preview, tt.ok)
		}
	}
	// 一覧とプレビューに分けられないほど狭ければ出さない。
	if _, _, ok := (PreviewRule{WidthPercent: 99, WindowWidthPercent: 100}).Layout(10, false); ok {
		t.Error("a 10-column window was split")
	}
	// Split は境目と比べない（出すと決めた窓を分ける）。tmux の 113 列の端末の 90% のポップアップは、枠線の内側が 99 列である。
	if w, p, ok := rule.Split(99, true); w != 99 || p != 59 || !ok {
		t.Errorf("Split(99, true) = %d, %d, %v", w, p, ok)
	}
}

// previewText は、n 行の "row i" のテキストである。
func previewText(n int) *buffer.Buffer {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("row %d", i)
	}
	return buffer.New(strings.Join(lines, "\n"))
}

// プレビューを出す窓は、画面の幅の規則の幅に広げて中央に置き、左に一覧、縦の線、右にプレビューを描く。プレビューの最初の行は見出しで、
// 色を付ける行（Line）を真ん中に置き、その行の全体に StylePreviewLine の色を付ける。一覧の最後の項目より下の行は空ける。
// 境目を超えなければ、プレビューを描かずに今までの幅で描く。中身が無ければ理由を書く。
// プレビューにフォーカスがあれば、端末のカーソルをプレビューの中に置く。
//
// req: FR-149
func TestDrawPreview(t *testing.T) {
	theme := testTheme()
	lineAttr := Attr{Bg: RGB(1, 2, 3)}
	theme[StylePreviewLine] = lineAttr
	rule := PreviewRule{MinWindowWidth: 40, WidthPercent: 50, WindowWidthPercent: 100}
	list := func(pv *Preview) *Frame {
		return &Frame{Text: buffer.New("x"), Float: &Float{
			ID: 1, View: &Frame{Text: buffer.New("one\ntwo"), Cursor: pos(1, 0)}, Center: true, Focused: true, CursorLine: true,
			Preview: pv, PreviewRule: rule,
		}}
	}
	r := &Renderer{Width: 60, Height: 14, Theme: theme}
	s := r.Render(list(&Preview{ID: 7, Title: "a.go:21", View: &Frame{Text: previewText(40), Cursor: pos(20, 0)}, Line: 20}))
	rows := strings.Split(s.Grid.String(), "\n")
	// 窓は 60 列の全体で、プレビューは 30 列、一覧は 29 列、縦の線は 29 列目である。表示領域は 12 行で、プレビューの中身は 11 行。
	sub := r.PreviewRenderer()
	if sub == nil || sub.Top() != 20-(11-1)/2 {
		t.Fatalf("preview scroll top = %v, want %d\n%s", sub, 20-(11-1)/2, s.Grid.String())
	}
	titleRow, lineRow := -1, -1
	for i, row := range rows {
		if strings.Contains(row, "a.go:21") {
			titleRow = i
		}
		if strings.Contains(row, "row 20") {
			lineRow = i
		}
	}
	if titleRow < 0 || lineRow != titleRow+1+5 {
		t.Fatalf("title row %d, line row %d (want the middle of 11 rows)\n%s", titleRow, lineRow, s.Grid.String())
	}
	if c := s.Grid.At(titleRow, 29); c.Text != previewSeparator {
		t.Errorf("separator = %q\n%s", c.Text, s.Grid.String())
	}
	if !strings.HasPrefix(rows[titleRow][:29], " one") || strings.Contains(rows[titleRow+2], "~") {
		t.Errorf("list part:\n%s", s.Grid.String())
	}
	for _, col := range []int{31, 58} {
		if a := s.Grid.At(lineRow, col).Attr; a.Bg != lineAttr.Bg {
			t.Errorf("line row col %d attr = %+v", col, a)
		}
	}
	if a := s.Grid.At(lineRow+1, 58).Attr; a.Bg == lineAttr.Bg {
		t.Error("the next row is colored too")
	}
	if !s.Cursor.Visible || s.Cursor.Col >= 29 {
		t.Errorf("cursor %+v, want in the list", s.Cursor)
	}

	// フォーカスがプレビューにあれば、端末のカーソルをプレビューの中に置く。
	s = r.Render(list(&Preview{ID: 7, Title: "a.go:21", View: &Frame{Text: previewText(40), Cursor: pos(21, 2)}, Line: 20, Focused: true}))
	if !s.Cursor.Visible || s.Cursor.Col != 30+1+2 || s.Cursor.Row != lineRow+1 {
		t.Errorf("focused preview cursor = %+v, want row %d col %d", s.Cursor, lineRow+1, 33)
	}

	// 中身が無ければ、見出しの下に理由を書く。
	s = r.Render(list(&Preview{ID: 8, Title: "big", Note: "大きなファイルです", Line: -1}))
	if g := s.Grid.String(); !strings.Contains(g, "大きなファイルです") {
		t.Errorf("note:\n%s", g)
	}

	// 出すかは渡す側が決めるので、境目を超えない窓でも、渡されたプレビューは描く（判定を 2 か所で行わない。PR #298 のレビューの必須 1）。
	rule.MinWindowWidth = 60
	s = r.Render(list(&Preview{ID: 9, Title: "a.go:21", View: &Frame{Text: previewText(40), Cursor: pos(20, 0)}, Line: 20}))
	if g := s.Grid.String(); !strings.Contains(g, "row 20") || !strings.Contains(g, previewSeparator) {
		t.Errorf("a given preview was not drawn:\n%s", g)
	}
	// 渡されなければ描かない。
	s = r.Render(list(nil))
	if g := s.Grid.String(); strings.Contains(g, previewSeparator) || r.PreviewRenderer() != nil {
		t.Errorf("preview without Float.Preview:\n%s", g)
	}
}

// ファインダー（Float.List）と、部品の client の画面の全体の窓（Fill）にも、同じ規則でプレビューを描く。
//
// req: FR-149
func TestDrawPreviewFinderAndFill(t *testing.T) {
	rule := PreviewRule{MinWindowWidth: 40, WidthPercent: 50, WindowWidthPercent: 100}
	pv := &Preview{ID: 1, Title: "t", View: &Frame{Text: previewText(3)}, Line: -1}
	r := &Renderer{Width: 60, Height: 10, Theme: testTheme()}
	s := r.Render(&Frame{Text: buffer.New("x"), Float: &Float{
		ID: 1, View: &Frame{Text: buffer.New("q"), Mode: ModeInsert}, List: &Frame{Text: buffer.New("a\nb")}, ListCount: 2,
		Center: true, Focused: true, Preview: pv, PreviewRule: rule,
	}})
	rows := strings.Split(s.Grid.String(), "\n")
	if !strings.Contains(rows[0], "> q") || s.Grid.At(0, 29).Text != previewSeparator || !strings.Contains(rows[1], "row 0") {
		t.Errorf("finder with a preview:\n%s", s.Grid.String())
	}
	if s.Cursor.Col >= 29 {
		t.Errorf("finder cursor %+v, want in the input line", s.Cursor)
	}
	r = &Renderer{Width: 60, Height: 10, Theme: testTheme()}
	s = r.Render(&Frame{Float: &Float{
		ID: 2, View: &Frame{Text: buffer.New("one")}, Fill: true, CursorLine: true, Preview: pv, PreviewRule: rule,
	}})
	if s.Grid.At(0, 29).Text != previewSeparator || !strings.Contains(strings.Split(s.Grid.String(), "\n")[1], "row 0") {
		t.Errorf("fill with a preview:\n%s", s.Grid.String())
	}
}
