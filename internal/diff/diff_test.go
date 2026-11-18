package diff

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

func TestParse(t *testing.T) {
	tests := []struct {
		content string
		want    Text
	}{
		{"", Text{EOL: true}},
		{"\n", Text{Lines: []string{""}, EOL: true}},
		{"a\nb\n", Text{Lines: []string{"a", "b"}, EOL: true}},
		{"a\nb", Text{Lines: []string{"a", "b"}, EOL: false}},
		{"a\n\n", Text{Lines: []string{"a", ""}, EOL: true}},
	}
	for _, tt := range tests {
		got := Parse(tt.content)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tt.content, got, tt.want)
		}
		if s := got.String(); s != tt.content {
			t.Errorf("Parse(%q).String() = %q", tt.content, s)
		}
	}
}

func TestFromBuffer(t *testing.T) {
	for _, content := range []string{"", "\n", "a\nb\n", "a\nb"} {
		b := buffer.New(content)
		b.SetFixEndOfLine(false)
		var sb strings.Builder
		if _, err := b.WriteTo(&sb); err != nil {
			t.Fatal(err)
		}
		if got := FromBuffer(b).String(); got != sb.String() {
			t.Errorf("FromBuffer(%q).String() = %q, want %q", content, got, sb.String())
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
		{"末尾の改行の有無", "a\nb\n", "a\nb", []Hunk{{Span{1, 2}, Span{1, 2}}}},
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

// applyHunks は、r の塊の通りに A の側の行を置き換えた結果を返す。
func applyHunks(r *Result) Text {
	a, b := r.Text(A), r.Text(B)
	var lines []string
	prev := 0
	for _, h := range r.Hunks {
		lines = append(lines, a.Lines[prev:h.A.Start]...)
		lines = append(lines, b.Lines[h.B.Start:h.B.End]...)
		prev = h.A.End
	}
	lines = append(lines, a.Lines[prev:]...)
	eol := a.EOL
	if len(r.Hunks) > 0 && r.Hunks[len(r.Hunks)-1].B.End == len(b.Lines) {
		eol = b.EOL
	}
	return Text{Lines: lines, EOL: eol}
}

// randomText は、少ない種類の行から n 行のテキストを作る。末尾の改行の有無も乱数で決める。
func randomText(rng *rand.Rand, n int) string {
	var sb strings.Builder
	for i := range n {
		sb.WriteByte("abcd"[rng.IntN(4)])
		if i < n-1 || rng.IntN(4) != 0 {
			sb.WriteString("\n")
		}
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
		if got := applyHunks(r).String(); got != b {
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

func TestNextPrevHunkAt(t *testing.T) {
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
		{"HunkAt A 1", r.HunkAt, A, 1, res{0, true}},
		{"HunkAt A 2", r.HunkAt, A, 2, res{0, false}},
		{"HunkAt A 4（末尾の空の範囲）", r.HunkAt, A, 4, res{2, true}},
		{"HunkAt B 3（空の範囲の次の行）", r.HunkAt, B, 3, res{1, true}},
		{"HunkAt B 4", r.HunkAt, B, 4, res{2, true}},
		{"HunkAt B 0", r.HunkAt, B, 0, res{0, false}},
	}
	for _, tt := range tests {
		if got := call(tt.f, tt.side, tt.line); got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
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

// applyPatches は、content のバッファに、patches を後ろの塊から順に適用した内容を返す。
// 後ろから適用すると、前の塊の位置は変わらない。
func applyPatches(t *testing.T, content string, patches []Patch) string {
	t.Helper()
	b := buffer.New(content)
	b.SetFixEndOfLine(false)
	for i := len(patches) - 1; i >= 0; i-- {
		p := patches[i]
		eol := b.HasTrailingNewline()
		if p.SetEOL {
			eol = p.EOL
		}
		if _, err := b.ApplyReload(buffer.Transaction{BaseVersion: b.Version(), Edits: []buffer.Edit{p.Edit}}, eol); err != nil {
			t.Fatalf("patch %d (%+v): %v", i, p, err)
		}
		if p.NoLines {
			b.MarkNoLines()
		}
	}
	return FromBuffer(b).String()
}

// TestObtainPut は、全ての塊を do（Obtain）で取り込むと、相手と同じ内容になることと、dp（Put）で送ると相手が同じ内容になることを、
// 乱数で作ったテキストの組と境界の組で確かめる。編集はバッファ（internal/buffer）に適用する。
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
		if got := applyPatches(t, a, obtain); got != b {
			t.Errorf("a = %q, b = %q: A で全ての塊を do した結果 %q", a, b, got)
		}
		if got := applyPatches(t, a, put); got != b {
			t.Errorf("a = %q, b = %q: B で全ての塊を dp した結果 %q", a, b, got)
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
	}
	for _, tt := range tests {
		sa, sb := Inline(tt.a, tt.b)
		if !reflect.DeepEqual(sa, tt.sa) || !reflect.DeepEqual(sb, tt.sb) {
			t.Errorf("Inline(%q, %q) = %v, %v, want %v, %v", tt.a, tt.b, sa, sb, tt.sa, tt.sb)
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
	return Text{Lines: a, EOL: true}, Text{Lines: b, EOL: true}
}

// TestComputeLarge は、NFR-05 の大きさ（10,000 行）のテキストの計算の時間を記録する。
// 時間は負荷に左右されるので、上限は緩く置き、正しさ（塊の通りに置き換えると b になること）を主に確かめる。
// 2026-11-18 の手元の計測（BenchmarkCompute10000）は、issue #185 の PR の説明に書いた。
// 200 箇所では lcs の探索の上限に届き、近くの違いがまとまって塊の数が箇所の数より少なくなる（Compute の doc）。
func TestComputeLarge(t *testing.T) {
	for _, changes := range []int{10, 40, 200} {
		a, b := largeTexts(10000, changes)
		start := time.Now()
		r := Compute(a, b)
		elapsed := time.Since(start)
		t.Logf("10,000 行、%d 箇所: %d 個の塊、%v", changes, len(r.Hunks), elapsed)
		if got := applyHunks(r).String(); got != b.String() {
			t.Fatalf("%d 箇所: 塊の通りに置き換えた結果が b と違う", changes)
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
