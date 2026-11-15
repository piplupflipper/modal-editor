// Package buffer は、テキストの保持、編集の適用、バージョン番号、アンドゥの履歴を受け持つ（基本設計 2 章、4 章）。
//
// 基本設計 2 章の土台の層にあり、このリポジトリの他のパッケージを import しない。
// カーソル、モード、画面は持たない。
//
// Buffer は並行に呼ばれる前提を置かない。
// 編集の状態はイベントループの goroutine 1 つだけが触る（基本設計 2.1）ので、排他制御をしない。
//
// テキストは UTF-8 で、改行は LF を前提にする。
// テキストを "\n" で区切ったものを行とする。
// このため "a\n" は "a" と "" の 2 行になり、末尾の改行の有無はテキストと行の数にそのまま現れる。
// 読み込んだテキストはそのまま書き出すので、末尾の改行の有無は保たれる。
// CR（"\r"）は特別に扱わず、行の中の 1 バイトとして保持する。
package buffer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

// Buffer は、1 つのテキストと、そのバージョン番号とアンドゥの履歴を保持する。
// ゼロ値は使わず、New か Read で作る。
type Buffer struct {
	// lines は少なくとも 1 要素を持つ。
	// テキストの保持の方法は外に見せない（基本設計 4.3）ので、構造を取り替えても公開の操作は変えない。
	lines   []string
	version uint64
	undo    []step
	redo    []step
}

// step は、アンドゥとリドゥの 1 単位で、1 つのトランザクションに当たる。
// inverse を順に適用すると、edits を適用する前のテキストに戻る。
type step struct {
	edits   []Edit
	inverse []Edit
}

// New は、text を内容とするバッファを、バージョン番号 0 で作る。
func New(text string) *Buffer {
	return &Buffer{lines: strings.Split(text, "\n")}
}

// Read は、r の内容を全て読み、それを内容とするバッファを作る。
func Read(r io.Reader) (*Buffer, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("buffer: read: %w", err)
	}
	return New(string(data)), nil
}

// ReadFile は、path のファイルを読み、それを内容とするバッファを作る。
func ReadFile(path string) (*Buffer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("buffer: %w", err)
	}
	defer f.Close() //nolint:errcheck // 読み込みだけなので、閉じるときの失敗は内容に影響しない
	return Read(f)
}

// WriteTo は、バッファのテキストを w へ書き出す。
// 書き出しはバージョン番号とアンドゥの履歴を変えない。
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	n, err := io.WriteString(w, b.Text())
	if err != nil {
		return int64(n), fmt.Errorf("buffer: write: %w", err)
	}
	return int64(n), nil
}

// WriteFile は、バッファのテキストを path のファイルへ書き出す。
// ファイルが既にあれば、その許可属性を保ったまま中身を置き換える。
// 無ければ、許可属性 0644（umask を適用した後の値）で作る。
func (b *Buffer) WriteFile(path string) error {
	perm := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), perm); err != nil {
		return fmt.Errorf("buffer: %w", err)
	}
	return nil
}

// Version は、現在のバージョン番号を返す。
// 番号は、編集の適用、アンドゥ、リドゥのたびに 1 増え、減ることは無い。
func (b *Buffer) Version() uint64 {
	return b.version
}

// LineCount は、行の数を返す。空のバッファでも 1 以上である。
func (b *Buffer) LineCount() int {
	return len(b.lines)
}

// Line は、i 番目（0 始まり）の行を、改行を含まずに返す。
// i が 0 以上 LineCount() 未満でなければ panic する。
func (b *Buffer) Line(i int) string {
	return b.lines[i]
}

// Text は、バッファのテキスト全体を返す。
func (b *Buffer) Text() string {
	return strings.Join(b.lines, "\n")
}

