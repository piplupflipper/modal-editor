package buffer

import (
	"fmt"
	"strings"
	"testing"
)

// globalDeleteLines は、n 行のバッファを作り、1 つのアンドゥのグループの中で偶数番目の行を 1 行ずつ Apply で消す。
// `:g/pat/d` で半分の行が当たったときと同じ呼び出し方である（#29）。
// 消した後のバッファを返す。
func globalDeleteLines(tb testing.TB, n int) *Buffer {
	tb.Helper()
	lines := make([]string, n)
	for i := range lines {
		if i%2 == 0 {
			lines[i] = fmt.Sprintf("line %d", i)
		} else {
			lines[i] = fmt.Sprintf("keep %d", i)
		}
	}
	b := New(strings.Join(lines, "\n") + "\n")
	b.BeginUndoGroup()
	// 偶数番目の行を消すと後ろの行が 1 つ詰まるので、k 回目に消す行は k 番目になる。
	for k := range n / 2 {
		tx := Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(k, 0, k+1, 0)}}}
		if _, err := b.Apply(tx); err != nil {
			tb.Fatal(err)
		}
	}
	b.EndUndoGroup()
	return b
}

func TestGlobalDeleteLinesUndo(t *testing.T) {
	const n = 1000
	b := globalDeleteLines(t, n)
	if got := b.LineCount(); got != n/2 {
		t.Fatalf("LineCount() = %d, want %d", got, n/2)
	}
	for i := range b.LineCount() {
		if want := fmt.Sprintf("keep %d", 2*i+1); b.Line(i) != want {
			t.Fatalf("Line(%d) = %q, want %q", i, b.Line(i), want)
		}
	}
	after := b.Text()
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() = false")
	}
	if got := b.LineCount(); got != n {
		t.Fatalf("after Undo: LineCount() = %d, want %d", got, n)
	}
	if b.CanUndo() {
		t.Fatal("group was not recorded as a single undo step")
	}
	if _, ok := b.Redo(); !ok {
		t.Fatal("Redo() = false")
	}
	if got := b.Text(); got != after {
		t.Fatal("after Redo: text differs from the text before Undo")
	}
}

// BenchmarkGlobalDelete50k は、5 万行のバッファで `:g/pat/d` 相当（1 グループの中で 2.5 万回の行削除）を測る（#29）。
func BenchmarkGlobalDelete50k(b *testing.B) {
	for b.Loop() {
		globalDeleteLines(b, 50000)
	}
}

// BenchmarkGlobalDelete50kUndo は、BenchmarkGlobalDelete50k でまとめた 1 単位を Undo で戻す時間を測る。
func BenchmarkGlobalDelete50kUndo(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		buf := globalDeleteLines(b, 50000)
		b.StartTimer()
		if _, ok := buf.Undo(); !ok {
			b.Fatal("Undo() = false")
		}
	}
}
