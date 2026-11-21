package editor

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/target"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 右端のスクロールバーと印（基本設計 6.7 の「スクロールバー」。FR-186〜FR-188）の、イベントループの側。
//
// 描くたびに（bufferFrame）、設定の今の値（scrollbar.show、scrollbar.currentOnly、scrollbar.excludedFileTypes、scrollbar.marks）を読んで、
// client の画面のバッファの表示領域にだけ ui.Frame.Scrollbar を詰める。つまみの位置と、表示領域に収まるかは描画（internal/ui）が決める。
// 印は、種類の表（scrollbarKinds）の名前を scrollbar.marks に書いた種類だけを、表の関数で作る。種類の名前で分岐する処理は表の外に無い。
// 後に拡張機能が自分の印（git の変更の位置など）を足すときは、この表に行を足す形になる見込みである。

// scrollbarKind は、スクロールバーの印の 1 つの種類である（scrollbarKinds の行）。
type scrollbarKind struct {
	// name は、設定の scrollbar.marks に書く名前である。
	name string
	// marks は、バッファ bs の印の一覧を作る。search は、一致に色を付けるパターン（ui.Frame.Search）で、付けていなければ nil である。
	marks func(e *Editor, bs *bufferState, search *regexp.Regexp) []ui.ScrollbarMarks
}

// scrollbarKinds は、スクロールバーの印の種類の表である。
var scrollbarKinds = []scrollbarKind{
	{name: "diagnostic", marks: (*Editor).diagnosticScrollbarMarks},
	{name: "search", marks: (*Editor).searchScrollbarMarks},
}

// 同じセルに重なったときの優先（ui.ScrollbarMarks.Priority）。大きいほうを出す。
// 誤りと警告を検索の一致より上にし、検索の一致を情報とヒントより上にした（#348 の本文の例の順）。
// nvim-scrollview の既定（search が 70、error が 60、warn が 50、info が 40、hint が 30）は検索の一致を最も上にするが、
// 一致の行の多い検索で、数の少ない誤りと警告の印が隠れないようにするため、順を変えた。値の間を空けたのは、後に種類を足すときに間に入れられるようにするためである。
const (
	scrollbarPriorityError   = 60
	scrollbarPriorityWarning = 50
	scrollbarPrioritySearch  = 45
	scrollbarPriorityInfo    = 40
	scrollbarPriorityHint    = 30
)

// diagnosticScrollbar は、診断の重要度ごとの、スクロールバーの印の優先と色と記号の名前である。
var diagnosticScrollbar = []struct {
	severity      ext.DiagnosticSeverity
	priority      int
	style, symbol string
}{
	{ext.SeverityError, scrollbarPriorityError, ui.StyleScrollbarDiagnosticError, ui.SymbolScrollbarDiagnosticError},
	{ext.SeverityWarning, scrollbarPriorityWarning, ui.StyleScrollbarDiagnosticWarning, ui.SymbolScrollbarDiagnosticWarning},
	{ext.SeverityInfo, scrollbarPriorityInfo, ui.StyleScrollbarDiagnosticInfo, ui.SymbolScrollbarDiagnosticInfo},
	{ext.SeverityHint, scrollbarPriorityHint, ui.StyleScrollbarDiagnosticHint, ui.SymbolScrollbarDiagnosticHint},
}

// uiScrollbar は、client の画面のバッファ bs の表示領域の右端に描くスクロールバー（ui.Frame.Scrollbar）を、設定の今の値から作る（FR-186、FR-187）。
// 出さないとき（scrollbar.show が偽、scrollbar.currentOnly が真でフォーカスのある client（Editor.focused。最後にキーを受けた client か、
// 本体がフォーカスを移した先の client）でない、bs のファイルの種類が scrollbar.excludedFileTypes にある）は nil を返す。
// search は、一致に色を付けるパターン（bufferFrame の ui.Frame.Search）で、検索の一致の印はこのパターンで作る。
// 描くたびに読むので、:option.set で値を変えたときに呼ぶ処理は無い（基本設計 7.3）。使うのは bufferFrame だけである。
//
// req: FR-186, FR-187
func (e *Editor) uiScrollbar(bs *bufferState, search *regexp.Regexp) *ui.Scrollbar {
	if !e.options.Get(option.ScrollbarShow).Bool() {
		return nil
	}
	if e.options.Get(option.ScrollbarCurrentOnly).Bool() {
		cur := e.focused
		if cur == nil {
			cur = e.op
		}
		if cur != nil && e.view != cur {
			return nil
		}
	}
	if slices.Contains(optionList(e.options.Get(option.ScrollbarExcludedFileTypes).String()), bs.fileType()) {
		return nil
	}
	sb := &ui.Scrollbar{}
	names := optionList(e.options.Get(option.ScrollbarMarks).String())
	for _, k := range scrollbarKinds {
		if slices.Contains(names, k.name) {
			sb.Marks = append(sb.Marks, k.marks(e, bs, search)...)
		}
	}
	return sb
}

