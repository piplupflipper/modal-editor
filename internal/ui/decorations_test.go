package ui

import (
	"fmt"
	"math"
	"runtime"
	"slices"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// spansOf は、d の n 行目の範囲を、[start end order style] の文字列にする。
func spansOf(d *Decorations, n int) []string {
	var out []string
	for _, sp := range d.line(n) {
		end := fmt.Sprint(sp.end)
		if sp.end == math.MaxInt {
			end = "$"
		}
		out = append(out, fmt.Sprintf("%d-%s #%d %s", sp.start, end, sp.order, d.style(sp)))
	}
	return out
}

func rng(sl, sb, el, eb int) buffer.Range { return buffer.Range{Start: pos(sl, sb), End: pos(el, eb)} }

// 行ごとの表は、並んでいない一覧も開始の順に並べ、同じ開始では元の順を保つ。行をまたぐ範囲は各行に分ける。
//
// req: FR-35
func TestNewDecorationsLines(t *testing.T) {
	d := NewDecorations([]Highlight{
		{Range: rng(1, 5, 1, 7), Style: StyleString},
		{Range: rng(1, 2, 1, 4), Style: StyleKeyword},
		{Range: rng(0, 3, 2, 1), Style: StyleComment}, // 行をまたぐ
		{Range: rng(1, 2, 1, 3), Style: StyleKeyword}, // 同じ開始は元の順
		{Range: rng(2, 4, 2, 2), Style: StyleType},    // 終わりが開始より前
		{Range: rng(2, 6, 2, 6), Style: StyleType},    // 空
		{Range: rng(2, 0, 2, 0), Style: StyleNumber},  // 空。表に名前を足さない
	}, 3)
	want := [][]string{
		{"3-$ #2 comment"},
		{"0-$ #2 comment", "2-4 #1 keyword", "2-3 #3 keyword", "5-7 #0 string"},
		{"0-1 #2 comment", "2-4 #4 type"},
	}
	for n, w := range want {
		if got := spansOf(d, n); !slices.Equal(got, w) {
			t.Errorf("line %d = %q, want %q", n, got, w)
		}
	}
	if d.line(3) != nil || d.line(-1) != nil {
		t.Error("範囲の外の行に範囲がある")
	}
	if !slices.Equal(d.styles, []string{StyleString, StyleKeyword, StyleComment, StyleType}) {
		t.Errorf("styles = %q", d.styles)
	}
}

// 行番号がとても大きい範囲を受け取っても、表は行数の分しか作らない（NFR-03）。
// 前は終わりの行まで行ごとの表を伸ばしたので、終わりが 20 億行目の範囲 1 つで 20 億要素の確保を試みた（#75）。
//
// req: NFR-03
func TestNewDecorationsHugeLine(t *testing.T) {
	hs := []Highlight{
		{Range: rng(0, 1, 2_000_000_000, 0), Style: StyleString},
		{Range: rng(2_000_000_000, 0, 2_000_000_000, 5), Style: StyleKeyword},
		{Range: rng(math.MaxInt, 0, 1, 2), Style: StyleComment}, // 入れ替えると 1 行目から MaxInt 行目まで
		{Range: rng(math.MinInt, 0, math.MaxInt, 0), Style: StyleType},
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	d := NewDecorations(hs, 3)
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 64<<10 {
		t.Errorf("確保が %d バイト", n)
	}
	want := [][]string{
		{"0-$ #3 type", "1-$ #0 string"},
		{"0-$ #0 string", "0-$ #3 type", "2-$ #2 comment"},
		{"0-$ #0 string", "0-$ #2 comment", "0-$ #3 type"},
	}
	for n, w := range want {
		if got := spansOf(d, n); !slices.Equal(got, w) {
			t.Errorf("line %d = %q, want %q", n, got, w)
		}
	}
	if d := NewDecorations(hs, 0); d.line(0) != nil {
		t.Error("0 行のテキストに範囲がある")
	}
}

// 色の種類の名前が多くても、添字は名前ごとに 1 つで、名前を引き直せる。
func TestNewDecorationsManyStyles(t *testing.T) {
	var hs []Highlight
	for i := range 3 * smallStyles {
		hs = append(hs, Highlight{Range: rng(0, i, 0, i+1), Style: fmt.Sprint("s", i%(2*smallStyles))})
	}
	d := NewDecorations(hs, 1)
	if len(d.styles) != 2*smallStyles {
		t.Errorf("len(styles) = %d", len(d.styles))
	}
	for i, sp := range d.line(0) {
		if got, want := d.style(sp), fmt.Sprint("s", i%(2*smallStyles)); got != want {
			t.Errorf("%d: style %q, want %q", i, got, want)
		}
	}
}
