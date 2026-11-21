package editor

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// mouseText は、マウスの突き合わせに使うファイルの中身である。2026-11-21 に、同じ中身を Neovim 0.12.5（nvim --clean、mouse=a）で
// 開き、tmux 3.6 の 40 列 12 行のペインに SGR の形のマウスの列（tmux send-keys -H）を送って、カーソルの位置を確かめた。
const mouseText = "hello world foo\n\ttab line\nshort\n日本語のテキスト\nlast\n"

// mouseStart は、40 列 12 行の偽の端末で files を開く。画面は、タブの行（0 行目）、表示領域（1 行目から 9 行目）、
// ステータス行（10 行目）、コマンドラインの行（11 行目）である。
func mouseStart(t *testing.T, cfg Config, files map[string]string, open ...string) *harness {
	t.Helper()
	dir := writeFiles(t, files)
	cfg.WorkDir = dir
	term := newFakeTerm()
	term.w, term.h = 40, 12
	return startTerm(t, term, cfg, open...)
}

// mouse は、マウスの出来事のキー key（Vim の表記）を、画面の row 行目の col 列目で端末から送り、処理し終えるまで待つ。
func (h *harness) mouse(key string, row, col int) {
	h.t.Helper()
	for _, k := range editcmd.MustParseKeys(key) {
		h.term.in <- TermEvent{Key: k, MouseRow: row, MouseCol: col}
		h.sent++
	}
	h.settle()
}

// click は、row 行目の col 列目で左のボタンを押して離す。
func (h *harness) click(row, col int) {
	h.t.Helper()
	h.mouse("<LeftMouse>", row, col)
	h.mouse("<LeftRelease>", row, col)
}

// viewTop は、読み込んでいる client の表示領域の最初の行を、イベントループの goroutine で読んで返す。
func (h *harness) viewTop() int {
	var top int
	h.e.do(func() { top = h.e.renderer.Top() })
	return top
}

// mode は、カーソルのあるバッファのモードを返す。
func (h *harness) mode() command.Mode {
	var m command.Mode
	h.e.do(func() { m = h.e.cur.state.Mode() })
	return m
}

// 左のクリックは、Neovim 0.12.5 の mouse=a と同じ位置へカーソルを移す。行末より右は最後の文字、"~" の行は最後の行、
// タブ文字の上はタブ文字、全角文字の右の半分はその文字、ステータス行とコマンドラインの行は動かさない。
// 望みの列は押した列にする（行末より右を押した後の j は、押した列へ移る）。行番号の列は本文の左の端の列にする。
//
// req: FR-181
func TestMouseClick(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText}, "a.txt")
	steps := []struct {
		keys     string
		row, col int
		want     buffer.Position
	}{
		{"", 1, 7, buffer.Position{Line: 0, Byte: 7}},
		{"", 3, 20, buffer.Position{Line: 2, Byte: 4}},
		{"j", -1, 0, buffer.Position{Line: 3, Byte: 21}},
		{"", 8, 1, buffer.Position{Line: 4, Byte: 1}},
		{"", 2, 3, buffer.Position{Line: 1, Byte: 0}},
		{"", 4, 3, buffer.Position{Line: 3, Byte: 3}},
		{"", 10, 5, buffer.Position{Line: 3, Byte: 3}},
		{"", 11, 5, buffer.Position{Line: 3, Byte: 3}},
		// 行番号の列（4 列）を出す。行番号の列の上は、その行の本文の左の端である。
		{":set number<CR>", 3, 1, buffer.Position{Line: 2, Byte: 0}},
		{"", 4, 9, buffer.Position{Line: 3, Byte: 6}},
		{"j", -1, 0, buffer.Position{Line: 4, Byte: 3}},
	}
	for i, s := range steps {
		if s.keys != "" {
			h.keys(s.keys)
			h.settle()
		}
		if s.row >= 0 {
			h.click(s.row, s.col)
		}
		if got := h.cursor(); got != s.want {
			t.Errorf("%d: %q (%d,%d) の後のカーソル = %+v, want %+v", i, s.keys, s.row, s.col, got, s.want)
		}
	}
	if m := h.mode(); m != command.ModeNormal {
		t.Errorf("モード = %v", m)
	}
}

