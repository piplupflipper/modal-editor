package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// muxCalls は、Config.OpenPopup と OpenPane の偽物が受けた呼び出しを記録する。
type muxCalls struct {
	mu     sync.Mutex
	popups []muxCall
	panes  []muxCall
	// fail が nil でなければ、呼び出しはこの誤りを返す。
	fail error
}

type muxCall struct {
	from string
	view uint64
	opts PaneOptions
}

func (m *muxCalls) config(cfg *Config) {
	cfg.OpenPopup = func(from string, view uint64, _ string) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.popups = append(m.popups, muxCall{from: from, view: view})
		return m.fail
	}
	cfg.OpenPane = func(from string, view uint64, _ string, opts PaneOptions) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.panes = append(m.panes, muxCall{from: from, view: view, opts: opts})
		return m.fail
	}
}

func (m *muxCalls) last(panes bool) (muxCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.popups
	if panes {
		l = m.panes
	}
	if len(l) == 0 {
		return muxCall{}, false
	}
	return l[len(l)-1], true
}

// compPeer は、AttachComponent で繋いだ部品の client の偽の端末である。
type compPeer struct {
	h    *harness
	term *exitTerm
}

// attachComponent は、部品 view を描く部品の client を繋ぐ。
// pane なら、ペインの部品の client として繋ぐ。
func (h *harness) attachComponent(view uint64, pane bool) *compPeer {
	h.t.Helper()
	term := &exitTerm{fakeTerm: newFakeTerm()}
	h.peers = append(h.peers, term.fakeTerm)
	if err := h.e.AttachComponent(term, view, pane); err != nil {
		h.t.Fatalf("AttachComponent(%d) = %v", view, err)
	}
	return &compPeer{h: h, term: term}
}

func (p *compPeer) keys(s string) {
	p.h.t.Helper()
	for _, k := range editcmd.MustParseKeys(s) {
		p.term.in <- TermEvent{Key: k}
		p.h.sent++
	}
}

// waitScreen は、部品の client の画面が substr を含むまで待つ。
func (p *compPeer) waitScreen(substr string) {
	p.h.t.Helper()
	p.h.eventually("部品の client の画面に "+substr, func() bool { return strings.Contains(p.term.screen(), substr) })
}

// clientOf は、偽の端末 term の client を返す。イベントループの goroutine で呼ぶ。
func (h *harness) clientOf(term Terminal) *clientView {
	for _, c := range h.e.clients {
		if h.e.viewOf(c).term == term {
			return c
		}
	}
	return nil
}

