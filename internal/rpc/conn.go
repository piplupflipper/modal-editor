package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
)

// ErrClosed は、接続が終わっていることを表す。
// 接続が終わった後の [Conn.Call] と [Conn.Notify] は、この値を包んだ誤りを返す。
// 終わった理由（相手の切断、プロトコルの誤りなど）も一緒に包むので、errors.Is で両方を判定できる。
var ErrClosed = errors.New("rpc: connection closed")

// Replier は、相手からのリクエストに応答を返す関数である。
//
// err が nil なら result を JSON にして成功の応答を送る。
// err が nil でなければ失敗の応答を送る。err が [*Error] を包んでいればそのコードを使い、そうでなければ [CodeInternalError] にする。
// 1 つのリクエストに応答できるのは 1 回だけで、2 回目以降は何も送らずに誤りを返す。
// どの goroutine から呼んでもよい。
type Replier func(result any, err error) error

// Handler は、相手から届いたリクエストと通知を受け取る。
//
// どちらのメソッドも、接続の読み取りの goroutine から、届いた順に 1 件ずつ呼ばれる。
// 呼ばれている間は次のメッセージ（自分が送ったリクエストへの応答を含む）を読まないので、長く止まってはならない。
// 応答に時間のかかるリクエストは、reply を保持して戻り、後で別の goroutine から reply を呼ぶ。
// ctx は、接続が終わると取り消される。
type Handler interface {
	// HandleRequest は、相手からのリクエストを受け取る。応答は reply で返す。
	HandleRequest(ctx context.Context, req *Request, reply Replier)
	// HandleNotification は、相手からの通知を受け取る。相手からの [CancelMethod] もここに届く。
	HandleNotification(ctx context.Context, n *Notification)
}

// HandlerFuncs は、関数の組で [Handler] を作るための型である。
// nil の項目は、リクエストなら [CodeMethodNotFound] の誤りを応答し、通知なら捨てる。
type HandlerFuncs struct {
	Request      func(ctx context.Context, req *Request, reply Replier)
	Notification func(ctx context.Context, n *Notification)
}

// HandleRequest は、Request が nil でなければそれを呼ぶ。
func (h HandlerFuncs) HandleRequest(ctx context.Context, req *Request, reply Replier) {
	if h.Request == nil {
		_ = reply(nil, methodNotFound(req.Method))
		return
	}
	h.Request(ctx, req, reply)
}

// HandleNotification は、Notification が nil でなければそれを呼ぶ。
func (h HandlerFuncs) HandleNotification(ctx context.Context, n *Notification) {
	if h.Notification != nil {
		h.Notification(ctx, n)
	}
}

