package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// scrollbarColumn は、画面の表示領域の rows 行の、右端の列の文字を上から並べた文字列を返す。
func scrollbarColumn(s Screen, rows int) string {
	var b strings.Builder
	x := s.Grid.Width() - 1
	for i := range rows {
		b.WriteString(s.Grid.At(i, x).Text)
	}
	return b.String()
}

// thumbRows は、表示領域の rows 行のうち、右端の列の属性につまみの背景の色 bg が付いた行を、"#" と "." で並べた文字列を返す。
func thumbRows(s Screen, rows int, bg Color) string {
	var b strings.Builder
	x := s.Grid.Width() - 1
	for i := range rows {
		if s.Grid.At(i, x).Attr.Bg == bg {
			b.WriteByte('#')
		} else {
			b.WriteByte('.')
		}
	}
	return b.String()
}

// スクロールバーは、表示領域に収まらないときだけ、右端の列に、見えている行の範囲を本文の行で割り付けたつまみを描く。
// つまみのセルは下の文字を残し、背景の色だけを替える。全角文字の右半分に当たるときは、その文字を空白にする。
// 本文が 1 列しかない画面と、Frame.Scrollbar が nil のときは描かない。行を折り返して画面より高い 1 行も、収まらないとして描く。
//
// req: FR-186
func TestRenderScrollbarThumb(t *testing.T) {
	theme := Theme{StyleScrollbar: {Bg: Indexed(8)}}
	bg := Indexed(8)
	var lines strings.Builder
	for range 100 {
		lines.WriteString("0123456789\n")
	}
	text := buffer.New(lines.String())
	r := &Renderer{Width: 10, Height: 12, Theme: theme}
	render := func(line int) Screen {
		return r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: line}, Scrollbar: &Scrollbar{}})
	}

	// 先頭の 10 行（100 行のうち）は、つまみが最初の 1 行になる。下の文字は残す。
	s := render(0)
	if got := thumbRows(s, 10, bg); got != "#........." {
		t.Errorf("先頭のつまみ = %s", got)
	}
	if c := s.Grid.At(0, 9); c.Text != "9" || c.Attr != theme[StyleScrollbar] {
		t.Errorf("つまみのセル = %+v", c)
	}
	// 50 行目へ跳ぶと、Vim と同じくカーソルの行を中ほどに置き、46 行目から 55 行目（0 から数える）を見る。
	// つまみは 46×10÷100 = 4 行目から、56×10÷100 の切り上げの 6 行目の手前まで。
	if got := thumbRows(render(50), 10, bg); got != "....##...." || r.Top() != 46 {
		t.Errorf("中ほどのつまみ = %s（最初の行 %d）", got, r.Top())
	}
	// 最後の行まで見えていれば、表示領域の最後の行まで伸ばす。
	if got := thumbRows(render(99), 10, bg); got != ".........#" {
		t.Errorf("最後のつまみ = %s", got)
	}
	// Scrollbar が nil なら描かない。
	s = r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: 99}})
	if got := thumbRows(s, 10, bg); got != ".........." {
		t.Errorf("nil のとき = %s", got)
	}

	// 表示領域に収まるバッファには描かない。
	short := &Renderer{Width: 10, Height: 12, Theme: theme}
	s = short.Render(&Frame{Text: buffer.New("a\nb\nc\n"), Scrollbar: &Scrollbar{}})
	if got := thumbRows(s, 10, bg); got != ".........." {
		t.Errorf("収まるバッファ = %s", got)
	}

	// 本文の幅が 1 列しかなければ描かない。
	narrow := &Renderer{Width: 1, Height: 12, Theme: theme}
	s = narrow.Render(&Frame{Text: text, Scrollbar: &Scrollbar{}})
	if got := thumbRows(s, 10, bg); got != ".........." {
		t.Errorf("1 列の本文 = %s", got)
	}

	// 全角文字の右半分に当たるつまみは、その文字を空白にする。
	wide := &Renderer{Width: 10, Height: 12, Theme: theme}
	s = wide.Render(&Frame{Text: buffer.New("あいうえお\n" + lines.String()), Scrollbar: &Scrollbar{}})
	if got := strings.TrimRight(s.Grid.RowText(0), " "); got != "あいうえ" {
		t.Errorf("全角の行 = %q", got)
	}
	if c := s.Grid.At(0, 9); c.Text != " " || c.Width != 1 || c.Attr.Bg != bg {
		t.Errorf("全角文字の右半分のつまみ = %+v", c)
	}

	// 行を折り返して表示領域より高い 1 行も、収まらないとして描く（最後の行を描ききれない）。
	tall := &Renderer{Width: 10, Height: 6, Theme: theme}
	s = tall.Render(&Frame{Text: buffer.New(strings.Repeat("x", 100) + "\n"), Wrap: true, Scrollbar: &Scrollbar{}})
	if got := thumbRows(s, 4, bg); got != "####" {
		t.Errorf("画面より高い 1 行 = %s", got)
	}
}

