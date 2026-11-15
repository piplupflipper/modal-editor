package ex

import (
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// runSubstitute は :[range]s/pat/rep/[flags] [count] を実行する。
//
// フラグは g（行の中の全ての一致を置き換える）、i（大文字と小文字を区別しない）、I（区別する。既定と同じ）、
// e（一致が無くても誤りにしない）を受け付ける。c（確認）などは未対応で、E319 を返す。
// 閉じる区切り文字を省いてもよく、置換の文字列を省くと一致した部分を消す。
//
// 置換は行ごとに行い、範囲の全ての行の置き換えを 1 つのトランザクションにする。
// カーソルは、最後に置き換えた行（置換の文字列が改行を含めば、その最後の行）の最初の空白でない文字へ移る。
// 一致が無ければ動かない（Vim 9.1 と同じ）。
// フラグと回数の後の "|" で、次のコマンドへ続ける。
// 置換の文字列の "~" は直前の置換の文字列に展開し、パターン、展開した置換の文字列、フラグを State.LastSubstitute に記録する。
//
// req: FR-08, FR-09
func runSubstitute(c *execCtx, inv *invocation) error {
	delim, err := checkDelimiter(inv.arg, inv.argPos)
	if err != nil {
		return err
	}
	patPos := inv.argPos + 1
	rest := inv.arg[1:]
	pat, n, closed := splitDelimited(rest, delim, true)
	rest = rest[n:]
	repPos := patPos + n
	var rep string
	if closed {
		rep, n, _ = splitDelimited(rest, delim, false)
		rest = rest[n:]
	}

	s := &scanner{src: rest, base: inv.argPos + len(inv.arg) - len(rest)}
	all, ignoreCase, noErr := false, false, false
	for isLetter(s.peek()) || s.peek() == '&' {
		switch s.peek() {
		case 'g':
			all = true
		case 'i':
			ignoreCase = true
		case 'I':
			ignoreCase = false
		case 'e':
			noErr = true
		default:
			return errAt(s.pos(), codeNotAvailable, ":s のフラグ %c は使えません", s.peek())
		}
		s.next()
	}
	s.skipBlanks()
	countPos := s.pos()
	if cnt, ok := s.number(); ok {
		if err = c.applyCount(inv, cnt, countPos); err != nil {
			return err
		}
	}
	s.skipBlanks()
	switch {
	case s.peek() == '|':
		s.next()
		inv.next, inv.nextPos, inv.hasNext = s.rest(), s.pos(), true
	case s.eof() || s.peek() == '"':
	default:
		return errAt(s.pos(), codeTrailing, "余分な文字があります: %s", s.rest())
	}

	re, err := c.compile(pat, ignoreCase, patPos)
	if err != nil {
		return err
	}
	var prevRep string
	if c.st.LastSubstitute != nil {
		prevRep = c.st.LastSubstitute.Replacement
	}
	rep = expandTilde(rep, prevRep)
	tmpl, err := convertReplacement(rep, repPos)
	if err != nil {
		return err
	}
	c.st.LastSubstitute = &Substitution{Pattern: c.st.LastPattern, Replacement: rep, Global: all, IgnoreCase: ignoreCase}

	limit := 1
	if all {
		limit = -1
	}
	var edits []buffer.Edit
	delta, lastLine, matched := 0, -1, false
	for l := inv.line1; l <= inv.line2; l++ {
		line := c.b.Line(l)
		ms := re.FindAllStringSubmatchIndex(line, limit)
		if ms == nil {
			continue
		}
		matched = true
		var out []byte
		prev := 0
		for _, m := range ms {
			out = append(out, line[prev:m[0]]...)
			out = re.ExpandString(out, tmpl, line, m)
			prev = m[1]
		}
		out = append(out, line[prev:]...)
		at := l + delta
		nl := strings.Count(string(out), "\n")
		if string(out) != line {
			edits = append(edits, buffer.Edit{
				Range: buffer.Range{Start: buffer.Position{Line: at}, End: buffer.Position{Line: at, Byte: len(line)}},
				Text:  string(out),
			})
			c.replaced = append(c.replaced, lineReplace{start: at, old: 1, new: 1 + nl})
		}
		delta += nl
		lastLine = at + nl
	}
	if !matched {
		if noErr || c.inGlobal {
			return nil
		}
		return errAt(patPos, codeNotFound, "パターンが見つかりません: %s", c.st.LastPattern)
	}
	if len(edits) > 0 {
		if err = c.apply(edits...); err != nil {
			return err
		}
		// Vim の do_sub は、カーソルを置き換える最初の行の列 0 へ移してからアンドゥの単位を作る。
		// アンドゥとリドゥの後のカーソルはその位置へ戻る（字下げのある行でも列 0）。:g の中では、最初の :s の位置が残る。
		c.b.SetUndoCursor(buffer.Position{Line: edits[0].Range.Start.Line})
	}
	c.setCursor(lastLine, CursorFirstNonBlank)
	return nil
}

// expandTilde は、置換の文字列 rep の中の "~" を、直前の置換の文字列 prev に置き換える（Vim の magic の ~）。
// "\~" は "~" そのものとして残す。それ以外の "\" の組は、後で convertReplacement が解釈するので変えない。
//
// req: FR-08
func expandTilde(rep, prev string) string {
	if !strings.Contains(rep, "~") {
		return rep
	}
	var sb strings.Builder
	for i := 0; i < len(rep); i++ {
		switch {
		case rep[i] == '\\' && i+1 < len(rep):
			sb.WriteString(rep[i : i+2])
			i++
		case rep[i] == '~':
			sb.WriteString(prev)
		default:
			sb.WriteByte(rep[i])
		}
	}
	return sb.String()
}

// convertReplacement は、Vim の置換の文字列を、Go の Regexp.Expand のテンプレートに変換する（FR-09）。
//
//	&、\0        一致した全体（${0}）
//	\1〜\9       グループ（${1}〜${9}）
//	\&           & そのもの
//	\r、\n       改行（行を分ける）
//	\t           タブ
//	\\           \ そのもの
//	\ と他の文字 その文字そのもの（区切り文字の \/、\~ など）
//	$            $ そのもの（Go の $1 の記法としては解釈しない）
//
// 大文字と小文字の変換（\u、\U、\l、\L、\e、\E）は未対応で、E319 を返す。
// ~（直前の置換の文字列）は、変換の前に expandTilde で展開しておく。
//
// req: FR-09
func convertReplacement(rep string, pos int) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(rep); i++ {
		ch := rep[i]
		switch {
		case ch == '$':
			sb.WriteString("$$")
		case ch == '&':
			sb.WriteString("${0}")
		case ch == '\\' && i+1 < len(rep):
			i++
			d := rep[i]
			switch {
			case isDigit(d):
				sb.WriteString("${")
				sb.WriteByte(d)
				sb.WriteString("}")
			case d == 'r' || d == 'n':
				sb.WriteByte('\n')
			case d == 't':
				sb.WriteByte('\t')
			case strings.IndexByte("uUlLeE", d) >= 0:
				return "", errAt(pos+i-1, codeNotAvailable, "置換の \\%c は使えません", d)
			case d == '$':
				sb.WriteString("$$")
			default:
				sb.WriteByte(d)
			}
		default:
			sb.WriteByte(ch)
		}
	}
	return sb.String(), nil
}

