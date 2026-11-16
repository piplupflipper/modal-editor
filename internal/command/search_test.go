package command

import (
	"errors"
	"fmt"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command/ex"
)

// searchCase は、検索（/、?、n、N、*、#）を Vim 9.1 と突き合わせたケースである。
//
// 期待値は、Vim 9.1 を Ex モードでない起動（`vim -Nu NONE -i NONE -n --not-a-term`）で動かし、text（"|" がカーソル）を置いて
// @/ を空、v:searchforward を 1、無名のレジスターを "U" にしてから、keys を feedkeys(keys, "xt") で入力した後の、
// バッファの内容、カーソル（行:バイト）、@/、v:searchforward、無名のレジスター、エラーの番号である。
//
// pattern は、この実装の直前のパターン（State.Ex.LastPattern）である。* と # は、Vim の \<foo\> の代わりに \bfoo\b になる
// （target.searchWord の doc コメント）。それ以外は Vim の @/ と同じ文字列である。
type searchCase struct {
	text, keys       string
	want, cursor     string
	pattern          string
	backward         bool
	unnamed, errCode string
}

var searchCases = []searchCase{
	{"|foo\nbar\nfoo", "/foo<CR>", "foo\nbar\nfoo", "2:0", "foo", false, "U", ""},
	{"foo\nbar\n|foo", "/foo<CR>", "foo\nbar\nfoo", "0:0", "foo", false, "U", ""},
	{"|abc\ndef\nghi", "/$<CR>n", "abc\ndef\nghi", "1:2", "$", false, "U", ""},
	{"ab|ab", "?b<CR>", "abab", "0:1", "b", true, "U", ""},
	{"a|bab", "?b<CR>", "abab", "0:3", "b", true, "U", ""},
	{"|aaaa", "/aa<CR>", "aaaa", "0:2", "aa", false, "U", ""},
	{"a|aaa", "/aa<CR>", "aaaa", "0:2", "aa", false, "U", ""},
	{"aaa|a", "?aa<CR>", "aaaa", "0:2", "aa", true, "U", ""},
	{"|foo\nbar\nfoo", "?foo<CR>nN", "foo\nbar\nfoo", "2:0", "foo", true, "U", ""},
	{"|x\n\ny\n", "/^$<CR>n", "x\n\ny\n", "3:0", "^$", false, "U", ""},
	{"|foo\nfoo\nfoo\nfoo\nx", "3/foo<CR>", "foo\nfoo\nfoo\nfoo\nx", "3:0", "foo", false, "U", ""},
	{"a|bc foo\nxx foo", "d/foo<CR>", "afoo\nxx foo", "0:1", "foo", false, "bc ", ""},
	{"|abc\nfoo", "d/foo<CR>", "foo", "0:0", "foo", false, "abc", ""},
	{"|abc foo bar", "v/foo<CR>d", "oo bar", "0:0", "foo", false, "abc f", ""},
	{"|abc foo bar", "vl/fo<Esc>d", "c foo bar", "0:0", "", false, "ab", ""},
	{"foo |+ bar\nbar foo", "*", "foo + bar\nbar foo", "1:0", `\bbar\b`, false, "U", ""},
	{"foo x foo\nf|oo", "#", "foo x foo\nfoo", "0:6", `\bfoo\b`, true, "U", ""},
	{"a| b", "*", "a b", "0:2", `\bb\b`, false, "U", ""},
	{"foo|.bar foo.bar", "*", "foo.bar foo.bar", "0:12", `\bbar\b`, false, "U", ""},
	{"a|日本語b 日本語", "*", "a日本語b 日本語", "0:12", "日本語", false, "U", ""},
	{"|foo bar\nfoo", "2*", "foo bar\nfoo", "0:0", `\bfoo\b`, false, "U", ""},
	{"abc\nab|c", "?$<CR>", "abc\nabc", "0:2", "$", true, "U", ""},
	{"|a foo b foo c foo", "d/foo<CR>.", "foo c foo", "0:0", "foo", false, "foo b ", ""},
	{"|abc\nxbx", ":s/b/B/<CR>n", "aBc\nxbx", "1:1", "b", false, "U", ""},
	{"|abc\nb", "/b<CR>:s//X/<CR>", "aXc\nb", "0:0", "b", false, "U", ""},
	{"|foo foo", "/foo<CR>/<CR>", "foo foo", "0:0", "foo", false, "U", ""},
	{"|abc foo", "c/foo<CR>X<Esc>", "Xfoo", "0:0", "foo", false, "abc ", ""},
	{"|a1\na2\na3", "qa/a<CR>xq@a", "a1\n2\n3", "2:0", "a", false, "a", ""},
	{"abc foo b|ar", "v?foo<CR>d", "abc r", "0:4", "foo", true, "foo ba", ""},
	{"foo |foo foo", "*N", "foo foo foo", "0:4", `\bfoo\b`, false, "U", ""},
	{"|abc", "/zzz<CR>", "abc", "0:0", "zzz", false, "U", "E486"},
	{"|f f f f", "/f<CR>2n", "f f f f", "0:6", "f", false, "U", ""},
	{"|abc", "/<BS>x", "bc", "0:0", "", false, "a", ""},
	{"|ab ab ab", "/b<CR>0dn", "b ab ab", "0:0", "b", false, "a", ""},
	{"|foo\nbar\nfoo", "#", "foo\nbar\nfoo", "2:0", `\bfoo\b`, true, "U", ""},
	{"|foo\nbar\nfoo", "#n", "foo\nbar\nfoo", "0:0", `\bfoo\b`, true, "U", ""},
	{"|  \nfoo", "*", "  \nfoo", "0:0", "", false, "U", "E348"},
	{"x |+++ y +++", "*", "x +++ y +++", "0:6", `\by\b`, false, "U", ""},
	{"x ++|+ y +++", "#", "x +++ y +++", "0:6", `\by\b`, true, "U", ""},
	{"|abc\ndef", "y/e<CR>", "abc\ndef", "0:0", "e", false, "abc\nd", ""},
	{"|abc/def/x", `/\/d<CR>`, "abc/def/x", "0:3", `\/d`, false, "U", ""},
	{"|a?b?c", `?\?<CR>`, "a?b?c", "0:3", `\?`, true, "U", ""},
	{"|ab\ncd", "d?b<CR>", "b\ncd", "0:0", "b", true, "a", ""},
	{"a|b\ncd\nef", "2d/f<CR>", "af", "0:1", "f", false, "b\ncd\ne", ""},
	{"|aXbXcXd", "/X<CR>3n", "aXbXcXd", "0:1", "X", false, "U", ""},
	{"|foo_bar foo", "*", "foo_bar foo", "0:0", `\bfoo_bar\b`, false, "U", ""},
	{"|é foo é", "*", "é foo é", "0:7", "é", false, "U", ""},
}

