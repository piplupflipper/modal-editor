package diff

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// screen は、side の側の Layout を、行を "行の内容（色の種類）"、詰め物を "-" にした画面の行の列にする。
func screen(r *Result, side Side) []string {
	l := r.Layout(side)
	lines := r.Text(side).Lines
	kinds := map[int]LineKind{}
	for _, m := range l.Marks {
		kinds[m.Line] = m.Kind
	}
	var out []string
	for line := 0; line <= l.Lines; line++ {
		for range l.Fill(line) {
			out = append(out, "-")
		}
		if line == l.Lines {
			break
		}
		s := ""
		if line < len(lines) {
			s = lines[line]
		}
		switch kinds[line] {
		case LineChanged:
			s += "(c)"
		case LineOnlyHere:
			s += "(+)"
		}
		out = append(out, s)
	}
	return out
}

// TestLayoutScreen は、両側の画面の行の並び（色と詰め物の位置）を確かめる。
// 期待値は、2026-11-19 に手元の Vim 9.1（patches 1-16）で vim -Nu NONE -n -d a b を tmux で開いて画面を見たものである。
// 変えた行の塊で片側が短ければ、短い側の塊の行の下に詰め物を描き、空のファイルの側は空の行を変えた行にし、その下に詰め物を 1 行少なく描く。
func TestLayoutScreen(t *testing.T) {
	tests := []struct {
		name  string
		a, b  string
		wantA []string
		wantB []string
	}{
		{
			name:  "変えた行と消した行と末尾に足した行",
			a:     "a\nb\nc\nd\ne\n",
			b:     "a\nx\ny\nc\ne\nf\n",
			wantA: []string{"a", "b(c)", "-", "c", "d(+)", "e", "-"},
			wantB: []string{"a", "x(c)", "y(+)", "c", "-", "e", "f(+)"},
		},
		{
			name:  "先頭に足した行",
			a:     "c\n",
			b:     "a\nb\nc\n",
			wantA: []string{"-", "-", "c"},
			wantB: []string{"a(+)", "b(+)", "c"},
		},
		{
			name:  "空のファイル",
			a:     "",
			b:     "d\nb\n",
			wantA: []string{"(c)", "-"},
			wantB: []string{"d(+)", "b(+)"},
		},
	}
	for _, tt := range tests {
		r := Compute(Parse(tt.a), Parse(tt.b))
		if got := screen(r, A); !reflect.DeepEqual(got, tt.wantA) {
			t.Errorf("%s: A = %q, want %q", tt.name, got, tt.wantA)
		}
		if got := screen(r, B); !reflect.DeepEqual(got, tt.wantB) {
			t.Errorf("%s: B = %q, want %q", tt.name, got, tt.wantB)
		}
	}
}

// TestLayoutRows は、乱数で作ったテキストの組で、次を確かめる。
//
//   - 両側の画面の行の数が等しい
//   - 塊の外の行（両側で同じ行）は、MapLine で対応させたもう片側の行と、同じ画面の行に出る
//   - LineAtRow が Row と詰め物の数の逆になる
//   - 変えた行の Other が、もう片側の行を指す
func TestLayoutRows(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 228))
	gen := func() string {
		n := rng.IntN(12)
		var sb strings.Builder
		for range n {
			fmt.Fprintf(&sb, "%c\n", 'a'+rune(rng.IntN(4)))
		}
		return sb.String()
	}
	for range 500 {
		a, b := gen(), gen()
		r := Compute(Parse(a), Parse(b))
		la, lb := r.Layout(A), r.Layout(B)
		if la.Rows() != lb.Rows() {
			t.Fatalf("%q %q: Rows %d != %d", a, b, la.Rows(), lb.Rows())
		}
		for side, l := range map[Side]Layout{A: la, B: lb} {
			ol := lb
			if side == B {
				ol = la
			}
			inHunk := map[int]bool{}
			for _, m := range l.Marks {
				inHunk[m.Line] = true
				if m.Kind == LineChanged && m.Other >= 0 && m.Other >= len(r.Text(side.Other()).Lines) {
					t.Fatalf("%q %q: side %d line %d: Other %d is out of range", a, b, side, m.Line, m.Other)
				}
			}
			for line := range len(r.Text(side).Lines) {
				if !inHunk[line] {
					if got, want := l.Row(line), ol.Row(r.MapLine(side, line)); got != want {
						t.Fatalf("%q %q: side %d line %d: Row %d, other Row %d", a, b, side, line, got, want)
					}
				}
				for k := 0; k <= l.Fill(line); k++ {
					gl, gf := l.LineAtRow(l.Row(line) - k)
					if gl != line || gf != k {
						t.Fatalf("%q %q: side %d LineAtRow(Row(%d)-%d) = %d, %d", a, b, side, line, k, gl, gf)
					}
				}
			}
		}
	}
}

// TestLayoutLineAtRowOutside は、画面の行の外の LineAtRow を確かめる。
func TestLayoutLineAtRowOutside(t *testing.T) {
	// A: c、B: a b c。A の 0 行目の上に詰め物が 2 行ある。
	l := Compute(Parse("c\n"), Parse("a\nb\nc\nd\n")).Layout(A)
	if line, fill := l.LineAtRow(-1); line != 0 || fill != 2 {
		t.Errorf("LineAtRow(-1) = %d, %d, want 0, 2", line, fill)
	}
	if line, fill := l.LineAtRow(100); line != 0 || fill != 0 {
		t.Errorf("LineAtRow(100) = %d, %d, want 0, 0", line, fill)
	}
	if got := l.Fill(1); got != 1 {
		t.Errorf("Fill(1)（最後の行の下）= %d, want 1", got)
	}
}
