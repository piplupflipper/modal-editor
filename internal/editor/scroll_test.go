package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// scrollFile は、スクロールの突き合わせに使うファイルの種類である（scrollFileText）。
type scrollFile int

const (
	// scrollFileMixed は、100 行のファイルである。7 の倍数の行は "x"、それ以外の 5 の倍数の行は空、残りは "line N abcdefghijklmnop"。
	scrollFileMixed scrollFile = iota
	// scrollFileWrap は、折り返しの確かめに使う wrapFile（20 行。7 行目が 400 文字）である。
	scrollFileWrap
	// scrollFileWrap2 は、60 行のファイルである。7 行目は 400 文字、3 の倍数の行は "L{N}-" に 100 文字を続けた行、残りは "line{N}"。
	scrollFileWrap2
	// scrollFileLong は、横のスクロールの確かめに使う 20 行のファイルである。行は "{N}:" に "abcdefghij" を 12 回続けた行。
	scrollFileLong
	// scrollFileTallEnd は、"line1" から "line14" の 14 行と、1000 文字の 15 行目（最後の行が表示領域より高い）のファイルである。
	scrollFileTallEnd
	// scrollFileTall2000 は、40 行のファイルである。3 行目は 2000 文字、20 行目は 600 文字、残りは "line{N}"。
	scrollFileTall2000
)

