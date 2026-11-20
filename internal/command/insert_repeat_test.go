package command

import (
	"strconv"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// 回数付きの挿入は、記録した入力を回数だけ繰り返す。続けて並んだ文字をまとめて入れても（typeRepeated）、
// 削除の操作、改行、o と O の行を開く処理、同時の入力（FR-141）との組み合わせの結果は、1 件ずつ入れたときと変わらない。
// 期待値のテキストは Vim 9.1（vim -Nu NONE -n --not-a-term。同時の入力は <C-v>j$ の矩形選択の A と I）で確かめた。
// 同時の入力の途中の改行は、Vim の矩形選択と扱いが違う（改行で同時の入力を終える。typeAtSites）ので、Vim と比べず、
// 1 件ずつ入れていたとき（#279 の前）の結果を期待値にした。
//
// req: FR-02, FR-07, FR-141
func TestCountInsertRepeat(t *testing.T) {
	tests := []struct {
		text, keys   string
		want, cursor string
	}{
		{"|abc", "3ia<BS>b<Esc>", "bbbabc", "0:2"},
		{"|abc", "3iab<CR>c<Esc>", "ab\ncab\ncab\ncabc", "3:0"},
		{"a|bc", "3ixy<BS><BS>z<Esc>", "azzzbc", "0:3"},
		{"|abc", "3afoo<Esc>", "afoofoofoobc", "0:9"},
		{"|abc", "3ofoo<Esc>", "abc\nfoo\nfoo\nfoo", "3:2"},
		{"|abc", "3Ofoo<Esc>", "foo\nfoo\nfoo\nabc", "2:2"},
		{"|abc", "2ofoo<CR>bar<Esc>", "abc\nfoo\nbar\nfoo\nbar", "4:2"},
		{"|abc", "3aé<Esc>", "aééébc", "0:5"},
		{"|abc\ndef", "Vj3Axy<Esc>", "abcxyxyxy\ndefxyxyxy", "0:8"},
		{"|abc\ndef", "Vj3Ixy<Esc>", "xyxyxyabc\nxyxyxydef", "0:5"},
		{"|abc\ndef", "Vj3Aab<BS>c<Esc>", "abcacacac\ndefacacac", "0:8"},
		{"|abc\ndef", "Vj2Axy<CR>z<Esc>", "abcxy\nzxy\nz\ndefxy", "2:0"},
		// まとめている文字を消す <BS>（#289）。まとめている文字より前を消す <BS> と、改行の後の <BS> を含む。
		{"|abc", "3axy<BS>z<Esc>", "axzxzxzbc", "0:6"},
		{"|abc", "3iあい<BS>う<Esc>", "あうあうあうabc", "0:15"},
		{"a|bc", "3ix<BS><BS>y<Esc>", "ybc", "0:0"},
		{"|abc", "3ixy<BS>z<CR>w<BS><BS>q<Esc>", "xzqxzqxzqabc", "0:8"},
		{"|abc\ndef", "Vj3Axy<BS>z<Esc>", "abcxzxzxz\ndefxzxzxz", "0:8"},
		// 何もしなかった <BS> は記録しない（#289）。記録すると、繰り返しの <BS> が前の回に入れた文字を消す。
		{"|abc", "3ia<BS><BS>b<Esc>", "bbbabc", "0:2"},
		{"|abc", "3i<BS>x<Esc>", "xxxabc", "0:2"},
		{"|", "3i<BS>x<Esc>", "xxx", "0:2"},
		{"|abc", "2ia<BS><BS><BS>b<CR>c<Esc>", "b\ncb\ncabc", "2:0"},
		{"a|bc", "2ia<BS><BS><BS>b<CR>c<Esc>", "b\nb\ncbc", "2:0"},
		{"|abc\ndef", "Vj3Ia<BS><BS>b<Esc>", "bbbabc\nbbbdef", "0:2"},
	}
	for _, tt := range tests {
		t.Run(tt.text+"/"+tt.keys, func(t *testing.T) {
			s := setup(t, tt.text)
			feedKeys(s, tt.keys)
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
			feedKeys(s, "u")
			if got, want := s.buf.Text(), strings.ReplaceAll(tt.text, "|", ""); got != want {
				t.Errorf("after u: text = %q, want %q", got, want)
			}
		})
	}
}

// 長い行になる回数付きの挿入は、続けて並んだ文字を 1 つの変更にまとめて入れる（#279）。
// 前は 1 文字ずつ入れ、編集のたびに行を作り直して表示の列を数え直したので、100000afoo xy <Esc> が行の長さの 2 乗の時間を要し、
// 文字の数（70 万）だけの変更を <Esc> の結果に溜めた。
// ここでは回数を 2000 に減らし、変更の数で確かめる（まとめなければ 13993 個になる）。
// 回数を大きくすると、直しを外したときに、テストがアンドゥの記録の保持（buffer の TestApplyDoesNotRetainLines）でメモリを使い切る。
// Vim 9.1 では、100000afoo xy <Esc> が 700000 バイトの 1 行を作り、カーソルは最後の文字に来る。
//
// req: FR-02, FR-07
func TestCountInsertLongLine(t *testing.T) {
	const count = 2000
	s := setup(t, "")
	feedKeys(s, strconv.Itoa(count)+"afoo xy ")
	res := s.Feed(editcmd.MustParseKeys("<Esc>")[0])
	want := strings.Repeat("foo xy ", count)
	if got := s.buf.Text(); got != want {
		t.Fatalf("text has %d bytes, want %d", len(got), len(want))
	}
	if got := s.Cursor().String(); got != pos(0, len(want)-1).String() {
		t.Errorf("cursor = %s, want 0:%d", got, len(want)-1)
	}
	// 繰り返しの 1999 回分は、1 つの変更になる。
	if n := len(res.Changes); n != 1 {
		t.Errorf("<Esc> made %d changes, want 1", n)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "" {
		t.Errorf("after u: text has %d bytes", len(got))
	}
}

// <BS> を挟む回数付きの挿入も、まとめている文字を <BS> で落として 1 つの変更にする（#289）。
// 前は <BS> を 1 件ずつ行い、そのたびにまとめた文字を入れたので、100000axy<BS>z<Esc> は行の長さの 2 乗の時間（11.4 秒）を要し、
// 変更（拡張機能への buffer/change）を回数の 2 倍だけ溜めた。ここでは回数を 2000 にし、変更の数で確かめる（まとめなければ 3999 個）。
// Vim 9.1 では、100000axy<BS>z<Esc> が xz を 100000 個並べた 1 行を作り、カーソルは最後の文字に来る。
//
// req: FR-02, FR-07
func TestCountInsertBackspaceLongLine(t *testing.T) {
	const count = 2000
	s := setup(t, "")
	feedKeys(s, strconv.Itoa(count)+"axy<BS>z")
	res := s.Feed(editcmd.MustParseKeys("<Esc>")[0])
	want := strings.Repeat("xz", count)
	if got := s.buf.Text(); got != want {
		t.Fatalf("text has %d bytes, want %d", len(got), len(want))
	}
	if got := s.Cursor().String(); got != pos(0, len(want)-1).String() {
		t.Errorf("cursor = %s, want 0:%d", got, len(want)-1)
	}
	if n := len(res.Changes); n != 1 {
		t.Errorf("<Esc> made %d changes, want 1", n)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "" {
		t.Errorf("after u: text has %d bytes", len(got))
	}
}

// ドットリピートの再実行の 1 回目も、回数による繰り返しと同じく、続けて並んだ文字をまとめて 1 つの変更で入れる（#289）。
// 前は記録を 1 件ずつ入れたので、長いテキストを入れた後の . は、文字の数だけの変更を作り、行の長さの 2 乗の時間を要した。
// <BS> と改行を挟む記録で、テキスト、カーソル、u と <C-r> の結果も確かめる。
//
// req: FR-06
func TestDotRepeatLongInsert(t *testing.T) {
	const count = 2000
	typed := strings.Repeat("foo xy<BS> ", count)
	s := setup(t, "")
	feedKeys(s, "i"+typed+"<Esc>")
	res := feedKeys(s, ".")
	once := strings.Repeat("foo x ", count)
	// . は、最後の文字（空白）の前から入れる。
	want := once[:len(once)-1] + once + " "
	if got := s.buf.Text(); got != want {
		t.Fatalf("text has %d bytes, want %d", len(got), len(want))
	}
	if got := s.Cursor().String(); got != pos(0, 2*len(once)-2).String() {
		t.Errorf("cursor = %s, want 0:%d", got, 2*len(once)-2)
	}
	if n := len(res[0].Changes); n != 1 {
		t.Errorf(". made %d changes, want 1", n)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != once {
		t.Errorf("after u: text has %d bytes, want %d", len(got), len(once))
	}
	feedKeys(s, "<C-r>")
	if got := s.buf.Text(); got != want {
		t.Errorf("after <C-r>: text has %d bytes, want %d", len(got), len(want))
	}

	s = setup(t, "|abc")
	feedKeys(s, "ixy<BS>z<CR>w<BS>q<Esc>.")
	if got, want := s.buf.Text(), "xz\nxz\nqqabc"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if got := s.Cursor().String(); got != "2:0" {
		t.Errorf("cursor = %s, want 2:0", got)
	}
}

// 位置の前を消す操作（<BS>、<C-w>、<C-u>）をバッファの先頭で打ち、何もしなかったときは、入力の記録に入れない（#289）。
// 記録すると、回数による繰り返しとドットリピートで、その操作が前の回に入れた文字を消す。
// カーソルキーで区切った後に何もしなかったときは、入力を始め直さず、. は区切る前の入力を繰り返す。
// バッファの先頭のほかでは記録する。行の先頭の <C-u> は、こちらでは何もしない（#260 の決め）が、Vim 9.1 では前の行とつなぐので記録に入る。
// <Del> は、何もしなくても記録する（Vim 9.1 の ins_del と同じ）。
// 期待値は Vim 9.1（vim -Nu NONE -n --not-a-term、set bs=indent,eol,start）で確かめた（2026-11-21）。
// Vim と違う結果になる行は、違いと理由を書いた。
//
// req: FR-06, FR-07, FR-127
func TestInsertNoopDeleteNotRecorded(t *testing.T) {
	tests := []struct {
		text, keys   string
		want, cursor string
	}{
		{"|abc", "100ia<BS><BS>b<Esc>", strings.Repeat("b", 100) + "abc", "0:99"},
		{"|abc", "ia<BS><BS>b<Esc>3.", "bbbbabc", "0:2"},
		{"|abc", "3ia<BS><BS>bc<Esc>.", "bcbcbbcbcbccabc", "0:10"},
		{"|abc", "ixy<Left><Left><Left><BS><Esc>.", "xyxyabc", "0:1"},
		{"|abc", "3i<C-w>x<Esc>", "xxxabc", "0:2"},
		{"|abc", "3i<C-u>x<Esc>", "xxxabc", "0:2"},
		{"|abc", "3ia<C-w><C-w>b<Esc>", "babc", "0:0"},
		// 行の先頭（バッファの先頭でない）の <BS> と <C-w> は、前の行とつなぐ。
		{"abc\n|def", "3i<BS>x<Esc>", "abcxdef", "0:3"},
		{"abc\n|def ghi", "i<BS>x<Esc>$.", "abcxdef gxi", "0:9"},
		{"abc\n|def ghi", "i<C-w>x<Esc>$.", "abcxdef xi", "0:8"},
		// 繰り返しの <C-w> は、前の回の x と前の行を消す。Vim 9.1 は入力の始まりで止まり、abcxdef になる（#260 の決め。
		// docs/vim-differences.md）。
		{"abc\n|def", "3i<C-w>x<Esc>", "xdef", "0:0"},
		// 行の先頭の <C-u> は何もしないが、記録する（PR #314 のレビュー）。繰り返しと . の <C-u> は、位置から行の先頭までを消す。
		// Vim 9.1 は前の行とつなぎ、abcxdef と xi（1 行）になる（#260 の決め。docs/vim-differences.md）。
		{"abc\n|def", "3i<C-u>x<Esc>", "abc\nxdef", "1:0"},
		{"abc\n|def ghi", "i<C-u>x<Esc>$.", "abc\nxi", "1:0"},
		// 位置の後ろを消す <Del> は、何もしなくても記録する（Vim 9.1 の ins_del と同じ）。繰り返しでも何もしない。
		{"ab|c", "3a<Del>x<Esc>", "abcxxx", "0:5"},
		// 空のバッファでも記録するので、. は位置の文字を消してから入れる。
		{"|", "i<Del>x<Esc>0.", "x", "0:0"},
	}
	for _, tt := range tests {
		t.Run(tt.text+"/"+tt.keys, func(t *testing.T) {
			s := setupInsertKeys(t, tt.text)
			feedKeys(s, tt.keys)
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
		})
	}
}
