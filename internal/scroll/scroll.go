// Package scroll は、バッファの表示領域の縦の位置（最初の行と、最初の行の途中から見せる位置）の数え方を持つ（基本設計 6.7 の「スクロール」）。
//
// 描画（internal/ui）がカーソルを表示領域に入れるときと、スクロールの操作（internal/command の <C-e>、<C-d>、<C-f>、zt など）が
// 表示の位置とカーソルの行を決めるときが、同じこのパッケージの数え方を使う。2 つのパッケージは同じ層で互いを import しないので、
// 数え方を 1 か所に置くために土台の層に置いた（#337）。
// カーソルの上下に残す余白（設定の scroll.margin。Vim の 'scrolloff'。FR-174）も、ここで数える。
// 動き方（j、検索、G、ジャンプ、スクロールのキー）ごとの分岐は持たない。
//
// 数え方は Neovim 0.12.5 の src/nvim/move.c（update_topline、scroll_cursor_top、scroll_cursor_bot、scroll_cursor_halfway、
// cursor_correct、scrollup、scrolldown、scroll_redraw、get_scroll_overlap、scroll_with_sms、curs_columns の w_skipcol の部分）を、
// 行の番号を 0 から数える形にして写した。本体に無いもの（折りたたみ、conceal、見比べの詰め物の行、'smoothscroll' の設定、
// 'scrolljump'、マウスのドラッグ）の分岐は写していない。'scrolljump' は既定の 1 である。
// Vim の w_skipcol（最初の行の途中から見せる位置）は、表示の列ではなく隠す見た目の行の数（View.Skip）で持つ。
//
// 基本設計 2 章の土台の層にあり、このリポジトリの他のパッケージを import しない。
// 1 つの View を使うのは、イベントループの goroutine 1 つだけである。
package scroll

// tall は、バッファの外の行の高さである（Neovim の MAXCOL）。足すと必ず表示領域を超える。
const tall = 1 << 30

// Lines は、表示領域に描くバッファの行の高さである。
type Lines interface {
	// LineCount は、行の数（1 以上）を返す。
	LineCount() int
	// Rows は、行 line（0 から数える）を描く見た目の行の数（1 以上）を返す。行を折り返さなければ 1 である。
	// 表示領域の行数で頭打ちにしない（必要な所でこのパッケージが頭打ちにする）。
	Rows(line int) int
}

// View は、1 つの表示領域の縦の位置と、数えるための入力である。
// 操作（Follow、CursorTop など）は Top と Skip を書き換える。カーソルは引数で受け取り、動かす操作は新しい行を返す。
type View struct {
	// Lines は、表示領域に描くバッファの行の高さである。
	Lines Lines
	// Height は、表示領域の行数である。0 以下なら、どの操作も最初の行をカーソルの行にするだけにする（Neovim の update_topline の
	// w_view_height が 0 のときと同じ）。
	Height int
	// Width は、行を折り返すときの本文の幅（列数）である。Neovim の w_skipcol を表示の列で数える式を、隠す見た目の行の数で
	// 写すときの換算に使う（ScrollSmooth）。折り返さなければ使わない。
	Width int
	// Margin は、カーソルの上下に残す余白の行数（Vim の 'scrolloff'）である。負の値は 0 として扱う。
	Margin int
	// Top は、表示領域の最初の行（0 から数える）である。
	Top int
	// Skip は、最初の行を折り返した見た目の行のうち、表示領域より上に隠す行の数（Vim の w_skipcol を見た目の行で数えたもの）である。
	Skip int
	// SmoothCursor は、見た目の行で送る ScrollRedraw（smooth が true）の最後に呼ぶ、カーソルの補正である（Neovim の cursor_correct_sms）。
	// カーソルの行 cursor を受け取り、補正した行を返す。カーソルの列と望みの列は呼び出し側が持つので、補正は呼び出し側が書く。nil なら呼ばない。
	SmoothCursor func(v *View, cursor int) int
}

// count は、行の数を返す。
func (v *View) count() int { return max(v.Lines.LineCount(), 1) }

// so は、余白の行数を返す（Neovim の get_scrolloff_value）。
func (v *View) so() int { return max(v.Margin, 0) }