// scrollFileText は、ファイルの種類 f の中身を返す。
func scrollFileText(f scrollFile) string {
	var lines []string
	switch f {
	case scrollFileMixed:
		for i := 1; i <= 100; i++ {
			switch {
			case i%7 == 0:
				lines = append(lines, "x")
			case i%5 == 0:
				lines = append(lines, "")
			default:
				lines = append(lines, fmt.Sprintf("line %d abcdefghijklmnop", i))
			}
		}
	case scrollFileWrap:
		return wrapFile()
	case scrollFileWrap2:
		for i := 1; i <= 60; i++ {
			switch {
			case i == 7:
				lines = append(lines, strings.Repeat("0123456789", 40))
			case i%3 == 0:
				lines = append(lines, fmt.Sprintf("L%d-", i)+strings.Repeat("abcdefghij", 10))
			default:
				lines = append(lines, fmt.Sprintf("line%d", i))
			}
		}
	case scrollFileLong:
		for i := 1; i <= 20; i++ {
			lines = append(lines, fmt.Sprintf("%d:", i)+strings.Repeat("abcdefghij", 12))
		}
	case scrollFileTallEnd:
		for i := 1; i <= 14; i++ {
			lines = append(lines, fmt.Sprintf("line%d", i))
		}
		lines = append(lines, strings.Repeat("abcdefghij", 100))
	case scrollFileTall2000:
		for i := 1; i <= 40; i++ {
			switch i {
			case 3:
				lines = append(lines, strings.Repeat("0123456789", 200))
			case 20:
				lines = append(lines, strings.Repeat("abcdefghij", 60))
			default:
				lines = append(lines, fmt.Sprintf("line%d", i))
			}
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// scrollStep は、スクロールの突き合わせの 1 つのキーと、その後の表示の位置とカーソルである。
// top と line は 1 から数えた最初の行とカーソルの行、col は 0 から数えたカーソルのバイトの位置、left は横のスクロールの位置（表示の列）である。
type scrollStep struct {
	keys                 string
	top, line, col, left int
}

// scrollCase は、Neovim 0.12.5 と突き合わせたスクロールのケースである。so と siso は scroll.margin と scroll.sideMargin、
// wrap は lineWrap.enabled、rows は表示領域の行数である。画面の幅は 41 列で、行番号の列は出さない。
type scrollCase struct {
	name     string
	file     scrollFile
	so, siso int
	wrap     bool
	rows     int
	steps    []scrollStep
}

// scrollCases の期待値は、2026-11-21 に Neovim 0.12.5 を `nvim --clean --headless` で起動し、'columns' を 41、'laststatus' を 2、
// 'lines' を rows+2 にして（折り返さないケースは 'nowrap'、横のスクロールのケースは 'sidescroll' を本体と同じ 0 にして）、
// ファイルの 1 行目から keys を 1 つずつ nvim_feedkeys で入力して :redraw を挟み、winsaveview() の topline、lnum、col、leftcol を読んだ値である。
// 42 ケース、437 キーで、すべて本体と一致した。
// G と gg は、Neovim の既定（'nostartofline'）では列を保つが、本体は Vim と同じく行の最初の空白でない文字へ移るので、
// 列が 0 でないときの G と gg はケースに入れていない（99j と 99k で代えた）。
var scrollCases = []scrollCase{
	{"ce-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-e>", 2, 2, 14, 0},
		{"3<C-e>", 5, 5, 0, 0},
		{"<C-e>", 6, 6, 14, 0},
		{"20<C-e>", 26, 26, 14, 0},
		{"<C-y>", 25, 26, 14, 0},
		{"2<C-y>", 23, 26, 14, 0},
	}},
	{"ce-end-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"G", 91, 100, 0, 0},
		{"<C-e>", 92, 100, 0, 0},
		{"<C-e>", 93, 100, 0, 0},
		{"5<C-e>", 98, 100, 0, 0},
		{"<C-y>", 97, 100, 0, 0},
		{"99<C-y>", 1, 10, 0, 0},
		{"<C-y>", 1, 10, 0, 0},
	}},
	{"cd-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-d>", 6, 6, 14, 0},
		{"<C-d>", 11, 11, 14, 0},
		{"3<C-d>", 14, 14, 0, 0},
		{"<C-d>", 17, 17, 14, 0},
		{"<C-u>", 14, 14, 0, 0},
		{"50<C-d>", 24, 24, 14, 0},
		{"<C-u>", 14, 14, 0, 0},
		{"99j", 91, 100, 0, 0},
		{"<C-d>", 91, 100, 0, 0},
		{"<C-u>", 81, 90, 0, 0},
		{"99k", 1, 1, 14, 0},
		{"<C-u>", 1, 1, 14, 0},
	}},
	{"cf-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"<C-f>", 9, 9, 0, 0},
		{"<C-f>", 17, 17, 0, 0},
		{"<C-b>", 9, 18, 0, 0},
		{"2<C-f>", 25, 25, 0, 0},
		{"G", 91, 100, 0, 0},
		{"<C-f>", 100, 100, 0, 0},
		{"<C-b>", 90, 99, 0, 0},
		{"<C-b>", 82, 91, 0, 0},
	}},
	{"z-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"20Gzt", 20, 20, 0, 0},
		{"zz", 16, 20, 0, 0},
		{"zb", 11, 20, 0, 0},
		{"40G", 36, 40, 0, 0},
		{"z<CR>", 40, 40, 0, 0},
		{"z.", 36, 40, 0, 0},
		{"z-", 31, 40, 0, 0},
		{"3zt", 3, 3, 0, 0},
		{"95zt", 95, 95, 0, 0},
		{"95zz", 91, 95, 0, 0},
		{"98zb", 89, 98, 0, 0},
		{"2zz", 1, 2, 0, 0},
		{"50zb", 41, 50, 0, 0},
	}},
	{"jk-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"8j", 2, 11, 0, 0},
		{"j", 3, 12, 0, 0},
		{"j", 4, 13, 0, 0},
		{"k", 4, 12, 0, 0},
		{"k", 4, 11, 0, 0},
		{"5k", 4, 6, 0, 0},
		{"k", 4, 5, 0, 0},
		{"G", 91, 100, 0, 0},
		{"k", 91, 99, 0, 0},
		{"15k", 80, 84, 0, 0},
		{"gg", 1, 1, 0, 0},
		{"50G", 46, 50, 0, 0},
		{"47G", 46, 47, 0, 0},
		{"60G", 51, 60, 0, 0},
	}},
	{"search-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"/line 33<CR>", 29, 33, 0, 0},
		{"n", 29, 33, 0, 0},
		{"/line 4<CR>", 32, 41, 0, 0},
		{"n", 34, 43, 0, 0},
		{"n", 35, 44, 0, 0},
		{"N", 35, 43, 0, 0},
		{"?line 1<CR>", 15, 19, 0, 0},
		{"N", 1, 1, 0, 0},
	}},
	{"vis-so0", scrollFileMixed, 0, 0, false, 10, []scrollStep{
		{"v", 1, 1, 0, 0},
		{"<C-d>", 6, 6, 0, 0},
		{"<C-e>", 7, 7, 0, 0},
		{"zt", 7, 7, 0, 0},
		{"<C-f>", 15, 15, 0, 0},
		{"<Esc>", 15, 15, 0, 0},
	}},
	{"small-so0", scrollFileMixed, 0, 0, false, 5, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"j", 1, 4, 0, 0},
		{"<C-d>", 3, 6, 0, 0},
		{"<C-e>", 4, 6, 0, 0},
		{"zt", 6, 6, 0, 0},
		{"zb", 2, 6, 0, 0},
		{"G", 96, 100, 0, 0},
		{"<C-y>", 95, 99, 0, 0},
	}},
	{"ce-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-e>", 2, 6, 14, 0},
		{"3<C-e>", 5, 9, 14, 0},
		{"<C-e>", 6, 10, 0, 0},
		{"20<C-e>", 26, 30, 0, 0},
		{"<C-y>", 25, 29, 14, 0},
		{"2<C-y>", 23, 27, 14, 0},
	}},
	{"ce-end-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"G", 91, 100, 0, 0},
		{"<C-e>", 92, 100, 0, 0},
		{"<C-e>", 93, 100, 0, 0},
		{"5<C-e>", 98, 100, 0, 0},
		{"<C-y>", 97, 100, 0, 0},
		{"99<C-y>", 1, 5, 0, 0},
		{"<C-y>", 1, 5, 0, 0},
	}},
	{"cd-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-d>", 6, 10, 0, 0},
		{"<C-d>", 11, 15, 0, 0},
		{"3<C-d>", 14, 18, 14, 0},
		{"<C-d>", 17, 21, 0, 0},
		{"<C-u>", 14, 18, 14, 0},
		{"50<C-d>", 24, 28, 0, 0},
		{"<C-u>", 14, 18, 14, 0},
		{"99j", 91, 100, 0, 0},
		{"<C-d>", 91, 100, 0, 0},
		{"<C-u>", 81, 85, 0, 0},
		{"99k", 1, 1, 14, 0},
		{"<C-u>", 1, 1, 14, 0},
	}},
	{"cf-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"<C-f>", 9, 13, 0, 0},
		{"<C-f>", 17, 21, 0, 0},
		{"<C-b>", 9, 13, 0, 0},
		{"2<C-f>", 25, 29, 0, 0},
		{"G", 91, 100, 0, 0},
		{"<C-f>", 100, 100, 0, 0},
		{"<C-b>", 90, 94, 0, 0},
		{"<C-b>", 82, 86, 0, 0},
	}},
	{"z-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"20Gzt", 16, 20, 0, 0},
		{"zz", 16, 20, 0, 0},
		{"zb", 15, 20, 0, 0},
		{"40G", 36, 40, 0, 0},
		{"z<CR>", 36, 40, 0, 0},
		{"z.", 36, 40, 0, 0},
		{"z-", 35, 40, 0, 0},
		{"3zt", 1, 3, 0, 0},
		{"95zt", 90, 95, 0, 0},
		{"95zz", 90, 95, 0, 0},
		{"98zb", 91, 98, 0, 0},
		{"2zz", 1, 2, 0, 0},
		{"50zb", 45, 50, 0, 0},
	}},
	{"jk-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"8j", 6, 11, 0, 0},
		{"j", 7, 12, 0, 0},
		{"j", 8, 13, 0, 0},
		{"k", 8, 12, 0, 0},
		{"k", 7, 11, 0, 0},
		{"5k", 2, 6, 0, 0},
		{"k", 1, 5, 0, 0},
		{"G", 91, 100, 0, 0},
		{"k", 91, 99, 0, 0},
		{"15k", 80, 84, 0, 0},
		{"gg", 1, 1, 0, 0},
		{"50G", 46, 50, 0, 0},
		{"47G", 43, 47, 0, 0},
		{"60G", 56, 60, 0, 0},
	}},
	{"search-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"/line 33<CR>", 29, 33, 0, 0},
		{"n", 29, 33, 0, 0},
		{"/line 4<CR>", 36, 41, 0, 0},
		{"n", 38, 43, 0, 0},
		{"n", 39, 44, 0, 0},
		{"N", 39, 43, 0, 0},
		{"?line 1<CR>", 15, 19, 0, 0},
		{"N", 1, 1, 0, 0},
	}},
	{"vis-so5", scrollFileMixed, 5, 0, false, 10, []scrollStep{
		{"v", 1, 1, 0, 0},
		{"<C-d>", 6, 10, 0, 0},
		{"<C-e>", 7, 11, 0, 0},
		{"zt", 7, 11, 0, 0},
		{"<C-f>", 15, 19, 0, 0},
		{"<Esc>", 15, 19, 0, 0},
	}},
	{"small-so5", scrollFileMixed, 5, 0, false, 5, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"j", 2, 4, 0, 0},
		{"<C-d>", 4, 6, 0, 0},
		{"<C-e>", 5, 7, 0, 0},
		{"zt", 5, 7, 0, 0},
		{"zb", 5, 7, 0, 0},
		{"G", 96, 100, 0, 0},
		{"<C-y>", 95, 97, 0, 0},
	}},
	{"ce-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-e>", 2, 6, 14, 0},
		{"3<C-e>", 5, 9, 14, 0},
		{"<C-e>", 6, 10, 0, 0},
		{"20<C-e>", 26, 30, 0, 0},
		{"<C-y>", 25, 29, 14, 0},
		{"2<C-y>", 23, 27, 14, 0},
	}},
	{"ce-end-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"G", 91, 100, 0, 0},
		{"<C-e>", 92, 100, 0, 0},
		{"<C-e>", 93, 100, 0, 0},
		{"5<C-e>", 98, 100, 0, 0},
		{"<C-y>", 97, 100, 0, 0},
		{"99<C-y>", 1, 5, 0, 0},
		{"<C-y>", 1, 5, 0, 0},
	}},
	{"cd-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"<C-d>", 6, 10, 0, 0},
		{"<C-d>", 11, 15, 0, 0},
		{"3<C-d>", 14, 18, 14, 0},
		{"<C-d>", 17, 21, 0, 0},
		{"<C-u>", 14, 18, 14, 0},
		{"50<C-d>", 24, 28, 0, 0},
		{"<C-u>", 14, 18, 14, 0},
		{"99j", 91, 100, 0, 0},
		{"<C-d>", 91, 100, 0, 0},
		{"<C-u>", 81, 85, 0, 0},
		{"99k", 1, 1, 14, 0},
		{"<C-u>", 1, 1, 14, 0},
	}},
	{"cf-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"<C-f>", 9, 13, 0, 0},
		{"<C-f>", 17, 21, 0, 0},
		{"<C-b>", 9, 13, 0, 0},
		{"2<C-f>", 25, 29, 0, 0},
		{"G", 91, 100, 0, 0},
		{"<C-f>", 100, 100, 0, 0},
		{"<C-b>", 90, 94, 0, 0},
		{"<C-b>", 82, 86, 0, 0},
	}},
	{"z-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"14l", 1, 1, 14, 0},
		{"20Gzt", 16, 20, 0, 0},
		{"zz", 16, 20, 0, 0},
		{"zb", 16, 20, 0, 0},
		{"40G", 36, 40, 0, 0},
		{"z<CR>", 36, 40, 0, 0},
		{"z.", 36, 40, 0, 0},
		{"z-", 36, 40, 0, 0},
		{"3zt", 1, 3, 0, 0},
		{"95zt", 91, 95, 0, 0},
		{"95zz", 91, 95, 0, 0},
		{"98zb", 91, 98, 0, 0},
		{"2zz", 1, 2, 0, 0},
		{"50zb", 46, 50, 0, 0},
	}},
	{"jk-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"8j", 7, 11, 0, 0},
		{"j", 8, 12, 0, 0},
		{"j", 9, 13, 0, 0},
		{"k", 8, 12, 0, 0},
		{"k", 7, 11, 0, 0},
		{"5k", 2, 6, 0, 0},
		{"k", 1, 5, 0, 0},
		{"G", 91, 100, 0, 0},
		{"k", 91, 99, 0, 0},
		{"15k", 80, 84, 0, 0},
		{"gg", 1, 1, 0, 0},
		{"50G", 46, 50, 0, 0},
		{"47G", 43, 47, 0, 0},
		{"60G", 56, 60, 0, 0},
	}},
	{"search-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"/line 33<CR>", 29, 33, 0, 0},
		{"n", 29, 33, 0, 0},
		{"/line 4<CR>", 37, 41, 0, 0},
		{"n", 39, 43, 0, 0},
		{"n", 40, 44, 0, 0},
		{"N", 39, 43, 0, 0},
		{"?line 1<CR>", 15, 19, 0, 0},
		{"N", 1, 1, 0, 0},
	}},
	{"vis-so999", scrollFileMixed, 999, 0, false, 10, []scrollStep{
		{"v", 1, 1, 0, 0},
		{"<C-d>", 6, 10, 0, 0},
		{"<C-e>", 7, 11, 0, 0},
		{"zt", 7, 11, 0, 0},
		{"<C-f>", 15, 19, 0, 0},
		{"<Esc>", 15, 19, 0, 0},
	}},
	{"small-so999", scrollFileMixed, 999, 0, false, 5, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"j", 2, 4, 0, 0},
		{"<C-d>", 4, 6, 0, 0},
		{"<C-e>", 5, 7, 0, 0},
		{"zt", 5, 7, 0, 0},
		{"zb", 5, 7, 0, 0},
		{"G", 96, 100, 0, 0},
		{"<C-y>", 95, 97, 0, 0},
	}},
	{"wrap-issue-so0", scrollFileWrap, 0, 0, true, 10, []scrollStep{
		{"G", 11, 20, 0, 0},
		{"<PageUp>", 8, 17, 0, 0},
		{"gj", 9, 18, 0, 0},
		{"gj", 10, 19, 0, 0},
		{"8G", 8, 8, 0, 0},
		{"j", 8, 9, 0, 0},
		{"j", 8, 10, 0, 0},
		{"k", 8, 9, 0, 0},
	}},
	{"wrap-ce-so0", scrollFileWrap2, 0, 0, true, 10, []scrollStep{
		{"<C-e>", 2, 2, 0, 0},
		{"<C-e>", 3, 3, 0, 0},
		{"<C-e>", 4, 4, 0, 0},
		{"<C-e>", 5, 5, 0, 0},
		{"<C-e>", 6, 6, 0, 0},
		{"<C-e>", 7, 7, 0, 0},
		{"<C-e>", 8, 8, 0, 0},
		{"<C-y>", 7, 7, 0, 0},
		{"<C-y>", 6, 6, 0, 0},
		{"<C-y>", 5, 6, 0, 0},
	}},
	{"wrap-cd-so0", scrollFileWrap2, 0, 0, true, 10, []scrollStep{
		{"<C-d>", 4, 4, 0, 0},
		{"<C-d>", 7, 7, 0, 0},
		{"<C-d>", 8, 8, 0, 0},
		{"<C-u>", 7, 7, 41, 0},
		{"<C-u>", 4, 5, 0, 0},
		{"<C-u>", 1, 2, 0, 0},
		{"<C-u>", 1, 1, 0, 0},
		{"30G", 28, 30, 0, 0},
		{"<C-d>", 31, 33, 0, 0},
		{"G", 55, 60, 0, 0},
		{"<C-u>", 52, 57, 0, 0},
	}},
	{"wrap-z-so0", scrollFileWrap2, 0, 0, true, 10, []scrollStep{
		{"9Gzt", 9, 9, 0, 0},
		{"zb", 8, 9, 0, 0},
		{"zz", 8, 9, 0, 0},
		{"7Gzt", 7, 7, 0, 0},
		{"zb", 7, 7, 0, 0},
		{"20G", 18, 20, 0, 0},
		{"zz", 18, 20, 0, 0},
		{"60Gzt", 60, 60, 0, 0},
		{"zb", 55, 60, 0, 0},
	}},
	{"wrap-gj-so0", scrollFileWrap2, 0, 0, true, 10, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"j", 1, 4, 0, 0},
		{"j", 1, 5, 0, 0},
		{"j", 1, 6, 0, 0},
		{"gj", 1, 6, 41, 0},
		{"gj", 1, 6, 82, 0},
		{"gj", 7, 7, 0, 0},
		{"gj", 7, 7, 41, 0},
		{"gj", 7, 7, 82, 0},
		{"gj", 7, 7, 123, 0},
		{"gj", 7, 7, 164, 0},
		{"gj", 7, 7, 205, 0},
		{"gj", 7, 7, 246, 0},
		{"gj", 7, 7, 287, 0},
		{"gj", 7, 7, 328, 0},
		{"gj", 7, 7, 369, 0},
		{"gk", 7, 7, 328, 0},
		{"gk", 7, 7, 287, 0},
		{"gk", 7, 7, 246, 0},
	}},
	{"wrap-cf-so0", scrollFileWrap2, 0, 0, true, 10, []scrollStep{
		{"<C-f>", 7, 7, 0, 0},
		{"<C-f>", 8, 8, 0, 0},
		{"<C-f>", 12, 12, 103, 0},
		{"<C-b>", 8, 13, 5, 0},
		{"<C-b>", 7, 7, 123, 0},
		{"<C-b>", 1, 6, 102, 0},
	}},
	{"wrap-issue-so5", scrollFileWrap, 5, 0, true, 10, []scrollStep{
		{"G", 11, 20, 0, 0},
		{"<PageUp>", 8, 12, 0, 0},
		{"gj", 8, 13, 0, 0},
		{"gj", 9, 14, 0, 0},
		{"8G", 8, 8, 0, 0},
		{"j", 8, 9, 0, 0},
		{"j", 8, 10, 0, 0},
		{"k", 8, 9, 0, 0},
	}},
	{"wrap-ce-so5", scrollFileWrap2, 5, 0, true, 10, []scrollStep{
		{"<C-e>", 2, 4, 0, 0},
		{"<C-e>", 3, 4, 0, 0},
		{"<C-e>", 4, 5, 0, 0},
		{"<C-e>", 5, 5, 0, 0},
		{"<C-e>", 6, 6, 0, 0},
		{"<C-e>", 7, 7, 0, 0},
		{"<C-e>", 8, 10, 0, 0},
		{"<C-y>", 7, 7, 0, 0},
		{"<C-y>", 6, 6, 0, 0},
		{"<C-y>", 5, 5, 0, 0},
	}},
	{"wrap-cd-so5", scrollFileWrap2, 5, 0, true, 10, []scrollStep{
		{"<C-d>", 4, 5, 0, 0},
		{"<C-d>", 7, 7, 41, 0},
		{"<C-d>", 8, 10, 0, 0},
		{"<C-u>", 7, 7, 205, 0},
		{"<C-u>", 4, 5, 0, 0},
		{"<C-u>", 1, 2, 0, 0},
		{"<C-u>", 1, 1, 0, 0},
		{"30G", 28, 30, 0, 0},
		{"<C-d>", 31, 34, 0, 0},
		{"G", 55, 60, 0, 0},
		{"<C-u>", 52, 55, 0, 0},
	}},
	{"wrap-z-so5", scrollFileWrap2, 5, 0, true, 10, []scrollStep{
		{"9Gzt", 8, 9, 0, 0},
		{"zb", 8, 9, 0, 0},
		{"zz", 8, 9, 0, 0},
		{"7Gzt", 7, 7, 0, 0},
		{"zb", 7, 7, 0, 0},
		{"20G", 18, 20, 0, 0},
		{"zz", 18, 20, 0, 0},
		{"60Gzt", 57, 60, 0, 0},
		{"zb", 55, 60, 0, 0},
	}},
	{"wrap-gj-so5", scrollFileWrap2, 5, 0, true, 10, []scrollStep{
		{"j", 1, 2, 0, 0},
		{"j", 1, 3, 0, 0},
		{"j", 1, 4, 0, 0},
		{"j", 1, 5, 0, 0},
		{"j", 1, 6, 0, 0},
		{"gj", 1, 6, 41, 0},
		{"gj", 1, 6, 82, 0},
		{"gj", 7, 7, 0, 0},
		{"gj", 7, 7, 41, 0},
		{"gj", 7, 7, 82, 0},
		{"gj", 7, 7, 123, 0},
		{"gj", 7, 7, 164, 0},
		{"gj", 7, 7, 205, 0},
		{"gj", 7, 7, 246, 0},
		{"gj", 7, 7, 287, 0},
		{"gj", 7, 7, 328, 0},
		{"gj", 7, 7, 369, 0},
		{"gk", 7, 7, 328, 0},
		{"gk", 7, 7, 287, 0},
		{"gk", 7, 7, 246, 0},
	}},
	{"wrap-cf-so5", scrollFileWrap2, 5, 0, true, 10, []scrollStep{
		{"<C-f>", 7, 7, 205, 0},
		{"<C-f>", 8, 10, 5, 0},
		{"<C-f>", 12, 14, 5, 0},
		{"<C-b>", 8, 10, 5, 0},
		{"<C-b>", 7, 7, 205, 0},
		{"<C-b>", 1, 3, 102, 0},
	}},
	{"side-siso0", scrollFileLong, 0, 0, false, 10, []scrollStep{
		{"34l", 1, 1, 34, 0},
		{"l", 1, 1, 35, 0},
		{"6l", 1, 1, 41, 21},
		{"2h", 1, 1, 39, 21},
		{"l", 1, 1, 40, 21},
		{"19l", 1, 1, 59, 21},
		{"40l", 1, 1, 99, 79},
		{"$", 1, 1, 121, 101},
		{"0", 1, 1, 0, 0},
		{"j", 1, 2, 0, 0},
		{"79l", 1, 2, 79, 59},
		{"50h", 1, 2, 29, 9},
		{"h", 1, 2, 28, 9},
		{"9h", 1, 2, 19, 9},
		{"e", 1, 2, 121, 101},
	}},
	{"side-siso8", scrollFileLong, 0, 8, false, 10, []scrollStep{
		{"34l", 1, 1, 34, 14},
		{"l", 1, 1, 35, 14},
		{"6l", 1, 1, 41, 14},
		{"2h", 1, 1, 39, 14},
		{"l", 1, 1, 40, 14},
		{"19l", 1, 1, 59, 39},
		{"40l", 1, 1, 99, 79},
		{"$", 1, 1, 121, 101},
		{"0", 1, 1, 0, 0},
		{"j", 1, 2, 0, 0},
		{"79l", 1, 2, 79, 59},
		{"50h", 1, 2, 29, 9},
		{"h", 1, 2, 28, 9},
		{"9h", 1, 2, 19, 9},
		{"e", 1, 2, 121, 101},
	}},
	{"side-siso999", scrollFileLong, 0, 999, false, 10, []scrollStep{
		{"34l", 1, 1, 34, 14},
		{"l", 1, 1, 35, 15},
		{"6l", 1, 1, 41, 21},
		{"2h", 1, 1, 39, 19},
		{"l", 1, 1, 40, 20},
		{"19l", 1, 1, 59, 39},
		{"40l", 1, 1, 99, 79},
		{"$", 1, 1, 121, 101},
		{"0", 1, 1, 0, 0},
		{"j", 1, 2, 0, 0},
		{"79l", 1, 2, 79, 59},
		{"50h", 1, 2, 29, 9},
		{"h", 1, 2, 28, 8},
		{"9h", 1, 2, 19, 0},
		{"e", 1, 2, 121, 101},
	}},
}

