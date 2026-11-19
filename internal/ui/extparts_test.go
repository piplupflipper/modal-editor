package ui

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// 拡張機能が足した部品（Frame.ExtParts）は、本体の部品と同じく並びの位置に出す。
// 中身が無い部品と、並びに書いても中身の届いていない部品は出さない（lualine の cond が偽の部品と同じ）。
// 色は statusline の上に statusline.ext を重ね、その上に部品の style を重ねる。
//
// req: FR-92
func TestStatusLineExtParts(t *testing.T) {
	b := buffer.New("abc\n")
	theme := DefaultTheme().WithColors(map[string]Attr{
		StyleStatusLineExt:     {Italic: true},
		StyleDiagnosticWarning: {Fg: Indexed(3)},
	})
	r := &Renderer{
		Width: 60, Height: 4, Theme: theme,
		StatusLine: &StatusLine{
			Left:  []string{"mode", "mrreview.status", "mrreview.drift", "name"},
			Right: []string{"memusage.rss", "missing.part", "ruler"},
		},
	}
	f := &Frame{Text: b, Name: "a.go", ExtParts: map[string]ExtPart{
		"mrreview.status": {Text: "!12 3/10"},
		"mrreview.drift":  {Text: "base drift", Style: StyleDiagnosticWarning},
		"memusage.rss":    {Text: "RSS 120M"},
	}}
	g := r.Render(f).Grid
	want := " NORMAL  !12 3/10  base drift  a.go           RSS 120M  1,1 "
	if got := g.RowText(2); got != want {
		t.Errorf("ステータス行\n got %q\nwant %q", got, want)
	}
	// 部品の style が無ければ、statusline（反転と太字）の上に statusline.ext（斜体）を重ねる。
	if got, want := g.At(2, 10).Attr, (Attr{Reverse: true, Bold: true, Italic: true}); got != want {
		t.Errorf("mrreview.status の色 = %+v, want %+v", got, want)
	}
	// style が文字の色を持てば、本体の部品と同じく statusline の反転を外す（PR #206 のレビュー）。
	if got, want := g.At(2, 21).Attr, (Attr{Fg: Indexed(3), Bold: true, Italic: true}); got != want {
		t.Errorf("mrreview.drift の色 = %+v, want %+v", got, want)
	}

	// 中身を空にした部品は出さない。
	f.ExtParts["mrreview.drift"] = ExtPart{}
	want = " NORMAL  !12 3/10  a.go                       RSS 120M  1,1 "
	if got := r.Render(f).Grid.RowText(2); got != want {
		t.Errorf("drift を消した後\n got %q\nwant %q", got, want)
	}
}

// 幅が足りなければ、本体の部品（モード、名前の末尾、位置）より先に、拡張機能の部品を幅の広いものから切り詰めるか落とす
// （PR #239 のレビューの推奨 1。幅 30 で長いブランチ名の部品を送ると、ステータス行がブランチ名だけになっていた）。
//
// req: FR-92
func TestStatusLineExtPartsNarrow(t *testing.T) {
	b := buffer.New("abc\n")
	layout := &StatusLine{Left: []string{"mode", "tp.a", "name"}, Right: []string{"tp.b", "tp.mem", "ruler"}}
	parts := map[string]ExtPart{
		"tp.a": {Text: "review:!12"}, "tp.b": {Text: "feature/very-long-branch-name"}, "tp.mem": {Text: "MEM"},
	}
	tests := []struct {
		width int
		want  string
	}{
		{80, " NORMAL  review:!12  a.txt              feature/very-long-branch-name  MEM  1,1 "},
		{50, " NORMAL  review:!12  a.txt  feature/ve>  MEM  1,1 "},
		{30, " NORMAL  re>  a.txt  MEM  1,1 "},
		{20, " NORMAL  a.txt  1,1 "},
	}
	for _, tt := range tests {
		r := &Renderer{Width: tt.width, Height: 4, StatusLine: layout}
		got := r.Render(&Frame{Text: b, Name: "a.txt", ExtParts: parts}).Grid.RowText(2)
		if got != tt.want {
			t.Errorf("幅 %d\n got %q\nwant %q", tt.width, got, tt.want)
		}
	}
}

// タブの行も、ステータス行と同じく left と right の並びで部品を描く（FR-92）。
// タブの見出しの並び（tabs）は、他の部品を描いた残りの幅を使い、収まらなければ #215 と同じく左のタブを省く。
//
// req: FR-62, FR-92
func TestTabLineParts(t *testing.T) {
	b := buffer.New("abc\n")
	tabs := []Tab{{Name: "a.go"}, {Name: "b.go", Current: true}}
	parts := map[string]ExtPart{"memusage.rss": {Text: "RSS 120M"}, "long.part": {Text: "feature/very-long-branch-name"}}
	tests := []struct {
		name   string
		width  int
		layout *StatusLine
		tabs   []Tab
		want   string
	}{
		{"既定は左にタブだけ", 30, nil, tabs, " a.go  b.go                   "},
		{
			"右に拡張機能の部品", 30, &StatusLine{Left: []string{"tabs"}, Right: []string{"memusage.rss"}}, tabs,
			" a.go  b.go          RSS 120M ",
		},
		{
			"右にタブ、左に本体の部品", 30, &StatusLine{Left: []string{"mode"}, Right: []string{"tabs"}}, tabs,
			" NORMAL  a.go  b.go           ",
		},
		{
			"タブが残りの幅に収まらなければ左のタブを省く", 20, &StatusLine{Left: []string{"tabs"}, Right: []string{"memusage.rss"}},
			[]Tab{{Name: "first.go"}, {Name: "second.go"}, {Name: "now.go", Current: true}},
			"< now.go   RSS 120M ",
		},
		{
			"幅が足りなければ拡張機能の部品を切り、いまのタブの見出しを残す", 20,
			&StatusLine{Left: []string{"tabs"}, Right: []string{"long.part"}}, tabs, "< b.go feature/ver> ",
		},
		{
			"タブを並べなければタブの見出しを出さない", 30, &StatusLine{Right: []string{"memusage.rss"}}, tabs,
			"                     RSS 120M ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRenderer(tt.width, 5)
			r.TabLineParts = tt.layout
			g := r.Render(&Frame{Text: b, Name: "b.go", Tabs: tt.tabs, ExtParts: parts}).Grid
			if got := g.RowText(0); got != tt.want {
				t.Errorf("タブの行\n got %q\nwant %q", got, tt.want)
			}
		})
	}
}

// タブの行に並べられる本体の部品は、タブの見出しの並びとステータス行の本体の部品である。
func TestIsTabLinePart(t *testing.T) {
	for id, want := range map[string]bool{"tabs": true, "mode": true, "ruler": true, "memusage.rss": false, "zzz": false} {
		if got := IsTabLinePart(id); got != want {
			t.Errorf("IsTabLinePart(%q) = %v, want %v", id, got, want)
		}
	}
	if IsStatusPart("tabs") {
		t.Error(`IsStatusPart("tabs") = true, want false`)
	}
}
