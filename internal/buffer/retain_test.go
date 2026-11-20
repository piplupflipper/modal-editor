package buffer

import (
	"runtime"
	"strings"
	"testing"
)

// アンドゥの記録（逆の編集のテキスト）は、編集の前の行を参照しない（#279）。
// 前は、逆の編集のテキストが編集の前の行の部分文字列で、行の全体を保持していた。空の範囲への挿入でも、長さ 0 の部分文字列が行を保持した。
// 長い行を 1 文字ずつ変えると、行の長さと編集の数の積のメモリをアンドゥの記録が保持し、
// 回数付きの挿入 100000afoo xy <Esc> で server のメモリが 13GB を超えた。
// ここでは 64 KiB の行を 512 回変える。保持していれば 32 MiB 以上が残る。
//
// req: FR-07
func TestApplyDoesNotRetainLines(t *testing.T) {
	const lineLen, edits = 64 << 10, 512
	tests := []struct {
		name string
		edit func(i int) Edit
	}{
		// 行末への 1 バイトの挿入（逆の編集は空のテキスト）。
		{"insert", func(i int) Edit { return Edit{Range: rng(0, lineLen+i, 0, lineLen+i), Text: "b"} }},
		// 行の途中の 1 バイトの削除（逆の編集は消した 1 バイト）。行末で消すと、残る行が前の行の部分文字列になり、写し直さない。
		{"delete", func(int) Edit { return Edit{Range: rng(0, 100, 0, 101)} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(strings.Repeat("a", lineLen) + "\n")
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			for i := range edits {
				if _, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: []Edit{tt.edit(i)}}); err != nil {
					t.Fatal(err)
				}
			}
			runtime.GC()
			runtime.ReadMemStats(&after)
			if after.HeapAlloc > before.HeapAlloc+8<<20 {
				t.Errorf("%d edits on a %d-byte line retain %d bytes", edits, lineLen, after.HeapAlloc-before.HeapAlloc)
			}
			for range edits {
				if _, ok := b.Undo(); !ok {
					t.Fatal("Undo failed")
				}
			}
			if got := b.Line(0); got != strings.Repeat("a", lineLen) {
				t.Errorf("after undo: line has %d bytes", len(got))
			}
		})
	}
}