// 表示領域（6 行）より高い行（scrollFileWrap2 の 7 行目。41 列で 10 行の高さ）の中で動いたときの、最初の行の途中から見せる位置
// （隠す見た目の行の数）を、余白が 0 と 2 で Neovim 0.12.5 と突き合わせる。期待値は scrollCases と同じ取り方で、winsaveview() の
// skipcol を本文の幅（41）で割った値である。余白があると、カーソルの見た目の行の上下にも余白を残して隠す行を決める（curs_columns）。
//
// req: FR-174
func TestScrollTallLineNeovim(t *testing.T) {
	type step struct {
		keys                 string
		top, line, col, skip int
	}
	tests := []struct {
		so    int
		steps []step
	}{
		{0, []step{
			{"6j", 7, 7, 0, 0},
			{"gj", 7, 7, 41, 0},
			{"gj", 7, 7, 82, 0},
			{"gj", 7, 7, 123, 0},
			{"gj", 7, 7, 164, 0},
			{"gj", 7, 7, 205, 0},
			{"gj", 7, 7, 246, 1},
			{"gj", 7, 7, 287, 2},
			{"gj", 7, 7, 328, 3},
			{"gj", 7, 7, 369, 4},
			{"gk", 7, 7, 328, 4},
			{"gk", 7, 7, 287, 4},
			{"gk", 7, 7, 246, 4},
			{"gk", 7, 7, 205, 4},
			{"gk", 7, 7, 164, 4},
			{"gk", 7, 7, 123, 3},
			{"gk", 7, 7, 82, 2},
			{"gk", 7, 7, 41, 1},
			{"j", 8, 8, 4, 0},
			{"k", 7, 7, 41, 0},
		}},
		{2, []step{
			{"6j", 7, 7, 0, 0},
			{"gj", 7, 7, 41, 0},
			{"gj", 7, 7, 82, 0},
			{"gj", 7, 7, 123, 0},
			{"gj", 7, 7, 164, 1},
			{"gj", 7, 7, 205, 1},
			{"gj", 7, 7, 246, 2},
			{"gj", 7, 7, 287, 2},
			{"gj", 7, 7, 328, 3},
			{"gj", 7, 7, 369, 4},
			{"gk", 7, 7, 328, 4},
			{"gk", 7, 7, 287, 4},
			{"gk", 7, 7, 246, 4},
			{"gk", 7, 7, 205, 3},
			{"gk", 7, 7, 164, 2},
			{"gk", 7, 7, 123, 1},
			{"gk", 7, 7, 82, 0},
			{"gk", 7, 7, 41, 0},
			{"j", 8, 8, 4, 0},
			{"k", 7, 7, 41, 0},
			{"<C-e>", 8, 9, 41, 0},
			{"<C-y>", 7, 7, 41, 0},
			{"<C-d>", 8, 9, 0, 0},
			{"<C-u>", 7, 7, 82, 0},
			{"zt", 7, 7, 82, 0},
			{"zb", 7, 7, 82, 0},
			{"zz", 7, 7, 82, 0},
		}},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("so%d", tc.so), func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": scrollFileText(scrollFileWrap2)})
			term := newFakeTerm()
			term.w, term.h = 41, 6+3
			opts := map[string]option.Value{option.ScrollMargin: option.IntValue(tc.so), option.LineWrapEnabled: option.BoolValue(true)}
			h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
			h.settle()
			for i, st := range tc.steps {
				h.keys(st.keys)
				h.settle()
				want := fmt.Sprintf("%d %d:%d skip %d", st.top, st.line, st.col, st.skip)
				var got string
				h.e.do(func() {
					c := h.e.cur.state.Cursor()
					got = fmt.Sprintf("%d %d:%d skip %d", h.e.renderer.Top()+1, c.Line+1, c.Byte, h.e.renderer.Skip())
				})
				if got != want {
					t.Fatalf("%d 番目の %s の後: 最初の行 カーソル 隠す行 = %s, want %s", i+1, st.keys, got, want)
				}
			}
		})
	}
}