// Slice は、範囲 r のテキストを返す（基本設計 4.3 の「範囲を読む」）。
// 範囲がバッファの外にあるか、UTF-8 の文字の途中を指す場合は ErrInvalidPosition を、
// 終わりが始まりより前にある場合は ErrInvalidRange を返す。
func (b *Buffer) Slice(r Range) (string, error) {
	if err := b.checkRange(r); err != nil {
		return "", err
	}
	return b.slice(r), nil
}

// Apply は、トランザクションを適用し、適用した変更を返す。
//
// tx.BaseVersion が現在のバージョン番号と一致しなければ、何も変えずに ErrVersionMismatch を返す（FR-15）。
// 編集の位置が正しくない場合も、何も変えずに ErrInvalidPosition か ErrInvalidRange を返す。
// 途中の編集で失敗しても、それより前の編集は取り消され、トランザクションは全体として適用されないか、全て適用されるかのどちらかになる。
//
// 適用に成功すると、バージョン番号を 1 増やし、トランザクションをアンドゥの 1 単位として記録し、リドゥの履歴を捨てる。
// Edits が空のトランザクションは、バージョン番号が一致していれば何もせず、Before と After が等しい Change を返す。
//
// req: FR-07, FR-15, FR-16
func (b *Buffer) Apply(tx Transaction) (Change, error) {
	if tx.BaseVersion != b.version {
		return Change{}, fmt.Errorf("%w: base %d, current %d", ErrVersionMismatch, tx.BaseVersion, b.version)
	}
	if len(tx.Edits) == 0 {
		return Change{Before: b.version, After: b.version}, nil
	}
	edits := slices.Clone(tx.Edits)
	inverse, err := b.applyAll(edits)
	if err != nil {
		return Change{}, err
	}
	b.undo = append(b.undo, step{edits: edits, inverse: inverse})
	b.redo = nil
	return b.commit(edits), nil
}

// CanUndo は、アンドゥできるトランザクションが残っているかどうかを返す。
func (b *Buffer) CanUndo() bool {
	return len(b.undo) > 0
}

// CanRedo は、リドゥできるトランザクションが残っているかどうかを返す。
func (b *Buffer) CanRedo() bool {
	return len(b.redo) > 0
}

// Undo は、最後に適用したトランザクションを 1 つ取り消し、その変更を返す（FR-07）。
// 取り消しも変更の 1 つとして扱い、バージョン番号を 1 増やす。
// 取り消せるものが無ければ、何もせずに false を返す。
//
// req: FR-07, FR-16
func (b *Buffer) Undo() (Change, bool) {
	if len(b.undo) == 0 {
		return Change{}, false
	}
	s := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.mustApplyAll(s.inverse)
	b.redo = append(b.redo, s)
	return b.commit(s.inverse), true
}

// Redo は、最後に取り消したトランザクションを 1 つやり直し、その変更を返す（FR-07）。
// やり直しも変更の 1 つとして扱い、バージョン番号を 1 増やす。
// やり直せるものが無ければ、何もせずに false を返す。
//
// req: FR-07, FR-16
func (b *Buffer) Redo() (Change, bool) {
	if len(b.redo) == 0 {
		return Change{}, false
	}
	s := b.redo[len(b.redo)-1]
	b.redo = b.redo[:len(b.redo)-1]
	b.mustApplyAll(s.edits)
	b.undo = append(b.undo, s)
	return b.commit(s.edits), true
}

// commit は、バージョン番号を 1 増やし、edits を適用した変更の記録を返す。
// 呼び出し側が記録を書き換えても履歴に影響しないように、edits を複製する。
func (b *Buffer) commit(edits []Edit) Change {
	before := b.version
	b.version++
	return Change{Before: before, After: b.version, Edits: slices.Clone(edits)}
}

