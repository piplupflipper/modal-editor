package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// setupInsertKeys は、setup と同じく text（"|" がカーソル）のバッファの State を作り、インサートモードの <C-w> と <C-u> に
// insert.delete-prev-word と insert.delete-to-line-start を割り当てる（同梱の config.example.jsonc と同じ割り当て）。
func setupInsertKeys(t *testing.T, text string) *State {
	t.Helper()
	s := setup(t, text)
	km := input.DefaultKeymap()
	for keys, id := range map[string]string{"<C-w>": "insert.delete-prev-word", "<C-u>": "insert.delete-to-line-start"} {
		b, err := input.UserBinding(input.MapInsert, editcmd.MustParseKeys(keys), id)
		if err != nil {
			t.Fatal(err)
		}
		if err := km.Bind(b); err != nil {
			t.Fatal(err)
		}
	}
	s.parser = input.NewParser(km)
	return s
}

// visualInsertText は、字下げの違う行、タブの字下げ、空の行、空白だけの行、多バイト文字の行を並べたバッファである。
// カーソルは 1 行目の先頭にある。
const visualInsertText = "|  abc\n\tdefg\n\n   \nあいう"

// ビジュアルモード（文字単位と行単位）の I と A は、選んだ行ごとに入力の位置を置き、すべての位置に同時に入力する。
// I の位置はその行でノーマルモードの I が入力を始める位置（空白だけの行と空の行では行末）、A の位置は行末である。
//
// A のテキストは、Vim 9.1 の矩形選択の $A（<C-v>4j$AX<Esc> など）のテキストと同じであることを確かめた（2026-11-20、
// vim -Nu NONE -n --not-a-term）。カーソルは、Vim が矩形の左上へ移すのに対し、ノーマルモードの A と同じく最初の行の入れた最後の文字にする。
//
// req: FR-141
func TestVisualInsert(t *testing.T) {
	tests := []struct {
		text, keys   string
		want, cursor string
		mode         Mode
	}{
		// V と v の A。Vim の <C-v>4j$AX<Esc> と同じテキスト。
		{visualInsertText, "V4jAX<Esc>", "  abcX\n\tdefgX\nX\n   X\nあいうX", "0:5", ModeNormal},
		{visualInsertText, "v4jAX<Esc>", "  abcX\n\tdefgX\nX\n   X\nあいうX", "0:5", ModeNormal},
		// 選んだ向きに依らない（下から上へ選ぶ）。カーソルは選んだ範囲の最初の行に置く。
		{"  abc\n\tdefg\n\n   \nあ|いう", "v4kAX<Esc>", "  abcX\n\tdefgX\nX\n   X\nあいうX", "0:5", ModeNormal},
		// V と v の I。字下げの違う行、タブ、空の行、空白だけの行（行末）、多バイト文字の行。
		{visualInsertText, "V4jIX<Esc>", "  Xabc\n\tXdefg\nX\n   X\nXあいう", "0:2", ModeNormal},
		{visualInsertText, "v4jIX<Esc>", "  Xabc\n\tXdefg\nX\n   X\nXあいう", "0:2", ModeNormal},
		// 多バイト文字の入力。Vim の <C-v>4j$Aあ,<Esc> と同じテキスト。
		{visualInsertText, "V4jAあ,<Esc>", "  abcあ,\n\tdefgあ,\nあ,\n   あ,\nあいうあ,", "0:8", ModeNormal},
		// 入力の途中の <BS>（前の文字の削除）。Vim の <C-v>4j$Aab<BS>c<Esc> と同じテキスト。
		{visualInsertText, "V4jAab<BS>c<Esc>", "  abcac\n\tdefgac\nac\n   ac\nあいうac", "0:6", ModeNormal},
		// 前の単語の削除。Vim の <C-v>4j$A// x<C-w>y<Esc> と同じテキスト。
		{visualInsertText, "V4jA// x<C-w>y<Esc>", "  abc// y\n\tdefg// y\n// y\n   // y\nあいう// y", "0:8", ModeNormal},
		// 行の先頭までの削除も、位置からの相対で書ける（それぞれの行の先頭まで消す）。
		{"  ab\n  cd", "VjA<C-u>X<Esc>", "X\nX", "0:0", ModeNormal},
		// 入力を始めた位置より前の文字も、それぞれの行で消す（字下げを 1 文字ずつ減らす）。
		{"  abc\n\tdefg", "VjI<BS>X<Esc>", " Xabc\nXdefg", "0:1", ModeNormal},
		// 途中の行から選ぶ。
		{visualInsertText, "jjVjAX<Esc>", "  abc\n\tdefg\nX\n   X\nあいう", "2:0", ModeNormal},
		// バッファの最後の行を含む範囲。Vim の G<C-v>k$AX<Esc> と同じテキスト。
		{visualInsertText, "GVkAX<Esc>", "  abc\n\tdefg\n\n   X\nあいうX", "3:3", ModeNormal},
		// 1 行だけの選択は、ノーマルモードの I と A と同じ。
		{visualInsertText, "VAX<Esc>", "  abcX\n\tdefg\n\n   \nあいう", "0:5", ModeNormal},
		// 打っている間から、すべての位置に入る。カーソルは最初の行の位置にある。
		{visualInsertText, "V2jAXY", "  abcXY\n\tdefgXY\nXY\n   \nあいう", "0:7", ModeInsert},
		{visualInsertText, "V2jIXY", "  XYabc\n\tXYdefg\nXY\n   \nあいう", "0:4", ModeInsert},
		// 回数は、それぞれの位置で入力を繰り返す（Vim 9.1 の <C-v>j2IX<Esc> と同じ）。
		{"abc\ndef", "Vj2AX<Esc>", "abcXX\ndefXX", "0:4", ModeNormal},
		// カーソルを動かすと、同時の入力を終える。それまでに入れたテキストはすべての位置に残り、その後はカーソルの位置だけに入る。
		{"abc\ndef", "VjAX<Left>Y<Esc>", "abcYX\ndefX", "0:3", ModeNormal},
		{"abc\ndef", "VjAX<Home>Y<Esc>", "YabcX\ndefX", "0:0", ModeNormal},
		// カーソルが動かない移動（行末の <End>）は、同時の入力を終えない（終えるかは、キーではなくカーソルが動いたかで決める）。
		// 入力も区切らないので、1 回の u ですべて戻り、. もすべての行に入れ直す。
		{"abc\ndef", "VjA,<End>;<Esc>", "abc,;\ndef,;", "0:4", ModeNormal},
		{"abc\ndef", "VjA,<End>;<Esc>u", "abc\ndef", "0:2", ModeNormal},
		{"abc\ndef\nghi\njkl", "VjA,<End>;<Esc>jj.", "abc,;\ndef,;\nghi,;\njkl,;", "2:4", ModeNormal},
		// 1 か所の入力では、Vim 9.1 と同じく動かない <End> でも入力を区切る（u は ; だけを戻す）。
		{"a|bc", "A,<End>;<Esc>u", "abc,", "0:3", ModeNormal},
		// 改行は位置の行の中に収まらないので、同時の入力を終え、カーソルの位置だけに入る。
		{"  abc\n  def", "VjIX<CR>Y<Esc>", "  X\nYabc\n  Xdef", "1:0", ModeNormal},
		// 行の先頭の <BS>（前の行とつなぐ）も行の中に収まらない。最初の行の <BS> はバッファの先頭で何もしない。
		{"ab\ncd", "VjI<BS>X<Esc>", "Xab\ncd", "0:0", ModeNormal},
		// 行末の <Del>（次の行をつなぐ）も同じ。
		{"ab\ncd\nef", "VjA<Del>X<Esc>", "abXcd\nef", "0:2", ModeNormal},
		// 1 回のアンドゥで、すべての位置が戻る。カーソルは Vim 9.1 の <C-v>4j$AX<Esc>u と同じく最初の行の最後の文字。
		{visualInsertText, "V4jAX<Esc>u", "  abc\n\tdefg\n\n   \nあいう", "0:4", ModeNormal},
		{visualInsertText, "V4jAX<Esc>u<C-r>", "  abcX\n\tdefgX\nX\n   X\nあいうX", "0:5", ModeNormal},
		// . は、カーソルの行から同じ数の行に位置を置き直して繰り返す（Vim 9.1 の <C-v>jIX<Esc>jj. と同じ範囲）。
		{"abc\ndef\nghi\njkl", "VjAX<Esc>jj.", "abcX\ndefX\nghiX\njklX", "2:3", ModeNormal},
		{"  a\n  b\n  c\n  d", "VjIX<Esc>jj.", "  Xa\n  Xb\n  Xc\n  Xd", "2:2", ModeNormal},
		// 行が足りなければ、バッファの最後の行で止める（Vim 9.1 の <C-v>jIX<Esc>G. と同じ）。
		{"abc\ndef\nghi", "VjAX<Esc>G.", "abcX\ndefX\nghiX", "2:3", ModeNormal},
		// . の後の u は、. の分だけ戻す。
		{"abc\ndef\nghi", "VjAX<Esc>j.u", "abcX\ndefX\nghi", "1:3", ModeNormal},
		// 回数を付けた . は、記録した値の回数を置き換える（. の汎用の規則。Vim の矩形選択の I の後の 3. は回数を使わない）。
		{"abc\ndef", "VjAX<Esc>2.", "abcXXX\ndefXXX", "0:5", ModeNormal},
		// バッファの最後の行で行が足りなくなった . の後も、次の . は記録した 3 行に置く
		// （Vim 9.1 の <C-v>2j$AX<Esc>4j.gg. と <C-v>2jIX<Esc>4j.gg. と同じテキスト。PR #274 のレビューの必須 2）。
		{"a\nb\nc\nd\ne\nf", "VjjAX<Esc>4j.gg.", "aXX\nbXX\ncXX\nd\neX\nfX", "0:2", ModeNormal},
		{"a\nb\nc\nd\ne\nf", "VjjIX<Esc>4j.gg.", "XXa\nXXb\nXXc\nd\nXe\nXf", "0:0", ModeNormal},
		// ノーマルモードの A を挟むと、. はその 1 行の A を繰り返す。
		{"abc\ndef\nghi", "VjAX<Esc>AY<Esc>j.", "abcXY\ndefXY\nghi", "1:4", ModeNormal},
		// カーソルを動かした後の入力は 1 か所の i として記録するので、. はそれを繰り返す。
		{"abc\ndef\nghi", "VjAX<Left>Y<Esc>jj.", "abcYX\ndefX\nghYi", "2:2", ModeNormal},
		// マクロの記録と再生。
		{"abc\ndef\nghi\njkl", "qaVjAX<Esc>qjj@a", "abcX\ndefX\nghiX\njklX", "2:3", ModeNormal},
		// ノーマルモードの I と A は今までどおり 1 か所。
		{"  a|bc\ndef", "IX<Esc>", "  Xabc\ndef", "0:2", ModeNormal},
		{"  a|bc\ndef", "AX<Esc>j.", "  abcX\ndefX", "1:3", ModeNormal},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q/%s", tt.text, tt.keys), func(t *testing.T) {
			s := setupInsertKeys(t, tt.text)
			for _, r := range feedKeys(s, tt.keys) {
				if r.Err != nil {
					t.Errorf("err = %v", r.Err)
				}
			}
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if got := s.Cursor().String(); got != tt.cursor {
				t.Errorf("cursor = %s, want %s", got, tt.cursor)
			}
			if s.Mode() != tt.mode {
				t.Errorf("mode = %v, want %v", s.Mode(), tt.mode)
			}
		})
	}
}

