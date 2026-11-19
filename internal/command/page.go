package command

import "github.com/piplupflipper/vi-imitation/internal/editcmd"

// Viewport は、画面のバッファの表示領域である。ページ単位のスクロール（<PageUp>、<PageDown>）が読み書きする。
// 表示の状態（どの行から描くか）は描画の側が持つので、呼び出し側（internal/editor）が State.Viewport に差し込む。
type Viewport interface {
	// Top は、表示領域の最初の行の番号（0 から数える）を返す。
	Top() int
	// Rows は、表示領域の行数を返す。
	Rows() int
	// SetTop は、表示領域の最初の行を top にする。
	SetTop(top int)
}

// actPage は、<PageDown>（forward が true）と <PageUp> である。Vim の <C-f> と <C-b> に当たる。
// 回数の分だけ、画面の行数から重ねて残す行を引いた行数ずつスクロールする（scrollPage）。
//
// req: FR-03
func actPage(forward bool) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		return s.scrollPage(forward, max(cmd.Count, 1))
	}
}

// scrollPage は、表示領域を count ページだけ動かし、カーソルを動かす。
// Vim 9.1（パッチ 1-16）の onepage() を、どの行も画面の 1 行に収まる（折り返さない）前提で写したものである。
// Vim の既定の 'scrolloff' は 0、'startofline' はオンである。
//
//   - 前へ（<PageDown>）: 表示領域の下の端の次の行から、重ねて残す行（pageOverlap）だけ戻った行を、新しい最初の行にする。
//     カーソルはその行へ移る。最後の行が表示領域に入っていれば、最後の行を最初の行にする。最後の行が最初の行なら失敗する
//   - 後ろへ（<PageUp>）: 表示領域の最初の行の前の行から、重ねて残す行だけ進んだ行を、新しい最後の行にする。
//     カーソルはその行へ移る。新しい最初の行が 2 行目より上（Vim の 1 行目と 2 行目）になるなら、最初の行を 0 にし、
//     カーソルを表示領域の最後の行より下に置かない。最初の行が 0 なら失敗する
//
// 成功したら、カーソルを行の最初の空白でない文字へ移す（'startofline'）。
// 回数の途中で失敗したら、そこまでのスクロールとカーソルの行を残し、列は変えずに失敗を返す（Vim と同じ）。
// 最初の回で失敗したら、何も変えない。
//
// 表示領域は描画のたびにカーソルが入るように動くが、描画の前に続けて動いたとき（マクロの再生など）は、カーソルが表示領域の外にありうる。
// そのときは、カーソルが表示領域の端の行に来るまで寄せてから動かす。
// s.Viewport が nil なら失敗する。
func (s *State) scrollPage(forward bool, count int) error {
	vp := s.Viewport
	if vp == nil {
		return failf("no viewport")
	}
	n := s.buf.LineCount()
	h := max(vp.Rows(), 1)
	cur := s.cursor.Line
	top := min(max(vp.Top(), 0), n-1)
	switch {
	case cur < top:
		top = cur
	case cur >= top+h:
		top = cur - h + 1
	}
	done := 0
	for ; done < count; done++ {
		if forward {
			if top >= n-1 {
				break
			}
			if next := top + h; next >= n {
				top = n - 1
			} else {
				top = next - pageOverlap(n, h, next, -1)
			}
			cur = top // 前へでは、カーソルは常に新しい最初の行へ移る（最後の行を最初の行にしたときも同じ行になる）
			continue
		}
		if top == 0 {
			break
		}
		cur = top - 1 + pageOverlap(n, h, top-1, 1)
		if cur-h <= 0 {
			top = 0
			cur = min(cur, h-1)
		} else {
			top = cur - h + 1
		}
	}
	if done == 0 {
		return failf("cannot scroll further")
	}
	vp.SetTop(top)
	if done < count {
		p := s.clampPos(pos(cur, s.cursor.Byte))
		if s.mode != ModeInsert && !s.mode.visual() {
			p = s.clampNormal(p)
		}
		s.cursor = p
		return failf("cannot scroll further")
	}
	s.cursor = s.firstNonBlankPos(cur)
	s.updateWant()
	return nil
}

// pageOverlap は、ページ単位のスクロールで前の画面から重ねて残す行の数（0 から 2）を返す。
// Vim の get_scroll_overlap() を、どの行の高さも 1 として写したものである。start の行から dir（1 か -1）の向きの行を数える。
// 表示領域が 3 行以下なら重ねない。4 行なら 1 行、5 行以上なら 2 行重ねる。ただし、数える行がバッファの外に出るなら減らす。
func pageOverlap(n, h, start, dir int) int {
	in := func(l int) bool { return l >= 0 && l < n }
	switch {
	case h < 4 || !in(start+dir) || !in(start+2*dir):
		return 0
	case h < 5 || !in(start+3*dir):
		return 1
	}
	return 2
}
