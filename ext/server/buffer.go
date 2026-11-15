package server

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
)

// ErrInvalidPosition は、位置がバッファの外にあるか、UTF-8 の文字の途中を指すことを表す。
var ErrInvalidPosition = errors.New("server: invalid position")

// ErrInvalidRange は、範囲の終わりが始まりより前にあることを表す。
var ErrInvalidRange = errors.New("server: invalid range")

// Buffer は、本体のバッファの複製である。
// buffer/open で受け取った全文に、buffer/change の差分を順に適用して、本体と同じ内容を保つ（FR-16）。
//
// 位置の数え方は本体と同じである（ext.Position、基本設計 4.1）。
// テキストを "\n" で区切ったものを行とし、"a\nb" は 2 行、空のテキストは空の行 1 つである。
// 本体は末尾の改行を含めずに全文を送る（ext.BufferOpenParams）。
//
// Server は、ハンドラーとフックを、メッセージを処理する 1 つの goroutine から呼ぶ。
// ハンドラーに渡した Buffer は、そのハンドラーが戻るまでの間だけ、渡した時点の内容を保つ。
// 戻った後は、次の buffer/change で内容が変わる。
// 戻った後も内容を読みたいとき（別の goroutine で解析するときなど）は、Clone で複製を取る。
// Buffer のメソッドは、同じ Buffer の更新と並行に呼んではならない。
type Buffer struct {
	id       ext.BufferID
	path     string
	fileType string
	version  ext.Version
	lines    []string
	// finalNewline は、本体がファイルへ書き出したときに最後の行の後に改行が付くかどうかである。
	finalNewline bool
}

// newBuffer は、buffer/open の params から複製を作る。
func newBuffer(p *ext.BufferOpenParams) *Buffer {
	return &Buffer{
		id:       p.Buffer,
		path:     p.Path,
		fileType: p.FileType,
		version:  p.Version,
		lines:    strings.Split(p.Text, "\n"),

		finalNewline: p.HasFinalNewline(),
	}
}

// ID は、バッファの番号を返す。
func (b *Buffer) ID() ext.BufferID { return b.id }

// Path は、バッファに対応するファイルのパスを返す。ファイルに対応しないバッファでは空である。
func (b *Buffer) Path() string { return b.path }

// FileType は、ファイルの種類を返す。決まらなければ空である。
func (b *Buffer) FileType() string { return b.fileType }

// Version は、複製の内容に対応するバージョン番号を返す。
func (b *Buffer) Version() ext.Version { return b.version }

// FinalNewline は、本体がこのバッファをファイルへ書き出したときに、最後の行の後に改行が付くかどうかを返す。
// Text は末尾の改行を含まないので、ファイルとしての内容は、これが true なら Text に "\n" を足したものになる。
// 最後に受け取った buffer/open の finalNewline の値で、省かれていれば true である（ext.BufferOpenParams）。
// buffer/change では変わらない。
func (b *Buffer) FinalNewline() bool { return b.finalNewline }

// LineCount は、行の数を返す。空のバッファでも 1 以上である。
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line は、i 番目（0 始まり）の行を、改行を含まずに返す。
// i が 0 以上 LineCount() 未満でなければ panic する。
func (b *Buffer) Line(i int) string { return b.lines[i] }

// Text は、行を "\n" でつないだ全文を返す。
func (b *Buffer) Text() string { return strings.Join(b.lines, "\n") }

// Slice は、範囲 r のテキストを返す。
// 範囲がバッファの外にあるか、文字の途中を指す場合は ErrInvalidPosition を、
// 終わりが始まりより前にある場合は ErrInvalidRange を包んだ誤りを返す。
func (b *Buffer) Slice(r ext.Range) (string, error) {
	if err := b.checkRange(r); err != nil {
		return "", err
	}
	return b.slice(r), nil
}

// Clone は、内容とバージョン番号が同じ、独立した複製を返す。
// 返した Buffer は、元の Buffer の更新の影響を受けないので、別の goroutine に渡してよい。
func (b *Buffer) Clone() *Buffer {
	c := *b
	c.lines = slices.Clone(b.lines)
	return &c
}

// applyChange は、buffer/change の Edits を逐次の数え方で順に適用し、バージョン番号を ToVersion にする。
// 呼ぶ前に FromVersion が一致することを確かめておく。
//
// 編集の位置が正しくない場合は誤りを返す。そのときの内容は途中までの編集を適用したものなので、呼び出し側は全文の送り直しを求める。
//
// req: FR-16
func (b *Buffer) applyChange(p *ext.BufferChangeParams) error {
	for i, e := range p.Edits {
		if err := b.checkRange(e.Range); err != nil {
			return fmt.Errorf("edit %d: %w", i, err)
		}
		start, end := e.Range.Start, e.Range.End
		joined := b.lines[start.Line][:start.Byte] + e.Text + b.lines[end.Line][end.Byte:]
		b.lines = slices.Replace(b.lines, start.Line, end.Line+1, strings.Split(joined, "\n")...)
	}
	b.version = p.ToVersion
	return nil
}

// slice は、正しいことを確かめた範囲 r のテキストを返す。
func (b *Buffer) slice(r ext.Range) string {
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
func (b *Buffer) checkRange(r ext.Range) error {
	if err := b.checkPosition(r.Start); err != nil {
		return err
	}
	if err := b.checkPosition(r.End); err != nil {
		return err
	}
	if before(r.End, r.Start) {
		return fmt.Errorf("%w: end %d:%d is before start %d:%d", ErrInvalidRange, r.End.Line, r.End.Byte, r.Start.Line, r.Start.Byte)
	}
	return nil
}

// checkPosition は、p がバッファの中の文字の境界（行末を含む）を指すことを確かめる。
// 文字の境界は本体（internal/buffer）と同じく、行の先頭から utf8.DecodeRuneInString で進めたときに止まる位置である。
// 不正な UTF-8 のバイトは 1 バイトを 1 文字として数える。
func (b *Buffer) checkPosition(p ext.Position) error {
	if p.Line < 0 || p.Line >= len(b.lines) {
		return fmt.Errorf("%w: %d:%d: line out of range [0, %d)", ErrInvalidPosition, p.Line, p.Byte, len(b.lines))
	}
	line := b.lines[p.Line]
	if p.Byte < 0 || p.Byte > len(line) {
		return fmt.Errorf("%w: %d:%d: byte out of range [0, %d]", ErrInvalidPosition, p.Line, p.Byte, len(line))
	}
	if insideRune(line, p.Byte) {
		return fmt.Errorf("%w: %d:%d: inside a UTF-8 character", ErrInvalidPosition, p.Line, p.Byte)
	}
	return nil
}

// before は、p が q より前にあるかどうかを返す。
func before(p, q ext.Position) bool {
	if p.Line != q.Line {
		return p.Line < q.Line
	}
	return p.Byte < q.Byte
}

// insideRune は、i が line の中の正しい UTF-8 の多バイト文字の途中を指すかどうかを返す。
// i の前 utf8.UTFMax-1 バイト以内から始まる正しい多バイト文字が i をまたぐかだけを見る。
func insideRune(line string, i int) bool {
	for j := max(0, i-(utf8.UTFMax-1)); j < i; j++ {
		_, size := utf8.DecodeRuneInString(line[j:])
		if size > 1 && j+size > i {
			return true
		}
	}
	return false
}
