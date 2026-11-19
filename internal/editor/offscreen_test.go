package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
)

// waitWrite は、fake.write が終わる（出力の欄 write に結果を書く）まで待ち、その結果の行を返す。
func (h *harness) waitWrite() string {
	h.t.Helper()
	var out string
	h.eventually("fake.write の終わり", func() bool {
		out = strings.Join(h.e.outputs[outputKey{"fake", "write"}], "\n")
		return strings.Contains(out, "done")
	})
	return out
}

// tabNames は、client i のタブのファイル名を、並びの順に空白で区切って返す。
func (h *harness) tabNames(i int) string {
	var names []string
	h.e.do(func() {
		for _, bs := range h.e.clients[i].tabs {
			names = append(names, filepath.Base(bs.path))
		}
	})
	return strings.Join(names, " ")
}

// 拡張機能が、どのタブにもなっていないファイルを editor/open（focus が false）で開いて編集し、buffer/save で書き出すと、
// タブを増やさずに書き出し、書き出したらすぐ閉じて、変えたファイルの一覧をメッセージの行に出す（FR-109。基本設計 4.4）。
// タブのファイル（a.txt）は、そのタブのバッファを編集して書き出し、閉じない。無いファイル（new.txt）は作る。
//
// req: FR-109
func TestOffscreenBuffer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "x.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	// タブのファイルの保存は「書き込みました」を出すので、一覧を確かめるために先に書く。
	paths := []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "x.txt"), filepath.Join(dir, "new.txt")}
	h.keys(":fake.write " + strings.Join(paths, " ") + "<CR>")
	if out := h.waitWrite(); !strings.Contains(out, "errs=[]") {
		t.Fatalf("fake.write = %s", out)
	}
	h.settle()
	for _, p := range paths {
		want := "written " + filepath.Base(p) + "\n"
		if got, err := os.ReadFile(p); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v, want %q", filepath.Base(p), got, err, want)
		}
	}
	if got := h.tabNames(0); got != "a.txt" {
		t.Errorf("タブ = %q, want a.txt だけ", got)
	}
	h.e.do(func() {
		for _, p := range paths[1:] {
			if h.e.findPath(p) != nil {
				t.Errorf("%s のバッファが残った", filepath.Base(p))
			}
		}
		if bs := h.e.findPath(paths[0]); bs == nil || bs.buf.Modified() {
			t.Errorf("a.txt のバッファ = %v, want 開いたまま、保存済み", bs)
		}
	})
	if got, want := h.message(), "拡張機能 fake が 2 個のファイルを変えました: x.txt、new.txt"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	// 拡張機能へは、開いたときの buffer/open と、閉じたときの buffer/close を送る。
	h.keys(":fake.bufs<CR>")
	h.eventually("fake.bufs", func() bool { return strings.HasPrefix(h.e.message.Text, "bufs:") })
	got := h.message()
	if !strings.Contains(got, "open 2 x.txt, close 2, open 3 new.txt") || !strings.HasSuffix(got, "close 3") {
		t.Errorf("buffer/open と buffer/close の記録 = %q", got)
	}
}