// 端末多重化ソフトの中（client のペインを知っている）で中央のポップアップを開くと、Config.OpenPopup で端末多重化ソフトの
// ポップアップを開かせる。ポップアップの部品の client が繋ぐまでは元の client の画面の中央に描き、繋いだら部品の client の画面に
// 部品を広げて描き、元の client の画面には描かない。部品の client で打ったキーは元の client の操作になり（popupKeys、<CR>、<Esc>）、
// 閉じたら部品の client に終わりを知らせる。部品の client が先に終わったら、ポップアップを閉じて view/closed を送る。
//
// req: FR-66, FR-67
func TestComponentPopup(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t)}
	calls.config(&cfg)
	h := start(t, cfg)
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})

	peer.keys(":fake.popup list<CR>")
	h.eventually("ポップアップ", func() bool { return h.e.viewOf(h.clientOf(peer.term)).float != nil })
	h.eventually("OpenPopup", func() bool { _, ok := calls.last(false); return ok })
	call, _ := calls.last(false)
	if call.from != "%9" {
		t.Errorf("OpenPopup from = %q, want the pane of the client that opened it", call.from)
	}
	// 部品の client が繋ぐまでは、元の client の画面の中央に描く。
	h.eventually("元の client の画面のポップアップ", func() bool { return strings.Contains(peer.term.screen(), "close me") })

	// ペインの部品の client は、中央のポップアップに繋がない（PR #248 のレビューの推奨 3）。
	if err := h.e.AttachComponent(&exitTerm{fakeTerm: newFakeTerm()}, call.view, true); !errors.Is(err, ErrUnknownView) {
		t.Errorf("pane AttachComponent to a popup = %v, want ErrUnknownView", err)
	}
	comp := h.attachComponent(call.view, false)
	if err := h.e.AttachComponent(&exitTerm{fakeTerm: newFakeTerm()}, call.view, false); !errors.Is(err, ErrUnknownView) {
		t.Errorf("second AttachComponent = %v, want ErrUnknownView", err)
	}
	comp.waitScreen("close me")
	h.eventually("元の client の画面から消える", func() bool { return !strings.Contains(peer.term.screen(), "close me") })
	if scr := comp.term.screen(); !strings.HasPrefix(strings.TrimLeft(scr, " "), "list") || strings.Contains(scr, "┌") {
		t.Errorf("component screen (title on the first row, no border):\n%s", scr)
	}

	// 部品の client のキーは、元の client の操作として処理する。
	comp.keys("x")
	h.waitMessage("kind=3 item=one key=fake.pick")
	comp.keys("j")
	h.settle()
	comp.keys("<CR>")
	h.waitMessage("kind=1 item=close")
	// 選んだ close に、拡張機能は view/close で応える。閉じたら部品の client に終わりを知らせる。
	h.eventually("view/close", func() bool { return h.e.viewOf(h.clientOf(peer.term)).float == nil })
	h.eventually("部品の client の終わり", func() bool { return len(comp.term.exits()) == 1 })
	if got := comp.term.exits(); got[0] != "" {
		t.Errorf("component exit reason = %q, want empty", got[0])
	}

	// 部品の client が先に終わったら（利用者が端末多重化ソフトでポップアップを閉じた）、ポップアップを閉じ、view/closed を送る。
	peer.keys(":fake.popup list<CR>")
	h.eventually("2 つ目の OpenPopup", func() bool { c, _ := calls.last(false); return c.view != call.view })
	call, _ = calls.last(false)
	comp = h.attachComponent(call.view, false)
	comp.waitScreen("close me")
	comp.term.closeInput()
	h.waitMessage("kind=0 item= key=")
	h.eventually("閉じたポップアップ", func() bool { return h.e.viewOf(h.clientOf(peer.term)).float == nil })

	// 端末多重化ソフトのポップアップを開けなければ、理由を出し、元の client の画面の中央に描いたままにする。
	calls.mu.Lock()
	calls.fail = errors.New("no popup")
	calls.mu.Unlock()
	peer.keys(":fake.popup list<CR>")
	h.waitMessage("端末多重化ソフトのポップアップを開けないので、この画面の中に出します: no popup")
	h.eventually("元の client の画面のポップアップ", func() bool { return strings.Contains(peer.term.screen(), "close me") })

	// 端末多重化ソフトの外の client（ペインを知らない）は、OpenPopup を呼ばずに画面の中央に描く。
	calls.mu.Lock()
	n := len(calls.popups)
	calls.mu.Unlock()
	h.keys(":fake.popup list<CR>")
	h.eventually("外のポップアップ", func() bool { return h.e.float != nil && h.e.float.center })
	h.settle()
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if len(calls.popups) != n {
		t.Errorf("OpenPopup was called for a client outside the terminal multiplexer")
	}
}

