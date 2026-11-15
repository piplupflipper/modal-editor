package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// writeFiles は、作業ディレクトリを作り、files（名前から内容）を置いて、そのパスを返す。
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// キー列を入れると、バッファが変わり、画面の格子に描かれる。コマンドラインの行の編集は command.State が持ち、
// マクロにはコマンドラインのキーも記録される。
//
// req: FR-01, FR-05
func TestKeysAndScreen(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "hello\nworld\nzzz\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys("xjdd")
	h.settle()
	if got := h.text(); got != "ello\nzzz" {
		t.Fatalf("text = %q", got)
	}
	h.keys(":s/z/Z/")
	h.settle()
	if scr := h.term.screen(); !strings.Contains(scr, ":s/z/Z/") {
		t.Errorf("コマンドラインが描かれない:\n%s", scr)
	}
	h.keys("<CR>")
	h.settle()
	if got := h.text(); got != "ello\nZzz" {
		t.Fatalf("text = %q", got)
	}
	// :s のキーを含めて記録し、別の行で再生する。
	h.keys("ggqa:s/l/L/<CR>qj@a")
	h.settle()
	if got := h.text(); got != "eLlo\nZzz" {
		t.Errorf("text = %q（2 行目に l は無いので失敗し、変えない）", got)
	}
	h.keys("k@a")
	h.settle()
	if got := h.text(); got != "eLLo\nZzz" {
		t.Errorf("text = %q", got)
	}
	scr := h.term.screen()
	if !strings.HasPrefix(scr, "eLLo") || !strings.Contains(scr, "a.txt") {
		t.Errorf("画面:\n%s", scr)
	}
}

// 利用者のキーマップは、キーの列を別のキーの列に展開する。接頭辞は時間切れで確定する。
//
// req: FR-41
func TestUserKeymap(t *testing.T) {
	cfg := Config{
		Keymap: map[string]map[string]string{
			"normal": {"lx": "dd", "<Space>w": ":w<CR>"},
			"insert": {"jk": "<Esc>"},
		},
		KeyTimeout: 50 * time.Millisecond,
	}
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	cfg.WorkDir = dir
	h := start(t, cfg, "a.txt")
	h.keys("lx")
	h.settle()
	if got := h.text(); got != "two\nthree" {
		t.Fatalf("lx: text = %q", got)
	}
	h.keys("Ajkjx")
	h.settle()
	h.eventually("jk で抜ける", func() bool { return h.e.cur.state.Mode() == command.ModeNormal })
	if got := h.text(); got != "two\nthee" {
		t.Errorf("A の後の jk: text = %q", got)
	}
	h.keys("ij<Esc>")
	h.settle()
	if got := h.text(); got != "two\nthjee" {
		t.Errorf("j の後の <Esc>: text = %q", got)
	}
	// l だけを打つと、時間切れで l（右へ 1 文字）に確定する。
	h.keys("0l")
	h.eventually("l の確定", func() bool { return h.e.cur.state.Cursor().Byte == 1 })
	h.keys("<Space>w")
	h.settle()
	b, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(b) != "two\nthjee\n" {
		t.Errorf("<Space>w: file = %q", b)
	}
}

// 拡張機能は、宣言した契機（ファイルの種類、コマンド）で初めて起動する。
//
// req: FR-14
func TestActivation(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.fakeft": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), FileTypes: map[string]string{".fakeft": "fakeft"}}, "a.txt")
	h.settle()
	for _, id := range []string{"fake", "ftx", "slow"} {
		if st := h.state(id); st != exthost.StateNotStarted {
			t.Errorf("%s: state = %v", id, st)
		}
	}
	h.keys(":e b.fakeft<CR>")
	h.eventually("ftx の起動", func() bool { return h.state("ftx") == exthost.StateRunning })
	if st := h.state("fake"); st != exthost.StateNotStarted {
		t.Errorf("fake: state = %v", st)
	}
	h.keys(`\u`)
	h.eventually("fake.upper", func() bool { return h.e.cur.buf.Text() == "X" })
	if st := h.state("fake"); st != exthost.StateRunning {
		t.Errorf("fake: state = %v", st)
	}
}

// 拡張機能は全文の後に差分を受け取り、本体と同じ内容の複製を持つ。マクロの再生の途中の問い合わせの前にも、差分が届く。
//
// req: FR-16
func TestDiffSync(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`x\d`)
	h.eventually("複製", func() bool { return h.e.message.Text == `v1:"bc\ndef"` })
	h.keys(`uofoo<Esc>jdd\d`)
	h.settle()
	h.eventually("差分の後の複製", func() bool {
		return h.e.message.Text == fmt.Sprintf("v%d:%q", h.e.cur.buf.Version(), "abc\nfoo")
	})
	// 再生の途中の x の後に、拡張機能のコマンドが現在の内容で答える。
	h.e.do(func() { h.e.cur.state.Registers().Set('q', command.Register{Text: `xj\u`}) })
	h.keys("gg@q")
	h.settle()
	if got := h.text(); got != "bc\nFOO" {
		t.Errorf("text = %q", got)
	}
}

