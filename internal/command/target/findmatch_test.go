package target

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// incText は、nvim 0.12.5 で <C-g> と <C-t> を確かめたファイルの、foo の一致の並びを写したテキストである。
// 一致は 1:12、4:15、4:23、34:8、45:8（0 から数えた行:バイト）の 5 つである。
var incText = func() string {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d x", i+1)
	}
	lines[1] = "line 2 beta foo"
	lines[4] = "line 5 epsilon foo bar foo"
	lines[34] = "line 35 foobar here"
	lines[45] = "line 46 foo end"
	return strings.Join(lines, "\n") + "\n"
}()

// FindMatch は、回数の分だけ検索した一致から、回数の一致を 1 単位にして steps だけ動かす。
// 動かす向きは / と ? によらず、正がバッファの末尾の方である。
// 期待値は、nvim 0.12.5 の --clean で、同じ並びのファイルの 1 行目の先頭（? は最後の行の先頭）から打った /foo、?foo、3/foo の後に、
// <C-g> と <C-t> を 1 回ずつ打って出た一致の位置（2026-11-20 に tmux で確かめた）である。
// 動かしたときは、端を越えても折り返したとしない（nvim 0.12.5 の 20 行目からの ?foo<C-g><CR> と /foo<C-g><C-g><CR> は、
// 端を越えて移っても折り返しの W を出さない。2026-11-20 に tmux で確かめた）。
//
// req: FR-153
func TestFindMatchSteps(t *testing.T) {
	b := buffer.New(incText)
	top, last := buffer.Position{}, buffer.Position{Line: 49}
	for _, tc := range []struct {
		name         string
		from         buffer.Position
		backward     bool
		count, steps int
		want         string
		wrapped      bool
	}{
		{"/foo", top, false, 1, 0, "1:12", false},
		{"/foo<C-g>", top, false, 1, 1, "4:15", false},
		{"/foo<C-g>×3", top, false, 1, 3, "34:8", false},
		{"/foo<C-g>×5（1 周して戻る）", top, false, 1, 5, "1:12", false},
		{"/foo<C-g>×5<C-t>（打った数の差で数え直す）", top, false, 1, 4, "45:8", false},
		{"/foo<C-t>（先頭の一致の前は末尾の一致）", top, false, 1, -1, "45:8", false},
		{"?foo", last, true, 1, 0, "45:8", false},
		{"?foo<C-g>", last, true, 1, 1, "1:12", false},
		{"20 行目からの ?foo<C-g>（端を越えても折り返したとしない）", buffer.Position{Line: 20}, true, 1, 1, "34:8", false},
		{"20 行目からの /foo<C-g><C-g>（端を越えても折り返したとしない）", buffer.Position{Line: 20}, false, 1, 2, "1:12", false},
		{"?foo<C-g><C-g><C-t><C-t><C-t>", last, true, 1, -1, "34:8", false},
		{"3/foo", top, false, 3, 0, "4:23", false},
		{"3/foo<C-t>", top, false, 3, -1, "45:8", false},
		{"3/foo<C-t><C-t>", top, false, 3, -2, "4:15", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := FindMatch(context.Background(), b, tc.from, Search{Pattern: "foo", Backward: tc.backward}, tc.count, tc.steps)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.Range.Start.String(); got != tc.want || m.Wrapped != tc.wrapped {
				t.Errorf("start = %s wrapped = %v, want %s %v", got, m.Wrapped, tc.want, tc.wrapped)
			}
			if m.Range.End != (buffer.Position{Line: m.Range.Start.Line, Byte: m.Range.Start.Byte + 3}) {
				t.Errorf("range = %v", m.Range)
			}
		})
	}
}

