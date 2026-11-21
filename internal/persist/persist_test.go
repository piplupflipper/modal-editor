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
	Version int
	Files   []cursorEntry
}

// readTestState は、state.json のカーソルの位置の項目を読む。
func readTestState(t *testing.T, dir string) testState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Cursors struct {
			Version int           `json:"version"`
			Data    []cursorEntry `json:"data"`
		} `json:"cursors"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return testState{Version: st.Cursors.Version, Files: st.Cursors.Data}
}

// Editor.Close のように、まとめて残す位置は 1 回の書き換えで入り、前のものほど新しい記録として並ぶ。
//
// req: FR-166
func TestSaveCursors(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCursor(dir, "/a", Position{Line: 1}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	cs := []Cursor{{Path: "/b", Pos: Position{Line: 2}}, {Path: "/a", Pos: Position{Line: 3, Byte: 4}}, {Path: "/\xff"}, {Path: "/b", Pos: Position{Line: 9}}}
	if err := SaveCursors(dir, cs, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	st := readTestState(t, dir)
	if len(st.Files) != 2 || st.Files[0].Path != "/b" || st.Files[0].Line != 3 || st.Files[1].Path != "/a" || st.Files[1].Line != 4 || st.Files[1].Col != 4 {
		t.Errorf("状態のファイル %+v", st)
	}
	if err := SaveCursors(t.TempDir(), nil, time.Unix(1, 0)); err != nil {
		t.Errorf("空の SaveCursors = %v", err)
	}
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

// 知らない名前の項目と、記録の知らない項目は、書き換えた後も残る。壊れた状態のファイルと、古い版や壊れた項目は、捨てて書き直す。
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
	write(`{"registers": {"version": 1, "data": {"a": "eA=="}}, "cursors": {"version": 1, "data": [{"path": "/a", "line": 3, "col": 0, "time": 1, "marks": {"x": 1}}]}}`)
	if err := SaveCursor(dir, "/a", Position{Line: 5}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{`"registers"`, `"eA=="`, `"marks"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("書き換えた後に %s が消えた: %s", want, data)
		}
	}

	for name, s := range map[string]string{
		"JSON でない":  `not json`,
		"古い版の項目":    `{"cursors": {"version": 0, "data": [{"path": "/a", "line": 3}]}}`,
		"中身の形が違う項目": `{"cursors": {"version": 1, "data": {"path": "/a"}}}`,
	} {
		write(s)
		if _, ok, _ := LoadCursor(dir, "/a"); ok {
			t.Errorf("%s: LoadCursor が記録を返した", name)
		}
		if err := SaveCursor(dir, "/a", Position{Line: 1}, time.Unix(3, 0)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p, ok, _ := LoadCursor(dir, "/a"); !ok || p.Line != 1 {
			t.Errorf("%s: 書き直した後の LoadCursor = %v, %v", name, p, ok)
		}
	}

	// 正しくない UTF-8 のパスは残さない。
	if err := SaveCursor(t.TempDir(), "/\xff", Position{}, time.Unix(1, 0)); err != nil {
		t.Errorf("正しくない UTF-8 のパスの SaveCursor = %v", err)
	}
}

// 版の違う本体の server が同じ state.json を交互に書いても、互いの項目を消さない。
// 古い本体は、新しい版の項目を読まず、書き換えずに残し、ErrNewerItem を返す（呼び出し側は警告にしない）。
// 新しい本体が足した知らない名前の項目は、古い本体が書いても残る。
//
// req: FR-166
func TestStateMixedVersions(t *testing.T) {
	dir := t.TempDir()
	// 新しい本体が、知らない名前の項目 history（版 3）と、版 2 のカーソルの位置の項目を書いた。
	newer := func() {
		t.Helper()
		err := UpdateState(dir, func(st *State) error {
			if err := st.SetItem("history", 3, []string{"ls", "make"}); err != nil {
				return err
			}
			return st.SetItem(CursorsItem, CursorsFormat+1, map[string]int{"newShape": 1})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for round := range 3 {
		newer()
		// 古い本体（この本体）が書く。
		if err := SaveCursor(dir, "/a", Position{Line: round}, time.Unix(int64(round), 0)); !errors.Is(err, ErrNewerItem) {
			t.Fatalf("%d 回目: SaveCursor = %v, want ErrNewerItem", round, err)
		}
		if _, ok, err := LoadCursor(dir, "/a"); ok || !errors.Is(err, ErrNewerItem) {
			t.Errorf("%d 回目: LoadCursor = %v, %v, want ErrNewerItem", round, ok, err)
		}
		st, err := ReadState(dir)
		if err != nil {
			t.Fatal(err)
		}
		var hist []string
		if ok, err := st.Item("history", 3, &hist); !ok || err != nil || len(hist) != 2 {
			t.Errorf("%d 回目: 新しい本体の history が消えた: %v, %v, %v", round, hist, ok, err)
		}
		var shape map[string]int
		if ok, err := st.Item(CursorsItem, CursorsFormat+1, &shape); !ok || err != nil || shape["newShape"] != 1 {
			t.Errorf("%d 回目: 新しい版のカーソルの位置の項目が書き換わった: %v, %v, %v", round, shape, ok, err)
		}
	}

	// 新しい本体が history だけを足し、カーソルの位置の項目は同じ版で書くなら、古い本体と新しい本体が交互に書いても、
	// どちらの項目も残り、古い本体は誤りを返さない。
	dir2 := t.TempDir()
	for round := range 3 {
		err := UpdateState(dir2, func(st *State) error {
			var hist []string
			if _, err := st.Item("history", 3, &hist); err != nil {
				return err
			}
			return st.SetItem("history", 3, append(hist, fmt.Sprint(round)))
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := SaveCursor(dir2, fmt.Sprintf("/f%d", round), Position{Line: round}, time.Unix(int64(round), 0)); err != nil {
			t.Fatalf("%d 回目: SaveCursor = %v", round, err)
		}
	}
	st, err := ReadState(dir2)
	if err != nil {
		t.Fatal(err)
	}
	var hist []string
	if ok, _ := st.Item("history", 3, &hist); !ok || len(hist) != 3 {
		t.Errorf("history = %v, want 3 件", hist)
	}
	for round := range 3 {
		if p, ok, err := LoadCursor(dir2, fmt.Sprintf("/f%d", round)); !ok || err != nil || p.Line != round {
			t.Errorf("/f%d: %v, %v, %v", round, p, ok, err)
		}
	}
}
