package command

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
)

// / と ? でパターンを打っている間は、打つたびの検索の問い（打った途中のパターン、向き、打ち始めたカーソル、回数、<C-g> と <C-t> の差）を返す。
// 打ったパターンが空のとき、打っていないとき、再生の中では問いを返さない。
// 打つたびの検索は、カーソル、モード、直前の検索を変えず、<Esc> で取り消すと打ち始める前に戻る。
//
// req: FR-153
func TestIncSearchQuery(t *testing.T) {
	s := setup(t, "ab|c\nfoo")
	if _, ok := s.IncSearch(); ok {
		t.Fatal("打っていないのに問いがある")
	}
	feedKeys(s, "3/")
	if _, ok := s.IncSearch(); ok {
		t.Error("空のパターンで問いがある")
	}
	feedKeys(s, "fo")
	q, ok := s.IncSearch()
	want := IncSearch{Gen: q.Gen, Search: target.Search{Pattern: "fo"}, From: buffer.Position{Byte: 2}, Count: 3}
	if !ok || q != want || q.Gen == 0 {
		t.Errorf("q = %+v, %v, want %+v", q, ok, want)
	}
	feedKeys(s, "<C-g><C-g><C-t>o/e")
	if q1, _ := s.IncSearch(); q1.Steps != 1 || q1.Search.Pattern != "foo" {
		t.Errorf("<C-g><C-g><C-t> とオフセットの後の q = %+v", q1)
	}
	if s.CommandLine() != "foo/e" {
		t.Errorf("<C-g> と <C-t> がコマンドラインに入った: %q", s.CommandLine())
	}
	s.playDepth = 1
	if _, ok := s.IncSearch(); ok {
		t.Error("再生の中で問いがある")
	}
	s.playDepth = 0
	feedKeys(s, "<Esc>")
	if _, ok := s.IncSearch(); ok || s.Mode() != ModeNormal || s.Cursor().String() != "0:2" || s.LastSearch().Pattern != "" {
		t.Errorf("<Esc> の後: ok=%v mode=%v cursor=%s last=%q", ok, s.Mode(), s.Cursor(), s.LastSearch().Pattern)
	}
	feedKeys(s, "?x")
	if q2, _ := s.IncSearch(); q2.Gen == q.Gen || !q2.Search.Backward || q2.Count != 1 || q2.Steps != 0 {
		t.Errorf("? の q = %+v（前の q = %+v）", q2, q)
	}
	feedKeys(s, "<Esc>:<C-g>")
	if s.CommandLine() != "" {
		t.Errorf(": の <C-g> が入った: %q", s.CommandLine())
	}
}

// 打つたびの検索の結果は、今の問いのものだけを受け取り、前の問いの結果は捨てる。
// 受け取った結果は、パターンを打ち足して次の結果を受け取るまで出し続け、一致の無い結果を受け取ったら出さない。
//
// req: FR-153
func TestIncSearchShow(t *testing.T) {
	s := setup(t, "|abc foo\nfoo bar")
	feedKeys(s, "/f")
	q1, _ := s.IncSearch()
	m1 := target.Match{Range: buffer.Range{Start: buffer.Position{Byte: 4}, End: buffer.Position{Byte: 5}}}
	if _, _, ok := s.IncSearchMatch(); ok {
		t.Error("結果を受け取る前に一致がある")
	}
	if !s.ShowIncSearch(q1, m1, true) {
		t.Fatal("今の問いの結果を捨てた")
	}
	feedKeys(s, "o")
	q2, _ := s.IncSearch()
	if pat, m, ok := s.IncSearchMatch(); !ok || pat != "f" || m != m1 {
		t.Errorf("打ち足した後の一致 = %q %v %v（次の結果までは前のものを出す）", pat, m, ok)
	}
	if s.ShowIncSearch(q1, m1, true) {
		t.Error("前の問いの結果を受け取った")
	}
	if !s.ShowIncSearch(q2, target.Match{}, false) {
		t.Fatal("今の問いの結果を捨てた")
	}
	if _, _, ok := s.IncSearchMatch(); ok {
		t.Error("一致の無い結果の後に一致がある")
	}
	feedKeys(s, "<BS><BS>")
	if _, _, ok := s.IncSearchMatch(); ok {
		t.Error("空のパターンで一致がある")
	}
	feedKeys(s, "<Esc>/f")
	if _, _, ok := s.IncSearchMatch(); ok {
		t.Error("取り消す前の入力の結果が残った")
	}
}

