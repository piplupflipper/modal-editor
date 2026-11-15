package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitTimeout は、テストが起こるはずのことを待つ上限である。これを超えたらテストを失敗させる。
const waitTimeout = 5 * time.Second

// newPair は、io.Pipe でつないだ 2 つの接続を作る。テストの終わりに両方を閉じる。
func newPair(t *testing.T, ha, hb Handler, opts ...Option) (a, b *Conn) {
	t.Helper()
	aRead, bWrite := io.Pipe()
	bRead, aWrite := io.Pipe()
	a = NewConn(aRead, aWrite, ha, opts...)
	b = NewConn(bRead, bWrite, hb, opts...)
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

// rawPeer は、接続の相手をバイト列の水準で演じる。壊れたメッセージを送るテストに使う。
type rawPeer struct {
	w *io.PipeWriter
	r *bufio.Reader
}

// newRawPair は、接続と、その相手を演じる rawPeer を作る。
func newRawPair(t *testing.T, opts ...Option) (*Conn, *rawPeer) {
	t.Helper()
	return newRawPairHandler(t, nil, opts...)
}

// newRawPairHandler は、ハンドラーを指定して newRawPair と同じものを作る。
func newRawPairHandler(t *testing.T, h Handler, opts ...Option) (*Conn, *rawPeer) {
	t.Helper()
	connRead, peerWrite := io.Pipe()
	peerRead, connWrite := io.Pipe()
	c := NewConn(connRead, connWrite, h, opts...)
	t.Cleanup(func() {
		_ = c.Close()
		_ = peerWrite.Close()
		_ = peerRead.Close()
	})
	return c, &rawPeer{w: peerWrite, r: bufio.NewReader(peerRead)}
}

// readJSON は、接続が送ったメッセージを 1 件読む。
func (p *rawPeer) readJSON(t *testing.T) map[string]any {
	t.Helper()
	body, err := ReadMessage(p.r, DefaultMaxMessageSize)
	if err != nil {
		t.Fatalf("peer read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("peer unmarshal %s: %v", body, err)
	}
	return m
}

// echoHandler は、params をそのまま結果として返すハンドラーを作る。
// method が "fail" なら InvalidParams の誤りを返す。
func echoHandler() Handler {
	return HandlerFuncs{Request: func(_ context.Context, req *Request, reply Replier) {
		if req.Method == "fail" {
			_ = reply(nil, &Error{Code: CodeInvalidParams, Message: "bad params"})
			return
		}
		_ = reply(req.Params, nil)
	}}
}

func waitDone(t *testing.T, c *Conn) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(waitTimeout):
		t.Fatal("connection did not finish")
	}
}

func TestCallRoundTrip(t *testing.T) {
	a, _ := newPair(t, nil, echoHandler())

	var got map[string]int
	if err := a.Call(context.Background(), "echo", map[string]int{"x": 1}, &got); err != nil {
		t.Fatal(err)
	}
	if got["x"] != 1 {
		t.Errorf("got %v", got)
	}

	// result が nil なら結果を読み捨てる。
	if err := a.Call(context.Background(), "echo", 1, nil); err != nil {
		t.Fatal(err)
	}
}

func TestCallBothDirections(t *testing.T) {
	// 双方向: どちらの側もリクエストを送り、相手のハンドラーが応答する。
	a, b := newPair(t, echoHandler(), echoHandler())
	var s string
	if err := a.Call(context.Background(), "echo", "from a", &s); err != nil || s != "from a" {
		t.Errorf("a.Call = %q, %v", s, err)
	}
	if err := b.Call(context.Background(), "echo", "from b", &s); err != nil || s != "from b" {
		t.Errorf("b.Call = %q, %v", s, err)
	}
}

