package diff

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// readText は、testdata のファイルを Text にする。
func readText(t *testing.T, name string) Text {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return Parse(string(b))
}

// TestComputeRefineEditorGo は、日常の大きさの変更で塊が粗くならないことを確かめる（#228 のレビューの必須 2）。
// 入力は、このリポジトリの 9c67b40 の internal/editor/editor.go の前と後の版（921 行と 928 行）である。
// 改名（#197）で、両方の版の import のモジュールパスを同じように置き換えた。行の数と、行どうしが等しいかは変わらない。
// 計算し直し（refine）が無いと、lcs の打ち切りで 467 行の塊ができ、塊の行は 989 行になった。
//
// 期待値は、2026-11-19 に手元の Vim 9.1（patches 1-16）の vim -d（diffopt=internal,filler）で数えたもので、
// 塊は 41 個、色の付く行（diff_hlID が 0 でない行）は両側で 97 行と 104 行だった（git diff --no-index でも 201 行）。
// 塊の選び方は Vim（xdiff）と違いうるので、塊の行の数が Vim の 1.5 倍以下で、最大の塊が 40 行以下であることを確かめる。
// あわせて、Vim で塊に入らない左の 282 行目（1 から数える。choiceDropped の欄）で do と dp の対象が無いことと、
// Vim の各塊の始まりの行で do（HunkAt の OpGet）が取り込む行の数が、Vim の塊と同じ程度（40 行以下）であることを確かめる。
func TestComputeRefineEditorGo(t *testing.T) {
	a, b := readText(t, "editor-9c67b40-before.txt"), readText(t, "editor-9c67b40-after.txt")
	r := Compute(a, b)
	if got := applyHunks(r); !slices.Equal(got, b.Lines) {
		t.Fatal("塊の通りに置き換えた結果が b と違う")
	}
	lines, largest := 0, 0
	for _, h := range r.Hunks {
		n := h.A.Len() + h.B.Len()
		lines += n
		largest = max(largest, n)
	}
	t.Logf("塊 %d 個、塊の行 %d 行、最大の塊 %d 行（Vim 9.1 は 41 個、201 行）", len(r.Hunks), lines, largest)
	if lines > 201*3/2 {
		t.Errorf("塊の行 = %d, want 301 以下", lines)
	}
	if largest > 40 {
		t.Errorf("最大の塊 = %d 行, want 40 以下", largest)
	}
	for _, op := range []Op{OpGet, OpPut} {
		if i, ok := r.HunkAt(A, 281, op); ok {
			t.Errorf("282 行目の %v の対象 = %+v, want 無し（Vim では塊の外）", op, r.Hunks[i])
		}
	}
	vimStarts := []int{
		151, 178, 179, 187, 197, 198, 200, 220, 251, 268, 293, 301, 382, 385, 390, 394, 419, 421, 435, 555,
		565, 573, 587, 591, 611, 631, 633, 636, 638, 639, 644, 693, 705, 707, 710, 711, 734, 735, 784, 803, 874,
	}
	for _, line := range vimStarts {
		i, ok := r.HunkAt(A, line-1, OpGet)
		if !ok {
			continue // 塊の位置が Vim と違い、この行が塊の外になった
		}
		if n := r.Hunks[i].A.Len() + r.Hunks[i].B.Len(); n > 40 {
			t.Errorf("%d 行目の do が取り込む塊 = %d 行（%+v）, want 40 以下", line, n, r.Hunks[i])
		}
	}
}
