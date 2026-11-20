package target

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// Search は、検索のパターンと向きである。
// Pattern が空のゼロ値は「検索していない」を表す。
type Search struct {
	// Pattern は、Go の regexp（RE2）の文法のパターンである（FR-09）。
	Pattern string
	// Backward は、後ろ向き（? と #）の検索であることを表す。
	Backward bool
}

// ErrPatternNotFound は、検索のパターンがバッファのどこにも見つからないことを表す（Vim の E486）。
// ErrNoTarget を包む。
var ErrPatternNotFound = fmt.Errorf("%w: pattern not found", ErrNoTarget)

// ErrNoPattern は、n や N で繰り返す直前のパターンが無いことを表す（Vim の E35）。
var ErrNoPattern = errors.New("target: no previous pattern")

// ErrNoString は、* や # で、カーソルの位置から行末までに空白でない文字が無いことを表す（Vim の E348）。
// ErrNoTarget を包む。
var ErrNoString = fmt.Errorf("%w: no string under cursor", ErrNoTarget)

// PatternError は、検索のパターンが Go の regexp の文法として正しくないことを表す。
type PatternError struct {
	Pattern string
	Err     error
}

func (e *PatternError) Error() string {
	return fmt.Sprintf("target: invalid pattern %q: %v", e.Pattern, e.Err)
}

func (e *PatternError) Unwrap() error { return e.Err }

// searchMotion は、/（backward が true なら ?）を返す。req.Search.Pattern を検索する。向きは req.Search を見ない。
// パターンを受け取るコマンドラインは呼び出し側が持つ。
//
// req: FR-03, FR-09
func searchMotion(backward bool) MotionFunc {
	return func(b *buffer.Buffer, req Request) (Result, error) {
		return search(b, req, req.Cursor, Search{Pattern: req.Search.Pattern, Backward: backward})
	}
}

// searchRepeat は、n（reverse が true なら N）を返す。req.Search のパターンを、req.Search の向き（N は逆の向き）に検索する。
// 呼び出し側は、直前の検索のパターンと向きを req.Search に入れる。パターンが空なら ErrNoPattern を返す。
//
// req: FR-03
func searchRepeat(reverse bool) MotionFunc {
	return func(b *buffer.Buffer, req Request) (Result, error) {
		s := req.Search
		s.Backward = s.Backward != reverse
		return search(b, req, req.Cursor, s)
	}
}

// WordPattern は、* と # が検索のパターンにする、カーソルの位置 at の単語に全体で一致するパターンを返す
// （Vim の nv_ident() と find_ident_at_pos()）。カーソルは動かさない（FR-139）。
//
//   - カーソルの位置から行末へ向かって、最初の単語の文字（分類が classWord 以上の文字）を探し、その文字を含む同じ分類の並びを単語にする
//   - 行末までに単語の文字が無ければ、最初の空白でない文字を探し、その文字から前の同じ分類の並びの先頭から、空白の手前までを文字列にする
//   - どちらも無ければ ErrNoString を返す
//
// 単語は regexp.QuoteMeta で文字どおりに一致させ、両端が ASCII の単語の文字（英数字と _）なら、その側に \b を付ける
// （Vim の \< と \>）。Go の \b は ASCII の文字だけで境界を決めるので、両端が日本語などの文字なら境界を付けない。
// このため、「日本語」の * は「日本語化」の中の「日本語」にも一致する（Vim は一致しない）。
//
// req: FR-139
func WordPattern(b *buffer.Buffer, at buffer.Position) (string, error) {
	line := b.Line(at.Line)
	start, end, keyword, ok := identAt(line, at.Byte)
	if !ok {
		return "", ErrNoString
	}
	word := line[start:end]
	pat := regexp.QuoteMeta(word)
	if keyword {
		if r, _ := utf8.DecodeRuneInString(word); isASCIIWord(r) {
			pat = `\b` + pat
		}
		if r, _ := utf8.DecodeLastRuneInString(word); isASCIIWord(r) {
			pat += `\b`
		}
	}
	return pat, nil
}

// identAt は、line の at から、* と # が検索する単語か文字列の範囲 [start, end) を返す。keyword は単語であるかを表す。
func identAt(line string, at int) (start, end int, keyword, ok bool) {
	i := at
	for i < len(line) && classAt(line, i) < classWord {
		i = nextCell(line, i)
	}
	keyword = i < len(line)
	if !keyword {
		i = at
		for i < len(line) && classAt(line, i) == classBlank {
			i = nextCell(line, i)
		}
		if i >= len(line) {
			return 0, 0, false, false
		}
	}
	class := classAt(line, i)
	start = i
	for start > 0 {
		p := prevCell(line, start)
		if classAt(line, p) != class {
			break
		}
		start = p
	}
	end = i
	for end < len(line) {
		c := classAt(line, end)
		if keyword && c != class || !keyword && c == classBlank {
			break
		}
		end = nextCell(line, end)
	}
	return start, end, keyword, true
}

