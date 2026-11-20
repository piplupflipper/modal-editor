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
