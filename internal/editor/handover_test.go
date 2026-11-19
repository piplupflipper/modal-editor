package editor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// handoverTerm は、端末を子プロセスへ渡せる client の端末（TerminalHandover）を真似る偽の端末である。
// 頼まれたことを記録するだけで、結果はテストが TermEvent.Terminal で送る（reply）。
type handoverTerm struct {
	*exitTerm
	// old は、端末の受け渡しを宣言しない（版の違う）client を真似ることを表す。
	old    bool
	hmu    sync.Mutex
	jobs   []TerminalJob
	events []string
}

func (h *handoverTerm) HandsOverTerminal() bool { return !h.old }

func (h *handoverTerm) RunInTerminal(job TerminalJob) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.jobs = append(h.jobs, job)
	h.events = append(h.events, "run")
}

func (h *handoverTerm) ReclaimTerminal(uint64) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.events = append(h.events, "reclaim")
}

func (h *handoverTerm) ReleaseTerminal(uint64) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.events = append(h.events, "release")
}

func (h *handoverTerm) log() []string {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	return slices.Clone(h.events)
}

func (h *handoverTerm) lastJob() (TerminalJob, bool) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	if len(h.jobs) == 0 {
		return TerminalJob{}, false
	}
	return h.jobs[len(h.jobs)-1], true
}

// setMode は、テストの一時ディレクトリ dir の許可属性を mode にする（書き込みを外して権限の不足を起こし、書く前に戻す）。
func setMode(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
}

// handoverPeer は、端末を子プロセスへ渡せる client を足したものである。
type handoverPeer struct {
	*peer
	ho *handoverTerm
}

// attachHandover は、端末を子プロセスへ渡せる client（識別子 2、作業ディレクトリ dir、ファイル files）を h のエディタに足す。
func (h *harness) attachHandover(dir string, files ...string) *handoverPeer {
	h.t.Helper()
	ho := &handoverTerm{exitTerm: &exitTerm{fakeTerm: newFakeTerm()}}
	h.peers = append(h.peers, ho.fakeTerm)
	if err := h.e.Attach(ho, ClientInfo{ID: 2, WorkDir: dir, Files: files}); err != nil {
		h.t.Fatalf("Attach = %v", err)
	}
	return &handoverPeer{peer: &peer{h: h, term: ho.exitTerm}, ho: ho}
}

// job は、client が頼まれた最後の実行を待って返す。
func (p *handoverPeer) job() TerminalJob {
	p.h.t.Helper()
	var j TerminalJob
	p.h.eventually("端末を渡す実行の頼み", func() bool {
		var ok bool
		j, ok = p.ho.lastJob()
		return ok
	})
	return j
}

// reply は、client の端末から、端末を渡した実行の結果 r を送る。
func (p *handoverPeer) reply(r TerminalResult) {
	p.term.in <- TermEvent{Terminal: &r}
}

// :! は、打った client の端末を子プロセスへ渡す実行を頼み（シェルのコマンド、問いあり、FR-82 の環境変数と client の識別子）、
// 結果が届くまでその client のキーを処理しない。結果の後にキーを処理し、0 でない終了コードを知らせる（FR-80、FR-82）。
// 端末を渡せない client（server を通さない端末）では誤りにする。
//
// req: FR-80, FR-82
func TestTerminalShell(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "xyz\n"})
	h := start(t, Config{WorkDir: dir, ChildEnv: []string{"MODAL_EDITOR_SOCKET=/run/s.sock", "EDITOR=/bin/me"}}, "a.txt")
	h.keys(":!true<CR>")
	h.settle()
	if msg := h.message(); !strings.Contains(msg, "E319") {
		t.Errorf("端末を渡せない client の :! = %q, want E319", msg)
	}

	b := h.attachHandover(dir, filepath.Join(dir, "b.txt"))
	b.keys(":!git commit | cat<CR>")
	j := b.job()
	if len(j.Commands) != 1 || j.Commands[0].Shell != "git commit | cat" || !j.Prompt {
		t.Errorf("頼み = %+v", j)
	}
	if want := []string{"MODAL_EDITOR_SOCKET=/run/s.sock", "EDITOR=/bin/me", EnvClient + "=2"}; !slices.Equal(j.Env, want) {
		t.Errorf("Env = %q, want %q", j.Env, want)
	}
	b.reply(TerminalResult{ID: j.ID, Status: 1})
	h.eventually("実行の終わり", func() bool { return h.e.shell == nil })
	var msg string
	h.e.do(func() { msg = h.e.viewOf(h.e.findClient(2)).message.Text })
	if msg != "シェルが 1 を返しました" {
		t.Errorf("B のメッセージ = %q", msg)
	}

	// 実行中は、打った client のキー（x）を処理しない。他の client のキーは処理する。
	b.keys(":!ls<CR>x")
	h.eventually("2 回目の頼み", func() bool { j2, _ := b.ho.lastJob(); return j2.ID != j.ID })
	j2, _ := b.ho.lastJob()
	h.keys("x")
	h.eventually("A の x", func() bool { return h.e.findPath(filepath.Join(dir, "a.txt")).buf.Text() == "bc" })
	if got := h.bufferText("b.txt"); got != "xyz" {
		t.Fatalf("実行中に B のキーを処理した: b.txt = %q", got)
	}
	b.reply(TerminalResult{ID: j2.ID})
	h.eventually("B の x", func() bool { return h.e.findPath(filepath.Join(dir, "b.txt")).buf.Text() == "yz" })
}

