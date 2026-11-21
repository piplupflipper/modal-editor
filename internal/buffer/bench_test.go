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

// historyBuffer は、NFR-05 の計測と同じ 10,000 行のテキストに、残すアンドゥの履歴の大きさを測るための編集を重ねたバッファを返す（#319）。
//
//   - 1,000 回の 1 文字の挿入（それぞれ 1 単位。インサートモードの入力を 1,000 回抜けたのに当たる）
//   - 全ての行を 1 単位で書き換える編集（:%s/^/x/ に当たる。逆の編集が 10,000 行の全体を持つ）
//   - 10,000 行の全体を置き換える読み直し（autoread で外の全体の書き換えを読み直したのに当たる）
func historyBuffer(tb testing.TB) *Buffer {
	tb.Helper()
	lines := make([]string, 10000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %05d: the quick brown fox jumps over the lazy dog", i)
	}
	b := New(strings.Join(lines, "\n") + "\n")
	for i := range 1000 {
		tx := Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(i*10, 0, i*10, 0), Text: "y"}}}
		if _, err := b.Apply(tx); err != nil {
			tb.Fatal(err)
		}
	}
	b.BeginUndoGroup()
	for i := range b.LineCount() {
		tx := Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(i, 0, i, 0), Text: "x"}}}
		if _, err := b.Apply(tx); err != nil {
			tb.Fatal(err)
		}
	}
	b.EndUndoGroup()
	last := b.LineCount() - 1
	whole := Range{End: Position{Line: last, Byte: len(b.Line(last))}}
	if _, err := b.ApplyReload(Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: whole, Text: strings.Join(lines, "\n")}}}, true); err != nil {
		tb.Fatal(err)
	}
	return b
}

// BenchmarkMarshalHistory10k は、historyBuffer の履歴をバイト列にする時間と、その大きさ（bytes/history）を測る（FR-165。#319）。
func BenchmarkMarshalHistory10k(b *testing.B) {
	buf := historyBuffer(b)
	var n int
	for b.Loop() {
		n = len(buf.MarshalHistory())
	}
	b.ReportMetric(float64(n), "bytes/history")
}

// BenchmarkRestoreHistory10k は、historyBuffer の履歴を、開き直したバッファへ読み戻す時間（確かめる時間を含む）を測る（FR-165。#319）。
func BenchmarkRestoreHistory10k(b *testing.B) {
	buf := historyBuffer(b)
	data := buf.MarshalHistory()
	var sb strings.Builder
	if _, err := buf.WriteTo(&sb); err != nil {
		b.Fatal(err)
	}
	text := sb.String()
	for b.Loop() {
		b.StopTimer()
		got := New(text)
		b.StartTimer()
		if err := got.RestoreHistory(data); err != nil {
			b.Fatal(err)
		}
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
