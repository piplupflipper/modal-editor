package editor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// exitTerm は、Run の途中で終わる client の端末（ClientExiter）を真似る偽の端末である。
type exitTerm struct {
	*fakeTerm
	mu     sync.Mutex
	exited []string
}

func (x *exitTerm) Exit(reason string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.exited = append(x.exited, reason)
}

func (x *exitTerm) exits() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]string(nil), x.exited...)
}

// peer は、harness のエディタに Attach で足した 2 つ目からの編集の client である。
type peer struct {
	h    *harness
	term *exitTerm
}

// attach は、info の client を h のエディタに足す。Attach はイベントループが繋ぎ終えるまで戻らない。
func (h *harness) attach(info ClientInfo) *peer {
	h.t.Helper()
	term := &exitTerm{fakeTerm: newFakeTerm()}
	h.peers = append(h.peers, term.fakeTerm)
	if err := h.e.Attach(term, info); err != nil {
		h.t.Fatalf("Attach = %v", err)
	}
	return &peer{h: h, term: term}
}

// keys は、Vim の表記のキーの列を、この client の端末から送る。
func (p *peer) keys(s string) {
	p.h.t.Helper()
	for _, k := range editcmd.MustParseKeys(s) {
		p.term.in <- TermEvent{Key: k}
		p.h.sent++
	}
}

// bufferText は、ファイル名が name のバッファのテキストを返す。無ければ "<none>" を返す。
func (h *harness) bufferText(name string) string {
	s := "<none>"
	h.e.do(func() {
		for _, bs := range h.e.bufs {
			if filepath.Base(bs.path) == name {
				s = bs.buf.Text()
			}
		}
	})
	return s
}

// 要件定義 8.1 の client をまたいだ使い方（レジスター、マクロ、ドットリピート）を、2 つの編集の client を 1 つのエディタに繋いで確かめる。
// どれも、1 つのプロセスの構成で同じキーを 1 つの client で打った結果（Vim 9.1 と同じ結果）と同じになる。
//
// req: FR-58, FR-62
func TestClientsShareRegistersMacroDot(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\n", "b.txt": "beta\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})

	// レジスター: A でヤンクしたテキストを B で貼り、<C-p>（FR-55）で、A で積んだ履歴の 1 つ前に置き換える。
	h.keys("yyjyy")
	h.settle()
	b.keys("p")
	h.settle()
	if got := h.bufferText("b.txt"); got != "beta\ntwo" {
		t.Errorf("A の yy の後の B の p: b.txt = %q", got)
	}
	b.keys("<C-p>")
	h.settle()
	if got := h.bufferText("b.txt"); got != "beta\none" {
		t.Errorf("B の <C-p>: b.txt = %q, want A の 1 つ前の yy の one", got)
	}

	// マクロ: A で qa で記録したマクロを、B で @a で再生する。
	h.keys("qaA!<Esc>q")
	h.settle()
	b.keys("@a")
	h.settle()
	if got := h.bufferText("b.txt"); got != "beta\none!" {
		t.Errorf("B の @a: b.txt = %q", got)
	}

	// ドットリピート: A で変更した後に、B で . を打つ。
	h.keys("A?<Esc>")
	h.settle()
	b.keys("gg.")
	h.settle()
	if got := h.bufferText("b.txt"); got != "beta?\none!" {
		t.Errorf("B の .: b.txt = %q", got)
	}
	if got := h.bufferText("a.txt"); got != "one\ntwo!?" {
		t.Errorf("a.txt = %q", got)
	}
}

// マクロの記録の途中に他の client で打ったキーは、記録しない（基本設計 10 章の仮の扱い）。
// 記録しているのは記録を始めた client だけで、ステータス行の recording もその client にだけ出す。
//
// req: FR-05, FR-58
func TestRecordingOnlyRecordsOwnClient(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "aaa\n", "b.txt": "bbb\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	h.keys("qq")
	h.settle()
	b.keys("x")
	h.settle()
	if s := b.term.screen(); strings.Contains(s, "recording") {
		t.Errorf("記録していない B の画面に recording が出た:\n%s", s)
	}
	if s := h.term.screen(); !strings.Contains(s, "recording @q") {
		t.Errorf("記録している A の画面に recording が出ない:\n%s", s)
	}
	h.keys("x")
	b.keys("x")
	h.keys("q")
	h.settle()
	h.e.do(func() {
		reg, _ := h.e.shared.Registers().Get('q')
		if reg.Text != "x" {
			t.Errorf("q = %q, want A の x だけ", reg.Text)
		}
	})
	if got := h.bufferText("b.txt"); got != "b" {
		t.Errorf("b.txt = %q", got)
	}
}