// インサートモードのクリックは、インサートモードのまま移り、行末より右は行末の位置に置く。
// ビジュアルモードのクリックは、選択を終えてノーマルモードへ戻ってから移る（Neovim 0.12.5 と同じ）。
// 編集を禁止したバッファでも移れ、ドットリピートには記録しない。
//
// req: FR-181
func TestMouseClickModes(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText}, "a.txt")
	h.keys("i")
	h.settle()
	h.click(3, 20)
	if got := h.cursor(); got != (buffer.Position{Line: 2, Byte: 5}) || h.mode() != command.ModeInsert {
		t.Fatalf("インサートモードのクリック = %+v %v", got, h.mode())
	}
	h.keys("X<Esc>")
	h.settle()
	if got := h.cursorLine(); got != "shortX" {
		t.Errorf("インサートモードのクリックの後の入力 = %q", got)
	}
	h.keys("vll")
	h.settle()
	h.click(1, 6)
	if got := h.cursor(); got != (buffer.Position{Line: 0, Byte: 6}) || h.mode() != command.ModeNormal {
		t.Errorf("ビジュアルモードのクリック = %+v %v", got, h.mode())
	}
	// x を . で繰り返した後にクリックしても、. はクリックでなく x を繰り返す。
	h.keys("x")
	h.settle()
	h.click(2, 3)
	h.keys(".")
	h.settle()
	if got := h.text(); !strings.HasPrefix(got, "hello orld foo\ntab line\n") {
		t.Errorf(". の後 = %q", got)
	}
	h.keys(":set readonly<CR>")
	h.settle()
	h.click(5, 2)
	if got := h.cursor(); got != (buffer.Position{Line: 4, Byte: 2}) {
		t.Errorf("編集を禁止したバッファのクリック = %+v（message=%q）", got, h.message())
	}
}

// 左のドラッグは、押した位置からマウスの位置までを文字単位のビジュアルモードで選ぶ。行末より右へ動かすと改行まで選ぶ。
// インサートモードのドラッグは、インサートモードを抜けてから選ぶ。表示領域の下へはみ出したら、はみ出した行の数だけ送る。
//
// req: FR-183
func TestMouseDrag(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText}, "a.txt")
	h.mouse("<LeftMouse>", 1, 2)
	h.mouse("<LeftDrag>", 1, 5)
	h.mouse("<LeftDrag>", 1, 8)
	h.mouse("<LeftRelease>", 1, 8)
	if h.mode() != command.ModeVisual || h.cursor() != (buffer.Position{Line: 0, Byte: 8}) {
		t.Fatalf("ドラッグの後 = %v %+v", h.mode(), h.cursor())
	}
	h.keys("d")
	h.settle()
	if got := h.cursorLine(); got != "held foo" {
		t.Errorf("選んだ範囲を消した後 = %q", got)
	}
	h.keys("u")
	h.settle()
	h.mouse("<LeftMouse>", 1, 2)
	h.mouse("<LeftDrag>", 1, 25)
	h.mouse("<LeftRelease>", 1, 25)
	h.keys("d")
	h.settle()
	if got := h.cursorLine(); got != "he\ttab line" {
		t.Errorf("行末より右までのドラッグの後に消した = %q", got)
	}
	h.keys("u")
	h.settle()
	// 押した文字の上で動かしただけなら、選ばない。ボタンを押していない動きは何もしない。
	h.mouse("<LeftMouse>", 2, 1)
	h.mouse("<LeftDrag>", 2, 2)
	h.mouse("<LeftRelease>", 2, 2)
	if h.mode() != command.ModeNormal {
		t.Errorf("タブ文字の中で動かした後のモード = %v", h.mode())
	}
	h.mouse("<LeftDrag>", 3, 3)
	if h.mode() != command.ModeNormal || h.cursor() != (buffer.Position{Line: 1, Byte: 0}) {
		t.Errorf("押さずに動かした後 = %v %+v", h.mode(), h.cursor())
	}
	// インサートモードのドラッグ。
	h.keys("gg0i")
	h.settle()
	h.mouse("<LeftMouse>", 1, 1)
	h.mouse("<LeftDrag>", 1, 4)
	h.mouse("<LeftRelease>", 1, 4)
	if h.mode() != command.ModeVisual {
		t.Fatalf("インサートモードのドラッグの後のモード = %v", h.mode())
	}
	h.keys("d")
	h.settle()
	if got := h.cursorLine(); got != "h world foo" {
		t.Errorf("インサートモードのドラッグで選んだ範囲を消した後 = %q", got)
	}
	// タブの行でボタンを押したドラッグは選ばない。
	h.mouse("<LeftMouse>", 0, 30)
	h.mouse("<LeftDrag>", 2, 3)
	if h.mode() != command.ModeNormal {
		t.Errorf("タブの行から動かした後のモード = %v", h.mode())
	}
}

