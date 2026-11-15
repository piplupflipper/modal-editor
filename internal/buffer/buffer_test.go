package buffer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func pos(line, b int) Position { return Position{Line: line, Byte: b} }

func rng(sl, sb, el, eb int) Range { return Range{Start: pos(sl, sb), End: pos(el, eb)} }

func TestNewLines(t *testing.T) {
	tests := []struct {
		text  string
		lines []string
	}{
		{"", []string{""}},
		{"abc", []string{"abc"}},
		{"abc\n", []string{"abc", ""}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb", []string{"a\r", "b"}}, // CR は行の中のバイトとして残す
	}
	for _, tt := range tests {
		b := New(tt.text)
		var got []string
		for i := range b.LineCount() {
			got = append(got, b.Line(i))
		}
		if !slices.Equal(got, tt.lines) {
			t.Errorf("New(%q) lines = %q, want %q", tt.text, got, tt.lines)
		}
		if b.Text() != tt.text {
			t.Errorf("New(%q).Text() = %q", tt.text, b.Text())
		}
		if b.Version() != 0 {
			t.Errorf("New(%q).Version() = %d, want 0", tt.text, b.Version())
		}
	}
}

// req: FR-15
func TestApply(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		edits   []Edit
		want    string
		wantErr error
	}{
		{
			name:  "行の途中への挿入",
			text:  "hello world",
			edits: []Edit{{Range: rng(0, 5, 0, 5), Text: ","}},
			want:  "hello, world",
		},
		{
			name:  "空のバッファへの挿入",
			text:  "",
			edits: []Edit{{Range: rng(0, 0, 0, 0), Text: "abc"}},
			want:  "abc",
		},
		{
			name:  "行の途中の削除",
			text:  "hello world",
			edits: []Edit{{Range: rng(0, 5, 0, 11)}},
			want:  "hello",
		},
		{
			name:  "置き換え",
			text:  "hello world",
			edits: []Edit{{Range: rng(0, 6, 0, 11), Text: "there"}},
			want:  "hello there",
		},
		{
			name:  "複数行の挿入",
			text:  "ad",
			edits: []Edit{{Range: rng(0, 1, 0, 1), Text: "b\nc\n"}},
			want:  "ab\nc\nd",
		},
		{
			name:  "行をまたぐ削除",
			text:  "one\ntwo\nthree",
			edits: []Edit{{Range: rng(0, 2, 2, 2)}},
			want:  "onree",
		},
		{
			name:  "改行の削除による行の結合",
			text:  "one\ntwo",
			edits: []Edit{{Range: rng(0, 3, 1, 0)}},
			want:  "onetwo",
		},
		{
			name:  "行をまたぐ置き換え",
			text:  "one\ntwo\nthree",
			edits: []Edit{{Range: rng(0, 1, 1, 1), Text: "X\nY"}},
			want:  "oX\nYwo\nthree",
		},
		{
			name:  "末尾の改行の削除",
			text:  "abc\n",
			edits: []Edit{{Range: rng(0, 3, 1, 0)}},
			want:  "abc",
		},
		{
			name: "複数の編集は前の編集の後のテキストに対して順に適用する",
			text: "abc",
			edits: []Edit{
				{Range: rng(0, 0, 0, 0), Text: "x\n"},
				{Range: rng(1, 3, 1, 3), Text: "y"},
			},
			want: "x\nabcy",
		},
		{
			name:  "多バイト文字の後への挿入",
			text:  "あい",
			edits: []Edit{{Range: rng(0, 3, 0, 3), Text: "う"}},
			want:  "あうい",
		},
		{
			name:  "多バイト文字の置き換え",
			text:  "日本語",
			edits: []Edit{{Range: rng(0, 3, 0, 6), Text: "a"}},
			want:  "日a語",
		},
		{
			name:    "多バイト文字の途中を指す位置",
			text:    "あい",
			edits:   []Edit{{Range: rng(0, 1, 0, 1), Text: "x"}},
			want:    "あい",
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "行の数を超える位置",
			text:    "abc",
			edits:   []Edit{{Range: rng(1, 0, 1, 0), Text: "x"}},
			want:    "abc",
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "行の長さを超える位置",
			text:    "abc",
			edits:   []Edit{{Range: rng(0, 4, 0, 4), Text: "x"}},
			want:    "abc",
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "負の位置",
			text:    "abc",
			edits:   []Edit{{Range: rng(0, -1, 0, 0), Text: "x"}},
			want:    "abc",
			wantErr: ErrInvalidPosition,
		},
		{
			name:    "終わりが始まりより前の範囲",
			text:    "abc",
			edits:   []Edit{{Range: rng(0, 2, 0, 1)}},
			want:    "abc",
			wantErr: ErrInvalidRange,
		},
		{
			name: "途中で失敗したトランザクションは全く適用しない",
			text: "one\ntwo",
			edits: []Edit{
				{Range: rng(0, 0, 1, 1), Text: "A\nB\nC"},
				{Range: rng(0, 0, 0, 0), Text: "x"},
				{Range: rng(9, 0, 9, 0), Text: "y"},
			},
			want:    "one\ntwo",
			wantErr: ErrInvalidPosition,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(tt.text)
			change, err := b.Apply(Transaction{BaseVersion: 0, Edits: tt.edits})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Apply() error = %v, want %v", err, tt.wantErr)
			}
			if got := b.Text(); got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
			wantVersion := uint64(1)
			if tt.wantErr != nil {
				wantVersion = 0
			}
			if b.Version() != wantVersion {
				t.Errorf("Version() = %d, want %d", b.Version(), wantVersion)
			}
			if tt.wantErr != nil {
				if b.CanUndo() {
					t.Error("失敗したトランザクションがアンドゥの履歴に残っている")
				}
				return
			}
			if change.Before != 0 || change.After != 1 || !slices.Equal(change.Edits, tt.edits) {
				t.Errorf("Change = %+v, want Before 0, After 1, Edits %+v", change, tt.edits)
			}
		})
	}
}

