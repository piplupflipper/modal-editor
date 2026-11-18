package client

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/term"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// fakeTerm は、テスト用の端末である。出したものを記録する。
type fakeTerm struct {
	in chan term.Event

	mu       sync.Mutex
	draws    []ui.Screen
	clip     []string
	restored int
}

func newFakeTerm() *fakeTerm { return &fakeTerm{in: make(chan term.Event, 8)} }

func (f *fakeTerm) Input() <-chan term.Event { return f.in }
func (f *fakeTerm) Size() (int, int)         { return 120, 40 }

func (f *fakeTerm) Draw(s ui.Screen) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.draws = append(f.draws, s)
	return nil
}

func (f *fakeTerm) SetClipboard(text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clip = append(f.clip, text)
	return nil
}

func (f *fakeTerm) Restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored++
	return nil
}

func runClient(tm Terminal, c protocol.ClientConn) <-chan error {
	done := make(chan error, 1)
	go func() { done <- Run(tm, c, nil) }()
	return done
}

func wait(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

// client は、最初に編集の client の Hello で端末の大きさを送り、端末のキーをそのまま server へ送る。
// server から届いた画面とクリップボードへの書き込みを届いた順に端末へ出し、server が閉じたら端末を戻して戻る。
func TestRunForwardsAndApplies(t *testing.T) {
	tm := newFakeTerm()
	c, s := protocol.Pipe()
	done := runClient(tm, c)

	first, err := s.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if want := (protocol.Hello{Kind: protocol.KindEdit, Width: 120, Height: 40}); first != want {
		t.Fatalf("first message = %+v, want %+v", first, want)
	}
	key := editcmd.Key{Code: editcmd.CodeEsc}
	tm.in <- term.Event{Key: key}
	tm.in <- term.Event{Paste: "p"}
	if ev, err := s.Recv(); err != nil || ev != (protocol.Event{Key: key}) {
		t.Fatalf("event = %+v, %v; want <Esc>", ev, err)
	}
	if ev, err := s.Recv(); err != nil || ev != (protocol.Event{Paste: "p"}) {
		t.Fatalf("event = %+v, %v; want paste", ev, err)
	}

	g := ui.NewGrid(3, 2)
	if err := s.Send(protocol.Draw{Screen: ui.Screen{Grid: g}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(protocol.SetClipboard{Text: "yank"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(protocol.Exit{}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	wait(t, done)

	tm.mu.Lock()
	defer tm.mu.Unlock()
	if len(tm.draws) != 1 || tm.draws[0].Grid != g {
		t.Errorf("draws = %v, want the one screen the server sent", tm.draws)
	}
	if len(tm.clip) != 1 || tm.clip[0] != "yank" {
		t.Errorf("clipboard = %q, want [yank]", tm.clip)
	}
	if tm.restored != 1 {
		t.Errorf("Restore called %d times, want 1", tm.restored)
	}
}

// server が終わりの知らせ（Exit）を送らずに接続を閉じたら、client は端末を戻してから ErrServerLost を返す。
// 理由付きの Exit なら、その理由の ServerExitError を返す（PR #200 のレビューの推奨 2）。
func TestRunReportsServerEnd(t *testing.T) {
	for _, tt := range []struct {
		name string
		last []protocol.ServerMessage
		want func(error) bool
	}{
		{"lost", nil, func(err error) bool { return errors.Is(err, ErrServerLost) }},
		{"reason", []protocol.ServerMessage{protocol.Exit{Reason: "stopped"}}, func(err error) bool {
			var e *ServerExitError
			return errors.As(err, &e) && e.Reason == "stopped"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tm := newFakeTerm()
			c, s := protocol.Pipe()
			done := runClient(tm, c)
			if _, err := s.Recv(); err != nil {
				t.Fatal(err)
			}
			for _, m := range tt.last {
				if err := s.Send(m); err != nil {
					t.Fatal(err)
				}
			}
			_ = s.Close()
			select {
			case err := <-done:
				if !tt.want(err) {
					t.Fatalf("Run = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return")
			}
			tm.mu.Lock()
			defer tm.mu.Unlock()
			if tm.restored != 1 {
				t.Errorf("Restore called %d times, want 1", tm.restored)
			}
		})
	}
}

// 端末の入力が終わったら、client は接続を閉じて server に知らせ、端末を戻して戻る。
func TestRunClosesWhenTerminalInputEnds(t *testing.T) {
	tm := newFakeTerm()
	c, s := protocol.Pipe()
	done := runClient(tm, c)
	if _, err := s.Recv(); err != nil {
		t.Fatal(err)
	}
	close(tm.in)
	if _, err := s.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("server Recv: err = %v, want io.EOF", err)
	}
	wait(t, done)
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.restored != 1 {
		t.Errorf("Restore called %d times, want 1", tm.restored)
	}
}