// runGlobal は :[range]g[!]/pat/cmd と :[range]v/pat/cmd を実行する。
//
// Vim と同じく、先に範囲の中でパターンに一致する（:v と :g! では一致しない）行に印を付け、
// 次に印の付いた行を順に現在の行にして cmd を実行する。
// cmd が行を消したり増やしたりしたら、残りの印をそれに合わせて動かし、消えた行の印は捨てる。
// cmd を省くと、最後に印を付けた行の最初の空白でない文字へ移るだけである（Vim は行を表示する）。
// cmd の中の "|" は、印の付いた行ごとにコマンドを区切る（:g/pat/s/a/b/|d）。
// カーソルは、cmd が動かさなければ最後の印の行の先頭へ移る（Vim 9.1 と同じ）。
// :g の中で :g は実行できない。
//
// req: FR-08, FR-09
func runGlobal(c *execCtx, inv *invocation) error {
	if c.inGlobal {
		return errAt(inv.namePos, codeNoNestedGlobal, ":global は入れ子にできません")
	}
	invert := inv.bang || strings.HasPrefix(inv.name, "v")
	delim, err := checkDelimiter(inv.arg, inv.argPos)
	if err != nil {
		return err
	}
	patPos := inv.argPos + 1
	pat, n, _ := splitDelimited(inv.arg[1:], delim, true)
	sub := inv.arg[1+n:]
	subPos := inv.argPos + 1 + n

	re, err := c.compile(pat, false, patPos)
	if err != nil {
		return err
	}
	var marks []int
	for l := inv.line1; l <= inv.line2; l++ {
		if re.MatchString(c.b.Line(l)) != invert {
			marks = append(marks, l)
		}
	}
	if len(marks) == 0 {
		if invert {
			return &Error{Pos: patPos, Msg: "全ての行でパターンが見つかりました: " + c.st.LastPattern}
		}
		return errAt(patPos, codeNotFound, "パターンが見つかりません: %s", c.st.LastPattern)
	}

	c.inGlobal = true
	defer func() { c.inGlobal = false }()
	for i, m := range marks {
		if m < 0 {
			continue
		}
		// Vim と同じく、印の行の先頭へ移ってから cmd を実行する。cmd を省いたときは、Vim の行の表示に合わせて最初の空白でない文字へ置く。
		c.setCursor(m, CursorLineStart)
		if strings.TrimSpace(sub) == "" {
			c.move = CursorFirstNonBlank
		}
		done := len(c.replaced)
		if err := c.runLine(sub, subPos); err != nil {
			return err
		}
		for _, r := range c.replaced[done:] {
			for j := i + 1; j < len(marks); j++ {
				marks[j] = r.shift(marks[j])
			}
		}
	}
	return nil
}

// shift は、置き換えの後に、行 m（0 始まり）がどの行になったかを返す。行が消えていれば -1 を返す。
// 置き換えた範囲の中の行は、置き換えた後の同じ位置の行（範囲を越えれば最後の行）へ移す。
func (r lineReplace) shift(m int) int {
	switch {
	case m < 0 || m < r.start:
		return m
	case m >= r.start+r.old:
		return m + r.new - r.old
	case r.new == 0:
		return -1
	default:
		return r.start + min(m-r.start, r.new-1)
	}
}