// 各編集の client はタブの行を持ち、1 つのバッファはどれか 1 つの client のタブになる（FR-62）。
// :e は新しいタブで開き（FR-113）、<Tab> と <S-Tab> でタブを移り（FR-98）、:tabmove で並びを変える（FR-65）。
// 別の client のタブのファイルを開こうとしたら、開かずにその client のペインへフォーカスを移す（FR-63）。
// :qa はその client のタブだけを閉じて、その client を終える（FR-124、FR-114）。
//
// req: FR-62, FR-63, FR-65, FR-98, FR-113, FR-114, FR-124
func TestClientTabs(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "A\n", "b.txt": "B\n", "c.txt": "C\n", "d.txt": "D\n"})
	type focus struct{ from, to string }
	focused := make(chan focus, 4)
	h := start(t, Config{WorkDir: dir, FocusPane: func(from, to string) error {
		focused <- focus{from, to}
		return nil
	}}, "a.txt")
	h.e.do(func() { h.e.clients[0].info = ClientInfo{ID: 1, Pane: "%1"} })
	b := h.attach(ClientInfo{ID: 2, Pane: "%2", WorkDir: dir, Files: []string{filepath.Join(dir, "b.txt")}})

	tabs := func(c int) string {
		var names []string
		h.e.do(func() {
			v := h.e.clients[c]
			cur := h.e.viewOf(v).cur
			for _, bs := range v.tabs {
				n := filepath.Base(bs.path)
				if bs == cur {
					n = "[" + n + "]"
				}
				names = append(names, n)
			}
		})
		return strings.Join(names, " ")
	}
	check := func(step string, c int, want string) {
		t.Helper()
		h.settle()
		if got := tabs(c); got != want {
			t.Errorf("%s: client %d のタブ = %s, want %s", step, c+1, got, want)
		}
	}

	// :e は新しいタブで開き、元のタブを残す。カーソルのあるタブの右に置く。
	h.keys(":e c.txt<CR>")
	check(":e c.txt", 0, "a.txt [c.txt]")
	h.keys("<S-Tab>:e d.txt<CR>")
	check(":e d.txt", 0, "a.txt [d.txt] c.txt")
	if s := h.term.screen(); !strings.HasPrefix(s, " a.txt  d.txt  c.txt ") {
		t.Errorf("タブの行:\n%s", s)
	}
	h.keys("<Tab>")
	check("<Tab>", 0, "a.txt d.txt [c.txt]")
	h.keys("<Tab>")
	check("<Tab> は端から反対の端へ回る", 0, "[a.txt] d.txt c.txt")
	h.keys("2<S-Tab>")
	check("2<S-Tab>", 0, "a.txt [d.txt] c.txt")
	h.keys(":tabmove<CR>")
	check(":tabmove", 0, "a.txt c.txt [d.txt]")
	h.keys(":tabmove 0<CR>")
	check(":tabmove 0", 0, "[d.txt] a.txt c.txt")
	h.keys(":tabm +1<CR>")
	check(":tabm +1", 0, "a.txt [d.txt] c.txt")
	h.keys(":tabm 9<CR>")
	check(":tabm 9", 0, "a.txt [d.txt] c.txt")
	if msg := h.message(); !strings.HasPrefix(msg, "E475") {
		t.Errorf(":tabm 9: message = %q", msg)
	}
	check("B", 1, "[b.txt]")

	// B で、A のタブの c.txt を開こうとすると、開かずに A のペインへフォーカスを移し、A のカーソルのあるタブを c.txt にする。
	b.keys(":e c.txt<CR>")
	check("B の :e c.txt", 1, "[b.txt]")
	check("B の :e c.txt の後の A", 0, "a.txt d.txt [c.txt]")
	select {
	case f := <-focused:
		if f != (focus{"%2", "%1"}) {
			t.Errorf("フォーカスの移動 = %+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("フォーカスを移さない")
	}
	if s := b.term.screen(); !strings.Contains(s, "c.txt は client 1 のタブです") {
		t.Errorf("B の画面:\n%s", s)
	}
	b.keys(":ls<CR>")
	h.settle()
	if s := b.term.screen(); !strings.Contains(s, "[client 1]") {
		t.Errorf("B の :ls:\n%s", s)
	}
	b.keys("<CR>")

	// :qa は B のタブだけを閉じて、B を終える。A は続ける。
	b.keys("ix<Esc>:qa<CR>")
	h.settle()
	if got := b.term.exits(); len(got) != 0 {
		t.Errorf("未保存の :qa で終わった: %q", got)
	}
	b.keys(":qa!<CR>")
	h.settle()
	h.eventually("B の終わり", func() bool { return len(h.e.clients) == 1 })
	if got := b.term.exits(); len(got) != 1 || got[0] != "" {
		t.Errorf("B の終わりの知らせ = %q", got)
	}
	check("B の :qa! の後の A", 0, "a.txt d.txt [c.txt]")
	if got := h.bufferText("b.txt"); got != "<none>" {
		t.Errorf("B の :qa! の後の b.txt = %q, want 閉じた", got)
	}
	// A の :q はカーソルのあるタブだけを閉じる（FR-64）。
	h.keys(":q<CR>")
	check("A の :q", 0, "a.txt [d.txt]")
}

// 端末の入力が閉じた client のタブのうち、未保存の変更の無いバッファは閉じ、変更のあるバッファはどのタブでもないバッファとして残し、
// 他の client に知らせる。残したバッファは :b でタブにできる（仮。clients.go の doc）。
//
// req: FR-62, FR-64
func TestLostClientKeepsModifiedBuffers(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "A\n", "b.txt": "B\n", "c.txt": "C\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt"), filepath.Join(dir, "c.txt")}})
	b.keys("ix<Esc>")
	h.settle()
	b.term.closeInput()
	h.eventually("B を外す", func() bool { return len(h.e.clients) == 1 })
	h.e.do(func() {
		var names []string
		for _, bs := range h.e.bufs {
			names = append(names, filepath.Base(bs.path))
		}
		if got := strings.Join(names, " "); got != "a.txt b.txt" {
			t.Errorf("残ったバッファ = %s, want a.txt b.txt（変更の無い c.txt は閉じる）", got)
		}
	})
	if msg := h.message(); !strings.Contains(msg, "b.txt") {
		t.Errorf("A への知らせ = %q", msg)
	}
	h.keys(":b b.txt<CR>")
	h.settle()
	h.e.do(func() {
		if got := filepath.Base(h.e.cur.path); got != "b.txt" || h.e.cur.owner != h.e.clients[0] || h.e.cur.buf.Text() != "xB" {
			t.Errorf(":b の後: cur = %s, text = %q", got, h.e.cur.buf.Text())
		}
	})
}