// 打っている間の <C-g> と <C-t> で動かした一致へ、<CR> で移る。動かす向きは / と ? によらず <C-g> がバッファの末尾の方で、
// 回数を付けたときは回数の数だけ動かす（target.FindMatch）。ジャンプリストと直前の検索は <CR> で決めたときだけ変わる。
// 期待値（<C-g> と <C-t> の後の <CR> の移り先）は、回数の無いケースを nvim 0.12.5 の --clean で 2026-11-20 に tmux で確かめた。
// ドットリピートは、動かした数を使わず、パターンと回数だけを繰り返す。
//
// req: FR-153
func TestIncSearchSteps(t *testing.T) {
	text := strings.Repeat("x\n", 49) + "x"
	lines := strings.Split(text, "\n")
	lines[1], lines[4], lines[34], lines[45] = "line 2 beta foo", "line 5 epsilon foo bar foo", "line 35 foobar here", "line 46 foo end"
	text = strings.Join(lines, "\n")
	for _, tc := range []struct{ start, keys, want string }{
		{"|" + text, "/foo<C-g><C-g><CR>", "4:23"},
		{"|" + text, "/foo<C-g><C-g><C-g><C-g><C-g><C-t><C-t><CR>", "34:8"},
		{"|" + text, "/foo<C-t><CR>", "45:8"},
		{strings.TrimSuffix(text, "x") + "|x", "?foo<C-g><CR>", "1:12"},
		{"|" + text, "3/foo<C-t><CR>", "45:8"},
		// 空のパターンの <C-g> と <C-t> は数えない（nvim 0.12.5 の /<C-g>foo<CR> は最初の一致へ移る）。
		{"|" + text, "/<C-g><C-g><C-t>foo<CR>", "1:12"},
	} {
		t.Run(tc.keys, func(t *testing.T) {
			s := setup(t, tc.start)
			rs := feedKeys(s, tc.keys)
			if err := rs[len(rs)-1].Err; err != nil {
				t.Fatal(err)
			}
			// どれも、打ち始めた位置から検索の向きに見て端を越えない一致なので、折り返しの警告を出さない
			// （nvim 0.12.5 の /foo<C-t><CR> は最後の一致へ移って W を出さない。PR #306 のレビューの推奨 2）。
			if m := rs[len(rs)-1].Message; m.Warning {
				t.Errorf("message = %+v（折り返していない）", m)
			}
			if got := s.Cursor().String(); got != tc.want {
				t.Errorf("cursor = %s, want %s", got, tc.want)
			}
			if s.LastSearch().Pattern != "foo" {
				t.Errorf("last search = %+v", s.LastSearch())
			}
		})
	}
	s := setup(t, "|a foo b foo c foo d foo")
	feedKeys(s, "d/foo<C-g><CR>")
	if got := s.buf.Text(); got != "foo c foo d foo" {
		t.Errorf("d/foo<C-g><CR> = %q", got)
	}
	feedKeys(s, ".")
	if got := s.buf.Text(); got != "foo d foo" {
		t.Errorf(". = %q（動かした数は繰り返さない）", got)
	}
}

// ビジュアルモードから / で打っている間は、選択を出し、動く側の端を打つたびの検索の一致の先頭にする。
// <Esc> で取り消すと、ビジュアルモードの選択は打ち始める前のままである。
//
// req: FR-153
func TestIncSearchShownSelection(t *testing.T) {
	s := setup(t, "abc |foo bar\nx foo")
	feedKeys(s, "lv/")
	if start, end, lw, ok := s.ShownSelection(); !ok || lw || start.String() != "0:5" || end.String() != "0:5" {
		t.Errorf("打つ前の選択 = %s %s %v %v", start, end, lw, ok)
	}
	if _, _, _, ok := s.Selection(); ok {
		t.Error("Selection がコマンドラインモードで選択を返した")
	}
	feedKeys(s, "foo")
	q, _ := s.IncSearch()
	s.ShowIncSearch(q, target.Match{Range: buffer.Range{Start: buffer.Position{Line: 1, Byte: 2}, End: buffer.Position{Line: 1, Byte: 5}}}, true)
	if start, end, _, ok := s.ShownSelection(); !ok || start.String() != "0:5" || end.String() != "1:2" {
		t.Errorf("一致まで広げた選択 = %s %s %v", start, end, ok)
	}
	feedKeys(s, "<Esc>")
	if start, end, _, ok := s.Selection(); s.Mode() != ModeVisual || !ok || start.String() != "0:5" || end.String() != "0:5" {
		t.Errorf("<Esc> の後: mode=%v 選択 = %s %s %v", s.Mode(), start, end, ok)
	}
	feedKeys(s, "<Esc>V?ab")
	q, _ = s.IncSearch()
	s.ShowIncSearch(q, target.Match{Range: buffer.Range{Start: buffer.Position{Byte: 0}, End: buffer.Position{Byte: 2}}}, true)
	if start, end, lw, ok := s.ShownSelection(); !ok || !lw || start.String() != "0:0" || end.String() != "0:5" {
		t.Errorf("V の ? の選択 = %s %s %v %v", start, end, lw, ok)
	}
}
