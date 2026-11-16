package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// reloadEdits の編集を適用すると読み直した内容になり、編集は変わった行の範囲だけを置き換える。
func TestReloadEdits(t *testing.T) {
	tests := []struct {
		cur, next string
		// changed は、編集が置き換える前のテキスト（cur の範囲）と、入れるテキストの長さの和の上限である。
		changed int
	}{
		{"a\nb\nc", "a\nb\nc", 0},
		{"a\nb\nc", "a\nB\nc", 2},
		{"a\nb\nc", "a\nb\nc\nd", 2},
		{"a\nb\nc", "z\na\nb\nc", 2},
		{"a\nb\nc", "a\nx\ny\nb\nc", 4},
		{"a\nb\nc", "a\nc", 2},
		{"a\nb\nc", "b\nc", 2},
		{"a\nb\nc", "a\nb", 2},
		{"a\nb\nc", "x", 6},
		{"", "x\ny", 3},
		{"x\ny", "", 3},
		{"a\na\na", "a\na", 2},
		{"a\na", "a\na\na", 2},
	}
	for _, tt := range tests {
		cur, next := buffer.New(tt.cur), buffer.New(tt.next)
		eds := reloadEdits(cur, next)
		size := 0
		for _, ed := range eds {
			s, err := cur.Slice(ed.Range)
			if err != nil {
				t.Fatalf("%q → %q: %v", tt.cur, tt.next, err)
			}
			size += len(s) + len(ed.Text)
		}
		if _, err := cur.Apply(buffer.Transaction{BaseVersion: cur.Version(), Edits: eds}); err != nil {
			t.Fatalf("%q → %q: %v", tt.cur, tt.next, err)
		}
		if got := cur.Text(); got != tt.next {
			t.Errorf("%q → %q: text = %q", tt.cur, tt.next, got)
		}
		if size > tt.changed {
			t.Errorf("%q → %q: 編集の大きさ %d > %d（%v）", tt.cur, tt.next, size, tt.changed, eds)
		}
	}
}

// 読み直しは変わった行だけを置き換えるので、変わっていない行の印と診断は動かない。
// 末尾の改行の有無が違うファイルの読み直しも、u 1 回で末尾の改行を含めて読み直す前へ戻る（Vim 9.1 と同じ）。
//
// req: FR-08
func TestReloadKeepsSignsAndUndo(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "l0\nl1\nl2\nl3\nl4\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond}, "a.txt")
	h.e.do(func() {
		h.e.cur.signs["x"] = []ext.Sign{{Line: 3}}
		h.e.cur.diagnostics["x"] = []ext.Diagnostic{{Range: ext.Range{Start: ext.Position{Line: 4}, End: ext.Position{Line: 4, Byte: 2}}}}
	})
	writeExternal(t, path, "AGENT\nl1\nl2\nl3\nl4\n")
	h.eventually("読み直し", func() bool { return h.e.cur.buf.Text() == "AGENT\nl1\nl2\nl3\nl4" })
	h.e.do(func() {
		if s := h.e.cur.signs["x"]; len(s) != 1 || s[0].Line != 3 {
			t.Errorf("印 = %+v", s)
		}
		if d := h.e.cur.diagnostics["x"]; len(d) != 1 || d[0].Range.Start.Line != 4 || d[0].Range.End.Line != 4 {
			t.Errorf("診断 = %+v", d)
		}
		last := h.e.cur.history[len(h.e.cur.history)-1]
		if len(last.Edits) != 1 || last.Edits[0].Text != "AGENT" {
			t.Errorf("読み直しの編集 = %+v", last.Edits)
		}
	})

	// 利用者の変更（dd と x）の後に、末尾の改行の無いファイルを読み直す。
	h.keys("ddx")
	h.settle()
	writeExternal(t, path, "AGENT")
	h.eventually("W12 の記録", func() bool { return strings.HasPrefix(h.e.cur.notice, "W12") })
	h.keys(":checktime<CR>")
	h.eventually("W12", func() bool { return h.e.prompt != nil })
	h.keys("l")
	h.settle()
	var eol bool
	h.e.do(func() { eol = h.e.cur.buf.HasTrailingNewline() })
	if got := h.text(); got != "AGENT" || eol {
		t.Fatalf("(L)oad File: text = %q, eol = %v", got, eol)
	}
	h.keys("u")
	h.settle()
	h.e.do(func() { eol = h.e.cur.buf.HasTrailingNewline() })
	if got := h.text(); got != "1\nl2\nl3\nl4" || !eol {
		t.Errorf("u: text = %q, eol = %v", got, eol)
	}
}

