package editor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/persist"
)

// persistConfig は、状態の置き場を stateDir にし、アンドゥの履歴（undo）とカーソルの位置（cursor）を残すかを決めた設定を返す。
func persistConfig(dir, stateDir string, undo, cursor bool) Config {
	return Config{WorkDir: dir, StateDir: stateDir, Options: map[string]option.Value{
		option.UndoPersist:   option.BoolValue(undo),
		option.CursorRestore: option.BoolValue(cursor),
	}}
}

// quit は、:q などで Run が戻るのを待ち、後始末（t.Cleanup）が待つ値を戻す。
func (h *harness) quit(keys string) {
	h.t.Helper()
	h.keys(keys)
	select {
	case err := <-h.done:
		if err != nil {
			h.t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		h.t.Fatalf("%s で終わらない", keys)
	}
	h.done <- nil
}

func (h *harness) cursor() buffer.Position {
	var p buffer.Position
	h.e.do(func() { p = h.e.cur.state.Cursor() })
	return p
}

func (h *harness) canUndo() (undo, redo bool) {
	h.e.do(func() { undo, redo = h.e.cur.buf.CanUndo(), h.e.cur.buf.CanRedo() })
	return undo, redo
}

// undoState は、アンドゥの 1 回ごとのテキストとカーソルである。
type undoState struct {
	text   string
	cursor buffer.Position
}

// undoAll は、取り消せなくなるまで u を打ち、各回の後のテキストとカーソルを返す。
func (h *harness) undoAll() []undoState {
	h.t.Helper()
	var out []undoState
	for range 100 {
		if u, _ := h.canUndo(); !u {
			return out
		}
		h.keys("u")
		h.settle()
		out = append(out, undoState{h.text(), h.cursor()})
	}
	h.t.Fatal("u が 100 回で終わらない")
	return nil
}

// 保存して閉じたファイルを開き直すと、u と <C-r>（U）で、閉じる前と同じテキストとカーソルへ戻れる。
// 履歴には、ビジュアルモードの編集、マクロの記録と再生、. の繰り返しを含める。
//
// req: FR-165
func TestUndoPersistAcrossSessions(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\nfour\nfive\n"})
	stateDir := filepath.Join(t.TempDir(), "state", "vi-imitation")
	path := filepath.Join(dir, "a.txt")

	h := start(t, persistConfig(dir, stateDir, true, false), path)
	h.keys("vlld")         // ビジュアルモードの削除
	h.keys("jqaA!<Esc>jq") // マクロの記録
	h.keys("@a")           // マクロの再生
	h.keys("jx.")          // . の繰り返し
	h.keys("ggOtop<Esc>")  // 挿入
	h.keys(":w<CR>")
	h.settle()
	final := h.text()
	// 閉じる前に、アンドゥでたどるテキストとカーソルを記録し、リドゥで戻す。
	want := h.undoAll()
	if len(want) < 5 {
		t.Fatalf("アンドゥの回数 %d が少ない: %+v", len(want), want)
	}
	for range want {
		h.keys("<C-r>")
	}
	h.settle()
	if h.text() != final {
		t.Fatalf("リドゥで戻した後のテキスト %q, want %q", h.text(), final)
	}
	h.quit(":q<CR>")
	if _, err := os.Stat(persist.UndoPath(stateDir, path)); err != nil {
		t.Fatalf("アンドゥの履歴のファイルが無い: %v", err)
	}

	h2 := start(t, persistConfig(dir, stateDir, true, false), path)
	if got := h2.text(); got != final {
		t.Fatalf("開き直したテキスト %q, want %q", got, final)
	}
	h2.e.do(func() {
		if h2.e.cur.buf.Modified() {
			t.Error("開き直した直後に変更がある")
		}
	})
	if got := h2.undoAll(); !slices.Equal(got, want) {
		t.Errorf("開き直した後のアンドゥ\n got %+v\nwant %+v", got, want)
	}
	// U は <C-r> と同じリドゥ（FR-145）。
	h2.keys(strings.Repeat("U", len(want)))
	h2.settle()
	if got := h2.text(); got != final {
		t.Errorf("U で戻した後のテキスト %q, want %q", got, final)
	}
}

// u の後に保存したリドゥの履歴も残り、開き直した後に <C-r> でやり直せる（Vim 9.1 と Neovim 0.12.5 と同じ）。
//
// req: FR-165
func TestUndoPersistRedo(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n"})
	stateDir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	h := start(t, persistConfig(dir, stateDir, true, false), path)
	h.quit("jddu:wq<CR>")

	h2 := start(t, persistConfig(dir, stateDir, true, false), path)
	if u, r := h2.canUndo(); u || !r {
		t.Fatalf("CanUndo %v、CanRedo %v, want false, true", u, r)
	}
	h2.keys("<C-r>")
	h2.settle()
	if got := h2.text(); got != "one" {
		t.Errorf("<C-r> の後のテキスト %q, want \"one\"", got)
	}
}

// 履歴を書いた後にファイルが外で変わっていれば、開き直しても履歴を読まない。形式の版が違う履歴のファイルも読まない。
//
// req: FR-165
func TestUndoPersistDiscard(t *testing.T) {
	stateDir := t.TempDir()
	write := func(t *testing.T) (string, string) {
		t.Helper()
		dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n"})
		path := filepath.Join(dir, "a.txt")
		h := start(t, persistConfig(dir, stateDir, true, false), path)
		h.quit("ddix<Esc>:wq<CR>")
		return dir, path
	}
	t.Run("外で変わった", func(t *testing.T) {
		dir, path := write(t)
		if err := os.WriteFile(path, []byte("xtwo\nadded\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		h := start(t, persistConfig(dir, stateDir, true, false), path)
		if u, r := h.canUndo(); u || r {
			t.Errorf("外で変わったファイルの CanUndo %v、CanRedo %v", u, r)
		}
		// 履歴の無いまま保存すると、古い履歴のファイルを消す（Vim 9.1 と同じ）。
		h.keys(":w<CR>")
		h.settle()
		if _, err := os.Stat(persist.UndoPath(stateDir, path)); !os.IsNotExist(err) {
			t.Errorf("古い履歴のファイルが残る: %v", err)
		}
	})
	t.Run("版が違う", func(t *testing.T) {
		dir, path := write(t)
		undoFile := persist.UndoPath(stateDir, path)
		data, err := os.ReadFile(undoFile)
		if err != nil {
			t.Fatal(err)
		}
		// 印の直後の 4 バイトが形式の版である。
		i := strings.Index(string(data), "\n") + 4
		data[i] = persist.UndoFormat + 1
		if err := os.WriteFile(undoFile, data, 0o600); err != nil { //nolint:gosec // G703: テストの一時ディレクトリの中の、本体が書いた履歴のファイルを書き換える
			t.Fatal(err)
		}
		h := start(t, persistConfig(dir, stateDir, true, false), path)
		if u, r := h.canUndo(); u || r {
			t.Errorf("版の違う履歴の CanUndo %v、CanRedo %v", u, r)
		}
	})
}

// 拡張機能の保存の前の処理が失敗し、アンドゥの履歴も残せないときは、メッセージの行に両方を出す（PR #326 のレビューの推奨 5）。
//
// req: FR-165
func TestUndoPersistWriteFailsWithExtError(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "willSaveFail\n", "blocker": ""})
	stateDir := filepath.Join(dir, "blocker", "vi-imitation")
	cfg := persistConfig(dir, stateDir, true, false)
	cfg.Extensions = fakeSpecs(t)
	h := start(t, cfg, filepath.Join(dir, "a.go"))
	h.keys(`\d`)
	h.eventually("fake の起動", func() bool { return h.state("fake") == exthost.StateRunning })
	h.keys("A!<Esc>:w<CR>")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "保存の前の処理が失敗しました") || !strings.Contains(msg, "アンドゥの履歴を残せません") {
		t.Errorf("メッセージ %q", msg)
	}
}