// ビジュアルモードの I と A は、ビジュアルモードを抜けて選択をマーク '< と '> に残す。. は Vim 9.1 と同じくマークを変えない。
//
// req: FR-141
func TestVisualInsertMarks(t *testing.T) {
	s := setup(t, "a|bc\ndef\nghi\njkl")
	feedKeys(s, "VjAX<Esc>")
	check := func(when string) {
		t.Helper()
		ls, okS := s.mark('<')
		le, okE := s.mark('>')
		if !okS || !okE || ls != 0 || le != 1 {
			t.Errorf("%s: marks = %d %v, %d %v", when, ls, okS, le, okE)
		}
	}
	check("A の後")
	feedKeys(s, "jj.")
	check(". の後")
}

// 端末からの貼り付けは、打った文字と同じく 1 文字ずつ入れる。改行の前までは、すべての位置に入る。
// 改行で同時の入力を終えるので、その後はカーソルの位置だけに入る（マクロの記録は打った文字と同じキーの列なので、再生でも同じ結果になる）。
//
// req: FR-141
func TestVisualInsertPaste(t *testing.T) {
	s := setup(t, "|abc\ndef")
	feedKeys(s, "VjA")
	s.Paste("12")
	if got := s.buf.Text(); got != "abc12\ndef12" {
		t.Errorf("text = %q", got)
	}
	s.Paste("x\ny")
	feedKeys(s, "<Esc>")
	if got := s.buf.Text(); got != "abc12x\ny\ndef12x" {
		t.Errorf("text = %q", got)
	}
}

