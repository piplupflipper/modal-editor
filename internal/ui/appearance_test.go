package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// 編集を禁止したバッファは、ステータス行の名前の後に [RO] を出す。未保存の印と並べるときは [+] の後に出す。
//
// req: FR-87
func TestStatusLineReadOnly(t *testing.T) {
	b := buffer.New("abc\n")
	r := newRenderer(40, 4)
	tests := []struct {
		name               string
		readOnly, modified bool
		want               string
	}{
		{"禁止していない", false, false, " NORMAL  a.go"},
		{"禁止", true, false, " NORMAL  a.go [RO]"},
		{"未保存と禁止", true, true, " NORMAL  a.go [+] [RO]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := r.Render(&Frame{Text: b, Name: "a.go", ReadOnly: tt.readOnly, Modified: tt.modified})
			got := s.Grid.RowText(2)
			if !strings.HasPrefix(got, tt.want+"   ") {
				t.Errorf("ステータス行 = %q, want 先頭が %q", got, tt.want)
			}
		})
	}
}

// ステータス行の部品は、設定の並び（StatusLine）の順に、左と右へ並べる。並べなかった部品は出さず、知らない ID は描かない。
// 部品の色（statusline.<ID>）は、ステータス行の色の上に重ねる。モードの部品は、モードごとの色も重ねる。
// 編集の禁止の印と未保存の印の文字は、記号の設定で変えられる。
//
// req: FR-87, FR-92, FR-93
func TestStatusLineLayout(t *testing.T) {
	b := buffer.New("abc\n")
	theme := testTheme()
	theme[StyleStatusLine] = Attr{Bold: true}
	theme[StyleStatusLineMode] = Attr{Fg: Indexed(2)}
	theme[StyleStatusLineModeInsert] = Attr{Fg: Indexed(3)}
	theme[StyleStatusLineReadOnly] = Attr{Fg: Indexed(1)}
	r := &Renderer{
		Width: 30, Height: 4, Theme: theme,
		StatusLine: &StatusLine{Left: []string{"readonly", "name", "unknown"}, Right: []string{"filetype", "mode"}},
		Symbols:    Symbols{SymbolReadOnly: "RO", SymbolModified: "+"},
	}
	golden(t, "statusline-layout", renderSteps(r, []step{
		{"並べた順に出す", Frame{Text: b, Name: "a.go", FileType: "go", ReadOnly: true, Modified: true}},
		{"インサートモードの色", Frame{Text: b, Name: "a.go", FileType: "go", Mode: ModeInsert}},
		{"中身の無い部品は出さない", Frame{Text: b, Name: "a.go"}},
	}))
}

// 印の列の印の文字は、記号の設定で変えられる。1 列の印は右に空白を置く。
//
// req: FR-93
func TestSignSymbols(t *testing.T) {
	b := buffer.New("l0\nl1\nl2\n")
	r := newRenderer(10, 5)
	r.Symbols = Symbols{SymbolSignBreakpoint: "●", SymbolSignExecutionPoint: "->", SymbolSignDiagnosticError: "全"}
	s := r.Render(&Frame{
		Text: b, Signs: []Sign{{Line: 0, Kind: SignBreakpoint}, {Line: 1, Kind: SignExecutionPoint}},
		Diagnostics: []Diagnostic{{Range: buffer.Range{Start: pos(2, 0), End: pos(2, 1)}, Severity: SeverityError}},
	})
	for i, want := range []string{"● l0", "->l1", "全l2"} {
		if got := s.Grid.RowText(i); !strings.HasPrefix(got, want) {
			t.Errorf("%d 行目 = %q, want 先頭が %q", i, got, want)
		}
	}
}

// 枠線の記号を設定すると、浮かべる窓（popup/show の本文と選択の一覧）の周りに枠線を描く。
// 窓の位置の決め方（下に置き、収まらなければ上、右の端で左へ寄せる）は枠線を含めた大きさで行う。
//
// req: FR-93
func TestGoldenBorder(t *testing.T) {
	b := buffer.New("line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\n")
	theme := testTheme()
	theme[StylePopup] = Attr{Reverse: true}
	theme[StylePopupBorder] = Attr{Fg: Indexed(2)}
	r := &Renderer{Width: 16, Height: 10, Theme: theme, Symbols: Symbols{SymbolPopupBorder: "┌─┐│┘─└│"}}
	golden(t, "border", renderSteps(r, []step{
		{"下に置く", Frame{Text: b, Cursor: pos(0, 2), Popup: "func f()\nok"}},
		{"下に収まらなければ上に置く", Frame{Text: b, Cursor: pos(6, 0), Popup: "a\nb"}},
		{"右の端で左へ寄せる", Frame{Text: b, Cursor: pos(1, 4), Popup: "abcdefghijkl"}},
		{"選択の一覧", Frame{Text: b, Cursor: pos(0, 0), List: &List{Title: "reg", Items: []string{"a", "b"}, Selected: 1}}},
	}))
}

func TestCheckSymbol(t *testing.T) {
	tests := []struct {
		name, value string
		ok          bool
	}{
		{SymbolSignBreakpoint, "●", true},
		{SymbolSignBreakpoint, "=>", true},
		{SymbolSignBreakpoint, "全", true},
		{SymbolSignBreakpoint, "", false},
		{SymbolSignBreakpoint, "abc", false},
		{SymbolSignBreakpoint, "\t", false},
		{SymbolReadOnly, "", true},
		{SymbolReadOnly, "[読み取り専用]", true},
		{SymbolReadOnly, "a\x01", false},
		{SymbolPopupBorder, "", true},
		{SymbolPopupBorder, "┌─┐│┘─└│", true},
		{SymbolPopupBorder, "+-+|+-+|", true},
		{SymbolPopupBorder, "+-+|+-+", false},
		{SymbolPopupBorder, "+-+|+-+全", false},
		{"sign.unknown", "x", false},
	}
	for _, tt := range tests {
		err := CheckSymbol(tt.name, tt.value)
		if (err == nil) != tt.ok {
			t.Errorf("CheckSymbol(%q, %q) = %v, want ok=%v", tt.name, tt.value, err, tt.ok)
		}
	}
	for _, n := range SymbolNames() {
		if err := CheckSymbol(n, DefaultSymbols()[n]); err != nil {
			t.Errorf("既定の記号 %s が検査を通らない: %v", n, err)
		}
	}
}

// 設定の colors は、既定の対応を名前ごとに置き換える。元の対応は変えない。
//
// req: FR-91
func TestThemeWithColors(t *testing.T) {
	base := DefaultTheme()
	got := base.WithColors(map[string]Attr{StyleKeyword: {Fg: RGB(0x84, 0xa0, 0xc6)}, StyleStatusLine: {Bg: Indexed(0)}})
	if got[StyleKeyword] != (Attr{Fg: RGB(0x84, 0xa0, 0xc6)}) {
		t.Errorf("keyword = %+v", got[StyleKeyword])
	}
	if got[StyleStatusLine] != (Attr{Bg: Indexed(0)}) {
		t.Errorf("statusline は既定と混ぜずに置き換える: %+v", got[StyleStatusLine])
	}
	if got[StyleComment] != base[StyleComment] || base[StyleKeyword] == got[StyleKeyword] {
		t.Error("上書きしない名前が変わったか、元の対応が変わった")
	}
	for _, n := range StyleNames() {
		if !IsStyleName(n) {
			t.Errorf("IsStyleName(%q) = false", n)
		}
	}
	for n := range DefaultTheme() {
		if !IsStyleName(n) {
			t.Errorf("既定の対応の名前 %q が StyleNames に無い", n)
		}
	}
}
