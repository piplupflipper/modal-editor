package editor

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// fakeTimers は、Editor.afterFunc に差し替える偽のタイマーである。時間を待たず、fire で呼ぶまで f を呼ばない。
type fakeTimers struct {
	mu      sync.Mutex
	fs      []func()
	ds      []time.Duration
	stopped []bool
}

// after は、Editor.afterFunc の偽物である。
func (ft *fakeTimers) after(d time.Duration, f func()) func() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	i := len(ft.fs)
	ft.fs, ft.ds, ft.stopped = append(ft.fs, f), append(ft.ds, d), append(ft.stopped, false)
	return func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		ft.stopped[i] = true
		return true
	}
}

// count は、掛けたタイマーの数を返す。
func (ft *fakeTimers) count() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.fs)
}

// fire は、i 番目に掛けたタイマーの時間が来たことにする。イベントループの外の goroutine（テスト）から呼ぶ。
func (ft *fakeTimers) fire(i int) {
	ft.mu.Lock()
	f := ft.fs[i]
	ft.mu.Unlock()
	f()
}

// startFlash は、a.txt に text を書いて、yank.highlightMs を ms にしたエディタを起動し、タイマーを偽物に差し替える。
// 色の名前 yank の色を yank に、visual の色を vis にする。
func startFlash(t *testing.T, text string, ms int) (*harness, *fakeTimers) {
	t.Helper()
	dir := writeFiles(t, map[string]string{"a.txt": text})
	cfg := Config{WorkDir: dir, Colors: map[string]ui.Attr{ui.StyleYank: flashAttr, ui.StyleVisual: {Bg: ui.RGB(4, 5, 6)}}}
	if ms != 0 {
		cfg.Options = map[string]option.Value{option.YankHighlightMs: option.IntValue(ms)}
	}
	h := start(t, cfg, "a.txt")
	ft := &fakeTimers{}
	h.e.do(func() { h.e.afterFunc = ft.after })
	h.settle()
	return h, ft
}

// flashAttr は、試験で色の名前 yank に付ける色である。
var flashAttr = ui.Attr{Bg: ui.RGB(1, 2, 3)}

// flashRows は、画面のバッファの 1 行目から 3 行目（画面の最初の行はタブの行）の、色 flashAttr のセルの印を "|" でつないで返す。
func flashRows(h *harness) string {
	return searchMarks(h.term, 1, flashAttr) + "|" + searchMarks(h.term, 2, flashAttr) + "|" + searchMarks(h.term, 3, flashAttr)
}

// y のオペレーター（yy、Y、yiw、ビジュアルモードの y）でヤンクした範囲に、yank.highlightMs のミリ秒の間だけ色の名前 yank の色を付け、
// 時間が来たら消す。新しくヤンクすると、前のヤンクの色を外して付け直す。
// d、c、x、マクロの再生と . の中では付けない（nvim 0.12.5 の vim.hl.on_yank と同じ条件）。
//
// req: FR-140
func TestYankHighlight(t *testing.T) {
	h, ft := startFlash(t, "abc def\nghi jkl\nmno\n", 300)
	steps := []struct {
		keys   string
		want   string
		timers int // それまでに掛けたタイマーの数
	}{
		{"yy", "^^^^^^^||", 1},
		{"jY", "|^^^^^^^|", 2}, // Y は y$（FR-146）。カーソルは行頭なので行の全体
		{"wyiw", "|    ^^^|", 3},
		{"0vly", "|^^|", 4},
		{"Vjy", "|^^^^^^^|^^^", 5}, // カーソルは y を押したときの 3 行目に残る（FR-129）
		{"dd", "||", 5},
		{"u", "||", 5},
		{"x", "||", 5},
		{"cw<Esc>", "||", 5},
		{"ggqayiwq", "^^^||", 6}, // 記録している間のヤンクは、再生の中ではない
		{"j@a", "^^^||", 6},      // 再生の中のヤンクは付けず、前のヤンクの色も外さない（nvim の on_yank も、外す前に戻る）
		{"x.", "||", 6},
	}
	for _, s := range steps {
		h.keys(s.keys)
		h.settle()
		h.eventually(s.keys, func() bool { return flashRows(h) == s.want })
		if got := ft.count(); got != s.timers {
			t.Errorf("%s: タイマー = %d, want %d", s.keys, got, s.timers)
		}
	}
	ft.mu.Lock()
	for i, d := range ft.ds {
		if d != 300*time.Millisecond {
			t.Errorf("%d 番目のタイマー = %v, want 300ms", i, d)
		}
	}
	// 新しいヤンクは、前のヤンクの色のタイマーを止めて外す。最後のもの（qayiwq）だけが止まっていない。
	for i, st := range ft.stopped {
		if want := i < len(ft.stopped)-1; st != want {
			t.Errorf("%d 番目のタイマーを止めたか = %v, want %v", i, st, want)
		}
	}
	ft.mu.Unlock()

	// 時間が来たら消す。外した後に、止める前のタイマー（置き換えた前の色付けのもの）が来ても何もしない。
	h.keys("ggyy")
	h.settle()
	h.eventually("yy", func() bool { return flashRows(h) == "^^^^^^^||" })
	ft.fire(ft.count() - 2)
	h.settle()
	h.eventually("前のタイマー", func() bool { return flashRows(h) == "^^^^^^^||" })
	ft.fire(ft.count() - 1)
	h.eventually("時間が来たら消す", func() bool { return flashRows(h) == "||" })
	h.e.do(func() {
		if n := len(h.e.cur.flashes); n != 0 {
			t.Errorf("flashes = %d", n)
		}
	})
}

