package ui

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// sameLines は、同じ行を n 行並べたテキストである。大きなバッファの描画を、メモリを使わずに測るために使う。
type sameLines struct {
	line string
	n    int
}

func (s sameLines) LineCount() int  { return s.n }
func (s sameLines) Line(int) string { return s.line }

// perLine は、各行に、幅 step バイトごとに 1 つずつ、count 個の色の範囲を付けた一覧を作る。
func perLine(lines, count, step int) *Decorations {
	styles := []string{StyleKeyword, StyleString, StyleComment, StyleType}
	hs := make([]Highlight, 0, lines*count)
	for n := range lines {
		for i := range count {
			hs = append(hs, Highlight{
				Range: buffer.Range{Start: pos(n, i*step), End: pos(n, i*step+step/2)},
				Style: styles[i%len(styles)],
			})
		}
	}
	return NewDecorations(hs, lines)
}

// benchRender は、200 × 60 の画面で、カーソルを 1 行ずつ下げながら f を描く時間を測る。
func benchRender(b *testing.B, f Frame) {
	b.Helper()
	r := &Renderer{Width: 200, Height: 60}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		f.Cursor.Line = 5000 + i%50
		r.Render(&f)
	}
}

func BenchmarkRenderPlain(b *testing.B) {
	line := "\tfunc example(あいう int) string { return \"日本語\" } // comment"
	benchRender(b, Frame{Text: sameLines{line, 10000}})
}

func BenchmarkRenderLongLines(b *testing.B) {
	line := strings.Repeat("abcdefghij", 2200)
	benchRender(b, Frame{Text: sameLines{line, 10000}, Cursor: pos(0, 20000)})
}

func BenchmarkRenderManyDecorations(b *testing.B) {
	line := strings.Repeat("abcd ", 40)
	benchRender(b, Frame{Text: sameLines{line, 10000}, Decorations: perLine(10000, 60, 3)})
}

func BenchmarkRenderDenseDecorationsScrolled(b *testing.B) {
	line := strings.Repeat("ab", 1000)
	f := Frame{Text: sameLines{line, 1000}, Decorations: perLine(1000, 500, 4), Cursor: pos(0, 1500)}
	r := &Renderer{Width: 200, Height: 60}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		f.Cursor.Line = 500 + i%50
		r.Render(&f)
	}
}

// spanning は、perLine(lines, 8, 4) の一覧に、ranges 個の行をまたぐ範囲を足した一覧である。
// i 個目の範囲は、i 行目の途中から、末尾から i 行目の途中までにまたがる（ほぼ全体）。
func spanning(lines, ranges int) []Highlight {
	styles := []string{StyleKeyword, StyleString, StyleComment, StyleType}
	var hs []Highlight
	for n := range lines {
		for i := range 8 {
			hs = append(hs, Highlight{Range: buffer.Range{Start: pos(n, i*4), End: pos(n, i*4+2)}, Style: styles[i%len(styles)]})
		}
	}
	for i := range ranges {
		hs = append(hs, Highlight{Range: buffer.Range{Start: pos(i, 3), End: pos(lines-1-i, 5)}, Style: styles[i%len(styles)]})
	}
	return hs
}

// 1 行の中の範囲だけの一覧（行をまたぐ範囲が無い、ふつうの場合）。
func BenchmarkNewDecorationsSingleLine100k(b *testing.B) {
	hs := spanning(100_000, 0)
	b.ReportAllocs()
	for b.Loop() {
		NewDecorations(hs, 100_000)
	}
}

// ほぼ全体にまたがる範囲が 100 個ある一覧。前は行ごとに展開したので、表が 100 × 10 万要素になった（#80）。
func BenchmarkNewDecorationsMultiline100k(b *testing.B) {
	hs := spanning(100_000, 100)
	b.ReportAllocs()
	for b.Loop() {
		NewDecorations(hs, 100_000)
	}
}

func BenchmarkRenderMultilineDecorations(b *testing.B) {
	line := strings.Repeat("abcd ", 40)
	benchRender(b, Frame{Text: sameLines{line, 10000}, Decorations: NewDecorations(spanning(10000, 100), 10000)})
}
