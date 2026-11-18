package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/ui"
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

	if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 40, Height: 10}); err != nil {
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
	if m := ms[len(ms)-1]; m != (protocol.Exit{}) {
		t.Errorf("last message = %#v, want Exit without a reason", m)
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
				if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 80, Height: 24}); err != nil {
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
	if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 80, Height: 24}); err != nil {
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

// privateDir は、権限 0700 の一時ディレクトリを返す。Listen は、ソケットのディレクトリの親も検査するので、
// t.TempDir()（権限は umask による）の下に 0700 のディレクトリを 1 段挟む。
func privateDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func newClientTerm() *clientTerm {
	return &clientTerm{log: slog.New(slog.DiscardHandler), events: make(chan editor.TermEvent)}
}

// イベントループへ渡すイベントには、接続ごとに割り当てた client の識別子が付く（基本設計 2.1）。
// Hello の端末の大きさを Size に使い、大きさが無ければ既定の大きさにする。
func TestClientTermAttach(t *testing.T) {
	key := editcmd.Key{Code: editcmd.CodeRune, Rune: 'x'}

	c, s := protocol.Pipe()
	ct := newClientTerm()
	if err := c.Send(protocol.Event{Key: key}); err != nil {
		t.Fatal(err)
	}
	ct.attach(7, s, protocol.Hello{Kind: protocol.KindEdit, Width: 100, Height: 30})
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
	ct.finish("")

	_, s = protocol.Pipe()
	ct = newClientTerm()
	ct.attach(8, s, protocol.Hello{Kind: protocol.KindEdit})
	if w, h := ct.Size(); w != defaultWidth || h != defaultHeight {
		t.Errorf("Size = %dx%d, want the default %dx%d", w, h, defaultWidth, defaultHeight)
	}
	_ = s.Close()
	ct.finish("")
}

// 大きさの変化の Event の大きさが範囲の外なら、イベントループへ渡さずに捨てる（PR #200 のレビューの推奨 1）。
func TestClientTermClampsResize(t *testing.T) {
	key := editcmd.Key{Code: editcmd.CodeRune, Rune: 'x'}
	c, s := protocol.Pipe()
	ct := newClientTerm()
	for _, ev := range []protocol.Event{
		{Resize: true, Width: 0, Height: 24},
		{Resize: true, Width: 2100, Height: 20},
		{Resize: true, Width: 100000, Height: 100000},
		{Key: key},
	} {
		if err := c.Send(ev); err != nil {
			t.Fatal(err)
		}
	}
	ct.attach(1, s, protocol.Hello{Kind: protocol.KindEdit, Width: 80, Height: 24})
	if ev := <-ct.Input(); !ev.Resize || ev.Width != 2100 || ev.Height != 20 {
		t.Errorf("first event = %+v, want the resize 2100x20 as is", ev)
	}
	if ev := <-ct.Input(); !ev.Resize || ev.Width != protocol.MaxWidth || ev.Width*ev.Height > protocol.MaxCells {
		t.Errorf("second event = %+v, want a resize clamped to the limits", ev)
	}
	if ev := <-ct.Input(); ev.Key != key {
		t.Errorf("third event = %+v, want key x", ev)
	}
	_ = c.Close()
	ct.finish("")
}

// Hello の大きさが上限を超えていても断らず、上限へ切り詰めた大きさで格子を作る（PR #200 の再レビューの必須 2）。
// 上限に届かない大きな端末（2100 列）は、そのままの大きさで描く。
func TestRunAcceptsLargeTerminal(t *testing.T) {
	for _, tt := range []struct {
		w, h           int
		wantW, wantMax int
	}{
		{2100, 20, 2100, 2100 * 20},
		{100000, 100000, protocol.MaxWidth, protocol.MaxCells},
	} {
		srv := newServer(t)
		c, s := protocol.Pipe()
		done := make(chan error, 1)
		go func() { done <- srv.Run(context.Background(), s) }()
		if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: tt.w, Height: tt.h}); err != nil {
			t.Fatal(err)
		}
		m, err := c.Recv()
		if err != nil {
			t.Fatal(err)
		}
		d, ok := m.(protocol.Draw)
		if !ok {
			t.Fatalf("first message = %#v, want Draw", m)
		}
		g := d.Screen.Grid
		if g.Width() != tt.wantW || g.Width()*g.Height() > tt.wantMax {
			t.Errorf("hello %dx%d: screen %dx%d, want width %d and at most %d cells", tt.w, tt.h, g.Width(), g.Height(), tt.wantW, tt.wantMax)
		}
		sendKeys(t, c, ":q<CR>")
		go func() {
			for {
				if _, err := c.Recv(); err != nil {
					return
				}
			}
		}()
		if err := <-done; err != nil {
			t.Fatalf("Run = %v", err)
		}
	}
}

