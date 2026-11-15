// Package buffer は、テキストの保持、編集の適用、バージョン番号、アンドゥの履歴を受け持つ（基本設計 2 章、4 章）。
//
// 基本設計 2 章の土台の層にあり、このリポジトリの他のパッケージを import しない。
// カーソル、モード、画面は持たない。
//
// Buffer は並行に呼ばれる前提を置かない。
// 編集の状態はイベントループの goroutine 1 つだけが触る（基本設計 2.1）ので、排他制御をしない。
//
// # 行の数え方
//
// テキストは UTF-8 で、改行は LF を前提にする。
// 行の数え方は Vim に合わせ、改行は行の終わりを表すものとして数える。
// このため "a\nb\n" と "a\nb" はどちらも "a" と "b" の 2 行で、違いは末尾の改行の有無（HasTrailingNewline）だけである。
// 空のファイル "" と "\n" は、どちらも空の行 1 つである。
// 末尾の改行の有無は読み込み時に記録し、書き出し時（WriteTo、WriteFile）に復元する。
//
// 位置と編集（Position、Edit）は、行を "\n" でつないだテキスト（Text が返すもの）に対して数える。
// 末尾の改行はこのテキストに含まれず、編集で消したり足したりすることはできない。
// CR（"\r"）は特別に扱わず、行の中の 1 バイトとして保持する。
package buffer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// Buffer は、1 つのテキストと、そのバージョン番号とアンドゥの履歴を保持する。
// ゼロ値は使わず、New か Read で作る。
type Buffer struct {
	// lines は少なくとも 1 要素を持つ。
	// テキストの保持の方法は外に見せない（基本設計 4.3）ので、構造を取り替えても公開の操作は変えない。
	lines []string
	// eol は、書き出すときに最後の行の後へ改行を付けるかどうかである。
	eol     bool
	version uint64
	undo    []step
	redo    []step

	// groupDepth は、BeginUndoGroup の入れ子の深さである。
	groupDepth int
	// groupStarted は、開いているグループの最初の Apply が済み、undo の末尾がそのグループの単位であることを表す。
	groupStarted bool

	// nextID は、次に作る、またはまとめ直したアンドゥの単位に付ける識別子である。
	nextID uint64
	// savedID は、MarkSaved を呼んだ時点の stateID である。
	savedID uint64
}

// step は、アンドゥとリドゥの 1 単位である。
// inverse を順に適用すると、edits を適用する前のテキストに戻る。
// id は、この単位を適用し終えた状態を見分けるための、履歴の中で一意な番号（1 以上）である。
type step struct {
	id      uint64
	edits   []Edit
	inverse []Edit
}

// New は、text を内容とするバッファを、バージョン番号 0 で作る。
// text の末尾に改行があれば、それを末尾の改行として記録し、行には含めない。
// 作った直後のバッファは、保存済み（Modified が false）として扱う。
func New(text string) *Buffer {
	eol := strings.HasSuffix(text, "\n")
	if eol {
		text = text[:len(text)-1]
	}
	return &Buffer{lines: strings.Split(text, "\n"), eol: eol}
}

// Read は、r の内容を全て読み、それを内容とするバッファを New と同じ規則で作る。
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

// WriteTo は、バッファの内容を、末尾の改行の有無を復元して w へ書き出す。
// 書き出しはバージョン番号、アンドゥの履歴、保存済みの記録を変えない。
func (b *Buffer) WriteTo(w io.Writer) (int64, error) {
	data := b.Text()
	if b.eol {
		data += "\n"
	}
	n, err := io.WriteString(w, data)
	if err != nil {
		return int64(n), fmt.Errorf("buffer: write: %w", err)
	}
	return int64(n), nil
}

// WriteFile は、バッファの内容を path のファイルへ書き出す。
//
// ファイルが既にあれば、同じディレクトリの一時ファイルへ書いてから rename で置き換えるので、途中で失敗しても元のファイルは壊れない。
// 一時ファイルには元のファイルの許可属性を付ける。
// path がシンボリックリンクなら、リンクの指す先のファイルを置き換える。
// 置き換えたファイルは新しいファイルになるので、ハードリンクは切れ、所有者は書き出したプロセスの利用者になる。
//
// ファイルが無ければ、許可属性 0644 に umask を適用して新しく作り、直接書き込む。守る元の内容が無いので、rename は使わない。
//
// WriteFile は保存済みの記録を変えない。編集中のファイルを保存したときは、呼び出し側が MarkSaved を呼ぶ。
func (b *Buffer) WriteFile(path string) error {
	target, perm, exists, err := writeTarget(path)
	if err != nil {
		return err
	}
	if !exists {
		return b.createFile(path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp*")
	if err != nil {
		return fmt.Errorf("buffer: %w", err)
	}
	tmpName := tmp.Name()
	if err := writeAndClose(tmp, b, perm); err != nil {
		_ = os.Remove(tmpName) // 一時ファイルの後始末に失敗しても、元のエラーを返すほうが大事である
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName) // 同上
		return fmt.Errorf("buffer: %w", err)
	}
	return nil
}

// writeTarget は、WriteFile が置き換えるファイルのパス（シンボリックリンクを解決したもの）と、その許可属性を返す。
// ファイルが無ければ exists を false にして返す。それ以外の Stat の失敗はエラーとして返す。
func writeTarget(path string) (target string, perm fs.FileMode, exists bool, err error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		target, err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", 0, false, fmt.Errorf("buffer: %w", err)
		}
		return target, info.Mode().Perm(), true, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", 0, false, nil
	default:
		return "", 0, false, fmt.Errorf("buffer: %w", err)
	}
}

