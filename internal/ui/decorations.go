package ui

import (
	"cmp"
	"iter"
	"math"
	"slices"
)

// Decorations は、構文の色の範囲の一覧を、行で引けるように分けたものである。
//
// decoration/set はバッファ全体の一覧を送る（基本設計 6.1）。描画のたびに全体をなめないように、
// internal/editor は一覧を受け取ったときに NewDecorations で 1 回だけ作り、描画ではそれを Frame に渡す。
// 作った後は変更しないので、同じ値を何度の描画に渡してもよく、作った goroutine と別の goroutine で読んでもよい。
//
// 範囲は、すべての行の分を 1 つの列 spans に行の順に詰め、行ごとの開始の位置 first で引く。
// 行ごとにスライスを持つと、一覧の大きさに比例する数の割り当てになり、作る時間の大半がガベージコレクションになった（#74）。
// spans はポインターを持たない（色の種類の名前は styles の添字で持つ）ので、ガベージコレクションが中を走査しない。
type Decorations struct {
	first  []int      // n 行目の範囲は spans[first[n]:first[n+1]]。長さは行数 + 1
	spans  []decoSpan // 行の順、行の中では開始のバイトの順に並べる
	styles []string   // 色の種類の名前の表。decoSpan.style が添字
}

// decoSpan は、1 行の中の構文の色の範囲 [start, end) である。end が math.MaxInt なら行末までを表す。
type decoSpan struct {
	start, end int
	order      int // 元の一覧での位置。重なったときは大きい方を上に重ねる
	style      int // Decorations.styles の添字
}

// NewDecorations は、hs を行ごとに分けた Decorations を返す。lines は、描画するテキストの行数である。
//
// 行をまたぐ範囲は、またいだ各行に分ける（途中の行は行の全体。行末の 1 セルは含めない）。
// End が Start より前の範囲は、2 つを入れ替えて扱う。空の範囲は捨てる。
// 位置は描画する時点のテキストを指す必要がある。行の長さを超えるバイト位置は、描画のときに行末に詰める。
// lines 行目以降の部分は捨てる。作る表の大きさは、lines と、各範囲が 0 行目から lines-1 行目のうち掛かる行の数の和で決まり、
// 範囲の行番号の大きさには比例しない。前は終わりの行まで表を伸ばしたので、不具合のある拡張機能が送った
// 終わりが 20 億行目の範囲 1 つで、本体が 20 億要素の表を確保しようとした（#75）。
func NewDecorations(hs []Highlight, lines int) *Decorations {
	return NewDecorationsSeq(slices.Values(hs), lines)
}

// NewDecorationsSeq は、NewDecorations の、範囲の列を iter.Seq で受け取る形である。
// hs を 2 回なめるので、hs は 2 回とも同じ列を返す必要がある。
// 呼ぶ側が持つ別の型の一覧から、[]Highlight の写しを作らずに表を作れる。
// 10 万行の JSON の一覧（80 万の範囲）では、写しが 38 MB になり、その走査でガベージコレクションの時間が増えた（#74）。
//
// req: FR-35, NFR-03
func NewDecorationsSeq(hs iter.Seq[Highlight], lines int) *Decorations {
	d := &Decorations{}
	if lines <= 0 {
		return d
	}
	// 1 回目に行ごとの範囲の数を数え、2 回目にその位置へ詰める。
	d.first = make([]int, lines+1)
	count := d.first[1:]
	forEachLineSpan(hs, lines, func(n int, _ decoSpan, _ string) { count[n]++ })
	total := 0
	for n, c := range count {
		count[n] = total
		total += c
	}
	// ここで count[n]（= first[n+1]）は n 行目の開始の位置である。詰めるたびに 1 つずつ進め、詰め終えると n+1 行目の開始の位置になる。
	d.spans = make([]decoSpan, total)
	styleIndex := styleTable{}
	forEachLineSpan(hs, lines, func(n int, sp decoSpan, style string) {
		sp.style = styleIndex.index(style)
		d.spans[count[n]] = sp
		count[n]++
	})
	d.styles = styleIndex.names
	for n := range lines {
		l := d.spans[d.first[n]:d.first[n+1]]
		// 構文の色の一覧はたいてい開始の順に並んでいるので、並んでいれば並べ替えない。
		if !slices.IsSortedFunc(l, compareStart) {
			slices.SortStableFunc(l, compareStart)
		}
	}
	return d
}

func compareStart(a, b decoSpan) int { return cmp.Compare(a.start, b.start) }

// forEachLineSpan は、hs の範囲を行ごとに分け、0 行目から lines-1 行目に掛かる部分を f に渡す。
// decoSpan の order には hs での位置を入れ、style は入れずに名前を f の 3 つ目の引数で渡す。
func forEachLineSpan(hs iter.Seq[Highlight], lines int, f func(n int, sp decoSpan, style string)) {
	i := -1
	for h := range hs {
		i++
		s, e := h.Range.Start, h.Range.End
		if e.Less(s) {
			s, e = e, s
		}
		if s == e || e.Line < 0 || s.Line >= lines {
			continue
		}
		for n := max(s.Line, 0); n <= min(e.Line, lines-1); n++ {
			sp := decoSpan{start: 0, end: math.MaxInt, order: i}
			if n == s.Line {
				sp.start = max(s.Byte, 0)
			}
			if n == e.Line {
				sp.end = e.Byte
			}
			if sp.start < sp.end {
				f(n, sp, h.Style)
			}
		}
	}
}

// styleTable は、色の種類の名前に、現れた順に添字を振る。
// 名前の種類はふつう十に満たないので、直前の名前と表を先頭から比べる。
// 同じ表から復号した名前は文字列の中身を共有するので、比べるのは速い。
// 不具合のある拡張機能が多くの種類を送っても比べる回数が増え続けないように、種類が smallStyles を超えたら map で引く。
type styleTable struct {
	names  []string
	last   int
	byName map[string]int
}

const smallStyles = 16

func (t *styleTable) index(name string) int {
	if t.last < len(t.names) && t.names[t.last] == name {
		return t.last
	}
	var i int
	if t.byName != nil {
		var ok bool
		if i, ok = t.byName[name]; !ok {
			i = -1
		}
	} else {
		i = slices.Index(t.names, name)
	}
	if i < 0 {
		i = len(t.names)
		t.names = append(t.names, name)
		if t.byName == nil && len(t.names) > smallStyles {
			t.byName = make(map[string]int, len(t.names))
			for j, n := range t.names {
				t.byName[n] = j
			}
		} else if t.byName != nil {
			t.byName[name] = i
		}
	}
	t.last = i
	return i
}

// line は、n 行目の範囲を、開始のバイトの順に返す。d が nil でもよい。
func (d *Decorations) line(n int) []decoSpan {
	if d == nil || n < 0 || n >= len(d.first)-1 {
		return nil
	}
	return d.spans[d.first[n]:d.first[n+1]]
}

// style は、sp の色の種類の名前を返す。sp は d.line が返したものである必要がある。
func (d *Decorations) style(sp decoSpan) string {
	return d.styles[sp.style]
}