// 置き場に書けなくても保存は失敗にせず、メッセージの行に警告を出す。
//
// req: FR-165
func TestUndoPersistWriteFails(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\n", "blocker": ""})
	stateDir := filepath.Join(dir, "blocker", "vi-imitation")
	path := filepath.Join(dir, "a.txt")
	h := start(t, persistConfig(dir, stateDir, true, true), path)
	h.keys("x:w<CR>")
	h.settle()
	if data, _ := os.ReadFile(path); string(data) != "ne\n" {
		t.Errorf("保存した中身 %q", data)
	}
	if msg := h.message(); !strings.Contains(msg, "を書き込みました") || !strings.Contains(msg, "アンドゥの履歴を残せません") {
		t.Errorf("メッセージ %q", msg)
	}
	h.e.do(func() {
		if h.e.cur.buf.Modified() {
			t.Error("保存した後に変更が残る")
		}
	})
	// カーソルの位置を残せないときも、閉じることは止めない。
	h.quit(":q<CR>")
}

// 設定が false なら、保存しても閉じても、置き場に何も作らない。:set undofile（udf）で実行中に切り替えられる。
//
// req: FR-165, FR-166
func TestPersistDisabled(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n"})
	stateDir := filepath.Join(t.TempDir(), "vi-imitation")
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, StateDir: stateDir}, path)
	h.quit("jx:wq<CR>")
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("設定が false なのに置き場がある: %v", err)
	}

	h2 := start(t, Config{WorkDir: dir, StateDir: stateDir}, path)
	h2.keys("x:set udf<CR>:w<CR>")
	h2.settle()
	if _, err := os.Stat(persist.UndoPath(stateDir, path)); err != nil {
		t.Errorf(":set udf の後に保存しても履歴のファイルが無い: %v", err)
	}
	h2.keys(":set noundofile<CR>x:w<CR>")
	h2.settle()
	var v bool
	h2.e.do(func() { v = h2.e.options.Get(option.UndoPersist).Bool() })
	if v {
		t.Error(":set noundofile の後も undo.persist が true")
	}
}