// PR #338 のレビューで見つかった境界を、Neovim 0.12.5 と突き合わせる（取り方は scrollCases と同じ。skip は winsaveview() の skipcol を 41 で割った値）。
//
//   - バッファの末尾の zz、zt、zb（余白 5）: Neovim は、描いた後の最後まで見える行の次の行が変わったときだけ余白を数え直す（必須 1）
//   - 表示領域より高い行へ <C-u> などで戻ったとき: カーソルが最初の行の左の端の "<<<" の下に来るなら、最初の行を決め直す（必須 2）
//
// req: FR-173, FR-174
func TestScrollEdgeNeovim(t *testing.T) {
	type step struct {
		keys                 string
		top, line, col, skip int
	}
	tests := []struct {
		name     string
		file     scrollFile
		so, rows int
		wrap     bool
		steps    []step
	}{
		{"end-zz-10", scrollFileMixed, 5, 10, false, []step{{"G", 91, 100, 0, 0}, {"zz", 96, 100, 0, 0}}},
		{"end-zz-9", scrollFileMixed, 5, 9, false, []step{{"G", 92, 100, 0, 0}, {"zz", 96, 100, 0, 0}}},
		{"end-zz-7", scrollFileMixed, 5, 7, false, []step{{"G", 94, 100, 0, 0}, {"zz", 97, 100, 0, 0}}},
		{"end-mix-7", scrollFileMixed, 5, 7, false, []step{
			{"G", 94, 100, 0, 0},
			{"zz", 97, 100, 0, 0},
			{"zb", 94, 100, 0, 0},
			{"zz", 97, 100, 0, 0},
			{"zt", 95, 100, 0, 0},
			{"zz", 97, 100, 0, 0},
			{"99G", 94, 99, 0, 0},
			{"zz", 96, 99, 0, 0},
		}},
		{"end-999zt-10", scrollFileMixed, 5, 10, false, []step{{"999zt", 95, 100, 0, 0}, {"zz", 96, 100, 0, 0}}},
		{"tallend-so2", scrollFileTallEnd, 2, 10, true, []step{
			{"G", 15, 15, 0, 0},
			{"<C-e>", 15, 15, 0, 0},
			{"<C-e>", 15, 15, 0, 0},
			{"<C-y>", 14, 14, 0, 0},
			{"zt", 12, 14, 0, 0},
			{"zz", 14, 14, 0, 0},
		}},
		{"marker-4G", scrollFileTall2000, 0, 10, true, []step{{"4G", 4, 4, 0, 0}, {"<C-u>", 3, 3, 41, 0}}},
		{"marker-2j", scrollFileTall2000, 0, 10, true, []step{{"2j", 3, 3, 0, 0}, {"<C-d>", 4, 4, 0, 0}, {"<C-u>", 3, 3, 41, 0}}},
		{"marker-3G", scrollFileTall2000, 0, 10, true, []step{{"3G", 3, 3, 0, 0}, {"<C-e>", 4, 4, 0, 0}, {"<C-u>", 3, 3, 41, 0}}},
		{"marker-15G", scrollFileTall2000, 0, 10, true, []step{
			{"15G", 11, 15, 0, 0}, {"<C-d>", 20, 20, 0, 0}, {"<C-d>", 21, 21, 0, 0}, {"<C-u>", 20, 20, 41, 0},
		}},
		// 表示領域より高い行の奥にカーソルがあるときの <C-y> は Neovim と違う（docs/vim-differences.md）。最後の <C-y> は本体の今の動きで、
		// Neovim 0.12.5 はカーソルを 3 行目の 820 バイト目に残す。
		{"deep", scrollFileTall2000, 0, 10, true, []step{
			{"3G", 3, 3, 0, 0}, {"<C-e>", 4, 4, 0, 0}, {"<C-y>", 3, 3, 0, 0}, {"20gj", 3, 3, 820, 11}, {"<C-y>", 2, 2, 4, 0},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": scrollFileText(tc.file)})
			term := newFakeTerm()
			term.w, term.h = 41, tc.rows+3
			opts := map[string]option.Value{option.ScrollMargin: option.IntValue(tc.so), option.LineWrapEnabled: option.BoolValue(tc.wrap)}
			h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
			h.settle()
			for i, st := range tc.steps {
				h.keys(st.keys)
				h.settle()
				want := fmt.Sprintf("%d %d:%d skip %d", st.top, st.line, st.col, st.skip)
				var got string
				h.e.do(func() {
					c := h.e.cur.state.Cursor()
					got = fmt.Sprintf("%d %d:%d skip %d", h.e.renderer.Top()+1, c.Line+1, c.Byte, h.e.renderer.Skip())
				})
				if got != want {
					t.Fatalf("%d 番目の %s の後: 最初の行 カーソル 隠す行 = %s, want %s", i+1, st.keys, got, want)
				}
			}
		})
	}
}

