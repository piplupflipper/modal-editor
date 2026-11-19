package protocol

import (
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

func TestPipeDeliversInOrder(t *testing.T) {
	c, s := Pipe()
	for _, r := range "abc" {
		if err := c.Send(Event{Key: editcmd.Key{Code: editcmd.CodeRune, Rune: r}}); err != nil {
			t.Fatalf("client Send: %v", err)
		}
	}
	for _, want := range "abc" {
		ev, err := s.Recv()
		if err != nil {
			t.Fatalf("server Recv: %v", err)
		}
		if e, ok := ev.(Event); !ok || e.Key.Rune != want {
			t.Fatalf("server Recv = %#v, want key %q", ev, want)
		}
	}
	if err := s.Send(SetClipboard{Text: "x"}); err != nil {
		t.Fatalf("server Send: %v", err)
	}
	if err := s.Send(Draw{}); err != nil {
		t.Fatalf("server Send: %v", err)
	}
	m, err := c.Recv()
	if err != nil || m != (SetClipboard{Text: "x"}) {
		t.Fatalf("client Recv = %#v, %v; want SetClipboard", m, err)
	}
	m, err = c.Recv()
	if _, ok := m.(Draw); err != nil || !ok {
		t.Fatalf("client Recv = %#v, %v; want Draw", m, err)
	}
}

// 閉じた側の相手は、届いていたものを読み終えてから io.EOF を受ける。
// server が最後に送ったクリップボードを、client が終わる前に受け取れることを確かめる。
func TestPipeDrainsBeforeEOF(t *testing.T) {
	c, s := Pipe()
	if err := s.Send(SetClipboard{Text: "last"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	m, err := c.Recv()
	if err != nil || m != (SetClipboard{Text: "last"}) {
		t.Fatalf("client Recv = %#v, %v; want the message sent before Close", m, err)
	}
	if _, err = c.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("client Recv after drain: err = %v, want io.EOF", err)
	}
	if err = c.Send(Event{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("client Send after server Close: err = %v, want ErrClosed", err)
	}

	c, s = Pipe()
	if err = c.Send(Event{Paste: "p"}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	ev, err := s.Recv()
	if err != nil || ev != (Event{Paste: "p"}) {
		t.Fatalf("server Recv = %#v, %v; want the event sent before Close", ev, err)
	}
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("server Recv after drain: err = %v, want io.EOF", err)
	}
	if err := s.Send(Draw{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("server Send after client Close: err = %v, want ErrClosed", err)
	}
}

// 自分が閉じた後の Recv と Send は ErrClosed を返す。待っている Recv も、相手の Close で戻る。
func TestPipeCloseUnblocks(t *testing.T) {
	c, s := Pipe()
	var wg sync.WaitGroup
	wg.Add(1)
	var recvErr error
	go func() {
		defer wg.Done()
		_, recvErr = s.Recv()
	}()
	_ = c.Close()
	_ = c.Close() // 2 回目は何もしない
	wg.Wait()
	if !errors.Is(recvErr, io.EOF) {
		t.Fatalf("blocked server Recv: err = %v, want io.EOF", recvErr)
	}
	if _, err := c.Recv(); !errors.Is(err, ErrClosed) {
		t.Fatalf("client Recv after own Close: err = %v, want ErrClosed", err)
	}
	_ = s.Close()
	if _, err := s.Recv(); !errors.Is(err, ErrClosed) {
		t.Fatalf("server Recv after own Close: err = %v, want ErrClosed", err)
	}
}

// 送り手が溜められる数を超えて送り、待っている間に相手が閉じたら、Send は ErrClosed で戻る。
func TestPipeSendUnblocksOnPeerClose(t *testing.T) {
	c, s := Pipe()
	done := make(chan error)
	go func() {
		for {
			if err := s.Send(Draw{}); err != nil {
				done <- err
				return
			}
		}
	}()
	_ = c.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("server Send: err = %v, want ErrClosed", err)
	}
}
