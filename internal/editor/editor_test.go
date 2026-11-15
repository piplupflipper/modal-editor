package editor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
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
// u で取り消すと、カーソルは適用する前の位置へ戻る（Vim 9.1 で、1 行目の 2 文字目から setline(1, "abc!") の後に u を打つと 1 行目の 2 文字目）。
//
// req: FR-07, FR-15
func TestExtensionEdit(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`l\l`)
	h.eventually("buffer/edit", func() bool { return h.e.cur.buf.Text() == "abc!" })
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc" {
		t.Errorf("u: text = %q", got)
	}
	var cur buffer.Position
	h.e.do(func() { cur = h.e.cur.state.Cursor() })
	if cur != (buffer.Position{Line: 0, Byte: 1}) {
		t.Errorf("u: cursor = %v, want 0:1", cur)
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

// 確認の問いを出している間は拡張機能のイベントを取っておくので、問いの間に届いた editor/open（focus）で
// カーソルのあるバッファが入れ替わっても、答えは問いを出したバッファで実行する。
//
// req: FR-08
func TestConfirmKeepsBuffer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\nd\n", "b.txt": "1\n2\n3\n4\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(5 * time.Second)}, "b.txt", "a.txt")
	h.keys(`\f:3,1d<CR>`)
	h.settle()
	time.Sleep(300 * time.Millisecond)
	var path string
	h.e.do(func() { path = h.e.cur.path })
	if filepath.Base(path) != "a.txt" {
		t.Fatalf("問いの間にバッファが移った: %s", path)
	}
	h.keys("y")
	h.eventually("editor/open の後の b.txt", func() bool { return filepath.Base(h.e.cur.path) == "b.txt" })
	h.e.do(func() {
		for _, bs := range h.e.bufs {
			want := map[string]string{"a.txt": "d", "b.txt": "1\n2\n3\n4"}[filepath.Base(bs.path)]
			if got := bs.buf.Text(); got != want {
				t.Errorf("%s: text = %q, want %q", filepath.Base(bs.path), got, want)
			}
		}
	})
}

// 再生中の command/execute の open が別のバッファを指すと、そのバッファへ移り、マクロの残りのキーは移った先で続く。
// 再生中でなければ、操作の後に移る。
//
// req: FR-19
func TestReplaySwitchesBuffer(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\na2\na3\n", "b.txt": "b1\nb2\nb3\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(5 * time.Second)}, "a.txt", "b.txt")
	h.e.do(func() { h.e.cur.state.Registers().Set('q', command.Register{Text: `\gddx`}) })
	h.keys("@q")
	h.settle()
	h.e.do(func() {
		if got := filepath.Base(h.e.cur.path); got != "a.txt" {
			t.Errorf("cur = %s", got)
		}
		for _, bs := range h.e.bufs {
			want := map[string]string{"a.txt": "a1\n3", "b.txt": "b1\nb2\nb3"}[filepath.Base(bs.path)]
			if got := bs.buf.Text(); got != want {
				t.Errorf("%s: text = %q, want %q", filepath.Base(bs.path), got, want)
			}
		}
	})
	// 再生中でなければ、Feed の後に移る。
	h.keys(`\g`)
	h.eventually("b.txt へ移る", func() bool {
		return filepath.Base(h.e.cur.path) == "b.txt" && h.e.cur.state.Cursor().Line == 1
	})
}

// 期限 0 の結果が、キーの列の解釈の途中（d の後）に届いたら、適用せずに捨てる。
//
// req: FR-18
func TestDeferredDuringPending(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc def\nghi\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SyncTimeout: durationPtr(0)}, "a.txt")
	h.keys("disd")
	h.settle()
	time.Sleep(2 * slowDelay)
	h.keys("w")
	h.settle()
	if got := h.text(); got != "def\nghi" {
		t.Errorf("text = %q", got)
	}
}

// 再生中の待ちの途中に Run の ctx が終わったら、待ちをやめて戻る。端末の入力が閉じたら ErrTerminalLost で戻る。
func TestRunStops(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), ReplayTimeout: time.Minute}, "a.txt")
	h.keys("dil")
	h.eventually("起動", func() bool { return h.e.cur.buf.Text() == "" })
	h.e.do(func() { h.e.cur.state.Registers().Set('q', command.Register{Text: "dih"}) })
	h.keys("u@q")
	time.Sleep(200 * time.Millisecond)
	began := time.Now()
	h.cancel()
	select {
	case err := <-h.done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run = %v", err)
		}
		h.done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("待ちの間に ctx が終わっても戻らない")
	}
	if d := time.Since(began); d > time.Second {
		t.Errorf("戻るまで %v", d)
	}

	h2 := start(t, Config{})
	close(h2.term.in)
	select {
	case err := <-h2.done:
		if !errors.Is(err, ErrTerminalLost) {
			t.Errorf("Run = %v", err)
		}
		h2.done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("端末の入力が閉じても戻らない")
	}
}