// 一致の範囲は、一致の終わりまでで、空の一致は始まりと終わりが同じである。
// / と ? のモーションの移り先は、同じ問いの FindMatch の一致の先頭である（打っている間に出した一致へ <CR> で移る）。
//
// req: FR-153
func TestFindMatchRange(t *testing.T) {
	b := buffer.New("abc\nxx foo bar\n")
	m, err := FindMatch(context.Background(), b, buffer.Position{}, Search{Pattern: "fo+ b"}, 1, 0)
	if err != nil || m.Range != (buffer.Range{Start: buffer.Position{Line: 1, Byte: 3}, End: buffer.Position{Line: 1, Byte: 8}}) {
		t.Errorf("range = %v, err = %v", m.Range, err)
	}
	m, err = FindMatch(context.Background(), b, buffer.Position{}, Search{Pattern: "^"}, 1, 0)
	if err != nil || m.Range.Start != m.Range.End || m.Range.Start != (buffer.Position{Line: 1}) {
		t.Errorf("empty match = %v, err = %v", m.Range, err)
	}
	fwd, _ := LookupMotion("search-forward")
	for steps := -3; steps <= 3; steps++ {
		req := Request{Cursor: buffer.Position{Line: 3}, Count: 2, Search: Search{Pattern: "foo"}, SearchSteps: steps}
		res, err := fwd(buffer.New(incText), req)
		m, merr := FindMatch(context.Background(), buffer.New(incText), req.Cursor, Search{Pattern: "foo"}, 2, steps)
		if err != nil || merr != nil || res.Pos != m.Range.Start || res.Wrapped != m.Wrapped {
			t.Errorf("steps %d: motion = %v %v (%v), FindMatch = %v %v (%v)", steps, res.Pos, res.Wrapped, err, m.Range.Start, m.Wrapped, merr)
		}
	}
}

// FindMatch は、パターンの誤りと見つからないことを、検索のモーションと同じ誤りで返す。
// 写した行（Snapshot）は、写した後にバッファを変えても変わらない。
//
// req: FR-153
func TestFindMatchErrors(t *testing.T) {
	b := buffer.New("abc\n")
	snap := Snapshot(b)
	if _, err := b.Apply(buffer.Transaction{Edits: []buffer.Edit{{Range: buffer.Range{End: buffer.Position{Byte: 3}}, Text: "foo"}}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var pe *PatternError
	if _, err := FindMatch(ctx, snap, buffer.Position{}, Search{Pattern: "foo("}, 1, 0); !errors.As(err, &pe) {
		t.Errorf("正しくないパターン: err = %v", err)
	}
	if _, err := FindMatch(ctx, snap, buffer.Position{}, Search{}, 1, 0); !errors.Is(err, ErrNoPattern) {
		t.Errorf("空のパターン: err = %v", err)
	}
	if _, err := FindMatch(ctx, snap, buffer.Position{}, Search{Pattern: "foo"}, 1, 0); !errors.Is(err, ErrPatternNotFound) {
		t.Errorf("写しの後の変更が見える: err = %v", err)
	}
	if m, err := FindMatch(ctx, b, buffer.Position{Line: 5, Byte: 9}, Search{Pattern: "o"}, 1, 0); err != nil || m.Range.Start.String() != "0:1" {
		t.Errorf("バッファの外の位置から: %v, %v", m.Range, err)
	}
}

// ctx が終わったら（打つたびの検索の時間の上限、次の入力での取り消し）、探すのをやめて ctx.Err() を返す。
// 一致がすぐ見つかるときは、ctx を調べる前に返す。
//
// req: FR-153
func TestFindMatchCanceled(t *testing.T) {
	lines := make(LineSnapshot, 200000)
	for i := range lines {
		lines[i] = "abcdefghij"
	}
	lines[len(lines)-1] = "needle"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FindMatch(ctx, lines, buffer.Position{}, Search{Pattern: "needle"}, 1, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if m, err := FindMatch(ctx, lines, buffer.Position{}, Search{Pattern: "def"}, 1, 0); err != nil || m.Range.Start.String() != "0:3" {
		t.Errorf("近くの一致: %v, %v", m.Range, err)
	}
	if m, err := FindMatch(context.Background(), lines, buffer.Position{}, Search{Pattern: "needle"}, 1, 0); err != nil || m.Range.Start.Line != len(lines)-1 {
		t.Errorf("上限なし: %v, %v", m.Range, err)
	}
}