// 拡張機能の pane/open は、元の client のペインを Config.OpenPane で分割させ、ペインの部品の client が描く。
// ペインの部品の client で打ったキーは元の client の操作になり（ヤンクしたレジスターを元の client で貼れる、<CR> で選ぶ、view/expand）、
// <Esc> では閉じない。元の client は、ペインを開いている間も自分のタブを編集できる。:q でペインを閉じ、部品の client に終わりを知らせる。
// 部品の client が先に終わったら view/closed を送る。
//
// req: FR-66, FR-115
func TestComponentPane(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t)}
	calls.config(&cfg)
	h := start(t, cfg)
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})

	peer.keys(":fake.pane tree<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	if call.from != "%9" || call.opts != (PaneOptions{Down: true, Size: 30}) {
		t.Errorf("OpenPane = %+v", call)
	}
	h.waitMessage("err=<nil>")
	var floating bool
	h.e.do(func() { floating = h.e.viewOf(h.clientOf(peer.term)).float != nil })
	if floating {
		t.Errorf("pane/open opened a popup on the client")
	}
	if err := h.e.AttachComponent(&exitTerm{fakeTerm: newFakeTerm()}, call.view, false); !errors.Is(err, ErrUnknownView) {
		t.Errorf("popup AttachComponent to a pane = %v, want ErrUnknownView", err)
	}
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("▸ dir")

	// ペインで開いた木の <CR> は開閉する（view/expand）。<Esc> では閉じない。
	comp.keys("<CR>")
	h.waitMessage("kind=4 item=dir key= expanded=true")
	comp.waitScreen("▾ dir")
	comp.keys("<Esc>")
	h.settle()
	var panes int
	h.e.do(func() { panes = len(h.e.viewOf(h.clientOf(peer.term)).panes) })
	if panes != 1 {
		t.Fatalf("panes after <Esc> = %d, want 1", panes)
	}

	// 元の client は自分のタブを編集でき、ペインでヤンクしたものを貼れる（元の client の操作で、レジスターを共有する）。
	comp.keys("yy")
	h.settle()
	peer.keys("ihello <Esc>p")
	h.settle()
	var text string
	h.e.do(func() { text = h.e.viewOf(h.clientOf(peer.term)).cur.buf.Text() })
	if text != "hello \n▾ dir" {
		t.Errorf("peer text = %q, want the line yanked in the pane pasted", text)
	}
	if scr := peer.term.screen(); strings.Contains(scr, "▾ dir\n") && strings.Contains(scr, "    a") {
		t.Errorf("the pane is drawn on the client screen:\n%s", scr)
	}

	// 子の無い項目の <CR> は選ぶ（view/select）。
	comp.keys("j<CR>")
	h.waitMessage("kind=1 item=dir/a")

	// :q でペインを閉じ、部品の client に終わりを知らせる。拡張機能へは view/closed を送る。
	comp.keys(":q<CR>")
	h.waitMessage("kind=0 item= key=")
	h.eventually("部品の client の終わり", func() bool { return len(comp.term.exits()) == 1 })
	h.e.do(func() { panes = len(h.e.viewOf(h.clientOf(peer.term)).panes) })
	if panes != 0 {
		t.Errorf("panes after :q = %d", panes)
	}
	h.e.do(func() { text = h.e.viewOf(h.clientOf(peer.term)).cur.buf.Text() })
	if text != "hello \n▾ dir" {
		t.Errorf(":q in the pane changed the client: %q", text)
	}

	// 部品の client が先に終わったら（利用者が端末多重化ソフトでペインを閉じた）、view/closed を送る。
	peer.keys(":fake.pane list<CR>")
	h.eventually("2 つ目の OpenPane", func() bool { c, _ := calls.last(true); return c.view != call.view })
	call, _ = calls.last(true)
	comp = h.attachComponent(call.view, true)
	comp.waitScreen("close me")
	comp.term.closeInput()
	h.waitMessage("kind=0 item= key=")
	h.e.do(func() { panes = len(h.e.viewOf(h.clientOf(peer.term)).panes) })
	if panes != 0 {
		t.Errorf("panes after the component client is gone = %d", panes)
	}

	// 元の client が終わったら、ペインの部品の client も終わらせる。
	peer.keys(":fake.pane list<CR>")
	h.eventually("3 つ目の OpenPane", func() bool { c, _ := calls.last(true); return c.view != call.view })
	call, _ = calls.last(true)
	comp = h.attachComponent(call.view, true)
	comp.waitScreen("close me")
	peer.keys(":q!<CR>")
	h.eventually("元の client が終わったときの部品の client の終わり", func() bool { return len(comp.term.exits()) == 1 })
}