// 名前の無いバッファに名前を付けずに編集しても、履歴を残さない（残す先のファイルが無い）。
// ファイルでないバッファ（:help）も残さない。
//
// req: FR-165, FR-166
func TestPersistSkipsNonFileBuffers(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "vi-imitation")
	h := start(t, persistConfig(dir, stateDir, true, true))
	h.keys("ihello<Esc>:help<CR>")
	h.settle()
	h.quit(":qa!<CR>")
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Errorf("ファイルでないバッファで置き場を作った: %v", err)
	}
}

// 閉じたときのカーソルの位置を残し、次に開いたときにその位置へ置く。
// 列が行の長さを超えるときは行の最後の文字へ寄せ、行が今の行数を超えるときは 1 行目のまま（Vim 9.1 の defaults.vim と同じ）。
//
// req: FR-166
func TestCursorRestore(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree xyz\nfour\n"})
	stateDir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	cfg := persistConfig(dir, stateDir, false, true)

	h := start(t, cfg, path)
	h.quit("3G5l:q<CR>")
	h2 := start(t, cfg, path)
	if got := h2.cursor(); got != (buffer.Position{Line: 2, Byte: 5}) {
		t.Errorf("開き直した後のカーソル %v, want 2:5", got)
	}
	h2.quit(":q<CR>")

	if err := os.WriteFile(path, []byte("one\nt\nthr\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h3 := start(t, cfg, path)
	if got := h3.cursor(); got != (buffer.Position{Line: 2, Byte: 2}) {
		t.Errorf("行が短くなった後のカーソル %v, want 2:2", got)
	}
	h3.quit(":q<CR>")

	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h4 := start(t, cfg, path)
	if got := h4.cursor(); got != (buffer.Position{}) {
		t.Errorf("行数を超える位置のときのカーソル %v, want 0:0", got)
	}
}

// 位置を指定して開くとき（拡張機能の editor/open。定義へ移動など）は、残した位置でなく指定した位置へ置く。
// 起動したときのバッファと別のバッファで開くときも、残した位置へ戻す。
//
// req: FR-166
func TestCursorRestoreWithPosition(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\nd\n", "b.txt": "1\n2\n3\n"})
	stateDir := t.TempDir()
	cfg := persistConfig(dir, stateDir, false, true)
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	h := start(t, cfg, a)
	h.keys(":e " + b + "<CR>2G")
	h.settle()
	h.quit(":qa<CR>")

	h2 := start(t, cfg, a)
	if got := h2.cursor(); got.Line != 0 {
		t.Errorf("a.txt のカーソル %v, want 1 行目（:e で離れたときの位置）", got)
	}
	h2.e.do(func() {
		if _, _, err := h2.e.editorOpen(ext.EditorOpenParams{Path: b, Focus: true, Position: &ext.Position{Line: 2}}); err != nil {
			t.Error(err)
		}
	})
	if got := h2.cursor(); got.Line != 2 {
		t.Errorf("位置を指定して開いた b.txt のカーソル %v, want 2 行目", got)
	}
	h2.keys(":e " + a + "<CR>")
	h2.settle()
	h2.quit(":qa<CR>")

	h3 := start(t, cfg, b)
	if got := h3.cursor(); got.Line != 2 {
		t.Errorf("b.txt のカーソル %v, want 2 行目（閉じたときの位置）", got)
	}
	h3.keys(":tab.new " + a + "<CR>")
	h3.settle()
	if got := h3.cursor(); got.Line != 0 {
		t.Errorf("新しいタブで開いた a.txt のカーソル %v, want 0 行目", got)
	}
}

// git のコミットのメッセージ（ファイルの種類 gitcommit）は、前の位置へ戻さない（Vim 9.1 の defaults.vim と同じ）。
//
// req: FR-166
func TestCursorRestoreSkipsCommitMessage(t *testing.T) {
	dir := writeFiles(t, map[string]string{"COMMIT_EDITMSG": "a\nb\nc\n"})
	stateDir := t.TempDir()
	cfg := persistConfig(dir, stateDir, false, true)
	path := filepath.Join(dir, "COMMIT_EDITMSG")
	h := start(t, cfg, path)
	h.quit("3G:q<CR>")
	h2 := start(t, cfg, path)
	if got := h2.cursor(); got.Line != 0 {
		t.Errorf("COMMIT_EDITMSG のカーソル %v, want 0 行目", got)
	}
}

// 2 つの server（ここでは 2 つのエディタ）が同じ置き場へ書いても、互いの記録を消さない。
//
// req: FR-166
func TestCursorRestoreTwoServers(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "1\n2\n3\n"})
	stateDir := t.TempDir()
	cfg := persistConfig(dir, stateDir, false, true)
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	h1 := start(t, cfg, a)
	h2 := start(t, cfg, b)
	h1.keys("2G")
	h2.keys("3G")
	h1.settle()
	h2.settle()
	h1.quit(":q<CR>")
	h2.quit(":q<CR>")
	for path, line := range map[string]int{a: 1, b: 2} {
		p, ok, err := persist.LoadCursor(stateDir, path)
		if !ok || err != nil || p.Line != line {
			t.Errorf("%s: %v, %v, %v; want 行 %d", filepath.Base(path), p, ok, err, line)
		}
	}
}