// マクロの再生の中の :! は、再生の残りを持って止まり、結果が届いてから残りを続ける（:{範囲}! と同じ。#231 の再生の待ちと食い違わない）。
// 待つだけの client のタブを編集している間（reclaimed）のキーは、再生の待ちに入らずに処理する。
//
// req: FR-80
func TestTerminalShellInMacro(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "uvwxyz\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attachHandover(dir, filepath.Join(dir, "b.txt"))
	b.keys("qa:!true<CR>")
	j := b.job()
	b.reply(TerminalResult{ID: j.ID})
	b.keys("xq@a")
	h.eventually("再生の中の頼み", func() bool { j2, _ := b.ho.lastJob(); return j2.ID != j.ID })
	if got := h.bufferText("b.txt"); got != "vwxyz" {
		t.Fatalf("再生の :! の結果の前に残りを続けた: b.txt = %q", got)
	}
	j, _ = b.ho.lastJob()
	b.reply(TerminalResult{ID: j.ID})
	h.eventually("再生の残り", func() bool { return h.e.findPath(filepath.Join(dir, "b.txt")).buf.Text() == "wxyz" })
}

// 端末の受け渡しを宣言しない client（版の違う client）では、:! を頼まずに E319 にし、その client のキーを処理し続ける
// （頼みを読み飛ばされて、キーを保留したまま戻らなくならないため。PR #234 のレビューの必須 1）。
//
// req: FR-80
func TestTerminalShellOldClient(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "b.txt": "xyz\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	ho := &handoverTerm{exitTerm: &exitTerm{fakeTerm: newFakeTerm()}, old: true}
	h.peers = append(h.peers, ho.fakeTerm)
	if err := h.e.Attach(ho, ClientInfo{ID: 2, WorkDir: dir, Files: []string{filepath.Join(dir, "b.txt")}}); err != nil {
		t.Fatal(err)
	}
	b := &peer{h: h, term: ho.exitTerm}
	b.keys(":!ls<CR>")
	h.settle()
	var msg string
	h.e.do(func() { msg = h.e.viewOf(h.e.findClient(2)).message.Text })
	if !strings.HasPrefix(msg, "E319") || len(ho.log()) != 0 {
		t.Errorf("message = %q, 頼み = %q", msg, ho.log())
	}
	b.keys("x")
	h.eventually("B の x", func() bool { return h.e.findPath(filepath.Join(dir, "b.txt")).buf.Text() == "yz" })
}

