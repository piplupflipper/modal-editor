package buffer

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// SetUndoCursor は最後のアンドゥの単位に最初の 1 回だけ記録し、Undo と Redo の後に LastUndo で返る。
// グループの中の 2 回目以降の Apply の後に呼んでも、最初の記録が残る。
//
// req: FR-07
func TestSetUndoCursor(t *testing.T) {
	b := New("abc\ndef\n")
	b.SetUndoCursor(pos(9, 9)) // 単位が無いので何もしない
	b.BeginUndoGroup()
	mustApply(t, b, Edit{Range: rng(1, 1, 1, 1), Text: "x"})
	b.SetUndoCursor(pos(1, 1))
	mustApply(t, b, Edit{Range: rng(1, 2, 1, 2), Text: "y"})
	b.SetUndoCursor(pos(1, 2))
	b.EndUndoGroup()
	mustApply(t, b, Edit{Range: rng(0, 0, 0, 1), Text: ""}) // 記録しない単位

	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() = false")
	}
	if got := b.LastUndo(); got.HasCursor || got.Top != 0 || got.Lines != 1 {
		t.Errorf("記録の無い単位の LastUndo() = %+v", got)
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() = false")
	}
	want := UndoInfo{Cursor: pos(1, 1), HasCursor: true, Top: 1, Lines: 1}
	if got := b.LastUndo(); got != want {
		t.Errorf("Undo の後の LastUndo() = %+v, want %+v", got, want)
	}
	if _, ok := b.Undo(); ok {
		t.Fatal("履歴の先頭で Undo() = true")
	}
	if got := b.LastUndo(); got != want {
		t.Errorf("失敗した Undo の後の LastUndo() = %+v, want %+v（前の情報のまま）", got, want)
	}
	if _, ok := b.Redo(); !ok {
		t.Fatal("Redo() = false")
	}
	if got := b.LastUndo(); got != want {
		t.Errorf("Redo の後の LastUndo() = %+v, want %+v", got, want)
	}
}

// LastUndo の変わった行の範囲（Top と Lines）は、取り消す前と後のテキストを行で比べ、
// 先頭と末尾の同じ行を除いた範囲に一致する。行の挿入、削除、複数の編集を含むトランザクションで確かめる。
//
// req: FR-07
func TestLastUndoLines(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		edits     []Edit
		top, n    int // 取り消した後の範囲
		rtop, rn  int // やり直した後の範囲
		sameLines bool
	}{
		{name: "行の中", text: "a\nbc\nd\n", edits: []Edit{{Range: rng(1, 1, 1, 2), Text: "X"}}, top: 1, n: 1, rtop: 1, rn: 1},
		{name: "o に当たる行末への行の挿入", text: "a\nb\n", edits: []Edit{{Range: rng(0, 1, 0, 1), Text: "\nx"}}, top: 1, n: 0, rtop: 1, rn: 1},
		{name: "dd に当たる行の削除", text: "a\nb\nc\n", edits: []Edit{{Range: rng(1, 0, 2, 0)}}, top: 1, n: 1, rtop: 1, rn: 0},
		{name: "最後の行の削除", text: "a\nb\n", edits: []Edit{{Range: rng(0, 1, 1, 1)}}, top: 1, n: 1, rtop: 1, rn: 0},
		{
			name: "離れた 2 か所", text: "a\nb\nc\nd\ne\n",
			edits: []Edit{{Range: rng(3, 0, 3, 1), Text: "D\nD2"}, {Range: rng(1, 0, 1, 1), Text: "B"}},
			top:   1, n: 3, rtop: 1, rn: 4,
		},
		{name: "同じテキストへの置き換え", text: "a\nb\n", edits: []Edit{{Range: rng(1, 0, 1, 1), Text: "b"}}, top: 1, n: 0, rtop: 1, rn: 0, sameLines: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.text)
			mustApply(t, b, tc.edits...)
			b.Undo()
			if got := b.LastUndo(); got.Top != tc.top || got.Lines != tc.n {
				t.Errorf("Undo: Top, Lines = %d, %d, want %d, %d", got.Top, got.Lines, tc.top, tc.n)
			}
			b.Redo()
			if got := b.LastUndo(); got.Top != tc.rtop || got.Lines != tc.rn {
				t.Errorf("Redo: Top, Lines = %d, %d, want %d, %d", got.Top, got.Lines, tc.rtop, tc.rn)
			}
		})
	}
}