// 自分の :w の直後に定期的な確認が走っても、外での変更として扱わない。シンボリックリンク越しに開いたファイル
// （書き出しはリンクの先を置き換える）でも同じである。
func TestNoFalseChangeAfterWrite(t *testing.T) {
	dir := writeFiles(t, map[string]string{"real.txt": "aaaa\n", "target.txt": "bbbb\n"})
	if err := os.Symlink(filepath.Join(dir, "target.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	h := start(t, Config{WorkDir: dir, CheckInterval: 5 * time.Millisecond}, "real.txt", "link.txt")
	for range 3 {
		for _, name := range []string{"real.txt", "link.txt"} {
			h.keys(":e " + name + "<CR>x:w<CR>")
			h.settle()
			time.Sleep(30 * time.Millisecond)
			if p, n, msg := h.promptText(), h.noticeOf(), h.message(); p != "" || n != "" || !strings.Contains(msg, "書き込みました") {
				t.Fatalf("%s の :w の後: prompt = %q, notice = %q, message = %q", name, p, n, msg)
			}
		}
	}
}

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

// noticeOf は、カーソルのあるバッファに記録した、まだ問うていない外での変更の問いを返す。
func (h *harness) noticeOf() string {
	var s string
	h.e.do(func() { s = h.e.cur.notice })
	return s
}

// 変更のあるバッファのファイルが外で変わったら、autoread によらず W12 を記録して警告を出す。
// 定期的な確認では問わず（打っている途中のキーを答えに取らない）、:checktime で問う。
// OK と答えると同じ変更をもう問わないが、:w はなお書き込むかを問う（Vim 9.1 と同じ）。(L)oad File と答えると読み直す。
//
// req: FR-08
func TestW12(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond}, "a.txt")
	h.keys("x")
	h.settle()
	writeExternal(t, path, "Y\nb\n")
	h.eventually("W12 の記録", func() bool { return strings.HasPrefix(h.e.cur.notice, "W12") })
	if p := h.promptText(); p != "" {
		t.Fatalf("定期的な確認で問うた: %q", p)
	}
	if msg := h.message(); !strings.HasPrefix(msg, "W12") {
		t.Errorf("警告を出さない: %q", msg)
	}
	// 記録の後に打つキーは、問いの答えではなくノーマルモードのコマンドである（PR #149 のレビューの再現）。
	h.keys("odd text<Esc>")
	h.settle()
	if got := h.text(); got != "\ndd text\nb" {
		t.Fatalf("記録の後のキー: text = %q", got)
	}
	h.keys(":checktime<CR>")
	h.settle()
	if p := h.promptText(); !strings.HasPrefix(p, "W12") {
		t.Fatalf(":checktime で問わない: %q", p)
	}
	h.keys("o")
	h.settle()
	h.keys(":checkt<CR>")
	h.settle()
	time.Sleep(50 * time.Millisecond)
	if p := h.promptText(); p != "" {
		t.Fatalf("OK の後にまた問う: %q", p)
	}
	if got := h.text(); got != "\ndd text\nb" {
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
	h.eventually("2 回目の W12 の記録", func() bool { return strings.HasPrefix(h.e.cur.notice, "W12") })
	h.keys(":checktime<CR>")
	h.eventually("2 回目の W12", func() bool { return h.e.prompt != nil })
	h.keys("l")
	h.settle()
	if got := h.text(); got != "Z\nz" {
		t.Fatalf("(L)oad File: text = %q", got)
	}
	// カーソルは読み直す前の 2 行目に残る（Vim の :e と同じ）。
	h.keys("x:w<CR>")
	h.settle()
	if p := h.promptText(); p != "" || readFile(t, path) != "Z\n\n" {
		t.Fatalf("読み直した後の :w: prompt = %q, file = %q", p, readFile(t, path))
	}
}

// 記録した問いは、拡張機能のコマンドの後に出す（Vim がシェルのコマンドの後に確かめるのと同じ時点）。
// Vim の (a)（Load File and Options）は l と同じく読み直す。
//
// req: FR-08
func TestNoticeAfterExtCommand(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: 10 * time.Millisecond, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\d`)
	h.eventually("fake の起動", func() bool { return h.state("fake") == exthost.StateRunning })
	h.keys("x")
	h.settle()
	writeExternal(t, path, "Y\n")
	h.eventually("W12 の記録", func() bool { return strings.HasPrefix(h.e.cur.notice, "W12") })
	h.keys(`\d`)
	h.eventually("拡張機能のコマンドの後の問い", func() bool { return h.e.prompt != nil && strings.HasPrefix(h.e.prompt.text, "W12") })
	h.keys("a")
	h.settle()
	if got := h.text(); got != "Y" {
		t.Fatalf("(a): text = %q", got)
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
	if n := h.noticeOf(); n != "" {
		t.Fatalf("中身が同じなのに記録した: %q", n)
	}
	writeExternal(t, path, "b\n")
	h.eventually("W11 の記録", func() bool { return strings.HasPrefix(h.e.cur.notice, "W11") })
	h.keys(":checktime<CR>")
	h.settle()
	if p := h.promptText(); !strings.HasPrefix(p, "W11") {
		t.Fatalf(":checktime で問わない: %q", p)
	}
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
