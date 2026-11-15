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
