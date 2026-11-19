package main

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/piplupflipper/vi-imitation/ext"
)

// errEditOutOfRange は、編集の位置が手元の全文の外を指すことを表す。
var errEditOutOfRange = errors.New("edit out of range")

// document は、1 つのバッファの構文木と、それを作ったもとの全文である。
//
// 位置の変換: 本体の位置（ext.Position）は、行の番号と行の先頭からのバイト数の組で、どちらも 0 から数える（基本設計 4.1）。
// tree-sitter の Point も、行（Row）と行の先頭からのバイト数（Column）の組で、0 から数え、行は "\n" で区切る。
// 本体は末尾の改行を含めない全文を送り、行を "\n" で区切るので、両者は同じ値になり、変換は型を写すだけである。
// 全文の中のバイト位置は、行の先頭のバイト位置の表（lineStarts）から求める。
type document struct {
	lang *language
	tree *sitter.Tree
	// src は、tree を作ったもとの全文である。末尾の改行を含めない（ext.BufferOpenParams）。
	src []byte
	// lineStarts は、各行の先頭のバイト位置である。lineStarts[0] は 0 で、長さは行の数である。
	lineStarts []int
}

// newDocument は、text を全文から解析した document を作る。
func newDocument(lang *language, text string) *document {
	d := &document{lang: lang}
	d.reset(text)
	return d
}

// reset は、構文木を捨て、text を全文から解析し直す。
func (d *document) reset(text string) {
	d.close()
	d.src = []byte(text)
	d.lineStarts = lineStartsOf(d.src)
	d.tree = d.lang.parser.Parse(d.src, nil)
}

// close は、構文木の C のメモリを解放する。
func (d *document) close() {
	if d.tree != nil {
		d.tree.Close()
		d.tree = nil
	}
}

// applyEdits は、buffer/change の編集を、逐次の数え方（ext.BufferChangeParams）のまま先頭から 1 つずつ、構文木と全文に適用し、増分で解析し直す。
//
// 各編集は直前の編集までを適用した全文で数えてあるので、適用するたびに数え直さずに、
// tree-sitter の InputEdit（開始・古い終わり・新しい終わりのバイト位置と Point）が得られる（基本設計 6.1）。
// 編集の位置が全文の外を指すときは errEditOutOfRange を包んだ誤りを返す。そのときの構文木と全文は途中までの編集を適用したものなので、呼び出し側は reset で解析し直す。
//
// req: FR-35
func (d *document) applyEdits(edits []ext.Edit) error {
	for i, e := range edits {
		if err := d.applyEdit(e); err != nil {
			return fmt.Errorf("edit %d: %w", i, err)
		}
	}
	old := d.tree
	d.tree = d.lang.parser.Parse(d.src, old)
	old.Close()
	return nil
}

// applyEdit は、1 つの編集を構文木と全文に適用する。構文木の解析し直しはしない。
func (d *document) applyEdit(e ext.Edit) error {
	ie, err := d.inputEdit(e)
	if err != nil {
		return err
	}
	d.tree.Edit(&ie)
	start, oldEnd := int(ie.StartByte), int(ie.OldEndByte)
	src := make([]byte, 0, len(d.src)-(oldEnd-start)+len(e.Text))
	src = append(src, d.src[:start]...)
	src = append(src, e.Text...)
	src = append(src, d.src[oldEnd:]...)
	d.src = src
	d.lineStarts = lineStartsOf(d.src)
	return nil
}

// inputEdit は、いまの全文に対する編集 e を、tree-sitter の InputEdit に写す。
// 開始と古い終わりは編集の範囲そのもの、新しい終わりは開始の位置に e.Text を挿入した直後の位置である。
func (d *document) inputEdit(e ext.Edit) (sitter.InputEdit, error) {
	start, err := d.offset(e.Range.Start)
	if err != nil {
		return sitter.InputEdit{}, err
	}
	oldEnd, err := d.offset(e.Range.End)
	if err != nil {
		return sitter.InputEdit{}, err
	}
	if oldEnd < start {
		return sitter.InputEdit{}, fmt.Errorf("%w: end %d:%d is before start %d:%d", errEditOutOfRange, e.Range.End.Line, e.Range.End.Byte, e.Range.Start.Line, e.Range.Start.Byte)
	}
	return sitter.InputEdit{
		StartByte:      uint(start),
		OldEndByte:     uint(oldEnd),
		NewEndByte:     uint(start + len(e.Text)),
		StartPosition:  toPoint(e.Range.Start),
		OldEndPosition: toPoint(e.Range.End),
		NewEndPosition: toPoint(endPosition(e.Range.Start, e.Text)),
	}, nil
}

// offset は、位置 p の全文の中のバイト位置を返す。行末（改行の直前）までを指せる。
func (d *document) offset(p ext.Position) (int, error) {
	if p.Line < 0 || p.Line >= len(d.lineStarts) || p.Byte < 0 || p.Byte > d.lineLen(p.Line) {
		return 0, fmt.Errorf("%w: %d:%d", errEditOutOfRange, p.Line, p.Byte)
	}
	return d.lineStarts[p.Line] + p.Byte, nil
}

// lineLen は、line 行目の長さ（改行を含まない）を返す。
func (d *document) lineLen(line int) int {
	if line+1 < len(d.lineStarts) {
		return d.lineStarts[line+1] - 1 - d.lineStarts[line]
	}
	return len(d.src) - d.lineStarts[line]
}

// position は、全文の中のバイト位置 off を、本体の位置に写す。
func (d *document) position(off int) ext.Position {
	// off を含む行は、先頭が off 以下の最後の行である。
	line := sort.Search(len(d.lineStarts), func(i int) bool { return d.lineStarts[i] > off }) - 1
	return ext.Position{Line: line, Byte: off - d.lineStarts[line]}
}

// lineStartsOf は、src の各行の先頭のバイト位置を返す。
func lineStartsOf(src []byte) []int {
	starts := make([]int, 1, bytes.Count(src, []byte{'\n'})+1)
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// endPosition は、start に text を挿入したときの、挿入したテキストの直後の位置を返す。
func endPosition(start ext.Position, text string) ext.Position {
	n := 0
	last := -1
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			n++
			last = i
		}
	}
	if n == 0 {
		return ext.Position{Line: start.Line, Byte: start.Byte + len(text)}
	}
	return ext.Position{Line: start.Line + n, Byte: len(text) - last - 1}
}

// toPoint は、本体の位置を tree-sitter の Point に写す（document の doc の「位置の変換」）。
func toPoint(p ext.Position) sitter.Point {
	return sitter.Point{Row: uint(p.Line), Column: uint(p.Byte)}
}
