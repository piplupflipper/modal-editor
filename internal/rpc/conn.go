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

// ErrOutboxFull は、送信の列が [WithMaxOutboxBytes] の上限を超えたことを表す。
// 上限を超えた接続は終わり、[Conn.Err] がこの値を包んだ誤りを返す。
var ErrOutboxFull = errors.New("rpc: outbox full")

// Replier は、相手からのリクエストに応答を返す関数である。
//
// err が nil なら result を JSON にして成功の応答を送る。
// err が nil でなければ失敗の応答を送る。err が [*Error] を包んでいればそのコードを使い、そうでなければ [CodeInternalError] にする。
// 1 つのリクエストに応答できるのは 1 回だけで、2 回目以降は何も送らずに誤りを返す。
// 応答すると、そのリクエストのハンドラーに渡した ctx は取り消される。
// どの goroutine から呼んでもよい。
type Replier func(result any, err error) error

// Handler は、相手から届いたリクエストと通知を受け取る。
//
// どちらのメソッドも、接続の読み取りの goroutine から、届いた順に 1 件ずつ呼ばれる。
// 呼ばれている間は次のメッセージ（自分が送ったリクエストへの応答を含む）を読まないので、長く止まってはならない。
// 応答に時間のかかるリクエストは、reply を保持して戻り、後で別の goroutine から reply を呼ぶ。
//
// ctx は、接続が終わると取り消される。HandleRequest の ctx は、さらに、応答したときと、
// 相手から取り消しの通知（[WithCancelMethod]、既定は [CancelMethod]）が届いたときにも取り消される。
// ハンドラーは ctx が取り消されたら速やかに戻らなければならない。[Conn.Close] は実行中のハンドラーが戻るのを待つからである。
//
// [Conn.Close] が戻った後に、ハンドラーが呼ばれることは無い。
// ハンドラーの中から [Conn.Close] と [Conn.Shutdown] を呼んではならない（自分が戻るのを待ってデッドロックする）。
// ハンドラーの中で接続を終わらせたいときは、別の goroutine から呼ぶ。
type Handler interface {
	// HandleRequest は、相手からのリクエストを受け取る。応答は reply で返す。
	HandleRequest(ctx context.Context, req *Request, reply Replier)
	// HandleNotification は、相手からの通知を受け取る。取り消しの通知は Conn が処理し、ここには届かない。
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

// Option は、[NewConn] に渡す接続の設定である。
type Option func(*options)

type options struct {
	cancelMethod   string
	maxMessageSize int
	maxOutboxBytes int
	onPanic        func()
}

// WithPanicHook は、接続の goroutine（読み取りと書き込み。読み取りの goroutine はハンドラーも呼ぶ）が panic したときに、
// その goroutine の上で f を呼んでから、同じ値で panic を続けさせる。panic を止めはしない。
// 本体が、どの goroutine の panic でも端末を元に戻してから終わるために使う（基本設計 8 章、#114）。
// f は、どの goroutine からも、何度呼ばれてもよいものにする。nil なら何もしない（既定）。
func WithPanicHook(f func()) Option {
	return func(o *options) { o.onPanic = f }
}

// recoverPanic は、goroutine の始めに defer で呼ぶ。panic していれば、WithPanicHook の f を呼んでから panic を続ける。
func (c *Conn) recoverPanic() {
	if c.opts.onPanic == nil {
		return
	}
	if p := recover(); p != nil {
		c.opts.onPanic()
		panic(p)
	}
}

// WithCancelMethod は、取り消しの通知のメソッド名を変える。既定は [CancelMethod] である。
// 期限を超えた [Conn.Call] が送る通知と、相手から受け取ってハンドラーの ctx を取り消す通知の両方に使う。
// 引数の形は [CancelParams] で、LSP の "$/cancelRequest" と同じである。
func WithCancelMethod(method string) Option {
	return func(o *options) { o.cancelMethod = method }
}

// WithMaxMessageSize は、受信する 1 件のメッセージの本文の上限のバイト数を変える。既定は [DefaultMaxMessageSize] である。
// 上限を超えるメッセージを受け取ると、接続は [ErrProtocol] で終わる。
func WithMaxMessageSize(n int) Option {
	return func(o *options) { o.maxMessageSize = n }
}

// WithMaxOutboxBytes は、送信の列に積んだまま書いていないバイト数の上限を決める。0 以下なら上限を置かない（既定）。
// 積むと上限を超える送信は、積まずに [ErrOutboxFull] を包んだ誤りを返し、接続を終わらせる。
// 相手が読まなくなったとき（拡張機能の無限ループやデッドロック）に、本体のメモリを使い切らないための上限である（NFR-03）。
func WithMaxOutboxBytes(n int) Option {
	return func(o *options) { o.maxOutboxBytes = n }
}

// Conn は、1 組の io.Reader と io.Writer の上の双方向の JSON-RPC 2.0 の接続である。
//
// こちらからのリクエストの送信と応答の対応付け（[Conn.Call]、[Conn.Go]）、通知の送信（[Conn.Notify]）、
// 相手からのリクエストと通知の受け付け（[Handler]）を行う。
//
// メソッドは複数の goroutine から並行に呼んでよい。
// 受信と送信は、[NewConn] が起動する読み取りと書き込みの 2 つの goroutine が行う。
// 送信するメッセージは列に積み、書き込みの goroutine が積んだ順に書く。
// 送信の関数（[Conn.Call]、[Conn.Notify]、[Replier]）は、相手が読まなくても書き込みの完了を待たない。
// これにより、双方がハンドラーの中で同時に応答を書いても、互いの読み取りが止まって詰まることが無い。
// 列の量は [Conn.OutboxBytes] で見られ、上限は [WithMaxOutboxBytes] で決められる。
//
// 次のいずれかで接続は終わり、[Conn.Done] が閉じる。
//   - [Conn.Close] か [Conn.Shutdown] を呼んだ
//   - 相手が切断した（読み取りが EOF になった）
//   - JSON-RPC として解釈できないメッセージを受け取った（[ErrProtocol]。基本設計 5.2 の異常の条件）
//   - 読み書きが失敗した
//   - 送信の列が上限を超えた（[ErrOutboxFull]）
//
// 終わった理由は [Conn.Err] で得る。接続が終わると、応答を待っている [Conn.Call] はすべて誤りを返す。
//
// 2 つの goroutine は、接続が終わるときに r と w を閉じることで、止まっている Read と Write から抜ける。
// io.Closer でない r と w や、閉じても止まっている Read と Write が戻らない r と w を渡すと、
// 接続が終わった後も、Read と Write が戻るまで goroutine が残る。
// [Conn.Close] は goroutine の終了を待たない（待つのは実行中のハンドラーだけである）。
// 子プロセスのパイプ（os/exec）と io.Pipe は、閉じると Read と Write が戻る。
// ただし、子プロセスを kill しても、パイプを継承した孫プロセスが残っていると EOF にならないので、kill の後に [Conn.Close] を呼ぶ。
type Conn struct {
	reader  io.Reader
	writer  io.Writer
	br      *bufio.Reader
	handler Handler
	opts    options

	// mu は、ここから err までのフィールドを守る。
	mu          sync.Mutex
	nextID      int64
	pending     map[ID]*Future
	incoming    map[ID]context.CancelFunc
	outbox      [][]byte
	outboxBytes int
	writing     bool
	idle        []chan struct{}
	err         error

	// dispatchMu は、ハンドラーの実行中に保持する。Close はこれを取って、実行中のハンドラーが戻るのを待つ。
	dispatchMu sync.Mutex

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
func NewConn(r io.Reader, w io.Writer, h Handler, opts ...Option) *Conn {
	if h == nil {
		h = HandlerFuncs{}
	}
	o := options{cancelMethod: CancelMethod, maxMessageSize: DefaultMaxMessageSize}
	for _, opt := range opts {
		opt(&o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		reader:   r,
		writer:   w,
		br:       bufio.NewReader(r),
		handler:  h,
		opts:     o,
		pending:  make(map[ID]*Future),
		incoming: make(map[ID]context.CancelFunc),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
	}
	go c.readLoop()
	go c.writeLoop()
	return c
}

// Future は、[Conn.Go] で送ったリクエストの応答を待つための値である。
//
// 応答が届くか、接続が終わるか、[Future.Cancel] を呼ぶと、[Future.Ready] が閉じる。
// その後に [Future.Result] で結果を得る。
type Future struct {
	id     ID
	method string
	c      *Conn
	ready  chan struct{}
	// resp と err は、ready を閉じる前に 1 回だけ書く。
	resp *Response
	err  error
}

// ID は、送ったリクエストの ID を返す。
func (f *Future) ID() ID { return f.id }

// Ready は、結果が決まると閉じるチャネルを返す。
func (f *Future) Ready() <-chan struct{} { return f.ready }

// Result は、結果が決まるまで待ち、結果を返す。
//
// 成功の応答なら、結果の JSON を result（ポインター）へ読み込む。result が nil なら結果を読み捨てる。
// 失敗の応答なら、その [*Error] を返す。
// 接続が終わったなら [ErrClosed] を包んだ誤りを、[Future.Cancel] したなら context.Canceled を包んだ誤りを返す。
func (f *Future) Result(result any) error {
	<-f.ready
	if f.err != nil {
		return f.err
	}
	if f.resp.Error != nil {
		return f.resp.Error
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(f.resp.Result, result); err != nil {
		return fmt.Errorf("rpc: call %s: unmarshal result: %w", f.method, err)
	}
	return nil
}

// Cancel は、応答を待つのをやめる。
// まだ結果が決まっていなければ、context.Canceled を結果にして [Future.Ready] を閉じ、相手へ取り消しの通知を送る。
// その後に届いた応答は捨てる。結果が既に決まっていれば何もせずに nil を返す。
//
// req: FR-17
func (f *Future) Cancel() error {
	return f.cancelWith(context.Canceled)
}

// cancelWith は、cause を理由に待つのをやめる。
func (f *Future) cancelWith(cause error) error {
	callErr := fmt.Errorf("rpc: call %s (id %s): %w", f.method, f.id, cause)
	if !f.c.settle(f.id, nil, callErr) {
		return nil
	}
	return f.c.Notify(f.c.opts.cancelMethod, CancelParams{ID: f.id})
}

// Go は、相手へリクエストを送り、応答を待たずに [Future] を返す。
//
// 期限を 0 にした問い合わせ（FR-18）のように、次の処理へ進みながら、届いた時点で結果を照合するときに使う。
// 結果が要らなくなったら [Future.Cancel] を呼ぶ。呼ばないと、応答が届くか接続が終わるまで待ち受けが残る。
func (c *Conn) Go(method string, params any) (*Future, error) {
	c.mu.Lock()
	if c.err != nil {
		cause := c.err
		c.mu.Unlock()
		return nil, closedError("call "+method, cause)
	}
	c.nextID++
	f := &Future{id: NumberID(c.nextID), method: method, c: c, ready: make(chan struct{})}
	c.pending[f.id] = f
	c.mu.Unlock()

	data, err := encodeRequest(f.id, method, params)
	if err == nil {
		err = c.write(data)
	}
	if err != nil {
		c.settle(f.id, nil, err)
		return nil, fmt.Errorf("rpc: call %s: %w", method, err)
	}
	return f, nil
}

// Call は、相手へリクエストを送り、応答を待つ。結果の扱いは [Future.Result] と同じである。
//
// ctx の期限を超えるか ctx が取り消されたら、応答を待つのをやめて ctx.Err() を包んだ誤りを返し、
// 相手へ取り消しの通知を送る。その後に届いた応答は捨てる。
//
// req: FR-17
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("rpc: call %s: %w", method, err)
	}
	f, err := c.Go(method, params)
	if err != nil {
		return err
	}
	select {
	case <-f.ready:
	case <-ctx.Done():
		if err := f.cancelWith(ctx.Err()); err != nil {
			<-f.ready
			return errors.Join(f.err, err)
		}
	}
	return f.Result(result)
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

// OutboxBytes は、送信の列に積んだまま、まだ書き終えていないバイト数を返す。
// 相手が読んでいるかどうかを外から見るのに使う。
func (c *Conn) OutboxBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.outboxBytes
}

// Flush は、呼んだ時点までに積んだメッセージを含め、送信の列が空になって書き込みが終わるまで待つ。
// ctx が終わるか接続が終わったら、待つのをやめて誤りを返す。
func (c *Conn) Flush(ctx context.Context) error {
	c.mu.Lock()
	if c.err != nil {
		cause := c.err
		c.mu.Unlock()
		return closedError("flush", cause)
	}
	if len(c.outbox) == 0 && !c.writing {
		c.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	c.idle = append(c.idle, ch)
	c.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("rpc: flush: %w", ctx.Err())
	case <-c.done:
		return closedError("flush", c.Err())
	}
}

// Shutdown は、送信の列を書き切ってから接続を終わらせる。
// LSP の exit 通知のように、最後のメッセージを相手に届けてから閉じるときに使う。
// ctx が終わったら、書き残しを捨てて接続を終わらせる。返す誤りは、書き切れなかった誤りと [Conn.Close] の誤りである。
func (c *Conn) Shutdown(ctx context.Context) error {
	flushErr := c.Flush(ctx)
	if errors.Is(flushErr, ErrClosed) {
		flushErr = nil
	}
	return errors.Join(flushErr, c.Close())
}

// Close は、接続を終わらせる。まだ書いていないメッセージは捨てる。
// r と w が io.Closer を実装していれば閉じ、その誤りを返す。
//
// ハンドラーの ctx を取り消した後、実行中のハンドラーが戻るのを待ってから戻る。
// Close が戻った後に、ハンドラーが呼ばれることは無い。
// 2 回目以降の呼び出しと、既に終わった接続への呼び出しは、ハンドラーを待つだけで nil を返す。
// 終わった後の [Conn.Err] は [ErrClosed] を返す。
func (c *Conn) Close() error {
	err := c.shutdown(ErrClosed)
	c.dispatchMu.Lock()
	//nolint:staticcheck // 空の臨界区間は、実行中のハンドラーが戻るのを待つためのものである。
	c.dispatchMu.Unlock()
	return err
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
//   - 送信の列の上限の超過: [ErrOutboxFull] を包んだ誤り
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// readLoop は、接続が終わるまでメッセージを読み、応答は待っている Future へ、リクエストと通知は Handler へ渡す。
func (c *Conn) readLoop() {
	defer c.recoverPanic()
	for {
		data, err := ReadMessage(c.br, c.opts.maxMessageSize)
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
		if !c.dispatch(msg) {
			return
		}
	}
}

// dispatch は、1 件のメッセージを処理する。接続が終わっていれば何もせずに false を返す。
//
// 接続の終了の判定とハンドラーの呼び出しを dispatchMu の中で行うので、Close が dispatchMu を取った後には、
// ハンドラーは呼ばれない。bufio に読み込み済みのメッセージが残っていても同じである。
func (c *Conn) dispatch(msg any) bool {
	c.dispatchMu.Lock()
	defer c.dispatchMu.Unlock()
	if c.ctx.Err() != nil {
		return false
	}
	switch m := msg.(type) {
	case *Response:
		c.settle(m.ID, m, nil)
	case *Request:
		ctx, ok := c.startIncoming(m.ID)
		if !ok {
			// 応答していない同じ ID のリクエストが来たのは相手の誤りなので、接続を終わらせる。
			_ = c.shutdown(fmt.Errorf("%w: duplicate request id %s", ErrProtocol, m.ID))
			return false
		}
		c.handler.HandleRequest(ctx, m, c.replier(m.ID))
	case *Notification:
		if m.Method == c.opts.cancelMethod {
			c.cancelIncoming(m.Params)
			return true
		}
		c.handler.HandleNotification(c.ctx, m)
	}
	return true
}

// settle は、id の Future の結果を決めて Ready を閉じる。
// resp が nil でなければ応答を、そうでなければ err を結果にする。
// 待ち受けが無ければ（期限切れや取り消しの後に届いた応答や、送っていない ID への応答なら）何もせずに false を返す。
//
// req: FR-17
func (c *Conn) settle(id ID, resp *Response, err error) bool {
	c.mu.Lock()
	f, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if !ok {
		return false
	}
	f.resp, f.err = resp, err
	close(f.ready)
	return true
}

// startIncoming は、相手からのリクエストのハンドラーに渡す ctx を作る。
// 同じ ID のリクエストに、まだ応答していなければ false を返す。
func (c *Conn) startIncoming(id ID) (context.Context, bool) {
	ctx, cancel := context.WithCancel(c.ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.incoming[id]; dup {
		cancel()
		return nil, false
	}
	c.incoming[id] = cancel
	return ctx, true
}

// finishIncoming は、相手からのリクエストの ctx を取り消して、表から消す。
func (c *Conn) finishIncoming(id ID) {
	c.mu.Lock()
	cancel, ok := c.incoming[id]
	delete(c.incoming, id)
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

// cancelIncoming は、相手からの取り消しの通知を受け、対象のリクエストの ctx を取り消す。
// 引数を解釈できないか、対象が無ければ（応答済みなら）何もしない。
func (c *Conn) cancelIncoming(params json.RawMessage) {
	var p CancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	c.mu.Lock()
	cancel, ok := c.incoming[p.ID]
	c.mu.Unlock()
	if ok {
		cancel()
	}
}

// replier は、id のリクエストに 1 回だけ応答する Replier を返す。
func (c *Conn) replier(id ID) Replier {
	var replied atomic.Bool
	return func(result any, err error) error {
		if !replied.CompareAndSwap(false, true) {
			return fmt.Errorf("rpc: request %s already replied", id)
		}
		c.finishIncoming(id)
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
// 接続が終わっていれば積まずに誤りを返す。積むと上限を超えるなら、積まずに接続を終わらせる。
func (c *Conn) write(data []byte) error {
	c.mu.Lock()
	if c.err != nil {
		cause := c.err
		c.mu.Unlock()
		return closedError("write", cause)
	}
	if limit := c.opts.maxOutboxBytes; limit > 0 && c.outboxBytes+len(data) > limit {
		err := fmt.Errorf("%w: %d bytes queued, limit %d", ErrOutboxFull, c.outboxBytes+len(data), limit)
		c.mu.Unlock()
		_ = c.shutdown(err)
		return closedError("write", c.Err())
	}
	c.outbox = append(c.outbox, data)
	c.outboxBytes += len(data)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

// writeLoop は、接続が終わるまで、送信の列に積まれたメッセージを積んだ順に書く。
// 書き込みが失敗したら接続を終わらせる。列が空になったら、Flush で待っている呼び出しに知らせる。
func (c *Conn) writeLoop() {
	defer c.recoverPanic()
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
			if len(batch) == 0 {
				c.writing = false
				for _, ch := range c.idle {
					close(ch)
				}
				c.idle = nil
				c.mu.Unlock()
				break
			}
			c.writing = true
			c.mu.Unlock()
			for _, data := range batch {
				if err := WriteMessage(c.writer, data); err != nil {
					_ = c.shutdown(fmt.Errorf("rpc: write: %w", err))
					return
				}
				c.mu.Lock()
				c.outboxBytes -= len(data)
				c.mu.Unlock()
			}
		}
	}
}

// shutdown は、cause を理由に接続を終わらせる。既に終わっていれば何もせずに nil を返す。
// 終わらせたときは、待っている Future をすべて誤りで終え、r と w を閉じた誤りを返す。
// 実行中のハンドラーは待たない（readLoop の中からも呼ばれるため）。
func (c *Conn) shutdown(cause error) error {
	c.mu.Lock()
	first := c.err == nil
	var pending map[ID]*Future
	if first {
		c.err = cause
		pending = c.pending
		c.pending = make(map[ID]*Future)
		c.incoming = make(map[ID]context.CancelFunc)
		c.outbox = nil
		c.outboxBytes = 0
	}
	c.mu.Unlock()
	if !first {
		return nil
	}

	c.cancel()
	close(c.done)
	for _, f := range pending {
		f.err = closedError("call "+f.method, cause)
		close(f.ready)
	}

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