// createFile は、path に新しいファイルを作って内容を書く。既にあれば失敗する。
func (b *Buffer) createFile(path string) error {
	//nolint:gosec // G302: 利用者が編集するテキストファイルなので、一般的なエディタと同じく 0644 に umask を適用した許可属性で作る
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("buffer: %w", err)
	}
	return writeAndClose(f, b, 0)
}

// writeAndClose は、f にバッファの内容を書き、perm が 0 でなければその許可属性を付け、ディスクへ同期してから閉じる。
func writeAndClose(f *os.File, b *Buffer, perm fs.FileMode) error {
	_, err := b.WriteTo(f)
	if err == nil && perm != 0 {
		err = f.Chmod(perm)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("buffer: %w", err)
	}
	return nil
}

// Version は、現在のバージョン番号を返す。
// 番号は、編集の適用、アンドゥ、リドゥのたびに 1 増え、減ることは無い。
func (b *Buffer) Version() uint64 {
	return b.version
}

// LineCount は、行の数を返す。末尾の改行は行として数えない。空のバッファでも 1 以上である。
func (b *Buffer) LineCount() int {
	return len(b.lines)
}

// Line は、i 番目（0 始まり）の行を、改行を含まずに返す。
// i が 0 以上 LineCount() 未満でなければ panic する。
func (b *Buffer) Line(i int) string {
	return b.lines[i]
}

// HasTrailingNewline は、最後の行の後に改行があるかどうか（書き出すときに改行を付けるかどうか）を返す。
func (b *Buffer) HasTrailingNewline() bool {
	return b.eol
}

// Text は、行を "\n" でつないだテキストを返す。末尾の改行は含まない。
// 位置と編集はこのテキストに対して数える。ファイルとしての内容は WriteTo で得る。
func (b *Buffer) Text() string {
	return strings.Join(b.lines, "\n")
}

// Slice は、範囲 r のテキストを返す（基本設計 4.3 の「範囲を読む」）。
// 範囲がバッファの外にあるか、文字の途中を指す場合は ErrInvalidPosition を、
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
// 適用に成功すると、バージョン番号を 1 増やし、リドゥの履歴を捨てる。
// トランザクションはアンドゥの 1 単位として記録する。
// ただし BeginUndoGroup でグループを開いている間は、グループの中の 2 回目以降の Apply を、最初の Apply の単位にまとめる。
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
	b.record(edits, inverse)
	b.redo = nil
	return b.commit(edits), nil
}

// record は、適用した編集をアンドゥの履歴に記録する。
// グループの中の 2 回目以降なら末尾の単位にまとめ、まとめた単位には新しい id を付ける。
// id を付け直すのは、まとめる前の状態で MarkSaved した場合に、まとめた後を変更ありと判定するためである。
func (b *Buffer) record(edits, inverse []Edit) {
	b.nextID++
	if b.groupDepth > 0 && b.groupStarted {
		last := &b.undo[len(b.undo)-1]
		last.id = b.nextID
		last.edits = append(last.edits, edits...)
		last.inverse = slices.Concat(inverse, last.inverse)
		return
	}
	b.undo = append(b.undo, step{id: b.nextID, edits: edits, inverse: inverse})
	if b.groupDepth > 0 {
		b.groupStarted = true
	}
}

// BeginUndoGroup は、アンドゥのグループを開く。
// EndUndoGroup で閉じるまでの Apply は、アンドゥとリドゥで 1 つの単位として扱う（FR-07）。
// インサートモードで 1 文字ごとに Apply し、モードを抜けるまでを 1 回のアンドゥで戻す、といった使い方を想定する。
//
// グループは入れ子にでき、最も外側の EndUndoGroup で閉じる。
// グループの中でも、バージョン番号と Change は Apply ごとに進み、返る。
// グループを開いている間に Undo か Redo を呼ぶと、グループは閉じる。
//
// req: FR-07
func (b *Buffer) BeginUndoGroup() {
	b.groupDepth++
}

