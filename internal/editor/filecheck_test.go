package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// writeExternal は、エディタの外から path を text に書き換える。
// 更新時刻を読み込んだときより後へ進め、同じ大きさの書き換えも更新時刻の刻みによらず変更として見えるようにする。
func writeExternal(t *testing.T, path, text string) {
	t.Helper()
	fi, err := os.Stat(path)
	var mtime time.Time
	if err == nil {
		mtime = fi.ModTime()
	} else {
		mtime = time.Now()
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	mtime = mtime.Add(2 * time.Second)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (h *harness) promptText() string {
	var s string
	h.e.do(func() {
		if h.e.prompt != nil {
			s = h.e.prompt.text
		}
	})
	return s
}

// 読み込んだ後に外で書き換えたファイルへの :w は、Vim と同じく書き込むかを問い、n なら書かず、y なら書く。
// :w! は問わずに上書きする。:wq は y と答えると書いてから終わる。
//
// req: FR-08
func TestWriteChangedOnDisk(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: -1}, "a.txt")

	writeExternal(t, path, "X\n")
	h.keys("ix<Esc>:w<CR>")
	h.settle()
	if p := h.promptText(); !strings.HasPrefix(p, "警告: 読み込んだ後にファイルが変更されています") {
		t.Fatalf("問いを出さない: prompt = %q, message = %q", p, h.message())
	}
	// 答えでないキーは捨て、問いを続ける。
	h.keys("q")
	h.settle()
	if h.promptText() == "" {
		t.Fatal("答えでないキーで問いが消えた")
	}
	h.keys("n")
	h.settle()
	if got := readFile(t, path); got != "X\n" {
		t.Fatalf("n で書いた: %q", got)
	}
	if h.promptText() != "" {
		t.Fatal("n の後も問いが残る")
	}

	h.keys(":w<CR>y")
	h.settle()
	if got := readFile(t, path); got != "xa\n" {
		t.Fatalf("y で書かない: %q", got)
	}
	// 書いた後は、外で変わっていないので問わない。
	h.keys("ix<Esc>:w<CR>")
	h.settle()
	if p := h.promptText(); p != "" || readFile(t, path) != "xxa\n" {
		t.Fatalf("書いた後の :w: prompt = %q, file = %q", p, readFile(t, path))
	}

	writeExternal(t, path, "Y\n")
	h.keys("ix<Esc>:w!<CR>")
	h.settle()
	if p := h.promptText(); p != "" || readFile(t, path) != "xxxa\n" {
		t.Fatalf(":w!: prompt = %q, file = %q", p, readFile(t, path))
	}

	writeExternal(t, path, "Z\n")
	h.keys("ix<Esc>:wq<CR>")
	h.settle()
	select {
	case err := <-h.done:
		t.Fatalf(":wq が問わずに終わった: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	h.keys("y")
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(":wq に y と答えても終わらない")
	}
	h.done <- nil
	if got := readFile(t, path); got != "xxxxa\n" {
		t.Errorf(":wq: file = %q", got)
	}
}

// 新しいファイルの編集中に外でそのファイルが作られたら、:w は Vim 9.1 と同じく E13 で失敗し、:w! で上書きする。
//
// req: FR-08
func TestWriteCreatedOnDisk(t *testing.T) {
	dir := writeFiles(t, nil)
	path := filepath.Join(dir, "n.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: -1}, "n.txt")
	writeExternal(t, path, "bye\n")
	h.keys("ix<Esc>:w<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E13") {
		t.Fatalf("message = %q", msg)
	}
	h.keys(":w!<CR>")
	h.settle()
	if got := readFile(t, path); got != "x\n" {
		t.Errorf(":w!: file = %q", got)
	}
}

// 未変更のバッファのファイルが外で変わったら、既定（autoread が有効）では問わずに読み直す。
// 読み直した後の :w は問わない（Vim 9.1 で autoread の読み直しの後の :w と同じ）。u で読み直す前へ戻れる。
//
// req: FR-08
func TestAutoRead(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond}, "a.txt")
	writeExternal(t, path, "agent\nwrote\n")
	h.eventually("読み直し", func() bool { return h.e.cur.buf.Text() == "agent\nwrote" })
	if msg := h.message(); !strings.Contains(msg, "読み直しました") {
		t.Errorf("message = %q", msg)
	}
	var modified bool
	h.e.do(func() { modified = h.e.cur.buf.Modified() })
	if modified {
		t.Error("読み直した後に未保存の変更がある")
	}
	h.keys("x:w<CR>")
	h.settle()
	if p := h.promptText(); p != "" || readFile(t, path) != "gent\nwrote\n" {
		t.Fatalf("読み直した後の :w: prompt = %q, file = %q", p, readFile(t, path))
	}
	h.keys("uu")
	h.settle()
	if got := h.text(); got != "a\nb" {
		t.Errorf("u: text = %q", got)
	}
}

