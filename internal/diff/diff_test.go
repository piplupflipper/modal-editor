package diff

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

func TestParse(t *testing.T) {
	tests := []struct {
		content string
		want    []string
	}{
		{"", nil},
		{"\n", []string{""}},
		{"a\nb\n", []string{"a", "b"}},
		{"a\nb", []string{"a", "b"}},
		{"a\n\n", []string{"a", ""}},
	}
	for _, tt := range tests {
		if got := Parse(tt.content).Lines; !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q).Lines = %#v, want %#v", tt.content, got, tt.want)
		}
	}
}

func TestFromBuffer(t *testing.T) {
	for _, content := range []string{"", "\n", "a\nb\n", "a\nb"} {
		got := FromBuffer(buffer.New(content)).Lines
		if want := Parse(content).Lines; !reflect.DeepEqual(got, want) {
			t.Errorf("FromBuffer(%q).Lines = %#v, want %#v", content, got, want)
		}
	}
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want []Hunk
	}{
		{"同じ", "a\nb\n", "a\nb\n", nil},
		{"変えた行", "a\nb\nc\n", "a\nx\nc\n", []Hunk{{Span{1, 2}, Span{1, 2}}}},
		{"足した行", "a\nc\n", "a\nb\nc\n", []Hunk{{Span{1, 1}, Span{1, 2}}}},
		{"消した行", "a\nb\nc\n", "a\nc\n", []Hunk{{Span{1, 2}, Span{1, 1}}}},
		// Vim 9.1 と同じく、末尾の改行の有無だけの違いは出さない（パッケージの doc）。
		{"末尾の改行の有無", "a\nb\n", "a\nb", nil},
		{"空のファイル", "", "a\n", []Hunk{{Span{0, 0}, Span{0, 1}}}},
		{"末尾へ足した行", "a\n", "a\nb\n", []Hunk{{Span{1, 1}, Span{1, 2}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Compute(Parse(tt.a), Parse(tt.b))
			if !reflect.DeepEqual(r.Hunks, tt.want) {
				t.Errorf("Hunks = %v, want %v", r.Hunks, tt.want)
			}
		})
	}
}

func TestKind(t *testing.T) {
	if k := (Hunk{Span{1, 1}, Span{1, 2}}).Kind(); k != Add {
		t.Errorf("Kind = %v, want Add", k)
	}
	if k := (Hunk{Span{1, 2}, Span{1, 1}}).Kind(); k != Delete {
		t.Errorf("Kind = %v, want Delete", k)
	}
	if k := (Hunk{Span{1, 2}, Span{1, 3}}).Kind(); k != Change {
		t.Errorf("Kind = %v, want Change", k)
	}
}

// applyHunks は、r の塊の通りに A の側の行を置き換えた結果の行を返す。
func applyHunks(r *Result) []string {
	a, b := r.Text(A), r.Text(B)
	lines := []string{}
	prev := 0
	for _, h := range r.Hunks {
		lines = append(lines, a.Lines[prev:h.A.Start]...)
		lines = append(lines, b.Lines[h.B.Start:h.B.End]...)
		prev = h.A.End
	}
	return append(lines, a.Lines[prev:]...)
}

// randomText は、少ない種類の行から n 行のテキストを作る。
func randomText(rng *rand.Rand, n int) string {
	var sb strings.Builder
	for range n {
		sb.WriteByte("abcd"[rng.IntN(4)])
		sb.WriteString("\n")
	}
	return sb.String()
}

var normalDiffHeader = regexp.MustCompile(`^(\d+)(?:,(\d+))?([acd])(\d+)(?:,(\d+))?$`)

// gnuDiff は、diff コマンドの通常の形式の出力から、消した行と足した行の数を数える。
func gnuDiff(t *testing.T, dir, a, b string) (deleted, added int) {
	t.Helper()
	pa, pb := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(pa, []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pb, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("diff", "--minimal", pa, pb).Output() //nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	if err != nil && len(out) == 0 && a != b {
		t.Fatalf("diff: %v", err)
	}
	count := func(from, to string) int {
		if to == "" {
			return 1
		}
		x, _ := strconv.Atoi(from)
		y, _ := strconv.Atoi(to)
		return y - x + 1
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		m := normalDiffHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[3] != "a" {
			deleted += count(m[1], m[2])
		}
		if m[3] != "d" {
			added += count(m[4], m[5])
		}
	}
	return deleted, added
}