// optionList は、コンマで区切った設定の値 s を、前後の空白を除いた空でない名前の並びにする（scrollbar.marks、scrollbar.excludedFileTypes）。
func optionList(s string) []string {
	var out []string
	for name := range strings.SplitSeq(s, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// diagnosticScrollbarMarks は、バッファ bs の診断の印を、重要度ごとの種類にして返す（FR-187）。
// 印を出す行は、印の列の診断の印と同じく範囲の始まりの行である。知らない重要度の診断は出さない。
//
// req: FR-187
func (e *Editor) diagnosticScrollbarMarks(bs *bufferState, _ *regexp.Regexp) []ui.ScrollbarMarks {
	if len(bs.diagnostics) == 0 {
		return nil
	}
	lines := map[ext.DiagnosticSeverity][]int{}
	for _, ds := range bs.diagnostics {
		for _, d := range ds {
			lines[d.Severity] = append(lines[d.Severity], d.Range.Start.Line)
		}
	}
	var out []ui.ScrollbarMarks
	for _, k := range diagnosticScrollbar {
		ls := lines[k.severity]
		if len(ls) == 0 {
			continue
		}
		slices.Sort(ls)
		out = append(out, ui.ScrollbarMarks{Lines: ls, Priority: k.priority, Style: k.style, Symbol: k.symbol})
	}
	return out
}

// scrollbarSearchTimeout は、スクロールバーの検索の一致の印のために、バッファの全体から一致のある行を集める時間の上限である（FR-188）。
// 超えたら、そのパターンとバージョン番号の間は印を出さない。Vim 9.1 の 'redrawtime' の既定（2000 ミリ秒。'hlsearch' の色付けの上限）と同じにした。
// テストが差し替える。
var scrollbarSearchTimeout = 2 * time.Second

// scrollSearchCheckBytes は、一致のある行を集めるときに、時間の上限と取り消しを調べる間隔の、読んだバイト数である。
const scrollSearchCheckBytes = 1 << 16

// scrollSearch は、1 つのバッファの、スクロールバーの検索の一致の印のための、一致のある行の一覧と、集めている検索である（bufferState.scrollSearch）。
type scrollSearch struct {
	// done は、最後に集め終えた結果である。has が false なら、まだ集め終えていない。
	done searchLinesResult
	has  bool
	// running は、集めている検索のパターンとバッファとバージョン番号である。active が false なら、集めていない。
	running searchKey
	active  bool
	// seq は、検索を始めるたびに進む番号である。結果がこれと違えば、取り消した検索の結果なので捨てる。
	seq uint64
	// cancel は、集めている検索を取り消す。
	cancel context.CancelFunc
}

// searchKey は、一致のある行を集めたときのパターンとバッファとバージョン番号である。
type searchKey struct {
	pattern string
	buf     *buffer.Buffer
	version uint64
}

// searchLinesResult は、一致のある行を集めた結果である。
type searchLinesResult struct {
	key searchKey
	// lines は、一致のある行（0 から数える）を小さい順に並べたものである。時間の上限を超えたら nil である。
	lines []int
}

// searchScrollbarMarks は、バッファ bs の検索の一致の印を返す（FR-187、FR-188）。search が nil（一致に色を付けていない）なら出さない
// （nvim-scrollview も、'hlsearch' の色が付いている間だけ出す）。
//
// 一致のある行は、描画の中では集めない。描くたびに、最後に集め終えた結果がパターン、バッファ、バージョン番号のどれかで今と違えば、
// イベントループの外の goroutine で集め始め（startScrollSearch）、集め終えるまでは前の結果で描く。
//
//   - パターンが同じでバージョン番号だけが違う（編集した）ときは、前の結果（行がずれていることがある）を出したまま集め直す。
//     集めている間に続けて編集しても、集めている検索は取り消さず、終わってから今のバージョン番号で集め直す。打つたびに始め直すと、
//     大きなバッファで打ち続ける間、いつまでも集め終えないためである。このため、集める検索は 1 つのバッファに 1 つまでである
//   - パターンが違う（バッファが違う）ときは、前の結果を出さず、集めている検索を取り消して集め直す
//   - 時間の上限（scrollbarSearchTimeout）を超えた結果も覚え、パターンかバージョン番号が変わるまで集め直さない（描くたびに上限まで待たない）
//
// req: FR-187, FR-188
func (e *Editor) searchScrollbarMarks(bs *bufferState, search *regexp.Regexp) []ui.ScrollbarMarks {
	if search == nil {
		return nil
	}
	s := &bs.scrollSearch
	key := searchKey{pattern: search.String(), buf: bs.buf, version: bs.buf.Version()}
	if !s.has || s.done.key != key {
		switch {
		case !s.active:
			e.startScrollSearch(bs, search, key)
		case s.running.pattern != key.pattern || s.running.buf != key.buf:
			s.cancel()
			e.startScrollSearch(bs, search, key)
		}
	}
	if !s.has || s.done.key.pattern != key.pattern || s.done.key.buf != key.buf || len(s.done.lines) == 0 {
		return nil
	}
	return []ui.ScrollbarMarks{{
		Lines: s.done.lines, Priority: scrollbarPrioritySearch, Style: ui.StyleScrollbarSearch, Symbol: ui.SymbolScrollbarSearch,
	}}
}

// startScrollSearch は、バッファ bs の、パターン re（key のパターン）の一致のある行を、イベントループの外の goroutine で集め始める（FR-188）。
// テキストは、打つたびの検索と同じ写し（incSearchLines）を使う。写すのはイベントループの goroutine で、量は行の数に比例する。
// 結果は inspect でイベントループへ戻し、後に始めた検索が無ければ覚える（scrollSearchDone）。覚えた後の描画が印を出す。
//
// req: FR-188
func (e *Editor) startScrollSearch(bs *bufferState, re *regexp.Regexp, key searchKey) {
	s := &bs.scrollSearch
	lines := incSearchLines(bs)
	ctx, cancel := context.WithTimeout(context.Background(), scrollbarSearchTimeout)
	s.seq++
	s.running, s.active, s.cancel = key, true, cancel
	seq, stop := s.seq, e.stop
	go func() {
		defer e.recoverPanic()
		defer cancel()
		found, err := matchLines(ctx, lines, re)
		select {
		case e.inspect <- func() { scrollSearchDone(s, seq, key, found, err) }:
		case <-stop:
		}
	}()
}

// scrollSearchDone は、seq 番目に始めた、key の一致のある行を集めた結果を受け取る。イベントループの goroutine で呼ぶ。
// 後に始めた検索がある（取り消した検索の結果）なら捨てる。時間の上限を超えたら、一致の無い結果として覚える。
func scrollSearchDone(s *scrollSearch, seq uint64, key searchKey, found []int, err error) {
	if s.seq != seq {
		return
	}
	s.active, s.cancel = false, nil
	if err != nil {
		found = nil
	}
	s.done, s.has = searchLinesResult{key: key, lines: found}, true
}

// matchLines は、src の行のうち、re に一致する所がある行（空の一致を含む）を小さい順に返す。
// 一致の色付け（FR-137）と同じく行ごとに当てるので、印のある行は、色の付く一致のある行と同じになる。
// 読んだ量が scrollSearchCheckBytes に達するたびに ctx を調べ、終わっていれば ctx.Err() を返す。
// src を読むだけで変えないので、イベントループの外の goroutine で呼んでよい。
//
// req: FR-188
func matchLines(ctx context.Context, src target.LineSnapshot, re *regexp.Regexp) ([]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []int
	read := 0
	for i, line := range src {
		if re.MatchString(line) {
			out = append(out, i)
		}
		if read += len(line) + 1; read >= scrollSearchCheckBytes {
			read = 0
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