// ヤンクした範囲の色は、そのバッファが変わったら（本体の編集、外からの編集、アンドゥ）、時間が来る前でも消す。
// 選択の色はヤンクした範囲の色の上に重ねる。
//
// req: FR-140
func TestYankHighlightClearedByChange(t *testing.T) {
	h, _ := startFlash(t, "abc def\nghi\n", 300)
	for _, s := range []struct{ name, keys string }{
		{"本体の編集", "A!<Esc>"},
		{"アンドゥ", "u"},
	} {
		h.keys("yy")
		h.settle()
		h.eventually(s.name+"の前", func() bool { return strings.HasPrefix(flashRows(h), "^^^^^^^") })
		h.keys(s.keys)
		h.settle()
		h.eventually(s.name, func() bool { return flashRows(h) == "||" })
	}

	// 外からの編集（拡張機能の buffer/edit と buffer/willSave の適用。applyExternal）もバージョン番号を進めるので、同じく消える。
	h.keys("yy")
	h.settle()
	h.eventually("外からの編集の前", func() bool { return strings.HasPrefix(flashRows(h), "^^^^^^^") })
	h.e.do(func() {
		bs := h.e.cur
		tx := buffer.Transaction{BaseVersion: bs.buf.Version(), Edits: []buffer.Edit{{Range: buffer.Range{Start: buffer.Position{Line: 1}, End: buffer.Position{Line: 1}}, Text: "x"}}}
		if _, err := h.e.applyExternal(bs, tx); err != nil {
			t.Error(err)
		}
	})
	h.eventually("外からの編集", func() bool { return flashRows(h) == "||" })

	// 選択と重なったところは選択の色にする。カーソルの文字（0:1）には選択の色を重ねないので、ヤンクした範囲の色が出る。
	h.keys("0yyvl")
	h.settle()
	h.eventually("選択との重なり", func() bool { return flashRows(h) == " ^^^^^^||" })
}

// yank.highlightMs が 0（既定）なら、ヤンクしても色を付けず、タイマーも掛けない。
// 実行中に :option.set で変えた値は次のヤンクから効き、範囲（0 から 10000）の外の値は誤りにして値を変えない。
//
// req: FR-140
func TestYankHighlightOption(t *testing.T) {
	h, ft := startFlash(t, "abc\n", 0)
	h.keys("yy")
	h.settle()
	h.eventually("既定", func() bool { return flashRows(h) == "||" })
	if n := ft.count(); n != 0 {
		t.Errorf("既定でタイマー = %d", n)
	}
	for _, s := range []struct{ keys, code string }{
		{":option.set yank.highlightMs -1<CR>", "E487"},
		{":option.set yank.highlightMs 10001<CR>", "E474"},
	} {
		h.keys(s.keys)
		h.settle()
		h.e.do(func() {
			if !strings.HasPrefix(h.e.message.Text, s.code) {
				t.Errorf("%s: message = %q, want %s", s.keys, h.e.message.Text, s.code)
			}
			if got := h.e.options.Get(option.YankHighlightMs).Int(); got != 0 {
				t.Errorf("%s: yank.highlightMs = %d, want 0（変えない）", s.keys, got)
			}
		})
	}
	h.keys(":option.set yank.highlightMs 50<CR>yy")
	h.settle()
	h.eventually("実行中に変えた値", func() bool { return flashRows(h) == "^^^||" })
	ft.mu.Lock()
	if len(ft.ds) != 1 || ft.ds[0] != 50*time.Millisecond {
		t.Errorf("タイマー = %v, want [50ms]", ft.ds)
	}
	ft.mu.Unlock()
}

// 本物のタイマー（time.AfterFunc）でも、時間が来たらイベントループに戻して色を外し、描き直す。
//
// req: FR-140
func TestYankHighlightExpires(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{
		WorkDir: dir, Colors: map[string]ui.Attr{ui.StyleYank: flashAttr},
		Options: map[string]option.Value{option.YankHighlightMs: option.IntValue(20)},
	}, "a.txt")
	h.settle()
	h.keys("yy")
	h.settle()
	h.eventually("時間が来たら消す", func() bool { return flashRows(h) == "||" && len(h.e.cur.flashes) == 0 })
}