func TestCallErrorResponse(t *testing.T) {
	a, _ := newPair(t, nil, echoHandler())
	err := a.Call(context.Background(), "fail", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams || rpcErr.Message != "bad params" {
		t.Errorf("Call = %v, want *Error with CodeInvalidParams", err)
	}
	// 接続はそのまま使える。
	if err := a.Call(context.Background(), "echo", 1, nil); err != nil {
		t.Errorf("Call after error response = %v", err)
	}
}

func TestCallPlainErrorBecomesInternalError(t *testing.T) {
	h := HandlerFuncs{Request: func(_ context.Context, _ *Request, reply Replier) {
		_ = reply(nil, errors.New("boom"))
	}}
	a, _ := newPair(t, nil, h)
	err := a.Call(context.Background(), "x", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternalError || rpcErr.Message != "boom" {
		t.Errorf("Call = %v, want InternalError boom", err)
	}
}

func TestMethodNotFound(t *testing.T) {
	a, _ := newPair(t, nil, nil)
	err := a.Call(context.Background(), "unknown", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeMethodNotFound {
		t.Errorf("Call = %v, want MethodNotFound", err)
	}
}

func TestReplyTwice(t *testing.T) {
	second := make(chan error, 1)
	h := HandlerFuncs{Request: func(_ context.Context, _ *Request, reply Replier) {
		_ = reply("first", nil)
		second <- reply("second", nil)
	}}
	a, _ := newPair(t, nil, h)
	var s string
	if err := a.Call(context.Background(), "x", nil, &s); err != nil || s != "first" {
		t.Errorf("Call = %q, %v", s, err)
	}
	if err := <-second; err == nil {
		t.Error("second reply succeeded, want error")
	}
}

func TestDeferredReply(t *testing.T) {
	// ハンドラーが reply を保持して戻り、別の goroutine から応答する。
	// その間も読み取りは続くので、応答を待つ側からのリクエストにも答えられる。
	held := make(chan Replier, 1)
	hb := HandlerFuncs{Request: func(_ context.Context, req *Request, reply Replier) {
		if req.Method == "slow" {
			held <- reply
			return
		}
		_ = reply("quick", nil)
	}}
	a, _ := newPair(t, nil, hb)

	slowDone := make(chan error, 1)
	go func() {
		var s string
		err := a.Call(context.Background(), "slow", nil, &s)
		if err == nil && s != "late" {
			err = fmt.Errorf("got %q", s)
		}
		slowDone <- err
	}()
	reply := <-held

	var s string
	if err := a.Call(context.Background(), "quick", nil, &s); err != nil || s != "quick" {
		t.Errorf("quick Call = %q, %v", s, err)
	}
	if err := reply("late", nil); err != nil {
		t.Fatal(err)
	}
	if err := <-slowDone; err != nil {
		t.Errorf("slow Call: %v", err)
	}
}

func TestNotify(t *testing.T) {
	got := make(chan *Notification, 3)
	hb := HandlerFuncs{Notification: func(_ context.Context, n *Notification) { got <- n }}
	a, _ := newPair(t, nil, hb)

	for i := range 3 {
		if err := a.Notify("buffer/change", map[string]int{"version": i}); err != nil {
			t.Fatal(err)
		}
	}
	// 通知は届いた順にハンドラーへ渡る。
	for i := range 3 {
		select {
		case n := <-got:
			var p map[string]int
			if err := json.Unmarshal(n.Params, &p); err != nil {
				t.Fatal(err)
			}
			if n.Method != "buffer/change" || p["version"] != i {
				t.Errorf("notification %d = %s %s", i, n.Method, n.Params)
			}
		case <-time.After(waitTimeout):
			t.Fatal("notification not delivered")
		}
	}
}

func TestConcurrentCalls(t *testing.T) {
	// 双方から同時に多数のリクエストを送り、それぞれが自分の応答を受け取ることを確かめる。
	// go test -race で、送信の直列化と待ち受けの表の競合を検出する。
	a, b := newPair(t, echoHandler(), echoHandler())
	const n = 200
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := range n {
		for _, c := range []*Conn{a, b} {
			wg.Go(func() {
				var got int
				if err := c.Call(context.Background(), "echo", i, &got); err != nil {
					errs <- err
					return
				}
				if got != i {
					errs <- fmt.Errorf("sent %d, got %d", i, got)
				}
				if err := c.Notify("note", i); err != nil {
					errs <- err
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// req: FR-17
func TestCallDeadlineExceeded(t *testing.T) {
	c, peer := newRawPair(t)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	callDone := make(chan error, 1)
	go func() { callDone <- c.Call(ctx, "motion/resolve", nil, nil) }()
	req := peer.readJSON(t)

	// 相手が応答しないまま期限を超えると、Call は DeadlineExceeded を返す。
	select {
	case err := <-callDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Call = %v, want DeadlineExceeded", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Call did not return after the deadline")
	}

	// 期限を超えたリクエストの ID で $/cancel が届く。
	n := peer.readJSON(t)
	params, _ := n["params"].(map[string]any)
	if n["method"] != CancelMethod || params["id"] != req["id"] {
		t.Errorf("got %v, want %s for id %v", n, CancelMethod, req["id"])
	}
}

func TestCancelMethodOption(t *testing.T) {
	// LSP の $/cancelRequest に名前を変える。送る側と受ける側の両方で使う。
	const lspCancel = "$/cancelRequest"
	canceled := make(chan struct{})
	notified := make(chan string, 4)
	hb := HandlerFuncs{
		Request: func(ctx context.Context, _ *Request, reply Replier) {
			go func() {
				<-ctx.Done()
				close(canceled)
				_ = reply(nil, ctx.Err())
			}()
		},
		Notification: func(_ context.Context, n *Notification) { notified <- n.Method },
	}
	a, _ := newPair(t, nil, hb, WithCancelMethod(lspCancel))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "textDocument/hover", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call = %v, want DeadlineExceeded", err)
	}
	select {
	case <-canceled:
	case <-time.After(waitTimeout):
		t.Fatal("handler context not canceled by " + lspCancel)
	}
	// 取り消しの通知は Conn が処理し、ハンドラーには届かない。既定の名前は普通の通知として届く。
	if err := a.Notify(CancelMethod, CancelParams{ID: NumberID(1)}); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-notified:
		if m != CancelMethod {
			t.Errorf("handler got %q, want %q", m, CancelMethod)
		}
	case <-time.After(waitTimeout):
		t.Fatal("notification not delivered")
	}
}

func TestIncomingRequestContext(t *testing.T) {
	// 相手からの取り消しで、そのリクエストのハンドラーの ctx だけが取り消される。
	type started struct {
		id  ID
		ctx context.Context
	}
	starts := make(chan started, 2)
	hb := HandlerFuncs{Request: func(ctx context.Context, req *Request, _ Replier) {
		starts <- started{req.ID, ctx}
	}}
	// 相手からリクエストを 2 件送り、1 件目だけを取り消す。
	c2, peer2 := newRawPairHandler(t, hb)
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"slow"}`,
		`{"jsonrpc":"2.0","id":2,"method":"slow"}`,
		`{"jsonrpc":"2.0","method":"$/cancel","params":{"id":1}}`,
	} {
		if err := WriteMessage(peer2.w, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	first, second := <-starts, <-starts
	select {
	case <-first.ctx.Done():
	case <-time.After(waitTimeout):
		t.Fatal("request 1 context not canceled")
	}
	if second.ctx.Err() != nil {
		t.Error("request 2 context canceled, want alive")
	}
	_ = c2.Close()
	if second.ctx.Err() == nil {
		t.Error("request 2 context alive after Close")
	}
}

// req: FR-17
func TestLateResponseDiscarded(t *testing.T) {
	held := make(chan Replier, 1)
	hb := HandlerFuncs{Request: func(_ context.Context, req *Request, reply Replier) {
		if req.Method == "slow" {
			held <- reply
			return
		}
		_ = reply("fresh", nil)
	}}
	a, _ := newPair(t, nil, hb)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var stale string
	if err := a.Call(ctx, "slow", nil, &stale); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call = %v, want DeadlineExceeded", err)
	}

	// 期限を超えた後に応答を返す。この応答は捨てられ、接続は続く。
	if err := (<-held)("stale", nil); err != nil {
		t.Fatal(err)
	}
	var s string
	if err := a.Call(context.Background(), "fast", nil, &s); err != nil {
		t.Fatal(err)
	}
	if s != "fresh" {
		t.Errorf("got %q, want fresh (late response must not be delivered)", s)
	}
	if stale != "" {
		t.Errorf("late response was written into the timed-out call: %q", stale)
	}
	if err := a.Err(); err != nil {
		t.Errorf("connection ended: %v", err)
	}
	// 遅れた応答は fast の応答より前に読まれている。期限切れの待ち受けは残っていない。
	a.mu.Lock()
	n := len(a.pending)
	a.mu.Unlock()
	if n != 0 {
		t.Errorf("pending has %d entries after the late response, want 0", n)
	}
}

func TestCallCanceledBeforeSend(t *testing.T) {
	a, _ := newRawPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.Call(ctx, "x", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Call = %v, want Canceled", err)
	}
}

func TestUnknownResponseIDDiscarded(t *testing.T) {
	c, peer := newRawPair(t)
	// 送っていない ID への応答は捨て、接続は続ける。
	if err := WriteMessage(peer.w, []byte(`{"jsonrpc":"2.0","id":99,"result":1}`)); err != nil {
		t.Fatal(err)
	}
	callDone := make(chan error, 1)
	go func() {
		var got int
		err := c.Call(context.Background(), "x", nil, &got)
		if err == nil && got != 7 {
			err = fmt.Errorf("got %d", got)
		}
		callDone <- err
	}()
	req := peer.readJSON(t)
	resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":7}`, req["id"])
	if err := WriteMessage(peer.w, []byte(resp)); err != nil {
		t.Fatal(err)
	}
	if err := <-callDone; err != nil {
		t.Error(err)
	}
}

// frame は、body に Content-Length ヘッダーを付けた文字列を返す。
func frame(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

func TestBrokenInputEndsConnection(t *testing.T) {
	tests := map[string]string{
		"broken header":  "Content-Length: abc\r\n\r\n",
		"missing length": "X-Foo: 1\r\n\r\n{}",
		"invalid json":   frame(`{abc}`),
		"wrong version":  frame(`{"jsonrpc":"1.0","method":"ping"}`),
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			c, peer := newRawPair(t)
			callDone := make(chan error, 1)
			go func() { callDone <- c.Call(context.Background(), "x", nil, nil) }()
			peer.readJSON(t)

			if _, err := io.WriteString(peer.w, in); err != nil {
				t.Fatal(err)
			}
			waitDone(t, c)
			if err := c.Err(); !errors.Is(err, ErrProtocol) {
				t.Errorf("Err = %v, want ErrProtocol", err)
			}
			// 応答を待っていた Call は、接続の終了と理由の両方を返す。
			err := <-callDone
			if !errors.Is(err, ErrClosed) || !errors.Is(err, ErrProtocol) {
				t.Errorf("pending Call = %v, want ErrClosed and ErrProtocol", err)
			}
			// 接続が終わった後の送信は失敗する。
			if err := c.Notify("x", nil); !errors.Is(err, ErrClosed) {
				t.Errorf("Notify after end = %v, want ErrClosed", err)
			}
		})
	}
}

func TestPeerDisconnect(t *testing.T) {
	c, peer := newRawPair(t)
	callDone := make(chan error, 1)
	go func() { callDone <- c.Call(context.Background(), "x", nil, nil) }()
	peer.readJSON(t)

	if err := peer.w.Close(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, c)
	if err := c.Err(); !errors.Is(err, io.EOF) {
		t.Errorf("Err = %v, want io.EOF", err)
	}
	if err := <-callDone; !errors.Is(err, ErrClosed) || !errors.Is(err, io.EOF) {
		t.Errorf("pending Call = %v, want ErrClosed and io.EOF", err)
	}
	if err := c.Call(context.Background(), "x", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Call after disconnect = %v, want ErrClosed", err)
	}
}

func TestPeerDisconnectMidMessage(t *testing.T) {
	c, peer := newRawPair(t)
	if _, err := io.WriteString(peer.w, "Content-Length: 100\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	if err := peer.w.Close(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, c)
	if err := c.Err(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestClose(t *testing.T) {
	a, b := newPair(t, nil, HandlerFuncs{Request: func(context.Context, *Request, Replier) {}})
	callDone := make(chan error, 1)
	go func() { callDone <- a.Call(context.Background(), "never", nil, nil) }()

	// 相手が応答しないまま Close すると、待っていた Call は ErrClosed を返す。
	time.Sleep(10 * time.Millisecond)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-callDone; !errors.Is(err, ErrClosed) {
		t.Errorf("pending Call = %v, want ErrClosed", err)
	}
	if err := a.Err(); !errors.Is(err, ErrClosed) {
		t.Errorf("Err = %v, want ErrClosed", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}
	// 片方を閉じると、もう片方からは相手の切断に見える。
	waitDone(t, b)
	if err := b.Err(); !errors.Is(err, io.EOF) {
		t.Errorf("peer Err = %v, want io.EOF", err)
	}
}

func TestHandlerContextCanceledOnClose(t *testing.T) {
	ctxs := make(chan context.Context, 1)
	hb := HandlerFuncs{Notification: func(ctx context.Context, _ *Notification) { ctxs <- ctx }}
	a, b := newPair(t, nil, hb)
	if err := a.Notify("x", nil); err != nil {
		t.Fatal(err)
	}
	ctx := <-ctxs
	if ctx.Err() != nil {
		t.Fatal("handler context canceled before close")
	}
	_ = b.Close()
	select {
	case <-ctx.Done():
	case <-time.After(waitTimeout):
		t.Fatal("handler context not canceled after close")
	}
}

func TestNoHandlerCallAfterClose(t *testing.T) {
	// 相手が通知を大量に流している最中に Close する。
	// bufio に読み込み済みのメッセージが残っていても、Close が戻った後にハンドラーは呼ばれない。
	var closed atomic.Bool
	var after atomic.Int64
	var calls atomic.Int64
	h := HandlerFuncs{Notification: func(context.Context, *Notification) {
		calls.Add(1)
		if closed.Load() {
			after.Add(1)
		}
	}}
	c, peer := newRawPairHandler(t, h)

	var buf bytes.Buffer
	for i := range 1000 {
		if err := WriteMessage(&buf, fmt.Appendf(nil, `{"jsonrpc":"2.0","method":"decoration/set","params":%d}`, i)); err != nil {
			t.Fatal(err)
		}
	}
	go func() { _, _ = peer.w.Write(buf.Bytes()) }()
	for calls.Load() < 10 {
		time.Sleep(time.Millisecond)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	closed.Store(true)
	time.Sleep(50 * time.Millisecond)
	if n := after.Load(); n != 0 {
		t.Errorf("handler called %d times after Close returned", n)
	}
}

func TestCloseWaitsForRunningHandler(t *testing.T) {
	// 実行中のハンドラーは ctx の取り消しで戻り、Close はそれを待ってから戻る。
	entered := make(chan struct{})
	var returned atomic.Bool
	h := HandlerFuncs{Notification: func(ctx context.Context, _ *Notification) {
		close(entered)
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		returned.Store(true)
	}}
	c, peer := newRawPairHandler(t, h)
	if err := WriteMessage(peer.w, []byte(`{"jsonrpc":"2.0","method":"x"}`)); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if !returned.Load() {
		t.Error("Close returned before the running handler")
	}
}

func TestShutdownFlushes(t *testing.T) {
	// Shutdown は、積んだ通知を書き切ってから閉じる。
	got := make(chan string, 1)
	hb := HandlerFuncs{Notification: func(_ context.Context, n *Notification) { got <- n.Method }}
	a, _ := newPair(t, nil, hb)
	if err := a.Notify("exit", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-got:
		if m != "exit" {
			t.Errorf("got %q", m)
		}
	case <-time.After(waitTimeout):
		t.Fatal("exit not delivered before close")
	}
	if !errors.Is(a.Err(), ErrClosed) {
		t.Errorf("Err = %v, want ErrClosed", a.Err())
	}
}

func TestShutdownDeadline(t *testing.T) {
	// 相手が読まないと書き切れないので、期限で書き残しを捨てて閉じる。
	c, _ := newRawPair(t)
	if err := c.Notify("x", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown = %v, want DeadlineExceeded", err)
	}
	waitDone(t, c)
}

func TestOutboxLimit(t *testing.T) {
	// 相手が読まない間、送信の列が溜まり、上限を超えると接続が終わる。
	c, _ := newRawPair(t, WithMaxOutboxBytes(1000))
	var err error
	for range 100 {
		if err = c.Notify("buffer/change", strings.Repeat("a", 50)); err != nil {
			break
		}
	}
	if !errors.Is(err, ErrOutboxFull) || !errors.Is(err, ErrClosed) {
		t.Fatalf("Notify = %v, want ErrOutboxFull and ErrClosed", err)
	}
	waitDone(t, c)
	if !errors.Is(c.Err(), ErrOutboxFull) {
		t.Errorf("Err = %v, want ErrOutboxFull", c.Err())
	}
	if n := c.OutboxBytes(); n != 0 {
		t.Errorf("OutboxBytes after end = %d, want 0", n)
	}
}

func TestOutboxBytes(t *testing.T) {
	c, peer := newRawPair(t)
	if err := c.Notify("x", "abc"); err != nil {
		t.Fatal(err)
	}
	if c.OutboxBytes() == 0 {
		t.Error("OutboxBytes = 0 while the peer is not reading")
	}
	peer.readJSON(t)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := c.OutboxBytes(); n != 0 {
		t.Errorf("OutboxBytes after flush = %d, want 0", n)
	}
}

func TestMaxMessageSizeOption(t *testing.T) {
	c, peer := newRawPair(t, WithMaxMessageSize(16))
	if _, err := io.WriteString(peer.w, frame(`{"jsonrpc":"2.0","method":"long"}`)); err != nil {
		t.Fatal(err)
	}
	waitDone(t, c)
	if !errors.Is(c.Err(), ErrProtocol) {
		t.Errorf("Err = %v, want ErrProtocol", c.Err())
	}
}

func TestGo(t *testing.T) {
	held := make(chan Replier, 1)
	hb := HandlerFuncs{Request: func(_ context.Context, _ *Request, reply Replier) { held <- reply }}
	a, _ := newPair(t, nil, hb)

	// 待たずに戻り、応答が届くと Ready が閉じる。
	f, err := a.Go("motion/resolve", nil)
	if err != nil {
		t.Fatal(err)
	}
	reply := <-held
	select {
	case <-f.Ready():
		t.Fatal("Ready closed before the response")
	default:
	}
	if err := reply(5, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.Ready():
	case <-time.After(waitTimeout):
		t.Fatal("Ready not closed")
	}
	var got int
	if err := f.Result(&got); err != nil || got != 5 {
		t.Errorf("Result = %d, %v", got, err)
	}
	// 結果が決まった後の Cancel は何もしない。
	if err := f.Cancel(); err != nil {
		t.Errorf("Cancel after result = %v", err)
	}
}

func TestGoCancel(t *testing.T) {
	c, peer := newRawPair(t)
	f, err := c.Go("motion/resolve", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := peer.readJSON(t)
	if err := f.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := f.Result(nil); !errors.Is(err, context.Canceled) {
		t.Errorf("Result = %v, want Canceled", err)
	}
	n := peer.readJSON(t)
	if n["method"] != CancelMethod {
		t.Errorf("got %v, want %s", n, CancelMethod)
	}
	// 取り消した後に届いた応答は捨て、待ち受けも残らない。
	if err := WriteMessage(peer.w, fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%v,"result":1}`, req["id"])); err != nil {
		t.Fatal(err)
	}
	// 応答の後に送ったリクエストへ MethodNotFound が返れば、応答は読み終えている。
	if err := WriteMessage(peer.w, []byte(`{"jsonrpc":"2.0","id":"sync","method":"sync"}`)); err != nil {
		t.Fatal(err)
	}
	if r := peer.readJSON(t); r["id"] != "sync" {
		t.Fatalf("got %v", r)
	}
	if err := c.Err(); err != nil {
		t.Errorf("connection ended: %v", err)
	}
	c.mu.Lock()
	n2 := len(c.pending)
	c.mu.Unlock()
	if n2 != 0 {
		t.Errorf("pending has %d entries, want 0", n2)
	}
}

func TestGoClosed(t *testing.T) {
	c, peer := newRawPair(t)
	f, err := c.Go("x", nil)
	if err != nil {
		t.Fatal(err)
	}
	peer.readJSON(t)
	_ = c.Close()
	if err := f.Result(nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Result = %v, want ErrClosed", err)
	}
	if _, err := c.Go("x", nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Go after Close = %v, want ErrClosed", err)
	}
}