// 変えたファイルの一覧が画面の幅に収まらなければ、件数だけを出す（FR-109。文言は仮）。
//
// req: FR-109
func TestOffscreenBufferLongList(t *testing.T) {
	long := strings.Repeat("n", 30)
	files := map[string]string{"a.txt": "alpha\n"}
	var paths []string
	for _, c := range []string{"1", "2", "3"} {
		name := long + c + ".txt"
		files[name] = "old\n"
		paths = append(paths, name)
	}
	dir := writeFiles(t, files)
	for i, p := range paths {
		paths[i] = filepath.Join(dir, p)
	}
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(":fake.write " + strings.Join(paths, " ") + "<CR>")
	h.waitWrite()
	h.settle()
	if got, want := h.message(), "拡張機能 fake が 3 個のファイルを変えました"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// 画面に出さないバッファを書き出すときに作ったディレクトリ（FR-78）は、変えたファイルの一覧に添え、メッセージの履歴にも残す
// （PR #240 のレビューの必須 2）。
//
// req: FR-78, FR-109
func TestOffscreenBufferMakesDir(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	sub := filepath.Join(dir, "newdir", "sub")
	h.keys(":fake.write " + filepath.Join(sub, "n.txt") + "<CR>")
	if out := h.waitWrite(); !strings.Contains(out, "errs=[]") {
		t.Fatalf("fake.write = %s", out)
	}
	h.settle()
	if got, want := h.message(), "拡張機能 fake が 1 個のファイルを変えました: n.txt（ディレクトリ \"newdir/sub\" を作りました）"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	var history []string
	h.e.do(func() {
		for _, m := range h.e.msgHistory {
			history = append(history, m.Text)
		}
	})
	if want := writtenText(filepath.Join(sub, "n.txt"), sub); !slices.Contains(history, want) {
		t.Errorf("履歴 = %q, want %q を含む", history, want)
	}
}

// 画面に出さないバッファの保存が失敗したら、変更を失わないようにタブの無いバッファとして残し、そのことと開き方を知らせる。
// 外で変わったファイルへの保存を断ったときの案内は、:w! の前に :b でタブにする形にする（PR #240 のレビューの推奨 3、4）。
//
// req: FR-109
func TestOffscreenBufferSaveFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root は書き込みの権限の無いディレクトリにも書ける")
	}
	// ro は書き込みの権限の無いディレクトリで、その中に無い x.txt は作れない。
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "ro/keep.txt": "", "y.txt": "y\n"})
	ro := filepath.Join(dir, "ro")
	if err := os.Chmod(ro, 0o500); err != nil { //nolint:gosec // ディレクトリなので実行の権限が要る
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // 同上
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(":fake.write " + filepath.Join(ro, "x.txt") + "<CR>")
	h.waitWrite()
	h.settle()
	var id ext.BufferID
	h.e.do(func() {
		if bs := h.e.findPath(filepath.Join(ro, "x.txt")); bs != nil && bs.buf.Modified() {
			id = bs.id
		}
	})
	if id == 0 {
		t.Fatalf("x.txt のバッファが、変更ありで残っていない（message=%q）", h.message())
	}
	if got, want := h.message(), fmt.Sprintf("（タブの無いバッファ %d として残しました。:b %d で開けます）", id, id); !strings.HasPrefix(got, "E212") || !strings.HasSuffix(got, want) {
		t.Errorf("message = %q, want E212 … %q", got, want)
	}

	// 外で変わったファイル。
	y := filepath.Join(dir, "y.txt")
	h.e.do(func() {
		bs, err := h.e.openFile(y, false)
		if err != nil {
			t.Fatal(err)
		}
		bs.offscreen, id = true, bs.id
	})
	if err := os.WriteFile(y, []byte("changed outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.e.do(func() { h.e.outputs = map[outputKey][]string{} })
	h.keys(":fake.write " + y + "<CR>")
	h.waitWrite()
	h.settle()
	if got, want := h.message(), fmt.Sprintf("(:b %d で開いてから :w! で上書きします)", id); !strings.HasSuffix(got, want) {
		t.Errorf("message = %q, want …%q", got, want)
	}
}

// 編集を禁止したパスのファイルでは、拡張機能の編集が断られるので、変更の無い画面に出さないバッファを残さずに閉じる（PR #240 のレビューの推奨 5）。
//
// req: FR-109, FR-120
func TestOffscreenBufferReadOnlyPath(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "node_modules/x.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), ReadOnlyPaths: []string{"node_modules"}}, "a.txt")
	x := filepath.Join(dir, "node_modules", "x.txt")
	h.keys(":fake.write " + x + "<CR>")
	if out := h.waitWrite(); !strings.Contains(out, "buffer/edit") {
		t.Fatalf("fake.write = %s, want buffer/edit の誤り", out)
	}
	h.settle()
	h.e.do(func() {
		if h.e.findPath(x) != nil {
			t.Error("編集を断った x.txt のバッファが残った")
		}
	})
}

// 中身がファイルと同じ画面に出さないバッファへの buffer/save は、書き出さずに閉じ、変えたファイルの一覧にも入れない
// （LSP の名前の変更を取り消したとき。#237）。editor/state は、編集の禁止と外での変更をバッファごとに答える。
//
// req: FR-109
func TestOffscreenBufferUnchanged(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "x.txt": "x\n", "ro/y.txt": "y\n"})
	x := filepath.Join(dir, "x.txt")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(x, old, old); err != nil {
		t.Fatal(err)
	}
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), ReadOnlyPaths: []string{"ro"}}, "a.txt")
	h.keys(":fake.touch " + x + "<CR>")
	if out := h.waitWrite(); !strings.Contains(out, "err=<nil>") {
		t.Fatalf("fake.touch = %s", out)
	}
	h.settle()
	h.e.do(func() {
		if h.e.findPath(x) != nil {
			t.Error("x.txt のバッファが残った")
		}
	})
	if st, err := os.Stat(x); err != nil || !st.ModTime().Equal(old) {
		t.Errorf("x.txt を書き出した: %v, %v", st.ModTime(), err)
	}
	if got := h.message(); strings.Contains(got, "ファイルを変えました") {
		t.Errorf("message = %q, want 一覧を出さない", got)
	}

	y := filepath.Join(dir, "ro", "y.txt")
	h.e.do(func() {
		if _, err := h.e.openFile(y, false); err != nil {
			t.Fatal(err)
		}
		if _, err := h.e.openFile(x, false); err != nil {
			t.Fatal(err)
		}
	})
	if err := os.WriteFile(x, []byte("changed outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.e.do(func() {
		for _, b := range h.e.editorState().Buffers {
			switch b.Path {
			case y:
				if !b.ReadOnly || b.ChangedOnDisk {
					t.Errorf("y.txt = %+v, want readOnly", b)
				}
			case x:
				if b.ReadOnly || !b.ChangedOnDisk {
					t.Errorf("x.txt = %+v, want changedOnDisk", b)
				}
			}
		}
	})
}

// 画面に出さないバッファを利用者が :e でタブにしたら、ふつうのタブのバッファになり、拡張機能の保存の後も閉じない（基本設計 4.4。仮）。
//
// req: FR-109
func TestOffscreenBufferOpenedByUser(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "x.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	x := filepath.Join(dir, "x.txt")
	h.e.do(func() {
		bs, err := h.e.openFile(x, false)
		if err != nil {
			t.Fatal(err)
		}
		bs.offscreen = true
	})
	h.keys(":e " + x + "<CR>:b1<CR>")
	h.settle()
	h.e.do(func() {
		if bs := h.e.findPath(x); bs == nil || bs.offscreen || bs.owner == nil {
			t.Errorf("x.txt のバッファ = %+v, want タブにして offscreen を下ろす", bs)
		}
	})
	h.keys(":fake.write " + x + "<CR>")
	h.waitWrite()
	h.settle()
	if got := h.tabNames(0); got != "a.txt x.txt" {
		t.Errorf("タブ = %q, want a.txt x.txt", got)
	}
	if got := h.message(); strings.Contains(got, "ファイルを変えました") {
		t.Errorf("message = %q, want タブのファイルは一覧に出さない", got)
	}
}
