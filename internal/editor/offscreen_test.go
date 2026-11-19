package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
