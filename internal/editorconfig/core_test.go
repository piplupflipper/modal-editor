package editorconfig

import (
	"maps"
	"testing"
)

// EditorConfig の公式のテスト（github.com/editorconfig/editorconfig-core-test の glob/braces.in と glob/star_star.in と、
// glob/CMakeLists.txt の braces_* と star_star_over_separator*）を写した（2026-11-21 に master を読んだ）。
// .editorconfig のあるディレクトリからの相対パスを、ファイルとして当てる。want は当たる項目で、空なら当たらない。
// 公式のテストは `name=value` の行を正規表現で比べるので、ここでは当たる項目の対応で比べる。
//
// req: FR-164
func TestCoreGlob(t *testing.T) {
	const bracesIn = `; test { and }

root=true

; word choice
[*.{py,js,html}]
choice=true

; single choice
[{single}.b]
choice=single

; empty choice
[{}.c]
empty=all

; choice with empty word
[a{b,c,}.d]
empty=word

; choice with empty words
[a{,b,,c,}.e]
empty=words

; no closing brace
[{.f]
closing=false

; nested braces
[{word,{also},this}.g]
nested=true

; nested braces, adjacent at start
[{{a,b},c}.k]
nested_start=true

; nested braces, adjacent at end
[{a,{b,c}}.l]
nested_end=true

; closing inside beginning
[{},b}.h]
closing=inside

; opening inside beginning
[{{,b,c{d}.i]
unmatched=true

; escaped comma
[{a\,b,cd}.txt]
comma=yes

; escaped closing brace
[{e,\},f}.txt]
closing=yes

; escaped backslash
[{g,\\,i}.txt]
backslash=yes

; patterns nested in braces
[{some,a{*c,b}[ef]}.j]
patterns=nested

; numeric braces
[{3..120}]
number=true

; alphabetical
[{aardvark..antelope}]
words=a
`
	const starStarIn = `; test **

root=true

[a**z.c]
key1=value1

[b/**z.c]
key2=value2

[c**/z.c]
key3=value3

[d/**/z.c]
key4=value4
`
	tests := []struct {
		in, file, want string
	}{
		{bracesIn, "test.py", "choice=true"},
		{bracesIn, "test.js", "choice=true"},
		{bracesIn, "test.html", "choice=true"},
		{bracesIn, "test.pyc", ""},
		{bracesIn, "{single}.b", "choice=single"},
		{bracesIn, ".b", ""},
		{bracesIn, "{}.c", "empty=all"},
		{bracesIn, ".c", ""},
		{bracesIn, "a.d", "empty=word"},
		{bracesIn, "ab.d", "empty=word"},
		{bracesIn, "ac.d", "empty=word"},
		{bracesIn, "a,.d", ""},
		{bracesIn, "a.e", "empty=words"},
		{bracesIn, "ab.e", "empty=words"},
		{bracesIn, "ac.e", "empty=words"},
		{bracesIn, "a,.e", ""},
		{bracesIn, "{.f", "closing=false"},
		{bracesIn, ".f", ""},
		{bracesIn, "word,this}.g", ""},
		{bracesIn, "{also,this}.g", ""},
		{bracesIn, "word.g", "nested=true"},
		{bracesIn, "{also}.g", "nested=true"},
		{bracesIn, "this.g", "nested=true"},
		{bracesIn, "{{a,b},c}.k", ""},
		{bracesIn, "{a,b}.k", ""},
		{bracesIn, "a.k", "nested_start=true"},
		{bracesIn, "b.k", "nested_start=true"},
		{bracesIn, "c.k", "nested_start=true"},
		{bracesIn, "{a,{b,c}}.l", ""},
		{bracesIn, "{b,c}.l", ""},
		{bracesIn, "a.l", "nested_end=true"},
		{bracesIn, "b.l", "nested_end=true"},
		{bracesIn, "c.l", "nested_end=true"},
		{bracesIn, "{},b}.h", "closing=inside"},
		{bracesIn, "{{,b,c{d}.i", "unmatched=true"},
		{bracesIn, "{.i", ""},
		{bracesIn, "b.i", ""},
		{bracesIn, "c{d.i", ""},
		{bracesIn, ".i", ""},
		{bracesIn, "a,b.txt", "comma=yes"},
		{bracesIn, "a.txt", ""},
		{bracesIn, "cd.txt", "comma=yes"},
		{bracesIn, "e.txt", "closing=yes"},
		{bracesIn, "}.txt", "closing=yes"},
		{bracesIn, "f.txt", "closing=yes"},
		{bracesIn, "g.txt", "backslash=yes"},
		{bracesIn, `\.txt`, "backslash=yes"},
		{bracesIn, "i.txt", "backslash=yes"},
		{bracesIn, "some.j", "patterns=nested"},
		{bracesIn, "abe.j", "patterns=nested"},
		{bracesIn, "abf.j", "patterns=nested"},
		{bracesIn, "abg.j", ""},
		{bracesIn, "ace.j", "patterns=nested"},
		{bracesIn, "acf.j", "patterns=nested"},
		{bracesIn, "acg.j", ""},
		{bracesIn, "abce.j", "patterns=nested"},
		{bracesIn, "abcf.j", "patterns=nested"},
		{bracesIn, "abcg.j", ""},
		{bracesIn, "ae.j", ""},
		{bracesIn, ".j", ""},
		{bracesIn, "1", ""},
		{bracesIn, "3", "number=true"},
		{bracesIn, "15", "number=true"},
		{bracesIn, "60", "number=true"},
		{bracesIn, "5a", ""},
		{bracesIn, "120", "number=true"},
		{bracesIn, "121", ""},
		{bracesIn, "060", ""},
		{bracesIn, "{aardvark..antelope}", "words=a"},
		{bracesIn, "a", ""},
		{bracesIn, "aardvark", ""},
		{bracesIn, "agreement", ""},
		{bracesIn, "antelope", ""},
		{bracesIn, "antimatter", ""},
		{starStarIn, "a/z.c", "key1=value1"},
		{starStarIn, "amnz.c", "key1=value1"},
		{starStarIn, "am/nz.c", "key1=value1"},
		{starStarIn, "a/mnz.c", "key1=value1"},
		{starStarIn, "amn/z.c", "key1=value1"},
		{starStarIn, "a/mn/z.c", "key1=value1"},
		{starStarIn, "b/z.c", "key2=value2"},
		{starStarIn, "b/mnz.c", "key2=value2"},
		{starStarIn, "b/mn/z.c", "key2=value2"},
		{starStarIn, "bmnz.c", ""},
		{starStarIn, "bm/nz.c", ""},
		{starStarIn, "bmn/z.c", ""},
		{starStarIn, "c/z.c", "key3=value3"},
		{starStarIn, "cmn/z.c", "key3=value3"},
		{starStarIn, "c/mn/z.c", "key3=value3"},
		{starStarIn, "cmnz.c", ""},
		{starStarIn, "cm/nz.c", ""},
		{starStarIn, "c/mnz.c", ""},
		{starStarIn, "d/z.c", "key4=value4"},
		{starStarIn, "d/mn/z.c", "key4=value4"},
		{starStarIn, "dmnz.c", ""},
		{starStarIn, "dm/nz.c", ""},
		{starStarIn, "d/mnz.c", ""},
		{starStarIn, "dmn/z.c", ""},
	}
	files := map[string]File{bracesIn: Parse([]byte(bracesIn)), starStarIn: Parse([]byte(starStarIn))}
	for _, tt := range tests {
		got := files[tt.in].Properties(tt.file)
		want := Properties{}
		if tt.want != "" {
			for i := range len(tt.want) {
				if tt.want[i] == '=' {
					want[tt.want[:i]] = tt.want[i+1:]
					break
				}
			}
		}
		if !maps.Equal(got, want) {
			t.Errorf("%s: %v, want %v", tt.file, got, want)
		}
	}
}
