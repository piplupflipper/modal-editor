package persist

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 書いた履歴は、同じハッシュで読み戻せる。置き場のディレクトリは 0700、ファイルは 0600 で作り、名前はパスの / を % に替えたものにする。
//
// req: FR-165
func TestUndoRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "vi-imitation")
	file := "/home/u/a.txt"
	sum := sha256.Sum256([]byte("abc\n"))
	history := []byte("history\xff\x00bytes")
	if err := WriteUndo(dir, file, sum, history); err != nil {
		t.Fatal(err)
	}
	got, err := ReadUndo(dir, file, sum)
	if err != nil || string(got) != string(history) {
		t.Fatalf("ReadUndo = %q, %v; want %q", got, err, history)
	}
	path := filepath.Join(dir, "undo", "%home%u%a.txt")
	if UndoPath(dir, file) != path {
		t.Errorf("UndoPath = %q, want %q", UndoPath(dir, file), path)
	}
	for p, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "undo"): 0o700, path: 0o600} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s の許可属性 %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
	// 一時ファイルを残さない。
	if ents, _ := os.ReadDir(filepath.Join(dir, "undo")); len(ents) != 1 {
		t.Errorf("undo の中身 %v, want 1 件", ents)
	}
	if err := RemoveUndo(dir, file); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUndo(dir, file, sum); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("消した後の ReadUndo の誤り %v, want ErrNotExist", err)
	}
	if err := RemoveUndo(dir, file); err != nil {
		t.Errorf("無いファイルの RemoveUndo = %v", err)
	}
}

// BenchmarkWriteUndo は、1.3 MB の履歴（internal/buffer の BenchmarkMarshalHistory10k の大きさ。10,000 行のファイルの全体を
// 2 回書き換えた履歴）を、ディスクへの同期を含めて書く時間を測る（#319）。
func BenchmarkWriteUndo(b *testing.B) {
	dir := b.TempDir()
	history := make([]byte, 1300342)
	for b.Loop() {
		if err := WriteUndo(dir, "/a.txt", Sum{}, history); err != nil {
			b.Fatal(err)
		}
	}
}

// ハッシュが違えば ErrUndoStale を、印や版が違えば ErrUndoFormat を返す。
//
// req: FR-165
func TestUndoReject(t *testing.T) {
	dir := t.TempDir()
	file := "/a.txt"
	sum := sha256.Sum256([]byte("abc\n"))
	if err := WriteUndo(dir, file, sum, []byte("h")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadUndo(dir, file, sha256.Sum256([]byte("abd\n"))); !errors.Is(err, ErrUndoStale) {
		t.Errorf("ハッシュの違う ReadUndo の誤り %v, want ErrUndoStale", err)
	}
	path := UndoPath(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	other := []byte(string(data))
	other[len(undoMagic)+3] = UndoFormat + 1
	for name, content := range map[string][]byte{
		"版が違う":   other,
		"印が無い":   []byte("not an undo file"),
		"途中で切れた": data[:len(undoMagic)+10],
	} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadUndo(dir, file, sum); !errors.Is(err, ErrUndoFormat) {
			t.Errorf("%s: ReadUndo の誤り %v, want ErrUndoFormat", name, err)
		}
	}
}

// シンボリックリンクのファイルの履歴は、リンクの指す先のパスから名前を決める（Vim 9.1 と Neovim 0.12.5 と同じ）。
//
// req: FR-165
func TestUndoPathSymlink(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "real.txt")
	link := filepath.Join(tmp, "link.txt")
	if err := os.WriteFile(target, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if a, b := UndoPath("/s", link), UndoPath("/s", target); a != b {
		t.Errorf("リンクの UndoPath %q と元の UndoPath %q が違う", a, b)
	}
	// 解決できないリンクは、そのままのパスで決める。
	dangling := filepath.Join(tmp, "dangling")
	if err := os.Symlink(filepath.Join(tmp, "none"), dangling); err != nil {
		t.Fatal(err)
	}
	if got, want := UndoPath("/s", dangling), filepath.Join("/s", "undo", strings.ReplaceAll(dangling, "/", "%")); got != want {
		t.Errorf("UndoPath = %q, want %q", got, want)
	}
}

// 書けない置き場（ディレクトリの代わりにファイルがある）では誤りを返す。
//
// req: FR-165, FR-166
func TestWriteFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "vi-imitation")
	if err := WriteUndo(dir, "/a", Sum{}, nil); err == nil {
		t.Error("WriteUndo が誤りを返さない")
	}
	if err := SaveCursor(dir, "/a", Position{}, time.Unix(1, 0)); err == nil {
		t.Error("SaveCursor が誤りを返さない")
	}
}

