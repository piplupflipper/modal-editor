package protocol

import (
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// socketPair は、一時ディレクトリの Unix ドメインソケットで繋いだ client と server の両端を返す。
func socketPair(t *testing.T) (ClientConn, ServerConn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	c, err := Dial(path)
	if err != nil {
		t.Fatal(err)
	}
	sc, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	s := NewServerSocket(sc)
	t.Cleanup(func() {
		_ = c.Close()
		_ = s.Close()
	})
	return c, s
}

// screenOf は、行の文字列から格子を作る。
func screenOf(width int, rows ...string) *ui.Grid {
	g := ui.NewGrid(width, len(rows))
	for r, s := range rows {
		var cells []ui.Cell
		for _, ch := range s {
			cells = append(cells, ui.Cell{Text: string(ch), Width: 1})
		}
		g.SetRow(r, cells)
	}
	return g
}

func sameGrid(a, b *ui.Grid) bool {
	if a.Width() != b.Width() || a.Height() != b.Height() {
		return false
	}
	for r := range a.Height() {
		if !sameRow(a, b, r) {
			return false
		}
	}
	return true
}

// ソケットの接続は、client の Hello とイベント、server の画面とクリップボードを、送った順に同じ値で届ける。
func TestSocketRoundTrip(t *testing.T) {
	c, s := socketPair(t)

	sent := []ClientMessage{
		Hello{Kind: KindEdit, Width: 80, Height: 24},
		Event{Key: editcmd.Key{Code: editcmd.CodeRune, Rune: 'j'}},
		Event{Key: editcmd.Key{Code: editcmd.CodeRune, Rune: 'w', Mod: editcmd.ModCtrl}},
		Event{Key: editcmd.Key{Code: editcmd.CodeEsc}},
		Event{Paste: "a\nb\tc"},
		Event{Resize: true, Width: 100, Height: 30},
	}
	for _, m := range sent {
		if err := c.Send(m); err != nil {
			t.Fatalf("client Send %#v: %v", m, err)
		}
	}
	for _, want := range sent {
		got, err := s.Recv()
		if err != nil || got != want {
			t.Fatalf("server Recv = %#v, %v; want %#v", got, err, want)
		}
	}

	wide := ui.NewGrid(4, 2)
	red := ui.Attr{Fg: ui.Indexed(1), Bold: true}
	wide.SetRow(0, []ui.Cell{
		{Text: "あ", Width: 2, Attr: red},
		{Text: "", Width: 0, Attr: red},
		{Text: "é", Width: 1},
		{Text: "x", Width: 1, Attr: ui.Attr{Bg: ui.RGB(1, 2, 3), Reverse: true}},
	})
	screens := []ui.Screen{
		{Grid: wide, Cursor: ui.Cursor{Row: 1, Col: 2, Shape: ui.CursorBar, Visible: true}},
		{Grid: screenOf(4, "abcd", "efgh")},                                                // 1 行目だけでなく 2 行目も変わる
		{Grid: screenOf(4, "abcd", "efgX")},                                                // 2 行目だけ変わる
		{Grid: screenOf(3, "abc", "def", "ghi")},                                           // 大きさが変わる
		{Grid: screenOf(3, "abc", "def", "ghi"), Cursor: ui.Cursor{Row: 2, Visible: true}}, // カーソルだけ変わる
	}
	for i, scr := range screens {
		if err := s.Send(Draw{Screen: scr}); err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			if err := s.Send(SetClipboard{Text: "yank\n"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i, want := range screens {
		m, err := c.Recv()
		if err != nil {
			t.Fatal(err)
		}
		d, ok := m.(Draw)
		if !ok {
			t.Fatalf("client Recv = %#v, want Draw", m)
		}
		if !sameGrid(d.Screen.Grid, want.Grid) || d.Screen.Cursor != want.Cursor {
			t.Fatalf("screen %d = %q %+v, want %q %+v", i, d.Screen.Grid.String(), d.Screen.Cursor, want.Grid.String(), want.Cursor)
		}
		if i == 1 {
			m, err := c.Recv()
			if err != nil || m != (SetClipboard{Text: "yank\n"}) {
				t.Fatalf("client Recv = %#v, %v; want SetClipboard", m, err)
			}
		}
	}
}

// 2 回目からは、前回送った格子から変わった行だけを載せる。
func TestScreenEncoderSendsChangedRows(t *testing.T) {
	var enc screenEncoder
	if d := enc.encode(ui.Screen{Grid: screenOf(3, "abc", "def", "ghi")}); len(d.Rows) != 3 {
		t.Fatalf("first encode has %d rows, want all 3", len(d.Rows))
	}
	d := enc.encode(ui.Screen{Grid: screenOf(3, "abc", "dXf", "ghi")})
	if len(d.Rows) != 1 || d.Rows[0].Row != 1 {
		t.Fatalf("rows = %+v, want only row 1", d.Rows)
	}
	if d := enc.encode(ui.Screen{Grid: screenOf(3, "abc", "dXf", "ghi")}); len(d.Rows) != 0 {
		t.Fatalf("rows = %+v, want none for the same screen", d.Rows)
	}
	if d := enc.encode(ui.Screen{Grid: screenOf(2, "ab", "dX", "gh")}); len(d.Rows) != 3 {
		t.Fatalf("encode after resize has %d rows, want all 3", len(d.Rows))
	}
}

// 相手が閉じたら、Recv は届いていたものを返してから io.EOF を返し、Send は ErrClosed を返す。自分が閉じた後は ErrClosed を返す。
func TestSocketClose(t *testing.T) {
	c, s := socketPair(t)
	if err := s.Send(SetClipboard{Text: "last"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if m, err := c.Recv(); err != nil || m != (SetClipboard{Text: "last"}) {
		t.Fatalf("client Recv = %#v, %v; want the message sent before Close", m, err)
	}
	if _, err := c.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("client Recv after server Close: err = %v, want io.EOF", err)
	}
	// 相手が閉じた後の最初の書き込みはカーネルに溜まって成功しうるので、誤りが返るまで送る。
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := c.Send(Event{Paste: "x"})
		if errors.Is(err, ErrClosed) {
			break
		}
		if err != nil {
			t.Fatalf("client Send after server Close: err = %v, want ErrClosed", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("client Send keeps succeeding after server Close")
		}
		time.Sleep(time.Millisecond)
	}
	_ = c.Close()
	if _, err := c.Recv(); !errors.Is(err, ErrClosed) {
		t.Fatalf("client Recv after own Close: err = %v, want ErrClosed", err)
	}
	if err := c.Send(Event{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("client Send after own Close: err = %v, want ErrClosed", err)
	}
}

// 待っている Recv は、自分の Close で ErrClosed で戻る。
func TestSocketCloseUnblocksRecv(t *testing.T) {
	_, s := socketPair(t)
	done := make(chan error, 1)
	go func() {
		_, err := s.Recv()
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	_ = s.Close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Recv = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recv did not return after Close")
	}
}