// 待つだけの client（FR-83）は、ファイルを、識別子の client のタブに開かせ、タブが閉じられるまで待つ。
// その client が :! の実行中なら、端末を取り戻させてキーを処理し（FR-117）、タブが閉じられたら端末を渡し直させ、
// 渡し直したことを受けてから待つだけの client を終わらせる。
//
// req: FR-83, FR-117
func TestWaiterReclaimsTerminal(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "MSG": "# comment\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attachHandover(dir)
	b.keys(":!git commit<CR>")
	j := b.job()

	done := make(chan string, 1)
	if _, err := h.e.Wait(context.Background(), WaitInfo{
		Files: []string{filepath.Join(dir, "MSG")}, Parent: 2, Done: func(r string) { done <- r },
	}); err != nil {
		t.Fatal(err)
	}
	h.eventually("端末を取り戻す頼み", func() bool { return slices.Contains(b.ho.log(), "reclaim") })
	var owner ClientID
	h.e.do(func() { owner = h.e.findPath(filepath.Join(dir, "MSG")).owner.info.ID })
	if owner != 2 {
		t.Errorf("MSG のタブの client = %d, want 2", owner)
	}
	// 取り戻している間は、B のキーを処理する。
	b.keys("ggOfix<Esc>:wq<CR>")
	h.eventually("端末を渡し直す頼み", func() bool { return slices.Contains(b.ho.log(), "release") })
	if b, err := os.ReadFile(filepath.Join(dir, "MSG")); err != nil || string(b) != "fix\n# comment\n" {
		t.Errorf("MSG = %q, %v", b, err)
	}
	select {
	case r := <-done:
		t.Fatalf("渡し直す前に待つだけの client を終わらせた（%q）", r)
	case <-time.After(50 * time.Millisecond):
	}
	b.reply(TerminalResult{ID: j.ID, Released: true})
	select {
	case r := <-done:
		if r != "" {
			t.Errorf("Done(%q), want empty", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("待つだけの client が終わらない")
	}
	// 渡し直した後は、実行が終わるまで B のキーを処理しない。
	b.keys("x")
	time.Sleep(50 * time.Millisecond)
	var held bool
	h.e.do(func() { held = h.e.shell != nil && h.e.keys.has(h.e.findClient(2)) })
	if !held {
		t.Error("渡し直した後に B のキーを処理した")
	}
	b.reply(TerminalResult{ID: j.ID})
	h.settle()
	if got := b.ho.log(); !slices.Equal(got, []string{"run", "reclaim", "release"}) {
		t.Errorf("頼み = %q", got)
	}
}

// 待つだけの client の識別子が無ければ、操作中の client のタブに開く。:! の外なので、端末は取り戻させない。
// タブを開いた client が終わったら、理由を付けて待つだけの client を終わらせる。ファイルを開けなければ誤りを返す。
//
// req: FR-83
func TestWaiterWithoutParent(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "x.txt": "x\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attachHandover(dir)
	b.keys("j")
	h.settle()
	done := make(chan string, 1)
	if _, err := h.e.Wait(context.Background(), WaitInfo{Files: []string{filepath.Join(dir, "x.txt")}, Done: func(r string) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	var owner ClientID
	h.e.do(func() { owner = h.e.findPath(filepath.Join(dir, "x.txt")).owner.info.ID })
	if owner != 2 {
		t.Errorf("x.txt のタブの client = %d, want 2（操作中の client）", owner)
	}
	b.term.closeInput()
	select {
	case r := <-done:
		if r == "" {
			t.Error("タブを開いた client が終わったのに、理由なしで終わらせた")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("待つだけの client が終わらない")
	}
	if len(b.ho.log()) != 0 {
		t.Errorf("頼み = %q, want none", b.ho.log())
	}

	if _, err := h.e.Wait(context.Background(), WaitInfo{Files: []string{filepath.Join(dir, "a.txt", "no")}, Done: func(string) {}}); err == nil {
		t.Error("開けないファイルで Wait が誤りを返さない")
	}
}

// 権限の不足で書けないファイルへの :wq は、権限を上げて書くかを問い、y なら端末を渡して <elevateCommand> tee <パス> に
// バッファの中身を標準入力で渡す。成功したら保存済みにし、外での変更の確認の記録を取り直し、続きの :q を行う（FR-79）。
// 親のディレクトリが無ければ、先に <elevateCommand> mkdir -p を動かす（FR-116）。失敗なら E212 を出して未保存のまま残す。
//
// req: FR-79, FR-116
func TestElevatedSave(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root は権限の不足にならない")
	}
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	// f.txt は書けないファイルで、link.txt（書けるディレクトリにある）から指す。w.txt は書けるファイルで、ディレクトリだけが書けない。
	f, w := filepath.Join(ro, "f.txt"), filepath.Join(ro, "w.txt")
	if err := os.WriteFile(f, []byte("old\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(f, link); err != nil {
		t.Fatal(err)
	}
	setMode(t, ro, 0o500)
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // 一時ディレクトリを消せるように書き込みを戻すだけである
	h := start(t, Config{WorkDir: dir, ElevateCommand: []string{"/fake/doas", "-n"}}, "a.txt")
	b := h.attachHandover(dir, w, link, filepath.Join(ro, "new", "g.txt"))

	// ファイルは書けてディレクトリだけが書けないときは、問わずにその場で書く（Vim 9.1 と同じ。PR #234 のレビューの推奨 3）。
	b.keys("ccin place<Esc>:w<CR>")
	h.settle()
	if got, _ := os.ReadFile(w); string(got) != "in place\n" || h.e.prompt != nil {
		t.Fatalf("w.txt = %q, prompt = %v", got, h.e.prompt != nil)
	}
	b.keys("<Tab>")

	// 失敗: E212 を出し、未保存のまま残す。問いと tee には、シンボリックリンクをたどった先のパスを使う（推奨 2）。
	b.keys("ccnew<Esc>:w<CR>")
	h.eventually("権限を上げる問い", func() bool {
		return h.e.prompt != nil && strings.HasPrefix(h.e.prompt.text, "権限を上げて書き込みますか (y/n)?") &&
			strings.Contains(h.e.prompt.text, f) && strings.Contains(h.e.prompt.text, "/fake/doas -n で書きます")
	})
	b.keys("y")
	j := b.job()
	if len(j.Commands) != 1 || !slices.Equal(j.Commands[0].Argv, []string{"/fake/doas", "-n", "tee", f}) ||
		!j.Commands[0].PipeInput || string(j.Commands[0].Input) != "new\n" || !j.Commands[0].DiscardOutput || j.Prompt {
		t.Fatalf("頼み = %+v", j)
	}
	b.reply(TerminalResult{ID: j.ID, Status: 1})
	h.eventually("権限を上げた保存の終わり", func() bool { return h.e.shell == nil })
	var msg string
	var modified bool
	h.e.do(func() {
		msg = h.e.viewOf(h.e.findClient(2)).message.Text
		modified = h.e.findPath(link).buf.Modified()
	})
	if !strings.HasPrefix(msg, "E212") || !modified {
		t.Errorf("失敗の後: message = %q, modified = %v", msg, modified)
	}

	// 成功: :wq の :w を権限を上げて書き、保存済みにして、続きの :q でタブを閉じる。
	b.keys(":wq<CR>")
	h.eventually("権限を上げる問い", func() bool { return h.e.prompt != nil })
	b.keys("y")
	h.eventually("2 回目の頼み", func() bool { j2, _ := b.ho.lastJob(); return j2.ID != j.ID })
	j, _ = b.ho.lastJob()
	// テストが tee の代わりに書く。
	setMode(t, ro, 0o700)
	setMode(t, f, 0o600)
	if err := os.WriteFile(f, j.Commands[0].Input, 0o600); err != nil {
		t.Fatal(err)
	}
	b.reply(TerminalResult{ID: j.ID})
	h.eventually("link.txt のタブを閉じる", func() bool { return h.e.findPath(link) == nil })
	if b, _ := os.ReadFile(f); string(b) != "new\n" {
		t.Errorf("f.txt = %q", b)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link.txt がシンボリックリンクでなくなった: %v", err)
	}

	// ディレクトリが無ければ、先に mkdir -p を動かす（FR-116）。閉じたタブの後は w.txt（裏のタブ）にいるので、g.txt へ移る。
	setMode(t, ro, 0o500)
	b.keys("<Tab>ig<Esc>:w<CR>")
	h.eventually("ディレクトリの問い", func() bool { return h.e.prompt != nil && strings.Contains(h.e.prompt.text, "ディレクトリ") })
	b.keys("y")
	h.eventually("3 回目の頼み", func() bool { j3, _ := b.ho.lastJob(); return j3.ID != j.ID })
	j, _ = b.ho.lastJob()
	g := filepath.Join(ro, "new", "g.txt")
	if len(j.Commands) != 2 || !slices.Equal(j.Commands[0].Argv, []string{"/fake/doas", "-n", "mkdir", "-p", filepath.Dir(g)}) ||
		!slices.Equal(j.Commands[1].Argv, []string{"/fake/doas", "-n", "tee", g}) {
		t.Fatalf("頼み = %+v", j)
	}
	setMode(t, ro, 0o700)
	if err := os.MkdirAll(filepath.Dir(g), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(g, j.Commands[1].Input, 0o600); err != nil {
		t.Fatal(err)
	}
	b.reply(TerminalResult{ID: j.ID})
	h.eventually("権限を上げた保存の終わり", func() bool { return h.e.shell == nil })
	h.e.do(func() {
		bs := h.e.findPath(g)
		msg = h.e.viewOf(h.e.findClient(2)).message.Text
		modified = bs.buf.Modified()
		now, _ := statFile(g)
		if bs.disk.differs(now) {
			t.Error("外での変更の確認の記録を取り直していない")
		}
	})
	if modified || !strings.Contains(msg, "を作り") || !strings.Contains(msg, "/fake/doas -n で書き込みました") {
		t.Errorf("成功の後: message = %q, modified = %v", msg, modified)
	}
}
