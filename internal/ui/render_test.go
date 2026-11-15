package ui

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

var update = flag.Bool("update", false, "testdata の .golden を書き直す")

// testTheme は、ゴールデンで属性を読み分けられるように、名前ごとに違う色を割り当てたテーマである。
func testTheme() Theme {
	return Theme{
		StyleComment:           {Fg: Indexed(2)},
		StyleKeyword:           {Fg: Indexed(3)},
		StyleString:            {Fg: Indexed(4)},
		StyleNonText:           {Fg: Indexed(5)},
		StyleSpecialKey:        {Fg: Indexed(6)},
		StyleMessageError:      {Fg: Indexed(1)},
		StyleMessageWarning:    {Fg: Indexed(7)},
		StyleVisual:            {Reverse: true},
		StyleStatusLine:        {Reverse: true, Bold: true},
		StyleDiagnosticError:   {Undercurl: true, Sp: Indexed(9)},
		StyleDiagnosticWarning: {Underline: true, Sp: Indexed(10)},
	}
}

// fgCode は、testTheme の文字の色の番号から、ゴールデンに書く文字への対応である。
var fgCode = map[uint8]byte{1: 'E', 2: 'c', 3: 'k', 4: 's', 5: 'n', 6: 'x', 7: 'W'}

// cellCodes は、1 つのセルの属性を、文字の色の 1 文字と、飾りの 1 文字にする。
//
// 文字の色: '.' は既定、それ以外は fgCode。
// 飾り: ' ' は無し、'S' は反転と太字（ステータス行）、'v' は反転（選択）、'~' は波線、'_' は下線、'*' はそれらの組み合わせ。
func cellCodes(a Attr) (fg, fx byte) {
	fg = '.'
	if n, ok := a.Fg.Index(); ok {
		fg = '?'
		if c, ok := fgCode[n]; ok {
			fg = c
		}
	}
	fx = ' '
	marks := 0
	if a.Reverse && a.Bold {
		fx, marks = 'S', marks+1
	} else if a.Reverse {
		fx, marks = 'v', marks+1
	}
	if a.Undercurl {
		fx, marks = '~', marks+1
	}
	if a.Underline {
		fx, marks = '_', marks+1
	}
	if marks > 1 {
		fx = '*'
	}
	return fg, fx
}

// dump は、画面を、行ごとの文字と属性の行にした文字列を返す。
// 属性の行は、既定でない属性のセルがある行だけに出す。
func dump(s Screen) string {
	g := s.Grid
	var b strings.Builder
	fmt.Fprintf(&b, "size %dx%d cursor %d,%d\n", g.Width(), g.Height(), s.Cursor.Row, s.Cursor.Col)
	for row := range g.Height() {
		fmt.Fprintf(&b, "   |%s|\n", g.RowText(row))
		fgs, fxs := make([]byte, g.Width()), make([]byte, g.Width())
		plainFg, plainFx := true, true
		for col, c := range g.Row(row) {
			fgs[col], fxs[col] = cellCodes(c.Attr)
			plainFg = plainFg && fgs[col] == '.'
			plainFx = plainFx && fxs[col] == ' '
		}
		if !plainFg {
			fmt.Fprintf(&b, "fg |%s|\n", fgs)
		}
		if !plainFx {
			fmt.Fprintf(&b, "fx |%s|\n", fxs)
		}
	}
	return b.String()
}

// golden は、got を testdata/<name>.golden と比べる。-update なら書き直す。
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s が一致しない\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// step は、ゴールデンの 1 つの画面である。同じ Renderer で順に描き、スクロールの位置を引き継ぐ。
type step struct {
	title string
	frame Frame
}

// renderSteps は、steps を順に描いた画面をつないだ文字列を返す。
func renderSteps(r *Renderer, steps []step) string {
	var b strings.Builder
	for i, s := range steps {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n", s.title)
		b.WriteString(dump(r.Render(&s.frame)))
	}
	return b.String()
}

func newRenderer(w, h int) *Renderer {
	return &Renderer{Width: w, Height: h, Theme: testTheme()}
}

func pos(line, b int) buffer.Position {
	return buffer.Position{Line: line, Byte: b}
}

