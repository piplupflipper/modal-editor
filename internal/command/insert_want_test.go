package command

import (
	"fmt"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// longWantText は、インサートモードのページ単位のスクロールとホイールの突き合わせに使う 30 行のテキストである。
// n 行目は "line<n> " の後に英小文字を (n×7) mod 23 文字続け、行ごとに長さを変える。
func longWantText() string {
	lines := make([]string, 30)
	for i := range lines {
		n := i + 1
		lines[i] = fmt.Sprintf("line%d ", n) + "abcdefghijklmnopqrstuvwxyz"[:n*7%23]
	}
	return strings.Join(lines, "\n")
}

// insertWantCase は、Neovim 0.12.5 と突き合わせた、インサートモードに入った後の上下の移動のケースである（#347）。
//
// 期待値は、2026-11-22 に、Neovim 0.12.5（nvim --clean -n、'noautoindent'、'showtabline' を 2、'laststatus' を 2）を
// tmux 3.6 の 40 列 12 行のペイン（表示領域は 9 行）で text のファイルを開いて起動し、keys を送った後のインサートモードの
// line('.')、col('.') と winsaveview() の curswant、line('w0') と、続けて <Esc> を押した後の line('.')、col('.') と curswant である。
// 記録は、インサートモードとノーマルモードの両方に割り当てた <F2> の <Cmd> で取った。
// curswant の MAXCOL は target.WantEOL にした。行と列は 1 から数え、列はバイトの位置である。
// --headless の Neovim は画面を描かないので、o の後などに curswant を古い列で数えることがあり、使わなかった。
//
// 同じ手順の Vim 9.1（vim -Nu NONE -n）も、<PageDown> と <PageUp> の 5 件のほかは同じ値だった。
// Vim 9.1 は 'startofline' が既定でオンなので、ページ単位のスクロールの後のカーソルが行の先頭へ移る（TestPageVim の doc）。
type insertWantCase struct {
	text       string
	keys       string
	insert     string // インサートモードのカーソル（行:列）
	insertWant int
	top        int    // 表示領域の最初の行（1 から数える）
	normal     string // <Esc> の後のカーソル（行:列）
	normalWant int
}

const (
	wantT1 = "abc\nabcdef"
	wantT3 = "abcdef\nab\nabcdefgh"
	wantT4 = "\tab\nabcdefghijkl\nx\ty"
)

var insertWantCases = []insertWantCase{
	// A の後は、行末の位置の列を望みの列にする。Vim の MAXCOL（$ と <End> の後）ではない。
	{wantT1, "A<Down>", "2:4", 3, 1, "2:3", 2},
	{wantT1, "A<Down><Up>", "1:4", 3, 1, "1:3", 2},
	{wantT1, "Ax<Down>", "2:5", 4, 1, "2:4", 3},
	{wantT1, "A<End><Down>", "2:7", target.WantEOL, 1, "2:6", 5},
	{wantT1, "A<Left><Down>", "2:3", 2, 1, "2:2", 1},
	{wantT1, "jA<Up>", "1:4", 6, 1, "1:3", 2},
	{wantT1, "jA<Up><Down>", "2:7", 6, 1, "2:6", 5},
	{wantT3, "A<Down><Down>", "3:7", 6, 1, "3:6", 5},
	{wantT3, "GA<Up><Up>", "1:7", 8, 1, "1:6", 5},
	// ほかの入り方も、入った位置の列を望みの列にする。ノーマルモードの望みの列（j の前の列、$ の行末）は引き継がない。
	{wantT1, "i<Down>", "2:1", 0, 1, "2:1", 0},
	{wantT1, "a<Down>", "2:2", 1, 1, "2:1", 0},
	{wantT1, "I<Down>", "2:1", 0, 1, "2:1", 0},
	{wantT1, "o<Up>", "1:1", 0, 1, "1:1", 0},
	{wantT1, "jO<Up>", "1:1", 0, 1, "1:1", 0},
	{wantT3, "$jA<Down>", "3:3", 2, 1, "3:2", 1},
	{wantT3, "$ji<Down>", "3:2", 1, 1, "3:1", 0},
	{wantT3, "5lji<Down>", "3:2", 1, 1, "3:1", 0},
	{wantT3, "5lja<Down>", "3:3", 2, 1, "3:2", 1},
	{wantT3, "5ljI<Down>", "3:1", 0, 1, "3:1", 0},
	{wantT3, "5ljA<Down>", "3:3", 2, 1, "3:2", 1},
	{wantT3, "5ljo<Down>", "4:1", 0, 1, "4:1", 0},
	{wantT3, "Gi<Up><Up>", "1:1", 0, 1, "1:1", 0},
	{wantT3, "G$i<Up><Up>", "1:7", 7, 1, "1:6", 5},
	// タブ文字の上のインサートモードのカーソルは、タブ文字の最初の列を望みの列にする（ノーマルモードは最後の列）。
	{wantT4, "i<Down>", "2:1", 0, 1, "2:1", 0},
	{wantT4, "i<Right><Down>", "2:9", 8, 1, "2:8", 7},
	{wantT4, "jA<Up>", "1:4", 12, 1, "1:3", 9},
	{wantT4, "G$i<Left><Up>", "2:2", 1, 1, "2:1", 0},
	{wantT4, "G$a<Up>", "2:10", 9, 1, "2:9", 8},
	// ページ単位のスクロールも、望みの列でカーソルの列を決める。
	{"long", "A<PageDown>", "8:14", 13, 8, "8:13", 12},
	{"long", "A<PageDown><PageUp>", "9:14", 13, 1, "9:13", 12},
	{"long", "A<Down><PageDown>", "8:14", 13, 8, "8:13", 12},
	{"long", "GA<PageUp>", "23:8", 10, 15, "23:7", 6},
	{"long", "$i<PageDown>", "8:13", 12, 8, "8:12", 11},
}

// TestInsertWantNeovim は、インサートモードに入った後の上下の移動とページ単位のスクロールが、入った位置の列を望みの列として保つことを
// Neovim 0.12.5 と突き合わせる（#347）。マウスのホイールは internal/editor の TestMouseWheelInsertWant で突き合わせる。
//
// req: FR-01
func TestInsertWantNeovim(t *testing.T) {
	for _, tc := range insertWantCases {
		text := tc.text
		if text == "long" {
			text = longWantText()
		}
		t.Run(fmt.Sprintf("%q/%s", tc.text, tc.keys), func(t *testing.T) {
			s := New(newBuffer(text), input.NewParser(input.DefaultKeymap()))
			vp := &fakeViewport{rows: 9}
			s.Viewport = vp
			s.Columns = tabColumns{}
			feedKeys(s, tc.keys)
			c := s.Cursor()
			if s.Mode() != ModeInsert {
				t.Fatalf("モード = %v, want インサートモード", s.Mode())
			}
			if got := fmt.Sprintf("%d:%d", c.Line+1, c.Byte+1); got != tc.insert || s.Want() != tc.insertWant || vp.top+1 != tc.top {
				t.Errorf("インサートモードのカーソル %s、望みの列 %d、最初の行 %d, want %s、%d、%d",
					got, s.Want(), vp.top+1, tc.insert, tc.insertWant, tc.top)
			}
			feedKeys(s, "<Esc>")
			c = s.Cursor()
			if got := fmt.Sprintf("%d:%d", c.Line+1, c.Byte+1); got != tc.normal || s.Want() != tc.normalWant {
				t.Errorf("<Esc> の後のカーソル %s、望みの列 %d, want %s、%d", got, s.Want(), tc.normal, tc.normalWant)
			}
		})
	}
}