// TestComputeAgainstDiffCommand は、乱数で作ったテキストの組で、塊の通りに置き換えると a が b になることと、
// 消した行と足した行の数が diff --minimal（最小の差分）と同じことを確かめる。
// 違いが少ない（lcs の探索の上限に届かない）大きさのテキストに限る。
// テキストは全て末尾の改行を持たせる（diff コマンドは末尾の改行の有無を違いとして出すため）。
func TestComputeAgainstDiffCommand(t *testing.T) {
	if _, err := exec.LookPath("diff"); err != nil {
		t.Skip("diff コマンドが無い")
	}
	dir := t.TempDir()
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // 再現できる無作為の入力が要る。暗号の用途ではない。
	for i := range 300 {
		a := randomText(rng, rng.IntN(15))
		b := randomText(rng, rng.IntN(15))
		r := Compute(Parse(a), Parse(b))
		if got := applyHunks(r); !slices.Equal(got, Parse(b).Lines) {
			t.Fatalf("#%d: 塊の通りに置き換えた結果 %q, want %q (a = %q)", i, got, b, a)
		}
		var deleted, added int
		for _, h := range r.Hunks {
			deleted += h.A.Len()
			added += h.B.Len()
		}
		wd, wa := gnuDiff(t, dir, a, b)
		if deleted != wd || added != wa {
			t.Errorf("#%d: a = %q, b = %q: 消した行 %d、足した行 %d, diff は %d、%d", i, a, b, deleted, added, wd, wa)
		}
	}
}