func TestGoldenWide(t *testing.T) {
	b := buffer.New("あいうえおかきくけこ\nxあいうえおかきくけこ\nabc日本語def\n")
	r := newRenderer(11, 6)
	golden(t, "wide", renderSteps(r, []step{
		{"右の端に半分だけ掛かる全角文字は出さない", Frame{Text: b, Name: "a.txt", Cursor: pos(0, 0)}},
		{"全角文字の上のカーソル", Frame{Text: b, Name: "a.txt", Cursor: pos(2, 6)}},
		{"横のスクロールで左の端が欠けた全角文字は <", Frame{Text: b, Name: "a.txt", Cursor: pos(0, 27)}},
		{"左の端より左へ戻るとスクロールを戻す", Frame{Text: b, Name: "a.txt", Cursor: pos(1, 1)}},
	}))
}

func TestGoldenTab(t *testing.T) {
	b := buffer.New("\tx\na\tb\nabcdefgh\tz\n")
	r := newRenderer(20, 6)
	golden(t, "tab", renderSteps(r, []step{
		{"ノーマルモードではタブの最後の列", Frame{Text: b, Cursor: pos(0, 0)}},
		{"インサートモードではタブの最初の列", Frame{Text: b, Mode: ModeInsert, Cursor: pos(1, 1)}},
		{"8 列ちょうどの後のタブ", Frame{Text: b, Cursor: pos(2, 8)}},
	})+"\n"+renderSteps(&Renderer{Width: 20, Height: 6, TabStop: 4, Theme: testTheme()}, []step{
		{"タブ幅 4", Frame{Text: b, Cursor: pos(1, 1)}},
	}))
}

func TestGoldenLongLine(t *testing.T) {
	var line strings.Builder
	for i := range 60 {
		line.WriteByte(byte('a' + i%26))
	}
	b := buffer.New(line.String() + "\nshort\n")
	r := newRenderer(20, 4)
	golden(t, "longline", renderSteps(r, []step{
		{"右の端まで", Frame{Text: b, Cursor: pos(0, 19)}},
		{"1 列はみ出すと、カーソルを幅の半分に置く", Frame{Text: b, Cursor: pos(0, 20)}},
		{"画面の中の移動ではスクロールしない", Frame{Text: b, Cursor: pos(0, 12)}},
		{"行末", Frame{Text: b, Cursor: pos(0, 59)}},
		{"短い行へ移ると左へ戻る", Frame{Text: b, Cursor: pos(1, 4)}},
	}))
}

func TestGoldenLastLines(t *testing.T) {
	var s strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&s, "line %d\n", i)
	}
	b := buffer.New(s.String())
	r := newRenderer(16, 7)
	golden(t, "lastlines", renderSteps(r, []step{
		{"先頭", Frame{Text: b, Name: "n.txt", Cursor: pos(0, 0)}},
		{"1 行下へはみ出す", Frame{Text: b, Name: "n.txt", Cursor: pos(5, 0)}},
		{"最終行へ飛ぶと最終行を下の端に置く", Frame{Text: b, Name: "n.txt", Cursor: pos(19, 0)}},
		{"最終行の付近で上へ", Frame{Text: b, Name: "n.txt", Cursor: pos(15, 3)}},
		{"短いバッファは ~ の行", Frame{Text: buffer.New("one\ntwo\n"), Name: "n.txt", Cursor: pos(1, 0), Modified: true}},
	}))
}

func TestGoldenVisual(t *testing.T) {
	b := buffer.New("abc\n\ndef\nあいう\n")
	r := newRenderer(12, 7)
	golden(t, "visual", renderSteps(r, []step{
		{"文字単位。行をまたぐと行末の 1 セルも選ぶ。カーソルの文字は反転しない", Frame{
			Text: b, Mode: ModeVisual, Cursor: pos(2, 0),
			Selection: Selection{Active: true, Start: pos(0, 1), End: pos(2, 0)},
		}},
		{"文字単位で後ろから選ぶ", Frame{
			Text: b, Mode: ModeVisual, Cursor: pos(0, 1),
			Selection: Selection{Active: true, Start: pos(2, 1), End: pos(0, 1)},
		}},
		{"行単位", Frame{
			Text: b, Mode: ModeVisualLine, Cursor: pos(3, 3),
			Selection: Selection{Active: true, Start: pos(2, 1), End: pos(3, 3), Linewise: true},
		}},
		{"全角文字の選択は 2 セル", Frame{
			Text: b, Mode: ModeVisual, Cursor: pos(3, 6),
			Selection: Selection{Active: true, Start: pos(3, 0), End: pos(3, 6)},
		}},
	}))
}

