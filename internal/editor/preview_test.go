package editor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// previewLineAttr は、試験で色の名前 preview.line に付ける色である。
var previewLineAttr = ui.Attr{Bg: ui.RGB(9, 8, 7)}

// previewDir は、プレビューの試験のディレクトリを作る。a.go は 40 行で、n 行目（1 から）は "// line n"（1 行目は "package main"）である。
func previewDir(t *testing.T) string {
	t.Helper()
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("// line %d", i+1)
	}
	lines[0] = "package main"
	dir := writeFiles(t, map[string]string{
		"a.go": strings.Join(lines, "\n") + "\n", "b.txt": "open b\n", "c.txt": "c\n", "x1.txt": "one\n", "x2.txt": "two\n", "x3.txt": "three\n",
		"sub/x.txt": "x\n", "sub/d/y.txt": "y\n", "bin.dat": "ab\x00cd\n",
	})
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("a\n", previewMaxBytes/2+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// startPreview は、大きさ w×h の偽の端末で、dir を作業ディレクトリにしてエディタを起動し、タイマーを偽物に差し替える。
func startPreview(t *testing.T, dir string, w, h int, opts map[string]option.Value, files ...string) (*harness, *fakeTimers) {
	t.Helper()
	term := newFakeTerm()
	term.w, term.h = w, h
	cfg := Config{WorkDir: dir, Options: opts, Colors: map[string]ui.Attr{ui.StylePreviewLine: previewLineAttr}}
	hs := startTerm(t, term, cfg, files...)
	ft := &fakeTimers{}
	hs.e.do(func() { hs.e.afterFunc = ft.after })
	hs.settle()
	return hs, ft
}

// openPreviewFloat は、本体の部品として、項目 items の種類 kind の中央のポップアップを開く（拡張機能を介さない）。
func openPreviewFloat(h *harness, kind ext.ViewKind, items []ext.ViewItem) *floatState {
	var f *floatState
	h.e.do(func() {
		f = h.e.newComponentFloat(ext.ViewComponent{Kind: kind, Items: items}, "")
		f.center, f.focused = true, true
		h.e.openFloat(f)
	})
	return f
}

// pvOf は、パス path（行を付けるなら line）のプレビューに出すものを返す。
func pvOf(path string, line ...int) *ext.ViewPreview {
	pv := &ext.ViewPreview{Path: path}
	if len(line) > 0 {
		pv.Line = &line[0]
	}
	return pv
}

// fireLast は、最後に掛けたタイマーが n 番目（0 から）より後に掛かるまで待ってから、その時間が来たことにし、番号を返す。
func fireLast(h *harness, ft *fakeTimers, n int) int {
	h.t.Helper()
	h.eventually("プレビューのタイマー", func() bool { return ft.count() > n })
	i := ft.count() - 1
	ft.fire(i)
	return i
}

// waitPreview は、部品 f のプレビューが key を出し終えるまで待つ。
func waitPreview(h *harness, f *floatState, key string) {
	h.t.Helper()
	h.eventually("プレビュー "+key, func() bool { return f.preview.key == key && !f.preview.loading })
}

// 項目のプレビューに出すものに合わせて、ファイルは先頭から、行を付けたものはその行を真ん中にして色を付け、ディレクトリは中身の一覧を、
// 編集を禁止したバッファにして窓の右に出す。ファイルの種類はファイルの名前から決める（構文の色を付けるため）。
// 同じファイルの別の行へ移ったときは読み直さない。開いているファイルとバッファは、そのバッファを出す。
// 大きなファイル、読めないファイル、バイナリのファイル、空のディレクトリは、中身を出さずに理由を出す。
// プレビューの無い項目では、プレビューの欄を空にする。替えたら、前に作ったプレビューのバッファを閉じる。
//
// req: FR-149
func TestPreviewContents(t *testing.T) {
	dir := previewDir(t)
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("s\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	h, ft := startPreview(t, dir, 200, 20, nil, "b.txt")
	var bufB *bufferState
	h.e.do(func() { bufB = h.e.cur })
	bid := bufB.id
	p := func(rel string) string { return filepath.Join(dir, rel) }
	items := []ext.ViewItem{
		{ID: "a", Label: "a.go", Preview: pvOf(p("a.go"))},
		{ID: "a20", Label: "a.go:21", Preview: pvOf("a.go", 20)},
		{ID: "sub", Label: "sub", Preview: pvOf(p("sub"))},
		{ID: "big", Label: "big.txt", Preview: pvOf(p("big.txt"))},
		{ID: "bin", Label: "bin.dat", Preview: pvOf(p("bin.dat"))},
		{ID: "missing", Label: "missing", Preview: pvOf(p("missing.txt"))},
		{ID: "empty", Label: "empty", Preview: pvOf(p("empty"))},
		{ID: "b", Label: "b.txt", Preview: pvOf(p("b.txt"))},
		{ID: "buf", Label: "buffer", Preview: &ext.ViewPreview{Buffer: &bid}},
		{ID: "none", Label: "none"},
		{ID: "secret", Label: "secret", Preview: pvOf(secret)},
	}
	f := openPreviewFloat(h, ext.ViewList, items)
	n := 0
	show := func(row int) *previewState {
		t.Helper()
		h.e.do(func() { f.bs.state.SetCursor(buffer.Position{Line: row}) })
		n = fireLast(h, ft, n) + 1
		var key string
		h.e.do(func() { key = h.e.previewKey(f.rows[row].preview) })
		waitPreview(h, f, key)
		return &f.preview
	}

	pv := show(0)
	var first *bufferState
	h.e.do(func() {
		first = pv.bs
		if pv.bs == nil || !pv.owned || pv.bs.buf.Line(0) != "package main" || pv.bs.fileType() != "go" || !pv.bs.buf.ReadOnly() || pv.line != -1 || pv.title != "a.go" {
			t.Errorf("file preview: %+v", pv)
		}
	})
	h.eventually("先頭からのプレビュー", func() bool { return strings.Contains(h.term.screen(), "package main") })

	// 同じファイルの別の行へ移ると、読み直さずに、その行を真ん中に置いて色を付ける。
	pv = show(1)
	h.e.do(func() {
		if pv.bs != first || pv.line != 20 || pv.title != "a.go:21" {
			t.Errorf("same file, another line: %+v (buffer changed: %v)", pv, pv.bs != first)
		}
	})
	h.eventually("行のプレビュー", func() bool {
		sub := h.e.renderer.PreviewRenderer()
		return sub != nil && strings.Contains(h.term.screen(), "// line 21") && sub.Top() == 20-(sub.TextRows()-1)/2
	})
	if !previewRowColored(h.term, "// line 21") {
		t.Errorf("the line of the item is not colored:\n%s", h.term.screen())
	}

	pv = show(2)
	h.e.do(func() {
		if pv.bs == nil || pv.bs.buf.Text() != "d/\nx.txt" || pv.title != "sub/" || pv.bs.fileType() != "" || pv.line != -1 {
			t.Errorf("directory preview: %+v %q", pv, pv.bs.buf.Text())
		}
		if slices.Contains(h.e.bufs, first) {
			t.Error("the previous preview buffer was not closed")
		}
	})
	for _, tt := range []struct {
		row  int
		note string
	}{{3, "大きなファイル"}, {4, "バイナリのファイル"}, {5, "読めません"}, {6, "空のディレクトリ"}} {
		pv = show(tt.row)
		h.e.do(func() {
			if pv.bs != nil || !strings.Contains(pv.note, tt.note) {
				t.Errorf("row %d: buffer %v, note %q, want %q", tt.row, pv.bs != nil, pv.note, tt.note)
			}
		})
	}
	h.eventually("理由のプレビュー", func() bool { return strings.Contains(h.term.screen(), "空のディレクトリです") })

	// 開いているファイルとバッファは、写さずにそのバッファを出す。
	for _, row := range []int{7, 8} {
		pv = show(row)
		h.e.do(func() {
			if pv.bs != bufB || pv.owned {
				t.Errorf("row %d: open buffer preview: %+v", row, pv)
			}
		})
	}
	h.eventually("開いているバッファのプレビュー", func() bool { return strings.Contains(h.term.screen(), "open b") })

	pv = show(9)
	h.e.do(func() {
		if pv.bs != nil || pv.key != "" || pv.note != "" {
			t.Errorf("an item without a preview: %+v", pv)
		}
	})

	if fp, err := os.Open(secret); err == nil {
		_ = fp.Close()
		t.Log("権限の無いファイルを読める（root で動いている）ので、読めないファイルを確かめない")
		return
	}
	pv = show(10)
	h.e.do(func() {
		if pv.bs != nil || !strings.Contains(pv.note, "読めません") {
			t.Errorf("unreadable: %+v", pv)
		}
	})
}

// previewRowColored は、画面の text を含む行の、プレビューの右の端の近くの列に、色 previewLineAttr の背景が付いているかを返す
// （行の全体に色を付けるので、text より右の空いた列にも付く）。
func previewRowColored(term *fakeTerm, text string) bool {
	return previewRowColoredBy(term, text, previewLineAttr)
}

// プレビューの欄を持つ窓は、窓の幅（画面の幅の preview.windowWidthPercent %）が preview.minWindowWidth を超えるときだけ、
// 窓を広げてプレビューを出す。超えなければプレビューを開かず（タイマーも掛けない）、今までの幅の窓に一覧だけを出す。
// 欄を持たない窓（どの項目にもプレビューが無い）は、境目に依らず出さない。出している間に設定で狭くしたら、プレビューを閉じる。
//
// req: FR-149
func TestPreviewWidthRule(t *testing.T) {
	dir := previewDir(t)
	// 120 列の画面の 90% は 108 列である。
	h, ft := startPreview(t, dir, 120, 12, map[string]option.Value{option.PreviewMinWindowWidth: option.IntValue(108)})
	f := openPreviewFloat(h, ext.ViewTree, []ext.ViewItem{{ID: "a", Label: "a.go", Preview: pvOf(filepath.Join(dir, "a.go"))}})
	h.settle()
	h.e.do(func() {
		if h.e.previewShown(f) || ft.count() != 0 {
			t.Errorf("window 108 <= 108: shown %v, timers %d", h.e.previewShown(f), ft.count())
		}
	})
	if s := h.term.screen(); strings.Contains(s, "│") || strings.Contains(s, "package main") {
		t.Errorf("preview drawn under the threshold:\n%s", s)
	}
	setOpt := func(name string, v int) {
		h.e.do(func() {
			d, _ := option.Lookup(name)
			if err := h.e.options.Set(d, option.IntValue(v)); err != nil {
				t.Fatal(err)
			}
		})
	}
	setOpt(option.PreviewMinWindowWidth, 107)
	fireLast(h, ft, 0)
	waitPreview(h, f, "p:"+filepath.Join(dir, "a.go")+"#-")
	h.eventually("境目を超えた窓のプレビュー", func() bool {
		s := h.term.screen()
		return strings.Contains(s, "│") && strings.Contains(s, "package main")
	})
	var bs *bufferState
	h.e.do(func() { bs = f.preview.bs })

	// 狭くしたら、プレビューを閉じる。
	setOpt(option.PreviewWindowWidthPercent, 80)
	fireLast(h, ft, 1)
	waitPreview(h, f, "")
	h.e.do(func() {
		if slices.Contains(h.e.bufs, bs) {
			t.Error("the preview buffer remains after the window got narrow")
		}
	})

	// プレビューの欄を持たない窓は、境目に依らず出さない。
	setOpt(option.PreviewMinWindowWidth, 0)
	setOpt(option.PreviewWindowWidthPercent, 100)
	h.keys("<Esc>")
	h.settle()
	before := ft.count()
	g := openPreviewFloat(h, ext.ViewList, []ext.ViewItem{{ID: "k", Label: "key"}})
	h.settle()
	h.e.do(func() {
		if h.e.previewShown(g) || ft.count() != before {
			t.Errorf("a window without previews: shown %v, timers %d -> %d", h.e.previewShown(g), before, ft.count())
		}
	})
}

// カーソルを続けて動かしている間は、項目が替わるたびにタイマーを掛け直し、前のタイマーを止める。止めたタイマーの時間が来ても開かない。
// 時間が来たら（previewDelay の後）、そのときのカーソルの項目を開く。ポップアップを閉じたら、タイマーを止め、プレビューのバッファを閉じる。
//
// req: FR-149
func TestPreviewDelayAndClose(t *testing.T) {
	dir := previewDir(t)
	h, ft := startPreview(t, dir, 200, 12, nil)
	p := func(rel string) string { return filepath.Join(dir, rel) }
	key := func(rel string) string { return "p:" + p(rel) + "#-" }
	f := openPreviewFloat(h, ext.ViewList, []ext.ViewItem{
		{ID: "1", Label: "x1", Preview: pvOf(p("x1.txt"))},
		{ID: "2", Label: "x2", Preview: pvOf(p("x2.txt"))},
		{ID: "3", Label: "x3", Preview: pvOf(p("x3.txt"))},
	})
	fireLast(h, ft, 0)
	waitPreview(h, f, key("x1.txt"))

	h.keys("j")
	h.settle()
	h.eventually("x2 のタイマー", func() bool { return ft.count() == 2 })
	h.keys("j")
	h.settle()
	h.eventually("x3 のタイマー", func() bool { return ft.count() == 3 })
	ft.mu.Lock()
	stopped, d := ft.stopped[1], ft.ds[2]
	ft.mu.Unlock()
	if !stopped || d != previewDelay {
		t.Errorf("timer for x2 stopped %v, delay %v", stopped, d)
	}
	ft.fire(1) // 止めたタイマー
	h.settle()
	h.e.do(func() {
		if f.preview.key != key("x1.txt") || f.preview.bs.buf.Text() != "one" {
			t.Errorf("a stopped timer opened %q", f.preview.key)
		}
	})
	ft.fire(2)
	waitPreview(h, f, key("x3.txt"))
	var bs *bufferState
	h.e.do(func() { bs = f.preview.bs })

	// 閉じたら、掛けたタイマーを止め、プレビューのバッファを閉じる。
	h.keys("k")
	h.settle()
	h.eventually("x2 のタイマー", func() bool { return ft.count() == 4 })
	h.keys("<Esc>")
	h.settle()
	ft.mu.Lock()
	stopped = ft.stopped[3]
	ft.mu.Unlock()
	h.e.do(func() {
		if h.e.float != nil || slices.Contains(h.e.bufs, bs) || !stopped {
			t.Errorf("after closing: float %v, preview buffer open %v, timer stopped %v", h.e.float != nil, slices.Contains(h.e.bufs, bs), stopped)
		}
	})
	ft.fire(3)
	h.settle()
	h.e.do(func() {
		for _, b := range h.e.bufs {
			if b.popup {
				t.Errorf("a popup buffer %q remains", b.title)
			}
		}
	})
}

// プレビューを出している窓では、<C-w>w でフォーカスをプレビューへ移し、ノーマルモードのキーで読める（FR-71）。
// <Esc> と <C-w>w で一覧へ戻り、ポップアップは閉じない。開いているバッファのプレビューへ移るときは、編集を禁止した写しに替え、
// タブのバッファのカーソルと中身を変えない。プレビューの中の :q はポップアップを閉じる。
//
// req: FR-149, FR-71
func TestPreviewFocus(t *testing.T) {
	dir := previewDir(t)
	h, ft := startPreview(t, dir, 200, 20, nil, "b.txt")
	var bufB *bufferState
	h.e.do(func() { bufB = h.e.cur })
	f := openPreviewFloat(h, ext.ViewList, []ext.ViewItem{
		{ID: "a", Label: "a.go", Preview: pvOf(filepath.Join(dir, "a.go"), 20)},
		{ID: "b", Label: "b.txt", Preview: pvOf(filepath.Join(dir, "b.txt"))},
	})
	fireLast(h, ft, 0)
	waitPreview(h, f, "p:"+filepath.Join(dir, "a.go")+"#20")

	h.keys("<C-w>w")
	h.settle()
	h.keys("j/line 30<CR>")
	h.settle()
	h.e.do(func() {
		if h.e.cur != f.preview.bs || h.e.cur.state.Cursor().Line != 29 {
			t.Errorf("focus in the preview: cur is preview %v, cursor %+v", h.e.cur == f.preview.bs, h.e.cur.state.Cursor())
		}
	})
	h.keys("<Esc>")
	h.settle()
	h.e.do(func() {
		if h.e.float != f || h.e.cur != f.bs || f.preview.focus {
			t.Errorf("<Esc> in the preview: float %v, cur is list %v", h.e.float == f, h.e.cur == f.bs)
		}
	})
	h.keys("<C-w>w")
	h.settle()
	h.keys("<C-w>w")
	h.settle()
	h.e.do(func() {
		if h.e.cur != f.bs {
			t.Error("<C-w>w in the preview did not go back to the list")
		}
	})

	// 開いているバッファのプレビューへは、写しに替えてから移る。
	h.keys("j")
	h.settle()
	fireLast(h, ft, 1)
	waitPreview(h, f, "p:"+filepath.Join(dir, "b.txt")+"#-")
	h.keys("<C-w>w")
	h.settle()
	h.keys("x")
	h.settle()
	h.e.do(func() {
		if f.preview.bs == bufB || !f.preview.owned || h.e.cur != f.preview.bs || !f.preview.bs.buf.ReadOnly() {
			t.Errorf("focus into an open buffer: copy %v, owned %v", f.preview.bs != bufB, f.preview.owned)
		}
		if bufB.buf.Text() != "open b" || f.preview.bs.buf.Text() != "open b" {
			t.Errorf("edits in the preview: tab %q, preview %q", bufB.buf.Text(), f.preview.bs.buf.Text())
		}
	})
	h.keys(":q<CR>")
	h.settle()
	h.e.do(func() {
		if h.e.float != nil || h.e.cur != bufB {
			t.Errorf(":q in the preview: float %v, cur is the tab %v", h.e.float != nil, h.e.cur == bufB)
		}
	})
}

// 端末多重化ソフトのポップアップに出す中央のポップアップも、編集の client の画面に描く窓と同じ規則と同じ設定の項目で決める。
// ポップアップの幅の百分率は端末の幅に対するものなので、出すかも、ポップアップを出す端末の幅（Config.PopupScreenWidth）の 90% で決め、
// 出すなら 90% の幅で開かせて、ポップアップの部品の client の画面（枠線の内側）で一覧の右にプレビューを描く。
// 開いた client のペインの幅では決めない（200 列の端末を分割した 100 列のペインからでも出す。PR #298 のレビューの必須 1）。
// 出さない（端末の 90% が境目を超えない、プレビューの欄が無い）なら、既定の大きさで開かせ、ポップアップの画面が広くてもプレビューを描かない。
// 端末の幅を得られなければ、ペインの幅で代える。
//
// req: FR-149, FR-67
func TestPreviewMuxPopupWidth(t *testing.T) {
	dir := previewDir(t)
	var calls muxCalls
	cfg := Config{WorkDir: dir}
	calls.config(&cfg)
	// 端末の幅は、ペインの識別子ごとに決める（無ければ得られない）。
	var mu sync.Mutex
	screens := map[string]int{}
	cfg.PopupScreenWidth = func(from string) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		if w, ok := screens[from]; ok {
			return w, nil
		}
		return 0, errors.New("no screen width")
	}
	h := start(t, cfg)
	items := []ext.ViewItem{{ID: "a", Label: "a.go", Preview: pvOf(filepath.Join(dir, "a.go"))}}
	n := ClientID(1)
	// open は、幅 width のペインの client を、幅 screen の端末（0 なら得られない）に繋いで、中央のポップアップを開く。
	open := func(width, screen int, items []ext.ViewItem) (*floatState, muxCall) {
		t.Helper()
		n++
		pane := fmt.Sprintf("%%%d", n)
		if screen > 0 {
			mu.Lock()
			screens[pane] = screen
			mu.Unlock()
		}
		term := &exitTerm{fakeTerm: newFakeTerm()}
		term.w, term.h = width, 24
		h.peers = append(h.peers, term.fakeTerm)
		if err := h.e.Attach(term, ClientInfo{ID: n, Pane: pane}); err != nil {
			t.Fatal(err)
		}
		var f *floatState
		h.e.do(func() {
			h.e.withView(h.clientOf(term), func() {
				f = h.e.newComponentFloat(ext.ViewComponent{Kind: ext.ViewList, Items: items}, "")
				f.center, f.focused = true, true
				h.e.openFloat(f)
			})
		})
		h.eventually("OpenPopup", func() bool { c, ok := calls.last(false); return ok && c.view == f.id })
		c, _ := calls.last(false)
		return f, c
	}
	component := func(view uint64, width int) *exitTerm {
		t.Helper()
		term := &exitTerm{fakeTerm: newFakeTerm()}
		term.w, term.h = width, 20
		h.peers = append(h.peers, term.fakeTerm)
		if err := h.e.AttachComponent(term, ComponentInfo{View: view}); err != nil {
			t.Fatal(err)
		}
		return term
	}

	// shown は、ポップアップの部品の client（幅 inner。端末の百分率の幅から枠線の 2 列を引いたもの）を繋ぎ、プレビューを描くまで待つ。
	shown := func(what string, f *floatState, call muxCall, inner int) {
		t.Helper()
		comp := component(call.view, inner)
		h.eventually(what, func() bool {
			s := comp.screen()
			return f.preview.bs != nil && strings.Contains(s, "│") && strings.Contains(s, "package main")
		})
	}
	// hidden は、ポップアップの部品の client（幅 inner）を繋ぎ、プレビューを開かず描かないことを確かめる。
	hidden := func(what string, f *floatState, call muxCall, inner int) {
		t.Helper()
		comp := component(call.view, inner)
		h.eventually(what+"の一覧", func() bool { return strings.Contains(comp.screen(), "a.go") })
		h.settle()
		h.e.do(func() {
			if f.preview.loading || f.preview.key != "" || strings.Contains(comp.screen(), "│") {
				t.Errorf("%s: a preview: %+v\n%s", what, f.preview, comp.screen())
			}
		})
	}
	for _, tt := range []struct {
		what         string
		pane, screen int
		width        int // OpenPopup の幅の百分率（0 は既定）
		inner        int // 開いたポップアップの枠線の内側の列数
		show         bool
	}{
		{"200 列の端末の 200 列のペイン", 200, 200, 90, 178, true},
		{"200 列の端末を分割した 100 列のペイン", 100, 200, 90, 178, true},
		{"113 列の端末（90% は 101 列）", 113, 113, 90, 99, true},
		{"112 列の端末（90% は 100 列）", 112, 112, 0, 87, false},
		{"90 列の端末", 90, 90, 0, 70, false},
		{"端末の幅を得られない 200 列のペイン", 200, 0, 90, 178, true},
		{"端末の幅を得られない 100 列のペイン", 100, 0, 0, 158, false},
	} {
		f, call := open(tt.pane, tt.screen, items)
		if call.popup.Width != tt.width {
			t.Errorf("%s: OpenPopup width = %d, want %d", tt.what, call.popup.Width, tt.width)
		}
		if tt.show {
			shown(tt.what, f, call, tt.inner)
		} else {
			hidden(tt.what, f, call, tt.inner)
		}
	}

	// プレビューの欄の無い一覧は、既定の大きさで開かせる。
	_, call := open(200, 200, []ext.ViewItem{{ID: "k", Label: "key"}})
	if call.popup.Width != 0 {
		t.Errorf("OpenPopup width without previews = %d, want 0 (the default)", call.popup.Width)
	}
}

// 分割したペインの部品でプレビューへフォーカスを移すと、ペインのステータス行とコマンドラインは、キーを受けるプレビューのバッファの
// もの（名前、モード、打ちかけの / の検索）になる。<Esc> で一覧へ戻ると、一覧のバッファのものに戻る（PR #298 のレビューの必須 2）。
//
// req: FR-149, FR-71
func TestPreviewFocusInPane(t *testing.T) {
	dir := previewDir(t)
	var calls muxCalls
	cfg := Config{WorkDir: dir}
	calls.config(&cfg)
	h := start(t, cfg)
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	var p *floatState
	h.e.do(func() {
		h.e.withView(h.clientOf(peer.term), func() {
			p = h.e.newComponentFloat(ext.ViewComponent{Kind: ext.ViewTree, Title: "tree", Items: []ext.ViewItem{
				{ID: "a", Label: "a.go", Preview: pvOf(filepath.Join(dir, "a.go"), 20)},
			}}, "")
			// pane/open の分割したペインと同じに置く（paneOpenRequest）。
			h.e.floatSeq++
			p.id, p.pane, p.base = h.e.floatSeq, true, h.e.cur
			p.shownTab = h.e.tab()
			p.bs.state.Viewport = floatViewport{f: p, r: h.e.renderer}
			h.e.panes = append(h.e.panes, p)
		})
	})
	term := &exitTerm{fakeTerm: newFakeTerm()}
	term.w, term.h = 130, 20
	h.peers = append(h.peers, term.fakeTerm)
	if err := h.e.AttachComponent(term, ComponentInfo{View: p.id, Pane: true, MuxPane: "%p1"}); err != nil {
		t.Fatal(err)
	}
	comp := &compPeer{h: h, term: term}
	h.eventually("ペインのプレビュー", func() bool { return p.preview.bs != nil && strings.Contains(term.screen(), "// line 21") })
	lastRows := func() string {
		rows := strings.Split(term.screen(), "\n")
		return strings.Join(rows[len(rows)-2:], "\n")
	}
	comp.keys("<C-w>w/func")
	h.eventually("プレビューのバッファのステータス行とコマンドライン", func() bool {
		s := lastRows()
		return strings.Contains(s, "a.go:21") && strings.Contains(s, "/func") && !strings.Contains(s, "tree")
	})
	comp.keys("<Esc><Esc>")
	h.eventually("一覧へ戻ったステータス行", func() bool {
		s := lastRows()
		return !p.preview.focus && strings.Contains(s, "tree") && !strings.Contains(s, "/func")
	})
}

// 標準のファインダーとファイラの拡張機能の項目にプレビューが出る。200 列の画面では <Space>ff のファイルの先頭、<Space>fg の一致した行、
// <Space>e のファイラのファイルとディレクトリを出し、90 列の画面では出さない。分割したペイン（ペインの部品の client の画面が 36 列）の
// ファイラにも出さない。どれも同じ幅の規則による。
//
// req: FR-149
func TestStandardPreviews(t *testing.T) {
	finder := buildFinder(t)
	filer := buildStdExt(t, "vi-imitation-ext-filer")
	dir := previewDir(t)
	specs := []exthost.Spec{finder, filer}
	wide := func(t *testing.T, w int) (*harness, func() *previewState) {
		term := newFakeTerm()
		term.w, term.h = w, 24
		h := startTerm(t, term, Config{WorkDir: dir, Extensions: specs}, "c.txt")
		return h, func() *previewState {
			if h.e.float == nil {
				return nil
			}
			return &h.e.float.preview
		}
	}

	h, pv := wide(t, 200)
	h.keys("<Space>ff")
	h.eventually("ファイルのファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
	h.keys("a.go")
	h.eventually("ファイルのプレビュー", func() bool {
		p := pv()
		return p != nil && p.key == "p:"+filepath.Join(dir, "a.go")+"#-" && p.bs != nil && p.bs.buf.Line(0) == "package main"
	})
	h.keys("<Esc>")
	h.settle()
	if _, err := exec.LookPath("rg"); err == nil {
		h.keys("<Space>fg")
		h.eventually("検索のファインダー", func() bool { _, _, _, ok := finderOf(h.e); return ok })
		h.keys("line 33$")
		h.eventually("検索のプレビュー", func() bool {
			p := pv()
			return p != nil && p.key == "p:"+filepath.Join(dir, "a.go")+"#32" && p.line == 32
		})
		h.eventually("一致した行の色", func() bool { return previewRowColoredBy(h.term, "// line 33", ui.DefaultTheme()[ui.StylePreviewLine]) })
		h.keys("<Esc>")
		h.settle()
	}
	// ファイラは、今のファイルの項目のプレビューを出し、ディレクトリへ移ると中身の一覧を出す。
	h.keys("<Space>e")
	h.eventually("ファイラのファイルのプレビュー", func() bool {
		p := pv()
		return p != nil && p.key == "p:"+filepath.Join(dir, "c.txt")+"#-" && p.bs != nil
	})
	h.keys("/^. sub<CR>")
	h.eventually("ファイラのディレクトリのプレビュー", func() bool {
		p := pv()
		return p != nil && p.key == "p:"+filepath.Join(dir, "sub")+"#-" && p.bs != nil && p.bs.buf.Text() == "d/\nx.txt"
	})
	h.keys("<Esc>")
	h.settle()

	// 分割したペインのファイラ（36 列のペインの部品の client）には出さない。
	var calls muxCalls
	cfg := Config{WorkDir: dir, Extensions: specs}
	calls.config(&cfg)
	hp := start(t, cfg, filepath.Join(dir, "c.txt"))
	peer := hp.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys("<Space>E")
	hp.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	term := &exitTerm{fakeTerm: newFakeTerm()}
	term.w, term.h = 36, 24
	hp.peers = append(hp.peers, term.fakeTerm)
	if err := hp.e.AttachComponent(term, ComponentInfo{View: call.view, Pane: true, MuxPane: "%p1"}); err != nil {
		t.Fatal(err)
	}
	hp.eventually("ペインの木", func() bool { return strings.Contains(term.screen(), "c.txt") })
	hp.settle()
	hp.e.do(func() {
		ps := hp.e.viewOf(hp.clientOf(peer.term)).panes
		if len(ps) != 1 || !ps[0].hasPreview || hp.e.previewShown(ps[0]) || ps[0].preview.loading || ps[0].preview.key != "" {
			t.Errorf("a preview in the 36-column pane: %d panes", len(ps))
		}
	})

	// 90 列の画面では、ファインダーにもファイラにも出さない。
	h90, pv90 := wide(t, 90)
	h90.keys("<Space>ff")
	h90.eventually("ファイルの一覧", func() bool { return finderText(h90.e) != "" })
	h90.settle()
	h90.e.do(func() {
		if p := pv90(); p == nil || p.loading || p.key != "" || h90.e.previewShown(h90.e.float) {
			t.Errorf("a preview on the 90-column screen: %+v", p)
		}
	})
	h90.keys("<Esc>")
	h90.keys("<Space>e")
	h90.eventually("ファイラ", func() bool { f := h90.e.float; return f != nil && f.kind == floatTree && f.hasPreview })
	h90.settle()
	h90.e.do(func() {
		if p := pv90(); p.loading || p.key != "" {
			t.Errorf("a preview of the filer on the 90-column screen: %+v", p)
		}
	})
}

// previewRowColoredBy は、previewRowColored の、色を a にした形である。
func previewRowColoredBy(term *fakeTerm, text string, a ui.Attr) bool {
	term.mu.Lock()
	defer term.mu.Unlock()
	g := term.last.Grid
	if g == nil {
		return false
	}
	for row := range g.Height() {
		// プレビューは一覧の右の縦の線より右にあるので、そこだけを見る（一覧の項目の表示の文字列にも同じ文字が出るため）。
		line, sep := "", false
		for c := range g.Width() {
			if s := g.At(row, c).Text; sep {
				line += s
			} else {
				sep = s == "│"
			}
		}
		if strings.Contains(line, text) {
			return g.At(row, g.Width()-12).Attr.Bg == a.Bg
		}
	}
	return false
}