// classAt は、line の i バイト目の文字の分類を返す。
func classAt(line string, i int) int { return classOf(classRune(line, i), false) }

// isASCIIWord は、Go の regexp の \b が単語の文字とする文字（ASCII の英数字と _）かを返す。
func isASCIIWord(r rune) bool {
	return r == '_' || '0' <= r && r <= '9' || 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z'
}

// search は、from から s のパターンを s の向きに回数だけ検索し、見つけた一致の先頭へ移る結果を返す。Exclusive である。
//
// 振る舞いは Vim 9.1 の既定（'wrapscan' がオン、'ignorecase' がオフ、'cpoptions' に c を含む）の searchit() に合わせる。
//
//   - 前向きの検索は、from の文字の次から探す。同じ行の一致は、行の先頭から重ならないように並べた一致（regexp の FindAll）のうち、
//     先頭が from の文字の次以降のものである。ただし、行末の位置での一致（/$ など）は 1 つ前の位置にあるものとして比べる
//     （Vim と同じく、行の最後の文字にいる /$ の n は次の行へ進む）
//   - 後ろ向きの検索は、同じ行の一致のうち、先頭が from より前の最後のものを選ぶ（行末の位置での一致は前向きと同じく数える）。
//     from が行の先頭なら前の行から探す
//   - バッファの端に着いたら、反対の端から from の行まで続け、Result.Wrapped を true にする
//
// パターンは行ごとに当てる。改行をまたぐ一致（\n を含むパターン）は見つからない。
// 回数の分だけ、見つけた一致から次の一致を探す。どこにも無ければ ErrPatternNotFound を返す。
// 返す Result.Search は s である。
//
// req: FR-09
func search(b *buffer.Buffer, req Request, from buffer.Position, s Search) (Result, error) {
	if s.Pattern == "" {
		return Result{}, ErrNoPattern
	}
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return Result{}, &PatternError{Pattern: s.Pattern, Err: err}
	}
	p := from
	wrapped := false
	for range req.count1() {
		var w, ok bool
		if s.Backward {
			p, w, ok = searchBackward(b, re, p)
		} else {
			p, w, ok = searchForward(b, re, p)
		}
		if !ok {
			return Result{Search: s}, ErrPatternNotFound
		}
		wrapped = wrapped || w
	}
	res := at(b, req, p, Exclusive)
	res.Search, res.Wrapped = s, wrapped
	return res, nil
}

// matchCol は、一致の先頭 start を、カーソルの位置と比べる列にする。行末の位置での一致は 1 つ前にする（Vim の searchit()）。
func matchCol(line string, start int) int {
	if start >= len(line) {
		return start - 1
	}
	return start
}

func searchForward(b *buffer.Buffer, re *regexp.Regexp, from buffer.Position) (p buffer.Position, wrapped, ok bool) {
	line := b.Line(from.Line)
	size := 1
	if from.Byte < len(line) {
		_, size = utf8.DecodeRuneInString(line[from.Byte:])
	}
	for _, m := range re.FindAllStringIndex(line, -1) {
		if matchCol(line, m[0]) >= from.Byte+size {
			return buffer.Position{Line: from.Line, Byte: m[0]}, false, true
		}
	}
	n := b.LineCount()
	for k := 1; k <= n; k++ {
		l := (from.Line + k) % n
		if m := re.FindStringIndex(b.Line(l)); m != nil {
			return buffer.Position{Line: l, Byte: m[0]}, l <= from.Line, true
		}
	}
	return from, false, false
}

func searchBackward(b *buffer.Buffer, re *regexp.Regexp, from buffer.Position) (p buffer.Position, wrapped, ok bool) {
	line := b.Line(from.Line)
	if from.Byte > 0 {
		found := -1
		for _, m := range re.FindAllStringIndex(line, -1) {
			if matchCol(line, m[0]) >= from.Byte {
				break
			}
			found = m[0]
		}
		if found >= 0 {
			return buffer.Position{Line: from.Line, Byte: found}, false, true
		}
	}
	n := b.LineCount()
	for k := 1; k <= n; k++ {
		l := ((from.Line-k)%n + n) % n
		if ms := re.FindAllStringIndex(b.Line(l), -1); len(ms) > 0 {
			return buffer.Position{Line: l, Byte: ms[len(ms)-1][0]}, l >= from.Line, true
		}
	}
	return from, false, false
}

// SplitSearch は、/ と ? のコマンドラインに入力した文字列 text を、パターンとオフセットに分ける。
// delim（'/' か '?'）が "\" の付かない形で現れたら、その手前までをパターン、その後をオフセットにする（Vim の /foo/e）。
// "\" と区切り文字の組は、パターンにそのまま残す（Go の regexp でも \/ と \? はその文字に一致する）。
func SplitSearch(text string, delim rune) (pattern, offset string, hasOffset bool) {
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\':
			i++
		case rune(text[i]) == delim:
			return text[:i], text[i+1:], true
		}
	}
	return text, "", false
}
