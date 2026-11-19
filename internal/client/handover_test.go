package client

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/term"
)

// handoverTerm は、端末を子プロセスへ渡せる偽の端末である。Release と Reacquire の呼び出しを記録する。
// 子プロセスの標準入力は /dev/null、標準出力と標準エラー出力は out のファイルである。
type handoverTerm struct {
	*fakeTerm
	devnull, out *os.File

	hmu   sync.Mutex
	calls []string
}

func newHandoverTerm(t *testing.T) *handoverTerm {
	t.Helper()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(t.TempDir(), "tty-out"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = devnull.Close() // テストの後始末
		_ = out.Close()     // テストの後始末
	})
	return &handoverTerm{fakeTerm: newFakeTerm(), devnull: devnull, out: out}
}

func (h *handoverTerm) record(s string) {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	h.calls = append(h.calls, s)
}

func (h *handoverTerm) Release() error { h.record("release"); return nil }
func (h *handoverTerm) Reacquire(screen bool) error {
	if screen {
		h.record("reacquire")
	} else {
		h.record("raw")
	}
	return nil
}
func (h *handoverTerm) EnsureForeground()       {}
func (h *handoverTerm) TTY() (in, out *os.File) { return h.devnull, h.out }

func (h *handoverTerm) callList() []string {
	h.hmu.Lock()
	defer h.hmu.Unlock()
	return append([]string(nil), h.calls...)
}