// applyAll は、edits を順に適用し、適用前に戻すための逆の編集の列を返す。
// 途中で失敗した場合は、それまでの編集を取り消してからエラーを返す。
func (b *Buffer) applyAll(edits []Edit) ([]Edit, error) {
	inverse := make([]Edit, 0, len(edits))
	for i, e := range edits {
		inv, err := b.apply(e)
		if err != nil {
			slices.Reverse(inverse)
			b.mustApplyAll(inverse)
			return nil, fmt.Errorf("edit %d: %w", i, err)
		}
		inverse = append(inverse, inv)
	}
	slices.Reverse(inverse)
	return inverse, nil
}

// mustApplyAll は、適用できることが分かっている編集の列（履歴に記録した列）を順に適用する。
// 失敗した場合は履歴とテキストが食い違っているので、panic する。
func (b *Buffer) mustApplyAll(edits []Edit) {
	for _, e := range edits {
		if _, err := b.apply(e); err != nil {
			panic(fmt.Sprintf("buffer: history is inconsistent with text: %v", err))
		}
	}
}

// apply は、1 つの編集を適用し、それを取り消す逆の編集を返す。
// 位置が正しくなければ、何も変えずにエラーを返す。
func (b *Buffer) apply(e Edit) (Edit, error) {
	if err := b.checkRange(e.Range); err != nil {
		return Edit{}, err
	}
	start, end := e.Range.Start, e.Range.End
	removed := b.slice(e.Range)

	joined := b.lines[start.Line][:start.Byte] + e.Text + b.lines[end.Line][end.Byte:]
	b.lines = slices.Replace(b.lines, start.Line, end.Line+1, strings.Split(joined, "\n")...)

	return Edit{Range: Range{Start: start, End: endOf(start, e.Text)}, Text: removed}, nil
}

// endOf は、start に text を挿入したとき、挿入したテキストの直後に当たる位置を返す。
func endOf(start Position, text string) Position {
	n := strings.Count(text, "\n")
	if n == 0 {
		return Position{Line: start.Line, Byte: start.Byte + len(text)}
	}
	return Position{Line: start.Line + n, Byte: len(text) - strings.LastIndexByte(text, '\n') - 1}
}

// slice は、正しいことを確かめた範囲 r のテキストを返す。
func (b *Buffer) slice(r Range) string {
	s, e := r.Start, r.End
	if s.Line == e.Line {
		return b.lines[s.Line][s.Byte:e.Byte]
	}
	var sb strings.Builder
	sb.WriteString(b.lines[s.Line][s.Byte:])
	for _, l := range b.lines[s.Line+1 : e.Line] {
		sb.WriteByte('\n')
		sb.WriteString(l)
	}
	sb.WriteByte('\n')
	sb.WriteString(b.lines[e.Line][:e.Byte])
	return sb.String()
}

// checkRange は、範囲の両端がバッファの中の文字の境界を指し、終わりが始まりより前に無いことを確かめる。
func (b *Buffer) checkRange(r Range) error {
	if err := b.checkPosition(r.Start); err != nil {
		return err
	}
	if err := b.checkPosition(r.End); err != nil {
		return err
	}
	if r.End.Less(r.Start) {
		return fmt.Errorf("%w: end %v is before start %v", ErrInvalidRange, r.End, r.Start)
	}
	return nil
}

// checkPosition は、p がバッファの中の文字の境界（行末を含む）を指すことを確かめる。
func (b *Buffer) checkPosition(p Position) error {
	if p.Line < 0 || p.Line >= len(b.lines) {
		return fmt.Errorf("%w: %v: line out of range [0, %d)", ErrInvalidPosition, p, len(b.lines))
	}
	line := b.lines[p.Line]
	if p.Byte < 0 || p.Byte > len(line) {
		return fmt.Errorf("%w: %v: byte out of range [0, %d]", ErrInvalidPosition, p, len(line))
	}
	if p.Byte < len(line) && !utf8.RuneStart(line[p.Byte]) {
		return fmt.Errorf("%w: %v: not at a UTF-8 character boundary", ErrInvalidPosition, p)
	}
	return nil
}
