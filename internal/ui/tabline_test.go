package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// req: FR-62, FR-87
func TestGoldenTabLine(t *testing.T) {
	b := buffer.New("one\ntwo\nthree\nfour\nfive\nsix\n")
	tabs := []Tab{{Name: "a.go"}, {Name: "b.go", Modified: true, Current: true}, {Name: "", ReadOnly: true}}
	r := newRenderer(30, 6)
	golden(t, "tabline", renderSteps(r, []step{
		{"タブが 1 つなら出さない", Frame{Text: b, Name: "a.go", Cursor: pos(0, 0), Tabs: tabs[:1]}},
		{"タブが 2 つ以上なら一番上の行に出し、表示領域を 1 行減らす", Frame{Text: b, Name: "b.go", Cursor: pos(3, 0), Tabs: tabs}},
		{"幅に収まらなければ、カーソルのあるタブが収まるまで左を省く", Frame{Text: b, Name: "c.go", Cursor: pos(0, 0), Tabs: []Tab{
			{Name: "first.go"}, {Name: "second.go"}, {Name: "third.go"}, {Name: "current.go", Current: true}, {Name: "after.go"},
		}}},
	}))
}

func TestTabLineMode(t *testing.T) {
	b := buffer.New("x\n")
	one := []Tab{{Name: "a.go", Current: true}}
	two := []Tab{{Name: "a.go", Current: true}, {Name: "b.go"}}
	tests := []struct {
		mode TabLineMode
		tabs []Tab
		want bool
	}{
		{TabLineMultiple, one, false},
		{TabLineMultiple, two, true},
		{TabLineAlways, one, true},
		{TabLineNever, two, false},
	}
	for _, tt := range tests {
		r := newRenderer(20, 5)
		r.TabLine = tt.mode
		s := r.Render(&Frame{Text: b, Tabs: tt.tabs})
		got := strings.HasPrefix(s.Grid.RowText(0), " a.go")
		if got != tt.want {
			t.Errorf("mode %d, %d タブ: タブの行 = %v, want %v\n%s", tt.mode, len(tt.tabs), got, tt.want, s.Grid)
		}
		if rows := r.TextRows(); rows != 3-boolToInt(tt.want) {
			t.Errorf("mode %d, %d タブ: TextRows = %d", tt.mode, len(tt.tabs), rows)
		}
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