// ペインで選んだ後に拡張機能が送る editor/open は、その拡張機能を後から別の client が使っていても、ペインを開いた client のタブに開く
// （基本設計 1.2。FR-105。PR #248 のレビューの必須）。ペインの部品の client のキーで選んで開いたら、
// 端末多重化ソフトのフォーカスをペインからその client のペインへ移す（推奨 1）。
//
// req: FR-115
func TestPaneSelectionGoesToOpener(t *testing.T) {
	var calls muxCalls
	cfg := Config{Extensions: fakeSpecs(t)}
	if err := os.WriteFile(filepath.Join(cfg.Extensions[0].Dir, "opened.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls.config(&cfg)
	var mu sync.Mutex
	var focused [][2]string
	cfg.FocusPane = func(from, pane string) error {
		mu.Lock()
		defer mu.Unlock()
		focused = append(focused, [2]string{from, pane})
		return nil
	}
	h := start(t, cfg)
	a := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	a.keys(":fake.pane files<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("opened.txt")

	// 最初の client（端末多重化ソフトの外）で同じ拡張機能を使い、拡張機能へ最後に問い合わせた client にする。
	h.keys(":fake.popup list<CR>")
	h.eventually("最初の client のポップアップ", func() bool { return h.e.viewOf(h.clientOf(h.term)).float != nil })
	h.keys("<Esc>")
	h.settle()

	comp.keys("<CR>")
	t.Cleanup(func() {
		if t.Failed() {
			h.e.do(func() {
				for _, m := range h.e.msgHistory {
					t.Logf("history: %s", m.Text)
				}
				for _, b := range h.e.bufs {
					t.Logf("buf %d %q owner=%v", b.id, b.path, b.owner != nil)
				}
			})
		}
	})
	h.eventually("ペインを開いた client のタブ", func() bool {
		return strings.HasSuffix(h.e.viewOf(h.clientOf(a.term)).cur.path, "opened.txt")
	})
	var firstPath string
	h.e.do(func() { firstPath = h.e.viewOf(h.clientOf(h.term)).cur.path })
	if strings.HasSuffix(firstPath, "opened.txt") {
		t.Errorf("the file opened in the client that used the extension last")
	}
	h.eventually("フォーカスの移動", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(focused) == 1 && focused[0] == [2]string{"%9", "%9"}
	})

	// ペインで :q を打ったときの view/closed の後のメッセージも、ペインを開いた client に出る。
	comp.keys(":q<CR>")
	h.eventually("ペインを開いた client の view/closed", func() bool {
		return strings.Contains(h.e.viewOf(h.clientOf(a.term)).message.Text, "kind=0")
	})
}

// テキストの部品は、カーソルが最後の行にあれば、view/update で書き足した後も最後の行へカーソルを置き、表示が末尾を追う
// （PR #248 のレビューの推奨 2）。最後の行より上を見ていれば、行の番号を保つ。
func TestTextFollowsTail(t *testing.T) {
	h := start(t, Config{})
	text := func(n int) ext.ViewComponent {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		return ext.ViewComponent{Kind: ext.ViewText, Text: strings.Join(lines, "\n")}
	}
	var got []int
	h.e.do(func() {
		f := h.e.newComponentFloat(text(1), "")
		h.e.setFloatContent(f, text(3))
		got = append(got, f.bs.state.Cursor().Line)
		f.bs.state.SetCursor(buffer.Position{Line: 1})
		h.e.setFloatContent(f, text(5))
		got = append(got, f.bs.state.Cursor().Line)
		h.e.closeBufferState(f.bs)
	})
	if !slices.Equal(got, []int{2, 1}) {
		t.Errorf("cursor lines = %v, want [2 1]", got)
	}
}

// 端末多重化ソフトの外（client のペインを知らない）と、ペインを開けなかったときは、pane/open の部品を、
// 元の client の画面の中央のポップアップに出す（FR-123）。中央のポップアップなので、<Esc> で閉じ、view/closed を送る。
// ペインを開けなかったときは、同じ ID のまま移すので、拡張機能は違いを知らずに view/update を送れる。
//
// req: FR-123
func TestPaneOutsideMux(t *testing.T) {
	var calls muxCalls
	calls.fail = errors.New("no pane")
	cfg := Config{Extensions: fakeSpecs(t)}
	calls.config(&cfg)
	h := start(t, cfg)

	h.keys(":fake.pane list<CR>")
	h.eventually("中央のポップアップ", func() bool { return h.e.float != nil && h.e.float.center && h.e.float.focused })
	if fi, _ := h.float(); fi.kind != floatList || fi.text != "first\nclose me\nupdate me" {
		t.Errorf("popup: %+v", fi)
	}
	calls.mu.Lock()
	n := len(calls.panes)
	calls.mu.Unlock()
	if n != 0 {
		t.Errorf("OpenPane was called outside the terminal multiplexer")
	}
	// 拡張機能は view/update で中身を替えられる。
	h.keys("jj<CR>")
	h.eventually("view/update", func() bool { return popupText(h.e) == "updated" })
	h.keys("<Esc>")
	h.waitMessage("kind=0 item= key=")
	if _, ok := h.float(); ok {
		t.Errorf("<Esc> did not close the popup")
	}

	// ペインを開けなかったときは、理由を出して中央のポップアップへ移す。
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9"})
	peer.keys(":fake.pane list<CR>")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	// 中央のポップアップへ移した後に、端末多重化ソフトのポップアップも開けずに誤りを出すので、メッセージの履歴で確かめる。
	h.eventually("ペインを開けなかった誤り", func() bool {
		return slices.ContainsFunc(h.e.msgHistory, func(m ui.Message) bool {
			return m.Text == "ペインを開けないので、中央のポップアップに出します: no pane"
		})
	})
	var id uint64
	h.e.do(func() {
		if f := h.e.viewOf(h.clientOf(peer.term)).float; f != nil && f.center && !f.pane {
			id = f.id
		}
	})
	if id != call.view {
		t.Errorf("fallback popup id = %d, want the pane id %d", id, call.view)
	}
	peer.keys("jj<CR>")
	h.eventually("移した後の view/update", func() bool {
		f := h.e.viewOf(h.clientOf(peer.term)).float
		return f != nil && f.bs.buf.Text() == "updated"
	})
	if !slices.Contains(peer.term.exits(), "") && len(peer.term.exits()) != 0 {
		t.Errorf("peer exited: %q", peer.term.exits())
	}
}
