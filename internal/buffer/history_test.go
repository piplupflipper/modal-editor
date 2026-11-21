package buffer

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// fileText は、b を書き出したときのファイルの中身を返す。
func fileText(t *testing.T, b *Buffer) string {
	t.Helper()
	var sb strings.Builder
	if _, err := b.WriteTo(&sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// reopen は、b を書き出したファイルを開き直したバッファへ、b の履歴を読み戻す（FR-165 の保存して開き直す流れ）。
func reopen(t *testing.T, b *Buffer) *Buffer {
	t.Helper()
	data := b.MarshalHistory()
	got := New(fileText(t, b))
	if err := got.RestoreHistory(data); err != nil {
		t.Fatalf("RestoreHistory: %v", err)
	}
	return got
}

// snapshot は、テキストと、書き出したときの中身と、行の無いバッファかを並べたものである。
type snapshot struct {
	text, file string
	noLines    bool
	info       UndoInfo
}

func snap(t *testing.T, b *Buffer) snapshot {
	t.Helper()
	return snapshot{text: b.Text(), file: fileText(t, b), noLines: b.NoLines(), info: b.LastUndo()}
}

// walk は、b のアンドゥを先頭まで、続けてリドゥを末尾まで行い、各時点のスナップショットを返す。
func walk(t *testing.T, b *Buffer) []snapshot {
	t.Helper()
	var out []snapshot
	for {
		if _, ok := b.Undo(); !ok {
			break
		}
		out = append(out, snap(t, b))
	}
	for {
		if _, ok := b.Redo(); !ok {
			break
		}
		out = append(out, snap(t, b))
	}
	return out
}

// 保存して開き直したバッファへ履歴を読み戻すと、アンドゥとリドゥで、元のバッファと同じテキスト、書き出す中身、
// 行の無いバッファの記録、カーソルの記録をたどる。リドゥの履歴（u の後に保存したもの）も残る。
// 不正な UTF-8 のバイトと、末尾の改行の記録を変える単位（ApplyReload）を含めて確かめる。
//
// req: FR-165
func TestHistoryRoundTrip(t *testing.T) {
	b := New("abc\n\xff\xfe def\nghi")
	b.BeginUndoGroup()
	mustApply(t, b, Edit{Range: rng(0, 1, 0, 1), Text: "X"})
	b.SetUndoCursor(pos(0, 1))
	mustApply(t, b, Edit{Range: rng(0, 2, 0, 2), Text: "\x80Y\n"})
	b.EndUndoGroup()
	mustApply(t, b, Edit{Range: rng(2, 0, 2, 3), Text: ""}, Edit{Range: rng(1, 0, 1, 0), Text: "日本\xc3"})
	b.SetUndoCursor(pos(2, 1))
	if _, err := b.ApplyReload(Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(3, 0, 3, 3), Text: "GHI"}}}, true); err != nil {
		t.Fatal(err)
	}
	// 全ての行を消して行の無いバッファにする。
	mustApply(t, b, Edit{Range: Range{Start: pos(0, 0), End: pos(b.LineCount()-1, len(b.Line(b.LineCount()-1)))}, Text: ""})
	b.MarkNoLines()
	mustApply(t, b, Edit{Range: rng(0, 0, 0, 0), Text: "new"})
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() = false")
	}
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo() = false")
	}
	b.MarkSaved()

	got := reopen(t, b)
	if got.Modified() {
		t.Error("読み戻した直後の Modified() = true")
	}
	if !got.CanUndo() || !got.CanRedo() {
		t.Errorf("CanUndo() = %v, CanRedo() = %v, want true, true", got.CanUndo(), got.CanRedo())
	}
	want := walk(t, b)
	if g := walk(t, got); !slices.Equal(g, want) {
		t.Errorf("読み戻した履歴のたどり方が違う\n got %+v\nwant %+v", g, want)
	}
}

// 空のファイル（行の無いバッファ）を読み戻しても、今の位置は行の無いバッファのままで、書き出すと空のファイルになる。
//
// req: FR-165
func TestHistoryRestoreKeepsNoLines(t *testing.T) {
	b := New("a\n")
	mustApply(t, b, Edit{Range: rng(0, 0, 0, 1), Text: ""})
	b.MarkNoLines()
	got := reopen(t, b)
	if !got.NoLines() || fileText(t, got) != "" {
		t.Errorf("NoLines() = %v, 中身 %q, want true, \"\"", got.NoLines(), fileText(t, got))
	}
	if _, ok := got.Undo(); !ok || got.Text() != "a" {
		t.Errorf("Undo の後のテキスト %q, want \"a\"", got.Text())
	}
}

