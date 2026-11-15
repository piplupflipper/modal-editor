package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// jsonDecorations は、cmd/modal-editor-ext-treesitter のテストの bigJSON(n) と同じ形の行
// （{"id": N, "name": "itemN", "ok": true, "v": null},）に、tree-sitter の拡張機能が付けるのと同じ 8 つの範囲を付けた一覧を返す。
// 色の種類の名前は、ext.DecorationSetParams の UnmarshalJSON と同じく、表の文字列を共有する。
func jsonDecorations(n int) []ext.Decoration {
	styles := []string{"string", "number", "constant"}
	decs := make([]ext.Decoration, 0, 8*n)
	for i := range n {
		line := fmt.Sprintf("{\"id\": %d, \"name\": \"item%d\", \"ok\": true, \"v\": null},", i, i)
		add := func(token string, style int, from int) int {
			s := from + strings.Index(line[from:], token)
			decs = append(decs, ext.Decoration{
				Range: ext.Range{Start: ext.Position{Line: i + 1, Byte: s}, End: ext.Position{Line: i + 1, Byte: s + len(token)}},
				Style: styles[style],
			})
			return s + len(token)
		}
		at := add(`"id"`, 0, 0)
		at = add(fmt.Sprint(i), 1, at)
		at = add(`"name"`, 0, at)
		at = add(fmt.Sprintf(`"item%d"`, i), 0, at)
		at = add(`"ok"`, 0, at)
		at = add("true", 2, at)
		at = add(`"v"`, 0, at)
		add("null", 2, at)
	}
	return decs
}

// jsonText は、jsonDecorations(n) の範囲が指す n+2 行の JSON のテキストである。
func jsonText(n int) string {
	var sb strings.Builder
	sb.WriteString("[\n")
	for i := range n {
		fmt.Fprintf(&sb, "{\"id\": %d, \"name\": \"item%d\", \"ok\": true, \"v\": null},\n", i, i)
	}
	sb.WriteString("{}\n]")
	return sb.String()
}

// benchBuild は、n 行の JSON の一覧から表を作る時間を測る。イベントループの外の goroutine で行う処理である。
func benchBuild(b *testing.B, n int) {
	b.Helper()
	src := []decoSource{{ext: "treesitter", decs: jsonDecorations(n)}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buildDecorations(src, n+2)
	}
}

func BenchmarkBuildDecorations20k(b *testing.B)  { benchBuild(b, 20000) }
func BenchmarkBuildDecorations100k(b *testing.B) { benchBuild(b, 100000) }
func BenchmarkBuildDecorations400k(b *testing.B) { benchBuild(b, 400000) }

// benchSet は、n 行の JSON の一覧が届いたときに、イベントループの上で行う処理
// （一覧を持ち、表を作る goroutine を起こす setDecorations と、作り終えた表を受け取る decorationsBuilt）の時間を測る。
// 表を作り終えるのを待つ間は計らない。
func benchSet(b *testing.B, n int) {
	b.Helper()
	buf := buffer.New(jsonText(n))
	bs := &bufferState{id: 1, buf: buf, decorations: map[string]decorationSet{}}
	e := &Editor{bufs: []*bufferState{bs}, decoDone: make(chan decoBuilt), stop: make(chan struct{})}
	defer close(e.stop)
	decs := jsonDecorations(n)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		e.setDecorations("treesitter", ext.DecorationSetParams{Buffer: 1, Version: ext.Version(buf.Version()), Decorations: decs})
		b.StopTimer()
		r := <-e.decoDone
		b.StartTimer()
		e.decorationsBuilt(r)
		if bs.decoView == nil {
			b.Fatal("表ができていない")
		}
	}
}

func BenchmarkSetDecorations20k(b *testing.B)  { benchSet(b, 20000) }
func BenchmarkSetDecorations100k(b *testing.B) { benchSet(b, 100000) }
func BenchmarkSetDecorations400k(b *testing.B) { benchSet(b, 400000) }