// 行を折り返すプレビューで、余白がある（scroll.margin が 1）ときも、<S-Down>（preview.page-down）の最初の行は描画で戻らない。
// プレビューのために作ったバッファのカーソルを、折り返した行の高さで数えて余白の外に入れるためである（PR #338 のレビューの推奨 3）。
//
// req: FR-174
func TestPreviewPageWrapMargin(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("%02d ", i+1) + strings.Repeat("abcdefghij", 30)
	}
	dir := writeFiles(t, map[string]string{"a.txt": strings.Join(lines, "\n") + "\n", "b.txt": "b\n"})
	opts := map[string]option.Value{option.ScrollMargin: option.IntValue(1), option.LineWrapEnabled: option.BoolValue(true)}
	h, ft := startPreview(t, dir, 200, 20, opts, "b.txt")
	f := openPreviewFloat(h, ext.ViewList, []ext.ViewItem{{ID: "a", Label: "a.txt", Preview: pvOf(filepath.Join(dir, "a.txt"))}})
	fireLast(h, ft, 0)
	waitPreview(h, f, "p:"+filepath.Join(dir, "a.txt")+"#-")
	var rows int
	h.e.do(func() { _, rows, _ = previewTop(h.e, f) })
	if rows < 6 {
		t.Fatalf("プレビューの行数 = %d", rows)
	}
	h.keys("<S-Down>")
	h.settle()
	h.keys("<S-Down>")
	h.settle()
	var top int
	h.e.do(func() { top, _, _ = previewTop(h.e, f) })
	if want := 2 * (rows - 2); top != want {
		t.Errorf("<S-Down> を 2 回: 最初の行 %d, want %d（0 から数える）", top, want)
	}
	// 上へ戻すと、カーソルは窓の下の端の近くへ入る。折り返した行の高さで数えないと、カーソルが窓の外に出て、描画が最初の行を下げる。
	h.keys("<S-Up>")
	h.settle()
	h.e.do(func() { top, _, _ = previewTop(h.e, f) })
	if want := rows - 2; top != want {
		t.Errorf("<S-Up>: 最初の行 %d, want %d（0 から数える）", top, want)
	}
}

