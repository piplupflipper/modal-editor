package server

import (
	"errors"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

func pos(line, b int) ext.Position { return ext.Position{Line: line, Byte: b} }

func rng(sl, sb, el, eb int) ext.Range { return ext.Range{Start: pos(sl, sb), End: pos(el, eb)} }

func open(text string, v ext.Version) *Buffer {
	return newBuffer(&ext.BufferOpenParams{Buffer: 1, Version: v, Text: text})
}

func TestBufferLines(t *testing.T) {
	tests := []struct {
		text  string
		lines int
	}{
		{"", 1},
		{"abc", 1},
		{"a\nb", 2},
		{"a\n", 2}, // 本体は末尾の改行を含めずに送るので、含まれていれば空の行として数える
	}
	for _, tt := range tests {
		b := open(tt.text, 0)
		if b.LineCount() != tt.lines || b.Text() != tt.text {
			t.Errorf("open(%q): LineCount = %d, Text = %q; want %d, %q", tt.text, b.LineCount(), b.Text(), tt.lines, tt.text)
		}
	}
}

// req: FR-16
func TestApplyChangeSequential(t *testing.T) {
	b := open("hello\nworld", 3)
	// 逐次の数え方: 2 つ目の編集の位置は、1 つ目を適用した後のテキストで数える。
	err := b.applyChange(&ext.BufferChangeParams{
		Buffer: 1, FromVersion: 3, ToVersion: 5,
		Edits: []ext.Edit{
			{Range: rng(0, 5, 1, 0), Text: ", "},    // "hello, world"
			{Range: rng(0, 12, 0, 12), Text: "!\n"}, // "hello, world!\n" → 2 行目は空
			{Range: rng(0, 0, 0, 5), Text: "こんにちは"},
		},
	})
	if err != nil {
		t.Fatalf("applyChange: %v", err)
	}
	if got, want := b.Text(), "こんにちは, world!\n"; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	if b.Version() != 5 {
		t.Errorf("Version = %d, want 5", b.Version())
	}
}

func TestApplyChangeInvalid(t *testing.T) {
	tests := []struct {
		name string
		r    ext.Range
		want error
	}{
		{"行の外", rng(2, 0, 2, 0), ErrInvalidPosition},
		{"行末の外", rng(0, 0, 0, 10), ErrInvalidPosition},
		{"負の位置", rng(0, -1, 0, 0), ErrInvalidPosition},
		{"多バイト文字の途中", rng(0, 1, 0, 3), ErrInvalidPosition},
		{"逆向き", rng(0, 3, 0, 0), ErrInvalidRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := open("あい\nx", 0)
			err := b.applyChange(&ext.BufferChangeParams{FromVersion: 0, ToVersion: 1, Edits: []ext.Edit{{Range: tt.r}}})
			if !errors.Is(err, tt.want) {
				t.Errorf("applyChange = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestSliceAndClone(t *testing.T) {
	b := open("ab\ncd\nef", 0)
	got, err := b.Slice(rng(0, 1, 2, 1))
	if err != nil || got != "b\ncd\ne" {
		t.Errorf("Slice = %q, %v; want %q", got, err, "b\ncd\ne")
	}
	// 不正な UTF-8 のバイトは 1 バイトを 1 文字として数えるので、その前後は境界である。
	bad := open("a\xffb", 0)
	if got, err := bad.Slice(rng(0, 1, 0, 2)); err != nil || got != "\xff" {
		t.Errorf("Slice(invalid utf-8) = %q, %v", got, err)
	}

	c := b.Clone()
	if err := b.applyChange(&ext.BufferChangeParams{ToVersion: 1, Edits: []ext.Edit{{Range: rng(1, 0, 1, 2), Text: "X"}}}); err != nil {
		t.Fatal(err)
	}
	if c.Text() != "ab\ncd\nef" || c.Version() != 0 {
		t.Errorf("Clone changed with the original: %q v%d", c.Text(), c.Version())
	}
	if b.Text() != "ab\nX\nef" {
		t.Errorf("Text = %q", b.Text())
	}
}
