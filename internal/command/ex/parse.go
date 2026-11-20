package ex

import (
	"regexp"
	"strings"
)

// scanner は、コマンドラインを先頭から 1 バイトずつ読む。
// 区切りに使う文字は全て ASCII なので、バイト単位で読んでも多バイト文字を壊さない。
type scanner struct {
	src  string
	i    int
	base int // src の先頭の、コマンドラインの中のバイト位置
}

func (s *scanner) eof() bool { return s.i >= len(s.src) }

// peek は、次のバイトを返す。終わりでは 0 を返す。
func (s *scanner) peek() byte {
	if s.eof() {
		return 0
	}
	return s.src[s.i]
}

func (s *scanner) next() { s.i++ }

// pos は、次のバイトの、コマンドラインの中の位置を返す。
func (s *scanner) pos() int { return s.base + s.i }

func (s *scanner) rest() string { return s.src[s.i:] }

func (s *scanner) skipBlanks() {
	for s.peek() == ' ' || s.peek() == '\t' {
		s.next()
	}
}

func (s *scanner) skipBlanksAndColons() {
	for s.peek() == ' ' || s.peek() == '\t' || s.peek() == ':' {
		s.next()
	}
}

// letters は、続く ASCII の英字の列を読む。
func (s *scanner) letters() string {
	start := s.i
	for isLetter(s.peek()) {
		s.next()
	}
	return s.src[start:s.i]
}