// フォーカスを入れたポップアップの中と、プレビューへフォーカスを移した中でも、スクロールのキーと余白が効く（FR-173、FR-174）。
// 窓の中を描く子の Renderer の最初の行と、窓のバッファのカーソルが、同じ数え方で動く。
//
// req: FR-173, FR-174
func TestScrollInPopupAndPreview(t *testing.T) {
	t.Run("ポップアップ", func(t *testing.T) {
		lines := make([]string, 100)
		for i := range lines {
			lines[i] = fmt.Sprintf("popup %d", i+1)
		}
		term := newFakeTerm()
		term.w, term.h = 80, 30
		h := startTerm(t, term, Config{Options: map[string]option.Value{option.ScrollMargin: option.IntValue(2)}})
		h.settle()
		var f *floatState
		h.e.do(func() {
			f = h.e.newComponentFloat(ext.ViewComponent{Kind: ext.ViewText, Text: strings.Join(lines, "\n")}, "")
			f.center, f.focused = true, true
			h.e.openFloat(f)
		})
		h.settle()
		// state は、窓の最初の行（1 から数える）、窓の行数、カーソルの行（1 から数える）を返す。
		state := func() (top, rows, line int) {
			h.e.do(func() {
				sub := h.e.renderer.FloatRenderer()
				top, rows, line = sub.Top()+1, sub.TextRows(), f.bs.state.Cursor().Line+1
			})
			return top, rows, line
		}
		_, rows, _ := state()
		if rows < 6 {
			t.Fatalf("窓の行数 = %d", rows)
		}
		h.keys("<C-d>")
		h.settle()
		if top, _, line := state(); top != rows/2+1 || line != rows/2+1+2 {
			t.Errorf("<C-d>: 最初の行 %d、カーソル %d, want %d、%d（余白の 2 行を残す）", top, line, rows/2+1, rows/2+3)
		}
		h.keys("50Gzt")
		h.settle()
		if top, _, line := state(); top != 48 || line != 50 {
			t.Errorf("50Gzt: 最初の行 %d、カーソル %d, want 48、50", top, line)
		}
		h.keys("<C-e>")
		h.settle()
		if top, _, line := state(); top != 49 || line != 51 {
			t.Errorf("<C-e>: 最初の行 %d、カーソル %d, want 49、51", top, line)
		}
	})
	t.Run("プレビュー", func(t *testing.T) {
		dir := previewDir(t)
		h, ft := startPreview(t, dir, 200, 20, map[string]option.Value{option.ScrollMargin: option.IntValue(1)}, "b.txt")
		f := openPreviewFloat(h, ext.ViewList, []ext.ViewItem{{ID: "a", Label: "a.go", Preview: pvOf(filepath.Join(dir, "a.go"))}})
		fireLast(h, ft, 0)
		waitPreview(h, f, "p:"+filepath.Join(dir, "a.go")+"#-")
		h.keys("<C-w>w")
		h.settle()
		state := func() (top, rows, line int, ok bool) {
			h.e.do(func() {
				top, rows, ok = previewTop(h.e, f)
				line = f.preview.bs.state.Cursor().Line
			})
			return top, rows, line, ok
		}
		_, rows, _, ok := state()
		if !ok || rows < 6 {
			t.Fatalf("プレビュー: 行数 %d、描いた %v", rows, ok)
		}
		h.keys("<C-d>")
		h.settle()
		if top, _, line, _ := state(); top != rows/2 || line != rows/2+1 {
			t.Errorf("<C-d>: 最初の行 %d、カーソル %d, want %d、%d（0 から数える。余白の 1 行を残す）", top, line, rows/2, rows/2+1)
		}
		h.keys("20Gzt")
		h.settle()
		if top, _, line, _ := state(); top != 18 || line != 19 {
			t.Errorf("20Gzt: 最初の行 %d、カーソル %d, want 18、19（0 から数える）", top, line)
		}
	})
}