// 閉じたときの位置を残し、読み戻せる。同じパスは 1 件にまとめて先頭へ移し、MaxCursors 件を超えたら古いものから捨てる。
//
// req: FR-166
func TestCursorSaveLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vi-imitation")
	if _, ok, err := LoadCursor(dir, "/a"); ok || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("無い置き場の LoadCursor = %v, %v", ok, err)
	}
	now := time.Unix(1000, 0)
	if err := SaveCursor(dir, "/a", Position{Line: 4, Byte: 7}, now); err != nil {
		t.Fatal(err)
	}
	if err := SaveCursor(dir, "/b", Position{Line: 0, Byte: 0}, now); err != nil {
		t.Fatal(err)
	}
	if p, ok, err := LoadCursor(dir, "/a"); !ok || err != nil || p != (Position{Line: 4, Byte: 7}) {
		t.Errorf("LoadCursor(/a) = %v, %v, %v", p, ok, err)
	}
	if err := SaveCursor(dir, "/a", Position{Line: 9, Byte: 1}, now); err != nil {
		t.Fatal(err)
	}
	if p, _, _ := LoadCursor(dir, "/a"); p != (Position{Line: 9, Byte: 1}) {
		t.Errorf("上書きした後の LoadCursor(/a) = %v", p)
	}
	st := readTestState(t, dir)
	if len(st.Files) != 2 || st.Files[0].Path != "/a" || st.Files[0].Line != 10 || st.Files[0].Col != 1 || st.Version != 1 {
		t.Errorf("状態のファイル %+v", st)
	}
	fi, err := os.Stat(filepath.Join(dir, StateFileName))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("状態のファイルの許可属性 %v, %v", fi.Mode().Perm(), err)
	}

	for i := range MaxCursors + 5 {
		if err := SaveCursor(dir, fmt.Sprintf("/f%d", i), Position{Line: i}, now); err != nil {
			t.Fatal(err)
		}
	}
	st = readTestState(t, dir)
	if len(st.Files) != MaxCursors || st.Files[0].Path != fmt.Sprintf("/f%d", MaxCursors+4) {
		t.Errorf("件数 %d、先頭 %q", len(st.Files), st.Files[0].Path)
	}
	if _, ok, _ := LoadCursor(dir, "/a"); ok {
		t.Error("古い /a の記録が残った")
	}
}

type testState struct {
	Version int           `json:"version"`
	Files   []cursorEntry `json:"files"`
}

func readTestState(t *testing.T, dir string) testState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var st testState
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// 複数の server が同時に書いても、互いの記録を消さない（ロックの中で読み直して合わせる）。
// ロックはファイルを開くごとに掛かるので、同じプロセスの goroutine の間でも、別のプロセスの間と同じく排他になる。
//
// req: FR-166
func TestCursorConcurrent(t *testing.T) {
	dir := t.TempDir()
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Go(func() {
			errs <- SaveCursor(dir, fmt.Sprintf("/f%d", i), Position{Line: i}, time.Unix(int64(i), 0))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range n {
		if p, ok, err := LoadCursor(dir, fmt.Sprintf("/f%d", i)); !ok || err != nil || p.Line != i {
			t.Errorf("/f%d: %v, %v, %v", i, p, ok, err)
		}
	}
}

// 知らない項目は書き換えた後も残る。壊れた状態のファイルは捨てて書き直し、新しい版のものは読みも書きもしない。
//
// req: FR-166
func TestCursorStateFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateFileName)
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version": 1, "registers": {"a": "x"}, "files": [{"path": "/a", "line": 3, "col": 0, "time": 1, "marks": {"x": 1}}]}`)
	if err := SaveCursor(dir, "/a", Position{Line: 5}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"registers"`, `"marks"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("書き換えた後に %s が消えた: %s", want, data)
		}
	}

	write(`not json`)
	if _, ok, err := LoadCursor(dir, "/a"); ok || err == nil {
		t.Errorf("壊れたファイルの LoadCursor = %v, %v", ok, err)
	}
	if err := SaveCursor(dir, "/a", Position{Line: 1}, time.Unix(3, 0)); err != nil {
		t.Fatal(err)
	}
	if p, ok, _ := LoadCursor(dir, "/a"); !ok || p.Line != 1 {
		t.Errorf("書き直した後の LoadCursor = %v, %v", p, ok)
	}

	newer := `{"version": 2, "files": [{"path": "/a", "line": 3, "col": 0, "time": 1}]}`
	write(newer)
	if _, ok, err := LoadCursor(dir, "/a"); ok || !errors.Is(err, ErrStateFormat) {
		t.Errorf("新しい版の LoadCursor = %v, %v", ok, err)
	}
	if err := SaveCursor(dir, "/a", Position{Line: 1}, time.Unix(3, 0)); !errors.Is(err, ErrStateFormat) {
		t.Errorf("新しい版の SaveCursor = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != newer {
		t.Errorf("新しい版のファイルを書き換えた: %s", data)
	}

	// 正しくない UTF-8 のパスは残さない。
	if err := SaveCursor(t.TempDir(), "/\xff", Position{}, time.Unix(1, 0)); err != nil {
		t.Errorf("正しくない UTF-8 のパスの SaveCursor = %v", err)
	}
}