// plines は、行 l の見た目の行の数を返す。limit なら表示領域の行数で頭打ちにする（Neovim の plines_win_nofill）。
// バッファの外の行は tall である。
func (v *View) plines(l int, limit bool) int {
	if l < 0 || l >= v.count() {
		return tall
	}
	h := max(v.Lines.Rows(l), 1)
	if limit && h > v.Height {
		return max(v.Height, 1)
	}
	return h
}

// plinesTop は、行 l の見た目の行の数を、最初の行なら隠した行を引き、表示領域の行数で頭打ちにして返す
// （Neovim の plines_correct_topline の limit_winheight が true のとき）。
func (v *View) plinesTop(l int) int {
	h := v.plines(l, false)
	if l == v.Top {
		h -= v.Skip
	}
	return min(max(h, 0), max(v.Height, 1))
}

// Botline は、表示領域に最後まで見える行の次の行（Neovim の w_botline）と、見える行の下に空いた表示領域の行数（w_empty_rows）を返す。
// 最後の行まで見えていれば、bot は行の数である。最初の行が表示領域より高いとき（1 行も最後まで見えない）は、空いた行数を 0 にする。
// Neovim の comp_botline と set_empty_rows を写した。
func (v *View) Botline() (bot, empty int) {
	n := v.count()
	done := 0
	l := v.Top
	for ; l < n; l++ {
		h := v.plinesTop(l)
		if done+h > v.Height {
			break
		}
		done += h
	}
	if done == 0 {
		return l, 0
	}
	return l, v.Height - done
}

// setTop は、最初の行を top にする。行が変われば、隠す見た目の行を 0 にする（'smoothscroll' がオフのときの reset_skipcol）。
func (v *View) setTop(top int) {
	if top != v.Top {
		v.Skip = 0
	}
	v.Top = top
}

// checkTopOffset は、カーソルの行 cursor の上に、余白の行数だけの見た目の行が表示領域の中に無いかを返す（Neovim の check_top_offset）。
func (v *View) checkTopOffset(cursor int) bool {
	so := v.so()
	if cursor >= v.Top+so {
		return false
	}
	n := 0
	for l := cursor - 1; n < so; l-- {
		if l < v.Top {
			break
		}
		n += v.plines(l, true)
	}
	return n < so
}

// Follow は、カーソル（行 cursor の、折り返した row 番目の見た目の行）が、余白を残して表示領域に入るように最初の行と Skip を決める。
// 描画が描くたびに呼ぶ。Neovim の update_topline と、curs_columns の w_skipcol を決める部分を写した。
//
//   - カーソルが最初の行より上か、上の余白が足りなければ、離れが小さいときはカーソルの行を上の余白の位置に置き（CursorTop）、
//     大きいとき（max(Height/2-1, 2) 行以上）は中ほどに置く（CursorHalfway）
//   - カーソルが表示領域の下へ出たか、下の余白が足りなければ、離れが小さいときは下の余白の位置に置き（CursorBot）、
//     大きいとき（Height+1 行より多い）は中ほどに置く
//   - カーソルの行が最初の行で、表示領域より高ければ、カーソルの見た目の行が余白を残して見えるように Skip を決める（fitSkip）
//
// req: FR-174
func (v *View) Follow(cursor, row int) {
	n := v.count()
	cursor = min(max(cursor, 0), n-1)
	if v.Height <= 0 {
		v.Top, v.Skip = cursor, 0
		return
	}
	so := v.so()
	oldTop := v.Top
	checkBot := false
	checkTop := false
	if v.Top > 0 || v.Skip > 0 {
		switch {
		case cursor < v.Top:
			checkTop = true
		case v.checkTopOffset(cursor):
			checkTop = true
		case v.Skip > 0 && cursor == v.Top && v.Skip > row:
			checkTop = true
		}
	}
	if checkTop {
		half := max(v.Height/2-1, 2)
		if v.Top+so-cursor >= half {
			v.CursorHalfway(cursor, false, false)
		} else {
			v.CursorTop(cursor, row, 1, false)
			checkBot = true
		}
	} else {
		checkBot = true
	}
	if checkBot {
		bot, empty := v.Botline()
		if bot < n {
			if cursor < bot {
				if cursor >= bot-so {
					// カーソルの下に、余白の行数だけの見た目の行があるかを数える。
					c := empty
					h := 0
					for l := cursor; l < bot; {
						c += h
						if c >= so {
							break
						}
						l++
						h = v.plines(l, true)
					}
					if c >= so {
						checkBot = false
					}
				} else {
					checkBot = false
				}
			}
			if checkBot {
				if cursor-bot+1+so <= v.Height+1 {
					v.CursorBot(cursor, 1, false)
				} else {
					v.CursorHalfway(cursor, false, false)
				}
			}
		}
	}
	if v.Top != oldTop {
		v.Skip = 0
	}
	v.fitSkip(cursor, row)
}