// スクロールバーの印の色と記号の名前は、本体の種類に加えて、"scrollbar." の後に scrollbar.marks に書ける形の名前が続くものを、
// 決まった表に頼らずに受け付ける（後に拡張機能が足す種類）。受け付けた名前の色と記号で、その種類の印を描ける。
//
// req: FR-187
func TestScrollbarMarkNames(t *testing.T) {
	for name, want := range map[string]bool{
		"scrollbar.git": true, "scrollbar.diagnostic.error": true, "scrollbar": true, "scrollbar.": false,
		"scrollbar.a,b": false, "scrollbar.a b": false, "scrollbarx": false, "gitsigns.add": false,
	} {
		if got := IsStyleName(name); got != want {
			t.Errorf("IsStyleName(%q) = %v, want %v", name, got, want)
		}
	}
	if err := CheckSymbol("scrollbar.git", "+"); err != nil {
		t.Errorf("CheckSymbol(scrollbar.git) = %v", err)
	}
	if err := CheckSymbol("scrollbar.git", "++"); err == nil {
		t.Error("2 文字の記号を受け付けた")
	}
	if err := CheckSymbol("scrollbar.", "+"); err == nil {
		t.Error("種類の名前の無い記号を受け付けた")
	}

	var lines strings.Builder
	for range 100 {
		lines.WriteString("0123456789\n")
	}
	theme := Theme{"scrollbar.git": {Fg: Indexed(2)}}
	r := &Renderer{Width: 10, Height: 12, Theme: theme, Symbols: Symbols{"scrollbar.git": "+"}}
	sb := &Scrollbar{Marks: []ScrollbarMarks{{Lines: []int{95}, Priority: 1, Style: "scrollbar.git", Symbol: "scrollbar.git"}}}
	s := r.Render(&Frame{Text: buffer.New(lines.String()), Scrollbar: sb})
	if c := s.Grid.At(9, 9); c.Text != "+" || c.Attr != theme["scrollbar.git"] {
		t.Errorf("拡張機能の種類の印 = %+v", c)
	}
}

// 描いたスクロールバーのつまみと印のセルは、HitTest が HitScrollbar として解き、そのセルに割り付けた行の範囲、つまみの上か、
// 描いた印の種類（優先で選んだもの）とその種類の範囲の中の行を返す。つまみも印も無いセルと、描いていないとき
// （Scrollbar が nil、表示領域に収まる）は、その列もテキストである。
//
// req: FR-189
func TestHitTestScrollbar(t *testing.T) {
	var lines strings.Builder
	for range 100 {
		lines.WriteString("0123456789\n")
	}
	text := buffer.New(lines.String())
	sb := &Scrollbar{Marks: []ScrollbarMarks{
		{Lines: []int{5, 52}, Priority: 1, Style: StyleScrollbarSearch, Symbol: SymbolScrollbarSearch},
		{Lines: []int{3, 7, 12}, Priority: 2, Style: StyleScrollbarDiagnosticError, Symbol: SymbolScrollbarDiagnosticError},
	}}
	r := &Renderer{Width: 10, Height: 12}
	r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: 50}, Scrollbar: sb}) // つまみは 4 行目と 5 行目
	h := r.HitTest(5, 9, HitNone)
	want := Hit{Area: HitScrollbar, Tab: -1, Line: 50, LineEnd: 60, Thumb: true, Mark: 0, MarkFirst: 52, MarkLast: 52}
	if h != want {
		t.Errorf("つまみの上の印 = %+v", h)
	}
	want = Hit{Area: HitScrollbar, Tab: -1, Line: 40, LineEnd: 50, Thumb: true, Mark: -1, MarkFirst: -1, MarkLast: -1}
	if h = r.HitTest(4, 9, HitNone); h != want {
		t.Errorf("印の無いつまみ = %+v", h)
	}
	// 0 行目は、優先の大きい error の印を描いたセルで、その種類の 0 行目から 9 行目の行を返す。
	want = Hit{Area: HitScrollbar, Tab: -1, Line: 0, LineEnd: 10, Mark: 1, MarkFirst: 3, MarkLast: 7}
	if h = r.HitTest(0, 9, HitNone); h != want {
		t.Errorf("つまみの外の印 = %+v", h)
	}
	// つまみも印も無いセルは、本文の最後の列である。
	if h = r.HitTest(7, 9, HitNone); h.Area != HitText || h.Line != 53 || h.Col != 9 {
		t.Errorf("何も描いていないセル = %+v", h)
	}
	if h = r.HitTest(5, 8, HitNone); h.Area != HitText || h.Line != 51 {
		t.Errorf("スクロールバーの左の列 = %+v", h)
	}
	// ドラッグ（テキストへ寄せて解く）の間は、スクロールバーの列もテキストである。
	if h = r.HitTest(5, 9, HitText); h.Area != HitText {
		t.Errorf("テキストへ寄せたとき = %+v", h)
	}
	// ステータス行はスクロールバーにしない。
	if h = r.HitTest(10, 9, HitNone); h.Area != HitStatusLine {
		t.Errorf("ステータス行 = %+v", h)
	}

	r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: 50}})
	if h = r.HitTest(5, 9, HitNone); h.Area != HitText {
		t.Errorf("描いていないとき = %+v", h)
	}
	short := &Renderer{Width: 10, Height: 12}
	short.Render(&Frame{Text: buffer.New("abc\n"), Scrollbar: &Scrollbar{}})
	if h = short.HitTest(0, 9, HitNone); h.Area != HitText {
		t.Errorf("収まるバッファ = %+v", h)
	}
}

