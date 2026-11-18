package command

import (
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// 囲みの操作（sa、sd、sr）は、vim-sandwich の既定の動作を写す（FR-73〜FR-75、基本設計 4.8）。
// 振る舞いは、Neovim 0.12.5 に vim-sandwich だけを読み込んで確かめた。
//
//   - (、[、{、< は、開きと閉じのどちらの文字で指定しても同じ組を指す。どちらで足しても空白は足さない（saiw( も saiw) も (foo)）
//   - それ以外の文字は、同じ文字で前と後ろを囲む。vim-sandwich が特別に扱う t、f、i などの文字（タグ、関数の呼び出し、入力）も、
//     ここではただの文字として扱う
//   - 引用符（"、'、`）の囲みは 1 行の中だけで探し、バックスラッシュで逃がした引用符を数えない（vim-sandwich の quoteescape）。
//     引用符でない同じ文字の囲みは、行をまたいで探す
//   - sd と sr の空白は、空白とタブの続き（\s\+）を 1 つの囲みとして扱う（vim-sandwich の既定のレシピ）
//   - <CR> は囲みの文字に使えない（vim-sandwich は空の行の囲みとして扱うが、ここでは持たない）

// surroundPair は、囲みの文字 c に当たる開きと閉じの文字列と、入れ子を数える組であるかを返す。
func surroundPair(c rune) (open, closing string, nesting bool) {
	switch c {
	case '(', ')':
		return "(", ")", true
	case '[', ']':
		return "[", "]", true
	case '{', '}':
		return "{", "}", true
	case '<', '>':
		return "<", ">", true
	}
	return string(c), string(c), false
}

// isQuote は、1 行の中だけで探し、バックスラッシュで逃がせる引用符であるかを返す。
func isQuote(c rune) bool { return c == '"' || c == '\'' || c == '`' }

// checkSurroundChar は、囲みの文字として使えるかを確かめる。
func checkSurroundChar(c rune) error {
	if c == 0 || c == '\n' {
		return failf("invalid surround character %q", c)
	}
	return nil
}

// opSurroundAdd は、sa{モーションかテキストオブジェクト}{文字} である（FR-73）。範囲の前と後ろに囲みを足す。
//
// 文字単位の範囲では、範囲の直前に開き、直後に閉じを足し、カーソルを囲んだテキストの最初の文字へ置く（saiw( の後のカーソルは単語の頭）。
// 行単位の範囲（saj(、saip( など）では、vim-sandwich と同じく、開きと閉じをそれぞれ独立した行として、範囲の前と後ろに足す。
// 開きの行には範囲の最初の行の字下げを、閉じの行には最後の行の字下げを付け、カーソルを最初の行の最初の空白でない文字へ置く。
// ただし引用符（"、'、`）は行にせず、最初の行の字下げの後と最後の行の末尾の空白の前に足す（saj" は "ab\ncd"）。
//
// 回数は、他のオペレーターと同じく対象の回数に掛ける（2saiw( は sa2iw( と同じ）。vim-sandwich は sa の前の回数を
// 囲む回数として扱うが、ここでは持たない。
//
// req: FR-73
func opSurroundAdd(s *State, cmd editcmd.Command, rg region, _ opOptions) error {
	if err := checkSurroundChar(cmd.Char); err != nil {
		return err
	}
	open, closing, _ := surroundPair(cmd.Char)
	if rg.linewise {
		l1, l2 := rg.r.Start.Line, rg.r.End.Line
		ind1, ind2 := leadingBlank(s.line(l1)), leadingBlank(s.line(l2))
		// アンドゥの後のカーソルは、vim-sandwich と同じく最初の行の頭へ戻す（開きの行を足した位置）。
		s.markUndoCursor(pos(l1, 0))
		if isQuote(cmd.Char) {
			// 引用符は、vim-sandwich の既定のレシピ（linewise が 0）と同じく行にせず、最初の行の最初の空白でない文字の前と、
			// 最後の行の最後の空白でない文字の後に足す。
			first := len(ind1)
			last := len(strings.TrimRight(s.line(l2), " \t"))
			if err := s.apply(
				buffer.Edit{Range: emptyRange(pos(l2, last)), Text: closing},
				buffer.Edit{Range: emptyRange(pos(l1, first)), Text: open},
			); err != nil {
				return err
			}
			s.setCursorNormal(pos(l1, first+len(open)))
			return nil
		}
		if err := s.apply(
			buffer.Edit{Range: emptyRange(s.lineEnd(l2)), Text: "\n" + ind2 + closing},
			buffer.Edit{Range: emptyRange(pos(l1, 0)), Text: ind1 + open + "\n"},
		); err != nil {
			return err
		}
		s.setCursorNormal(s.firstNonBlankPos(l1 + 1))
		return nil
	}
	start, end := rg.r.Start, rg.r.End
	if err := s.apply(
		buffer.Edit{Range: emptyRange(end), Text: closing},
		buffer.Edit{Range: emptyRange(start), Text: open},
	); err != nil {
		return err
	}
	s.setCursorNormal(pos(start.Line, start.Byte+len(open)))
	return nil
}

// leadingBlank は、行の先頭の空白とタブを返す。
func leadingBlank(line string) string { return line[:len(line)-len(strings.TrimLeft(line, " \t"))] }

// actSurroundDelete は、sd{文字} である（FR-74）。カーソルを含む、内側から回数番目の囲みを消す
// （vim-sandwich と同じく、2sd( は内側から 2 番目の丸括弧）。カーソルは、消した開きの位置（囲んでいたテキストの頭）へ移る。
// 開きが行頭にあったときは、その行の最初の空白でない文字へ移る。
// 囲みが見つからなければ、何も変えずに失敗する。消したテキストはレジスターに入れない。
//
// req: FR-74
func actSurroundDelete(s *State, cmd editcmd.Command) error {
	if err := checkSurroundChar(cmd.Char); err != nil {
		return err
	}
	open, closing, err := s.findSurround(cmd.Char, max(cmd.Count, 1))
	if err != nil {
		return err
	}
	// アンドゥの後のカーソルは、Vim と同じく変更の始まり（開きの位置）へ戻す。
	s.markUndoCursor(open.Start)
	defer s.clearUndoCursor()
	if err := s.apply(buffer.Edit{Range: closing}, buffer.Edit{Range: open}); err != nil {
		return err
	}
	at := open.Start
	if at.Byte == 0 {
		// vim-sandwich は、行頭の開きを消したときは、カーソルを行の最初の空白でない文字へ置く（"( foo )" の sd( で f）。
		if b := len(leadingBlank(s.line(at.Line))); b < len(s.line(at.Line)) {
			at.Byte = b
		}
	}
	s.setCursorNormal(at)
	return nil
}

// actSurroundReplace は、sr{前の文字}{後の文字} である（FR-75）。カーソルを含む、内側から回数番目の前の文字の囲みを、
// 後の文字の囲みに替える。カーソルは、新しい開きの直後（囲んでいるテキストの頭）へ移る。開きが行末にあれば、次の行の最初の空白でない文字へ移る。
//
// req: FR-75
func actSurroundReplace(s *State, cmd editcmd.Command) error {
	if err := checkSurroundChar(cmd.Char); err != nil {
		return err
	}
	if err := checkSurroundChar(cmd.Char2); err != nil {
		return err
	}
	open, closing, err := s.findSurround(cmd.Char, max(cmd.Count, 1))
	if err != nil {
		return err
	}
	newOpen, newClose, _ := surroundPair(cmd.Char2)
	s.markUndoCursor(open.Start)
	defer s.clearUndoCursor()
	if err := s.apply(buffer.Edit{Range: closing, Text: newClose}, buffer.Edit{Range: open, Text: newOpen}); err != nil {
		return err
	}
	at := pos(open.Start.Line, open.Start.Byte+len(newOpen))
	if at.Byte >= len(s.line(at.Line)) && at.Line < s.lastLine() {
		// 開きが行末にあれば、vim-sandwich と同じく次の行の最初の空白でない文字（囲んでいるテキストの頭）へ置く。
		at = s.firstNonBlankPos(at.Line + 1)
	}
	s.setCursorNormal(at)
	return nil
}

// findSurround は、カーソルを含む、内側から n 番目の c の囲みの、開きと閉じの範囲を返す。
// カーソルが開きか閉じの文字の上にあれば、その囲みはカーソルを含む。
// 入れ子を数えない囲み（引用符、同じ文字、空白）は、vim-sandwich と同じく n が 1 のときだけ探す。
func (s *State) findSurround(c rune, n int) (open, closing buffer.Range, err error) {
	notFound := failf("surround %q not found", c)
	if c == ' ' || c == '\t' {
		if n != 1 {
			return open, closing, notFound
		}
		return s.findBlankSurround()
	}
	o, cl, nesting := surroundPair(c)
	if nesting {
		return s.findNestedSurround(o[0], cl[0], n)
	}
	if n != 1 {
		return open, closing, notFound
	}
	return s.findSameSurround(c)
}

// findNestedSurround は、入れ子を数える括弧（1 バイトの o と c）の、カーソルを含む内側から n 番目の組を探す。行をまたいで探す。
// 括弧は ASCII なので、UTF-8 の多バイト文字の途中のバイトと取り違えない。
func (s *State) findNestedSurround(o, c byte, n int) (open, closing buffer.Range, err error) {
	notFound := failf("surround %q not found", o)
	p := s.cursor
	// カーソルの上の閉じ括弧は、カーソルを含む組の閉じなので、手前から開きを探す。
	skipCursor := p.Byte < len(s.line(p.Line)) && s.line(p.Line)[p.Byte] == c
	depth, found := 0, 0
	var at buffer.Position
	for l := p.Line; l >= 0 && found < n; l-- {
		line := s.line(l)
		b := len(line) - 1
		if l == p.Line {
			b = min(p.Byte, len(line)-1)
		}
		for ; b >= 0; b-- {
			if l == p.Line && b == p.Byte && skipCursor {
				continue
			}
			switch line[b] {
			case c:
				depth++
			case o:
				if depth > 0 {
					depth--
					continue
				}
				found++
				at = pos(l, b)
			}
			if found == n {
				break
			}
		}
	}
	if found < n {
		return open, closing, notFound
	}
	// 開きに対応する閉じを前へ探す。
	depth = 0
	for l := at.Line; l <= s.lastLine(); l++ {
		line := s.line(l)
		b := 0
		if l == at.Line {
			b = at.Byte + 1
		}
		for ; b < len(line); b++ {
			switch line[b] {
			case o:
				depth++
			case c:
				if depth > 0 {
					depth--
					continue
				}
				end := pos(l, b)
				if end.Less(p) {
					return open, closing, notFound
				}
				return byteRange(at), byteRange(end), nil
			}
		}
	}
	return open, closing, notFound
}

// byteRange は、p の 1 バイトの範囲を返す。
func byteRange(p buffer.Position) buffer.Range {
	return buffer.Range{Start: p, End: pos(p.Line, p.Byte+1)}
}

// findSameSurround は、前と後ろが同じ文字 c の囲みを探す。
//
// カーソルがその文字の上にあれば、Vim の i" と同じく、行の頭から数えて奇数番目なら開き、偶数番目なら閉じとして組を作る。
// 文字の上になければ、カーソルの手前の最も近い文字と、後ろの最も近い文字を組にする。
// 引用符は 1 行の中だけで探し、バックスラッシュで逃がした引用符を数えない。引用符でない文字は、行に無ければ前と後ろの行へ探しに行く。
func (s *State) findSameSurround(c rune) (open, closing buffer.Range, err error) {
	notFound := failf("surround %q not found", c)
	quote := isQuote(c)
	d := string(c)
	p := s.cursor
	cur := delimiters(s.line(p.Line), d, quote)
	w := len(d)
	mk := func(l, b int) buffer.Range { return buffer.Range{Start: pos(l, b), End: pos(l, b+w)} }

	// before と after は、カーソルの行で、カーソルより前と後ろの文字の位置である。
	var before, after []int
	on := -1
	for i, b := range cur {
		switch {
		case b < p.Byte:
			before = append(before, b)
		case b == p.Byte:
			on = i
		default:
			after = append(after, b)
		}
	}
	if on >= 0 {
		if on%2 == 1 {
			return mk(p.Line, cur[on-1]), mk(p.Line, cur[on]), nil
		}
		if len(after) > 0 {
			return mk(p.Line, cur[on]), mk(p.Line, after[0]), nil
		}
		if quote {
			return open, closing, notFound
		}
		l, b, ok := s.nextDelimiter(p.Line+1, d)
		if !ok {
			return open, closing, notFound
		}
		return mk(p.Line, cur[on]), mk(l, b), nil
	}
	var ol, ob, cl, cb int
	switch {
	case len(before) > 0:
		ol, ob = p.Line, before[len(before)-1]
	case quote:
		return open, closing, notFound
	default:
		var ok bool
		if ol, ob, ok = s.prevDelimiter(p.Line-1, d); !ok {
			return open, closing, notFound
		}
	}
	switch {
	case len(after) > 0:
		cl, cb = p.Line, after[0]
	case quote:
		return open, closing, notFound
	default:
		var ok bool
		if cl, cb, ok = s.nextDelimiter(p.Line+1, d); !ok {
			return open, closing, notFound
		}
	}
	return mk(ol, ob), mk(cl, cb), nil
}

// delimiters は、行の中の d の位置（バイト）を順に返す。escape なら、奇数個のバックスラッシュの後の d を除く。
func delimiters(line, d string, escape bool) []int {
	var out []int
	backslashes := 0
	for b := 0; b < len(line); {
		if strings.HasPrefix(line[b:], d) && (!escape || backslashes%2 == 0) {
			out = append(out, b)
			backslashes = 0
			b += len(d)
			continue
		}
		if line[b] == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
		_, w := utf8.DecodeRuneInString(line[b:])
		b += w
	}
	return out
}

// nextDelimiter は、行 from から後ろへ、最初の d の位置を探す。
func (s *State) nextDelimiter(from int, d string) (line, b int, ok bool) {
	for l := from; l <= s.lastLine(); l++ {
		if ds := delimiters(s.line(l), d, false); len(ds) > 0 {
			return l, ds[0], true
		}
	}
	return 0, 0, false
}

// prevDelimiter は、行 from から前へ、最後の d の位置を探す。
func (s *State) prevDelimiter(from int, d string) (line, b int, ok bool) {
	for l := from; l >= 0; l-- {
		if ds := delimiters(s.line(l), d, false); len(ds) > 0 {
			return l, ds[len(ds)-1], true
		}
	}
	return 0, 0, false
}

// findBlankSurround は、空白とタブの続きの囲みを、カーソルの行の中で探す（vim-sandwich の sd<Space>）。
// カーソルが空白の上にあれば、その続きを開きとし、次の続きを閉じとする。
// 空白の上になければ、カーソルの手前の最も近い続きを開き、後ろの最も近い続きを閉じとする。
func (s *State) findBlankSurround() (open, closing buffer.Range, err error) {
	notFound := failf("surround %q not found", ' ')
	p := s.cursor
	line := s.line(p.Line)
	type run struct{ start, end int }
	var runs []run
	for b := 0; b < len(line); {
		if line[b] != ' ' && line[b] != '\t' {
			b++
			continue
		}
		e := b
		for e < len(line) && (line[e] == ' ' || line[e] == '\t') {
			e++
		}
		runs = append(runs, run{b, e})
		b = e
	}
	mk := func(r run) buffer.Range { return buffer.Range{Start: pos(p.Line, r.start), End: pos(p.Line, r.end)} }
	for i, r := range runs {
		if r.start <= p.Byte && p.Byte < r.end {
			if i+1 < len(runs) {
				return mk(r), mk(runs[i+1]), nil
			}
			return open, closing, notFound
		}
		if r.start > p.Byte {
			if i > 0 {
				return mk(runs[i-1]), mk(r), nil
			}
			return open, closing, notFound
		}
	}
	return open, closing, notFound
}