// req: FR-35
func TestGoldenDecorations(t *testing.T) {
	b := buffer.New("func f() {\n\t// note\n\treturn \"s\"\n}\n")
	r := newRenderer(20, 6)
	decos := []Highlight{
		{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 4)}, Style: StyleKeyword},
		{Range: buffer.Range{Start: pos(1, 1), End: pos(1, 8)}, Style: StyleComment},
		{Range: buffer.Range{Start: pos(2, 1), End: pos(2, 7)}, Style: StyleKeyword},
		{Range: buffer.Range{Start: pos(2, 8), End: pos(2, 11)}, Style: StyleString},
		{Range: buffer.Range{Start: pos(0, 5), End: pos(0, 6)}, Style: "unknown"},
		{Range: buffer.Range{Start: pos(3, 0), End: pos(3, 1)}, Style: StyleNumber}, // テーマに無い名前は色を付けない
	}
	diags := []Diagnostic{
		{Range: buffer.Range{Start: pos(0, 2), End: pos(0, 7)}, Severity: SeverityError},
		{Range: buffer.Range{Start: pos(2, 11), End: pos(2, 11)}, Severity: SeverityWarning}, // 行末の空の範囲
		{Range: buffer.Range{Start: pos(1, 4), End: pos(2, 2)}, Severity: SeverityWarning},   // 行をまたぐ
	}
	golden(t, "decorations", renderSteps(r, []step{
		{"構文の色と診断", Frame{Text: b, Cursor: pos(3, 0), Decorations: decos, Diagnostics: diags}},
		{"選択を重ねる", Frame{
			Text: b, Mode: ModeVisual, Cursor: pos(0, 9), Decorations: decos,
			Selection: Selection{Active: true, Start: pos(0, 2), End: pos(0, 9)},
		}},
	}))
}

func TestGoldenSpecialChars(t *testing.T) {
	b := buffer.New("a\x01b\x7fc\n\xe3\x81d\u0085e\n\u200bcafe\u0301!\n\u0301x\n")
	r := newRenderer(20, 6)
	golden(t, "special", renderSteps(r, []step{
		{"制御文字と不正な UTF-8 と結合文字", Frame{Text: b, Cursor: pos(2, 3)}},
	}))
}

func TestGoldenStatusAndCommandLine(t *testing.T) {
	b := buffer.New("hello\n")
	r := newRenderer(24, 4)
	golden(t, "statusline", renderSteps(r, []step{
		{"名前なし", Frame{Text: b, Cursor: pos(0, 4)}},
		{"変更ありと記録中", Frame{Text: b, Name: "dir/file.go", Modified: true, Recording: 'q', Mode: ModeInsert, Cursor: pos(0, 5)}},
		{"幅が足りなければ左の部分を切る", Frame{Text: b, Name: "very/long/path/to/日本語.go", Mode: ModeVisualLine, Cursor: pos(0, 0)}},
		{"コマンドライン", Frame{Text: b, Mode: ModeCommandLine, CommandLine: "s/a/b/g", Cursor: pos(0, 0)}},
		{"長いコマンドラインは末尾を見せる", Frame{Text: b, Mode: ModeCommandLine, CommandLine: "s/abcdefghijklmnop/qrstuvwxyz/g", Cursor: pos(0, 0)}},
		{"エラーのメッセージ", Frame{Text: b, Cursor: pos(0, 0), Message: Message{Text: "E37: 変更が保存されていません\n2 行目", Level: MessageError}}},
		{"空の行のルーラー", Frame{Text: buffer.New(""), Cursor: pos(0, 0)}},
	}))
}

// TestScrollVertical は、縦のスクロールの位置を、端末の Vim 9.1 で測った値と比べる。
// 測り方: 100 行のファイルを `vim -Nu NONE` で開き、表示領域の高さ h の端末で、gg の後に nG（または G の後に nG）を打ち、line('w0') を読んだ。
func TestScrollVertical(t *testing.T) {
	var s strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&s, "%d\n", i)
	}
	b := buffer.New(s.String())
	tests := []struct {
		h        int
		from, to int // 1 から数えた行（Vim の nG）
		wantTop  int // 1 から数えた line('w0')
	}{
		{11, 1, 12, 2},
		{11, 1, 17, 7},
		{11, 1, 18, 13},
		{11, 1, 50, 45},
		{11, 1, 100, 90},
		{11, 100, 89, 89},
		{11, 100, 87, 87},
		{11, 100, 86, 81},
		{11, 100, 85, 80},
		{10, 1, 15, 6},
		{10, 1, 16, 11},
		{10, 100, 88, 88},
		{10, 100, 87, 83},
		{12, 1, 18, 7},
		{12, 1, 19, 13},
		{12, 100, 85, 85},
		{12, 100, 84, 79},
		{21, 1, 32, 12},
		{21, 1, 33, 23},
		{21, 100, 72, 72},
		{21, 100, 71, 61},
		{4, 1, 6, 3},
		{4, 1, 7, 5},
		{4, 100, 96, 96},
		{4, 100, 95, 94},
		{3, 100, 97, 97},
		{3, 100, 96, 95},
	}
	for _, tt := range tests {
		r := &Renderer{Width: 20, Height: tt.h + 2}
		r.Render(&Frame{Text: b, Cursor: pos(tt.from-1, 0)})
		r.Render(&Frame{Text: b, Cursor: pos(tt.to-1, 0)})
		if got := r.Top() + 1; got != tt.wantTop {
			t.Errorf("h=%d %dG→%dG: top %d, want %d", tt.h, tt.from, tt.to, got, tt.wantTop)
		}
	}
}