// Conn は、1 組の io.Reader と io.Writer の上の双方向の JSON-RPC 2.0 の接続である。
//
// こちらからのリクエストの送信と応答の対応付け（[Conn.Call]）、通知の送信（[Conn.Notify]）、
// 相手からのリクエストと通知の受け付け（[Handler]）を行う。
//
// メソッドは複数の goroutine から並行に呼んでよい。
// 受信と送信は、[NewConn] が起動する読み取りと書き込みの 2 つの goroutine が行う。
// 送信するメッセージは上限の無い列に積み、書き込みの goroutine が 1 件ずつ順に書く。
// 送信の関数（[Conn.Call]、[Conn.Notify]、[Replier]）は、相手が読まなくても書き込みの完了を待たない。
// これにより、双方がハンドラーの中で同時に応答を書いても、互いの読み取りが止まって詰まることが無い。
//
// 次のいずれかで接続は終わり、[Conn.Done] が閉じる。
//   - [Conn.Close] を呼んだ
//   - 相手が切断した（読み取りが EOF になった）
//   - JSON-RPC として解釈できないメッセージを受け取った（[ErrProtocol]。基本設計 5.2 の異常の条件）
//   - 読み書きが失敗した
//
// 終わった理由は [Conn.Err] で得る。接続が終わると、応答を待っている [Conn.Call] はすべて誤りを返す。
type Conn struct {
	reader  io.Reader
	writer  io.Writer
	br      *bufio.Reader
	handler Handler

	// mu は、nextID、pending、outbox、err を守る。
	mu      sync.Mutex
	nextID  int64
	pending map[ID]chan *Response
	outbox  [][]byte
	err     error

	// wake は、outbox に積んだことを書き込みの goroutine へ知らせる。
	wake   chan struct{}
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// NewConn は、r から読み w へ書く接続を作り、読み取りと書き込みの goroutine を起動する。
//
// h が nil なら、相手からのリクエストには [CodeMethodNotFound] を応答し、通知は捨てる。
// r と w が io.Closer を実装していれば、接続が終わるときに閉じる。
// 閉じることで、読み取りの goroutine が止まる（子プロセスのパイプや io.Pipe はそう振る舞う）。
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	if h == nil {
		h = HandlerFuncs{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		reader:  r,
		writer:  w,
		br:      bufio.NewReader(r),
		handler: h,
		pending: make(map[ID]chan *Response),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		ctx:     ctx,
		cancel:  cancel,
	}
	go c.readLoop()
	go c.writeLoop()
	return c
}

// Call は、相手へリクエストを送り、応答を待つ。
//
// 成功の応答なら、結果の JSON を result（ポインター）へ読み込む。result が nil なら結果を読み捨てる。
// 失敗の応答なら、その [*Error] を返す。
//
// ctx の期限を超えるか ctx が取り消されたら、応答を待つのをやめて ctx.Err() を包んだ誤りを返し、
// 相手へ [CancelMethod] の通知を送る。その後に届いた応答は捨てる。
//
// req: FR-17
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rpc: call %s: %w", method, err)
	}
	c.mu.Lock()
	if c.err != nil {
		cause := c.err
		c.mu.Unlock()
		return closedError(method, cause)
	}
	c.nextID++
	id := NumberID(c.nextID)
	ch := make(chan *Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	data, err := encodeRequest(id, method, params)
	if err != nil {
		c.forget(id)
		return err
	}
	if err := c.write(data); err != nil {
		c.forget(id)
		return fmt.Errorf("rpc: call %s: %w", method, err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("rpc: call %s: unmarshal result: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		// 応答の待ち受けを消してから取り消しを送る。以後に届いた同じ ID の応答は、待ち受けが無いので捨てられる。
		c.forget(id)
		callErr := fmt.Errorf("rpc: call %s (id %s): %w", method, id, ctx.Err())
		if err := c.Notify(CancelMethod, CancelParams{ID: id}); err != nil {
			return errors.Join(callErr, err)
		}
		return callErr
	case <-c.done:
		return closedError(method, c.Err())
	}
}

// Notify は、相手へ通知を送る。応答は待たない。
func (c *Conn) Notify(method string, params any) error {
	data, err := encodeNotification(method, params)
	if err != nil {
		return err
	}
	if err := c.write(data); err != nil {
		return fmt.Errorf("rpc: notify %s: %w", method, err)
	}
	return nil
}

// Close は、接続を終わらせる。r と w が io.Closer を実装していれば閉じ、その誤りを返す。
// まだ書いていないメッセージは捨てる。
// 2 回目以降の呼び出しと、既に終わった接続への呼び出しは何もせずに nil を返す。
// 終わった後の [Conn.Err] は [ErrClosed] を返す。
func (c *Conn) Close() error {
	return c.shutdown(ErrClosed)
}

// Done は、接続が終わると閉じるチャネルを返す。
func (c *Conn) Done() <-chan struct{} {
	return c.done
}

// Err は、接続が終わった理由を返す。接続が続いている間は nil を返す。
//
//   - [Conn.Close] による終了: [ErrClosed]
//   - 相手の切断: io.EOF を包んだ誤り（メッセージの途中で切れたなら io.ErrUnexpectedEOF）
//   - 解釈できないメッセージ: [ErrProtocol] を包んだ誤り
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// readLoop は、接続が終わるまでメッセージを読み、応答は待っている Call へ、リクエストと通知は Handler へ渡す。
func (c *Conn) readLoop() {
	for {
		data, err := ReadMessage(c.br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = fmt.Errorf("rpc: peer closed the connection: %w", err)
			} else if !errors.Is(err, ErrProtocol) {
				err = fmt.Errorf("rpc: read: %w", err)
			}
			_ = c.shutdown(err)
			return
		}
		msg, err := decodeMessage(data)
		if err != nil {
			_ = c.shutdown(err)
			return
		}
		switch m := msg.(type) {
		case *Response:
			c.deliver(m)
		case *Request:
			c.handler.HandleRequest(c.ctx, m, c.replier(m.ID))
		case *Notification:
			c.handler.HandleNotification(c.ctx, m)
		}
	}
}

// deliver は、応答を待っている Call へ渡す。待っている Call が無ければ（期限切れの後に届いた応答や、
// 送っていない ID への応答なら）捨てる。
//
// req: FR-17
func (c *Conn) deliver(resp *Response) {
	c.mu.Lock()
	ch, ok := c.pending[resp.ID]
	delete(c.pending, resp.ID)
	c.mu.Unlock()
	if ok {
		ch <- resp
	}
}

// replier は、id のリクエストに 1 回だけ応答する Replier を返す。
func (c *Conn) replier(id ID) Replier {
	var replied atomic.Bool
	return func(result any, err error) error {
		if !replied.CompareAndSwap(false, true) {
			return fmt.Errorf("rpc: request %s already replied", id)
		}
		var rpcErr *Error
		if err != nil && !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: CodeInternalError, Message: err.Error()}
		}
		data, encErr := encodeResponse(id, result, rpcErr)
		if encErr != nil {
			// 結果を JSON にできなかったことを相手に知らせ、相手の Call を待たせ続けない。
			data, _ = encodeResponse(id, nil, &Error{Code: CodeInternalError, Message: encErr.Error()})
			if werr := c.write(data); werr != nil {
				return errors.Join(encErr, werr)
			}
			return encErr
		}
		return c.write(data)
	}
}

