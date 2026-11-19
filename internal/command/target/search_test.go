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
	word, _ := LookupMotion("search-word-forward")
	_, err = word(buffer.New("  \n"), Request{Cursor: buffer.Position{}, Count: 1})
	if !errors.Is(err, ErrNoString) {
		t.Errorf("* on blank = %v", err)
	}
}
