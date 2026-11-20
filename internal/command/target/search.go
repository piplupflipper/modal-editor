package target

import (
	"context"
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
// パターンを受け取るコマンドラインは呼び出し側が持つ。打っている間に <C-g> と <C-t> で動かした数（req.SearchSteps）だけ、一致を動かす。
//
// req: FR-03, FR-09
func searchMotion(backward bool) MotionFunc {
	return func(b *buffer.Buffer, req Request) (Result, error) {
		return search(b, req, req.Cursor, Search{Pattern: req.Search.Pattern, Backward: backward}, req.SearchSteps)
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
		return search(b, req, req.Cursor, s, 0)
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
// 回数の分だけ、見つけた一致から次の一致を探す。steps が 0 でなければ、その一致からさらに動かす（FindMatch。/ と ? の <C-g> と <C-t>）。
// どこにも無ければ ErrPatternNotFound を返す。返す Result.Search は s である。
// 一致を決める規則は、打っている間の打つたびの検索（FR-153）と同じ FindMatch である。時間の上限は付けない。
//
// req: FR-09
func search(b *buffer.Buffer, req Request, from buffer.Position, s Search, steps int) (Result, error) {
	m, err := FindMatch(context.Background(), b, from, s, req.count1(), steps)
	switch {
	case errors.Is(err, ErrPatternNotFound):
		return Result{Search: s}, err
	case err != nil:
		return Result{}, err
	}
	res := at(b, req, m.Range.Start, Exclusive)
	res.Search, res.Wrapped = s, m.Wrapped
	return res, nil
}

// Lines は、検索が読む行の並びである。*buffer.Buffer と、イベントループの外で検索するために写した行（LineSnapshot）が満たす。
type Lines interface {
	// Line は、i 行目（0 から数える）の文字列を、改行を除いて返す。
	Line(i int) string
	// LineCount は、行の数を返す。
	LineCount() int
}

// LineSnapshot は、バッファの行を写した並びである（Snapshot）。
// 写した後にバッファを変えても変わらないので、イベントループの外の goroutine で読んでよい（基本設計 2.1）。
type LineSnapshot []string

// Line は、i 行目の文字列を返す。
func (l LineSnapshot) Line(i int) string { return l[i] }

// LineCount は、行の数を返す。
func (l LineSnapshot) LineCount() int { return len(l) }

// Snapshot は、b の行を写した並びを返す。行の文字列は写さずに共有する（Go の文字列は変わらない）ので、
// 時間と量は、行の長さによらず行の数に比例する。イベントループの goroutine で呼ぶ。
func Snapshot(b *buffer.Buffer) LineSnapshot {
	out := make(LineSnapshot, b.LineCount())
	for i := range out {
		out[i] = b.Line(i)
	}
	return out
}

// Match は、検索で見つけた一致である。
type Match struct {
	// Range は、一致の範囲（半開区間）である。空の一致（^、$、x* など）は、Start と End が同じである。
	Range buffer.Range
	// Wrapped は、バッファの端で折り返して見つけたかである。
	Wrapped bool
}

// checkBytes は、FindMatch が ctx の終わりを調べる間隔である。行を読み進めて、読んだバイト数（行の数を足す）がこれに達するたびに調べる。
const checkBytes = 64 << 10

// FindMatch は、src の from から s のパターンを s の向きに count 回検索した一致から、count の一致を 1 単位にして steps 単位だけ
// 動かした一致を返す。steps が正ならバッファの末尾の方へ、負なら先頭の方へ動かす（s の向きによらない）。count が 1 未満なら 1 にする。
// from は src の中に丸める。
//
// / と ? を <CR> で決めたときの移り先（search。Request.SearchSteps）と、打っている間の打つたびの検索（FR-153）は、
// どちらもこの関数で一致を決める。このため、打っている間に出した一致と、<CR> で移る先は同じになる。
// 1 回ずつの検索の規則は search の doc のとおりである。
// steps は、打っている間の <C-g> と <C-t> である。nvim 0.12.5 の <C-g> と <C-t> も、/ と ? のどちらでも <C-g> が末尾の方へ動き、
// 1 回で回数の数だけ一致を進める（2026-11-20 に nvim --clean で、3/foo の後の <C-t> と、?foo の後の <C-g> を確かめた）。
//
// ctx が終わったら（打つたびの検索の時間の上限を超えた、次の入力で取り消した）、探すのをやめて ctx.Err() を返す。
// ctx は、行を読み進めて読んだ量が checkBytes に達するたびに調べる。1 行の中の正規表現の照合は途中で止めない。
// パターンが空なら ErrNoPattern、正しくなければ *PatternError、どこにも無ければ ErrPatternNotFound を返す。
// src を読むだけで変えないので、src が LineSnapshot ならイベントループの外の goroutine で呼んでよい。
//
// req: FR-153
func FindMatch(ctx context.Context, src Lines, from buffer.Position, s Search, count, steps int) (Match, error) {
	if s.Pattern == "" {
		return Match{}, ErrNoPattern
	}
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return Match{}, &PatternError{Pattern: s.Pattern, Err: err}
	}
	n := src.LineCount()
	if n == 0 {
		return Match{}, ErrPatternNotFound
	}
	from.Line = min(max(from.Line, 0), n-1)
	from.Byte = min(max(from.Byte, 0), len(src.Line(from.Line)))
	sc := scanner{ctx: ctx, src: src, re: re}
	m := Match{Range: buffer.Range{Start: from, End: from}}
	walk := func(backward bool, times int) error {
		for range times {
			r, w, err := sc.next(m.Range.Start, backward)
			if err != nil {
				return err
			}
			m.Range, m.Wrapped = r, m.Wrapped || w
		}
		return nil
	}
	count = max(count, 1)
	if err := walk(s.Backward, count); err != nil {
		return Match{}, err
	}
	if steps != 0 {
		if err := walk(steps < 0, count*abs(steps)); err != nil {
			return Match{}, err
		}
	}
	return m, nil
}

// abs は、n の絶対値を返す。
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// scanner は、FindMatch の 1 回ずつの検索である。読んだ量を数え、checkBytes ごとに ctx の終わりを調べる。
type scanner struct {
	ctx  context.Context
	src  Lines
	re   *regexp.Regexp
	read int
}

// next は、from の次（backward なら前）の一致の範囲と、バッファの端で折り返したかを返す。
func (sc *scanner) next(from buffer.Position, backward bool) (buffer.Range, bool, error) {
	if backward {
		return sc.backward(from)
	}
	return sc.forward(from)
}

// advance は、n バイトの行を読んだことを数え、checkBytes に達したら ctx の終わりを調べる。
func (sc *scanner) advance(n int) error {
	sc.read += n + 1
	if sc.read < checkBytes {
		return nil
	}
	sc.read = 0
	return sc.ctx.Err()
}

// matchRange は、line 行目の一致 m（regexp の位置の組）の範囲を返す。
func matchRange(line int, m []int) buffer.Range {
	return buffer.Range{Start: buffer.Position{Line: line, Byte: m[0]}, End: buffer.Position{Line: line, Byte: m[1]}}
}

// matchCol は、一致の先頭 start を、カーソルの位置と比べる列にする。行末の位置での一致は 1 つ前にする（Vim の searchit()）。
func matchCol(line string, start int) int {
	if start >= len(line) {
		return start - 1
	}
	return start
}

// forward は、前向きの検索の 1 回である（search の doc の規則）。
func (sc *scanner) forward(from buffer.Position) (buffer.Range, bool, error) {
	line := sc.src.Line(from.Line)
	size := 1
	if from.Byte < len(line) {
		_, size = utf8.DecodeRuneInString(line[from.Byte:])
	}
	for _, m := range sc.re.FindAllStringIndex(line, -1) {
		if matchCol(line, m[0]) >= from.Byte+size {
			return matchRange(from.Line, m), false, nil
		}
	}
	if err := sc.advance(len(line)); err != nil {
		return buffer.Range{}, false, err
	}
	n := sc.src.LineCount()
	for k := 1; k <= n; k++ {
		l := (from.Line + k) % n
		s := sc.src.Line(l)
		if m := sc.re.FindStringIndex(s); m != nil {
			return matchRange(l, m), l <= from.Line, nil
		}
		if err := sc.advance(len(s)); err != nil {
			return buffer.Range{}, false, err
		}
	}
	return buffer.Range{}, false, ErrPatternNotFound
}

// backward は、後ろ向きの検索の 1 回である（search の doc の規則）。
func (sc *scanner) backward(from buffer.Position) (buffer.Range, bool, error) {
	line := sc.src.Line(from.Line)
	if from.Byte > 0 {
		var found []int
		for _, m := range sc.re.FindAllStringIndex(line, -1) {
			if matchCol(line, m[0]) >= from.Byte {
				break
			}
			found = m
		}
		if found != nil {
			return matchRange(from.Line, found), false, nil
		}
	}
	if err := sc.advance(len(line)); err != nil {
		return buffer.Range{}, false, err
	}
	n := sc.src.LineCount()
	for k := 1; k <= n; k++ {
		l := ((from.Line-k)%n + n) % n
		s := sc.src.Line(l)
		if ms := sc.re.FindAllStringIndex(s, -1); len(ms) > 0 {
			return matchRange(l, ms[len(ms)-1]), l >= from.Line, nil
		}
		if err := sc.advance(len(s)); err != nil {
			return buffer.Range{}, false, err
		}
	}
	return buffer.Range{}, false, ErrPatternNotFound
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