// KeepTop は、最初の行を変えずに、Skip だけを Follow と同じく決める（Neovim の curs_columns が、最初の行が決まっている
// （VALID_TOPLINE）ときも w_skipcol を決め直すのと同じ）。スクロールの操作が決めた最初の行を、描画で動かさないときに使う。
func (v *View) KeepTop(cursor, row int) {
	if v.Height <= 0 {
		return
	}
	v.fitSkip(min(max(cursor, 0), v.count()-1), row)
}

// fitSkip は、カーソルの行が最初の行で表示領域より高いとき、カーソルの見た目の行 row が余白を残して見えるように Skip を決める。
// そうでなければ Skip を 0 にする。Neovim の curs_columns の「Cursor past end of screen」の部分を、表示の列を見た目の行の数にして写した。
func (v *View) fitSkip(cursor, row int) {
	if cursor != v.Top || v.Height <= 0 {
		v.Skip = 0
		return
	}
	h, so := v.Height, v.so()
	prev := v.Skip
	// wrow は、カーソルの見た目の行の、表示領域の最初の行からの行数である。カーソルが隠した行にあれば、引かない（Neovim と同じ）。
	wrow := row
	if v.Skip > 0 && row >= v.Skip {
		wrow -= v.Skip
	}
	plines := 0
	tallLine := false
	if wrow >= h {
		tallLine = true
	} else if prev > 0 || wrow+so >= h {
		plines = v.plines(cursor, false)
		tallLine = plines-1 >= h
	}
	if !tallLine {
		v.Skip = 0
		return
	}
	extra := 0
	if v.Skip+so > row {
		extra = 1
	}
	if plines == 0 {
		plines = v.plines(cursor, false)
	}
	plines--
	n := plines
	if plines > wrow+so {
		n = wrow + so
	}
	if n >= h+v.Skip-so {
		extra += 2
	}
	switch {
	case extra == 3 || h <= so*2:
		// 余白を残す場所が無いので、カーソルを中ほどに置く。
		n = row
		if n > h/2 {
			n -= h / 2
		} else {
			n = 0
		}
		n = min(n, plines-h+1)
		v.Skip = max(n, 0)
	case extra == 1:
		// 上の余白が足りないので、隠す行を減らす。
		e := v.Skip + so - row
		if e > 0 {
			v.Skip -= min(e, v.Skip)
		}
	case extra == 2:
		// 下の余白が足りないので、隠す行を増やす。
		v.Skip = max(v.Skip, min(n-h+1, row))
	}
	if row-v.Skip >= h {
		v.Skip += row - v.Skip - h + 1
	}
}

// CursorTop は、カーソルの行 cursor を、上の余白を残した表示領域の上の端の近くに置く（Neovim の scroll_cursor_top）。
// minScroll は、スクロールするなら少なくとも動かす見た目の行の数（'scrolljump'）である。always なら必ず最初の行を決め直す（zt）。
// そうでなければ、最初の行を上へ動かすときだけ変える。余白を残す場所が無ければ、中ほどに置く（CursorHalfway）。
// row は、カーソルの見た目の行で、最初の行を変えないときに Skip を戻すかの判定に使う。
func (v *View) CursorTop(cursor, row, minScroll int, always bool) {
	n, off := v.count(), v.so()
	scrolled := 0
	used := v.plines(cursor, true)
	if cursor < v.Top {
		scrolled = used
	}
	top, bot := cursor-1, cursor+1
	newTop := top + 1
	extra := 0
	for top >= 0 {
		i := v.plines(top, true)
		if top < v.Top {
			scrolled += i
		}
		if (newTop >= v.Top || scrolled > minScroll) && extra >= off {
			break
		}
		used += i
		if extra+i <= off && bot < n-1 {
			used += v.plines(bot, true)
		}
		if used > v.Height {
			break
		}
		extra += i
		newTop = top
		top--
		bot++
	}
	if used > v.Height {
		v.CursorHalfway(cursor, false, false)
		return
	}
	t := v.Top
	if newTop < t || always {
		t = newTop
	}
	t = min(t, cursor)
	switch {
	case t != v.Top:
		v.Top, v.Skip = t, 0
	case t == cursor && v.Skip > row:
		v.Skip = 0
	}
}

