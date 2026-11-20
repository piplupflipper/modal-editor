package target

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// FindMatches は、行ごとに、行の先頭から重ならないように並べた一致を集め、Count は位置が何件目の一致かと件数を返す。
// 何件目かは、先頭が位置と同じかそれより前にある一致の数（Vim の searchcount() の current）で、一致の途中にいればその一致の番号である。
// 行末の位置での一致（$）は、n と同じく 1 つ前の位置にあるものとして比べる。
// 全角の文字を含む行でも、位置はバイトで比べる。
//
// req: FR-167
func TestFindMatchesCount(t *testing.T) {
	pos := func(l, c int) buffer.Position { return buffer.Position{Line: l, Byte: c} }
	for _, tc := range []struct {
		name, text, pattern string
		at                  buffer.Position
		cur, total          int
	}{
		{"一致の先頭", "foo bar\nbaz foo\nqux\nfoo foo\nend", "foo", pos(1, 4), 2, 4},
		{"一致の途中", "foo bar\nbaz foo\nqux\nfoo foo\nend", "foo", pos(3, 5), 4, 4},
		{"最初の一致の手前", "x foo foo", "foo", pos(0, 0), 0, 2},
		{"1 行の重ならない一致", "aaaaa", "aa", pos(0, 2), 2, 2},
		{"行末の一致は 1 つ前の位置で比べる", "abc\ndef", "$", pos(0, 2), 1, 2},
		{"空の行の行末の一致", "x\n\ny", "^$", pos(1, 0), 1, 1},
		{"全角の文字を含む一致", "xあいx あい", "あい", pos(0, 9), 2, 2},
		{"全角の文字の後の一致", "あfooい\nfoo", "foo", pos(0, 3), 1, 2},
		{"一致が無い", "abc", "zzz", pos(0, 0), 0, 0},
		// 行末の空の一致が前に寄せた位置で前の一致と重なったら、1 件と数える（PR #322 のレビューの必須 1）。
		// Neovim 0.12.5 の同じ中身の /\<\|\> は 6 件、ab の /x* は 2 件だった（2026-11-21 に tmux で確かめた）。
		{"行末の \\b は前の \\b と重なる", "a b", `\b`, pos(0, 2), 3, 3},
		{"3 行の \\b", "ab\na b\nx", `\b`, pos(2, 0), 6, 6},
		{"行末の空の一致の x*", "ab", "x*", pos(0, 1), 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms, err := FindMatches(context.Background(), buffer.New(tc.text), tc.pattern, 999)
			if err != nil {
				t.Fatal(err)
			}
			cur, total, over := ms.Count(tc.at)
			if cur != tc.cur || total != tc.total || over {
				t.Errorf("Count(%s) = %d, %d, %v, want %d, %d, false", tc.at, cur, total, over, tc.cur, tc.total)
			}
		})
	}
}

// 件数が上限を超えたら、上限に 1 を足した件数で集めるのをやめ、over を返す。上限を超えた先の位置の番号も上限に 1 を足した数になる。
// 1 行に上限を超える一致があっても、上限に 1 を足した数で止める。
//
// req: FR-167
func TestFindMatchesLimit(t *testing.T) {
	b := buffer.New(strings.Repeat("x\n", 20))
	ms, err := FindMatches(context.Background(), b, "x", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		line, cur int
	}{{0, 1}, {4, 5}, {5, 6}, {19, 6}} {
		cur, total, over := ms.Count(buffer.Position{Line: tc.line})
		if cur != tc.cur || total != 6 || !over {
			t.Errorf("Count(%d 行目) = %d, %d, %v, want %d, 6, true", tc.line, cur, total, over, tc.cur)
		}
	}
	ms, err = FindMatches(context.Background(), buffer.New(strings.Repeat("x", 100)), "x", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, total, over := ms.Count(buffer.Position{Byte: 99}); total != 6 || !over {
		t.Errorf("1 行の一致: total = %d, over = %v, want 6, true", total, over)
	}
	ms, err = FindMatches(context.Background(), b, "x", 20)
	if err != nil {
		t.Fatal(err)
	}
	if cur, total, over := ms.Count(buffer.Position{Line: 19}); cur != 20 || total != 20 || over {
		t.Errorf("上限と同じ件数: %d, %d, %v, want 20, 20, false", cur, total, over)
	}
	// 上限は重なりを除いた数で数える。"a b" の \b は 4 つの一致のうち 2 つが重なるので、3 行で 9 件である。
	ms, err = FindMatches(context.Background(), buffer.New(strings.Repeat("a b\n", 3)), `\b`, 9)
	if err != nil {
		t.Fatal(err)
	}
	if ms.Len() != 9 || ms.Over() {
		t.Errorf("重なりを除いた上限: Len = %d, Over = %v, want 9, false", ms.Len(), ms.Over())
	}
}

// Len と At で、集めた一致の位置を先頭から読める（後に作る右端の列の一致の印が使う口）。位置はカーソルと比べる位置で、
// 行末の一致は 1 つ前のバイト、空の行の行末の一致は Byte が -1 である。
//
// req: FR-167
func TestFindMatchesAt(t *testing.T) {
	ms, err := FindMatches(context.Background(), buffer.New("ab\n\nfoo ab\n"), "b|$", 999)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := range ms.Len() {
		got = append(got, ms.At(i).String())
	}
	if want := "0:1 1:-1 2:5"; strings.Join(got, " ") != want {
		t.Errorf("At = %v, want %s", got, want)
	}
}

// 空のパターンは ErrNoPattern、正しくないパターンは *PatternError を返す。
// ctx が終わったら、読んだ量が checkBytes に達したところで ctx.Err() を返す。
//
// req: FR-167
func TestFindMatchesErrors(t *testing.T) {
	b := buffer.New("foo")
	if _, err := FindMatches(context.Background(), b, "", 999); !errors.Is(err, ErrNoPattern) {
		t.Errorf("空のパターン: err = %v", err)
	}
	var pe *PatternError
	if _, err := FindMatches(context.Background(), b, "foo(", 999); !errors.As(err, &pe) {
		t.Errorf("正しくないパターン: err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	big := buffer.New(strings.Repeat("abcdefghij\n", 2*checkBytes/11) + "foo\n")
	if _, err := FindMatches(ctx, big, "foo", 999); !errors.Is(err, context.Canceled) {
		t.Errorf("終わった ctx: err = %v", err)
	}
	if _, err := FindMatches(ctx, b, "foo", 999); err != nil {
		t.Errorf("checkBytes に達しない量は ctx を調べない: err = %v", err)
	}
}
