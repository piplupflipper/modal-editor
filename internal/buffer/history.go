package buffer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
)

// historyFormat は、MarshalHistory が書くバイト列の形式の版である。
// 形式を変えたら 1 増やす。違う版のバイト列は RestoreHistory が ErrHistoryFormat で断る（読めない形式を黙って誤読しない。FR-165）。
const historyFormat = 1

// ErrHistoryFormat は、RestoreHistory に渡したバイト列が、このパッケージの書く形式でない（版が違う、途中で切れている、
// 値が範囲の外にある）ことを表す。
var ErrHistoryFormat = errors.New("buffer: unknown undo history format")

// ErrHistoryMismatch は、RestoreHistory に渡した履歴が、バッファの今のテキストに当てはまらない（履歴の編集を
// 今のテキストから順に取り消せない、取り消した後にやり直しても今のテキストに戻らない）ことを表す。
var ErrHistoryMismatch = errors.New("buffer: undo history does not match the text")

// ErrHistoryNotEmpty は、アンドゥかリドゥの履歴を既に持つバッファへ RestoreHistory を呼んだことを表す。
var ErrHistoryNotEmpty = errors.New("buffer: undo history is not empty")

// MarshalHistory は、アンドゥとリドゥの履歴を、RestoreHistory で読み戻せるバイト列にする（FR-165）。
//
// 書くのは、アンドゥとリドゥの単位（編集、逆の編集、変更の前のカーソル、末尾の改行の記録の変更、単位の識別子）と、
// 行の無いバッファの記録（MarkNoLines）である。
// 編集のテキストはバイトのまま書くので、不正な UTF-8 を含むバッファの履歴も変えずに往復する。
// テキスト、バージョン番号、保存済みの記録、編集の禁止、末尾の改行を補う設定は書かない。
// 書いた時点のテキストは呼び出し側がファイルとして持つ（ファイルのアンドゥの履歴は、ファイルの中身と組にして残す）。
//
// 開いているアンドゥのグループは、書いた履歴では閉じたものとして扱う。
// 履歴が空なら、空でないバイト列（版と空の一覧）を返す。履歴があるかは CanUndo と CanRedo で見る。
//
// req: FR-165
func (b *Buffer) MarshalHistory() []byte {
	w := historyWriter{}
	w.num(historyFormat)
	w.num(b.nextID)
	ids := make([]uint64, 0, len(b.noLinesAt))
	for id, on := range b.noLinesAt {
		if on {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	w.num(uint64(len(ids)))
	for _, id := range ids {
		w.num(id)
	}
	for _, steps := range [][]step{b.undo, b.redo} {
		w.num(uint64(len(steps)))
		for _, s := range steps {
			w.step(s)
		}
	}
	return w.buf
}

// RestoreHistory は、MarshalHistory が書いたバイト列 data から、アンドゥとリドゥの履歴を読み戻す（FR-165）。
//
// バッファは、履歴を書いた時点と同じテキストで作ったばかりのもの（ReadFile で開き直したファイルなど）を渡す。
// アンドゥかリドゥの履歴を既に持つバッファには ErrHistoryNotEmpty を返す。
// 形式が違えば ErrHistoryFormat を、履歴が今のテキストに当てはまらなければ ErrHistoryMismatch を返す。
// どちらも、履歴の編集を今のテキストの写しで取り消し、やり直して確かめるので、壊れた履歴を読み戻した後のアンドゥで
// 履歴とテキストの食い違い（mustApplyAll の panic）は起きない。
// 誤りを返したときは、バッファを変えない。
//
// 読み戻した後は、今の内容を保存済みとして記録する（MarkSaved）。バージョン番号は変えない。
// 読み戻す前のバッファが行の無いバッファ（空のファイルを読んだもの）なら、読み戻した後の今の位置でも行の無いバッファのままにする。
//
// req: FR-165
func (b *Buffer) RestoreHistory(data []byte) error {
	if len(b.undo) > 0 || len(b.redo) > 0 {
		return ErrHistoryNotEmpty
	}
	r := historyReader{buf: data}
	if v := r.num(); r.err == nil && v != historyFormat {
		return fmt.Errorf("%w: version %d", ErrHistoryFormat, v)
	}
	nextID := r.num()
	noLines := map[uint64]bool{}
	for n := r.count(1); n > 0 && r.err == nil; n-- {
		noLines[r.num()] = true
	}
	var stacks [2][]step
	for i := range stacks {
		n := r.count(minStepBytes)
		stacks[i] = make([]step, 0, n)
		for ; n > 0 && r.err == nil; n-- {
			stacks[i] = append(stacks[i], r.step())
		}
	}
	if r.err == nil && len(r.buf) > 0 {
		r.err = errors.New("trailing bytes")
	}
	if r.err != nil {
		return fmt.Errorf("%w: %w", ErrHistoryFormat, r.err)
	}
	undo, redo := stacks[0], stacks[1]
	if err := checkIDs(nextID, undo, redo); err != nil {
		return fmt.Errorf("%w: %w", ErrHistoryFormat, err)
	}
	if err := b.checkHistory(undo, redo); err != nil {
		return fmt.Errorf("%w: %w", ErrHistoryMismatch, err)
	}
	wasNoLines := b.NoLines()
	b.closeUndoGroup()
	b.undo, b.redo, b.nextID, b.noLinesAt = undo, redo, nextID, noLines
	if wasNoLines {
		b.noLinesAt[b.stateID()] = true
	}
	b.MarkSaved()
	return nil
}

// checkIDs は、単位の識別子が 1 以上 nextID 以下で、互いに違うことを確かめる。
// 識別子は保存済みの記録（Modified）と行の無いバッファの記録の位置に使うので、重なると記録が別の位置に付く。
func checkIDs(nextID uint64, stacks ...[]step) error {
	seen := map[uint64]bool{}
	for _, steps := range stacks {
		for _, s := range steps {
			if s.id == 0 || s.id > nextID || seen[s.id] {
				return fmt.Errorf("bad step id %d (next %d)", s.id, nextID)
			}
			seen[s.id] = true
		}
	}
	return nil
}

// checkHistory は、undo と redo の履歴が今のテキストに当てはまるかを、行の写しの上で確かめる。
// アンドゥの単位を末尾から順に取り消して先頭まで戻り、やり直して今のテキストに戻ること、
// リドゥの単位を順にやり直して、取り消して今のテキストに戻ることを見る。
func (b *Buffer) checkHistory(undo, redo []step) error {
	tmp := &Buffer{lines: slices.Clone(b.lines)}
	for i := len(undo) - 1; i >= 0; i-- {
		if err := tmp.applyChecked(reversed(undo[i].inverse)); err != nil {
			return fmt.Errorf("undo %d: %w", i, err)
		}
	}
	for i, s := range undo {
		if err := tmp.applyChecked(s.edits); err != nil {
			return fmt.Errorf("redo of undo %d: %w", i, err)
		}
	}
	if !slices.Equal(tmp.lines, b.lines) {
		return errors.New("undo history does not lead back to the text")
	}
	for i := len(redo) - 1; i >= 0; i-- {
		if err := tmp.applyChecked(redo[i].edits); err != nil {
			return fmt.Errorf("redo %d: %w", i, err)
		}
	}
	for i, s := range redo {
		if err := tmp.applyChecked(reversed(s.inverse)); err != nil {
			return fmt.Errorf("undo of redo %d: %w", i, err)
		}
	}
	if !slices.Equal(tmp.lines, b.lines) {
		return errors.New("redo history does not lead back to the text")
	}
	return nil
}

// applyChecked は、edits を順に適用する。位置が正しくない編集があれば、そこで止めて誤りを返す（checkHistory の写しの上で使う）。
func (b *Buffer) applyChecked(edits []Edit) error {
	for _, e := range edits {
		if _, err := b.apply(e); err != nil {
			return err
		}
	}
	return nil
}

// reversed は、edits を逆の順に並べた写しを返す。
func reversed(edits []Edit) []Edit {
	out := slices.Clone(edits)
	slices.Reverse(out)
	return out
}

// 単位の印のビット。
const (
	stepHasCursor = 1 << iota
	stepEOLSet
	stepEOLBefore
	stepEOLAfter
)

// minStepBytes は、1 つの単位が占める最小のバイト数である（識別子、印、編集の数 2 つ）。
// 読む前に、残りのバイト数で一覧の長さを確かめ、壊れた長さで大きな領域を確保しないために使う。
const minStepBytes = 4

// minEditBytes は、1 つの編集が占める最小のバイト数である（範囲の 4 つの数とテキストの長さ）。
const minEditBytes = 5

// historyWriter は、履歴のバイト列を書く。数は符号なしの可変長の整数（encoding/binary の Uvarint）で、
// テキストは長さとバイトの並びで書く。
type historyWriter struct {
	buf []byte
}

func (w *historyWriter) num(n uint64) {
	w.buf = binary.AppendUvarint(w.buf, n)
}

func (w *historyWriter) step(s step) {
	w.num(s.id)
	var flags uint64
	if s.hasCursor {
		flags |= stepHasCursor
	}
	if s.eolSet {
		flags |= stepEOLSet
	}
	if s.eolBefore {
		flags |= stepEOLBefore
	}
	if s.eolAfter {
		flags |= stepEOLAfter
	}
	w.num(flags)
	if s.hasCursor {
		w.pos(s.cursor)
	}
	w.edits(s.edits)
	w.edits(s.inverse)
}

func (w *historyWriter) edits(edits []Edit) {
	w.num(uint64(len(edits)))
	for _, e := range edits {
		w.pos(e.Range.Start)
		w.pos(e.Range.End)
		w.num(uint64(len(e.Text)))
		w.buf = append(w.buf, e.Text...)
	}
}

// pos は位置を書く。負の値は 0 にする（履歴の編集の位置は checkPosition が確かめたもので負にならない。
// カーソルの記録は呼び出し側の値なので、念のため丸める）。
func (w *historyWriter) pos(p Position) {
	w.num(uint64(max(p.Line, 0)))
	w.num(uint64(max(p.Byte, 0)))
}

// historyReader は、historyWriter が書いたバイト列を読む。最初の誤りを err に残し、その後の読み出しはゼロ値を返す。
type historyReader struct {
	buf []byte
	err error
}

func (r *historyReader) num() uint64 {
	if r.err != nil {
		return 0
	}
	n, k := binary.Uvarint(r.buf)
	if k <= 0 {
		r.err = errors.New("bad number")
		return 0
	}
	r.buf = r.buf[k:]
	return n
}

// count は、一覧の長さを読む。1 つの要素が少なくとも minBytes バイトを占めるとして、残りのバイト数に収まらない長さは誤りにする。
func (r *historyReader) count(minBytes int) int {
	n := r.num()
	if r.err == nil && n > uint64(len(r.buf)/minBytes) { //nolint:gosec // G115: スライスの長さは負にならない
		r.err = fmt.Errorf("bad length %d", n)
	}
	if r.err != nil {
		return 0
	}
	return int(n) //nolint:gosec // G115: 残りのバイト数以下である
}

// index は、位置の数を読む。int に収まらない数は誤りにする。
func (r *historyReader) index() int {
	n := r.num()
	if r.err == nil && n > math.MaxInt {
		r.err = fmt.Errorf("bad position %d", n)
	}
	if r.err != nil {
		return 0
	}
	return int(n) //nolint:gosec // G115: math.MaxInt 以下である
}

func (r *historyReader) step() step {
	s := step{id: r.num()}
	flags := r.num()
	if flags&^(stepHasCursor|stepEOLSet|stepEOLBefore|stepEOLAfter) != 0 && r.err == nil {
		r.err = fmt.Errorf("bad flags %#x", flags)
	}
	s.hasCursor = flags&stepHasCursor != 0
	s.eolSet = flags&stepEOLSet != 0
	s.eolBefore = flags&stepEOLBefore != 0
	s.eolAfter = flags&stepEOLAfter != 0
	if s.hasCursor {
		s.cursor = r.pos()
	}
	s.edits = r.edits()
	s.inverse = r.edits()
	if r.err == nil && len(s.edits) != len(s.inverse) {
		r.err = errors.New("edits and inverse differ in length")
	}
	return s
}

func (r *historyReader) edits() []Edit {
	n := r.count(minEditBytes)
	edits := make([]Edit, 0, n)
	for ; n > 0 && r.err == nil; n-- {
		var e Edit
		e.Range.Start = r.pos()
		e.Range.End = r.pos()
		size := r.num()
		if r.err == nil && size > uint64(len(r.buf)) {
			r.err = fmt.Errorf("bad text length %d", size)
		}
		if r.err != nil {
			return nil
		}
		// string への変換はバイトを写すので、テキストは編集ごとに別に確保した文字列になり、data の全体を保持しない（基本設計 4.3）。
		e.Text = string(r.buf[:size])
		r.buf = r.buf[size:]
		edits = append(edits, e)
	}
	return edits
}

func (r *historyReader) pos() Position {
	return Position{Line: r.index(), Byte: r.index()}
}