// 期限が正の値なら、期限を超えた問い合わせの操作は失敗し、後から届いた結果は適用しない。
//
// req: FR-17
func TestSyncTimeout(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(100 * time.Millisecond)}, "a.txt")
	// 起動の時間を期限に含めないように、先に起動しておく。
	h.keys("dil")
	h.eventually("dil", func() bool { return h.e.cur.buf.Text() == "\ndef" })
	h.keys("jdis")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "timed out") {
		t.Errorf("message = %q", msg)
	}
	time.Sleep(2 * slowDelay)
	h.keys("x")
	h.settle()
	if got := h.text(); got != "\nef" {
		t.Errorf("text = %q（期限を超えた結果を適用した）", got)
	}
}

// 期限が 0 なら待たずに次のキーを受け付け、結果が届いた時点で、バージョン番号とカーソルが同じなら適用し、違えば破棄する。
//
// req: FR-18
func TestDeferred(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\nghi\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(0)}, "a.txt")
	h.keys("dis")
	h.settle()
	if got := h.text(); got != "abc\ndef\nghi" {
		t.Fatalf("待たずに戻る前に適用した: %q", got)
	}
	h.eventually("後からの適用", func() bool { return h.e.cur.buf.Text() == "\ndef\nghi" })
	// 問い合わせの後にカーソルを動かすと、結果は破棄する。
	h.keys("jdisj")
	h.settle()
	time.Sleep(2 * slowDelay)
	if got := h.text(); got != "\ndef\nghi" {
		t.Errorf("カーソルが動いた後に適用した: %q", got)
	}
	if got := h.e.cur.state.Cursor().Line; got != 2 {
		t.Errorf("cursor line = %d", got)
	}
}

// マクロとドットリピートの再生中は、期限 0 の設定でも前の操作の完了を待ち、応答が無ければその位置で止める。
// 再生中のコマンドは sync で問い合わせ、結果の Open（カーソルの移動）を次の操作の前に行う。
//
// req: FR-19
func TestReplayWaits(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\nb2\nc3\nd4\n"})
	h := start(t, Config{
		WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(0), ReplayTimeout: time.Second,
	}, "a.txt")
	set := func(reg rune, text string) {
		h.e.do(func() { h.e.cur.state.Registers().Set(reg, command.Register{Text: text}) })
	}
	set('a', "disj")
	h.keys("2@a")
	h.settle()
	if got := h.text(); got != "\n\nc3\nd4" {
		t.Fatalf("2@a: text = %q", got)
	}
	// 応答しない拡張機能で、再生はその位置で止まる（後ろの j は行わない）。
	set('b', "dihj")
	h.keys("@b")
	h.settle()
	if got, line := h.text(), h.e.cur.state.Cursor().Line; got != "\n\nc3\nd4" || line != 2 {
		t.Errorf("@b: text = %q, line = %d", got, line)
	}
	// ドットリピートも待つ。
	h.keys("dis")
	h.eventually("dis", func() bool { return h.e.cur.buf.Text() == "\n\n\nd4" })
	h.keys("j.")
	h.settle()
	if got := h.text(); got != "\n\n\n" {
		t.Errorf(". : text = %q", got)
	}
	// 再生中の command/execute の Open は、次の操作の前にカーソルを動かす。
	set('c', `\j\j`)
	h.keys("gg@c")
	h.settle()
	if got := h.text(); got != "*\n*\n\n" {
		t.Errorf("@c: text = %q", got)
	}
}

// 拡張機能の起動（initialize の応答）を待たずに、キー入力を受け付け、キーマップも効く。
//
// req: NFR-04, FR-41
func TestNoWaitForStartup(t *testing.T) {
	dir := writeFiles(t, map[string]string{"x.slowft": "abc\n"})
	h := start(t, Config{
		WorkDir: dir, Extensions: fakeSpecs(t), FileTypes: map[string]string{".slowft": "slowft"},
		Keymap: map[string]map[string]string{"normal": {"Q": "ihi<Esc>"}},
	}, "x.slowft")
	began := time.Now()
	h.keys("Q")
	h.eventually("キーの処理", func() bool { return h.e.cur.buf.Text() == "hiabc" })
	if d := time.Since(began); d >= slowInitDelay {
		t.Errorf("起動を待った: %v", d)
	}
	if st := h.state("slow"); st != exthost.StateStarting {
		t.Errorf("slow: state = %v（起動中のはず）", st)
	}
	h.eventually("起動の完了", func() bool { return h.state("slow") == exthost.StateRunning })
}

