// Package target は、編集コマンドの「対象」（基本設計 3 章）であるモーションとテキストオブジェクトを、
// バッファの上の位置と範囲に解決する。
//
// 基本設計 2 章の internal/command の一部で、internal/buffer を読むだけの純粋な関数の集まりである。
// バッファを変更せず、カーソル、望みの列、直前の f の検索などの状態も持たない。
// 状態は呼び出し側（コマンドの実行）が持ち、Request で渡して Result で受け取る。
//
// 振る舞いは Vim 9 の既定の設定（'cpoptions' の既定値、'whichwrap' は "b,s"、'startofline' はオン、'selection' は inclusive）に合わせる。
// Vim と違う点は、各関数の doc コメントに書く。
//
// # 位置の表し方
//
// 位置は buffer.Position（行の番号と、行の先頭からのバイト数）で表す。
// 行の長さ（行末）を指す位置を「行末の位置」と呼ぶ。
// Vim は行末の NUL にカーソルを置いて単語の境界を数えるので、この実装も行末の位置を空白と同じ分類の 1 文字として数える。
// オペレーターの無いモーションの結果は、空の行を除いて行末の位置を指さない。
package target

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// Kind は、範囲の端の扱い（Vim の exclusive、inclusive、linewise。`:help exclusive`、`:help linewise`）を表す。
type Kind int

const (
	// Exclusive は、終わりの位置の文字を範囲に含まないことを表す。
	Exclusive Kind = iota
	// Inclusive は、終わりの位置の文字を範囲に含むことを表す。
	Inclusive
	// Linewise は、始まりの行から終わりの行までの行全体を範囲にすることを表す。
	Linewise
)