// 乱数で作ったトランザクションを取り消し、やり直したときの Top と Lines が、
// テキスト全体を行で比べた結果（先頭と末尾の同じ行を除いた範囲）と同じ大きさで、範囲の外の行が変わっていない。
//
// req: FR-07
func TestLastUndoLinesRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(79, 1)) //nolint:gosec // 再現できる乱数で入力を作るテストで、暗号の用途ではない。
	alphabet := []string{"a", "b", "\n", "\n", "x"}
	randText := func(n int) string {
		var sb strings.Builder
		for range n {
			sb.WriteString(alphabet[r.IntN(len(alphabet))])
		}
		return sb.String()
	}
	for i := range 3000 {
		b := New(randText(r.IntN(12)) + "\n")
		var edits []Edit
		cur := New(b.Text() + "\n")
		for range 1 + r.IntN(3) {
			p1 := randPos(r, cur)
			p2 := randPos(r, cur)
			if p2.Less(p1) {
				p1, p2 = p2, p1
			}
			e := Edit{Range: Range{Start: p1, End: p2}, Text: randText(r.IntN(4))}
			mustApply(t, cur, e)
			edits = append(edits, e)
		}
		mustApply(t, b, edits...)
		for _, undo := range []bool{true, false} {
			before := b.Text()
			if undo {
				b.Undo()
			} else {
				b.Redo()
			}
			after := b.Text()
			if before == after {
				continue
			}
			bl, al := strings.Split(before, "\n"), strings.Split(after, "\n")
			_, n := lineDiff(bl, al)
			got := b.LastUndo()
			// 同じ行が続くところでは、どこを変わった範囲と見るかに幅がある（"\n\n" から "\n" への変更など）。
			// 範囲の外の行が前と後で同じであることと、範囲の行の数が最小であることを確かめる。
			old := got.Lines + len(bl) - len(al)
			ok := got.Lines == n && old >= 0 && got.Top+got.Lines <= len(al) &&
				slices.Equal(bl[:got.Top], al[:got.Top]) && slices.Equal(bl[got.Top+old:], al[got.Top+got.Lines:])
			if !ok {
				t.Fatalf("%d: undo=%v %q -> %q（編集 %+v）: Top, Lines = %d, %d, want Lines %d",
					i, undo, before, after, edits, got.Top, got.Lines, n)
			}
		}
	}
}

// lineDiff は、before と after の先頭と末尾の同じ行を除いた範囲の、after での始まりと行の数を返す。
// 先頭の同じ行を先に、できるだけ多く除く。
func lineDiff(before, after []string) (top, n int) {
	for top < len(before) && top < len(after) && before[top] == after[top] {
		top++
	}
	tail := 0
	for tail < len(before)-top && tail < len(after)-top && before[len(before)-1-tail] == after[len(after)-1-tail] {
		tail++
	}
	return top, len(after) - top - tail
}

// randPos は、バッファの中の位置を乱数で選ぶ（テキストは 1 バイトの文字だけなので、どのバイトも文字の境界である）。
func randPos(r *rand.Rand, b *Buffer) Position {
	l := r.IntN(b.LineCount())
	return pos(l, r.IntN(len(b.Line(l))+1))
}

// mustApply は、edits を 1 つのトランザクションとして適用する。
func mustApply(t *testing.T, b *Buffer, edits ...Edit) {
	t.Helper()
	if _, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: edits}); err != nil {
		t.Fatal(err)
	}
}