// 表示領域の下（ステータス行とコマンドラインの行）へドラッグすると、はみ出した行の数だけ表示領域を送り、最後の行へ移る。
// 上（タブの行）へドラッグすると、上へ送り、最初の行へ移る（Neovim 0.12.5 と同じ）。
//
// req: FR-183
func TestMouseDragScroll(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	h := mouseStart(t, Config{}, map[string]string{"a.txt": strings.Join(lines, "\n") + "\n"}, "a.txt")
	h.mouse("<LeftMouse>", 5, 1)
	h.mouse("<LeftDrag>", 9, 1)
	h.mouse("<LeftDrag>", 10, 1)
	if top, cur := h.viewTop(), h.cursor(); top != 1 || cur.Line != 9 {
		t.Errorf("ステータス行へのドラッグ = 最初の行 %d、カーソル %+v", top, cur)
	}
	h.mouse("<LeftDrag>", 11, 1)
	if top, cur := h.viewTop(), h.cursor(); top != 3 || cur.Line != 11 {
		t.Errorf("コマンドラインの行へのドラッグ = 最初の行 %d、カーソル %+v", top, cur)
	}
	h.mouse("<LeftDrag>", 0, 1)
	if top, cur := h.viewTop(), h.cursor(); top != 2 || cur.Line != 2 {
		t.Errorf("タブの行へのドラッグ = 最初の行 %d、カーソル %+v", top, cur)
	}
	h.mouse("<LeftRelease>", 0, 1)
	if h.mode() != command.ModeVisual {
		t.Errorf("モード = %v", h.mode())
	}
	// 画面の外の位置（端末によっては報告する）へのドラッグは、画面の最後の行（コマンドラインの行。2 行のはみ出し）として数える
	// （Neovim 0.12.5 と同じ。PR #346 のレビューの推奨 2）。
	h.mouse("<LeftMouse>", 5, 1)
	h.mouse("<LeftDrag>", 30, 1)
	if top, cur := h.viewTop(), h.cursor(); top != 4 || cur.Line != 12 {
		t.Errorf("画面の外へのドラッグ = 最初の行 %d、カーソル %+v", top, cur)
	}
	h.mouse("<LeftRelease>", 30, 1)
}

// 同じ server の 2 つの client で、マクロに記録したクリックは、再生した client が最後に受けたマウスの出来事の位置で働く。
// 別の client が後から受けた出来事の位置を、再生した client の画面の座標として解かない（PR #346 のレビューの必須 1）。
//
// req: FR-185
func TestMouseTwoClients(t *testing.T) {
	var lines []string
	for i := 1; i <= 40; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText, "long.txt": strings.Join(lines, "\n") + "\n"}, "a.txt")
	peer := &exitTerm{fakeTerm: newFakeTerm()}
	peer.w, peer.h = 40, 30
	h.peers = append(h.peers, peer.fakeTerm)
	if err := h.e.Attach(peer, ClientInfo{ID: 2, Files: []string{filepath.Join(h.e.cfg.WorkDir, "long.txt")}}); err != nil {
		t.Fatalf("Attach = %v", err)
	}
	// 上の client で、3 行目の 4 列目のクリックと x を記録する。
	h.keys("qa")
	h.settle()
	h.click(3, 3)
	h.keys("xqgg")
	h.settle()
	if got := h.bufferText("a.txt"); !strings.Contains(got, "\nshot\n") {
		t.Fatalf("記録の後 = %q", got)
	}
	// 下の client で、上の client の画面に無い 21 行目を押す。
	peer.in <- TermEvent{Key: editcmd.Special(editcmd.CodeLeftMouse), MouseRow: 21, MouseCol: 5}
	peer.in <- TermEvent{Key: editcmd.Special(editcmd.CodeLeftRelease), MouseRow: 21, MouseCol: 5}
	h.sent += 2
	h.settle()
	// 上の client で再生すると、上の client で最後に押した 3 行目の 4 列目の文字を消す。
	h.keys("@a")
	h.settle()
	if got := h.bufferText("a.txt"); !strings.Contains(got, "\nsho\n") || !strings.Contains(got, "\nlast") {
		t.Errorf("再生の後 = %q", got)
	}
	if got := h.cursor(); got != (buffer.Position{Line: 2, Byte: 2}) {
		t.Errorf("再生の後のカーソル = %+v", got)
	}
}