// req: FR-15
func TestApplyVersionMismatch(t *testing.T) {
	b := New("abc")
	if _, err := b.Apply(Transaction{BaseVersion: 0, Edits: []Edit{{Range: rng(0, 3, 0, 3), Text: "d"}}}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		base uint64
	}{
		{"古い番号", 0},
		{"未来の番号", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := b.Apply(Transaction{BaseVersion: tt.base, Edits: []Edit{{Range: rng(0, 0, 0, 0), Text: "x"}}})
			if !errors.Is(err, ErrVersionMismatch) {
				t.Fatalf("Apply() error = %v, want ErrVersionMismatch", err)
			}
			if b.Text() != "abcd" || b.Version() != 1 {
				t.Errorf("不一致のトランザクションでバッファが変わった: Text() = %q, Version() = %d", b.Text(), b.Version())
			}
		})
	}
}

func TestApplyEmptyTransaction(t *testing.T) {
	b := New("abc")
	change, err := b.Apply(Transaction{BaseVersion: 0})
	if err != nil {
		t.Fatal(err)
	}
	if change.Before != 0 || change.After != 0 || b.Version() != 0 || b.CanUndo() {
		t.Errorf("空のトランザクションで状態が変わった: Change = %+v, Version() = %d", change, b.Version())
	}
	if _, err := b.Apply(Transaction{BaseVersion: 3}); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("空のトランザクションでも番号を確かめる: error = %v", err)
	}
}

// TestUndoRedoRoundTrip は、トランザクションを順に適用した後、全てアンドゥすると元に戻り、
// 全てリドゥすると適用後に戻ることを確かめる。
//
// req: FR-07
func TestUndoRedoRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		text string
		txs  [][]Edit
	}{
		{
			name: "1 行の挿入と削除",
			text: "hello",
			txs: [][]Edit{
				{{Range: rng(0, 5, 0, 5), Text: " world"}},
				{{Range: rng(0, 0, 0, 1), Text: "H"}},
				{{Range: rng(0, 5, 0, 11)}},
			},
		},
		{
			name: "複数行と複数の編集",
			text: "one\ntwo\nthree\n",
			txs: [][]Edit{
				{{Range: rng(0, 1, 2, 3), Text: "X\nY"}, {Range: rng(1, 0, 1, 0), Text: "\n\n"}},
				{{Range: rng(0, 0, 3, 0)}},
				{{Range: rng(0, 0, 0, 0), Text: "z\n"}},
			},
		},
		{
			name: "多バイト文字",
			text: "日本語\nかな",
			txs: [][]Edit{
				{{Range: rng(0, 3, 1, 3), Text: "🙂"}},
				{{Range: rng(0, 0, 0, 3), Text: "にほん\n"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(tt.text)
			texts := []string{b.Text()}
			for _, edits := range tt.txs {
				if _, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: edits}); err != nil {
					t.Fatal(err)
				}
				texts = append(texts, b.Text())
			}
			version := b.Version()

			for i := len(tt.txs) - 1; i >= 0; i-- {
				if _, ok := b.Undo(); !ok {
					t.Fatalf("Undo() = false at %d", i)
				}
				version++
				if b.Text() != texts[i] || b.Version() != version {
					t.Errorf("Undo の後: Text() = %q, Version() = %d, want %q, %d", b.Text(), b.Version(), texts[i], version)
				}
			}
			if _, ok := b.Undo(); ok {
				t.Error("履歴の先頭で Undo() = true")
			}

			for i := 1; i < len(texts); i++ {
				if _, ok := b.Redo(); !ok {
					t.Fatalf("Redo() = false at %d", i)
				}
				version++
				if b.Text() != texts[i] || b.Version() != version {
					t.Errorf("Redo の後: Text() = %q, Version() = %d, want %q, %d", b.Text(), b.Version(), texts[i], version)
				}
			}
			if _, ok := b.Redo(); ok {
				t.Error("履歴の末尾で Redo() = true")
			}
		})
	}
}