// CursorBot は、カーソルの行 cursor を、下の余白を残した表示領域の下の端の近くに置く（Neovim の scroll_cursor_bot）。
// minScroll は CursorTop と同じである。setTopBot なら、カーソルの行が下の端に来る最初の行から数え直す（zb）。
// 動かす量が表示領域の行数以上なら、中ほどに置く（CursorHalfway の preferAbove を true にして）。
func (v *View) CursorBot(cursor, minScroll int, setTopBot bool) {
	n, so := v.count(), v.so()
	var bot, empty int
	if setTopBot {
		oldTop := v.Top
		used := 0
		bot = cursor + 1
		for l := cursor; l >= 0; l-- {
			h := v.plines(l, false)
			if used+h > v.Height {
				break
			}
			v.Top = l
			used += h
		}
		if used != 0 {
			empty = v.Height - used
		}
		if v.Top != oldTop || v.Skip != 0 {
			v.Skip = 0
		}
	} else {
		bot, empty = v.Botline()
	}
	used := v.plines(cursor, true)
	scrolled := 0
	if cursor >= bot {
		scrolled = used
		if cursor == bot {
			scrolled -= empty
		}
	}
	loff, boff := cursor, cursor
	extra := 0
	for loff > 0 {
		if (((scrolled <= 0 || scrolled >= minScroll) && extra >= so) || boff >= n-1) && loff <= bot {
			break
		}
		before := loff
		loff--
		h := v.plines(loff, true)
		if h == tall {
			used = tall
		} else {
			used += h
		}
		if used > v.Height {
			break
		}
		if loff >= bot {
			scrolled += h
			if loff == bot && before > bot {
				scrolled -= empty
			}
		}
		if boff < n-1 {
			bbefore := boff
			boff++
			bh := v.plines(boff, true)
			used += bh
			if used > v.Height {
				break
			}
			if extra < so || scrolled < minScroll {
				extra += bh
				if boff >= bot {
					scrolled += bh
					if bbefore < bot {
						scrolled -= empty
					}
				}
			}
		}
	}
	lineCount := 0
	switch {
	case scrolled <= 0:
	case used > v.Height:
		lineCount = used
	default:
		i := 0
		for b := v.Top - 1; i < scrolled && b < bot; {
			b++
			i += v.plines(b, true)
			lineCount++
		}
		if i < scrolled {
			lineCount = 9999
		}
	}
	switch {
	case lineCount >= v.Height && lineCount > minScroll:
		v.CursorHalfway(cursor, false, true)
	case lineCount > 0:
		v.setTop(min(v.Top+lineCount, n-1))
	}
}

// CursorHalfway は、カーソルの行 cursor を表示領域の中ほどに置く（Neovim 0.12.5 の scroll_cursor_halfway の 'smoothscroll' がオフのとき）。
// カーソルの行の下と上へ 1 行ずつ交互に足し（preferAbove なら上を先に足す）、表示領域に入る最も上の行を最初の行にする。
// 下に行が無ければ "~" の行を 1 行と数える。atEnd なら、その "~" の行も表示領域の行に数える（zz。バッファの末尾でもカーソルを中ほどに置く）。
func (v *View) CursorHalfway(cursor int, atEnd, preferAbove bool) {
	n := v.count()
	used := v.plines(cursor, true)
	loff, boff := cursor, cursor
	top := cursor
	for top > 0 {
		done := false
		above, below := 0, 0
		for round := 1; round <= 2; round++ {
			var addBelow, addAbove bool
			if preferAbove {
				addBelow = round == 2 && below < above
			} else {
				addBelow = round == 1 && below <= above
			}
			if addBelow {
				if boff < n-1 {
					boff++
					h := v.plines(boff, true)
					used += h
					if used > v.Height {
						done = true
						break
					}
					below += h
				} else {
					below++
					if atEnd {
						used++
					}
				}
			}
			if preferAbove {
				addAbove = round == 1 && below >= above
			} else {
				addAbove = round == 1 && below > above
			}
			if addAbove {
				loff--
				h := v.plines(loff, true)
				if h == tall {
					used = tall
				} else {
					used += h
				}
				if used > v.Height {
					done = true
					break
				}
				above += h
				top = loff
			}
		}
		if done {
			break
		}
	}
	v.setTop(top)
}