// 2 回目のクリックは単語を、3 回目のクリックは行を選ぶ。4 回目は 1 回目と同じく移るだけである（Neovim の矩形の選択の代わり）。
//
// req: FR-183
func TestMouseMultiClick(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText}, "a.txt")
	h.mouse("<LeftMouse>", 1, 7)
	h.mouse("<LeftRelease>", 1, 7)
	h.mouse("<2-LeftMouse>", 1, 7)
	if h.mode() != command.ModeVisual || h.cursor() != (buffer.Position{Line: 0, Byte: 10}) {
		t.Fatalf("2 回目のクリック = %v %+v", h.mode(), h.cursor())
	}
	h.mouse("<LeftRelease>", 1, 7)
	h.keys("d")
	h.settle()
	if got := h.cursorLine(); got != "hello  foo" {
		t.Errorf("単語を消した後 = %q", got)
	}
	h.keys("u")
	h.settle()
	h.mouse("<2-LeftMouse>", 1, 5)
	h.keys("d")
	h.settle()
	if got := h.cursorLine(); got != "helloworld foo" {
		t.Errorf("空白の上の 2 回目のクリックで選んで消した後 = %q", got)
	}
	h.keys("u")
	h.settle()
	h.mouse("<3-LeftMouse>", 1, 7)
	if h.mode() != command.ModeVisualLine || h.cursor() != (buffer.Position{Line: 0, Byte: 7}) {
		t.Fatalf("3 回目のクリック = %v %+v", h.mode(), h.cursor())
	}
	h.mouse("<4-LeftMouse>", 2, 3)
	if h.mode() != command.ModeNormal || h.cursor() != (buffer.Position{Line: 1, Byte: 0}) {
		t.Errorf("4 回目のクリック = %v %+v", h.mode(), h.cursor())
	}
	h.keys("i")
	h.settle()
	h.mouse("<2-LeftMouse>", 1, 1)
	h.keys("d")
	h.settle()
	if got := h.text(); !strings.HasPrefix(got, " world foo\n") || h.mode() != command.ModeNormal {
		t.Errorf("インサートモードの 2 回目のクリックで選んで消した後 = %q %v", got, h.mode())
	}
}

// ホイールは、表示領域を mouse.scrollLines の行（既定は 3）だけ送る。送り方は <C-e> と <C-y> と同じで、カーソルが表示領域から出たら
// 余白を残した端の行へ移る。インサートモードでもインサートモードのまま送る。0 行なら送らない（Neovim 0.12.5 の mousescroll=ver:3）。
//
// req: FR-182
func TestMouseWheel(t *testing.T) {
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	h := mouseStart(t, Config{}, map[string]string{"a.txt": strings.Join(lines, "\n") + "\n"}, "a.txt")
	check := func(what string, top, line int) {
		t.Helper()
		if gotTop, cur := h.viewTop(), h.cursor(); gotTop != top || cur.Line != line {
			t.Errorf("%s: 最初の行 %d、カーソルの行 %d, want %d、%d", what, gotTop, cur.Line, top, line)
		}
	}
	h.mouse("<ScrollWheelDown>", 3, 3)
	check("下へ 1 回", 3, 3)
	h.mouse("<ScrollWheelDown>", 3, 3)
	check("下へ 2 回", 6, 6)
	h.mouse("<ScrollWheelUp>", 3, 3)
	check("上へ 1 回", 3, 6)
	h.keys("gg:set so=2<CR>")
	h.settle()
	h.mouse("<ScrollWheelDown>", 3, 3)
	check("余白が 2 行", 3, 5)
	h.keys("i")
	h.settle()
	h.mouse("<ScrollWheelDown>", 3, 3)
	check("インサートモード", 6, 8)
	if h.mode() != command.ModeInsert {
		t.Errorf("インサートモードのホイールの後のモード = %v", h.mode())
	}
	h.keys("<Esc>:option.set mouse.scrollLines 0<CR>")
	h.settle()
	h.mouse("<ScrollWheelDown>", 3, 3)
	check("0 行の設定", 6, 8)
	// ステータス行とコマンドラインの行の上のホイールも、表示領域を送る（Neovim 0.12.5 と同じ）。タブの行の上では送らない。
	h.keys(":option.set mouse.scrollLines 3<CR>")
	h.settle()
	h.mouse("<ScrollWheelDown>", 10, 3)
	check("ステータス行の上", 9, 11)
	h.mouse("<ScrollWheelDown>", 11, 3)
	check("コマンドラインの行の上", 12, 14)
	h.mouse("<ScrollWheelDown>", 0, 3)
	check("タブの行の上", 12, 14)
	// ステータス行のクリックはカーソルを動かさない。
	h.click(10, 3)
	check("ステータス行のクリック", 12, 14)
}