// 確認の問い（逆順の範囲の E493 の問い）は、問いを出した client の画面にだけ出し、答えるまで他の client のキーを処理しない（仮）。
//
// req: FR-58
func TestQuestionHoldsOtherClients(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "bb\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	h.keys(":2,1d<CR>")
	h.eventually("問い", func() bool { return h.e.confirm != nil })
	var prompt string
	h.e.do(func() { prompt = h.e.confirm.Prompt })
	b.keys("x")
	time.Sleep(50 * time.Millisecond)
	if got := h.bufferText("b.txt"); got != "bb" {
		t.Errorf("問いの間に B のキーを処理した: b.txt = %q", got)
	}
	if s := b.term.screen(); strings.Contains(s, prompt) {
		t.Errorf("B の画面に問いが出た:\n%s", s)
	}
	if s := h.term.screen(); !strings.Contains(s, prompt) {
		t.Errorf("A の画面に問いが出ない:\n%s", s)
	}
	h.keys("y")
	h.settle()
	if got := h.bufferText("b.txt"); got != "b" {
		t.Errorf("問いの後の B の x: b.txt = %q", got)
	}
	if got := h.bufferText("a.txt"); got != "c" {
		t.Errorf("a.txt = %q", got)
	}
}

// LSP と DAP の表示のデータ（診断と印）はバッファに結びつくので、そのバッファをタブに持つ client の画面に出る（要件定義 8.1）。
// ホバーは、そのバッファをカーソルのあるタブに持つ client の画面に出る（応答の前に別の client がキーを打っていてもよい）。
//
// req: FR-58, FR-62
func TestClientsShowBufferDecorations(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	h.e.do(func() {
		for _, bs := range h.e.bufs {
			v := ext.Version(bs.buf.Version())
			switch filepath.Base(bs.path) {
			case "a.txt":
				h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: []ext.Diagnostic{
					{Range: ext.Range{End: ext.Position{Byte: 1}}, Severity: ext.SeverityError, Message: "bad alpha"},
				}})
			case "b.txt":
				h.e.setDiagnostics("lsp", ext.DiagnosticSetParams{Buffer: bs.id, Version: v, Diagnostics: []ext.Diagnostic{
					{Range: ext.Range{End: ext.Position{Byte: 1}}, Severity: ext.SeverityWarning, Message: "bad beta"},
				}})
				h.e.setSigns("dap", ext.SignSetParams{Buffer: bs.id, Version: v, Signs: []ext.Sign{{Line: 0, Kind: ext.SignExecutionPoint}}})
				// B のバッファのホバーの応答が、A がキーを打った後（操作中の client が A）に届く。
				h.e.showPopup(ext.PopupShowParams{Buffer: bs.id, Version: v, Text: "hover of beta"})
			}
		}
		h.e.render()
	})
	a, bs := h.term.screen(), b.term.screen()
	// 1 行目はタブの行で、2 行目からがバッファである。
	if !strings.Contains(a, "\nE alpha") || !strings.Contains(a, "bad alpha") || strings.Contains(a, "beta") {
		t.Errorf("A の画面:\n%s", a)
	}
	if !strings.Contains(bs, "\n=>beta") || !strings.Contains(bs, "hover of beta") || strings.Contains(bs, "alpha") {
		t.Errorf("B の画面:\n%s", bs)
	}
}

