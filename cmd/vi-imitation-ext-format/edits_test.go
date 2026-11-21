package main

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
)

// offset は、本文 text の位置 p のバイトの位置を返す。範囲の外なら -1 を返す。
func offset(text string, p ext.Position) int {
	lines := strings.Split(text, "\n")
	if p.Line < 0 || p.Line >= len(lines) || p.Byte < 0 || p.Byte > len(lines[p.Line]) {
		return -1
	}
	off := 0
	for _, l := range lines[:p.Line] {
		off += len(l) + 1
	}
	return off + p.Byte
}

// apply は、本文 text にトランザクションの編集を逐次の数え方（ext.Transaction）で適用する。範囲の外を指す編集があれば失敗にする。
func apply(t *testing.T, text string, tx *ext.Transaction) string {
	t.Helper()
	if tx == nil {
		return text
	}
	for i, e := range tx.Edits {
		s, end := offset(text, e.Range.Start), offset(text, e.Range.End)
		if s < 0 || end < 0 || end < s {
			t.Fatalf("編集 %d %+v は %q の範囲の外を指す", i, e, text)
		}
		text = text[:s] + e.Text + text[end:]
	}
	return text
}

// 整形の結果は、元の本文の行の差分の塊ごとの編集にし、全文を置き換えない。適用すると整形の結果になる。
// 最後の行まで及ぶ塊（行を足す、消す、置き換える）と、すべての行を消すときも、範囲は本文の中を指す。
//
// req: FR-175
func TestFormatTransaction(t *testing.T) {
	for _, c := range []struct {
		name, old, formatted string
		edits                int
	}{
		{"同じ", "a\nb", "a\nb", 0},
		{"中の行を変える", "a\n  b\nc", "a\nb\nc", 1},
		{"離れた 2 行を変える", "x=1\nkeep\nkeep\nkeep\ny=2", "x = 1\nkeep\nkeep\nkeep\ny = 2", 2},
		{"最後の行を変える", "a\nb", "a\nB", 1},
		{"最後に行を足す", "a", "a\nb\nc", 1},
		{"最後の行を消す", "a\nb\nc", "a", 1},
		{"先頭に行を足す", "b", "a\nb", 1},
		{"先頭の行を消す", "a\nb", "b", 1},
		{"すべての行を消す", "a\nb", "", 1},
		{"空の本文に足す", "", "a\nb", 1},
		{"空の行を足す", "a\nb", "a\n\nb", 1},
		{"多バイト文字", "あ  い\nう", "あ い\nう", 1},
	} {
		tx := formatTransaction(c.old, c.formatted, 7, nil)
		if c.edits == 0 {
			if tx != nil {
				t.Errorf("%s: %+v, want nil", c.name, tx)
			}
			continue
		}
		if tx == nil || len(tx.Edits) != c.edits || tx.BaseVersion != 7 {
			t.Errorf("%s: %+v, want %d 個の編集と BaseVersion 7", c.name, tx, c.edits)
			continue
		}
		if got := apply(t, c.old, tx); got != c.formatted {
			t.Errorf("%s: 適用した結果 %q, want %q", c.name, got, c.formatted)
		}
	}

	// 1 行だけ変えた長い本文では、その行だけを置き換える。
	var lines []string
	for range 1000 {
		lines = append(lines, "line")
	}
	old := strings.Join(lines, "\n")
	lines[500] = "changed"
	tx := formatTransaction(old, strings.Join(lines, "\n"), 1, nil)
	if tx == nil || len(tx.Edits) != 1 || tx.Edits[0].Range.Start.Line != 500 || tx.Edits[0].Text != "changed\n" {
		t.Errorf("1 行の変更: %+v", tx)
	}
}

// 乱数で作った本文の組でも、編集を適用すると整形の結果になる。
//
// req: FR-175
func TestFormatTransactionRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // 再現できる無作為な本文を作るためで、暗号の用途ではない
	words := []string{"", "a", "b", "c", "  d", "é"}
	text := func() string {
		n := rng.IntN(8)
		ls := make([]string, n)
		for i := range ls {
			ls[i] = words[rng.IntN(len(words))]
		}
		return strings.Join(ls, "\n")
	}
	for range 3000 {
		old, formatted := text(), text()
		if got := apply(t, old, formatTransaction(old, formatted, 1, nil)); got != formatted {
			t.Fatalf("%q → %q: 適用した結果 %q", old, formatted, got)
		}
	}
}

// gq{モーション} の範囲があれば、範囲の行と重なる差分の塊だけを適用する。
// 行単位のモーションの範囲が次の行の先頭で終わるときは、その行を含めない。
//
// req: FR-176
func TestFormatTransactionRange(t *testing.T) {
	old := "x=1\nkeep\ny=2\nkeep\nz=3"
	formatted := "x = 1\nkeep\ny = 2\nkeep\nz = 3"
	for _, c := range []struct {
		name string
		rg   ext.Range
		want string
	}{
		{"2 行目から 3 行目", ext.Range{Start: ext.Position{Line: 1}, End: ext.Position{Line: 2, Byte: 3}}, "x=1\nkeep\ny = 2\nkeep\nz=3"},
		{"次の行の先頭で終わる", ext.Range{Start: ext.Position{Line: 0}, End: ext.Position{Line: 2}}, "x = 1\nkeep\ny=2\nkeep\nz=3"},
		{"最後の行", ext.Range{Start: ext.Position{Line: 4}, End: ext.Position{Line: 4, Byte: 3}}, "x=1\nkeep\ny=2\nkeep\nz = 3"},
		{"変わらない行だけ", ext.Range{Start: ext.Position{Line: 1}, End: ext.Position{Line: 1, Byte: 4}}, old},
	} {
		r := rangeLines(c.rg)
		if got := apply(t, old, formatTransaction(old, formatted, 1, &r)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	// 行を足すだけの塊は、足す位置が範囲の最初の行から最後の行の次の行までにあれば適用する。
	r := lineRange{first: 1, last: 1}
	if got := apply(t, "a\nb\nc", formatTransaction("a\nb\nc", "a\nb\n\nc", 1, &r)); got != "a\nb\n\nc" {
		t.Errorf("範囲の後ろに足す行: %q", got)
	}
	if got := apply(t, "a\nb\nc\nd", formatTransaction("a\nb\nc\nd", "a\nb\nc\n\nd", 1, &r)); got != "a\nb\nc\nd" {
		t.Errorf("範囲から離れて足す行: %q", got)
	}
}

// 整形ツールに渡す中身は、本体が書き出すときと同じく、末尾に改行が付くバッファでは改行を足し、出力の末尾の改行は 1 つ除いて本文にする。
//
// req: FR-175
func TestFileText(t *testing.T) {
	if got := fileText("a\nb", true); got != "a\nb\n" {
		t.Errorf("fileText(finalNewline) = %q", got)
	}
	if got := fileText("a\nb", false); got != "a\nb" {
		t.Errorf("fileText(!finalNewline) = %q", got)
	}
	for _, c := range []struct {
		out   string
		final bool
		want  string
	}{
		{"a\nb\n", true, "a\nb"},
		{"a\nb\n\n", true, "a\nb\n"},
		{"a\nb", true, "a\nb"},
		{"a\nb\n", false, "a\nb\n"},
	} {
		if got := bufferText(c.out, c.final); got != c.want {
			t.Errorf("bufferText(%q, %v) = %q, want %q", c.out, c.final, got, c.want)
		}
	}
}