func TestNextPrev(t *testing.T) {
	// A: a b c d e、B: a x c e f。塊は [1,2)→[1,2)、[3,4)→[3,3)、[5,5)→[4,5)。
	r := Compute(Parse("a\nb\nc\nd\ne\n"), Parse("a\nx\nc\ne\nf\n"))
	want := []Hunk{{Span{1, 2}, Span{1, 2}}, {Span{3, 4}, Span{3, 3}}, {Span{5, 5}, Span{4, 5}}}
	if !reflect.DeepEqual(r.Hunks, want) {
		t.Fatalf("Hunks = %v, want %v", r.Hunks, want)
	}
	type res struct {
		i  int
		ok bool
	}
	call := func(f func(Side, int) (int, bool), s Side, line int) res {
		i, ok := f(s, line)
		return res{i, ok}
	}
	tests := []struct {
		name string
		f    func(Side, int) (int, bool)
		side Side
		line int
		want res
	}{
		{"Next A 0", r.Next, A, 0, res{0, true}},
		{"Next A 1", r.Next, A, 1, res{1, true}},
		{"Next A 3", r.Next, A, 3, res{2, true}},
		{"Next A 4（末尾の空の範囲は最後の行）", r.Next, A, 4, res{0, false}},
		{"Next B 1（空の範囲は次の行）", r.Next, B, 1, res{1, true}},
		{"Next B 3", r.Next, B, 3, res{2, true}},
		{"Prev A 4", r.Prev, A, 4, res{1, true}},
		{"Prev A 1", r.Prev, A, 1, res{0, false}},
		{"Prev B 4", r.Prev, B, 4, res{1, true}},
	}
	for _, tt := range tests {
		if got := call(tt.f, tt.side, tt.line); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

// writeOut は、バッファを書き出した内容を返す。
func writeOut(t *testing.T, b *buffer.Buffer) string {
	t.Helper()
	var sb strings.Builder
	if _, err := b.WriteTo(&sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

// applyPatch は、p をバッファへ適用する。
func applyPatch(t *testing.T, b *buffer.Buffer, p Patch) {
	t.Helper()
	if _, err := b.Apply(buffer.Transaction{BaseVersion: b.Version(), Edits: []buffer.Edit{p.Edit}}); err != nil {
		t.Fatalf("patch %+v: %v", p, err)
	}
}

// TestHunkAtVim は、A の側にカーソルを置いて do（Obtain）か dp（Put）を打った後の両側の内容が、Vim 9.1 と同じになることを確かめる。
// 期待値は、2026-11-18 に手元の Vim 9.1（patches 1-16）で
// vim -N -u NONE -n --not-a-term -d a b -c 'set diffopt=internal,filler' に、カーソルの移動と :diffget か :diffput を流し、
// 両側を :w! で書き出して取った。
// 組は、#185 の 2 回のレビューの組と、最初の行と最後の行、詰め物（この側に無い行）が最初の行の上・途中・最後の行の下にあるもの、
// 変えた行、空のファイルを組み合わせたもので、組ごとに A の側の全ての行で do と dp を打った。
// a と b を入れ替えた組も含めるので、B の側にカーソルを置いた場合も確かめている。
// 塊の位置が Vim（xdiff）と違う組では結果が違いうる（Hunk の doc）が、ここに挙げた組は全て Vim と一致した。
// バッファは buffer.New の既定（'fixendofline' が有効）である。
func TestHunkAtVim(t *testing.T) {
	tests := []struct {
		a, b         string
		line         int // A の側のカーソルの行（0 始まり）
		put          bool
		wantA, wantB string
	}{
		// 1 回目のレビューの組。
		{"a\nb\nc\nd\n", "a\nc\nd\n", 2, false, "a\nc\nd\n", "a\nc\nd\n"},
		{"a\nb\nc\nd\ne\n", "X\nb\nc\nd\nY\n", 1, false, "X\nb\nc\nd\ne\n", "X\nb\nc\nd\nY\n"},
		{"c\nd\n", "d\nd\na\na\nb\n", 1, false, "d\n", "d\nd\na\na\nb\n"},
		{"", "x\ny\n", 0, true, "", "\n"},
		{"a\nb\nc\n", "a\nb\nZ\nd\n", 2, false, "a\nb\nZ\nd\n", "a\nb\nZ\nd\n"},
		// 2 回目のレビューの組と、網羅の組。
		{"p\nq\n", "y\np\nq\nz\n", 0, false, "y\np\nq\n", "y\np\nq\nz\n"},
		{"p\nq\n", "y\np\nq\nz\n", 1, false, "p\nq\n", "y\np\nq\nz\n"},
		{"p\nq\n", "y\np\nq\nz\n", 0, true, "p\nq\n", "p\nq\nz\n"},
		{"p\nq\n", "y\np\nq\nz\n", 1, true, "p\nq\n", "y\np\nq\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 0, false, "p\nq\nr\n", "p\ny\nq\nr\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 1, false, "p\ny\nq\nr\n", "p\ny\nq\nr\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 2, false, "p\nq\nr\n", "p\ny\nq\nr\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 0, true, "p\nq\nr\n", "p\ny\nq\nr\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 1, true, "p\nq\nr\n", "p\nq\nr\nz\n"},
		{"p\nq\nr\n", "p\ny\nq\nr\nz\n", 2, true, "p\nq\nr\n", "p\ny\nq\nr\nz\n"},
		{"a\nd\n", "b\na\nd\nc\n", 0, false, "b\na\nd\n", "b\na\nd\nc\n"},
		{"a\nd\n", "b\na\nd\nc\n", 1, false, "a\nd\n", "b\na\nd\nc\n"},
		{"a\nd\n", "b\na\nd\nc\n", 0, true, "a\nd\n", "a\nd\nc\n"},
		{"a\nd\n", "b\na\nd\nc\n", 1, true, "a\nd\n", "b\na\nd\nc\n"},
		{"p\nq\n", "p\nq\nz\n", 0, false, "p\nq\n", "p\nq\nz\n"},
		{"p\nq\n", "p\nq\nz\n", 1, false, "p\nq\nz\n", "p\nq\nz\n"},
		{"p\nq\n", "p\nq\nz\n", 0, true, "p\nq\n", "p\nq\nz\n"},
		{"p\nq\n", "p\nq\nz\n", 1, true, "p\nq\n", "p\nq\nz\n"},
		{"a\nc\nc\n", "a\n", 0, false, "a\nc\nc\n", "a\n"},
		{"a\nc\nc\n", "a\n", 1, false, "a\n", "a\n"},
		{"a\nc\nc\n", "a\n", 2, false, "a\n", "a\n"},
		{"a\nc\nc\n", "a\n", 0, true, "a\nc\nc\n", "a\n"},
		{"a\nc\nc\n", "a\n", 1, true, "a\nc\nc\n", "a\nc\nc\n"},
		{"a\nc\nc\n", "a\n", 2, true, "a\nc\nc\n", "a\nc\nc\n"},
		{"a\n", "a\nc\nc\n", 0, false, "a\nc\nc\n", "a\nc\nc\n"},
		{"a\n", "a\nc\nc\n", 0, true, "a\n", "a\nc\nc\n"},
		{"b\n", "b\nc\na\nd\nd\n", 0, false, "b\nc\na\nd\nd\n", "b\nc\na\nd\nd\n"},
		{"b\n", "b\nc\na\nd\nd\n", 0, true, "b\n", "b\nc\na\nd\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 0, false, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 1, false, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 2, false, "a\nb\nc\nd\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 0, true, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 1, true, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\nc\nd\n", 2, true, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 0, false, "a\nb\nc\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 1, false, "a\nb\nc\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 2, false, "a\nb\nX\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 3, false, "a\nb\nX\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 0, true, "a\nb\nc\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 1, true, "a\nb\nc\nd\n", "a\nb\nX\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 2, true, "a\nb\nc\nd\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nb\nX\nd\n", 3, true, "a\nb\nc\nd\n", "a\nb\nc\nd\n"},
		{"x\n", "y\n", 0, false, "y\n", "y\n"},
		{"x\n", "y\n", 0, true, "x\n", "x\n"},
		{"a\n", "", 0, false, "\n", ""},
		{"a\n", "", 0, true, "a\n", "a\n"},
		{"", "a\n", 0, false, "a\n", "a\n"},
		{"", "a\n", 0, true, "", "\n"},
		{"a\nb\n", "x\na\nb\n", 0, false, "x\na\nb\n", "x\na\nb\n"},
		{"a\nb\n", "x\na\nb\n", 1, false, "a\nb\n", "x\na\nb\n"},
		{"a\nb\n", "x\na\nb\n", 0, true, "a\nb\n", "a\nb\n"},
		{"a\nb\n", "x\na\nb\n", 1, true, "a\nb\n", "x\na\nb\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 0, false, "a\nb\nc\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 1, false, "a\nX\nc\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 2, false, "a\nX\nc\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 0, true, "a\nb\nc\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 1, true, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nX\nc\nd\n", 2, true, "a\nb\nc\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\n", "a\nb\n", 0, false, "a\nb\nc\n", "a\nb\n"},
		{"a\nb\nc\n", "a\nb\n", 1, false, "a\nb\nc\n", "a\nb\n"},
		{"a\nb\nc\n", "a\nb\n", 2, false, "a\nb\n", "a\nb\n"},
		{"a\nb\nc\n", "a\nb\n", 0, true, "a\nb\nc\n", "a\nb\n"},
		{"a\nb\nc\n", "a\nb\n", 1, true, "a\nb\nc\n", "a\nb\n"},
		{"a\nb\nc\n", "a\nb\n", 2, true, "a\nb\nc\n", "a\nb\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 0, false, "b\nc\n", "b\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 1, false, "b\nc\n", "b\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 2, false, "a\nb\nc\n", "b\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 0, true, "a\nb\nc\n", "a\nb\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 1, true, "a\nb\nc\n", "a\nb\nc\n"},
		{"a\nb\nc\n", "b\nc\n", 2, true, "a\nb\nc\n", "b\nc\n"},
		{"a\nb\n", "a\nx\nb\n", 0, false, "a\nb\n", "a\nx\nb\n"},
		{"a\nb\n", "a\nx\nb\n", 1, false, "a\nx\nb\n", "a\nx\nb\n"},
		{"a\nb\n", "a\nx\nb\n", 0, true, "a\nb\n", "a\nx\nb\n"},
		{"a\nb\n", "a\nx\nb\n", 1, true, "a\nb\n", "a\nb\n"},
		{"y\np\nq\nz\n", "p\nq\n", 0, false, "p\nq\nz\n", "p\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 1, false, "p\nq\nz\n", "p\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 2, false, "y\np\nq\nz\n", "p\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 3, false, "y\np\nq\n", "p\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 0, true, "y\np\nq\nz\n", "y\np\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 1, true, "y\np\nq\nz\n", "y\np\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 2, true, "y\np\nq\nz\n", "p\nq\n"},
		{"y\np\nq\nz\n", "p\nq\n", 3, true, "y\np\nq\nz\n", "p\nq\nz\n"},
		{"p\nq\nz\n", "p\nq\n", 0, false, "p\nq\nz\n", "p\nq\n"},
		{"p\nq\nz\n", "p\nq\n", 1, false, "p\nq\nz\n", "p\nq\n"},
		{"p\nq\nz\n", "p\nq\n", 2, false, "p\nq\n", "p\nq\n"},
		{"p\nq\nz\n", "p\nq\n", 0, true, "p\nq\nz\n", "p\nq\n"},
		{"p\nq\nz\n", "p\nq\n", 1, true, "p\nq\nz\n", "p\nq\n"},
		{"p\nq\nz\n", "p\nq\n", 2, true, "p\nq\nz\n", "p\nq\nz\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 0, false, "a\nb\nc\nd\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 1, false, "a\nX\nc\nd\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 2, false, "a\nX\nc\nd\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 3, false, "a\nb\nc\nd\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 0, true, "a\nb\nc\nd\n", "a\nX\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 1, true, "a\nb\nc\nd\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 2, true, "a\nb\nc\nd\n", "a\nb\nc\nd\n"},
		{"a\nb\nc\nd\n", "a\nX\nc\nd\n", 3, true, "a\nb\nc\nd\n", "a\nX\nc\nd\n"},
	}
	for _, tt := range tests {
		ba, bb := buffer.New(tt.a), buffer.New(tt.b)
		r := Compute(FromBuffer(ba), FromBuffer(bb))
		op := OpGet
		if tt.put {
			op = OpPut
		}
		if i, ok := r.HunkAt(A, tt.line, op); ok {
			if tt.put {
				applyPatch(t, bb, r.Put(i, A))
			} else {
				applyPatch(t, ba, r.Obtain(i, A))
			}
		}
		if ga, gb := writeOut(t, ba), writeOut(t, bb); ga != tt.wantA || gb != tt.wantB {
			t.Errorf("a = %q, b = %q, 行 %d, put = %v: A = %q, B = %q, want %q, %q (塊 %v)",
				tt.a, tt.b, tt.line, tt.put, ga, gb, tt.wantA, tt.wantB, r.Hunks)
		}
	}
}

func TestMapLine(t *testing.T) {
	// A: a b c d e、B: a x y c e f。塊は [1,2)→[1,3)、[3,4)→[4,4)、[5,5)→[5,6)。
	r := Compute(Parse("a\nb\nc\nd\ne\n"), Parse("a\nx\ny\nc\ne\nf\n"))
	wantAB := []int{0, 1, 3, 4, 4}
	for line, want := range wantAB {
		if got := r.MapLine(A, line); got != want {
			t.Errorf("MapLine(A, %d) = %d, want %d", line, got, want)
		}
	}
	wantBA := []int{0, 1, 1, 2, 4, 4}
	for line, want := range wantBA {
		if got := r.MapLine(B, line); got != want {
			t.Errorf("MapLine(B, %d) = %d, want %d", line, got, want)
		}
	}
	// 範囲の外の行は、もう片側の行の範囲に収める。
	if got := r.MapLine(A, 100); got != 5 {
		t.Errorf("MapLine(A, 100) = %d, want 5", got)
	}
	empty := Compute(Parse(""), Parse("a\nb\n"))
	if got := empty.MapLine(B, 1); got != 0 {
		t.Errorf("0 行への MapLine = %d, want 0", got)
	}
}

// applyPatches は、content のバッファに、patches を後ろの塊から順に適用して書き出した内容を返す。
// 後ろから適用すると、前の塊の位置は変わらない。
func applyPatches(t *testing.T, content string, patches []Patch) string {
	t.Helper()
	b := buffer.New(content)
	for i := len(patches) - 1; i >= 0; i-- {
		applyPatch(t, b, patches[i])
	}
	return writeOut(t, b)
}

// TestObtainPut は、全ての塊を do（Obtain）で取り込むと、相手と同じ行になることと、dp（Put）で送ると相手が同じ行になることを、
// 乱数で作ったテキストの組と境界の組で確かめる。編集はバッファ（internal/buffer）に適用する。
// 相手が空のファイルなら、Vim 9.1 と同じく空の行 1 つ（書き出すと "\n"）になる（Patch の doc）。
func TestObtainPut(t *testing.T) {
	pairs := [][2]string{
		{"", "a\n"},
		{"a\n", ""},
		{"a\nb", "a\nb\n"},
		{"a\nb\n", "a\n"},
		{"a\n", "a\nb\nc"},
		{"x\ny\n", "a\nb\n"},
		{"\n", ""},
	}
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // 再現できる無作為の入力が要る。暗号の用途ではない。
	for range 300 {
		pairs = append(pairs, [2]string{randomText(rng, rng.IntN(10)), randomText(rng, rng.IntN(10))})
	}
	for _, pair := range pairs {
		a, b := pair[0], pair[1]
		r := Compute(Parse(a), Parse(b))
		var obtain, put []Patch
		for i := range r.Hunks {
			obtain = append(obtain, r.Obtain(i, A))
			put = append(put, r.Put(i, B))
		}
		// buffer.New の既定は 'fixendofline' が有効なので、書き出すと末尾に改行が付く。
		want := b
		switch {
		case want == "" && len(r.Hunks) > 0:
			want = "\n"
		case want == "" || strings.HasSuffix(want, "\n"):
		default:
			want += "\n"
		}
		if len(r.Hunks) == 0 {
			want = writeOut(t, buffer.New(a))
		}
		if got := applyPatches(t, a, obtain); got != want {
			t.Errorf("a = %q, b = %q: A で全ての塊を do した結果 %q, want %q", a, b, got, want)
		}
		if got := applyPatches(t, a, put); got != want {
			t.Errorf("a = %q, b = %q: B で全ての塊を dp した結果 %q, want %q", a, b, got, want)
		}
		for i := range r.Hunks {
			if p := r.Put(i, A); p.Side != B {
				t.Fatalf("Put(%d, A).Side = %v, want B", i, p.Side)
			}
		}
	}
}

func TestInline(t *testing.T) {
	tests := []struct {
		a, b   string
		sa, sb []Span
	}{
		{"same", "same", nil, nil},
		{"foo bar", "foo baz", []Span{{6, 7}}, []Span{{6, 7}}},
		{"abc", "abXc", []Span{{2, 2}}, []Span{{2, 3}}},
		{"aXbYc", "abc", []Span{{1, 2}, {3, 4}}, []Span{{1, 1}, {2, 2}}},
		{"あいう", "あえう", []Span{{3, 6}}, []Span{{3, 6}}},
		// 不正な UTF-8（#185 のレビュー）。1 バイトを 1 文字として、バイトの位置で返す。
		{"\xff\xff\xffx", "\xff\xff\xffy", []Span{{3, 4}}, []Span{{3, 4}}},
		{"\xffあx", "\xffあy", []Span{{4, 5}}, []Span{{4, 5}}},
		{"\xe3\x81x", "\xe3\x81y", []Span{{2, 3}}, []Span{{2, 3}}},
		{"\xff\xfeab", "\xffab", []Span{{1, 2}}, []Span{{1, 1}}},
		// 違う不正なバイトは違う文字である。
		{"\xff", "\xfe", []Span{{0, 1}}, []Span{{0, 1}}},
	}
	for _, tt := range tests {
		sa, sb := Inline(tt.a, tt.b)
		if !reflect.DeepEqual(sa, tt.sa) || !reflect.DeepEqual(sb, tt.sb) {
			t.Errorf("Inline(%q, %q) = %v, %v, want %v, %v", tt.a, tt.b, sa, sb, tt.sa, tt.sb)
		}
	}
}

// TestInlineReplace は、乱数で作ったバイト列（不正な UTF-8 と多バイト文字を含む）の組で、
// 範囲が行の中に収まり、a の範囲を b の範囲の内容で置き換えると b になることを確かめる。
func TestInlineReplace(t *testing.T) {
	pieces := []string{"a", "b", "あ", "é", "\xff", "\xfe", "\xe3\x81", "\xef\xbf\xbd"}
	rng := rand.New(rand.NewPCG(7, 8)) //nolint:gosec // 再現できる無作為の入力が要る。暗号の用途ではない。
	gen := func() string {
		var sb strings.Builder
		for range rng.IntN(12) {
			sb.WriteString(pieces[rng.IntN(len(pieces))])
		}
		return sb.String()
	}
	for range 2000 {
		a, b := gen(), gen()
		sa, sb := Inline(a, b)
		if len(sa) != len(sb) {
			t.Fatalf("Inline(%q, %q): 範囲の数が違う %v, %v", a, b, sa, sb)
		}
		var out strings.Builder
		prev := 0
		for i := range sa {
			if sa[i].Start < prev || sa[i].End > len(a) || sb[i].End > len(b) || sa[i].Start > sa[i].End || sb[i].Start > sb[i].End {
				t.Fatalf("Inline(%q, %q): 範囲が行の外か逆順 %v, %v", a, b, sa, sb)
			}
			out.WriteString(a[prev:sa[i].Start])
			out.WriteString(b[sb[i].Start:sb[i].End])
			prev = sa[i].End
		}
		out.WriteString(a[prev:])
		if out.String() != b {
			t.Fatalf("Inline(%q, %q) = %v, %v: 置き換えた結果 %q", a, b, sa, sb, out.String())
		}
	}
}

// largeTexts は、n 行のテキストと、changes 箇所の行を変え、足し、消したテキストを作る。
func largeTexts(n, changes int) (Text, Text) {
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // 再現できる無作為の入力が要る。暗号の用途ではない。
	a := make([]string, n)
	for i := range a {
		a[i] = fmt.Sprintf("line %d: %08x", i, rng.Uint32())
	}
	b := make([]string, 0, n+changes)
	step := n / changes
	for i, l := range a {
		switch {
		case i%step == 0 && (i/step)%3 == 0:
			b = append(b, l+" changed")
		case i%step == 0 && (i/step)%3 == 1:
			b = append(b, l, "added")
		case i%step == 0:
			// 消す。
		default:
			b = append(b, l)
		}
	}
	return Text{Lines: a}, Text{Lines: b}
}

// TestComputeLarge は、NFR-05 の大きさ（10,000 行）のテキストの計算の時間を記録する。
// 時間は負荷に左右されるので、上限は緩く置き、正しさ（塊の通りに置き換えると b になること）を主に確かめる。
// 2026-11-18 の手元の計測（BenchmarkCompute10000）は、issue #185 の PR の説明に書いた。
// 200 箇所では lcs の探索の上限に届くが、まとまった塊を計算し直して、塊の数は箇所の数と同じになる（Compute の doc）。
func TestComputeLarge(t *testing.T) {
	for _, changes := range []int{10, 40, 200} {
		a, b := largeTexts(10000, changes)
		start := time.Now()
		r := Compute(a, b)
		elapsed := time.Since(start)
		t.Logf("10,000 行、%d 箇所: %d 個の塊、%v", changes, len(r.Hunks), elapsed)
		if got := applyHunks(r); !slices.Equal(got, b.Lines) {
			t.Fatalf("%d 箇所: 塊の通りに置き換えた結果が b と違う", changes)
		}
		if len(r.Hunks) != changes {
			t.Errorf("%d 箇所: 塊 = %d 個, want 箇所の数", changes, len(r.Hunks))
		}
		if elapsed > 2*time.Second {
			t.Errorf("%d 箇所: 計算に %v かかった", changes, elapsed)
		}
	}
}

func BenchmarkCompute10000(b *testing.B) {
	for _, changes := range []int{10, 40, 200} {
		ta, tb := largeTexts(10000, changes)
		b.Run(strconv.Itoa(changes), func(b *testing.B) {
			for b.Loop() {
				Compute(ta, tb)
			}
		})
	}
}
