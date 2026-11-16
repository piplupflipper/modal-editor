package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// 印の列は、印か診断があるときだけ 2 列で出し、1 つの行に複数の印があれば優先順位の最も高いものを出す。
// 止まった位置（=>）はブレークポイントより、ブレークポイントは診断より上に出る。表示領域の外の印は描かない。
//
// req: FR-31
func TestGoldenSigns(t *testing.T) {
	b := buffer.New("l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\n")
	theme := testTheme()
	theme[StyleSignBreakpoint] = Attr{Fg: Indexed(1)}
	theme[StyleSignExecutionPoint] = Attr{Fg: Indexed(3)}
	theme[StyleSignDiagnosticWarning] = Attr{Fg: Indexed(7)}
	r := &Renderer{Width: 12, Height: 7, Theme: theme}
	signs := []Sign{
		{Line: 0, Kind: SignBreakpoint},
		{Line: 1, Kind: SignBreakpoint},
		{Line: 1, Kind: SignExecutionPoint}, // ブレークポイントより上
		{Line: 2, Kind: SignBreakpointUnverified},
		{Line: 3, Kind: "unknown"}, // 知らない種類は描かない
		{Line: 7, Kind: SignBreakpoint},
	}
	diags := []Diagnostic{
		{Range: buffer.Range{Start: pos(0, 0), End: pos(0, 1)}, Severity: SeverityError}, // ブレークポイントに隠れる
		{Range: buffer.Range{Start: pos(4, 1), End: pos(5, 0)}, Severity: SeverityWarning},
		{Range: buffer.Range{Start: pos(4, 0), End: pos(4, 0)}, Severity: SeverityHint}, // warning に隠れる
	}
	golden(t, "signs", renderSteps(r, []step{
		{"印と診断", Frame{Text: b, Cursor: pos(1, 1), Signs: signs, Diagnostics: diags}},
		{"下へスクロール", Frame{Text: b, Cursor: pos(7, 0), Signs: signs, Diagnostics: diags}},
		{"印が無ければ列を出さない", Frame{Text: b, Cursor: pos(7, 0)}},
	}))
}

// 出力の欄は、ステータス行とコマンドラインの行の間に、中身と欄の名前の行を出す。
// 長い行は折り返し、末尾に追従し、Bottom で上へ戻せる。最後の空の行（改行で終わった後の行）は出さない。
func TestGoldenOutput(t *testing.T) {
	b := buffer.New("x\ny\n")
	theme := testTheme()
	theme[StyleStatusLineNC] = Attr{Reverse: true}
	r := &Renderer{Width: 12, Height: 10, Theme: theme}
	var lines []string
	for i := range 8 {
		lines = append(lines, strings.Repeat(string(rune('a'+i)), 1+i*3))
	}
	lines = append(lines, "全角の幅を折り返す", "\x1b[0m", "")
	golden(t, "output", renderSteps(r, []step{
		{"末尾に追従", Frame{Text: b, Output: &Output{Title: "acp", Lines: lines, Bottom: -1}}},
		{"上へ戻す", Frame{Text: b, Output: &Output{Title: "acp", Lines: lines, Bottom: 3}}},
		{"先頭より上へは戻らない", Frame{Text: b, Output: &Output{Title: "acp", Lines: lines, Bottom: 0}}},
		{"空の欄", Frame{Text: b, Output: &Output{Title: "dap"}}},
	}))
	if r.OutputRows() != 3 || r.TextRows() != 4 {
		t.Errorf("10 行の画面: OutputRows = %d, TextRows = %d, want 3, 4", r.OutputRows(), r.TextRows())
	}
	r = &Renderer{Width: 12, Height: 4}
	r.Render(&Frame{Text: b, Output: &Output{Lines: lines}})
	if r.OutputRows() != 0 || r.TextRows() != 2 {
		t.Errorf("4 行の画面: OutputRows = %d, TextRows = %d, want 0, 2", r.OutputRows(), r.TextRows())
	}
}

// 折り返しは表示幅で数え、全角文字が収まらなければ次の行へ送る。1 文字が幅より広ければ、その文字だけで 1 行にする。
func TestWrapLine(t *testing.T) {
	tests := []struct {
		s     string
		width int
		want  []string
	}{
		{"", 5, []string{""}},
		{"abcdefg", 3, []string{"abc", "def", "g"}},
		{"aあい", 4, []string{"aあ", "い"}},
		{"あ", 1, []string{"あ"}},
		{"a\tb", 4, []string{"a", "\t", "b"}},
	}
	for _, tt := range tests {
		var got []string
		for _, row := range wrapLine(0, tt.s, tt.width, 8) {
			got = append(got, tt.s[row.start:row.end])
		}
		if strings.Join(got, "|") != strings.Join(tt.want, "|") {
			t.Errorf("wrapLine(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}