// 検索のモーションの移動、オペレーターとの組み合わせ、ビジュアルモード、回数、ドットリピート、マクロ、Ex との直前のパターンの共有が、
// Vim 9.1 と一致する。
//
// req: FR-01, FR-03, FR-09
func TestSearchVim(t *testing.T) {
	for _, tc := range searchCases {
		t.Run(fmt.Sprintf("%q/%s", tc.text, tc.keys), func(t *testing.T) {
			s := setup(t, tc.text)
			s.registers.unnamed = Register{Text: "U"}
			var err error
			for _, r := range feedKeys(s, tc.keys) {
				if r.Err != nil && err == nil {
					err = r.Err
				}
			}
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if got := s.Cursor().String(); got != tc.cursor {
				t.Errorf("cursor = %s, want %s", got, tc.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			if got := s.LastSearch(); got.Pattern != tc.pattern || got.Backward != tc.backward {
				t.Errorf("last search = %+v, want %q backward=%v", got, tc.pattern, tc.backward)
			}
			if got := s.registers.unnamed.Text; got != tc.unnamed {
				t.Errorf(`register " = %q, want %q`, got, tc.unnamed)
			}
			var ee *ex.Error
			switch {
			case tc.errCode == "" && err != nil:
				t.Errorf("err = %v", err)
			case tc.errCode != "" && (!errors.As(err, &ee) || ee.Code != tc.errCode):
				t.Errorf("err = %v, want %s", err, tc.errCode)
			}
		})
	}
}

// / と ? の入力の間はコマンドラインモードで、先頭の文字を CommandLineType で返す。
// 見つけたら検索したパターンを、折り返したら警告を Result.Message で返す。
//
// req: FR-01
func TestSearchCommandLine(t *testing.T) {
	s := setup(t, "|foo\nbar\nfoo")
	feedKeys(s, "/fo")
	if s.Mode() != ModeCommandLine || s.CommandLineType() != '/' || s.CommandLine() != "fo" {
		t.Fatalf("mode = %v, type = %q, line = %q", s.Mode(), s.CommandLineType(), s.CommandLine())
	}
	rs := feedKeys(s, "o<CR>")
	if m := rs[len(rs)-1].Message; m != (Message{Text: "/foo"}) {
		t.Errorf("message = %+v", m)
	}
	if s.CommandLineType() != ':' {
		t.Errorf("type after search = %q", s.CommandLineType())
	}
	rs = feedKeys(s, "n")
	if m := rs[0].Message; !m.Warning || m.Text == "" {
		t.Errorf("message after wrap = %+v", m)
	}
	feedKeys(s, "?")
	if s.CommandLineType() != '?' {
		t.Errorf("type = %q", s.CommandLineType())
	}
	feedKeys(s, "<Esc>")
	if s.Mode() != ModeNormal || s.CommandLineType() != ':' {
		t.Errorf("mode = %v, type = %q", s.Mode(), s.CommandLineType())
	}
	feedKeys(s, "N")
	if got := s.Cursor().String(); got != "2:0" {
		t.Errorf("cursor after N = %s", got)
	}
	rs = feedKeys(s, "?<CR>")
	if m := rs[len(rs)-1].Message; m != (Message{Text: "?foo"}) {
		t.Errorf("message = %+v", m)
	}
}

// 検索の誤り（直前のパターンが無い、正しくない正規表現、未対応のオフセット）は、Vim のエラー番号の付いた誤りで失敗し、
// カーソルと直前のパターンを変えない。
//
// req: FR-09
func TestSearchErrors(t *testing.T) {
	for _, tc := range []struct {
		keys, code, pattern string
	}{
		{"n", "E35", ""},
		{"/<CR>", "E35", ""},
		{"/(<CR>", "E383", ""},
		{"/foo<CR>/(<CR>", "E383", "foo"},
		{"/b/e<CR>", "E319", ""},
		{"/zz<CR>n", "E486", "zz"},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			s := setup(t, "|abc foo")
			rs := feedKeys(s, tc.keys)
			var ee *ex.Error
			if err := rs[len(rs)-1].Err; !errors.As(err, &ee) || ee.Code != tc.code {
				t.Errorf("err = %v, want %s", err, tc.code)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			if got := s.LastSearch().Pattern; got != tc.pattern {
				t.Errorf("pattern = %q, want %q", got, tc.pattern)
			}
		})
	}
}

// ドットリピートは、/ で入力したパターンを値に残し、繰り返す時点の直前のパターンが変わっていても同じパターンを検索する。
//
// req: FR-06
func TestSearchDotKeepsPattern(t *testing.T) {
	s := setup(t, "|a x b y c x d y")
	feedKeys(s, "d/x<CR>")
	feedKeys(s, "/y<CR>0")
	feedKeys(s, ".")
	if got := s.buf.Text(); got != "x d y" {
		t.Errorf("text = %q", got)
	}
	if s.dot == nil || s.dot.Target.Pattern != "x" {
		t.Errorf("dot = %+v", s.dot)
	}
	if got := s.LastSearch().Pattern; got != "x" {
		t.Errorf("pattern = %q", got)
	}
}