// 印は、行を表示領域の行へ floor(行×行数÷バッファの行数) で割り付け、同じセルでは優先の大きい種類を 1 つだけ出す。
// 優先が同じなら前の種類を出す。つまみの上の印は、つまみの色の上に印の色を重ねる。記号が空の種類は描かない。
// 印もつまみと同じく、表示領域に収まらないときだけ描く。
//
// req: FR-187
func TestRenderScrollbarMarks(t *testing.T) {
	theme := Theme{
		StyleScrollbar:                {Bg: Indexed(8)},
		StyleScrollbarDiagnosticError: {Fg: Indexed(9)},
		StyleScrollbarSearch:          {Fg: Indexed(12)},
	}
	var lines strings.Builder
	for range 100 {
		lines.WriteString("0123456789\n")
	}
	text := buffer.New(lines.String())
	marks := []ScrollbarMarks{
		{Lines: []int{5, 55, 55, 99}, Priority: 1, Style: StyleScrollbarSearch, Symbol: SymbolScrollbarSearch},
		{Lines: []int{50, 59}, Priority: 3, Style: StyleScrollbarDiagnosticError, Symbol: SymbolScrollbarDiagnosticError},
		// 優先が同じなら、前の種類（error）を出す。
		{Lines: []int{51}, Priority: 3, Style: StyleScrollbarDiagnosticWarning, Symbol: SymbolScrollbarDiagnosticWarning},
		{Lines: []int{70}, Priority: 2, Style: StyleScrollbarDiagnosticInfo, Symbol: SymbolScrollbarDiagnosticInfo},
	}
	r := &Renderer{Width: 10, Height: 12, Theme: theme}
	s := r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: 50}, Scrollbar: &Scrollbar{Marks: marks}})
	if got, want := scrollbarColumn(s, 10), "=9999E9I9="; got != want {
		t.Errorf("右端の列 = %q, want %q", got, want)
	}
	// 5 行目はつまみ（4 行目から 5 行目）の上なので、つまみの背景に印の文字の色を重ねる。7 行目はつまみの外なので、印の色だけである。
	if a := s.Grid.At(5, 9).Attr; a != (Attr{Fg: Indexed(9), Bg: Indexed(8)}) {
		t.Errorf("つまみの上の印の色 = %+v", a)
	}
	if a := s.Grid.At(7, 9).Attr; a != (Attr{}) {
		t.Errorf("info の印の色（theme に無い） = %+v", a)
	}
	if a := s.Grid.At(0, 9).Attr; a != theme[StyleScrollbarSearch] {
		t.Errorf("つまみの外の印の色 = %+v", a)
	}

	// 記号を空にした種類は描かず、次に優先の大きい種類が出る。
	r.Symbols = Symbols{SymbolScrollbarDiagnosticError: ""}
	s = r.Render(&Frame{Text: text, Cursor: buffer.Position{Line: 50}, Scrollbar: &Scrollbar{Marks: marks}})
	if got, want := scrollbarColumn(s, 10), "=9999W9I9="; got != want {
		t.Errorf("error の記号が空の右端の列 = %q, want %q", got, want)
	}

	// 表示領域に収まるバッファには、印も描かない。
	short := &Renderer{Width: 10, Height: 12, Theme: theme}
	s = short.Render(&Frame{Text: buffer.New("abc\n"), Scrollbar: &Scrollbar{Marks: marks}})
	if got := strings.TrimRight(s.Grid.RowText(0), " "); got != "abc" {
		t.Errorf("収まるバッファの 0 行目 = %q", got)
	}
}