// req: FR-07
func TestApplyClearsRedo(t *testing.T) {
	b := New("a")
	if _, err := b.Apply(Transaction{BaseVersion: 0, Edits: []Edit{{Range: rng(0, 1, 0, 1), Text: "b"}}}); err != nil {
		t.Fatal(err)
	}
	b.Undo()
	if !b.CanRedo() {
		t.Fatal("Undo の後に CanRedo() = false")
	}
	if _, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(0, 1, 0, 1), Text: "c"}}}); err != nil {
		t.Fatal(err)
	}
	if b.CanRedo() {
		t.Error("新しい編集の後にリドゥの履歴が残っている")
	}
	if b.Text() != "ac" {
		t.Errorf("Text() = %q, want %q", b.Text(), "ac")
	}
}

// TestChangeReplay は、Apply、Undo、Redo が返す Change を、別のバッファ（拡張機能の側の写し）へ
// 順に適用すると、同じテキストとバージョン番号の対応になることを確かめる。
//
// req: FR-16
func TestChangeReplay(t *testing.T) {
	b := New("alpha\nbeta\n")
	mirror := New(b.Text())

	ops := []func() (Change, bool){
		func() (Change, bool) {
			c, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: []Edit{
				{Range: rng(0, 0, 0, 5), Text: "ALPHA"},
				{Range: rng(1, 4, 2, 0), Text: "!\ngamma\n"},
			}})
			return c, err == nil
		},
		b.Undo,
		b.Redo,
		func() (Change, bool) {
			c, err := b.Apply(Transaction{BaseVersion: b.Version(), Edits: []Edit{{Range: rng(0, 2, 2, 2)}}})
			return c, err == nil
		},
		b.Undo,
		b.Undo,
	}
	for i, op := range ops {
		before := b.Version()
		c, ok := op()
		if !ok {
			t.Fatalf("op %d failed", i)
		}
		if c.Before != before || c.After != b.Version() || c.After != c.Before+1 {
			t.Errorf("op %d: Change の版 = %d -> %d, バッファ %d -> %d", i, c.Before, c.After, before, b.Version())
		}
		for _, e := range c.Edits {
			if _, err := mirror.apply(e); err != nil {
				t.Fatalf("op %d: 写しへの適用に失敗: %v", i, err)
			}
		}
		if mirror.Text() != b.Text() {
			t.Errorf("op %d: 写し = %q, バッファ = %q", i, mirror.Text(), b.Text())
		}
	}
}

func TestChangeIsIndependentOfHistory(t *testing.T) {
	b := New("abc")
	edits := []Edit{{Range: rng(0, 0, 0, 0), Text: "x"}}
	c, err := b.Apply(Transaction{BaseVersion: 0, Edits: edits})
	if err != nil {
		t.Fatal(err)
	}
	// 呼び出し側が渡した列や返された列を書き換えても、履歴は変わらない。
	edits[0].Text = "EVIL"
	c.Edits[0].Text = "EVIL"
	b.Undo()
	b.Redo()
	if b.Text() != "xabc" {
		t.Errorf("Text() = %q, want %q", b.Text(), "xabc")
	}
}

func TestSlice(t *testing.T) {
	b := New("one\nあいう\nthree\n")
	tests := []struct {
		r       Range
		want    string
		wantErr error
	}{
		{rng(0, 0, 0, 3), "one", nil},
		{rng(0, 1, 0, 1), "", nil},
		{rng(1, 3, 1, 6), "い", nil},
		{rng(0, 2, 2, 2), "e\nあいう\nth", nil},
		{rng(0, 3, 1, 0), "\n", nil},
		{rng(0, 0, 3, 0), "one\nあいう\nthree\n", nil},
		{rng(1, 2, 1, 3), "", ErrInvalidPosition},
		{rng(0, 0, 4, 0), "", ErrInvalidPosition},
		{rng(1, 0, 0, 0), "", ErrInvalidRange},
	}
	for _, tt := range tests {
		got, err := b.Slice(tt.r)
		if !errors.Is(err, tt.wantErr) || got != tt.want {
			t.Errorf("Slice(%v-%v) = %q, %v; want %q, %v", tt.r.Start, tt.r.End, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestFileRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"空のファイル", ""},
		{"末尾の改行あり", "a\nb\n"},
		{"末尾の改行なし", "a\nb"},
		{"空行で終わる", "a\n\n"},
		{"多バイト文字", "日本語\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "f.txt")
			if err := os.WriteFile(path, []byte(tt.text), 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if b.Text() != tt.text {
				t.Errorf("ReadFile の Text() = %q, want %q", b.Text(), tt.text)
			}
			if err = b.WriteFile(path); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.text {
				t.Errorf("WriteFile の内容 = %q, want %q", got, tt.text)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Errorf("許可属性 = %v, want 0600", info.Mode().Perm())
			}
		})
	}
}

func TestWriteTo(t *testing.T) {
	b, err := Read(bytes.NewBufferString("x\ny\n"))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	n, err := b.WriteTo(&out)
	if err != nil || n != 4 || out.String() != "x\ny\n" {
		t.Errorf("WriteTo() = %d, %v, %q", n, err, out.String())
	}
}

func TestReadFileNotExist(t *testing.T) {
	_, err := ReadFile(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadFile() error = %v, want os.ErrNotExist", err)
	}
}
