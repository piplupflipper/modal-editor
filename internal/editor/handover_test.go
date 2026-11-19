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
	hmu    sync.Mutex
	jobs   []TerminalJob
	events []string
}

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

// handoverPeer は、端末を子プロセスへ渡せる client を足したものである。
type handoverPeer struct {
	*peer
	ho *handoverTerm
}

// attachHandover は、端末を子プロセスへ渡せる client（識別子 id、作業ディレクトリ dir、ファイル files）を h のエディタに足す。
func (h *harness) attachHandover(id ClientID, dir string, files ...string) *handoverPeer {
	h.t.Helper()
	ho := &handoverTerm{exitTerm: &exitTerm{fakeTerm: newFakeTerm()}}
	h.peers = append(h.peers, ho.fakeTerm)
	if err := h.e.Attach(ho, ClientInfo{ID: id, WorkDir: dir, Files: files}); err != nil {
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

	b := h.attachHandover(2, dir, filepath.Join(dir, "b.txt"))
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

// 待つだけの client（FR-83）は、ファイルを、識別子の client のタブに開かせ、タブが閉じられるまで待つ。
// その client が :! の実行中なら、端末を取り戻させてキーを処理し（FR-117）、タブが閉じられたら端末を渡し直させ、
// 渡し直したことを受けてから待つだけの client を終わらせる。
//
// req: FR-83, FR-117
func TestWaiterReclaimsTerminal(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n", "MSG": "# comment\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	b := h.attachHandover(2, dir)
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
	b := h.attachHandover(2, dir)
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
	f := filepath.Join(ro, "f.txt")
	if err := os.WriteFile(f, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // 一時ディレクトリを消せるように書き込みを戻すだけである
	h := start(t, Config{WorkDir: dir, ElevateCommand: []string{"/fake/doas", "-n"}}, "a.txt")
	b := h.attachHandover(2, dir, f, filepath.Join(ro, "new", "g.txt"))

	// 失敗: E212 を出し、未保存のまま残す。
	b.keys("ccnew<Esc>:w<CR>")
	h.eventually("権限を上げる問い", func() bool {
		return h.e.prompt != nil && strings.Contains(h.e.prompt.text, "/fake/doas -n で書き込みますか")
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
		modified = h.e.findPath(f).buf.Modified()
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
	if err := os.Chmod(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, j.Commands[0].Input, 0o600); err != nil {
		t.Fatal(err)
	}
	b.reply(TerminalResult{ID: j.ID})
	h.eventually("f.txt のタブを閉じる", func() bool { return h.e.findPath(f) == nil })
	if b, _ := os.ReadFile(f); string(b) != "new\n" {
		t.Errorf("f.txt = %q", b)
	}

	// ディレクトリが無ければ、先に mkdir -p を動かす（FR-116）。
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	b.keys("ig<Esc>:w<CR>")
	h.eventually("ディレクトリの問い", func() bool { return h.e.prompt != nil && strings.Contains(h.e.prompt.text, "ディレクトリ") })
	b.keys("y")
	h.eventually("3 回目の頼み", func() bool { j3, _ := b.ho.lastJob(); return j3.ID != j.ID })
	j, _ = b.ho.lastJob()
	g := filepath.Join(ro, "new", "g.txt")
	if len(j.Commands) != 2 || !slices.Equal(j.Commands[0].Argv, []string{"/fake/doas", "-n", "mkdir", "-p", filepath.Dir(g)}) ||
		!slices.Equal(j.Commands[1].Argv, []string{"/fake/doas", "-n", "tee", g}) {
		t.Fatalf("頼み = %+v", j)
	}
	if err := os.Chmod(ro, 0o700); err != nil {
		t.Fatal(err)
	}
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
