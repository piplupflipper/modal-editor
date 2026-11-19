package target

import (
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// この file の関数は、表 textObjects に登録するテキストオブジェクトである。
// 各関数は Request が正しいこと（validate を通ったこと）を前提にする。
//
// どれも、ノーマルモードでオペレーターの後に使う場合（Vim の Visual モードでない場合）の動作だけを実装する。
// ビジュアルモードで選択を広げる動作は持たない。

// word は、iw、aw、iW、aW を返す。include は a、big は W である（Vim の current_word()）。
//
// iw は、カーソルの位置の単語か、空白の並びを範囲にする。
// aw は、単語とその後の空白を範囲にする。後に空白が無ければ、前の空白を含める。ただし字下げの空白は含めない。
// カーソルが空白の上にあれば、aw は空白とその後の単語を範囲にする。
// 回数があれば、単語と空白を 1 つずつ数えて広げる（iw）か、単語と空白の組を数えて広げる（aw）。
// 空の行は、iw では空の範囲になる。
// 回数の分だけ広げられずにバッファの末尾に着いたら失敗する。
func word(include, big bool) TextObjectFunc {
	return func(b *buffer.Buffer, req Request) (Span, error) {
		c := cursor{b: b, Position: req.Cursor}
		inclusive := true
		includeWhite := false

		backInLine(&c, big)
		start := c.Position
		if (c.cls(big) == classBlank) == include {
			// 空白の上の aw と、単語の上の iw は、単語の終わりまで。
			if !endWord(&c, 1, big, true, true) {
				return Span{}, ErrNoTarget
			}
		} else {
			// 単語の上の aw と、空白の上の iw は、次の単語の手前まで。
			// 1 文字の単語の後が改行で、次の行の先頭に着いたときは、前の行の終わりへ戻る。
			fwdWord(&c, 1, big, true)
			if c.Byte == 0 {
				c.decl()
			} else {
				c.oneLeft()
			}
			includeWhite = include
		}

		for count := req.count1() - 1; count > 0; count-- {
			inclusive = true
			if c.incl() == stuck {
				return Span{}, ErrNoTarget
			}
			if include != (c.cls(big) == classBlank) {
				if !fwdWord(&c, 1, big, true) && count > 1 {
					return Span{}, ErrNoTarget
				}
				// 次の行の先頭に着いたら、その文字は含めない。
				if !c.oneLeft() {
					inclusive = false
				}
			} else if !endWord(&c, 1, big, true, true) {
				return Span{}, ErrNoTarget
			}
		}

		// aw の後に空白が無ければ、前の空白を含める（文の最後の単語の daw のため）。字下げは含めない。
		if includeWhite && (c.cls(big) != classBlank || c.Byte == 0 && !inclusive) {
			s := cursor{b: b, Position: start}
			if s.oneLeft() {
				backInLine(&s, big)
				if s.cls(big) == classBlank && s.Byte > 0 {
					start = s.Position
				}
			}
		}

		kind := Inclusive
		if !inclusive {
			kind = Exclusive
		}
		return Span{Start: start, End: c.Position, Kind: kind}, nil
	}
}

// backInLine は、同じ行の中で、今の文字と同じ分類の並びの先頭へ戻る（Vim の back_in_line()）。
func backInLine(c *cursor, big bool) {
	sclass := c.cls(big)
	for c.Byte > 0 {
		c.dec()
		if c.cls(big) != sclass {
			c.inc()
			break
		}
	}
}

// quote は、i"、a"、i'、a' を返す。include は a、q は引用符である（Vim の current_quote()）。
//
// 引用符の対は今の行の中だけで探す。バックスラッシュの直後の引用符は数えない（Vim の 'quoteescape' の既定）。
// カーソルが引用符の上にあれば、行の先頭から引用符を対にして、カーソルを含む対を選ぶ。
// そうでなければ、カーソルの前の引用符と、その次の引用符を対にする。前に無ければ、カーソルの後の最初の対を選ぶ。
// このため、2 つの文字列の間にカーソルがあると、文字列の間の部分を範囲にする（Vim と同じ）。
//
// i は引用符の内側、a は引用符を含み、後の空白（無ければ前の空白）も含む。
// 回数が 2 以上なら、i でも引用符を含める（Vim と同じ）。
// 対が見つからなければ失敗する。
func quote(include bool, q byte) TextObjectFunc {
	return func(b *buffer.Buffer, req Request) (Span, error) {
		lnum := req.Cursor.Line
		line := b.Line(lnum)
		colStart, colEnd := req.Cursor.Byte, 0

		if colStart < len(line) && line[colStart] == q {
			first := colStart
			colStart = 0
			for {
				colStart = findNextQuote(line, colStart, q, false)
				if colStart < 0 || colStart > first {
					return Span{}, ErrNoTarget
				}
				colEnd = findNextQuote(line, colStart+1, q, true)
				if colEnd < 0 {
					return Span{}, ErrNoTarget
				}
				if colStart <= first && first <= colEnd {
					break
				}
				colStart = colEnd + 1
			}
		} else {
			colStart = findPrevQuote(line, colStart, q)
			if colStart >= len(line) || line[colStart] != q {
				colStart = findNextQuote(line, colStart, q, false)
				if colStart < 0 {
					return Span{}, ErrNoTarget
				}
			}
			colEnd = findNextQuote(line, colStart+1, q, true)
			if colEnd < 0 {
				return Span{}, ErrNoTarget
			}
		}

		if include {
			if colEnd+1 < len(line) && isBlank(rune(line[colEnd+1])) {
				for colEnd+1 < len(line) && isBlank(rune(line[colEnd+1])) {
					colEnd++
				}
			} else {
				for colStart > 0 && isBlank(rune(line[colStart-1])) {
					colStart--
				}
			}
		}

		withQuotes := include || req.count1() > 1
		end := colEnd // 閉じる引用符の手前まで
		if withQuotes {
			end = colEnd + 1 // 閉じる引用符（a では後の空白）まで。どちらも 1 バイトの文字である
		} else {
			colStart++ // 開く引用符の後から
		}
		return Span{
			Start: buffer.Position{Line: lnum, Byte: colStart},
			End:   buffer.Position{Line: lnum, Byte: end},
			Kind:  Exclusive,
		}, nil
	}
}

// findNextQuote は、col から後ろへ q を探し、その位置を返す。無ければ -1 を返す。
// escape が true なら、バックスラッシュの直後の文字を飛ばす。
func findNextQuote(line string, col int, q byte, escape bool) int {
	for col < len(line) {
		switch c := line[col]; {
		case escape && c == '\\':
			col++
			if col >= len(line) {
				return -1
			}
		case c == q:
			return col
		}
		_, size := utf8.DecodeRuneInString(line[col:])
		col += size
	}
	return -1
}

// findPrevQuote は、col より前で、バックスラッシュが奇数個続いた後でない q を探し、その位置を返す。
// 無ければ 0 を返す（0 の位置が q とは限らない）。
func findPrevQuote(line string, col int, q byte) int {
	for col > 0 {
		_, size := utf8.DecodeLastRuneInString(line[:col])
		col -= size
		n := 0
		for col-n > 0 && line[col-n-1] == '\\' {
			n++
		}
		if n%2 == 1 {
			col -= n
		} else if line[col] == q {
			break
		}
	}
	return col
}

// block は、i(、a( などを返す。include は a、open と closing は括弧の対である（Vim の current_block()）。
//
// カーソルを囲む、入れ子になっていない open と closing の対を探す。行をまたいでよい。
// カーソルが open か closing の上にあれば、その括弧の対を選ぶ。{ では、カーソルが字下げの中にあれば、字下げの後から探す。
// 回数があれば、回数番目に外側の対を選ぶ。
// カーソルを囲む対が無ければ、カーソルより後ろにある最初の open から始まる対を選ぶ。行をまたいで探す（Vim 9.1 と同じ）。
// このとき回数があれば、その open から後ろへ回数番目の open を選ぶ。
// バックスラッシュでエスケープされた括弧（\( など）は数えない（Vim の 'cpoptions' に M が無い既定の動作）。
//
// a は括弧を含む。i は括弧の内側で、閉じ括弧の前が字下げだけなら、その字下げも含めない。
// 閉じ括弧が行の先頭か字下げの後にあるときは、閉じ括弧の行の先頭までの Exclusive な範囲を返す。
// この範囲は AdjustExclusive で行単位になるので、di{ は中の行を消し、括弧の行を残す。
// 括弧の間が空なら、空の範囲を返す。
//
// 引用符の中の括弧も、区別せずに数える。
// Vim は開き括弧を探すときは区別しないが、閉じ括弧を探すときは区別するので、閉じ括弧の候補が引用符の中にあると Vim と結果が違う。
// 囲む対も後ろの対も無いとき、回数の分の対が無いときは失敗する。
func block(include bool, open, closing rune) TextObjectFunc {
	return func(b *buffer.Buffer, req Request) (Span, error) {
		c := cursor{b: b, Position: req.Cursor}
		if open == '{' {
			for inIndent(&c, 1) {
				if c.inc() != moved {
					break
				}
			}
		}
		if c.char() == open {
			c.Byte++ // 開き括弧の後から探して、その括弧を見つけるため
		}
		// 囲む対があれば後ろ向きに、無ければ前向きに、回数の数だけ開き括弧を探す。
		probe := c
		forward := !findPair(&probe, open, closing, false, false)
		for range req.count1() {
			if !findPair(&c, open, closing, forward, false) {
				return Span{}, ErrNoTarget
			}
		}
		start := c.Position
		if !findPair(&c, closing, open, true, false) {
			return Span{}, ErrNoTarget
		}

		sol := false
		if !include {
			s := cursor{b: b, Position: start}
			s.incl()
			start = s.Position
			sol = c.Byte == 0
			c.decl()
			for inIndent(&c, 1) {
				sol = true
				if c.decl() != moved {
					break
				}
			}
		}
		switch {
		case sol:
			c.incl()
			return Span{Start: start, End: c.Position, Kind: Exclusive}, nil
		case !c.Less(start):
			return Span{Start: start, End: c.Position, Kind: Inclusive}, nil
		default:
			return Span{Start: start, End: start, Kind: Exclusive}, nil
		}
	}
}

// inIndent は、行の先頭の空白の並びの長さが、位置のバイト数に extra を足した値以上かどうかを返す（Vim の inindent()）。
// extra が 1 なら、位置が字下げの空白の上にあるかどうかになる。
func inIndent(c *cursor, extra int) bool {
	line := c.line()
	n := 0
	for n < len(line) && isBlank(rune(line[n])) {
		n++
	}
	return n >= c.Byte+extra
}

// paragraphObject は、ip（include が true なら ap）を返す（Vim の current_par()）。
//
// 空の行と空白だけの行を「白い行」とし、白い行の並びと、白くない行の並びを、それぞれ 1 つの段落として数える。
// ip は、カーソルの行を含む並びを範囲にし、回数があれば続く並びを 1 つずつ数えて広げる。
// ap は、段落とその後の白い行を範囲にする。後に白い行が無ければ、前の白い行を含める。
// カーソルが白い行にあれば、ap は白い行とその後の段落を範囲にする。
// 範囲は Linewise である。回数の分だけ広げられずにバッファの末尾に着いたら失敗する。
func paragraphObject(include bool) TextObjectFunc {
	return func(b *buffer.Buffer, req Request) (Span, error) {
		n := b.LineCount()
		white := func(i int) bool { return isLineWhite(b.Line(i)) }

		start := req.Cursor.Line
		whiteInFront := white(start)
		for start > 0 && white(start-1) == whiteInFront {
			start--
		}

		end := start
		for end < n && white(end) {
			end++
		}
		end--

		i := req.count1()
		if !include && whiteInFront {
			i--
		}
		for i > 0 {
			i--
			if end == n-1 {
				return Span{}, ErrNoTarget
			}
			doWhite := false
			if !include {
				doWhite = white(end + 1)
			}
			if include || !doWhite {
				end++
				for end < n-1 && !white(end+1) {
					end++
				}
			}
			if i == 0 && whiteInFront && include {
				break
			}
			if include || doWhite {
				for end < n-1 && white(end+1) {
					end++
				}
			}
		}

		if !whiteInFront && !white(end) && include {
			for start > 0 && white(start-1) {
				start--
			}
		}
		return Span{Start: buffer.Position{Line: start}, End: buffer.Position{Line: end}, Kind: Linewise}, nil
	}
}