// 最初のメッセージが編集の client の Hello でなければ、Run は ErrUnexpectedHello で戻り、接続を閉じる。
func TestRunRejectsNonHello(t *testing.T) {
	for _, tt := range []struct {
		first protocol.ClientMessage
		want  error
	}{
		{protocol.Event{Resize: true, Width: 80, Height: 24}, ErrUnexpectedHello},
		{protocol.Hello{Kind: protocol.KindWait}, ErrUnexpectedHello},
	} {
		first := tt.first
		srv := newServer(t)
		c, s := protocol.Pipe()
		done := make(chan error, 1)
		go func() { done <- srv.Run(context.Background(), s) }()
		if err := c.Send(first); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, tt.want) {
				t.Fatalf("first %#v: Run = %v, want %v", first, err, tt.want)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return")
		}
		if _, err := c.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("client Recv after Run: err = %v, want io.EOF", err)
		}
	}
}

// blockingConn は、release を閉じるまで Send が止まる接続である。読み取りの遅い client を表す。
type blockingConn struct {
	release chan struct{}
	closed  chan struct{}
	once    sync.Once

	mu    sync.Mutex
	sent  []protocol.ServerMessage
	first chan struct{} // 最初の Send が始まったら閉じる
	fonce sync.Once
}

func newBlockingConn() *blockingConn {
	return &blockingConn{release: make(chan struct{}), closed: make(chan struct{}), first: make(chan struct{})}
}

func (b *blockingConn) Send(m protocol.ServerMessage) error {
	b.fonce.Do(func() { close(b.first) })
	select {
	case <-b.release:
	case <-b.closed:
		return protocol.ErrClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, m)
	return nil
}

func (b *blockingConn) Recv() (protocol.ClientMessage, error) {
	<-b.closed
	return nil, protocol.ErrClosed
}

func (b *blockingConn) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

// 読み取りの遅い client があっても、Draw と SetClipboard はイベントループを待たせない。
// 送れるようになったら、client には最新の画面と、最新のクリップボードへの書き込みだけが届く（PR #187 のレビューの推奨 2）。
func TestClientTermDoesNotBlockOnSlowClient(t *testing.T) {
	conn := newBlockingConn()
	ct := newClientTerm()
	ct.attach(1, conn, protocol.Hello{Kind: protocol.KindEdit, Width: 10, Height: 2})

	screens := make([]*ui.Grid, 100)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range screens {
			screens[i] = ui.NewGrid(10, 2)
			_ = ct.Draw(ui.Screen{Grid: screens[i]})
			if i == 0 {
				<-conn.first // 最初の画面を送る途中で止める
			}
			if i%10 == 0 {
				_ = ct.SetClipboard(fmt.Sprint(i))
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Draw and SetClipboard blocked on a slow client")
	}
	close(conn.release)
	// 送る goroutine が置き場を空にするまで待つ。
	deadline := time.Now().Add(5 * time.Second)
	for {
		ct.out.mu.Lock()
		empty := ct.out.screen == nil && ct.out.clip == nil
		ct.out.mu.Unlock()
		conn.mu.Lock()
		n := len(conn.sent)
		var last protocol.ServerMessage
		if n > 0 {
			last = conn.sent[n-1]
		}
		conn.mu.Unlock()
		if d, ok := last.(protocol.Draw); empty && ok && d.Screen.Grid == screens[len(screens)-1] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the latest screen was not sent: %d messages", n)
		}
		time.Sleep(time.Millisecond)
	}
	ct.finish("")
	_ = conn.Close()

	conn.mu.Lock()
	defer conn.mu.Unlock()
	var draws int
	var clips []string
	for _, m := range conn.sent {
		switch m := m.(type) {
		case protocol.Draw:
			draws++
		case protocol.SetClipboard:
			clips = append(clips, m.Text)
		}
	}
	if draws > 3 {
		t.Errorf("%d screens were sent, want at most 3 (the one in flight, and the latest)", draws)
	}
	// 止まっている間の書き込みは、最新の 1 つにまとめる（PR #200 のレビューの推奨 7）。
	want := []string{"90"}
	if !slices.Equal(clips, want) {
		t.Errorf("clipboard = %q, want %q", clips, want)
	}
}