// 確かめるのは、ノーマルモードで入力の途中でないときだけである。インサートモードの間は読み直さず、<Esc> の後に読み直す。
func TestAutoReadWaitsForNormal(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond}, "a.txt")
	h.keys("i")
	h.settle()
	writeExternal(t, path, "new\n")
	time.Sleep(100 * time.Millisecond)
	if got := h.text(); got != "a" {
		t.Fatalf("インサートモードの間に読み直した: %q", got)
	}
	h.keys("<Esc>")
	h.eventually("<Esc> の後の読み直し", func() bool { return h.e.cur.buf.Text() == "new" })
}

// 変更のあるバッファのファイルが外で変わったら、autoread によらず W12 を問う。
// OK と答えると同じ変更をもう問わないが、:w はなお書き込むかを問う（Vim 9.1 と同じ）。(L)oad File と答えると読み直す。
//
// req: FR-08
func TestW12(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond}, "a.txt")
	h.keys("x")
	h.settle()
	writeExternal(t, path, "Y\n")
	h.eventually("W12", func() bool { return h.e.prompt != nil && strings.HasPrefix(h.e.prompt.text, "W12") })
	h.keys("o")
	h.settle()
	time.Sleep(50 * time.Millisecond)
	if p := h.promptText(); p != "" {
		t.Fatalf("OK の後にまた問う: %q", p)
	}
	if got := h.text(); got != "" {
		t.Fatalf("OK で読み直した: %q", got)
	}
	h.keys(":w<CR>")
	h.settle()
	if p := h.promptText(); !strings.HasPrefix(p, "警告") {
		t.Fatalf("W12 に OK と答えた後の :w が問わない: %q", p)
	}
	h.keys("n")
	h.settle()

	writeExternal(t, path, "Z\nz\n")
	h.eventually("2 回目の W12", func() bool { return h.e.prompt != nil && strings.HasPrefix(h.e.prompt.text, "W12") })
	h.keys("l")
	h.settle()
	if got := h.text(); got != "Z\nz" {
		t.Fatalf("(L)oad File: text = %q", got)
	}
	h.keys("x:w<CR>")
	h.settle()
	if p := h.promptText(); p != "" || readFile(t, path) != "\nz\n" {
		t.Fatalf("読み直した後の :w: prompt = %q, file = %q", p, readFile(t, path))
	}
}

// autoread を無効にすると、未変更のバッファでも W11 を問う。中身が同じなら（更新時刻だけが変わったなら）問わない（Vim 9.1 と同じ）。
// ファイルが消えたら E211 で知らせる。
//
// req: FR-08
func TestW11AndE211(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	no := false
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond, AutoRead: &no}, "a.txt")
	writeExternal(t, path, "a\n")
	time.Sleep(100 * time.Millisecond)
	if p := h.promptText(); p != "" {
		t.Fatalf("中身が同じなのに問う: %q", p)
	}
	writeExternal(t, path, "b\n")
	h.eventually("W11", func() bool { return h.e.prompt != nil && strings.HasPrefix(h.e.prompt.text, "W11") })
	h.keys("<CR>")
	h.settle()
	if got := h.text(); got != "a" {
		t.Fatalf("OK で読み直した: %q", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	h.eventually("E211", func() bool { return strings.HasPrefix(h.e.message.Text, "E211") })
}

// 拡張機能の buffer/save は、外で変わったファイルへ書き出さずに断り（saved が false）、利用者に知らせる。
//
// req: FR-08
func TestBufferSaveChangedOnDisk(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: -1, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\d`)
	h.eventually("fake の起動", func() bool { return h.state("fake") == exthost.StateRunning })
	h.keys("ix<Esc>")
	h.settle()
	writeExternal(t, path, "X\n")
	h.keys(`\w`)
	h.eventually("断りの表示", func() bool { return strings.HasPrefix(h.e.message.Text, "saved=false") })
	if got := readFile(t, path); got != "X\n" {
		t.Fatalf("buffer/save が上書きした: %q", got)
	}
	h.keys(":w!<CR>")
	h.settle()
	h.keys(`ix<Esc>\w`)
	h.eventually("保存", func() bool { return strings.HasPrefix(h.e.message.Text, "saved=true") })
	if got := readFile(t, path); got != "xxa\n" {
		t.Errorf("file = %q", got)
	}
}
