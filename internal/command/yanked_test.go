package command

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// lastYanked は、results のうち最後に Yanked を持つものの Yanked を返す。無ければ nil である。
func lastYanked(results []Result) *Yanked {
	var y *Yanked
	for _, r := range results {
		if r.Yanked != nil {
			y = r.Yanked
		}
	}
	return y
}

// 本体の y（yy、Y、yiw、ビジュアルモードの y と Y）は、ヤンクした範囲を Result.Yanked で知らせる。
// d、c、x、D、ブラックホールのレジスターへのヤンク、失敗したヤンクは知らせない（nvim 0.12.5 の TextYankPost と同じ。
// 2026-11-20 に "_yy、yy、yiw、vly、Y、dd、x の v:event を確かめた）。
//
// req: FR-140
func TestYankedRange(t *testing.T) {
	rg := func(l1, b1, l2, b2 int) *buffer.Range {
		return &buffer.Range{Start: pos(l1, b1), End: pos(l2, b2)}
	}
	const text = "abc def\nghi j|kl\nmno"
	tests := []struct {
		keys     string
		want     *buffer.Range // nil なら知らせない
		linewise bool
	}{
		{"yy", rg(1, 0, 1, 7), true},
		{"2yy", rg(1, 0, 2, 3), true},
		{"Y", rg(1, 5, 1, 7), false},
		{"yiw", rg(1, 4, 1, 7), false},
		{`"ayy`, rg(1, 0, 1, 7), true},
		{"vly", rg(1, 5, 1, 7), false},
		{"vjy", rg(1, 5, 2, 3), false},
		{"Vy", rg(1, 0, 1, 7), true},
		{"vY", rg(1, 0, 1, 7), true},
		{`"_yy`, nil, false},
		{"5yy", rg(1, 0, 2, 3), true},
		{"j2yy", nil, false}, // 最後の行で回数が 2 以上なので失敗する
		{"dd", nil, false},
		{"D", nil, false},
		{"x", nil, false},
		{"cw<Esc>", nil, false},
		{"vld", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.keys, func(t *testing.T) {
			s := setup(t, text)
			y := lastYanked(feedKeys(s, tt.keys))
			switch {
			case tt.want == nil && y != nil:
				t.Errorf("Yanked = %+v, want nil", *y)
			case tt.want == nil:
			case y == nil:
				t.Errorf("Yanked = nil, want %v", *tt.want)
			case y.Range != *tt.want || y.Linewise != tt.linewise || y.Replayed:
				t.Errorf("Yanked = %+v, want %v linewise=%v", *y, *tt.want, tt.linewise)
			}
		})
	}
}

// マクロの再生の中のヤンクは、Replayed を立てて知らせる（色を付けるかは呼び出し側が決める。FR-140）。
// 記録している間のヤンクは、再生の中ではない。. はヤンクを繰り返さないので、. の後には知らせない。
//
// req: FR-140
func TestYankedInReplay(t *testing.T) {
	s := setup(t, "a|bc def\nghi")
	if y := lastYanked(feedKeys(s, "qayiwq")); y == nil || y.Replayed {
		t.Fatalf("記録の中の Yanked = %+v", y)
	}
	y := lastYanked(feedKeys(s, "w@a"))
	if y == nil || !y.Replayed || y.Range != (buffer.Range{Start: pos(0, 4), End: pos(0, 7)}) {
		t.Errorf("再生の中の Yanked = %+v", y)
	}
	if y := lastYanked(feedKeys(s, "yiwx.")); y == nil || y.Replayed {
		t.Errorf("yiwx. の Yanked = %+v（. の前の yiw のもの）", y)
	}
	if y := lastYanked(feedKeys(s, ".")); y != nil {
		t.Errorf(". の Yanked = %+v", *y)
	}
}
