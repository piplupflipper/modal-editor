package server

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	srv, err := New(editor.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return srv
}

func sendKeys(t *testing.T, c protocol.ClientConn, s string) {
	t.Helper()
	for _, k := range editcmd.MustParseKeys(s) {
		if err := c.Send(protocol.Event{Key: k}); err != nil {
			t.Fatalf("Send %v: %v", k, err)
		}
	}
}

// collect は、server から届いたメッセージを、接続が閉じるまで集めて返すチャネルを返す。
func collect(c protocol.ClientConn) <-chan []protocol.ServerMessage {
	out := make(chan []protocol.ServerMessage, 1)
	go func() {
		var ms []protocol.ServerMessage
		for {
			m, err := c.Recv()
			if err != nil {
				out <- ms
				return
			}
			ms = append(ms, m)
		}
	}()
	return out
}

// client から送ったキーで編集と保存をし、:q で終わると、server は画面とクリップボードへの書き込みを client へ送り、
// Run が nil で戻って接続を閉じる。
func TestRunEditsThroughConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	srv := newServer(t)
	if err := srv.Open(path); err != nil {
		t.Fatal(err)
	}
	c, s := protocol.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background(), s) }()

	if err := c.Send(protocol.Event{Resize: true, Width: 40, Height: 10}); err != nil {
		t.Fatal(err)
	}
	msgs := collect(c)
	sendKeys(t, c, "ihello<Esc>yy:w<CR>:q<CR>")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	ms := <-msgs // Run が接続を閉じたので、集め終わる
	var draws int
	var clip []string
	var last protocol.Draw
	for _, m := range ms {
		switch m := m.(type) {
		case protocol.Draw:
			draws++
			last = m
		case protocol.SetClipboard:
			clip = append(clip, m.Text)
		}
	}
	if draws == 0 {
		t.Fatal("no Draw was sent")
	}
	if g := last.Screen.Grid; g.Width() != 40 || g.Height() != 10 {
		t.Errorf("screen size = %dx%d, want the size the client sent (40x10)", g.Width(), g.Height())
	}
	if len(clip) != 1 || clip[0] != "hello\n" {
		t.Errorf("SetClipboard = %q, want [\"hello\\n\"]", clip)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello\n" {
		t.Errorf("saved %q, want %q", b, "hello\n")
	}
}

// client が接続を閉じたら（端末の入力が終わった）、Run は editor.ErrTerminalLost で戻る。
func TestRunReturnsWhenClientCloses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		resize bool
	}{
		{"after size", true},
		{"before size", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t)
			c, s := protocol.Pipe()
			done := make(chan error, 1)
			go func() { done <- srv.Run(context.Background(), s) }()
			if tt.resize {
				if err := c.Send(protocol.Event{Resize: true, Width: 80, Height: 24}); err != nil {
					t.Fatal(err)
				}
			}
			_ = c.Close()
			select {
			case err := <-done:
				if !errors.Is(err, editor.ErrTerminalLost) {
					t.Fatalf("Run = %v, want ErrTerminalLost", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not return")
			}
		})
	}
}

// ctx が終わったら、Run は ctx.Err() で戻り、接続を閉じる。
func TestRunReturnsOnContextDone(t *testing.T) {
	srv := newServer(t)
	c, s := protocol.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, s) }()
	if err := c.Send(protocol.Event{Resize: true, Width: 80, Height: 24}); err != nil {
		t.Fatal(err)
	}
	msgs := collect(c)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
	<-msgs
	if _, err := c.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("client Recv after Run: err = %v, want io.EOF", err)
	}
}

// client が最初のメッセージを送らないまま ctx が終わっても、Run は ctx.Err() で戻り、接続を閉じる。
func TestRunReturnsOnContextDoneBeforeFirstMessage(t *testing.T) {
	srv := newServer(t)
	c, s := protocol.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, s) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return while the client sent nothing")
	}
	if _, err := c.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("client Recv after Run: err = %v, want io.EOF", err)
	}
}

// イベントループへ渡すイベントには、接続ごとに割り当てた client の識別子が付く（基本設計 2.1）。
// 最初のイベントが端末の大きさなら Size に使い、そうでなければ既定の大きさにしてイベントとして渡す。
func TestClientTermAttach(t *testing.T) {
	key := editcmd.Key{Code: editcmd.CodeRune, Rune: 'x'}

	c, s := protocol.Pipe()
	ct := &clientTerm{events: make(chan editor.TermEvent)}
	if err := c.Send(protocol.Event{Resize: true, Width: 100, Height: 30}); err != nil {
		t.Fatal(err)
	}
	if err := c.Send(protocol.Event{Key: key}); err != nil {
		t.Fatal(err)
	}
	if err := ct.attach(context.Background(), 7, s); err != nil {
		t.Fatal(err)
	}
	if w, h := ct.Size(); w != 100 || h != 30 {
		t.Errorf("Size = %dx%d, want 100x30", w, h)
	}
	if ev := <-ct.Input(); ev.Key != key || ev.Client != 7 {
		t.Errorf("event = %+v, want key x from client 7", ev)
	}
	_ = c.Close()
	if _, ok := <-ct.Input(); ok {
		t.Error("Input is not closed after the client closed")
	}

	c, s = protocol.Pipe()
	ct = &clientTerm{events: make(chan editor.TermEvent)}
	if err := c.Send(protocol.Event{Key: key}); err != nil {
		t.Fatal(err)
	}
	if err := ct.attach(context.Background(), 8, s); err != nil {
		t.Fatal(err)
	}
	if w, h := ct.Size(); w != defaultWidth || h != defaultHeight {
		t.Errorf("Size = %dx%d, want the default %dx%d", w, h, defaultWidth, defaultHeight)
	}
	if ev := <-ct.Input(); ev.Key != key || ev.Client != 8 {
		t.Errorf("event = %+v, want key x from client 8", ev)
	}
	_ = c.Close()
}