// TestScrollHorizontal は、横のスクロールの位置を、端末の Vim 9.1（'nowrap'）で測った値と比べる。
// 測り方: 300 文字の行を幅 w の端末で開き、0 の後に n|（または 100| の後に n|）を打ち、winsaveview().leftcol を読んだ。
func TestScrollHorizontal(t *testing.T) {
	b := buffer.New(strings.Repeat("x", 300) + "\n")
	tests := []struct {
		w        int
		from, to int // 1 から数えた列（Vim の n|）
		wantLeft int
	}{
		{40, 1, 40, 0},
		{40, 1, 41, 20},
		{40, 1, 100, 79},
		{40, 100, 99, 79},
		{40, 100, 79, 58},
		{40, 100, 30, 9},
		{40, 100, 20, 0},
		{40, 100, 120, 99},
		{41, 1, 41, 0},
		{41, 1, 42, 21},
		{41, 100, 120, 79},
		{41, 100, 121, 100},
	}
	for _, tt := range tests {
		r := &Renderer{Width: tt.w, Height: 5}
		r.Render(&Frame{Text: b, Cursor: pos(0, tt.from-1)})
		r.Render(&Frame{Text: b, Cursor: pos(0, tt.to-1)})
		if got := r.Left(); got != tt.wantLeft {
			t.Errorf("w=%d %d|→%d|: left %d, want %d", tt.w, tt.from, tt.to, got, tt.wantLeft)
		}
	}
}

// TestRenderSmall は、小さな画面と空の入力で落ちないことを確かめる。
func TestRenderSmall(t *testing.T) {
	for _, size := range [][2]int{{0, 0}, {1, 1}, {1, 2}, {3, 3}, {2, 5}} {
		r := &Renderer{Width: size[0], Height: size[1]}
		frames := []Frame{
			{},
			{Text: buffer.New("あいう\tx\n"), Cursor: pos(5, 99), Mode: ModeCommandLine, CommandLine: "w"},
			{
				Text: buffer.New("abc\n"), Mode: ModeVisual,
				Selection:   Selection{Active: true, Start: pos(-1, -1), End: pos(9, 9)},
				Decorations: []Highlight{{Range: buffer.Range{Start: pos(9, 9), End: pos(0, 1)}, Style: StyleKeyword}},
			},
		}
		for _, f := range frames {
			g := r.Render(&f).Grid
			if g.Width() != size[0] || g.Height() != size[1] {
				t.Errorf("size %v: grid %dx%d", size, g.Width(), g.Height())
			}
		}
	}
}

// TestRenderDoesNotModify は、描画が入力の Frame とバッファを変えないことを確かめる。
func TestRenderDoesNotModify(t *testing.T) {
	b := buffer.New("abc\n")
	f := Frame{Text: b, Cursor: pos(5, 9), Decorations: []Highlight{{Range: buffer.Range{Start: pos(0, 2), End: pos(0, 0)}, Style: StyleKeyword}}}
	before := fmt.Sprintf("%+v", f)
	v := b.Version()
	(&Renderer{Width: 10, Height: 4}).Render(&f)
	if after := fmt.Sprintf("%+v", f); after != before {
		t.Errorf("Frame が変わった: %s → %s", before, after)
	}
	if b.Version() != v || b.Modified() {
		t.Error("バッファが変わった")
	}
}

func TestDefaultTheme(t *testing.T) {
	th := DefaultTheme()
	for _, name := range []string{StyleKeyword, StyleVisual, StyleStatusLine, StyleDiagnosticError, StyleMessageError} {
		if _, ok := th[name]; !ok {
			t.Errorf("DefaultTheme に %s が無い", name)
		}
	}
	if th[StyleVisual] != (Attr{Reverse: true}) {
		t.Errorf("選択は反転で表示する: %+v", th[StyleVisual])
	}
}