// CursorCorrect は、カーソルの行 cursor を、表示領域の上と下から余白の行数だけ離れた行へ動かした行を返す。最初の行は変えない。
// 離れられなければ、中ほど（CursorHalfway と同じ位置）にする。最初の行が 0 なら上の余白を、最後の行まで見えていれば下の余白を求めない。
// Neovim の cursor_correct を写した。
func (v *View) CursorCorrect(cursor int) int {
	n := v.count()
	aboveWanted, belowWanted := v.so(), v.so()
	if v.Top == 0 {
		aboveWanted = 0
		belowWanted = min(belowWanted, v.Height/2)
	}
	bot, _ := v.Botline()
	if bot == n {
		belowWanted = 0
		aboveWanted = min(aboveWanted, (v.Height-1)/2)
	}
	if cursor >= v.Top+aboveWanted && cursor < bot-belowWanted {
		return cursor
	}
	topline, botline := v.Top, bot-1
	above, below := 0, 0
	for (above < aboveWanted || below < belowWanted) && topline < botline {
		if below < belowWanted && (below <= above || above >= aboveWanted) {
			below += v.plines(botline, true)
			botline--
		}
		if above < aboveWanted && (above < below || below >= belowWanted) {
			above += v.plines(topline, true)
			topline++
		}
	}
	switch {
	case topline == botline || botline == -1:
		return topline
	case topline > botline:
		return botline
	}
	if cursor < topline && v.Top > 0 {
		cursor = topline
	}
	if cursor > botline && bot < n {
		cursor = botline
	}
	return cursor
}

// scrollUp は、表示領域を count 行だけ下の行へ送る（<C-e>。Neovim の scrollup）。smooth なら見た目の行で数え（'smoothscroll'）、
// そうでなければバッファの行で数える。最初の行は最後の行より下へは送らない。カーソルの行が最初の行より上になったら、最初の行にした行を返す。
func (v *View) scrollUp(count int, smooth bool, cursor int) int {
	n := v.count()
	if smooth {
		size := v.plines(v.Top, false)
		for todo := count; todo > 0; todo-- {
			l := v.Top
			v.Skip++
			if v.Skip >= size {
				if l == n-1 {
					v.Skip--
					break
				}
				l++
			}
			if l > v.Top {
				v.Top, v.Skip = l, 0
				size = v.plines(v.Top, false)
			}
		}
	} else {
		v.setTop(min(v.Top+count, n-1))
	}
	v.Top = min(v.Top, n-1)
	if cursor < v.Top {
		cursor = v.Top
	}
	return cursor
}

// scrollDown は、表示領域を count 行だけ上の行へ戻す（<C-y>。Neovim の scrolldown）。smooth は scrollUp と同じである。
// カーソルの行の最後の見た目の行が表示領域の下へ出たら、入るまでカーソルを上の行へ動かした行を返す。
func (v *View) scrollDown(count int, smooth bool, cursor int) int {
	for todo := count; todo > 0; todo-- {
		if v.Top == 0 && (!smooth || v.Skip < 1) {
			break
		}
		if smooth && v.Skip >= 1 {
			v.Skip--
			continue
		}
		v.Top--
		v.Skip = 0
		if smooth {
			v.Skip = v.plines(v.Top, false) - 1
		}
	}
	// カーソルの行の最後の見た目の行の、表示領域の最初の行からの行数。
	wrow := -v.Skip
	for l := v.Top; l < cursor; l++ {
		wrow += v.plines(l, false)
	}
	wrow += v.plines(cursor, true) - 1
	for wrow >= v.Height && cursor > 0 {
		wrow -= v.plines(cursor, true)
		cursor--
	}
	return max(cursor, v.Top)
}