// shiftByEdit は、internal/editor の shiftKept と同じ規則で、位置を 1 つの編集 ed の後の位置へずらす関数を返す
// （範囲より前は動かさない。範囲の内側は範囲の始まりへ寄せて inside を true にする。範囲の終わり以降は増減した行とバイトだけ動かす）。
// 実物の経路（applyExternal）は internal/editor のテストが見る。
func shiftByEdit(ed buffer.Edit) func(buffer.Position) (buffer.Position, bool) {
	return func(p buffer.Position) (buffer.Position, bool) {
		st, end := ed.Range.Start, ed.Range.End
		switch {
		case p.Less(st):
			return p, false
		case p.Less(end):
			return st, st != p
		}
		nl := strings.Count(ed.Text, "\n")
		newEnd := pos(st.Line+nl, st.Byte+len(ed.Text))
		if nl > 0 {
			newEnd.Byte = len(ed.Text) - strings.LastIndex(ed.Text, "\n") - 1
		}
		if p.Line == end.Line {
			return pos(newEnd.Line, newEnd.Byte+p.Byte-end.Byte), false
		}
		return pos(p.Line+newEnd.Line-end.Line, p.Byte), false
	}
}

// 入力の途中の外からの編集（ShiftPositions）は、同時の入力の位置もそれぞれずらす。
// 編集が置き換えた範囲の内側にあった位置、バッファの外へ出た位置、ほかの位置と同じ行へ寄った位置は落とす。
// I の位置（字下げの後）でも、残った位置は行の中の位置を保つ（PR #274 のレビューの必須 1 と推奨 3）。
//
// req: FR-141
func TestVisualInsertShift(t *testing.T) {
	tests := []struct {
		name, text, keys string
		edit             buffer.Edit
		want             string
	}{
		// 先頭に行を足す。すべての位置が 1 行下へずれる。
		{"先頭に行を足す", "|abc\ndef\nghi", "VjjAX", buffer.Edit{Text: "Z\n"}, "Z\nabcXY\ndefXY\nghiXY"},
		// 2 行目を消す。消した行の位置は範囲の内側なので落とし、3 行目の位置を残す。
		{"途中の行を消す", "|abc\ndef\nghi", "VjjAX", buffer.Edit{Range: buffer.Range{Start: pos(1, 0), End: pos(2, 0)}}, "abcXY\nghiXY"},
		{"I で途中の行を消す", "|  a\n  b\n  c", "VjjIX", buffer.Edit{Range: buffer.Range{Start: pos(1, 0), End: pos(2, 0)}}, "  XYa\n  XYc"},
		// 2 行目と 3 行目を消す。消した行の位置はカーソルの行の行末へ寄るので落とす。
		{"末尾の行を消す", "|abc\ndef\nghi", "VjjAX", buffer.Edit{Range: buffer.Range{Start: pos(0, 4), End: pos(2, 4)}}, "abcXY"},
		// 最後の行を、前の行の行末から消す（最後の行には後ろに改行が無いので、外からはこの形でしか消せない）。
		// 消した行の位置は前の行の行末へ寄るが、範囲の内側なので落とし、前の行の I の位置（字下げの後）を残す。
		{"I で最後の行を消す", "|  a\n  b\n  c", "VjjIX", buffer.Edit{Range: buffer.Range{Start: pos(1, 4), End: pos(2, 4)}}, "  XYa\n  XYb"},
		// 2 行目を丸ごと書き換える。2 行目の位置は範囲の内側なので落とし、行の先頭には入れない。ほかの位置は続ける。
		{"I で行を書き換える", "|  a\n  b\n  c", "VjjIX", buffer.Edit{Range: buffer.Range{Start: pos(1, 0), End: pos(1, 4)}, Text: "  Xbb"}, "  XYa\n  Xbb\n  XYc"},
		// 2 行目の位置より前に文字を足す。その位置はバイトの分だけずれる。
		{"位置の前に文字を足す", "|abc\ndef\nghi", "VjjAX", buffer.Edit{Range: buffer.Range{Start: pos(1, 0), End: pos(1, 0)}, Text: "Q"}, "abcXY\nQdefXY\nghiXY"},
		// 位置から後ろを消す。位置は範囲の始まりなので内側ではなく、そのまま残る。
		{"位置の後ろを消す", "|  ab\n  cd", "VjIX", buffer.Edit{Range: buffer.Range{Start: pos(1, 3), End: pos(1, 5)}}, "  XYab\n  XY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := setup(t, tt.text)
			feedKeys(s, tt.keys)
			if _, err := s.buf.Apply(buffer.Transaction{BaseVersion: s.buf.Version(), Edits: []buffer.Edit{tt.edit}}); err != nil {
				t.Fatal(err)
			}
			s.ShiftPositions(shiftByEdit(tt.edit))
			feedKeys(s, "Y<Esc>")
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

// ずらす関数がバッファの外を返した位置は落とす（読み直しのように、編集と合わない関数が渡ったとき）。
//
// req: FR-141
func TestVisualInsertShiftOutside(t *testing.T) {
	s := setup(t, "|abc\ndef\nghi")
	feedKeys(s, "VjjAX")
	s.ShiftPositions(func(p buffer.Position) (buffer.Position, bool) {
		if p.Line > 0 {
			return pos(p.Line+5, 0), false
		}
		return p, false
	})
	feedKeys(s, "Y<Esc>")
	if got := s.buf.Text(); got != "abcXY\ndefX\nghiX" {
		t.Errorf("text = %q", got)
	}
}

// 呼び出し側がカーソルを動かした（SetCursor）ら、カーソルキーと同じく同時の入力を終える。同じ位置へ置き直しても終えない。
//
// req: FR-141
func TestVisualInsertSetCursor(t *testing.T) {
	s := setup(t, "|abc\ndef")
	feedKeys(s, "VjAX")
	s.SetCursor(s.Cursor())
	feedKeys(s, "Y")
	s.SetCursor(pos(0, 0))
	feedKeys(s, "Z<Esc>")
	if got := s.buf.Text(); got != "ZabcXY\ndefXY" {
		t.Errorf("text = %q", got)
	}
}
