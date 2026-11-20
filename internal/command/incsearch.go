package command

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
)

// 打つたびの検索（FR-153。基本設計 6.7 の「打つたびの検索」）の、State の側の部品。
//
// State は、/ か ? でパターンを打っている間の、何を検索するか（IncSearch）と、受け取った結果（ShowIncSearch）だけを持つ。
// 検索そのものと時間の上限は、呼び出し側（internal/editor）が、イベントループの外の goroutine で target.FindMatch を呼んで行う。
// 打つたびの検索は、カーソル、ジャンプリスト、直前の検索、消した一致の色のどれも変えない。それらは <CR> で決めたときに
// finishSearch が実行する値が変える。<Esc> と <C-c> で取り消したら（cancelSearch）、受け取った結果は入力の値ごと捨てる。

// IncSearch は、打つたびの検索の問いである。値が同じ問いは、同じテキストに対して同じ一致になる（== で比べられる）。
type IncSearch struct {
	// Gen は、/ か ? の入力を始めるたびに進む番号である。別の入力の問いと見分ける。
	Gen uint64
	// Search は、打っている途中のパターン（オフセットを除いたもの。target.SplitSearch）と向き（? なら後ろ向き）である。
	Search target.Search
	// From は、検索を始める位置（入力を始めたときのカーソル）である。
	From buffer.Position
	// Count は、/ の前の回数である。省いたときは 1 である。
	Count int
	// Steps は、<C-g> を打った数から <C-t> を打った数を引いたものである（target.FindMatch の steps）。
	Steps int
}

// incShown は、打つたびの検索で受け取った結果である。
type incShown struct {
	// q は、結果を出した問いである。
	q IncSearch
	// match は、見つけた一致である。found が false なら使わない。
	match target.Match
	// found は、一致を見つけたかである。見つからない、パターンが正しくない、時間の上限を超えたときは false である。
	found bool
}

// IncSearch は、/ か ? でパターンを打っている間の、打つたびの検索の問いを返す。
// 打っていない、打ったパターンが空、マクロの再生かドットリピートの中（Replaying）なら、ok が false である。
// 再生の中で打つたびの検索をしないのは、再生のキーは画面に出す前にすべて処理し、<CR> を押したときと同じ結果になればよいためである（#296 の本文）。
// 打っている途中のパターンが正規表現として正しくないかは問わない。正しくなければ、検索が誤りを返すので一致を出さない。
//
// req: FR-153
func (s *State) IncSearch() (IncSearch, bool) {
	ps := s.search
	if ps == nil || s.Replaying() {
		return IncSearch{}, false
	}
	pat, _, _ := target.SplitSearch(string(s.cmdline), ps.delim)
	if pat == "" {
		return IncSearch{}, false
	}
	return IncSearch{
		Gen:    ps.gen,
		Search: target.Search{Pattern: pat, Backward: ps.delim == '?'},
		From:   s.cursor,
		Count:  max(ps.cmd.Count, 1),
		Steps:  ps.steps,
	}, true
}

// ShowIncSearch は、打つたびの検索の問い q の結果を受け取る。found が false なら一致が無い（見つからない、パターンが正しくない、
// 時間の上限を超えた）ことを表し、m は使わない。
// q が今の問い（IncSearch）でなければ、前の入力の結果なので捨て、false を返す。
// 受け取った結果は、次の結果を受け取るまで、パターンを打ち足しても出し続ける（IncSearchMatch）。
//
// req: FR-153
func (s *State) ShowIncSearch(q IncSearch, m target.Match, found bool) bool {
	cur, ok := s.IncSearch()
	if !ok || cur != q {
		return false
	}
	s.search.shown = &incShown{q: q, match: m, found: found}
	return true
}

// IncSearchMatch は、打つたびの検索で出す一致と、その一致を見つけたパターンを返す。
// / か ? でパターンを打っていない、打ったパターンが空、まだ結果を受け取っていない、受け取った結果に一致が無いときは、ok が false である。
// 結果は、パターンを打ち足して次の結果を受け取るまでの間は、前の問いのものを返す（受け取るまで画面を空にしないため）。
//
// req: FR-153
func (s *State) IncSearchMatch() (pattern string, m target.Match, ok bool) {
	if _, cur := s.IncSearch(); !cur {
		return "", target.Match{}, false
	}
	sh := s.search.shown
	if sh == nil || !sh.found {
		return "", target.Match{}, false
	}
	return sh.q.Search.Pattern, sh.match, true
}

// stepSearch は、打っている間の <C-g>（next が true）と <C-t> である。打つたびの検索で出す一致を、回数の数だけ次（前）の一致へ動かす。
// 動かした数は、<CR> で決めたときの移り先にも使う（finishSearch）。ジャンプリストは変えない。
// nvim 0.12.5 と同じく、/ と ? のどちらでも <C-g> がバッファの末尾の方へ動かす（target.FindMatch）。
// パターンを打ち足しても、動かした数は残る（打ち始めた位置から数え直す）。
// 打ったパターンが空のときは数えない（nvim 0.12.5 の /<C-g>foo<CR> は最初の一致へ移る。PR #306 のレビューの推奨 3）。
// 一致が無いときと、パターンが正しくないときは数える。一致があるかは後から届く検索の結果で決まり、再生の中では検索しないので、
// 数えるかをそれで分けると、打ったときと再生したときで移り先が変わるためである。
//
// req: FR-153
func (s *State) stepSearch(next bool) {
	if pat, _, _ := target.SplitSearch(string(s.cmdline), s.search.delim); pat == "" {
		return
	}
	if next {
		s.search.steps++
	} else {
		s.search.steps--
	}
}

// ShownSelection は、描画に出す選択を返す。ふだんは Selection と同じである。
// ビジュアルモードから / か ? でパターンを打っている間は、モードはコマンドラインモードだが、選択を出す。
// 選択の動く側の端は、打つたびの検索で出している一致の先頭（無ければ打ち始めたときの端）にする。
// nvim 0.12.5 も、打っている間は選択を一致まで広げて出す（2026-11-20 に nvim --clean の v/foo で確かめた）。
// <Esc> で取り消すと、選択は打ち始める前のものに戻る（選択の状態は打っている間に変えない）。
//
// req: FR-153
func (s *State) ShownSelection() (start, end buffer.Position, linewise, ok bool) {
	ps := s.search
	if ps == nil || !ps.back.visual() {
		return s.Selection()
	}
	head := s.visual.head
	if _, m, found := s.IncSearchMatch(); found {
		head = m.Range.Start
	}
	start, end = s.clampPos(s.visual.anchor), s.clampPos(head)
	if end.Less(start) {
		start, end = end, start
	}
	return start, end, ps.back == ModeVisualLine, true
}