// number は、続く 10 進の数字の列を読む。数字が無ければ ok は false である。
// 大きすぎる数は、バッファの外を指す値として扱えるように上限で止める。
func (s *scanner) number() (n int, ok bool) {
	for isDigit(s.peek()) {
		if n < 1<<30 {
			n = n*10 + int(s.peek()-'0')
		}
		s.next()
		ok = true
	}
	return n, ok
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// lineRange は、解析した範囲である。行は 1 始まりで、0 は「1 行目の前」を表す。
// line1 と line2 は、バッファの行数を超えることがある（"10" を 3 行のバッファで書いたとき）。
// 行数を超えた行をどう扱うかはコマンドによって違うので、使う側が check で誤りにするか、最後の行に丸めるかを決める。
type lineRange struct {
	line1, line2 int
	count        int // 書かれていたアドレスの数（"%" は 2 と数える）。0 なら範囲は省略されている
	pos          int // 範囲の始まりの位置
	pos1, pos2   int // line1、line2 を書いたアドレスの始まりの位置（誤りの位置に使う）
}

// check は、line1 と line2 が lines 行のバッファの中（0 から lines まで）にあり、line1 が line2 を超えないことを確かめる。
// 外れていれば E16 を返す。位置は、行数を超えたときはそのアドレス、逆順のときは範囲の始まりである。
// 範囲を伴うコマンド（:3d、:1,4s/a/b/）と、範囲の後に "|" が続くとき（:4|、:3,1|）に使う。Vim 9.1 の invalid_range と同じである。
// 範囲を伴うコマンドの逆順の範囲は、check の前に E493 の確認で扱う（runOne）。
// :q のようにウィンドウの番号を取るコマンドでは、lines にウィンドウの数を渡す。
//
// req: FR-08
func (r lineRange) check(lines int) error {
	switch {
	case r.line1 > r.line2:
		return errAt(r.pos, codeInvalidRange, "範囲が正しくありません")
	case r.line1 > lines:
		return errAt(r.pos1, codeInvalidRange, "範囲が正しくありません")
	case r.line2 > lines:
		return errAt(r.pos2, codeInvalidRange, "範囲が正しくありません")
	}
	return nil
}

// addrKind は、範囲のアドレスが何を数えるかを表す。Vim の addr_type に当たる。
type addrKind int

const (
	// addrLines は、アドレスがバッファの行を数えることを表す（:d、:s、:w など）。
	addrLines addrKind = iota
	// addrWindows は、アドレスがウィンドウの番号を数えることを表す（:q）。
	// このエディタのウィンドウは 1 つなので、"." と "$" は 1 を指し、検索とマークと "%" は E16 にする（Vim 9.1 で 1 つのウィンドウのときと同じ）。
	addrWindows
)

// windowCount は、ウィンドウの数である。このエディタは画面を分割しないので 1 である。
const windowCount = 1

// skipRange は、s の先頭の範囲を、評価せずに読み飛ばす。Vim 9.1 の skip_range と同じ規則で読む。
// 範囲を評価する前にコマンド名を知り、アドレスが行とウィンドウのどちらを数えるかを決めるのに使う。
func skipRange(s *scanner) {
	for !s.eof() && strings.IndexByte(" \t0123456789.$%'/?-+,;\\", s.peek()) >= 0 {
		switch ch := s.peek(); ch {
		case '\\':
			if s.i+1 >= len(s.src) || strings.IndexByte("/?&", s.src[s.i+1]) < 0 {
				return
			}
			s.next()
		case '\'':
			s.next()
		case '/', '?':
			s.next()
			for !s.eof() && s.peek() != ch {
				if s.peek() == '\\' && s.i+1 < len(s.src) {
					s.next()
				}
				s.next()
			}
		}
		if !s.eof() {
			s.next()
		}
	}
	s.skipBlanksAndColons()
}

// parseRange は、コマンド名の前の範囲を読み、kind の数え方で行番号（またはウィンドウの番号）に評価する。
// 3 つ以上のアドレスがあれば、Vim と同じく最後の 2 つを使う。
// ";" は、次のアドレスを読む前に、カーソルの行を前のアドレスへ移す（行数を超えた行は最後の行に丸める）。
// 移したカーソルは、範囲の後で誤りになっても戻さない（Vim 9.1 と同じ。:2;9d は E16 で失敗し、カーソルは 2 行目に残る）。
// 移したときの Result.Move は CursorKeepColumn で、後に続くコマンドがカーソルを置けば、そちらで上書きする。
// kind が addrWindows のときも ";" はカーソルの行を移す（Vim 9.1 で :2;3q が 2 行目へ移るのと同じ）。
//
// req: FR-08
func (c *execCtx) parseRange(s *scanner, kind addrKind) (lineRange, error) {
	r := lineRange{pos: s.pos()}
	cur := c.cursor + 1
	if kind == addrWindows {
		cur = windowCount
	}
	if s.peek() == '%' {
		if kind == addrWindows {
			return lineRange{}, errAt(s.pos(), codeInvalidRange, "範囲が正しくありません")
		}
		s.next()
		r.line1, r.line2, r.count = 1, c.b.LineCount(), 2
		r.pos1, r.pos2 = r.pos, r.pos
		s.skipBlanks()
	}
	for {
		start := s.pos()
		lnum, has, err := c.parseAddress(s, cur, kind)
		if err != nil {
			return lineRange{}, err
		}
		s.skipBlanks()
		sep := s.peek()
		if !has {
			if sep != ',' && sep != ';' {
				return r, nil
			}
			// 区切りの前のアドレスを省くと、現在の行を指す（":,3" は ".,3"）。
			lnum = cur
		}
		r.line1, r.line2 = r.line2, lnum
		r.pos1, r.pos2 = r.pos2, start
		r.count++
		switch sep {
		case ',':
			s.next()
		case ';':
			s.next()
			// 現在の行を移すときは、行数を超えた行を最後の行に丸める（Vim の check_cursor と同じ。"10;/pat/" は最後の行から探す）。
			line := min(max(lnum, 1), c.b.LineCount())
			c.setCursor(line-1, CursorKeepColumn)
			if kind == addrLines {
				cur = line
			}
		default:
			if r.count == 1 {
				r.line1 = r.line2
				r.pos1 = r.pos2
			}
			return r, nil
		}
		s.skipBlanks()
		if s.eof() || isLetter(s.peek()) || s.peek() == '"' {
			// 区切りの後のアドレスを省くと、現在の行を指す（":3," は "3,."）。
			r.line1, r.line2 = r.line2, cur
			r.pos1, r.pos2 = r.pos2, s.pos()
			r.count++
			return r, nil
		}
	}
}

// parseAddress は、アドレスを 1 つ読む。cur は "." が指す行（1 始まり）である。
// アドレスが無ければ has は false である。
// kind が addrWindows なら、"$" はウィンドウの数を指し、検索とマークは E16 にする。
// 行が負になれば E16 で失敗する。行数を超えた行は誤りにせず、そのまま返す（扱いは lineRange.check の呼び出し側が決める）。
// "+n" と "-n" の途中で範囲の外に出ても、最後の値が範囲の中なら誤りにしない（3 行のバッファで "4-2" は 2 行目。Vim と同じ）。
//
// req: FR-08
func (c *execCtx) parseAddress(s *scanner, cur int, kind addrKind) (lnum int, has bool, err error) {
	start := s.pos()
	switch ch := s.peek(); {
	case kind == addrWindows && (ch == '/' || ch == '?' || ch == '\''):
		return 0, false, errAt(start, codeInvalidRange, "範囲が正しくありません")
	case kind == addrWindows && ch == '$':
		s.next()
		lnum, has = windowCount, true
	case ch == '.':
		s.next()
		lnum, has = cur, true
	case ch == '$':
		s.next()
		lnum, has = c.b.LineCount(), true
	case isDigit(ch):
		lnum, _ = s.number()
		has = true
	case ch == '/' || ch == '?':
		s.next()
		pat, n, _ := splitDelimited(s.rest(), ch, true)
		s.i += n
		lnum, err = c.search(pat, cur, ch == '/', start)
		if err != nil {
			return 0, false, err
		}
		has = true
	case ch == '\'':
		s.next()
		lnum, err = c.markLine(s, start)
		if err != nil {
			return 0, false, err
		}
		has = true
	case ch == '\\':
		return 0, false, errAt(start, codeNotAvailable, "\\/、\\?、\\& のアドレスは使えません")
	}
	for s.peek() == '+' || s.peek() == '-' {
		if !has {
			// 基点を省いた "+n" と "-n" は、現在の行を基点にする。
			lnum, has = cur, true
		}
		sign := 1
		if s.peek() == '-' {
			sign = -1
		}
		s.next()
		n, ok := s.number()
		if !ok {
			n = 1
		}
		lnum += sign * n
	}
	if has && lnum < 0 {
		return 0, false, errAt(start, codeInvalidRange, "範囲が正しくありません")
	}
	return lnum, has, nil
}

// search は、パターン pat に一致する行を、from 行目（1 始まり）の次の行から探し、その行（1 始まり）を返す。
// forward が false なら前の行から後ろへ探す。バッファの端では反対の端へ折り返し、from 行目自身は最後に調べる。
//
// req: FR-09
func (c *execCtx) search(pat string, from int, forward bool, pos int) (int, error) {
	re, err := c.compile(pat, false, pos)
	if err != nil {
		return 0, err
	}
	n := c.b.LineCount()
	for k := 1; k <= n; k++ {
		var l int
		if forward {
			l = (from - 1 + k) % n
		} else {
			l = ((from-1-k)%n + n) % n
		}
		if re.MatchString(c.b.Line(l)) {
			return l + 1, nil
		}
	}
	return 0, errAt(pos, codeNotFound, "パターンが見つかりません: %s", c.st.LastPattern)
}

// markLine は、"'" の後のマークの名前を読み、State.Mark で行（1 始まり）に解決する。
func (c *execCtx) markLine(s *scanner, pos int) (int, error) {
	if s.eof() {
		return 0, errAt(pos, codeMarkNotSet, "マークの名前がありません")
	}
	name := rune(s.peek())
	s.next()
	if c.st.Mark == nil {
		return 0, errAt(pos, codeMarkNotSet, "マークは設定されていません: %c", name)
	}
	line, ok := c.st.Mark(name)
	if !ok {
		return 0, errAt(pos, codeMarkNotSet, "マークは設定されていません: %c", name)
	}
	return line + 1, nil
}

// compile は、パターンを Go の regexp（RE2）の文法でコンパイルする（FR-09）。
// pat が空なら直前のパターン（State.LastPattern）を使う。ignoreCase なら大文字と小文字を区別しない。
// コンパイルに成功したら、使ったパターンを直前のパターンとして記録し、一致の色を消した状態を解く（State.UsePattern。FR-138）。
// 一致が無くても解く。失敗したパターンは記録しない（Vim と同じ）。
//
// req: FR-09, FR-138
func (c *execCtx) compile(pat string, ignoreCase bool, pos int) (*regexp.Regexp, error) {
	if pat == "" {
		if c.st.LastPattern == "" {
			return nil, errAt(pos, codeNoPrevRegexp, "直前の正規表現がありません")
		}
		pat = c.st.LastPattern
	}
	src := pat
	if ignoreCase {
		src = "(?i)" + src
	}
	re, err := regexp.Compile(src)
	if err != nil {
		return nil, errAt(pos, codeInvalidPattern, "正規表現が正しくありません（Go の regexp の文法で書きます）: %s: %v", pat, err)
	}
	c.st.UsePattern(pat)
	return re, nil
}

// splitDelimited は、s を区切り文字 delim の手前までと、その後に分ける。
// n は、閉じる区切り文字を含めて読んだバイト数である。閉じる区切り文字が無ければ s の全てを読み、closed は false である。
// unescape が true なら、"\" と区切り文字の組を区切り文字にする。false なら "\" を残す（置換の文字列は後で "\" を解釈する）。
// どちらでも、"\" と他の文字の組はそのまま残す。
func splitDelimited(s string, delim byte, unescape bool) (part string, n int, closed bool) {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && i+1 < len(s):
			if s[i+1] != delim || !unescape {
				sb.WriteByte(ch)
			}
			sb.WriteByte(s[i+1])
			i++
		case ch == delim:
			return sb.String(), i + 1, true
		default:
			sb.WriteByte(ch)
		}
	}
	return sb.String(), len(s), false
}

// checkDelimiter は、:s と :g のパターンの区切り文字に使えるかを調べる（Vim と同じく英数字、"\"、"""、"|"、空白は使えない）。
func checkDelimiter(arg string, pos int) (byte, error) {
	if arg == "" {
		return 0, errAt(pos, codeNoPrevRegexp, "直前の正規表現がありません")
	}
	d := arg[0]
	if isLetter(d) || isDigit(d) || d == '\\' || d == '"' || d == '|' || d == ' ' || d == '\t' || d >= 0x80 {
		return 0, errAt(pos, codeBadDelimiter, "正規表現は英数字や \\、\"、| で区切れません")
	}
	return d, nil
}
