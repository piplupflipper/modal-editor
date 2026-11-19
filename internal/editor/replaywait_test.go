package editor

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 再生の途中で拡張機能の結果を待つ間も、イベントループは止まらず、別の client のキーを処理する（#224）。
// 待っている client のキーは、再生が止まるか続け終えてから処理する。<C-c> は待ちを取り消して再生を止める。
//
// req: FR-19
func TestReplayWaitKeepsOtherClients(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\nb2\nc3\n", "b.txt": "bb\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), ReplayTimeout: 2 * time.Second}, "a.txt")
	h.e.do(func() { h.e.cur.state.Registers().Set('a', command.Register{Text: "dihj"}) })
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	// textOf は、イベントループの goroutine で呼ぶ（eventually の cond の中）。
	textOf := func(name string) string {
		for _, bs := range h.e.bufs {
			if filepath.Base(bs.path) == name {
				return bs.buf.Text()
			}
		}
		return "<none>"
	}

	// fake.hang（ih）は応答しないので、A の再生は期限まで待つ。
	h.keys("@ax")
	h.eventually("A の待ち", func() bool { return h.e.replayPending() })
	b.keys("x")
	h.eventually("待つ間の B の x", func() bool { return textOf("b.txt") == "b" })
	var pending bool
	var a string
	h.e.do(func() { pending, a = h.e.replayPending(), textOf("a.txt") })
	if !pending || a != "a1\nb2\nc3" {
		t.Fatalf("B の x の後: pending = %v, a.txt = %q（A の x は待ちの後に処理する）", pending, a)
	}
	// 期限で再生が止まり（後ろの j は行わない）、待たせた A の x を 1 行目で処理する。誤りは履歴に残る。
	h.eventually("期限の後の A の x", func() bool { return textOf("a.txt") == "1\nb2\nc3" })
	h.settle()
	var timedOut bool
	h.e.do(func() {
		timedOut = slices.ContainsFunc(h.e.msgHistory, func(m ui.Message) bool {
			return strings.Contains(m.Text, "再生の途中で拡張機能 fake の fake.hang の結果を 2s 待ったので、再生を止めました")
		})
	})
	if !timedOut {
		t.Errorf("期限で止まった誤りが履歴に無い")
	}

	// <C-c> は待ちを取り消し、再生を止める。<C-c> より前に打ったキー（x）は捨てる。
	h.keys("@a")
	h.eventually("2 回目の待ち", func() bool { return h.e.replayPending() })
	h.keys("x<C-c>")
	h.settle()
	if got := h.text(); got != "1\nb2\nc3" {
		t.Errorf("<C-c> の後: a.txt = %q", got)
	}
	if msg := h.message(); msg != "中断しました" {
		t.Errorf("<C-c> の後: message = %q", msg)
	}
}

// 拡張機能の結果を待つ前と後の変更は、1 つのアンドゥの単位になり、再生の後の 1 回の u で全体を戻す（Vim 9.1 と同じ。#229 のレビューの必須 1）。
//
// req: FR-07, FR-19
func TestReplayWaitUndo(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\nb2\nc3\nd4\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	// fake.slow（is）は slowDelay の後に応答する。
	h.e.do(func() { h.e.cur.state.Registers().Set('a', command.Register{Text: "xjdisjx"}) })
	h.keys("@a")
	h.settle()
	if got := h.text(); got != "1\n\n3\nd4" {
		t.Fatalf("@a: text = %q", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "a1\nb2\nc3\nd4" {
		t.Errorf("u: text = %q, want the whole @a undone", got)
	}
}

// 再生の続きは、待つ間に別の client が打った変更ではなく、止めたときのドットリピートの値で続ける（#229 のレビューの必須 2）。
//
// req: FR-19, FR-58
func TestReplayWaitKeepsDot(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\nb2\nc3\n", "b.txt": "x\ny\nz\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.e.do(func() { h.e.cur.state.Registers().Set('a', command.Register{Text: "xyisj."}) })
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	textOf := func(name string) string {
		for _, bs := range h.e.bufs {
			if filepath.Base(bs.path) == name {
				return bs.buf.Text()
			}
		}
		return "<none>"
	}
	h.keys("@a")
	h.eventually("A の待ち", func() bool { return h.e.replayPending() })
	b.keys("dd")
	h.eventually("待つ間の B の dd", func() bool { return textOf("b.txt") == "y\nz" })
	h.settle()
	if got := h.bufferText("a.txt"); got != "1\n2\nc3" {
		t.Errorf("a.txt = %q, want %q（再生の中の . は x を繰り返す）", got, "1\n2\nc3")
	}
}

// 再生の途中で結果を待つ間に、止めた操作のバッファが変わったら、届いた結果を使わずに再生を止めて知らせる。
//
// req: FR-19
func TestReplayWaitStale(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a1\nb2\nc3\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	// 拡張機能を先に起動しておく（起動の途中に変えると、問い合わせが古い版で届いて失敗し、ここで確かめたいことと違う理由で止まる）。
	h.keys(`\s`)
	h.eventually("fake の起動", func() bool { return h.state("fake") == exthost.StateRunning })
	h.settle()
	// fake.slow（is）は slowDelay の後に応答する。
	h.e.do(func() { h.e.cur.state.Registers().Set('a', command.Register{Text: "disj"}) })
	h.keys("@a")
	h.eventually("待ち", func() bool { return h.e.replayPending() })
	h.e.do(func() {
		bs := h.e.cur
		tx := buffer.Transaction{BaseVersion: bs.buf.Version(), Edits: []buffer.Edit{{Text: "z"}}}
		if _, err := h.e.applyExternal(bs, tx); err != nil {
			t.Error(err)
		}
	})
	h.settle()
	if got := h.text(); got != "za1\nb2\nc3" {
		t.Errorf("text = %q", got)
	}
	if msg := h.message(); !strings.Contains(msg, "結果を待つ間にバッファかカーソルが変わったので、再生を止めました") {
		t.Errorf("message = %q", msg)
	}
}