// 読み戻した後の編集は、読み戻した単位と違う識別子の単位になり、保存した時点へアンドゥで戻ると Modified が false に戻る。
//
// req: FR-165
func TestHistoryRestoreThenEdit(t *testing.T) {
	b := New("x\n")
	mustApply(t, b, Edit{Range: rng(0, 1, 0, 1), Text: "1"})
	mustApply(t, b, Edit{Range: rng(0, 2, 0, 2), Text: "2"})
	got := reopen(t, b)
	mustApply(t, got, Edit{Range: rng(0, 3, 0, 3), Text: "3"})
	if !got.Modified() {
		t.Error("編集の後の Modified() = false")
	}
	got.Undo()
	if got.Modified() || got.Text() != "x12" {
		t.Errorf("Modified() = %v, テキスト %q, want false, \"x12\"", got.Modified(), got.Text())
	}
	got.Undo()
	got.Undo()
	if got.Text() != "x" || got.CanUndo() {
		t.Errorf("テキスト %q, CanUndo() = %v, want \"x\", false", got.Text(), got.CanUndo())
	}
}

// 乱数の編集、グループ、アンドゥ、リドゥを重ねた履歴も、保存して読み戻すと同じたどり方をする。
//
// req: FR-165
func TestHistoryRoundTripRandom(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // 再現できる乱数で入力を作るテストで、暗号の用途ではない。
	alphabet := []string{"a", "b", "\n", "é", "\xff", ""}
	for range 50 {
		b := New("start\nline\n")
		for range 40 {
			switch r.IntN(6) {
			case 0:
				b.Undo()
			case 1:
				b.Redo()
			case 2:
				b.BeginUndoGroup()
			case 3:
				b.EndUndoGroup()
			default:
				l := r.IntN(b.LineCount())
				p := Position{Line: l, Byte: len(b.Line(l))}
				var sb strings.Builder
				for range r.IntN(4) {
					sb.WriteString(alphabet[r.IntN(len(alphabet))])
				}
				mustApply(t, b, Edit{Range: Range{Start: Position{Line: l}, End: p}, Text: sb.String()})
				b.SetUndoCursor(Position{Line: l})
			}
		}
		b.MarkSaved()
		got := reopen(t, b)
		if g, w := walk(t, got), walk(t, b); !slices.Equal(g, w) {
			t.Fatalf("読み戻した履歴のたどり方が違う\n got %+v\nwant %+v", g, w)
		}
	}
}

// 形式の版が違うバイト列、途中で切れたバイト列、余計なバイトのあるバイト列は ErrHistoryFormat で断り、バッファを変えない。
//
// req: FR-165
func TestHistoryRestoreFormat(t *testing.T) {
	b := New("abc\n")
	mustApply(t, b, Edit{Range: rng(0, 0, 0, 1), Text: "X"})
	data := b.MarshalHistory()

	other := slices.Clone(data)
	other[0] = historyFormat + 1
	tests := map[string][]byte{
		"版が違う":     other,
		"空":        nil,
		"途中で切れた":   data[:len(data)-1],
		"余計なバイト":   append(slices.Clone(data), 0),
		"大きな長さ":    {historyFormat, 1, 0, 0xff, 0xff, 0xff, 0xff, 0x0f},
		"範囲の外の識別子": {historyFormat, 0, 0, 1, 5, 0, 0, 0, 0, 0},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			got := New(fileText(t, b))
			if err := got.RestoreHistory(in); !errors.Is(err, ErrHistoryFormat) {
				t.Errorf("RestoreHistory = %v, want ErrHistoryFormat", err)
			}
			if got.CanUndo() || got.CanRedo() || got.Modified() {
				t.Errorf("断った後に履歴がある（CanUndo %v、CanRedo %v、Modified %v）", got.CanUndo(), got.CanRedo(), got.Modified())
			}
		})
	}
}

// 履歴が今のテキストに当てはまらなければ ErrHistoryMismatch で断り、バッファを変えない（アンドゥで panic しない）。
// 履歴を既に持つバッファには ErrHistoryNotEmpty を返す。
//
// req: FR-165
func TestHistoryRestoreMismatch(t *testing.T) {
	b := New("abc\ndef\n")
	mustApply(t, b, Edit{Range: rng(1, 0, 1, 3), Text: "long line"})
	data := b.MarshalHistory()

	// 行の後ろに行を足したテキスト（"abc\nlong line\nmore\n"）は、履歴の編集がそのまま当てはまるので断れない。
	// ファイルが外で変わったかは、呼び出し側が中身のハッシュで見分ける（FR-165）。
	for _, text := range []string{"abc\n", "abc\nxyz\n", "abc\nlong\n"} {
		got := New(text)
		if err := got.RestoreHistory(data); !errors.Is(err, ErrHistoryMismatch) {
			t.Errorf("%q: RestoreHistory = %v, want ErrHistoryMismatch", text, err)
		}
		if got.CanUndo() {
			t.Errorf("%q: 断った後に CanUndo() = true", text)
		}
	}

	busy := New("abc\nlong line\n")
	mustApply(t, busy, Edit{Range: rng(0, 0, 0, 0), Text: "x"})
	busy.Undo()
	if err := busy.RestoreHistory(data); !errors.Is(err, ErrHistoryNotEmpty) {
		t.Errorf("履歴のあるバッファへの RestoreHistory = %v, want ErrHistoryNotEmpty", err)
	}
}
