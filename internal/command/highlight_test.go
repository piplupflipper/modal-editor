package command

import (
	"errors"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// * と # は、カーソルの単語を検索のパターンと向きにして直前の検索に記録し、カーソルを動かさず、ジャンプリストにも記録しない。
// 記録したパターンを通知に出す。回数は使わない。次の一致へは n で移り、n はジャンプリストに記録する。
//
// req: FR-139
func TestSearchWordKeepsCursor(t *testing.T) {
	tests := []struct {
		keys     string
		backward bool
		message  string
	}{
		{"*", false, `/\bfoo\b`},
		{"#", true, `?\bfoo\b`},
		{"3*", false, `/\bfoo\b`},
		{"2#", true, `?\bfoo\b`},
	}
	for _, tt := range tests {
		t.Run(tt.keys, func(t *testing.T) {
			s := setup(t, "foo f|oo foo")
			nav := &jumpCountNav{}
			s.Navigator = nav
			var msg Message
			for _, r := range feedKeys(s, tt.keys) {
				if r.Err != nil {
					t.Fatalf("err = %v", r.Err)
				}
				msg = r.Message
			}
			if got := s.Cursor().String(); got != "0:5" || nav.marks != 0 || s.Mode() != ModeNormal {
				t.Errorf("cursor = %s, jumps = %d, mode = %v", got, nav.marks, s.Mode())
			}
			if got := s.LastSearch(); got.Pattern != `\bfoo\b` || got.Backward != tt.backward {
				t.Errorf("last search = %+v", got)
			}
			if msg.Text != tt.message || msg.Warning {
				t.Errorf("message = %+v, want %q", msg, tt.message)
			}
			feedKeys(s, "n")
			want := "0:8"
			if tt.backward {
				want = "0:4"
			}
			if got := s.Cursor().String(); got != want || nav.marks != 1 {
				t.Errorf("n の後の cursor = %s, jumps = %d, want %s, 1", got, nav.marks, want)
			}
		})
	}
}

// ビジュアルモードの * と # は、選択とモードを変えずにパターンだけを決める。続くオペレーターは元の選択に効く。
//
// req: FR-139
func TestSearchWordVisual(t *testing.T) {
	for _, mode := range []string{"v", "V"} {
		s := setup(t, "|foo bar foo\nbaz")
		feedKeys(s, mode+"l*")
		start, end, _, ok := s.Selection()
		if !ok || start.String() != "0:0" || end.String() != "0:1" || s.Cursor().String() != "0:1" || !s.Mode().visual() {
			t.Errorf("%s: selection = %s-%s (%v), cursor = %s, mode = %v", mode, start, end, ok, s.Cursor(), s.Mode())
		}
		if got := s.LastSearch().Pattern; got != `\bfoo\b` {
			t.Errorf("%s: pattern = %q", mode, got)
		}
		feedKeys(s, "d")
		want := "o bar foo\nbaz"
		if mode == "V" {
			want = "baz"
		}
		if got := s.buf.Text(); got != want {
			t.Errorf("%s: text = %q, want %q", mode, got, want)
		}
	}
}

// * と # はモーションでないので、オペレーターの後には使えない。オペレーターを取り消し、バッファと直前の検索を変えない。
//
// req: FR-139
func TestSearchWordAfterOperator(t *testing.T) {
	for _, keys := range []string{"d*", "c#", "y*"} {
		s := setup(t, "|foo bar foo")
		feedKeys(s, keys)
		if got := s.buf.Text(); got != "foo bar foo" || s.Mode() != ModeNormal || s.Pending() {
			t.Errorf("%s: text = %q, mode = %v, pending = %v", keys, got, s.Mode(), s.Pending())
		}
		if got := s.LastSearch().Pattern; got != "" {
			t.Errorf("%s: pattern = %q", keys, got)
		}
		// 続くキーは、取り消した後のノーマルモードのキーとして効く。
		feedKeys(s, "x")
		if got := s.buf.Text(); got != "oo bar foo" {
			t.Errorf("%s の後の x: text = %q", keys, got)
		}
	}
	// * と # は操作の表にあり、オペレーターの後で使えるモーションの表には無い。
	for _, id := range []string{"search-word-forward", "search-word-backward"} {
		var kinds []input.BindingKind
		for _, b := range input.DefaultKeymap().Bindings() {
			if b.Ref.ID == id {
				kinds = append(kinds, b.Kind)
			}
		}
		if len(kinds) != 1 || kinds[0] != input.BindAction {
			t.Errorf("%s の登録の種類 = %v, want 操作だけ", id, kinds)
		}
	}
}

// * と # はテキストを変えないので、編集を禁止したバッファでも使える。空白だけの行では E348 で失敗し、直前の検索を変えない。
// ドットリピートには記録しない（. は直前の x を繰り返す）。
//
// req: FR-139
func TestSearchWordReadOnlyAndErrors(t *testing.T) {
	s := setup(t, "|foo bar")
	s.buf.SetReadOnly(true)
	for _, r := range feedKeys(s, "*") {
		if r.Err != nil {
			t.Errorf("編集を禁止したバッファの * = %v", r.Err)
		}
	}
	if got := s.LastSearch().Pattern; got != `\bfoo\b` {
		t.Errorf("pattern = %q", got)
	}

	s = setup(t, "|  \nfoo")
	s.SetLastSearch(target.Search{Pattern: "bar"})
	var err error
	for _, r := range feedKeys(s, "#") {
		err = errors.Join(err, r.Err)
	}
	var ee *ex.Error
	if !errors.As(err, &ee) || ee.Code != "E348" || s.LastSearch().Pattern != "bar" || s.LastSearch().Backward {
		t.Errorf("空白の行の # = %v, last search = %+v", err, s.LastSearch())
	}

	s = setup(t, "|abcd abcd")
	feedKeys(s, "x*.")
	if got := s.buf.Text(); got != "cd abcd" {
		t.Errorf("x*. = %q", got)
	}
}

// :nohlsearch で消した一致の色は、パターンを使う操作（/、?、n、N、*、#、:s、:g）で戻る。パターンが見つからなくても戻る。
// カーソルを動かしただけ、/ の入力を <Esc> で取り消したとき、正しくないパターン、空白の行の * では戻らない。
// 期待値は、2026-11-20 に Vim 9.1 で、:nohlsearch の後に同じキーを打ち、v:hlsearch を読んで確かめた
// （正しくないパターンだけは、記録しないこの実装の扱いで、Vim と違う。docs/vim-differences.md）。
//
// req: FR-138
func TestClearHighlightByKeys(t *testing.T) {
	tests := []struct {
		keys    string
		shown   bool
		pattern string
	}{
		{"j", false, "foo"},
		{"w", false, "foo"},
		{"n", true, "foo"},
		{"N", true, "foo"},
		{"dn", true, "foo"},
		{"/bar<CR>", true, "bar"},
		{"?bar<CR>", true, "bar"},
		{"/zzz<CR>", true, "zzz"},
		{"/<CR>", true, "foo"},
		{"/bar<Esc>", false, "foo"},
		{"/(<CR>", false, "foo"},
		{"*", true, `\bfoo\b`},
		{"#", true, `\bfoo\b`},
		{"jj*", false, "foo"},
		{":s/foo/x/<CR>", true, "foo"},
		{":g/bar/<CR>", true, "bar"},
		{":2<CR>", false, "foo"},
		{":noh<CR>", false, "foo"},
		{":search.clearHighlight<CR>", false, "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.keys, func(t *testing.T) {
			s := setup(t, "|foo bar\nbar foo\n  ")
			feedKeys(s, "/foo<CR>gg0")
			if got := s.HighlightPattern(0); got != "foo" {
				t.Fatalf("/foo の後の HighlightPattern = %q", got)
			}
			feedKeys(s, ":nohlsearch<CR>")
			if got := s.HighlightPattern(0); got != "" || s.LastSearch().Pattern != "foo" {
				t.Fatalf(":nohlsearch の後の HighlightPattern = %q, last search = %+v", got, s.LastSearch())
			}
			feedKeys(s, tt.keys)
			if got := s.HighlightPattern(0); (got != "") != tt.shown || s.LastSearch().Pattern != tt.pattern {
				t.Errorf("HighlightPattern = %q（色を付ける=%v）, last search = %q, want 色を付ける=%v, %q",
					got, got != "", s.LastSearch().Pattern, tt.shown, tt.pattern)
			}
		})
	}
	// 見つからない n も戻す。
	s := setup(t, "|abc")
	feedKeys(s, "/zzz<CR>:noh<CR>n")
	if got := s.HighlightPattern(0); got != "zzz" {
		t.Errorf("見つからない n の後の HighlightPattern = %q", got)
	}
}

// 一致の色を消した状態は、直前の検索と組で、バッファと client をまたいで引き継ぐ（TakeOverShared）。
//
// req: FR-138
func TestClearHighlightTakeOver(t *testing.T) {
	a, b := setup(t, "|foo"), setup(t, "|bar")
	feedKeys(a, "/foo<CR>:noh<CR>")
	b.TakeOverShared(a)
	if got := b.HighlightPattern(0); got != "" || b.LastSearch().Pattern != "foo" {
		t.Errorf("引き継いだ HighlightPattern = %q, last search = %+v", got, b.LastSearch())
	}
	feedKeys(b, "n")
	a.TakeOverShared(b)
	if got := a.HighlightPattern(0); got != "foo" {
		t.Errorf("n の後に引き継いだ HighlightPattern = %q", got)
	}
}
