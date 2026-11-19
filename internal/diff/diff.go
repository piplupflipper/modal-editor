// Package diff は、2 つのテキストの行の差分と、両側の行の対応の表を計算する（基本設計 6.8）。
//
// 基本設計 2 章の機能の層にあり、土台の internal/buffer だけを import する。
// 差分の計算には github.com/aymanbagabas/go-udiff（Myers のアルゴリズム）を使う。
// 表示（差分の色、詰め物の行の描画）は持たない。
//
// 計算はテキストの行数に比例しうるので、呼び出し側はイベントループの外の goroutine で Compute を呼べる（基本設計 2.1）。
// そのために、Compute はバッファではなく、イベントループで写した Text を受け取る。
// Text と Result は作った後に変えないので、goroutine の間で共有してよい。
//
// # 行の数え方
//
// 行は Vim と同じく数える。
// "a\nb\n" と "a\nb" はどちらも "a" と "b" の 2 行で、空のファイル "" は 0 行、"\n" は空の行 1 つである。
//
// 末尾の改行の有無だけの違いは、違いとして扱わない（仮。#185 のオーケストレータの決定）。
// Vim 9.1 の diff は 'fixendofline' の設定によらず、末尾の改行の有無だけが違う 2 つのファイルに違いを出さない（2026-11-18 に手元の Vim 9.1 で確かめた）。
// FR-101 の操作を Vim に合わせるので、違いの出し方も Vim に合わせる。
// diff コマンド（"\ No newline at end of file" を付けて最後の行を違う行にする）とは違う。
// このため Text は末尾の改行の有無を持たず、Parse と FromBuffer のどちらで作っても、行が同じなら違いは出ない。
//
// # 行の番号
//
// 行の番号は 0 から数え、範囲（Span）は Start を含み End を含まない。
// 差分の塊（Hunk）は、片側の範囲が空でもよい（もう片側だけにある行）。
// 空の範囲の Start は、もう片側にだけある行が、この側のどの行の前に入るかを表す。
package diff