// スクロールのキー（FR-173）と、カーソルの上下と左右の余白（FR-174）を、Neovim 0.12.5 と突き合わせる（scrollCases の doc）。
// 各キーの回数あり、なし、バッファの端、折り返す長い行をまたぐとき、余白の各値（0、5、表示領域の半分を超える 999）での
// j、k、G、gg、n、gj、ビジュアルモード、#337 の issue のずれ（G <PageUp> gj gj 8G j j k）を含む。
//
// req: FR-173, FR-174
func TestScrollNeovim(t *testing.T) {
	for _, tc := range scrollCases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": scrollFileText(tc.file)})
			term := newFakeTerm()
			// タブの行、ステータス行、コマンドラインの行を除いて、表示領域を rows 行にする。
			term.w, term.h = 41, tc.rows+3
			opts := map[string]option.Value{
				option.ScrollMargin:     option.IntValue(tc.so),
				option.ScrollSideMargin: option.IntValue(tc.siso),
				option.LineWrapEnabled:  option.BoolValue(tc.wrap),
			}
			h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
			h.settle()
			for i, st := range tc.steps {
				h.keys(st.keys)
				h.settle()
				want := fmt.Sprintf("%d %d:%d left %d", st.top, st.line, st.col, st.left)
				var got string
				h.e.do(func() {
					c := h.e.cur.state.Cursor()
					got = fmt.Sprintf("%d %d:%d left %d", h.e.renderer.Top()+1, c.Line+1, c.Byte, h.e.renderer.Left())
				})
				if got != want {
					t.Fatalf("%d 番目の %s の後: 最初の行 カーソル 横の位置 = %s, want %s", i+1, st.keys, got, want)
				}
			}
		})
	}
}
