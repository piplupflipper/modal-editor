package diff

import (
	"unicode/utf8"

	"github.com/aymanbagabas/go-udiff/lcs"
)

// invalidByteBase は、不正な UTF-8 のバイトを比べるときの値の始まりである。
// Unicode の符号位置の最大（U+10FFFF）より大きいので、正しい文字と重ならない。
const invalidByteBase = utf8.MaxRune + 1

// Inline は、変えた行どうし a と b の中で、違う部分のバイトの範囲を返す（行の中の差分の色付けに使う）。
// 1 つ目の戻り値は a の中の範囲、2 つ目は b の中の範囲で、どちらも位置の順に並び、同じ番号の範囲どうしが対応する。
// 片側だけにある文字の範囲は、もう片側では長さ 0 の範囲になる。
// a の範囲を、対応する b の範囲の内容で置き換えると b になる。
//
// 文字の単位で比べ、範囲の境界は文字の境界（buffer.Position の doc と同じく、不正な UTF-8 のバイトは 1 バイトを 1 文字とする）にある。
// 不正なバイトは、バイトの値ごとに違う文字として比べる。
// go-udiff の Strings は不正なバイトを U+FFFD（3 バイト）として位置を数え、元の文字列のバイトの位置とずれるので使わず、
// 文字の列とバイトの位置の表を自前で作って lcs で比べる（#185 のレビュー）。
//
// 文字の単位の Myers のアルゴリズムで比べるので、Vim の 'diffopt' の inline:char に近い。
// 単語の単位にはそろえない。
// 違う文字が多いと、Compute と同じく探索の上限で打ち切り、最小とは限らない範囲を返す。
func Inline(a, b string) (sa, sb []Span) {
	if a == b {
		return nil, nil
	}
	ra, oa := chars(a)
	rb, ob := chars(b)
	ds := lcs.DiffRunes(ra, rb)
	if len(ds) == 0 {
		return nil, nil
	}
	sa = make([]Span, len(ds))
	sb = make([]Span, len(ds))
	for i, d := range ds {
		sa[i] = Span{oa[d.Start], oa[d.End]}
		sb[i] = Span{ob[d.ReplStart], ob[d.ReplEnd]}
	}
	return sa, sb
}

// chars は、s を文字の列に分け、各文字の値と、各文字の始まりのバイトの位置（末尾に len(s) を足したもの）を返す。
// 不正な UTF-8 のバイトは 1 バイトを 1 文字とし、invalidByteBase にバイトの値を足した値にする。
func chars(s string) ([]rune, []int) {
	rs := make([]rune, 0, len(s))
	offs := make([]int, 0, len(s)+1)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			r = invalidByteBase + rune(s[i])
		}
		rs = append(rs, r)
		offs = append(offs, i)
		i += size
	}
	return rs, append(offs, len(s))
}