import (
	"sort"
	"strings"

	"github.com/aymanbagabas/go-udiff/lcs"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// Side は、見比べる 2 つのテキストのどちらの側かを表す。
type Side int

const (
	// A は、Compute の 1 つ目の引数の側である。
	A Side = iota
	// B は、Compute の 2 つ目の引数の側である。
	B
)

// Other は、もう片側を返す。
func (s Side) Other() Side {
	if s == A {
		return B
	}
	return A
}

// Text は、見比べるテキストを行の列で表す。
// Lines の各要素は改行を含まない。
// 末尾の改行の有無は持たない（パッケージの doc の「行の数え方」）。
type Text struct {
	Lines []string
}

// Parse は、ファイルの内容 content を、パッケージの doc の「行の数え方」に従って Text にする。
// 改行は LF を前提にする。
//
// バッファになっていないファイルの版（FR-104 の git の HEAD の版など）に使う。
// 開いているバッファは FromBuffer で作る（行の無いバッファを 0 行として扱うため）。
// 末尾の改行の有無は比べないので、片方を Parse、もう片方を FromBuffer で作っても、末尾の改行の扱いの違いで違いは出ない。
func Parse(content string) Text {
	if content == "" {
		return Text{}
	}
	return Text{Lines: strings.Split(strings.TrimSuffix(content, "\n"), "\n")}
}

// FromBuffer は、バッファ b の行の Text を作る。
// 行の無いバッファ（NoLines）は 0 行、それ以外は b の行である。
// 行の文字列は b と共有するが、Go の文字列は変わらないので、返した Text を別の goroutine へ渡してよい。
// b を読むので、イベントループの goroutine で呼ぶ。
func FromBuffer(b *buffer.Buffer) Text {
	if b.NoLines() {
		return Text{}
	}
	lines := make([]string, b.LineCount())
	for i := range lines {
		lines[i] = b.Line(i)
	}
	return Text{Lines: lines}
}

// Span は、Start から End の直前までの範囲を表す。
// 行の範囲にも、行の中のバイトの範囲にも使う。
type Span struct {
	Start int
	End   int
}

// Len は、範囲の長さを返す。
func (s Span) Len() int {
	return s.End - s.Start
}

// Kind は、差分の塊の種類を表す。
// 差分の色（基本設計 6.8 の diff.add、diff.delete、diff.change）を選ぶのに使う。
type Kind int

const (
	// Change は、両側に行があり、内容が違う塊である。
	Change Kind = iota
	// Add は、A の側に行が無く、B の側にだけ行がある塊である（A に B の行を足すと B になる）。
	Add
	// Delete は、B の側に行が無く、A の側にだけ行がある塊である（A から行を消すと B になる）。
	Delete
)

// Hunk は、差分の塊である。
// A の側の行の範囲 A を、B の側の行の範囲 B で置き換えると、A の側のテキストが B の側に近づく。
// 2 つの範囲が両方とも空になることは無い。
//
// 詰め物の行（Vim の diff の filler）は、範囲の短い側に、長さの差だけ描く。
// ただし、0 行のテキスト（空のファイル）の側は、画面に空の行 1 つが出るので、Vim 9.1 はその行を変えた行として色を付け、
// 詰め物をもう片側の範囲の長さより 1 行少なくする（a = ""、b = "d\nb\n" で、A の側の空の行は変えた行、詰め物は 1 行。#185 のレビュー）。
// 詰め物を描く側（server）はこの規則を写す。
//
// 塊の位置は、同じ長さの共通部分列の選び方によって、Vim（xdiff）と違いうる。
// 違いの少ないテキストでも起こる（#185 のレビューで、0〜5 行の乱数の組の多くで位置か詰め物の数が違った）。
// その場合も、塊の通りに置き換えれば A が B になることは変わらないが、do で取り込む行は Vim と変わりうる。
type Hunk struct {
	A Span
	B Span
}

// Kind は、塊の種類を返す。
func (h Hunk) Kind() Kind {
	switch {
	case h.A.Len() == 0:
		return Add
	case h.B.Len() == 0:
		return Delete
	default:
		return Change
	}
}

// Span は、side の側の行の範囲を返す。
func (h Hunk) Span(side Side) Span {
	if side == A {
		return h.A
	}
	return h.B
}

// Result は、2 つのテキストの差分の計算の結果である。
// Hunks は A の側（したがって B の側も）の行の順に並び、隣り合う塊の間には、両側で同じ行が少なくとも 1 行ある。
// Result は計算に使った Text を持ち、Obtain と Put の編集はその Text に対する位置で作る。
type Result struct {
	Hunks []Hunk
	a, b  Text
}

// Compute は、a と b の行の差分を計算する。
//
// 計算は go-udiff の lcs パッケージ（golang.org/x/tools/internal/diff/lcs を写したもの）による。
// 違う行が多いと、lcs は探索の深さの上限（違いがおよそ 50 箇所）で打ち切り、最小とは限らない差分を返す。
// その場合は、近くの違いが 1 つの塊にまとまり、同じ行も塊に含まれうる（10,000 行に 200 箇所の違いで、塊は 75 個になった）。
// その場合も、塊の通りに置き換えれば a が b になることは保たれる。
func Compute(a, b Text) *Result {
	ds := lcs.DiffLines(a.Lines, b.Lines)
	if len(ds) == 0 {
		return &Result{a: a, b: b}
	}
	hunks := make([]Hunk, len(ds))
	for i, d := range ds {
		hunks[i] = Hunk{A: Span{d.Start, d.End}, B: Span{d.ReplStart, d.ReplEnd}}
	}
	return &Result{Hunks: hunks, a: a, b: b}
}

// Text は、side の側の、計算に使ったテキストを返す。
func (r *Result) Text(side Side) Text {
	if side == A {
		return r.a
	}
	return r.b
}

// lineCount は、side の側の行の数を返す。
func (r *Result) lineCount(side Side) int {
	return len(r.Text(side).Lines)
}

// anchor は、i 番目の塊の、side の側で塊を代表する行（カーソルを置く行）を返す。
// 範囲が空でなければその最初の行、空なら、もう片側にだけある行が入る位置の次の行（Vim が詰め物の行の下に置く行）である。
// 空の範囲がテキストの末尾にあれば、最後の行にする。0 行のテキストでは 0 を返す。
func (r *Result) anchor(i int, side Side) int {
	s := r.Hunks[i].Span(side)
	if n := r.lineCount(side); s.Start >= n {
		return max(n-1, 0)
	}
	return s.Start
}

// Start は、i 番目の塊の、side の側で塊が始まる行（]c と [c でカーソルを置く行）を返す。
// 範囲が空でなければその最初の行、空なら、もう片側にだけある行の位置の次の行（末尾なら最後の行）である。
// i は 0 以上 len(r.Hunks) 未満でなければ panic する。
func (r *Result) Start(i int, side Side) int {
	return r.anchor(i, side)
}

// Next は、side の側の行 line より後ろで始まる、最初の塊の番号を返す（Vim の ]c）。
// 塊が始まる行は、範囲が空でなければその最初の行、空ならもう片側にだけある行の位置の次の行である。
// 無ければ false を返す。
func (r *Result) Next(side Side, line int) (int, bool) {
	for i := range r.Hunks {
		if r.anchor(i, side) > line {
			return i, true
		}
	}
	return 0, false
}

// Prev は、side の側の行 line より前で始まる、最後の塊の番号を返す（Vim の [c）。
// 無ければ false を返す。
func (r *Result) Prev(side Side, line int) (int, bool) {
	for i := len(r.Hunks) - 1; i >= 0; i-- {
		if r.anchor(i, side) < line {
			return i, true
		}
	}
	return 0, false
}

// Op は、塊を取り込む操作の種類である（HunkAt）。
type Op int

const (
	// OpGet は、Vim の do（:diffget）である。Obtain で編集を作る。
	OpGet Op = iota
	// OpPut は、Vim の dp（:diffput）である。Put で編集を作る。
	OpPut
)

// HunkAt は、side の側でカーソルが行 line にあるときに、op（do か dp。範囲を付けない :diffget か :diffput）が対象にする塊の番号を返す。
//
// Vim 9.1 の ex_diffgetput（src/diff.c）の範囲の決め方を写す。
// 範囲を付けないと、Vim はカーソルの行と 1 つ上の行（1 始まりで line1 = カーソル - 1、line2 = カーソル）を範囲にし、
// 範囲に掛かる塊（この側の始まりが line2 以下で、終わりが line1 より後ろ）を対象にする。
// 0 始まりの行で言い直すと、範囲が空でない塊は、カーソルの行が範囲の中か、範囲の直後の行なら対象になる。
// 範囲が空の塊（この側に無い行の詰め物）は、カーソルの行が詰め物の直下の行なら対象になる。
//
// op が OpGet のときだけ、カーソルが最後の行にあり、その行と 1 つ上の行の diff_check が 0（塊の中でも、詰め物の直下でもない）なら、
// 範囲をカーソルの行と 1 つ下の行にする。
// このとき対象になるのは、末尾の空の範囲の塊（最後の行の下の詰め物）だけである。
// OpPut にこの規則は無い（Vim のソースで :diffget に限っている）。
// 2026-11-18 に手元の Vim 9.1（patches 1-16）で、最初の行、最後の行、詰め物の上下を組み合わせた組（TestHunkAtVim）の結果を確かめた。
//
// 塊の間には同じ行が少なくとも 1 行あるので、対象の塊は高々 1 つである。無ければ false を返す。
func (r *Result) HunkAt(side Side, line int, op Op) (int, bool) {
	n := r.lineCount(side)
	inHunk := func(l int) bool {
		for _, h := range r.Hunks {
			if s := h.Span(side); s.Start <= l && l < s.End {
				return true
			}
		}
		return false
	}
	fillerAbove := func(l int) bool {
		for _, h := range r.Hunks {
			if s := h.Span(side); s.Len() == 0 && s.Start == l {
				return true
			}
		}
		return false
	}
	// clean は、Vim の diff_check が 0 を返す行（塊の中でも、詰め物の直下でもない行）かどうかである。
	clean := func(l int) bool { return !inHunk(l) && !fillerAbove(l) }
	if op == OpGet && line == max(n-1, 0) && clean(line) && (line == 0 || clean(line-1)) {
		for i, h := range r.Hunks {
			if s := h.Span(side); s.Len() == 0 && s.Start >= n {
				return i, true
			}
		}
		return 0, false
	}
	for i, h := range r.Hunks {
		s := h.Span(side)
		if s.Len() == 0 {
			if s.Start == line {
				return i, true
			}
		} else if s.Start <= line && line <= s.End {
			return i, true
		}
	}
	return 0, false
}

// MapLine は、from の側の行 line に対応する、もう片側の行を返す（FR-100 のスクロールのそろえに使う）。
//
// 両側で同じ行は、同じ内容の行へ対応させる。
// 塊の中の行は、もう片側の範囲の先頭から同じだけ進んだ行へ対応させ、範囲を超えれば範囲の最後の行にする。
// もう片側の範囲が空なら、その範囲の Start（詰め物の行の下の行）にする。
// 結果は、もう片側の行の範囲（0 以上、行の数が 1 以上なら行の数未満）に収める。
// 塊を二分探索するので、計算量は塊の数の対数である。
func (r *Result) MapLine(from Side, line int) int {
	to := from.Other()
	// line より前で始まる塊の数。
	i := sort.Search(len(r.Hunks), func(i int) bool { return r.Hunks[i].Span(from).Start > line })
	var got int
	if i == 0 {
		got = line
	} else {
		h := r.Hunks[i-1]
		fs, ts := h.Span(from), h.Span(to)
		switch {
		case line >= fs.End:
			// 塊の後ろの、両側で同じ行。
			got = ts.End + (line - fs.End)
		case ts.Len() == 0:
			got = ts.Start
		default:
			got = ts.Start + min(line-fs.Start, ts.Len()-1)
		}
	}
	n := r.lineCount(to)
	return min(max(got, 0), max(n-1, 0))
}
