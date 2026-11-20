package target

import (
	"errors"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// / と ? の入力は、"\" の付かない区切り文字でパターンとオフセットに分ける。
func TestSplitSearch(t *testing.T) {
	for _, tc := range []struct {
		text      string
		delim     rune
		pat, off  string
		hasOffset bool
	}{
		{"foo", '/', "foo", "", false},
		{"foo/", '/', "foo", "", true},
		{"foo/e", '/', "foo", "e", true},
		{`a\/b`, '/', `a\/b`, "", false},
		{`a\/b/e+1`, '/', `a\/b`, "e+1", true},
		{"a/b", '?', "a/b", "", false},
		{`a\?b?e`, '?', `a\?b`, "e", true},
		{`a\`, '/', `a\`, "", false},
	} {
		pat, off, has := SplitSearch(tc.text, tc.delim)
		if pat != tc.pat || off != tc.off || has != tc.hasOffset {
			t.Errorf("SplitSearch(%q, %q) = %q, %q, %v", tc.text, tc.delim, pat, off, has)
		}
	}
}

// 検索のモーションは、回数の分だけ一致を進め、折り返したかを返す。見つからなければパターンを付けて ErrPatternNotFound を返す。
//
// req: FR-03, FR-09
func TestSearchMotions(t *testing.T) {
	b := buffer.New("ab ab\nx\nab\n")
	fwd, _ := LookupMotion("search-forward")
	res, err := fwd(b, Request{Cursor: buffer.Position{Line: 0, Byte: 0}, Count: 2, Search: Search{Pattern: "ab"}})
	if err != nil || res.Pos != (buffer.Position{Line: 2, Byte: 0}) || res.Wrapped || res.Kind != Exclusive {
		t.Errorf("2/ab = %+v, %v", res, err)
	}
	next, _ := LookupMotion("search-next")
	res, err = next(b, Request{Cursor: buffer.Position{Line: 2, Byte: 0}, Count: 1, Search: Search{Pattern: "ab"}})
	if err != nil || res.Pos != (buffer.Position{Line: 0, Byte: 0}) || !res.Wrapped {
		t.Errorf("n = %+v, %v", res, err)
	}
	prev, _ := LookupMotion("search-prev")
	res, err = prev(b, Request{Cursor: buffer.Position{Line: 2, Byte: 0}, Count: 1, Search: Search{Pattern: "ab", Backward: true}})
	if err != nil || res.Pos != (buffer.Position{Line: 0, Byte: 0}) || !res.Wrapped || res.Search.Backward {
		t.Errorf("N after ? = %+v, %v", res, err)
	}
	res, err = fwd(b, Request{Cursor: buffer.Position{}, Count: 1, Search: Search{Pattern: "zz"}})
	if !errors.Is(err, ErrPatternNotFound) || !errors.Is(err, ErrNoTarget) || res.Search.Pattern != "zz" {
		t.Errorf("/zz = %+v, %v", res, err)
	}
	_, err = fwd(b, Request{Cursor: buffer.Position{}, Count: 1, Search: Search{Pattern: "("}})
	var pe *PatternError
	if !errors.As(err, &pe) || pe.Pattern != "(" {
		t.Errorf("/( = %v", err)
	}
	_, err = next(b, Request{Cursor: buffer.Position{}, Count: 1})
	if !errors.Is(err, ErrNoPattern) {
		t.Errorf("n without pattern = %v", err)
	}
}

// * と # のパターンは、カーソルの単語に全体で一致するもので、空白だけの行では ErrNoString になる。
// * と # はモーションではない（カーソルを動かさない操作。FR-139）ので、モーションの表に無い。
//
// req: FR-139
func TestWordPattern(t *testing.T) {
	tests := []struct {
		line string
		at   int
		want string
	}{
		{"foo bar", 0, `\bfoo\b`},
		{"foo bar", 3, `\bbar\b`},
		{"a.b", 1, `\bb\b`},
		{"x +++ y", 2, `\by\b`},
		{"+++ ", 0, `\+\+\+`},
		{"日本語b", 0, "日本語"},
		{"foo_bar", 4, `\bfoo_bar\b`},
	}
	for _, tt := range tests {
		got, err := WordPattern(buffer.New(tt.line+"\n"), buffer.Position{Byte: tt.at})
		if err != nil || got != tt.want {
			t.Errorf("WordPattern(%q, %d) = %q, %v, want %q", tt.line, tt.at, got, err, tt.want)
		}
	}
	if _, err := WordPattern(buffer.New("  \n"), buffer.Position{}); !errors.Is(err, ErrNoString) {
		t.Errorf("* on blank = %v", err)
	}
	for _, id := range []string{"search-word-forward", "search-word-backward"} {
		if _, ok := LookupMotion(id); ok {
			t.Errorf("LookupMotion(%q) が見つかる（* と # はモーションではない）", id)
		}
	}
}
