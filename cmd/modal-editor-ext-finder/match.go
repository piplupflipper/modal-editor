package main

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
)

// 絞り込みの計算（基本設計 6.9。FR-106）。
//
// 入力は空白で区切った語の列で、候補はすべての語に一致したものだけを残す（fzf の拡張検索の AND に当たる）。
// 語は、候補の文字列の中に、語の文字が順に（間に他の文字を挟んでよい）現れれば一致する（fzf の既定のあいまい一致）。
// 語に大文字が無ければ大文字と小文字を区別せず、あれば区別する（fzf と rg の smart case）。
//
// 一致の点数は fzf v1 の考え方を簡単にしたもので、次の和である（どれも仮）。
//
//   - 一致した文字ごとに scoreMatch
//   - 一致した文字が語の切れ目（先頭、/ の後、- _ . 空白 : の後、小文字の後の大文字）なら、その種類の加点
//   - 前の文字に続けて一致したら scoreConsecutive
//   - 一致の間に挟んだ文字の列ごとに scoreGapStart、挟んだ文字ごとに scoreGapExtend を引く
//
// 1 つの語の一致は、前から最初に一致する位置で文字を拾ったあと、最後の文字から後ろへ拾い直して一致の範囲を縮める（fzf v1 と同じ）。
// 並びは点数の高い順、同じなら候補の文字列の短い順、同じなら元の順である。

const (
	scoreMatch       = 16
	scoreConsecutive = 8
	scoreGapStart    = 3
	scoreGapExtend   = 1
	bonusStart       = 10
	bonusSlash       = 9
	bonusDelimiter   = 8
	bonusCamel       = 7
)

// term は、入力の 1 つの語である。
type term struct {
	runes []rune
	// exact は、大文字と小文字を区別することを表す（語に大文字がある）。
	exact bool
}

// parseQuery は、入力 q を語の列にする。空白だけなら空である。
func parseQuery(q string) []term {
	var out []term
	for _, f := range strings.Fields(q) {
		t := term{runes: []rune(f)}
		for _, r := range t.runes {
			if unicode.IsUpper(r) {
				t.exact = true
				break
			}
		}
		if !t.exact {
			for i, r := range t.runes {
				t.runes[i] = unicode.ToLower(r)
			}
		}
		out = append(out, t)
	}
	return out
}

// result は、一致した候補 1 つである。
type result struct {
	// index は、候補の列の中の添字である。
	index int
	score int
	// spans は、一致した文字のバイトの範囲を、続いたものをまとめて前から並べたものである。
	spans []ext.ViewSpan
}

// matchOne は、候補 s が語の列 terms のすべてに一致するかを調べ、一致したら点数と一致した文字の範囲を返す。
// terms が空なら、点数 0 で一致したものとする。
func matchOne(s string, terms []term) (int, []ext.ViewSpan, bool) {
	if len(terms) == 0 {
		return 0, nil, true
	}
	rs := []rune(s)
	offs := make([]int, len(rs)+1) // rune の添字からバイトの位置
	off := 0
	for i, r := range rs {
		offs[i] = off
		off += utf8.RuneLen(r)
	}
	offs[len(rs)] = off
	var lower []rune
	total := 0
	var pos []int
	for _, t := range terms {
		hay := rs
		if !t.exact {
			if lower == nil {
				lower = make([]rune, len(rs))
				for i, r := range rs {
					lower[i] = unicode.ToLower(r)
				}
			}
			hay = lower
		}
		p, ok := matchTerm(hay, t.runes)
		if !ok {
			return 0, nil, false
		}
		total += scorePositions(rs, p)
		pos = append(pos, p...)
	}
	slices.Sort(pos)
	pos = slices.Compact(pos)
	var spans []ext.ViewSpan
	for _, i := range pos {
		start, end := offs[i], offs[i+1]
		if n := len(spans); n > 0 && spans[n-1].End == start {
			spans[n-1].End = end
			continue
		}
		spans = append(spans, ext.ViewSpan{Start: start, End: end, Style: matchStyle})
	}
	return total, spans, true
}

// matchStyle は、一致した文字に付ける色の名前である（本体の ui.StyleFinderMatch）。
const matchStyle = "finder.match"

// matchTerm は、hay の中に needle の文字が順に現れる位置（rune の添字）を返す。
// 前から最初に一致する位置まで拾った後、最後の文字から後ろへ拾い直して範囲を縮める。
func matchTerm(hay, needle []rune) ([]int, bool) {
	if len(needle) == 0 {
		return nil, true
	}
	j := 0
	end := -1
	for i, r := range hay {
		if r == needle[j] {
			j++
			if j == len(needle) {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return nil, false
	}
	pos := make([]int, len(needle))
	j = len(needle) - 1
	for i := end; i >= 0 && j >= 0; i-- {
		if hay[i] == needle[j] {
			pos[j] = i
			j--
		}
	}
	return pos, true
}

// scorePositions は、元の文字列 rs の位置 pos（昇順）に一致した 1 つの語の点数を返す。
func scorePositions(rs []rune, pos []int) int {
	score := 0
	for k, i := range pos {
		score += scoreMatch + boundaryBonus(rs, i)
		if k > 0 {
			if gap := i - pos[k-1] - 1; gap == 0 {
				score += scoreConsecutive
			} else {
				score -= scoreGapStart + scoreGapExtend*(gap-1)
			}
		}
	}
	return score
}

// boundaryBonus は、rs の i 番目の文字が語の切れ目にあるときの加点を返す。
func boundaryBonus(rs []rune, i int) int {
	if i == 0 {
		return bonusStart
	}
	prev, cur := rs[i-1], rs[i]
	switch {
	case prev == '/':
		return bonusSlash
	case prev == '-' || prev == '_' || prev == '.' || prev == ' ' || prev == ':' || prev == '\t':
		return bonusDelimiter
	case unicode.IsLower(prev) && unicode.IsUpper(cur):
		return bonusCamel
	}
	return 0
}

// filter は、候補 labels を入力 q で絞り、並べた結果を返す。limit が正なら、上位 limit 件だけを返す。
// 2 つ目の戻り値は、一致した候補の数（limit で切る前）である。
// q が空白だけなら、すべての候補を元の順に返す。
func filter(labels []string, q string, limit int) ([]result, int) {
	terms := parseQuery(q)
	var out []result
	for i, s := range labels {
		score, spans, ok := matchOne(s, terms)
		if ok {
			out = append(out, result{index: i, score: score, spans: spans})
		}
	}
	n := len(out)
	if len(terms) > 0 {
		slices.SortStableFunc(out, func(a, b result) int {
			if c := cmp.Compare(b.score, a.score); c != 0 {
				return c
			}
			if c := cmp.Compare(len(labels[a.index]), len(labels[b.index])); c != 0 {
				return c
			}
			return cmp.Compare(a.index, b.index)
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, n
}