// write は、1 件のメッセージを送信の列に積む。書き込みの完了は待たない。
// 接続が終わっていれば積まずに誤りを返す。
func (c *Conn) write(data []byte) error {
	c.mu.Lock()
	if c.err != nil {
		cause := c.err
		c.mu.Unlock()
		return closedError("write", cause)
	}
	c.outbox = append(c.outbox, data)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

// writeLoop は、接続が終わるまで、送信の列に積まれたメッセージを積んだ順に書く。
// 書き込みが失敗したら接続を終わらせる。
func (c *Conn) writeLoop() {
	for {
		select {
		case <-c.wake:
		case <-c.done:
			return
		}
		for {
			c.mu.Lock()
			batch := c.outbox
			c.outbox = nil
			c.mu.Unlock()
			if len(batch) == 0 {
				break
			}
			for _, data := range batch {
				if err := WriteMessage(c.writer, data); err != nil {
					_ = c.shutdown(fmt.Errorf("rpc: write: %w", err))
					return
				}
			}
		}
	}
}

// forget は、id の応答の待ち受けを消す。
func (c *Conn) forget(id ID) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// shutdown は、cause を理由に接続を終わらせる。既に終わっていれば何もせずに nil を返す。
// 終わらせたときは、r と w を閉じた誤りを返す。
func (c *Conn) shutdown(cause error) error {
	c.mu.Lock()
	first := c.err == nil
	if first {
		c.err = cause
		c.pending = make(map[ID]chan *Response)
		c.outbox = nil
	}
	c.mu.Unlock()
	if !first {
		return nil
	}

	c.cancel()
	close(c.done)

	var errs []error
	if rc, ok := c.reader.(io.Closer); ok {
		errs = append(errs, rc.Close())
	}
	// 読み書きに同じ値（net.Conn など）を渡された場合は 2 回閉じない。
	if wc, ok := c.writer.(io.Closer); ok && !sameValue(c.reader, c.writer) {
		errs = append(errs, wc.Close())
	}
	return errors.Join(errs...)
}

// sameValue は、a と b が同じ値かどうかを、比較できない型でも panic せずに判定する。
func sameValue(a, b any) bool {
	ta := reflect.TypeOf(a)
	if ta == nil || ta != reflect.TypeOf(b) || !ta.Comparable() {
		return false
	}
	return a == b
}

// closedError は、接続が終わった後の操作の誤りを作る。[ErrClosed] と終わった理由の両方を包む。
func closedError(op string, cause error) error {
	if errors.Is(cause, ErrClosed) {
		return fmt.Errorf("rpc: %s: %w", op, cause)
	}
	return fmt.Errorf("rpc: %s: %w: %w", op, ErrClosed, cause)
}

// methodNotFound は、メソッドが無いことを表す誤りを作る。
func methodNotFound(method string) *Error {
	return &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}