// 保存は buffer/willSave の編集を適用してから書き出す。:q は未保存の変更があれば失敗し、:wq で終わる。
//
// req: FR-08
func TestSaveAndQuit(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.go": "import fmt\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.go")
	h.keys(`\d`)
	h.eventually("fake の起動", func() bool { return h.state("fake") == exthost.StateRunning })
	h.keys("A!<Esc>:q<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E37") {
		t.Errorf(":q: message = %q", msg)
	}
	h.keys(":w<CR>")
	h.settle()
	b, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(b) != "import FMT!\n" {
		t.Errorf("file = %q", b)
	}
	if got := h.text(); got != "import FMT!" {
		t.Errorf("text = %q", got)
	}
	h.keys("x:wq<CR>")
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal(":wq で終わらない")
	}
	h.done <- nil
	b, _ = os.ReadFile(filepath.Join(dir, "a.go"))
	if string(b) != "import FMT\n" {
		t.Errorf("file = %q", b)
	}
}

// 名前の無いバッファの :w は E32、名前を付けた :w はそのファイルのバッファにする。
//
// req: FR-08
func TestWriteNoName(t *testing.T) {
	dir := writeFiles(t, nil)
	h := start(t, Config{WorkDir: dir})
	h.keys("ihi<Esc>:w<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E32") {
		t.Errorf("message = %q", msg)
	}
	h.keys(":w new.txt<CR>")
	h.settle()
	b, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(b) != "hi\n" {
		t.Errorf("file = %q", b)
	}
	var path string
	var modified bool
	h.e.do(func() { path, modified = h.e.cur.path, h.e.cur.buf.Modified() })
	if path != filepath.Join(dir, "new.txt") || modified {
		t.Errorf("path = %q, modified = %v", path, modified)
	}
}

// 拡張機能の buffer/edit は、元にしたバージョン番号が現在と一致すれば適用する。
//
// req: FR-15
func TestExtensionEdit(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\l`)
	h.eventually("buffer/edit", func() bool { return h.e.cur.buf.Text() == "abc!" })
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc" {
		t.Errorf("u: text = %q", got)
	}
}

// choice/ask は問いをコマンドラインの行に出し、数字のキーで選ばせる。editor/state は編集の状態を返す。
func TestChoiceAndState(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\a`)
	h.eventually("問い", func() bool { return h.e.activeChoice() != nil })
	h.settle()
	if scr := h.term.screen(); !strings.Contains(scr, "どれ?") {
		t.Errorf("問いが描かれない:\n%s", scr)
	}
	h.keys("x2")
	h.eventually("選んだ結果", func() bool { return strings.HasPrefix(h.e.message.Text, "selected=b") })
	if got := h.text(); got != "abc" {
		t.Errorf("問いの間のキーで編集した: %q", got)
	}
	h.keys(`l\s`)
	h.eventually("editor/state", func() bool { return h.e.message.Text == "mode=normal cursor=0:1 buffers=1 err=<nil>" })
}

// 逆順の範囲の確認は、問いを出し、y で続ける。
//
// req: FR-08
func TestConfirm(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\nd\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":3,1d<CR>")
	h.settle()
	var cf bool
	h.e.do(func() { cf = h.e.confirm != nil })
	if !cf {
		t.Fatalf("確認を求めない: message = %q", h.message())
	}
	h.keys("n:3,1d<CR>")
	h.settle()
	if got := h.text(); got != "a\nb\nc\nd" {
		t.Fatalf("n で消した: %q", got)
	}
	h.keys("y")
	h.settle()
	if got := h.text(); got != "d" {
		t.Errorf("y: text = %q", got)
	}
}

// 大きな回数の再生は <C-c> で止まる。
func TestInterrupt(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.keys(":s/a/aa/<CR>")
	h.settle()
	h.keys("999999999@:")
	time.Sleep(100 * time.Millisecond)
	h.keys("<C-c>")
	h.settle()
	if msg := h.message(); msg != "中断しました" {
		t.Errorf("message = %q", msg)
	}
	h.keys("ox<Esc>")
	h.settle()
	if got := h.text(); !strings.HasSuffix(got, "a\nx") {
		t.Errorf("中断の後のキー: text の末尾 = %q", got[max(0, len(got)-10):])
	}
}