// 行を折り返さないときの横のホイールは、表示領域の左の端を mouse.scrollColumns の列（既定は 6）だけ送る。
// 左の端は見える行のうち最も長い行の最後の列より右へ送らず、カーソルの行がその列より短ければ最も長い行へ移る（Neovim 0.12.5 と同じ）。
//
// req: FR-182
func TestMouseWheelHorizontal(t *testing.T) {
	long := "a very long line that goes past the forty column width of the terminal window here"
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText + long + "\n"}, "a.txt")
	left := func() int {
		var l int
		h.e.do(func() { l = h.e.renderer.Left() })
		return l
	}
	steps := []struct {
		key        string
		left, line int
		byteOff    int
	}{
		{"<ScrollWheelRight>", 6, 0, 6},
		{"<ScrollWheelRight>", 12, 0, 12},
		{"<ScrollWheelRight>", 18, 5, 18},
		{"<ScrollWheelLeft>", 12, 5, 18},
	}
	for i, s := range steps {
		h.mouse(s.key, 3, 3)
		if l, cur := left(), h.cursor(); l != s.left || cur != (buffer.Position{Line: s.line, Byte: s.byteOff}) {
			t.Errorf("%d: %s の後 = 左の端 %d、カーソル %+v, want %d、%d:%d", i, s.key, l, cur, s.left, s.line, s.byteOff)
		}
	}
	for range 20 {
		h.mouse("<ScrollWheelRight>", 3, 3)
	}
	if l := left(); l != len(long)-1 {
		t.Errorf("右へ送りきった左の端 = %d, want %d", l, len(long)-1)
	}
	// 行を折り返すときは、横に送らない。
	h.keys(":set wrap<CR>")
	h.settle()
	before := h.cursor()
	h.mouse("<ScrollWheelLeft>", 3, 3)
	if l, cur := left(), h.cursor(); l != 0 || cur != before {
		t.Errorf("折り返すときの横のホイール = 左の端 %d、カーソル %+v", l, cur)
	}
}

// タブの行のタブの見出しを押すと、そのタブへ移る。
//
// req: FR-181
func TestMouseTabLine(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": "aaa\n", "b.txt": "bbb\n"}, "a.txt", "b.txt")
	if got := h.cursorLine(); got != "bbb" {
		t.Fatalf("開いた後のタブ = %q", got)
	}
	h.click(0, 2)
	if got := h.cursorLine(); got != "aaa" {
		t.Errorf("1 つ目の見出しを押した後 = %q（画面\n%s）", got, h.term.screen())
	}
	h.click(0, 9)
	if got := h.cursorLine(); got != "bbb" {
		t.Errorf("2 つ目の見出しを押した後 = %q", got)
	}
	h.click(0, 30)
	if got := h.cursorLine(); got != "bbb" {
		t.Errorf("見出しの無いところを押した後 = %q", got)
	}
}

// 設定の mouse.enabled（既定は使わない）が真の間だけ、端末にマウスの報告を求める。:option.set の後の次の描画から効く。
//
// req: FR-180
func TestMouseEnabled(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": "aaa\n"}, "a.txt")
	mouse := func() bool {
		h.term.mu.Lock()
		defer h.term.mu.Unlock()
		return h.term.last.Mouse
	}
	if mouse() {
		t.Error("既定でマウスの報告を求めた")
	}
	h.keys(":option.set mouse.enabled true<CR>")
	h.settle()
	if !mouse() {
		t.Error(":option.set mouse.enabled true の後に求めない")
	}
	h.keys(":option.toggle mouse.enabled<CR>")
	h.settle()
	if mouse() {
		t.Error(":option.toggle の後も求めた")
	}
}

