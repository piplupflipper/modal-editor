package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/protocol"
)

// recvType は、c から T のメッセージが届くまで読み、届いたものを返す。届く前に読んだ Draw の最後の画面も返す。
func recvType[T protocol.ServerMessage](t *testing.T, c protocol.ClientConn) (T, string) {
	t.Helper()
	type got struct {
		m   protocol.ServerMessage
		err error
	}
	var screen string
	deadline := time.After(10 * time.Second)
	for {
		ch := make(chan got, 1)
		go func() {
			m, err := c.Recv()
			ch <- got{m, err}
		}()
		select {
		case g := <-ch:
			if g.err != nil {
				t.Fatalf("Recv: %v", g.err)
			}
			if d, ok := g.m.(protocol.Draw); ok {
				screen = d.Screen.Grid.String()
			}
			if v, ok := g.m.(T); ok {
				return v, screen
			}
		case <-deadline:
			t.Fatalf("%T が届かない", *new(T))
		}
	}
}

// waitScreen は、c に want を含む画面が届くまで読む。
func waitScreen(t *testing.T, c protocol.ClientConn, want string) {
	t.Helper()
	for {
		if _, scr := recvType[protocol.Draw](t, c); strings.Contains(scr, want) {
			return
		}
	}
}

// 待つだけの client（FR-83）は、Hello の Parent の編集の client のタブにファイルを開かせ、タブが閉じられたら理由の無い終わりの知らせを受ける。
// 編集の client が :! を打つと、server は端末を渡す実行を頼み（TerminalRun）、client の TerminalDone を受けて続ける（FR-80）。
// :! の実行中に待つだけの client が繋ぐと、端末を取り戻す頼み（TerminalReclaim）を送り、その後に画面を送る。
// タブが閉じられたら TerminalRelease を送り、TerminalReleased を受けてから待つだけの client を終わらせる（FR-117）。
//
// req: FR-80, FR-83, FR-117
func TestServeWaitClientAndTerminal(t *testing.T) {
	dir := t.TempDir()
	a, msg := filepath.Join(dir, "a.txt"), filepath.Join(dir, "MSG")
	if err := os.WriteFile(msg, []byte("# edit me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newServer(t)
	sock := filepath.Join(privateDir(t), "s", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln, nil) }()
	c, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	hello := protocol.Hello{Kind: protocol.KindEdit, Width: 60, Height: 10, Files: []string{a}, Features: []string{protocol.FeatureTerminal}}
	if err = c.Send(hello); err != nil {
		t.Fatal(err)
	}
	recvType[protocol.Draw](t, c)

	// 端末の受け渡しを宣言しない client（版の違う client）の :! は、頼みを送らずに E319 にし、その client のキーを処理し続ける（PR #234 のレビューの必須 1）。
	old, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 60, Height: 10}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, old, "[No Name]")
	sendKeys(t, old, ":!ls<CR>")
	waitScreen(t, old, "E319")
	sendKeys(t, old, "iok<Esc>")
	waitScreen(t, old, "ok")
	sendKeys(t, old, ":q!<CR>")
	if ex, _ := recvType[protocol.Exit](t, old); ex.Reason != "" {
		t.Errorf("宣言しない client の終わり = %+v", ex)
	}
	_ = old.Close()

	// :! の外の待つだけの client: 操作中の client のタブに開き、:q で閉じると、理由なしで終わる。
	w1, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w1.Close() }()
	if err = w1.Send(protocol.Hello{Kind: protocol.KindWait, Files: []string{msg}}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, c, "# edit me")
	sendKeys(t, c, ":q<CR>")
	if ex, _ := recvType[protocol.Exit](t, w1); ex.Reason != "" {
		t.Errorf("待つだけの client の終わり = %+v, want 理由なし", ex)
	}

	// :! の中の待つだけの client（FR-117）。
	sendKeys(t, c, ":!git commit<CR>")
	run, _ := recvType[protocol.TerminalRun](t, c)
	if len(run.Commands) != 1 || run.Commands[0].Shell != "git commit" || !run.Prompt {
		t.Fatalf("TerminalRun = %+v", run)
	}
	w2, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w2.Close() }()
	if err = w2.Send(protocol.Hello{Kind: protocol.KindWait, Files: []string{msg}, Parent: 1}); err != nil {
		t.Fatal(err)
	}
	if r, _ := recvType[protocol.TerminalReclaim](t, c); r.ID != run.ID {
		t.Errorf("TerminalReclaim = %+v", r)
	}
	// 取り戻す頼みの後に、開いたタブの画面が届く。
	waitScreen(t, c, "# edit me")
	sendKeys(t, c, "ggOfix<Esc>:wq<CR>")
	if r, _ := recvType[protocol.TerminalRelease](t, c); r.ID != run.ID {
		t.Errorf("TerminalRelease = %+v", r)
	}
	if err = c.Send(protocol.TerminalReleased{ID: run.ID}); err != nil {
		t.Fatal(err)
	}
	if ex, _ := recvType[protocol.Exit](t, w2); ex.Reason != "" {
		t.Errorf("待つだけの client の終わり = %+v, want 理由なし", ex)
	}
	if b, rerr := os.ReadFile(msg); rerr != nil || string(b) != "fix\n# edit me\n" {
		t.Errorf("MSG = %q, %v", b, rerr)
	}
	if err = c.Send(protocol.TerminalDone{ID: run.ID}); err != nil {
		t.Fatal(err)
	}
	sendKeys(t, c, ":qa!<CR>")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
}
