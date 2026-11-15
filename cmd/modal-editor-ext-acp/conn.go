package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// JSON-RPC 2.0 のエラーコード。
const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// errConnClosed は、接続が終わった後にメッセージを送ろうとしたか、応答を待つ間に接続が終わったことを表す。
var errConnClosed = errors.New("acp: connection closed")

// rpcError は、JSON-RPC 2.0 のエラーの応答である。
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("acp: %s (code %d)", e.Message, e.Code) }

// wireMessage は、送受信する 1 件のメッセージである。リクエスト、通知、応答のどれにも使う。
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// requestHandler は、相手からのリクエストに答える関数である。
// 返した result を JSON にして応答する。誤りが *rpcError ならそのまま、それ以外は codeInternalError で応答する。
type requestHandler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// notificationHandler は、相手からの通知を受け取る関数である。
type notificationHandler func(method string, params json.RawMessage)

// lineConn は、改行で区切った JSON-RPC 2.0 の接続である（ACP の Transports の stdio）。
//
// ACP の標準入出力の接続は、1 件のメッセージを 1 行にし、改行（\n）で区切る。メッセージの中に改行を含めない。
// 本体との接続（internal/rpc）は Content-Length の形式なので、その部品はそのまま使えない（基本設計 6.5）。
// encoding/json は文字列の中の改行を \n に書くので、Marshal した 1 件はそのまま 1 行になる。
//
// 読み取りは serve の goroutine が行う。
// 通知は、届いた順に読み取りの goroutine の上で onNotification を呼ぶ（session/update の順序を保つため）。
// リクエストは、1 件ごとに別の goroutine で onRequest を呼ぶ。権限の確認のように利用者の応答を待つリクエストがあっても、
// 次のメッセージ（session/update や session/prompt の応答）の読み取りを止めないためである。
type lineConn struct {
	w   io.Writer
	wmu sync.Mutex

	onRequest      requestHandler
	onNotification notificationHandler

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan *wireMessage
	err     error
	// reqCtx は、相手からのリクエストのハンドラーに渡す ctx である。接続が終わると取り消す。
	reqCtx    context.Context
	reqCancel context.CancelFunc
	done      chan struct{}
}

func newLineConn(w io.Writer, onRequest requestHandler, onNotification notificationHandler) *lineConn {
	ctx, cancel := context.WithCancel(context.Background())
	return &lineConn{
		w:              w,
		onRequest:      onRequest,
		onNotification: onNotification,
		pending:        make(map[string]chan *wireMessage),
		reqCtx:         ctx,
		reqCancel:      cancel,
		done:           make(chan struct{}),
	}
}

// serve は、r からメッセージを読み、r が終わるまで処理する。戻る前に、応答を待っている呼び出しを errConnClosed で終わらせる。
// 戻り値は、読み取りが終わった理由である（相手が出力を閉じたときは io.EOF）。
func (c *lineConn) serve(r io.Reader) error {
	br := bufio.NewReader(r)
	var err error
	for {
		var line []byte
		line, err = br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			break
		}
	}
	c.close(err)
	return err
}

// close は、接続を終わったものにする。2 回目からは何もしない。
func (c *lineConn) close(cause error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	if cause == nil {
		cause = errConnClosed
	}
	c.err = cause
	pending := c.pending
	c.pending = map[string]chan *wireMessage{}
	c.mu.Unlock()
	c.reqCancel()
	for _, ch := range pending {
		close(ch)
	}
	close(c.done)
}

func (c *lineConn) dispatch(line []byte) {
	var m wireMessage
	if err := json.Unmarshal(line, &m); err != nil {
		// 解釈できない行は捨てる。id が分からないので応答もできない。
		return
	}
	switch {
	case m.Method != "" && len(m.ID) > 0:
		go c.handleRequest(&m)
	case m.Method != "":
		if c.onNotification != nil {
			c.onNotification(m.Method, m.Params)
		}
	case len(m.ID) > 0:
		c.mu.Lock()
		key := string(m.ID)
		ch, ok := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ok {
			ch <- &m
		}
	}
}

func (c *lineConn) handleRequest(m *wireMessage) {
	var (
		res any
		err error
	)
	if c.onRequest == nil {
		err = &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
	} else {
		res, err = c.onRequest(c.reqCtx, m.Method, m.Params)
	}
	out := wireMessage{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		var re *rpcError
		if !errors.As(err, &re) {
			re = &rpcError{Code: codeInternalError, Message: err.Error()}
		}
		out.Error = re
	} else {
		raw, merr := json.Marshal(res)
		if merr != nil {
			out.Error = &rpcError{Code: codeInternalError, Message: merr.Error()}
		} else {
			out.Result = raw
		}
	}
	_ = c.write(&out)
}

// call は、リクエストを送り、応答の result を result へ読み込む。
// 相手がエラーで応答したら *rpcError を、応答の前に接続が終わったら errConnClosed を返す。
// ctx が終わったら、応答を待つのをやめて ctx.Err() を返す（ACP の取り消しは session/cancel で行うので、ここでは何も送らない）。
func (c *lineConn) call(ctx context.Context, method string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan *wireMessage, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return errConnClosed
	}
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	c.pending[string(id)] = ch
	c.mu.Unlock()
	if err := c.write(&wireMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return errConnClosed
		}
		if m.Error != nil {
			return m.Error
		}
		if result == nil || len(m.Result) == 0 {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *lineConn) forget(id json.RawMessage) {
	c.mu.Lock()
	delete(c.pending, string(id))
	c.mu.Unlock()
}

// notify は、通知を送る。
func (c *lineConn) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.write(&wireMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

// write は、1 件のメッセージを 1 行にして書く。
func (c *lineConn) write(m *wireMessage) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.mu.Lock()
	closed := c.err != nil
	c.mu.Unlock()
	if closed {
		return errConnClosed
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(data)
	return err
}