// タブを閉じずに終わったとき（端末が切れた、tmux の window を閉じた、server へのシグナル）も、Editor.Close が
// 開いていたファイルのカーソルの位置をまとめて残す。
//
// req: FR-166
func TestCursorRestoreOnClose(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "1\n2\n3\n4\n"})
	stateDir := t.TempDir()
	cfg := persistConfig(dir, stateDir, false, true)
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	// t.Cleanup は後に登録したものから動くので、この確認はハーネスの後始末（Run を止めて Close を呼ぶ）の後に動く。
	t.Cleanup(func() {
		for path, want := range map[string]persist.Position{a: {Line: 1, Byte: 0}, b: {Line: 3, Byte: 0}} {
			p, ok, err := persist.LoadCursor(stateDir, path)
			if !ok || err != nil || p != want {
				t.Errorf("%s: %v, %v, %v; want %v", filepath.Base(path), p, ok, err, want)
			}
		}
	})
	h := start(t, cfg, a, b)
	h.keys("4G<S-Tab>2G")
	h.settle()
	h.e.do(func() {
		if h.e.cur.path != a {
			t.Errorf("<S-Tab> の後のバッファ %q, want a.txt", h.e.cur.path)
		}
	})
	if _, err := os.Stat(filepath.Join(stateDir, persist.StateFileName)); !os.IsNotExist(err) {
		t.Errorf("閉じる前に状態のファイルがある: %v", err)
	}
}

// 新しい版の本体が書いたカーソルの位置の項目は、書き換えずに残し、タブを閉じても警告を出さない。
//
// req: FR-166
func TestCursorRestoreNewerItem(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "1\n"})
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, persist.StateFileName)
	newer := `{"cursors": {"version": 99, "data": {"shape": "new"}}, "history": {"version": 1, "data": ["ls"]}}`
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, persistConfig(dir, stateDir, false, true), filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt"))
	h.keys("3G:q<CR>")
	h.settle()
	if msg := h.message(); strings.Contains(msg, "カーソルの位置を残せません") {
		t.Errorf("新しい版の項目で警告を出した: %q", msg)
	}
	if data, _ := os.ReadFile(path); string(data) != newer {
		t.Errorf("新しい版の項目を書き換えた: %s", data)
	}
}