// Run の終わりで、まだ送っていないクリップボードへの書き込みを送ってから接続を閉じる。
func TestRunFlushesClipboardBeforeClose(t *testing.T) {
	srv := newServer(t)
	c, s := protocol.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background(), s) }()
	if err := c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 40, Height: 10}); err != nil {
		t.Fatal(err)
	}
	msgs := collect(c)
	sendKeys(t, c, "ilast<Esc>yy:q!<CR>")
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	var clips []string
	for _, m := range <-msgs {
		if m, ok := m.(protocol.SetClipboard); ok {
			clips = append(clips, m.Text)
		}
	}
	if !slices.Equal(clips, []string{"last\n"}) {
		t.Errorf("clipboard = %q, want [\"last\\n\"]", clips)
	}
}

// server のソケットで待ち受けた Serve は、最初に Hello を送った編集の client を繋ぎ、その接続で編集して :q で終わる。
// 編集の client を繋いだ後に届いた接続は、受け付けてすぐ閉じる。Serve はソケットのファイルを消して戻る。
func TestServeOverSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	srv := newServer(t)
	if err := srv.Open(path); err != nil {
		t.Fatal(err)
	}
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
	if err = c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 40, Height: 10}); err != nil {
		t.Fatal(err)
	}
	m, err := c.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := m.(protocol.Draw); !ok || d.Screen.Grid.Width() != 40 || d.Screen.Grid.Height() != 10 {
		t.Fatalf("first message = %#v, want a 40x10 Draw", m)
	}

	second, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = second.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("second client Recv = %v, want io.EOF (closed by the server)", err)
	}
	_ = second.Close()

	msgs := collect(c)
	sendKeys(t, c, "isock<Esc>:wq<CR>")
	select {
	case serr := <-done:
		if serr != nil {
			t.Fatalf("Serve = %v, want nil", serr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
	<-msgs
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "sock\n" {
		t.Errorf("saved %q, want %q", b, "sock\n")
	}
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("socket file after Serve: %v, want removed", err)
	}
}

// Hello を送らない接続や、編集の client でない接続があっても、Serve はそれを閉じて次の接続を待つ。
// 待つ間に ctx が終わったら ctx.Err() で戻る。
func TestServeSkipsBadCandidates(t *testing.T) {
	srv := newServer(t)
	sock := filepath.Join(privateDir(t), "s", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln, nil) }()

	bad, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	if err = bad.Send(protocol.Event{Paste: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err = bad.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("bad client Recv = %v, want io.EOF", err)
	}
	_ = bad.Close()

	good, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = good.Close() }()
	if err := good.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 20, Height: 5}); err != nil {
		t.Fatal(err)
	}
	if m, err := good.Recv(); err != nil {
		t.Fatalf("good client Recv = %#v, %v; want a Draw", m, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve = %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
}

// Listen は、ソケットのディレクトリを 0700 で作り、他の利用者の権限があるディレクトリでは待ち受けない（基本設計 1.2）。
// 繋げない古いソケットは消して待ち受け、繋げるソケットがあれば誤りにする。
func TestListen(t *testing.T) {
	base := privateDir(t)
	sock := filepath.Join(base, "new", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Dir(sock))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v, want 0700", fi.Mode().Perm())
	}
	if _, err = Listen(sock); err == nil {
		t.Error("Listen on a live socket succeeded")
	}
	_ = ln.Close()

	// 古いソケット: 待ち受けたまま消さずに閉じたものを作る。
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	_ = l.Close()
	ln, err = Listen(sock)
	if err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	_ = ln.Close()

	open := filepath.Join(base, "open")
	if err = os.Mkdir(open, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(open, 0o755); err != nil { //nolint:gosec // 他の利用者の権限があるディレクトリを、検査が断ることを確かめる
		t.Fatal(err)
	}
	if _, err = Listen(filepath.Join(open, SocketName)); err == nil {
		t.Error("Listen in a directory readable by other users succeeded")
	}
	// 親のディレクトリを他の利用者が書けるなら、`<名前>/` が 0700 でも断る（基本設計 1.2。PR #200 のレビューの推奨 5）。
	if _, err = Listen(filepath.Join(open, "d", SocketName)); err == nil {
		t.Error("Listen under a parent directory accessible by other users succeeded")
	}
	// ListenInOwnDir は、呼び出し側が排他的に作ったディレクトリなので、親を検査しない。
	own, err := os.MkdirTemp(open, "own")
	if err != nil {
		t.Fatal(err)
	}
	ln, err = ListenInOwnDir(filepath.Join(own, SocketName))
	if err != nil {
		t.Fatalf("ListenInOwnDir: %v", err)
	}
	_ = ln.Close()
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(base, "new"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filepath.Join(link, SocketName)); err == nil {
		t.Error("Listen in a symlinked directory succeeded")
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file); err == nil {
		t.Error("Listen over a regular file succeeded")
	}
}
