package editor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// createdBuffer は、タイトルが title のバッファの、編集の禁止と、タブの持ち主の client の ID（タブでなければ 0）を返す。無ければ ok が false。
func (h *harness) createdBuffer(title string) (readOnly bool, owner ClientID, ok bool) {
	h.e.do(func() {
		for _, bs := range h.e.bufs {
			if bs.title == title {
				readOnly, ok = bs.buf.ReadOnly(), true
				if bs.owner != nil {
					owner = bs.owner.info.ID
				}
			}
		}
	})
	return readOnly, owner, ok
}

// 拡張機能は、buffer/create で編集を禁止したファイルでないバッファを作り（FR-88）、diff/start でファイルのタブと見比べさせられる（FR-99）。
// 作ったバッファはタブでないので、本体はファイルのタブの client のペインを分割し、ファイルを渡さずに起動した新しい client のタブにしてから
// 見比べを始める。git 連携の拡張機能（FR-104）が HEAD の版と見比べるのに使う。
//
// req: FR-88, FR-99
func TestDiffStartFromExtension(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n"})
	calls := make(chan diffCall, 1)
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t), SplitPane: func(from, file, d string) error {
		calls <- diffCall{from, file, d}
		return nil
	}}, "a.txt")
	h.e.do(func() { h.e.clients[0].info = ClientInfo{ID: 1, Pane: "%1"} })

	h.keys(":fake.headdiff<CR>")
	h.waitMessage("headdiff buf=")
	if msg := h.message(); !strings.Contains(msg, "split=true err=<nil>") {
		t.Fatalf("message = %q", msg)
	}
	var call diffCall
	select {
	case call = <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("diff/start がペインを開かない")
	}
	// ファイルを渡さずに、ファイルのタブの client のペインを分割する。
	if want := (diffCall{"%1", "", dir}); call != want {
		t.Errorf("SplitPane = %+v, want %+v", call, want)
	}
	readOnly, owner, ok := h.createdBuffer("HEAD:x")
	if !ok || !readOnly || owner != 0 {
		t.Fatalf("作ったバッファ: ok=%v readOnly=%v owner=%d", ok, readOnly, owner)
	}
	if n := h.diffSessions(); n != 0 {
		t.Fatalf("繋ぐ前の組 = %d", n)
	}

	// ファイルを渡さずに繋いだ client は、名前の無い空のタブの代わりに、作ったバッファをタブにする。
	b := h.attach(ClientInfo{ID: 2, Pane: "%2", WorkDir: dir})
	h.settle()
	if _, owner, _ := h.createdBuffer("HEAD:x"); owner != 2 {
		t.Fatalf("作ったバッファの持ち主 = %d, want 2", owner)
	}
	var tabs int
	h.e.do(func() { tabs = len(h.e.clients[1].tabs) })
	if tabs != 1 {
		t.Errorf("client 2 のタブ = %d, want 1", tabs)
	}
	if n := h.diffSessions(); n != 1 {
		t.Fatalf("見比べの組 = %d, want 1", n)
	}
	h.e.do(func() {
		s := h.e.diff.sessions[0]
		if s.bs[0].path == "" || s.bs[1].title != "HEAD:x" {
			t.Errorf("組 = %q と %q, want a.txt と HEAD:x", s.bs[0].path, s.bs[1].title)
		}
		if got := s.view[0].Lines[1].Style; got != "diff.change" {
			t.Errorf("a.txt の 2 行目 = %q, want diff.change", got)
		}
	})

	// 編集を禁止した HEAD の版へは dp で送れず（E21）、do で取り込める（基本設計 6.8）。
	h.keys("jdp")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E21") {
		t.Errorf("HEAD の版への dp = %q", msg)
	}
	h.keys("do")
	h.settle()
	if got := h.bufferText("a.txt"); got != "one\nTWO" {
		t.Errorf("do の後の a.txt = %q", got)
	}
	// HEAD の版の側でも編集は断る。
	b.keys("x")
	h.settle()
	var head string
	h.e.do(func() { head = h.e.diff.sessions[0].bs[1].buf.Text() })
	if head != "one\nTWO" {
		t.Errorf("HEAD の版 = %q", head)
	}

	// 同じ client の 2 つのタブは見比べられない。
	h.keys(fmt.Sprintf(":e %s/b.txt<CR>:fake.headdiff %d<CR>", dir, 1))
	h.waitMessage("同じ client のタブです")
	// どちらもタブでなければ断る。
	h.keys(":fake.headdiff notab<CR>")
	h.waitMessage("どちらもタブではありません")
}

// 端末多重化ソフトの外では、diff/start で分割できないので断る（基本設計 6.8）。拡張機能はその文言を受け取る。
//
// req: FR-99
func TestDiffStartOutsideMux(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(":fake.headdiff<CR>")
	h.waitMessage("headdiff buf=")
	if msg := h.message(); !strings.Contains(msg, "端末多重化ソフト") || !strings.Contains(msg, "split=false") {
		t.Errorf("message = %q", msg)
	}
	if n := h.diffSessions(); n != 0 {
		t.Errorf("組 = %d", n)
	}
}