// String は、デバッグとテストのために種類の名前を返す。
func (k Kind) String() string {
	switch k {
	case Exclusive:
		return "exclusive"
	case Inclusive:
		return "inclusive"
	case Linewise:
		return "linewise"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Operator は、モーションがどのオペレーターの後に使われたかを表す。
// オペレーターの有無で、w の行末の扱い（`:help w` の特例）や、l が行末の位置まで進めるかどうかが変わる。
type Operator int

const (
	// NoOperator は、オペレーターの無い移動を表す。
	NoOperator Operator = iota
	// AnyOperator は、c 以外のオペレーター（d、y など）の後であることを表す。
	AnyOperator
	// ChangeOperator は、c の後であることを表す。cw と cW は ce と cE のように動く（`:help cw`）。
	ChangeOperator
)

// WantEOL は、望みの列が「行末」であることを表す。$ の後の j と k は、各行の最後の文字へ移る。
const WantEOL = math.MaxInt

// Find は、直前の f、F、t、T の検索を表す。; と , はこれを繰り返す。
// Char が 0 のゼロ値は「検索していない」を表す。
type Find struct {
	Char     rune
	Backward bool // F と T のとき true
	Till     bool // t と T のとき true
}

// Request は、モーションとテキストオブジェクトへの入力である。
type Request struct {
	// Cursor は、現在のカーソルの位置である。バッファの中の文字の境界を指す必要がある。
	Cursor buffer.Position
	// Count は、入力された回数である。0 は回数の省略を表し、多くのコマンドで 1 として扱う。
	// gg、G、% は、回数の有無で意味が変わる。
	Count int
	// Char は、f、F、t、T で検索する文字である。
	Char rune
	// Want は、j と k で使う望みの列（Vim の curswant）である。
	//
	// 仮の実装として、表示の列ではなく、行頭からの文字数で数える。
	// 表示幅（タブ、全角文字）の計算は internal/ui の担当なので、それができた時点で表示の列に替える。
	Want int
	// Last は、; と , が繰り返す直前の検索である。
	Last Find
	// Operator は、このモーションの前に入力されたオペレーターである。テキストオブジェクトは見ない。
	Operator Operator
}

// count1 は、回数の省略を 1 として返す。
func (r Request) count1() int {
	return max(r.Count, 1)
}

// Result は、モーションの結果である。
type Result struct {
	// Pos は、移動先の位置である。
	Pos buffer.Position
	// Kind は、オペレーターと組み合わせたときの範囲の扱いである。
	Kind Kind
	// Want は、移動した後の望みの列である。呼び出し側はこの値で望みの列を置き換える。
	// j と k は Request.Want をそのまま返し、$ は WantEOL を返す。
	Want int
	// Find は、f、F、t、T のときに、; と , のために記録する検索である。それ以外のモーションではゼロ値である。
	Find Find
}

// Span は、オペレーターが働く範囲、またはテキストオブジェクトの範囲である。
// Start は End より後に無い。
type Span struct {
	Start buffer.Position
	End   buffer.Position
	Kind  Kind
}

// Range は、範囲を、buffer.Slice と buffer.Edit で使える半開区間に直して返す。
//
//   - Exclusive は、Start から End の直前まで
//   - Inclusive は、Start から End の文字の直後まで。End が行末の位置なら、改行は含めず行末まで
//   - Linewise は、Start の行の先頭から End の行の行末まで。行の後の改行をどう扱うかは、オペレーターが決める
func (s Span) Range(b *buffer.Buffer) buffer.Range {
	switch s.Kind {
	case Exclusive:
	case Inclusive:
		line := b.Line(s.End.Line)
		if s.End.Byte < len(line) {
			_, size := utf8.DecodeRuneInString(line[s.End.Byte:])
			return buffer.Range{Start: s.Start, End: buffer.Position{Line: s.End.Line, Byte: s.End.Byte + size}}
		}
	case Linewise:
		return buffer.Range{
			Start: buffer.Position{Line: s.Start.Line},
			End:   buffer.Position{Line: s.End.Line, Byte: len(b.Line(s.End.Line))},
		}
	}
	return buffer.Range{Start: s.Start, End: s.End}
}

// MotionFunc は、モーション 1 つを解決する関数である。
// 該当が無い場合（f で文字が無い、% で括弧が無い、など）は ErrNoTarget を返す。
type MotionFunc func(b *buffer.Buffer, req Request) (Result, error)

// TextObjectFunc は、テキストオブジェクト 1 つを解決する関数である。
// 返す範囲は AdjustExclusive を適用する前のものである。
type TextObjectFunc func(b *buffer.Buffer, req Request) (Span, error)

// ErrNoTarget は、モーションの移動先、またはテキストオブジェクトの範囲が見つからないことを表す。
var ErrNoTarget = errors.New("target: no target")

// ErrInvalidRequest は、Request の Cursor がバッファの中の文字の境界を指していないか、Count が負であることを表す。
var ErrInvalidRequest = errors.New("target: invalid request")

// motions は、モーションの名前（入力するキーの列）と、それを解決する関数の表である。
// 1 つのモーションを 1 つの関数にし、対象の増減はこの表の行の増減で行う。
//
// req: FR-03
var motions = map[string]MotionFunc{
	"h":  left,
	"l":  right,
	"j":  down,
	"k":  up,
	"w":  wordForward(false),
	"W":  wordForward(true),
	"b":  wordBackward(false),
	"B":  wordBackward(true),
	"e":  wordEnd(false),
	"E":  wordEnd(true),
	"0":  lineStart,
	"^":  firstNonBlank,
	"$":  lineEnd,
	"gg": gotoLine(false),
	"G":  gotoLine(true),
	"f":  findChar(false, false),
	"F":  findChar(true, false),
	"t":  findChar(false, true),
	"T":  findChar(true, true),
	";":  repeatFind(false),
	",":  repeatFind(true),
	"%":  percent,
	"{":  paragraph(false),
	"}":  paragraph(true),
}

// textObjects は、テキストオブジェクトの名前と、それを解決する関数の表である。
// b と B は、Vim と同じく ( と { の別名にする。閉じ括弧の名前（i) など）も Vim と同じく別名として持つ。
//
// req: FR-03
var textObjects = map[string]TextObjectFunc{
	"iw": word(false, false),
	"aw": word(true, false),
	"iW": word(false, true),
	"aW": word(true, true),
	`i"`: quote(false, '"'),
	`a"`: quote(true, '"'),
	"i'": quote(false, '\''),
	"a'": quote(true, '\''),
	"i(": block(false, '(', ')'),
	"a(": block(true, '(', ')'),
	"i)": block(false, '(', ')'),
	"a)": block(true, '(', ')'),
	"ib": block(false, '(', ')'),
	"ab": block(true, '(', ')'),
	"i[": block(false, '[', ']'),
	"a[": block(true, '[', ']'),
	"i]": block(false, '[', ']'),
	"a]": block(true, '[', ']'),
	"i{": block(false, '{', '}'),
	"a{": block(true, '{', '}'),
	"i}": block(false, '{', '}'),
	"a}": block(true, '{', '}'),
	"iB": block(false, '{', '}'),
	"aB": block(true, '{', '}'),
	"ip": paragraphObject(false),
	"ap": paragraphObject(true),
}

// LookupMotion は、名前が name のモーションを返す。
// 返す関数は、Request を確かめてから解決し、正しくなければ ErrInvalidRequest を返す。
func LookupMotion(name string) (MotionFunc, bool) {
	f, ok := motions[name]
	if !ok {
		return nil, false
	}
	return func(b *buffer.Buffer, req Request) (Result, error) {
		if err := validate(b, req); err != nil {
			return Result{}, err
		}
		return f(b, req)
	}, true
}

// LookupTextObject は、名前が name のテキストオブジェクトを返す。
// 返す関数は、Request を確かめてから解決し、正しくなければ ErrInvalidRequest を返す。
func LookupTextObject(name string) (TextObjectFunc, bool) {
	f, ok := textObjects[name]
	if !ok {
		return nil, false
	}
	return func(b *buffer.Buffer, req Request) (Span, error) {
		if err := validate(b, req); err != nil {
			return Span{}, err
		}
		return f(b, req)
	}, true
}

// MotionNames は、表にあるモーションの名前を並べ替えて返す。
func MotionNames() []string {
	return sortedKeys(motions)
}

// TextObjectNames は、表にあるテキストオブジェクトの名前を並べ替えて返す。
func TextObjectNames() []string {
	return sortedKeys(textObjects)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// validate は、Cursor がバッファの中の文字の境界を指し、Count が負でないことを確かめる。
func validate(b *buffer.Buffer, req Request) error {
	if req.Count < 0 {
		return fmt.Errorf("%w: negative count %d", ErrInvalidRequest, req.Count)
	}
	if _, err := b.Slice(buffer.Range{Start: req.Cursor, End: req.Cursor}); err != nil {
		return fmt.Errorf("%w: cursor: %w", ErrInvalidRequest, err)
	}
	return nil
}

// OperatorSpan は、from（オペレーターを入力したときのカーソル）からモーションの結果 res までを、オペレーターが働く範囲にして返す。
// 始まりと終わりを並べ替えてから、AdjustExclusive を適用する。
//
// req: FR-03
func OperatorSpan(b *buffer.Buffer, from buffer.Position, res Result) Span {
	start, end := from, res.Pos
	if end.Less(start) {
		start, end = end, start
	}
	return AdjustExclusive(b, Span{Start: start, End: end, Kind: res.Kind})
}

// AdjustExclusive は、`:help exclusive` の 2 つの規則を、オペレーターが働く範囲に適用する。
//
//  1. Exclusive で、終わりが行の先頭にあり、範囲が 2 行以上にわたる場合、終わりを前の行の最後の文字へ移して Inclusive にする
//  2. 1 の場合に、始まりが行の最初の空白でない文字かそれより前（字下げの中）にあれば、Linewise にする
//
// 例えば d} は、段落の後の空の行の先頭までの Exclusive な範囲になり、2 の規則で行単位の削除になる。
// 前の行が空なら、終わりはその空の行の先頭で Exclusive のままにする（Vim と同じ）。
//
// req: FR-03
func AdjustExclusive(b *buffer.Buffer, s Span) Span {
	if s.Kind != Exclusive || s.End.Byte != 0 || s.End.Line <= s.Start.Line {
		return s
	}
	s.End.Line--
	if strings.Trim(b.Line(s.Start.Line)[:s.Start.Byte], " \t") == "" {
		s.Kind = Linewise
		return s
	}
	line := b.Line(s.End.Line)
	s.End.Byte = len(line)
	if len(line) > 0 {
		_, size := utf8.DecodeLastRuneInString(line)
		s.End.Byte -= size
		s.Kind = Inclusive
	}
	return s
}