// written は、子プロセスと問いが端末へ書いたものを返す。
func (h *handoverTerm) written(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(h.out.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// recvUntil は、server の側で、want の型のメッセージが届くまで読み、それまでに届いたものと一緒に返す。
func recvUntil[T protocol.ClientMessage](t *testing.T, s protocol.ServerConn) (T, []protocol.ClientMessage) {
	t.Helper()
	var before []protocol.ClientMessage
	ch := make(chan protocol.ClientMessage)
	errc := make(chan error, 1)
	for {
		go func() {
			m, err := s.Recv()
			if err != nil {
				errc <- err
				return
			}
			ch <- m
		}()
		select {
		case m := <-ch:
			if v, ok := m.(T); ok {
				return v, before
			}
			before = append(before, m)
		case err := <-errc:
			t.Fatalf("Recv: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatalf("%T が届かない。届いたもの: %#v", *new(T), before)
		}
	}
}

func startHandoverClient(t *testing.T) (*handoverTerm, protocol.ServerConn, <-chan error) {
	t.Helper()
	tm := newHandoverTerm(t)
	c, s := protocol.Pipe()
	done := runClient(tm, c)
	if _, err := s.Recv(); err != nil { // Hello
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = s.Send(protocol.Exit{})
		_ = s.Close()
		close(tm.in)
		<-done
	})
	return tm, s, done
}

// :! は、端末を子プロセスへ渡してシェルでコマンドを動かし、終わったら終了コードと「続けるには ENTER」を出してキーを待ち、
// 端末を取り戻して TerminalDone を返す。問いに <CR> 以外のキーで答えたら、そのキーを TerminalDone より先に server へ送る（FR-80）。
//
// req: FR-80, FR-82
func TestTerminalRunShellPrompt(t *testing.T) {
	tm, s, _ := startHandoverClient(t)
	if err := s.Send(protocol.TerminalRun{
		ID: 4, Commands: []protocol.TerminalCommand{{Shell: `printf 'out %s\n' "$MODAL_EDITOR_CLIENT"; exit 3`}},
		Env: []string{"MODAL_EDITOR_CLIENT=9"}, Prompt: true,
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(tm.written(t), promptText) {
		if time.Now().After(deadline) {
			t.Fatalf("問いが出ない。端末: %q", tm.written(t))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got, want := tm.written(t), "\r\nout 9\n\r\nシェルが 3 を返しました\r\n"+promptText; got != want {
		t.Errorf("端末 = %q, want %q", got, want)
	}
	tm.in <- term.Event{Key: editcmd.Rune(':')}
	done, before := recvUntil[protocol.TerminalDone](t, s)
	if done != (protocol.TerminalDone{ID: 4, Status: 3}) {
		t.Errorf("TerminalDone = %+v", done)
	}
	if len(before) != 1 || before[0] != (protocol.Event{Key: editcmd.Rune(':')}) {
		t.Errorf("TerminalDone の前に届いたもの = %#v, want the ':' key", before)
	}
	if got, want := tm.callList(), []string{"release", "raw", "reacquire"}; !reflect.DeepEqual(got, want) {
		t.Errorf("端末の操作 = %q, want %q", got, want)
	}
}

// <CR> で答えた問いのキーは server へ送らない。
//
// req: FR-80
func TestTerminalRunPromptEnter(t *testing.T) {
	tm, s, _ := startHandoverClient(t)
	if err := s.Send(protocol.TerminalRun{ID: 1, Commands: []protocol.TerminalCommand{{Shell: "true"}}, Prompt: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(tm.written(t), promptText) {
		if time.Now().After(deadline) {
			t.Fatal("問いが出ない")
		}
		time.Sleep(10 * time.Millisecond)
	}
	tm.in <- term.Event{Key: editcmd.Special(editcmd.CodeEnter)}
	done, before := recvUntil[protocol.TerminalDone](t, s)
	if done != (protocol.TerminalDone{ID: 1}) || len(before) != 0 {
		t.Errorf("TerminalDone = %+v, before = %#v", done, before)
	}
}

// 権限を上げた保存のコマンドは、シェルを通さずに順に動かし、Input を標準入力へ渡し、標準出力を捨てる（FR-79、FR-116）。
// 0 でない終了コードのコマンドで止める。Prompt でなければ問わない。
//
// req: FR-79, FR-116
func TestTerminalRunArgvInput(t *testing.T) {
	tm, s, _ := startHandoverClient(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "f.txt")
	if err := s.Send(protocol.TerminalRun{ID: 2, Commands: []protocol.TerminalCommand{
		{Argv: []string{"mkdir", "-p", filepath.Dir(target)}},
		{Argv: []string{"tee", target}, Input: []byte("a\nb\n"), PipeInput: true, DiscardOutput: true},
	}}); err != nil {
		t.Fatal(err)
	}
	done, _ := recvUntil[protocol.TerminalDone](t, s)
	if done != (protocol.TerminalDone{ID: 2}) {
		t.Errorf("TerminalDone = %+v", done)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "a\nb\n" {
		t.Errorf("書いた中身 = %q, %v", b, err)
	}
	if w := tm.written(t); w != "\r\n" {
		t.Errorf("tee の標準出力が端末へ出た: %q", w)
	}
	if got, want := tm.callList(), []string{"release", "reacquire"}; !reflect.DeepEqual(got, want) {
		t.Errorf("端末の操作 = %q, want %q", got, want)
	}

	if err := s.Send(protocol.TerminalRun{ID: 3, Commands: []protocol.TerminalCommand{
		{Argv: []string{"false"}},
		{Argv: []string{"touch", filepath.Join(dir, "not-run")}},
	}}); err != nil {
		t.Fatal(err)
	}
	done, _ = recvUntil[protocol.TerminalDone](t, s)
	if done != (protocol.TerminalDone{ID: 3, Status: 1}) {
		t.Errorf("TerminalDone = %+v, want status 1", done)
	}
	if _, err := os.Stat(filepath.Join(dir, "not-run")); err == nil {
		t.Error("失敗したコマンドの後のコマンドを動かした")
	}
	if err := s.Send(protocol.TerminalRun{ID: 5, Commands: []protocol.TerminalCommand{{Argv: []string{filepath.Join(dir, "missing")}}}}); err != nil {
		t.Fatal(err)
	}
	if done, _ = recvUntil[protocol.TerminalDone](t, s); done.Err == "" {
		t.Errorf("起動できないコマンドの TerminalDone = %+v, want Err", done)
	}
}

// 実行中に TerminalReclaim を受けたら端末を取り戻し、TerminalRelease で渡し直して TerminalReleased を返す（FR-117）。
// 取り戻している間にコマンドが終わったら、問わずに TerminalDone を返す。
//
// req: FR-117
func TestTerminalReclaimRelease(t *testing.T) {
	tm, s, _ := startHandoverClient(t)
	dir := t.TempDir()
	stop1, stop2 := filepath.Join(dir, "stop1"), filepath.Join(dir, "stop2")
	wait := `while [ ! -e "$1" ]; do sleep 0.01; done`
	send := func(m protocol.ServerMessage) {
		t.Helper()
		if err := s.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	send(protocol.TerminalRun{ID: 6, Commands: []protocol.TerminalCommand{
		{Argv: []string{"sh", "-c", wait, "sh", stop1}},
		{Argv: []string{"sh", "-c", wait, "sh", stop2}},
	}, Prompt: true})
	send(protocol.TerminalReclaim{ID: 6})
	send(protocol.TerminalRelease{ID: 6})
	if r, _ := recvUntil[protocol.TerminalReleased](t, s); r.ID != 6 {
		t.Errorf("TerminalReleased = %+v", r)
	}
	if got, want := tm.callList(), []string{"release", "reacquire", "release"}; !reflect.DeepEqual(got, want) {
		t.Errorf("端末の操作 = %q, want %q", got, want)
	}
	if err := os.WriteFile(stop1, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	send(protocol.TerminalReclaim{ID: 6})
	// 取り戻した後に TerminalReclaim の処理を待つ。受信の goroutine が処理し終えたことを、知らない ID の Release の返事で確かめる。
	send(protocol.TerminalRelease{ID: 99})
	if r, _ := recvUntil[protocol.TerminalReleased](t, s); r.ID != 99 {
		t.Errorf("TerminalReleased = %+v", r)
	}
	if err := os.WriteFile(stop2, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	done, _ := recvUntil[protocol.TerminalDone](t, s)
	if done != (protocol.TerminalDone{ID: 6}) {
		t.Errorf("TerminalDone = %+v", done)
	}
	if got, want := tm.callList(), []string{"release", "reacquire", "release", "reacquire"}; !reflect.DeepEqual(got, want) {
		t.Errorf("端末の操作 = %q, want %q（取り戻している間に終わったら問わない）", got, want)
	}
	if strings.Contains(tm.written(t), promptText) {
		t.Error("取り戻している間に終わったのに問いを出した")
	}
}

// 端末を渡せない端末（Handover を満たさない）では、TerminalRun をすぐに断る。
func TestTerminalRunUnsupported(t *testing.T) {
	tm := newFakeTerm()
	c, s := protocol.Pipe()
	done := runClient(tm, c)
	if _, err := s.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(protocol.TerminalRun{ID: 1, Commands: []protocol.TerminalCommand{{Shell: "true"}}}); err != nil {
		t.Fatal(err)
	}
	if d, _ := recvUntil[protocol.TerminalDone](t, s); d.ID != 1 || d.Err == "" {
		t.Errorf("TerminalDone = %+v, want Err", d)
	}
	_ = s.Send(protocol.Exit{})
	_ = s.Close()
	wait(t, done)
}
