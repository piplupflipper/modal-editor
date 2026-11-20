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
// Vim 9.1（パッチ 1-16）の onepage() を写したものである。Vim の既定の 'scrolloff' は 0、'startofline' はオンである。
// 行の高さ（表示領域の何行を使うか）は、行を折り返して描くとき（s.ScreenLines。FR-156）は見た目の行の数で、そうでなければ 1 である。
// 下の「最後まで見える行」は、最初の行から行の高さを足して表示領域の行数に収まる行である。
//
//   - 前へ（<PageDown>）: 最後まで見える行の次の行（Vim の w_botline）から、重ねて残す行（pageOverlap）だけ戻った行を、新しい最初の行にする。
//     カーソルはその行へ移る。最後の行まで見えていれば、最後の行を最初の行にする。最後の行が最初の行なら失敗する。
//     最初の行が表示領域より高く、次の行が最初の行のままになるときは、1 行は進める
//   - 後ろへ（<PageUp>）: 表示領域の最初の行の前の行から、重ねて残す行だけ進んだ行を、新しい最後の行にする。
//     カーソルはその行へ移る。新しい最後の行までが入る最も上の行を新しい最初の行にし、それが 2 行目より上（Vim の 1 行目と 2 行目）になるなら、
//     最初の行を 0 にし、カーソルを最初の行から最後まで見える行より下に置かない。最初の行が 0 なら失敗する。
//     新しい最初の行が前の最初の行より上にならなければ、1 行は戻す（Vim の「少なくとも 1 行はスクロールする」）
//
// 成功したら、カーソルを行の最初の空白でない文字へ移す（'startofline'）。
// 回数の途中で失敗したら、そこまでのスクロールとカーソルの行を残し、列は変えずに失敗を返す（Vim と同じ）。
// 最初の回で失敗したら、何も変えない。
//
// 表示領域は描画のたびにカーソルが入るように動くが、描画の前に続けて動いたとき（マクロの再生など）は、カーソルが表示領域の外にありうる。
// そのときは、カーソルが表示領域の端の行に来るまで寄せてから動かす。
// s.Viewport が nil なら失敗する。
//
// req: FR-03, FR-156
func (s *State) scrollPage(forward bool, count int) error {
	vp := s.Viewport
	if vp == nil {
		return failf("no viewport")
	}
	n := s.buf.LineCount()
	h := max(vp.Rows(), 1)
	height := s.lineHeight()
	// botline は、最初の行を t にしたときに、最後まで見える行の次の行を返す。
	botline := func(t int) int {
		used := 0
		for ; t < n; t++ {
			used += height(t)
			if used > h {
				break
			}
		}
		return t
	}
	// topFor は、行 l の全体が表示領域の最後に入る、最も上の最初の行を返す。行 l が表示領域より高ければ l である。
	topFor := func(l int) int {
		t, used := l, height(l)
		for t > 0 && used+height(t-1) <= h {
			t--
			used += height(t)
		}
		return t
	}
	cur := s.cursor.Line
	top := min(max(vp.Top(), 0), n-1)
	switch {
	case cur < top:
		top = cur
	case cur >= botline(top):
		top = topFor(cur)
	}
	done := 0
	for ; done < count; done++ {
		prev := top
		if forward {
			if top >= n-1 {
				break
			}
			if next := botline(top); next >= n {
				top = n - 1
			} else {
				top = max(next-pageOverlap(n, h, next, -1, height), prev+1)
			}
			cur = top // 前へでは、カーソルは常に新しい最初の行へ移る（最後の行を最初の行にしたときも同じ行になる）
			continue
		}
		if top == 0 {
			break
		}
		cur = top - 1 + pageOverlap(n, h, top-1, 1, height)
		if t := topFor(cur); t <= 1 {
			top = 0
			cur = min(cur, botline(0)-1)
		} else {
			top = min(t, prev-1)
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
		s.followCursor()
		return failf("cannot scroll further")
	}
	s.cursor = s.firstNonBlankPos(cur)
	s.followCursor()
	s.updateWant()
	return nil
}

// pageOverlap は、ページ単位のスクロールで前の画面から重ねて残す行の数（0 から 2）を返す。
// Vim の get_scroll_overlap() を写したもので、start の行から dir（1 か -1）の向きの行を、height で数えた行の高さで数える。
// 重ねる行の高さの和が、表示領域の行数から 2 を引いた数を超えないようにする。どの行の高さも 1 なら、
// 表示領域が 3 行以下なら重ねず、4 行なら 1 行、5 行以上なら 2 行重ねる。どちらも、数える行がバッファの外に出るなら減らす。
func pageOverlap(n, h, start, dir int, height func(int) int) int {
	in := func(l int) bool { return l >= 0 && l < n }
	limit := h - 2
	h1 := height(start)
	if h1 > limit || !in(start+dir) {
		return 0
	}
	h2 := height(start + dir)
	if h2+h1 > limit || !in(start+2*dir) {
		return 0
	}
	h3 := height(start + 2*dir)
	if h3+h2 > limit {
		return 0
	}
	if !in(start + 3*dir) {
		return 1
	}
	if h4 := height(start + 3*dir); h4+h3+h2 > limit || h3+h2+h1 > limit {
		return 1
	}
	return 2
}

// lineHeight は、行の高さ（表示領域の何行を使うか）を返す関数を返す。行を折り返して描くとき（s.ScreenLines）は見た目の行の数で、
// そうでなければ 1 である。
func (s *State) lineHeight() func(int) int {
	sl := s.ScreenLines
	if sl == nil || !sl.Wrap() {
		return func(int) int { return 1 }
	}
	return func(l int) int { return sl.Rows(s.buf.Line(l)) }
}