// マウスの出来事のキーは、ほかのキーと同じく設定の keymap で割り当て直せる（FR-184）。
// 右辺をキーの列で書いた割り当ても、操作の ID で書いた割り当ても効く。既定で割り当てていない右のボタンも割り当てられる。
//
// req: FR-184
func TestMouseKeymap(t *testing.T) {
	cfg := Config{
		Keymap:        map[string]map[string]string{"normal": {"<LeftMouse>": "x"}},
		KeymapActions: map[string]map[string]string{"normal": {"<RightMouse>": "mouse.click", "<ScrollWheelDown>": "down"}},
	}
	h := mouseStart(t, cfg, map[string]string{"a.txt": mouseText}, "a.txt")
	h.mouse("<LeftMouse>", 1, 7)
	if got, cur := h.cursorLine(), h.cursor(); got != "ello world foo" || cur != (buffer.Position{}) {
		t.Errorf("<LeftMouse> を x に割り当てた後 = %q %+v", got, cur)
	}
	h.mouse("<RightMouse>", 3, 2)
	if cur := h.cursor(); cur != (buffer.Position{Line: 2, Byte: 2}) {
		t.Errorf("<RightMouse> に mouse.click を割り当てた後 = %+v", cur)
	}
	h.mouse("<ScrollWheelDown>", 3, 2)
	if cur := h.cursor(); cur.Line != 3 || h.viewTop() != 0 {
		t.Errorf("<ScrollWheelDown> に down を割り当てた後 = %+v 最初の行 %d", cur, h.viewTop())
	}
	// 割り当てていない中のボタンは何もしない。
	before := h.text()
	h.mouse("<MiddleMouse>", 1, 1)
	if got := h.text(); got != before || h.message() != "" {
		t.Errorf("<MiddleMouse> の後 = %q（message=%q）", got, h.message())
	}
}

// 問いと一覧の窓（レジスターの履歴の一覧）を出している間のマウスの出来事は、答えにも一覧の操作にもせずに捨てる。
//
// req: FR-184
func TestMouseWhileAsking(t *testing.T) {
	h := mouseStart(t, Config{}, map[string]string{"a.txt": mouseText}, "a.txt")
	h.keys("yy:registers<CR>")
	h.eventually("一覧の窓", func() bool { return h.e.regList != nil })
	h.mouse("<ScrollWheelDown>", 3, 3)
	h.click(3, 3)
	var open bool
	h.e.do(func() { open = h.e.regList != nil })
	if !open {
		t.Fatal("マウスの出来事で一覧の窓を閉じた")
	}
	h.keys("<Esc>")
	h.settle()
	if got := h.cursor(); got != (buffer.Position{}) {
		t.Errorf("一覧の窓を閉じた後のカーソル = %+v", got)
	}
	// 逆順の範囲の確認の問いの間のホイールは、n の答えにしない。
	h.keys(":3,1d<CR>")
	h.eventually("問い", func() bool { return h.e.confirm != nil })
	h.mouse("<ScrollWheelDown>", 3, 3)
	h.click(3, 3)
	h.keys("y")
	h.settle()
	if got := h.text(); got != "日本語のテキスト\nlast" {
		t.Errorf("問いにマウスの出来事を送ってから y と答えた後 = %q", got)
	}
}

// ペインの部品の client の画面のクリックは、その client の画面の座標で解き、部品のバッファのカーソルを移す（FR-185）。
// 部品の client の端末にも、マウスの報告を求める。
//
// req: FR-185
func TestMouseComponent(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t)}
	calls.config(&cfg)
	h := start(t, cfg)
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys(":option.set mouse.enabled true<CR>:fake.pane tree<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	h.waitPaneOpened(call.view)
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("top")
	row := -1
	for i, l := range strings.Split(comp.term.screen(), "\n") {
		if strings.Contains(l, "top") {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("部品の client の画面に top が無い:\n%s", comp.term.screen())
	}
	comp.term.in <- TermEvent{Key: editcmd.Special(editcmd.CodeLeftMouse), MouseRow: row, MouseCol: 3}
	h.sent++
	h.settle()
	var line string
	h.e.do(func() {
		c := h.clientOf(peer.term)
		p := h.e.viewOf(c).panes[0]
		line = p.bs.buf.Line(p.bs.state.Cursor().Line)
	})
	if !strings.Contains(line, "top") {
		t.Errorf("部品の client の画面のクリックの後のカーソルの行 = %q", line)
	}
	ft := comp.term.fakeTerm
	ft.mu.Lock()
	on := ft.last.Mouse
	ft.mu.Unlock()
	if !on {
		t.Error("部品の client の端末にマウスの報告を求めない")
	}
}
