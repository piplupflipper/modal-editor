package editor

import (
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/command"
)

// paste は、端末から貼り付けのイベントを送る。
func (h *harness) paste(text string) {
	h.t.Helper()
	h.term.in <- TermEvent{Paste: text}
	h.sent++
}

// TestPaste は、端末の貼り付けを、ノーマルモードでもコマンドとして実行せずに入れ（#114）、
// 利用者のキーマップを通さず、キーマップの接頭辞を待っていれば先に確定させることを確かめる。
func TestPaste(t *testing.T) {
	cfg := Config{
		Keymap: map[string]map[string]string{
			"normal": {"lx": "dd"},
			"insert": {"jk": "<Esc>"},
		},
		KeyTimeout: time.Hour, // 時間切れでは確定させない
	}
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n"})
	cfg.WorkDir = dir
	h := start(t, cfg, "a.txt")

	h.paste("dd")
	h.settle()
	if got := h.text(); got != "ddone\ntwo" {
		t.Fatalf("ノーマルモードの貼り付け: text = %q", got)
	}
	// 挿入の中の jk は、インサートモードのキーマップで <Esc> にしない。
	h.keys("A")
	h.paste("jkx")
	h.keys("<Esc>")
	h.settle()
	if got := h.text(); got != "ddonejkx\ntwo" {
		t.Fatalf("インサートモードの貼り付け: text = %q", got)
	}
	// キーマップの接頭辞 l を待っている間の貼り付けは、l を確定させて（右へ 1 文字）から、a の位置に入れる。
	h.keys("0l")
	h.paste("Z")
	h.settle()
	h.eventually("ノーマルモード", func() bool { return h.e.cur.state.Mode() == command.ModeNormal })
	if got := h.text(); got != "ddZonejkx\ntwo" {
		t.Errorf("接頭辞の後の貼り付け: text = %q", got)
	}
}