// EndUndoGroup は、BeginUndoGroup で開いたグループを 1 段閉じる。
// 開いているグループが無ければ何もしない。
//
// req: FR-07
func (b *Buffer) EndUndoGroup() {
	if b.groupDepth == 0 {
		return
	}
	b.groupDepth--
	if b.groupDepth == 0 {
		b.groupStarted = false
	}
}

// closeUndoGroup は、開いているグループを全て閉じる。
func (b *Buffer) closeUndoGroup() {
	b.groupDepth = 0
	b.groupStarted = false
}

// MarkSaved は、現在の内容を保存した内容として記録する。
// 以後、編集、アンドゥ、リドゥでこの内容に戻ったときは、Modified が false を返す。
func (b *Buffer) MarkSaved() {
	b.savedID = b.stateID()
}

// Modified は、現在の内容が、最後に MarkSaved した時点（呼んでいなければ作った時点）の内容から変わっているかどうかを返す。
//
// 判定はアンドゥの履歴の位置で行う。
// 保存した後に編集し、アンドゥで保存した時点まで戻れば false になる。
// 保存した時点より前へアンドゥしてから別の編集をすると、保存した時点へは戻れなくなるので、次に MarkSaved するまで true になる。
// 別々の編集の結果がたまたま保存した内容と同じテキストになっても、true を返す。
func (b *Buffer) Modified() bool {
	return b.stateID() != b.savedID
}

// stateID は、アンドゥの履歴の現在の位置を表す番号を返す。履歴が空なら 0 である。
func (b *Buffer) stateID() uint64 {
	if len(b.undo) == 0 {
		return 0
	}
	return b.undo[len(b.undo)-1].id
}

// CanUndo は、アンドゥできる単位が残っているかどうかを返す。
func (b *Buffer) CanUndo() bool {
	return len(b.undo) > 0
}

// CanRedo は、リドゥできる単位が残っているかどうかを返す。
func (b *Buffer) CanRedo() bool {
	return len(b.redo) > 0
}

// Undo は、アンドゥの単位を 1 つ取り消し、その変更を返す（FR-07）。
// 取り消しも変更の 1 つとして扱い、バージョン番号を 1 増やす。
// 取り消せるものが無ければ、何もせずに false を返す。
// 開いているアンドゥのグループは閉じる。
//
// req: FR-07, FR-16
func (b *Buffer) Undo() (Change, bool) {
	b.closeUndoGroup()
	if len(b.undo) == 0 {
		return Change{}, false
	}
	s := b.undo[len(b.undo)-1]
	b.undo = b.undo[:len(b.undo)-1]
	b.mustApplyAll(s.inverse)
	b.redo = append(b.redo, s)
	return b.commit(s.inverse), true
}

// Redo は、最後に取り消した単位を 1 つやり直し、その変更を返す（FR-07）。
// やり直しも変更の 1 つとして扱い、バージョン番号を 1 増やす。
// やり直せるものが無ければ、何もせずに false を返す。
// 開いているアンドゥのグループは閉じる。
//
// req: FR-07, FR-16
func (b *Buffer) Redo() (Change, bool) {
	b.closeUndoGroup()
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
// 文字の境界の定義は Position の doc コメントにある。
func (b *Buffer) checkPosition(p Position) error {
	if p.Line < 0 || p.Line >= len(b.lines) {
		return fmt.Errorf("%w: %v: line out of range [0, %d)", ErrInvalidPosition, p, len(b.lines))
	}
	line := b.lines[p.Line]
	if p.Byte < 0 || p.Byte > len(line) {
		return fmt.Errorf("%w: %v: byte out of range [0, %d]", ErrInvalidPosition, p, len(line))
	}
	if insideRune(line, p.Byte) {
		return fmt.Errorf("%w: %v: inside a UTF-8 character", ErrInvalidPosition, p)
	}
	return nil
}

// insideRune は、i が line の中の正しい UTF-8 の多バイト文字の途中を指すかどうかを返す。
//
// 正しい多バイト文字の 2 バイト目以降は継続バイトなので、別の正しい文字の先頭にはならない。
// このため、i の前 utf8.UTFMax-1 バイト以内から始まる正しい多バイト文字が i をまたぐかだけを見れば、
// 行の先頭から utf8.DecodeRuneInString で進めたときの刻みと一致する。
func insideRune(line string, i int) bool {
	for j := max(0, i-(utf8.UTFMax-1)); j < i; j++ {
		_, size := utf8.DecodeRuneInString(line[j:])
		// 不正なバイトは size が 1 になる。正しい文字なら size は 1 以上で、U+FFFD そのものも 3 になる。
		if size > 1 && j+size > i {
			return true
		}
	}
	return false
}