// 引数の無い :e は、バッファの番号を変えずに読み直す。読み直した内容は拡張機能へ差分で届き、u で戻れる。
//
// req: FR-08, FR-16
func TestReload(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	var id ext.BufferID
	h.e.do(func() { id = h.e.cur.id })
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("xyz\nnew\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.keys(`:e<CR>\d`)
	h.eventually("複製", func() bool {
		return h.e.message.Text == fmt.Sprintf("v%d:%q", h.e.cur.buf.Version(), "xyz\nnew")
	})
	h.e.do(func() {
		if h.e.cur.id != id || h.e.cur.buf.Modified() || len(h.e.bufs) != 1 {
			t.Errorf("id = %d（前は %d）, modified = %v, bufs = %d", h.e.cur.id, id, h.e.cur.buf.Modified(), len(h.e.bufs))
		}
	})
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc" {
		t.Errorf("u: text = %q", got)
	}
}

// 取っておくイベントが上限を超えたら、最も古い通知から捨てる。問い合わせの結果は捨てない。
func TestHoldLimit(t *testing.T) {
	h := start(t, Config{Host: exthost.Config{MaxQueuedEvents: 3}})
	h.e.do(func() {
		e := h.e
		e.hold(&exthost.QueryResult{Tag: queryTag(99)})
		for i := range 4 {
			e.hold(&exthost.MessageShow{Params: ext.MessageShowParams{Text: fmt.Sprint(i)}})
		}
		if len(e.held) != 3 {
			t.Fatalf("held = %d", len(e.held))
		}
		if _, ok := e.held[0].(*exthost.QueryResult); !ok {
			t.Errorf("held[0] = %T", e.held[0])
		}
		if m := e.held[1].(*exthost.MessageShow); m.Params.Text != "2" {
			t.Errorf("held[1] = %q", m.Params.Text)
		}
		e.held = nil
	})
}

// buffer/edit の noLines と endOfLine は、適用した後のバッファの行の無い状態と末尾の改行の記録を変え、書き出しの結果に効く。
// 拡張機能へは、変わった finalNewline を buffer/open で送り直す。
// 書き出しの結果は、Vim 9.1 で外から書き換えたファイルを :e! で読み直して :w したもの（"" は ""、"\n" は "\n"）と同じである。
func TestExtensionEditLineState(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		return string(b)
	}

	// 全文を消して行の無いバッファにすると、空のファイルとして書き出す。
	h.keys(`\z`)
	h.eventually("fake.empty", func() bool { return h.e.message.Text == "fake.empty applied=true v1 err=<nil>" })
	h.keys(`\e`)
	h.eventually("fake.empty の後の finalNewline", func() bool { return h.e.message.Text == "b1 finalNewline=false" })
	h.keys(":w<CR>")
	h.settle()
	if got := read("a.txt"); got != "" {
		t.Errorf("fake.empty の後の :w: a.txt = %q, want %q", got, "")
	}

	// 編集を持たずに行の無いバッファでなくすと、バージョン番号は変わらず、空の行 1 つとして書き出す。
	h.keys(`\n`)
	h.eventually("fake.lines", func() bool { return h.e.message.Text == "fake.lines applied=false v1 err=<nil>" })
	h.keys(`\e`)
	h.eventually("fake.lines の後の finalNewline", func() bool { return h.e.message.Text == "b1 finalNewline=true" })
	h.keys(":w<CR>")
	h.settle()
	if got := read("a.txt"); got != "\n" {
		t.Errorf("fake.lines の後の :w: a.txt = %q, want %q", got, "\n")
	}

	// 行の無い記録はアンドゥの履歴の位置に付くので、その位置で外した記録は、アンドゥとリドゥでその位置へ戻っても外れたままである。
	h.keys("u")
	h.settle()
	h.keys("<C-r>")
	h.settle()
	var noLines bool
	h.e.do(func() { noLines = h.e.cur.buf.NoLines() })
	if noLines {
		t.Error("fake.lines で外した記録が、アンドゥとリドゥで戻った")
	}

	// endOfLine は末尾の改行の記録を変える。末尾の改行を補う設定を無効にしたバッファで確かめる。
	h.e.do(func() {
		b := buffer.New("y")
		b.SetFixEndOfLine(false)
		h.e.newBuffer(b, filepath.Join(dir, "b.txt"), "")
	})
	h.keys(`\o`)
	h.eventually("fake.endofline", func() bool { return h.e.message.Text == "fake.endofline applied=false v0 err=<nil>" })
	h.keys(`\e`)
	h.eventually("fake.endofline の後の finalNewline", func() bool { return h.e.message.Text == "b2 finalNewline=true" })
	h.keys(":w<CR>")
	h.settle()
	if got := read("b.txt"); got != "y\n" {
		t.Errorf("fake.endofline の後の :w: b.txt = %q, want %q", got, "y\n")
	}
}

