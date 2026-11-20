package buffer

import (
	"runtime"
	"strings"
	"testing"
)

// 編集の後に残る文字列（アンドゥの記録の逆の編集のテキストと、作り直した後の行）は、編集の前の行を参照しない（#279）。
// 前は、逆の編集のテキストが編集の前の行の部分文字列で、行の全体を保持していた。空の範囲への挿入でも、長さ 0 の部分文字列が行を保持した。
// 長い行を 1 文字ずつ変えると、行の長さと編集の数の積のメモリをアンドゥの記録が保持し、
// 回数付きの挿入 100000afoo xy <Esc> で server のメモリが 13GB を超えた。
// 改行を入れる編集では、分けた行のどれもが、前の行と入れたテキストをつないだ全体を指していた。
// 後で一方の行を作り直しても、もう一方（空の行や短い行）が全体を保持した（PR #285 のレビュー。20000 バイトの行の先頭で 20000i<CR><Esc> を流すと 393 MB が残った）。
// ここでは 64 KiB の行に 512 回の編集を行う。保持していれば 32 MiB 以上が残る。
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
		// 長い行の先頭への改行の挿入を、分けた後の下の行（長い行）の先頭へ続ける。上に空の行が並ぶ。
		{"newline", func(i int) Edit { return Edit{Range: rng(i, 0, i, 0), Text: "\n"} }},
		// 長い行の先頭へ 1 文字を入れ、その後ろで行を分けることを続ける（<count>ix<CR><Esc>）。上に 1 文字の行が並ぶ。
		// 分けた上の行は、前の行の部分文字列とテキストの最初の行（空）の連結なので、+ でつなぐと前の行をそのまま指す。
		{"char and newline", func(i int) Edit {
			if i%2 == 0 {
				return Edit{Range: rng(i/2, 0, i/2, 0), Text: "x"}
			}
			return Edit{Range: rng(i/2, 1, i/2, 1), Text: "\n"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := strings.Repeat("a", lineLen)
			b := New(text + "\n")
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
			if got := b.Text(); got != text {
				t.Errorf("after undo: text has %d bytes in %d lines", len(got), b.LineCount())
			}
		})
	}
}

// Slice が返すテキストは、バッファの行を参照しない（PR #285 のレビュー）。
// 前は 1 行の範囲なら行の部分文字列を返したので、長い行から 1 文字をヤンクしたレジスターの履歴が、ヤンクした時点の行の全体を保持した。
// 履歴の 1 件の上限（FR-53）はテキストの長さで比べるので、1 文字のヤンクは上限に掛からない（1 MiB の行で ylx を 100 回流すと 78 MB が残った）。
// ここでは 64 KiB の行の 1 バイトを読んで持ち、行の途中の 1 バイトを消して行を作り直すことを 512 回続ける。
func TestSliceDoesNotRetainLines(t *testing.T) {
	const lineLen, reads = 64 << 10, 512
	b := New(strings.Repeat("a", lineLen) + "\n")
	kept := make([]string, 0, reads)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for range reads {
		s, err := b.Slice(rng(0, 200, 0, 201))
		if err != nil {
			t.Fatal(err)
		}
		kept = append(kept, s)
		if _, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(0, 100, 0, 101)}}}); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+8<<20 {
		t.Errorf("%d one-byte slices of a %d-byte line retain %d bytes", reads, lineLen, after.HeapAlloc-before.HeapAlloc)
	}
	for _, s := range kept {
		if s != "a" {
			t.Fatalf("Slice = %q, want %q", s, "a")
		}
	}
}
