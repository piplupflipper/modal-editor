package buffer

import (
	"errors"
	"path/filepath"
	"testing"
)

// TestReadOnlyApply は、編集を禁止したバッファへの Apply が、何も変えずに ErrReadOnly を返すことを確かめる。
//
// req: FR-85, FR-118
func TestReadOnlyApply(t *testing.T) {
	b := New("abc\n")
	b.SetReadOnly(true)
	if !b.ReadOnly() {
		t.Fatal("ReadOnly() = false after SetReadOnly(true)")
	}
	_, err := b.Apply(Transaction{BaseVersion: 0, Edits: []Edit{{Range: rng(0, 0, 0, 1), Text: "x"}}})
	if !errors.Is(err, ErrReadOnly) {
		t.Fatalf("Apply err = %v, want ErrReadOnly", err)
	}
	if b.Text() != "abc" || b.Version() != 0 || b.CanUndo() || b.Modified() {
		t.Errorf("after refused Apply: text %q, version %d, CanUndo %v, Modified %v", b.Text(), b.Version(), b.CanUndo(), b.Modified())
	}

	// 版の不一致は禁止より先に返す。空の編集は禁止中でも何もしない成功である。
	if _, err := b.Apply(Transaction{BaseVersion: 5, Edits: []Edit{{Range: rng(0, 0, 0, 0), Text: "x"}}}); !errors.Is(err, ErrVersionMismatch) {
		t.Errorf("Apply with stale version err = %v, want ErrVersionMismatch", err)
	}
	if ch, err := b.Apply(Transaction{BaseVersion: 0}); err != nil || ch.Before != ch.After {
		t.Errorf("Apply empty = %+v, %v; want no-op", ch, err)
	}

	b.SetReadOnly(false)
	if _, err := b.Apply(Transaction{BaseVersion: 0, Edits: []Edit{{Range: rng(0, 0, 0, 1), Text: "x"}}}); err != nil {
		t.Fatalf("Apply after SetReadOnly(false): %v", err)
	}
	if b.Text() != "xbc" {
		t.Errorf("Text() = %q, want %q", b.Text(), "xbc")
	}
}

// TestReadOnlyUndoRedo は、禁止中のアンドゥとリドゥが断られ、禁止の前の履歴は残って、禁止を解けば戻れることを確かめる。
//
// req: FR-85
func TestReadOnlyUndoRedo(t *testing.T) {
	b := New("abc")
	mustApply(t, b, Edit{Range: rng(0, 0, 0, 1), Text: "x"})
	mustApply(t, b, Edit{Range: rng(0, 1, 0, 2), Text: "y"})
	if _, ok := b.Undo(); !ok {
		t.Fatal("Undo failed")
	}
	b.SetReadOnly(true)
	v := b.Version()
	if _, ok := b.Undo(); ok {
		t.Error("Undo succeeded on a read-only buffer")
	}
	if _, ok := b.Redo(); ok {
		t.Error("Redo succeeded on a read-only buffer")
	}
	if b.Text() != "xbc" || b.Version() != v {
		t.Errorf("after refused Undo/Redo: text %q version %d, want %q %d", b.Text(), b.Version(), "xbc", v)
	}
	b.SetReadOnly(false)
	if _, ok := b.Undo(); !ok || b.Text() != "abc" {
		t.Errorf("Undo after SetReadOnly(false): ok %v text %q", ok, b.Text())
	}
}

// TestReadOnlyReloadAndWrite は、禁止中でも本体の読み直し（ApplyReload）と書き出しは断らないことを確かめる。
//
// req: FR-85
func TestReadOnlyReloadAndWrite(t *testing.T) {
	b := New("abc\n")
	b.SetReadOnly(true)
	if _, err := b.ApplyReload(Transaction{BaseVersion: 0, Edits: []Edit{{Range: rng(0, 0, 0, 3), Text: "new"}}}, true); err != nil {
		t.Fatalf("ApplyReload on a read-only buffer: %v", err)
	}
	if b.Text() != "new" {
		t.Errorf("Text() = %q, want %q", b.Text(), "new")
	}
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := b.WriteFile(path); err != nil {
		t.Fatalf("WriteFile on a read-only buffer: %v", err)
	}
	b.MarkSaved()
	if b.Modified() {
		t.Error("Modified() = true after MarkSaved")
	}
}