// ScrollRedraw は、表示領域を count 行だけ送り（up なら下の行へ。<C-e>）、カーソルの行を表示領域に入れて返す（Neovim の scroll_redraw）。
// smooth は scrollUp と同じである。余白があれば、カーソルを余白の外へ動かし（CursorCorrect）、表示領域が動かなかったときは、
// カーソルを送る向きへ 1 行動かす（バッファの端で止まらないため）。カーソルの列は呼び出し側が決める。
func (v *View) ScrollRedraw(up bool, count, cursor int, smooth bool) int {
	n := v.count()
	prevTop, prevSkip, prevCursor := v.Top, v.Skip, cursor
	if up {
		cursor = v.scrollUp(count, smooth, cursor)
	} else {
		cursor = v.scrollDown(count, smooth, cursor)
	}
	if v.so() > 0 {
		cursor = v.CursorCorrect(cursor)
		for v.Top == prevTop && v.Skip == prevSkip {
			if up {
				if cursor > prevCursor || cursor >= n-1 {
					break
				}
				cursor++
			} else {
				if cursor < prevCursor || prevTop == 0 || cursor == 0 {
					break
				}
				cursor--
			}
		}
	}
	if smooth && v.SmoothCursor != nil {
		cursor = v.SmoothCursor(v, cursor)
	}
	return cursor
}

// ScrollSmooth は、表示領域を見た目の行で count 行だけ送り（forward なら下の行へ）、最初の行が途中から見える位置で止まったら、
// 行の切れ目まで送り足すか戻す（Neovim の scroll_with_sms の 'smoothscroll' がオフのとき）。
// 表示の位置が変わったかと、送った見た目の行の数の補正（送り足した数を足し、戻した数を引く）と、カーソルの行を返す。
// 送り足すか戻すかと数は、Neovim と同じく w_skipcol を表示の列（Skip × Width）にした式で決める。
func (v *View) ScrollSmooth(forward bool, count, cursor int) (moved bool, delta, cur int) {
	prevTop, prevSkip := v.Top, v.Skip
	cursor = v.ScrollRedraw(forward, count, cursor, true)
	if v.Skip > 0 {
		fix := forward
		d := v.Top - prevTop
		if d < 0 {
			d = -d
		}
		back := 0
		if !forward {
			back = 1
		}
		if d > back {
			fix = !forward
		}
		w := max(v.Width, 1)
		skipcol := v.Skip * w
		c := 1 + (skipcol-w-1)/w
		if fix {
			size := v.plines(v.Top, false) * w
			c = 1 + (size-skipcol-w+w-1)/w
		}
		cursor = v.ScrollRedraw(fix, c, cursor, true)
		if fix == forward {
			delta = c
		} else {
			delta = -c
		}
	}
	return v.Top != prevTop || v.Skip != prevSkip, delta, cursor
}

// Overlap は、ページ単位のスクロール（<C-f>、<C-b>）で 1 ページに送る見た目の行の数を返す（Neovim の get_scroll_overlap）。
// 表示領域の行数から、前の画面と重ねて残す行（0 から 2 行。重ねる行の高さの和が表示領域の行数から 2 を引いた数を超えない）を引いた数である。
// 前へ送るときに最後の行まで見えているか、後ろへ戻すときに最初の行が 0 なら、重ねずに表示領域の行数を返す。
func (v *View) Overlap(forward bool) int {
	minH := v.Height - 2
	bot, _ := v.Botline()
	if (!forward && v.Top == 0) || (forward && bot >= v.count()) {
		return minH + 2
	}
	l := v.Top - 1
	step := 1
	if forward {
		l, step = bot, -1
	}
	h1 := v.plines(l, true)
	if h1 > minH {
		return minH + 2
	}
	l += step
	h2 := v.plines(l, true)
	if h2 == tall || h2+h1 > minH {
		return minH + 2
	}
	l += step
	h3 := v.plines(l, true)
	if h3 == tall || h3+h2 > minH {
		return minH + 2
	}
	l += step
	h4 := v.plines(l, true)
	if h4 == tall || h4+h3+h2 > minH || h3+h2+h1 > minH {
		return minH + 1
	}
	return minH
}

// TopRows は、最初の行のうち表示領域に見える見た目の行の数（隠した行を引いた数。表示領域の行数で頭打ちにしない）を返す
// （Neovim の plines_correct_topline の limit_winheight が false のとき）。
func (v *View) TopRows() int {
	return max(v.plines(v.Top, false)-v.Skip, 0)
}

// RowsBetween は、行 first から last までの見た目の行の数の和を、limit で頭打ちにして返す（Neovim の plines_m_win）。
func (v *View) RowsBetween(first, last, limit int) int {
	c := 0
	for l := first; l <= last && c < limit; l++ {
		c += v.plines(l, false)
	}
	return min(c, limit)
}