// FR-63 でフォーカスを移される client が入力の途中（インサートモード）なら、その client のタブは替えない（PR #223 のレビューの必須 1）。
// 移された先で打ち続けたキーは、入力の途中のタブに入り、見ていないファイルを変えない。
// ノーマルモードに戻った後なら、タブを替える。
//
// req: FR-63
func TestFocusOwnerKeepsBusyClient(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n", "c.txt": "gamma\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt", "b.txt") // 最後に開いた b.txt がカーソルのあるタブ
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "c.txt")}})
	// レビューの再現: A は b.txt で Oabc（<Esc> を打たない）。B で :e a.txt。A は def<Esc> と打ち続ける。
	h.keys("ggOabc")
	h.settle()
	b.keys(":e a.txt<CR>")
	h.settle()
	if s := h.term.screen(); !strings.Contains(s, "入力の途中なのでタブは替えません") {
		t.Errorf("A の画面に知らせが出ない:\n%s", s)
	}
	h.keys("def<Esc>")
	h.settle()
	if got := h.bufferText("a.txt"); got != "alpha" {
		t.Errorf("a.txt = %q, want 変わらない", got)
	}
	if got := h.bufferText("b.txt"); got != "abcdef\nbeta" {
		t.Errorf("b.txt = %q, want 入力の続き", got)
	}
	// ノーマルモードに戻った後は、B の :e a.txt で A のタブを a.txt にする。
	b.keys(":e a.txt<CR>")
	h.settle()
	h.e.do(func() {
		if got := filepath.Base(h.e.viewOf(h.e.clients[0]).cur.path); got != "a.txt" {
			t.Errorf("A のカーソルのあるタブ = %s, want a.txt", got)
		}
	})
}

// 問いを出した client が終わったら、問いを他の client へ移さずに取り下げる（PR #223 のレビューの必須 2）。
// レビューの再現: A が外で変わったファイルに :w して問いを出した後に A の端末が閉じると、B で打った y で書き出していた。
//
// req: FR-58
func TestQuestionCancelledWhenAskerGone(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "target.txt": "t\n"})
	path := filepath.Join(dir, "a.txt")
	h := start(t, Config{WorkDir: dir, CheckInterval: -1}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "target.txt")}})
	h.e.do(func() { h.e.clients[0].info.ID = 1 })
	h.keys("Ax<Esc>")
	h.settle()
	writeExternal(t, path, "alpha\next\n")
	h.keys(":w<CR>")
	h.eventually("書き込みの問い", func() bool { return h.e.prompt != nil })
	h.term.closeInput()
	h.eventually("A を外す", func() bool { return len(h.e.clients) == 1 })
	h.e.do(func() {
		if h.e.prompt != nil || h.e.asker != nil {
			t.Errorf("問いが残った: prompt = %v, asker = %v", h.e.prompt != nil, h.e.asker)
		}
	})
	if msg := h.message(); !strings.Contains(msg, "問いを取り下げました") || !strings.Contains(msg, "a.txt") {
		t.Errorf("B への知らせ = %q", msg)
	}
	b.keys("y")
	h.settle()
	if got, err := os.ReadFile(path); err != nil || string(got) != "alpha\next\n" {
		t.Errorf("a.txt = %q, %v; want 外で変えた内容のまま", got, err)
	}
	if got := h.bufferText("target.txt"); got != "t" {
		t.Errorf("target.txt = %q", got)
	}
}