// 拡張機能へ送る buffer/open の finalNewline は、バッファの FinalNewline（末尾の改行の有無と、末尾の改行を補う設定）である。
// 末尾の改行の無いファイルも、既定の設定では改行を付けて書き出すので true になる。
func TestFinalNewlineToExtension(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(`\e`)
	h.eventually("a.txt の finalNewline", func() bool { return h.e.message.Text == "b1 finalNewline=true" })

	// 行の無いバッファは空のファイルとして書き出すので false で送り、行の無いバッファでなくなれば true で送り直す。
	for _, step := range []struct {
		keys string
		want bool
	}{{"ggdG", false}, {"u", true}, {"<C-r>", false}, {"ix<Esc>", true}} {
		h.keys(step.keys + `\e`)
		want := fmt.Sprintf("b1 finalNewline=%v", step.want)
		h.eventually(step.keys+" の後の finalNewline", func() bool { return h.e.message.Text == want })
	}
	h.keys("uu") // ix と、やり直した ggdG を戻して "abc" にする
	h.settle()

	// 末尾の改行を補う設定を無効にしたバッファ（設定ファイルの項目は #9 の後）は、false で送る。
	h.e.do(func() {
		b := buffer.New("x")
		b.SetFixEndOfLine(false)
		h.e.newBuffer(b, filepath.Join(dir, "b.txt"), "")
	})
	h.keys(`\e`)
	h.eventually("b.txt の finalNewline", func() bool { return h.e.message.Text == "b2 finalNewline=false" })
	h.keys(":w<CR>")
	h.settle()
	if b, _ := os.ReadFile(filepath.Join(dir, "b.txt")); string(b) != "x" {
		t.Errorf("b.txt = %q, want %q", b, "x")
	}
	h.e.do(func() { h.e.switchTo(h.e.bufs[0]) })
	h.keys(":w<CR>")
	h.settle()
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "abc\n" {
		t.Errorf("a.txt = %q, want %q", b, "abc\n")
	}
}

// 全ての行を消したバッファと、新しいファイルを編集せずに書き出すと、Vim 9.1 と同じく空のファイルになる。
// 期待値は、手元の Vim 9.1 で vim -Nu NONE -n --not-a-term -c '<操作>' -c 'wq! out' in を実行して得た（2026-11-16）。
//
// req: FR-08
func TestWriteNoLines(t *testing.T) {
	tests := []struct {
		name, in, keys, want string
	}{
		{"dd で最後の行を消す", "\n", "dd", ""},
		{"末尾の改行なしの dd", "a", "dd", ""},
		{"ggdG", "a\nb\n", "ggdG", ""},
		{"ggVGd", "a\nb", "ggVGd", ""},
		{":%d", "a\nb", ":%d<CR>", ""},
		{":g/./d", "a\nb\n", ":g/./d<CR>", ""},
		{"dd の後に u", "a", "ddu", "a\n"},
		{"dd の後に入力して u", "a", "ddix<Esc>u", ""},
		{"ggdG を戻してやり直す", "a\nb", "ggdGu<C-r>", ""},
		{"ggdG の後に入力", "a\nb", "ggdGix<Esc>", "x\n"},
		{"dd の後に o", "a", "ddo<Esc>", "\n\n"},
		{"cc は行を残す", "a", "cc<Esc>", "\n"},
		{"0d$ は行を残す", "abc", "0d$", "\n"},
		{"空のファイルを編集しない", "", "", ""},
		{"空のファイルに入力して消す", "", "ix<Esc>x", "\n"},
		// 行の無いバッファの :d は、dd と同じくレジスターを変えない。yy した x が p で入る。
		{"行の無いバッファの :d", "", "ix<Esc>yyu:d<CR>p", "\nx\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeFiles(t, map[string]string{"a.txt": tt.in})
			h := start(t, Config{WorkDir: dir}, "a.txt")
			h.keys(tt.keys + ":w<CR>")
			h.settle()
			if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != tt.want {
				t.Errorf("file = %q, want %q", b, tt.want)
			}
		})
	}
	// 行の無いバッファを開いた後、外部で "\n"（空の行 1 つ）に書き換えて :e! すると、行の無いバッファではなくなる。
	for _, keys := range []string{"", "ggdG"} {
		dir := writeFiles(t, map[string]string{"a.txt": ""})
		h := start(t, Config{WorkDir: dir}, "a.txt")
		h.keys(keys)
		h.settle()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		h.keys(":e!<CR>:w!<CR>")
		h.settle()
		if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "\n" {
			t.Errorf("%q の後に :e! :w!: file = %q, want %q", keys, b, "\n")
		}
	}
	// 無いファイルを開いて、編集せずに書き出す。
	dir := writeFiles(t, nil)
	h := start(t, Config{WorkDir: dir}, "new.txt")
	h.keys(":w<CR>")
	h.settle()
	if b, err := os.ReadFile(filepath.Join(dir, "new.txt")); err != nil || len(b) != 0 {
		t.Errorf("new.txt = %q, %v; want empty", b, err)
	}
}