// 問いを取り下げた後は、問いの間に待たせていた他の client のキーを、その client が次のキーを打つのを待たずに処理する
// （PR #223 の再レビューの必須）。問いに答えて終わった場合も同じく処理する。
//
// req: FR-58
func TestHeldKeysResumeAfterQuestion(t *testing.T) {
	for _, gone := range []bool{true, false} {
		dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "bb\n"})
		h := start(t, Config{WorkDir: dir}, "a.txt")
		b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
		h.keys(":2,1d<CR>")
		h.eventually("問い", func() bool { return h.e.confirm != nil })
		b.keys("x")
		time.Sleep(50 * time.Millisecond)
		if got := h.bufferText("b.txt"); got != "bb" {
			t.Fatalf("問いの間に B のキーを処理した: b.txt = %q", got)
		}
		if gone {
			h.term.closeInput() // 問いを出した A が終わり、問いを取り下げる
		} else {
			h.keys("n")
		}
		h.eventually("待たせていた B の x", func() bool {
			for _, bs := range h.e.bufs {
				if filepath.Base(bs.path) == "b.txt" {
					return bs.buf.Text() == "b"
				}
			}
			return false
		})
		if s := b.term.screen(); strings.Contains(s, "待っています") {
			h.e.do(func() { h.e.render() })
			if s = b.term.screen(); strings.Contains(s, "待っています") {
				t.Errorf("gone=%v: B の画面が待ったまま:\n%s", gone, s)
			}
		}
	}
}

// 問いの間に待たせている client には、待たせている理由を出す（PR #223 のレビューの推奨 2）。
//
// req: FR-58
func TestHeldClientShowsWhy(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\nb\nc\n", "b.txt": "bb\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	h.e.do(func() { h.e.clients[0].info.ID = 1 })
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	h.keys(":2,1d<CR>")
	h.eventually("問い", func() bool { return h.e.confirm != nil })
	h.e.do(func() { h.e.render() })
	if s := b.term.screen(); !strings.Contains(s, "client 1 の問いの答えを待っています") {
		t.Errorf("B の画面:\n%s", s)
	}
	h.keys("n")
	h.settle()
}

// 拡張機能が後から届ける editor/open と message/show は、その拡張機能へ問い合わせを始めた client に出す。
// 応答を待つ間に別の client がキーを打っても、その client には出さない（PR #223 のレビューの裁定要 2 へのオーケストレータの決定。仮）。
// 始めた client が終わっていれば、操作中の client に出す。
//
// req: FR-62
func TestExtResultGoesToRequester(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "alpha\n", "b.txt": "beta\n", "t1.txt": "t1\n", "t2.txt": "t2\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	tabsOf := func(i int) string {
		var names []string
		h.e.do(func() {
			for _, bs := range h.e.clients[i].tabs {
				names = append(names, filepath.Base(bs.path))
			}
		})
		return strings.Join(names, " ")
	}
	h.keys(":fake.openlater " + filepath.Join(dir, "t1.txt") + "<CR>")
	h.settle()
	b.keys("j")
	h.eventually("t1.txt を開く", func() bool { return h.e.findPath(filepath.Join(dir, "t1.txt")) != nil })
	h.settle()
	if got := tabsOf(0); got != "a.txt t1.txt" {
		t.Errorf("A のタブ = %s, want t1.txt を足す", got)
	}
	if got := tabsOf(1); got != "b.txt" {
		t.Errorf("B のタブ = %s, want 変わらない", got)
	}
	h.eventually("A への message/show", func() bool {
		return h.e.viewOf(h.e.clients[0]).message.Text == "opened later"
	})
	if s := b.term.screen(); strings.Contains(s, "opened later") {
		t.Errorf("B の画面に出た:\n%s", s)
	}

	// 問い合わせを始めた client（B）が終わっていれば、操作中の client（A）に出す。
	b.keys(":fake.openlater " + filepath.Join(dir, "t2.txt") + "<CR>:q<CR>")
	h.eventually("t2.txt を開く", func() bool { return h.e.findPath(filepath.Join(dir, "t2.txt")) != nil })
	h.settle()
	if got := tabsOf(0); !strings.Contains(got, "t2.txt") {
		t.Errorf("A のタブ = %s, want t2.txt を足す", got)
	}
}
