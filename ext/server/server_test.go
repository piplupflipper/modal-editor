package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

const timeout = 5 * time.Second

// host は、テストで本体の役をする接続である。拡張機能から届いた通知を notes に積む。
type host struct {
	conn  *rpc.Conn
	notes chan *rpc.Notification
	// edits は、拡張機能から届いた buffer/edit の params である。
	edits chan ext.BufferEditParams
	// served は、Serve が返した誤りを受け取る。
	served chan error
	log    *syncWriter
}

// start は、s を io.Pipe の上で動かし、本体の役の rpc.Conn をつなぐ。
func start(t *testing.T, s *server.Server) *host {
	t.Helper()
	return startWith(t, s, nil)
}

// startWith は、start と同じだが、buffer/edit 以外のリクエストを handle に渡す。
// handle が nil か false を返したら、CodeMethodNotFound で応答する。
func startWith(t *testing.T, s *server.Server, handle func(req *rpc.Request, reply rpc.Replier) bool) *host {
	t.Helper()
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	h := &host{
		notes:  make(chan *rpc.Notification, 16),
		edits:  make(chan ext.BufferEditParams, 4),
		served: make(chan error, 1),
		log:    &syncWriter{b: new(bytes.Buffer)},
	}
	s.ErrorLog = log.New(h.log, "", 0)
	h.conn = rpc.NewConn(hostR, hostW, rpc.HandlerFuncs{
		Request: func(_ context.Context, req *rpc.Request, reply rpc.Replier) {
			if req.Method != ext.MethodBufferEdit {
				if handle != nil && handle(req, reply) {
					return
				}
				_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: req.Method})
				return
			}
			var p ext.BufferEditParams
			_ = json.Unmarshal(req.Params, &p)
			h.edits <- p
			_ = reply(ext.BufferEditResult{Applied: true, Version: p.Transaction.BaseVersion + 1}, nil)
		},
		Notification: func(_ context.Context, n *rpc.Notification) { h.notes <- n },
	})
	go func() { h.served <- s.Serve(context.Background(), extR, extW) }()
	t.Cleanup(func() {
		_ = h.conn.Close()
		select {
		case err := <-h.served:
			if err != nil {
				t.Errorf("Serve = %v, want nil", err)
			}
		case <-time.After(timeout):
			t.Error("Serve did not return after the host closed")
		}
	})
	return h
}

func (h *host) call(t *testing.T, method string, params, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return h.conn.Call(ctx, method, params, result)
}

func (h *host) notify(t *testing.T, method string, params any) {
	t.Helper()
	if err := h.conn.Notify(method, params); err != nil {
		t.Fatalf("notify %s: %v", method, err)
	}
}

// ping は、拡張機能がそれより前のメッセージを処理し終えるのを待つ（メッセージは届いた順に処理される）。
func (h *host) ping(t *testing.T) {
	t.Helper()
	if err := h.call(t, ext.MethodPing, ext.PingParams{}, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func (h *host) nextNote(t *testing.T) *rpc.Notification {
	t.Helper()
	select {
	case n := <-h.notes:
		return n
	case <-time.After(timeout):
		t.Fatal("no notification from the extension")
		return nil
	}
}

func (h *host) noNote(t *testing.T) {
	t.Helper()
	h.ping(t)
	select {
	case n := <-h.notes:
		t.Fatalf("unexpected notification %s %s", n.Method, n.Params)
	default:
	}
}

// syncWriter は、テストの ErrorLog を Serve と並行に読むための、排他つきの書き込み先である。
type syncWriter struct {
	mu sync.Mutex
	b  *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func textObject(h *host, t *testing.T, v ext.Version, cursor ext.Position) (ext.TextObjectResolveResult, error) {
	t.Helper()
	var res ext.TextObjectResolveResult
	err := h.call(t, ext.MethodTextObjectResolve, ext.TextObjectResolveParams{
		Buffer: 1, Version: v, ID: "example.innerLine", Cursor: cursor,
	}, &res)
	return res, err
}

func newExample() *server.Server {
	s := server.New()
	s.TextObject("example.innerLine", innerLine)
	return s
}

func TestInitialize(t *testing.T) {
	s := server.New()
	var got json.RawMessage
	s.OnInitialize(func(_ context.Context, e *server.InitializeEvent) error {
		got = e.Settings
		return nil
	})
	h := start(t, s)
	var res ext.InitializeResult
	err := h.call(t, ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion, Settings: json.RawMessage(`{"a":1}`)}, &res)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.APIVersion != ext.APIVersion {
		t.Errorf("APIVersion = %q, want %q", res.APIVersion, ext.APIVersion)
	}
	h.ping(t)
	if string(got) != `{"a":1}` {
		t.Errorf("settings = %s", got)
	}
}

func TestInitializeHookError(t *testing.T) {
	s := server.New()
	s.OnInitialize(func(context.Context, *server.InitializeEvent) error { return errors.New("bad settings") })
	h := start(t, s)
	err := h.call(t, ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion}, nil)
	var rerr *rpc.Error
	if !errors.As(err, &rerr) || !strings.Contains(rerr.Message, "bad settings") {
		t.Errorf("initialize = %v, want an error with the hook's message", err)
	}
}

// 全文を受け取り、逐次の数え方の差分を適用した複製に対して、テキストオブジェクトの問い合わせに答える。
//
// req: FR-11, FR-16
func TestFullTextAndDiff(t *testing.T) {
	s := newExample()
	var seen []string
	s.OnBufferOpen(func(_ context.Context, e *server.BufferOpenEvent) {
		seen = append(seen, fmt.Sprintf("open(reopen=%t) %q", e.Reopen, e.Buffer.Text()))
	})
	s.OnBufferChange(func(_ context.Context, e *server.BufferChangeEvent) {
		seen = append(seen, fmt.Sprintf("change %d->%d edits=%d %q", e.FromVersion, e.ToVersion, len(e.Edits), e.Buffer.Text()))
	})
	s.OnBufferClose(func(_ context.Context, e *server.BufferCloseEvent) {
		seen = append(seen, fmt.Sprintf("close %d", e.Buffer))
	})
	h := start(t, s)

	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 7, Text: "first\n  second"})
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 7, ToVersion: 8,
		Edits: []ext.Edit{
			{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 0}, End: ext.Position{Line: 1, Byte: 0}}}, // 1 行目を消す
			{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 8}, End: ext.Position{Line: 0, Byte: 8}}, Text: " 行"},
		},
	})
	res, err := textObject(h, t, 8, ext.Position{Line: 0, Byte: 0})
	if err != nil {
		t.Fatalf("textObject/resolve: %v", err)
	}
	want := ext.TextObjectResolveResult{
		OK: true, Version: 8,
		Range: ext.Range{Start: ext.Position{Line: 0, Byte: 2}, End: ext.Position{Line: 0, Byte: 12}},
	}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	// 同じバッファの全文を受け取り直したら Reopen になる。開いていないバッファの close ではフックを呼ばない。
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 9, Text: "z"})
	h.notify(t, ext.MethodBufferClose, ext.BufferCloseParams{Buffer: 1})
	h.notify(t, ext.MethodBufferClose, ext.BufferCloseParams{Buffer: 2})
	h.noNote(t)
	want2 := []string{
		`open(reopen=false) "first\n  second"`,
		`change 7->8 edits=2 "  second 行"`,
		`open(reopen=true) "z"`,
		`close 1`,
	}
	if strings.Join(seen, "|") != strings.Join(want2, "|") {
		t.Errorf("hooks saw\n%s\nwant\n%s", strings.Join(seen, "\n"), strings.Join(want2, "\n"))
	}
}

// buffer/change の FromVersion が合わなければ buffer/resync を 1 回だけ送り、buffer/open まで問い合わせに失敗を応答する。
//
// req: FR-16
func TestResyncOnMissingChange(t *testing.T) {
	h := start(t, newExample())
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: "a"})
	// 2 → 3 の差分が欠けて、3 → 4 が届いた。
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 3, ToVersion: 4,
		Edits: []ext.Edit{{Text: "x"}},
	})
	n := h.nextNote(t)
	var p ext.BufferResyncParams
	if err := json.Unmarshal(n.Params, &p); err != nil || n.Method != ext.MethodBufferResync {
		t.Fatalf("got %s %s, want buffer/resync", n.Method, n.Params)
	}
	if p != (ext.BufferResyncParams{Buffer: 1, Version: 1}) {
		t.Errorf("resync params = %+v", p)
	}

	// 送り直しを待つ間の差分は捨て、resync を重ねて送らない。
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 4, ToVersion: 5,
		Edits: []ext.Edit{{Text: "y"}},
	})
	if _, err := textObject(h, t, 5, ext.Position{}); err == nil {
		t.Error("query while waiting for resync succeeded")
	}
	h.noNote(t)

	// 本体が全文を送り直したら、問い合わせに答える。
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 5, Text: "yx a"})
	res, err := textObject(h, t, 5, ext.Position{})
	if err != nil || !res.OK || res.Range.End.Byte != 4 {
		t.Errorf("after resync: %+v, %v", res, err)
	}
}

// 差分の位置が複製の外を指す場合も、差分が欠けたと判断して buffer/resync を送る。
//
// req: FR-16
func TestResyncOnInvalidEdit(t *testing.T) {
	h := start(t, newExample())
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: "あ"})
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 1, ToVersion: 2,
		Edits: []ext.Edit{{Range: ext.Range{Start: ext.Position{Byte: 1}, End: ext.Position{Byte: 1}}, Text: "x"}},
	})
	if n := h.nextNote(t); n.Method != ext.MethodBufferResync {
		t.Fatalf("got %s, want buffer/resync", n.Method)
	}
	// 開いていないバッファの差分も同じである。
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{Buffer: 9, FromVersion: 0, ToVersion: 1})
	if n := h.nextNote(t); n.Method != ext.MethodBufferResync || !strings.Contains(string(n.Params), `"buffer":9`) {
		t.Fatalf("got %s %s, want buffer/resync for buffer 9", n.Method, n.Params)
	}
}

func TestQueryFailures(t *testing.T) {
	h := start(t, newExample())
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 2, Text: "abc"})
	tests := []struct {
		name string
		id   string
		v    ext.Version
		buf  ext.BufferID
	}{
		{"未登録の ID", "nope", 2, 1},
		{"バージョン番号の不一致", "example.innerLine", 3, 1},
		{"開いていないバッファ", "example.innerLine", 2, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.call(t, ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: tt.buf, Version: tt.v, ID: tt.id}, nil)
			var rerr *rpc.Error
			if !errors.As(err, &rerr) {
				t.Errorf("err = %v, want *rpc.Error", err)
			}
		})
	}
	err := h.call(t, "unknown/method", nil, nil)
	var rerr *rpc.Error
	if !errors.As(err, &rerr) || rerr.Code != rpc.CodeMethodNotFound {
		t.Errorf("unknown method: %v", err)
	}
	// buffer/close の後は、そのバッファへの問い合わせに失敗する。
	h.notify(t, ext.MethodBufferClose, ext.BufferCloseParams{Buffer: 1})
	if _, err := textObject(h, t, 2, ext.Position{}); err == nil {
		t.Error("query after buffer/close succeeded")
	}
}

// ハンドラーの panic は、そのリクエストのエラー応答になり、拡張機能は次のリクエストに答え続ける。
func TestHandlerPanic(t *testing.T) {
	s := server.New()
	s.Motion("boom", func(context.Context, *server.Buffer, *ext.MotionResolveParams) (ext.MotionResolveResult, error) {
		panic("boom")
	})
	s.OnBufferOpen(func(context.Context, *server.BufferOpenEvent) { panic("hook boom") })
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Text: "x"})
	err := h.call(t, ext.MethodMotionResolve, ext.MotionResolveParams{Buffer: 1, ID: "boom"}, nil)
	var rerr *rpc.Error
	if !errors.As(err, &rerr) || rerr.Code != rpc.CodeInternalError || !strings.Contains(rerr.Message, "boom") {
		t.Errorf("motion/resolve = %v, want internal error", err)
	}
	h.ping(t)
	out := h.log.String()
	if !strings.Contains(out, "panic in buffer open hook: hook boom") || !strings.Contains(out, "panic in motion/resolve boom: boom") {
		t.Errorf("ErrorLog = %q", out)
	}
}

// $/cancel は、実行中のハンドラーの ctx を取り消し、まだ処理していないリクエストのハンドラーを呼ばない。
func TestCancel(t *testing.T) {
	s := server.New()
	started := make(chan struct{})
	called := make(chan string, 2)
	s.TextObject("slow", func(ctx context.Context, _ *server.Buffer, _ *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		called <- "slow"
		close(started)
		<-ctx.Done()
		return ext.TextObjectResolveResult{}, ctx.Err()
	})
	s.TextObject("queued", func(context.Context, *server.Buffer, *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		called <- "queued"
		return ext.TextObjectResolveResult{OK: true}, nil
	})
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Text: "x"})

	slow, err := h.conn.Go(ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: 1, ID: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	queued, err := h.conn.Go(ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: 1, ID: "queued"})
	if err != nil {
		t.Fatal(err)
	}
	// 送る順は queued の取り消し、slow の取り消しである。拡張機能は届いた順に読むので、
	// slow のハンドラーが戻った時点で、queued は取り消し済みである。
	if err := queued.Cancel(); err != nil {
		t.Fatal(err)
	}
	if err := slow.Cancel(); err != nil {
		t.Fatal(err)
	}
	h.ping(t)
	close(called)
	var got []string
	for c := range called {
		got = append(got, c)
	}
	if strings.Join(got, ",") != "slow" {
		t.Errorf("handlers called: %v, want only slow", got)
	}
}

// オペレーターとコマンドの結果のトランザクションには、問い合わせのバージョン番号が入る。
func TestOperatorAndCommand(t *testing.T) {
	s := server.New()
	upper := func(b *server.Buffer, r ext.Range) ([]ext.Edit, error) {
		text, err := b.Slice(r)
		return []ext.Edit{{Range: r, Text: strings.ToUpper(text)}}, err
	}
	s.Operator("upper", func(_ context.Context, b *server.Buffer, p *ext.OperatorApplyParams) (ext.OperatorApplyResult, error) {
		edits, err := upper(b, p.Range)
		return ext.OperatorApplyResult{OK: true, Transaction: ext.Transaction{Edits: edits}}, err
	})
	s.Command("upperLine", func(_ context.Context, b *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		l := p.Cursor.Line
		edits, err := upper(b, ext.Range{Start: ext.Position{Line: l}, End: ext.Position{Line: l, Byte: len(b.Line(l))}})
		return ext.CommandExecuteResult{OK: true, Transaction: &ext.Transaction{Edits: edits}}, err
	})
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 4, Text: "abc\ndef"})

	var op ext.OperatorApplyResult
	r := ext.Range{Start: ext.Position{Byte: 1}, End: ext.Position{Byte: 3}}
	if err := h.call(t, ext.MethodOperatorApply, ext.OperatorApplyParams{Buffer: 1, Version: 4, ID: "upper", Range: r}, &op); err != nil {
		t.Fatal(err)
	}
	if !op.OK || op.Transaction.BaseVersion != 4 || len(op.Transaction.Edits) != 1 || op.Transaction.Edits[0].Text != "BC" {
		t.Errorf("operator/apply = %+v", op)
	}

	var cmd ext.CommandExecuteResult
	if err := h.call(t, ext.MethodCommandExecute, ext.CommandExecuteParams{Buffer: 1, Version: 4, ID: "upperLine", Cursor: ext.Position{Line: 1}}, &cmd); err != nil {
		t.Fatal(err)
	}
	if !cmd.OK || cmd.Transaction == nil || cmd.Transaction.BaseVersion != 4 || cmd.Transaction.Edits[0].Text != "DEF" {
		t.Errorf("command/execute = %+v", cmd)
	}
}

// 本体がバッファを送っていない command/execute（Withheld。基本設計 5.3）は、複製を探さずに nil をハンドラーへ渡す。
// 他の問い合わせは、これまでどおり開いていないバッファの誤りで応答する。
func TestCommandWithheld(t *testing.T) {
	s := server.New()
	var gotNil bool
	s.Command("resume", func(_ context.Context, b *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		gotNil = b == nil
		return ext.CommandExecuteResult{OK: true}, nil
	})
	s.TextObject("obj", func(context.Context, *server.Buffer, *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
		return ext.TextObjectResolveResult{OK: true}, nil
	})
	h := start(t, s)
	var cmd ext.CommandExecuteResult
	if err := h.call(t, ext.MethodCommandExecute, ext.CommandExecuteParams{Buffer: 3, Version: 1, ID: "resume", Withheld: true}, &cmd); err != nil || !cmd.OK || !gotNil {
		t.Fatalf("withheld command/execute = %+v, %v (buffer nil %v)", cmd, err, gotNil)
	}
	if err := h.call(t, ext.MethodCommandExecute, ext.CommandExecuteParams{Buffer: 3, Version: 1, ID: "resume"}, &cmd); err == nil {
		t.Error("command/execute for a buffer not open without Withheld succeeded")
	}
	var obj ext.TextObjectResolveResult
	if err := h.call(t, ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: 3, Version: 1, ID: "obj"}, &obj); err == nil {
		t.Error("textObject/resolve for a buffer not open succeeded")
	}
}

// 拡張機能から本体へ、buffer/edit、decoration/set、message/show を送る。
func TestSendToHost(t *testing.T) {
	s := server.New()
	if err := s.ShowMessage(ext.MessageInfo, "x"); !errors.Is(err, server.ErrNotRunning) {
		t.Errorf("ShowMessage before Serve = %v, want ErrNotRunning", err)
	}
	h := start(t, s)
	h.ping(t)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tx := ext.Transaction{BaseVersion: 3, Edits: []ext.Edit{{Text: "a"}}}
	res, err := s.Edit(ctx, ext.BufferEditParams{Buffer: 1, Transaction: tx})
	if err != nil || !res.Applied || res.Version != 4 {
		t.Errorf("Edit = %+v, %v", res, err)
	}
	if p := <-h.edits; p.Buffer != 1 || p.Transaction.BaseVersion != 3 {
		t.Errorf("host received %+v", p)
	}

	if err := s.SetDecorations(ext.DecorationSetParams{Buffer: 1, Version: 3, Decorations: []ext.Decoration{{Style: "keyword"}}}); err != nil {
		t.Fatal(err)
	}
	if n := h.nextNote(t); n.Method != ext.MethodDecorationSet || !strings.Contains(string(n.Params), `"keyword"`) {
		t.Errorf("got %s %s", n.Method, n.Params)
	}
	if err := s.ShowMessage(ext.MessageWarning, "こんにちは"); err != nil {
		t.Fatal(err)
	}
	if n := h.nextNote(t); n.Method != ext.MethodMessageShow || !strings.Contains(string(n.Params), "こんにちは") {
		t.Errorf("got %s %s", n.Method, n.Params)
	}
}

// 本体が接続を閉じると、Serve は nil を返す。ctx が終わったときも同じである。
func TestServeReturns(t *testing.T) {
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	s := server.New()
	served := make(chan error, 1)
	go func() { served <- s.Serve(context.Background(), extR, extW) }()
	_ = hostW.Close()
	_ = hostR.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve = %v, want nil", err)
		}
	case <-time.After(timeout):
		t.Fatal("Serve did not return")
	}
	if err := s.Serve(context.Background(), extR, extW); err == nil {
		t.Error("second Serve succeeded")
	}

	s2 := server.New()
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := io.Pipe()
	_, w := io.Pipe()
	go func() { served <- s2.Serve(ctx, r, w) }()
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("Serve after cancel = %v, want nil", err)
		}
	case <-time.After(timeout):
		t.Fatal("Serve did not return after cancel")
	}
}

func TestRegisterPanics(t *testing.T) {
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", name)
			}
		}()
		f()
	}
	s := server.New()
	s.TextObject("a", innerLine)
	mustPanic("duplicate id", func() { s.TextObject("a", innerLine) })
}

// モーションの結果の Version には、問い合わせのバージョン番号が入る。
//
// req: FR-11
func TestMotion(t *testing.T) {
	s := server.New()
	// 行末へ移動する。回数の省略（0）は 1 として扱う。
	s.Motion("eol", func(_ context.Context, b *server.Buffer, p *ext.MotionResolveParams) (ext.MotionResolveResult, error) {
		l := p.Cursor.Line + max(p.Count, 1) - 1
		if l >= b.LineCount() {
			return ext.MotionResolveResult{OK: false}, nil
		}
		return ext.MotionResolveResult{OK: true, Position: ext.Position{Line: l, Byte: len(b.Line(l))}, Inclusive: true}, nil
	})
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 6, Text: "ab\ncde"})

	var res ext.MotionResolveResult
	if err := h.call(t, ext.MethodMotionResolve, ext.MotionResolveParams{Buffer: 1, Version: 6, ID: "eol", Count: 2}, &res); err != nil {
		t.Fatal(err)
	}
	want := ext.MotionResolveResult{OK: true, Version: 6, Position: ext.Position{Line: 1, Byte: 3}, Inclusive: true}
	if res != want {
		t.Errorf("motion/resolve = %+v, want %+v", res, want)
	}
	res = ext.MotionResolveResult{}
	if err := h.call(t, ext.MethodMotionResolve, ext.MotionResolveParams{Buffer: 1, Version: 6, ID: "eol", Count: 3}, &res); err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Errorf("motion/resolve past the last line = %+v, want OK false", res)
	}
}

// 非同期のハンドラーが答えるまでの間も、ループは buffer/change と ping を処理し、結果には問い合わせの番号が入る。
func TestAsyncHandler(t *testing.T) {
	s := server.New()
	type pending struct {
		snapshot *server.Buffer
		reply    func(ext.TextObjectResolveResult, error)
	}
	got := make(chan pending, 1)
	s.TextObjectAsync("slow", func(_ context.Context, b *server.Buffer, _ *ext.TextObjectResolveParams, reply func(ext.TextObjectResolveResult, error)) {
		got <- pending{snapshot: b.Clone(), reply: reply}
	})
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: "abc"})

	f, err := h.conn.Go(ext.MethodTextObjectResolve, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: "slow"})
	if err != nil {
		t.Fatal(err)
	}
	p := <-got
	// 応答の前でも、差分の適用と ping は進む。
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 1, ToVersion: 2,
		Edits: []ext.Edit{{Text: "x"}},
	})
	h.ping(t)
	select {
	case <-f.Ready():
		t.Fatal("answered before reply")
	default:
	}

	r := ext.Range{End: ext.Position{Byte: len(p.snapshot.Line(0))}}
	p.reply(ext.TextObjectResolveResult{OK: true, Range: r}, nil)
	p.reply(ext.TextObjectResolveResult{}, errors.New("second"))
	var res ext.TextObjectResolveResult
	if err := f.Result(&res); err != nil {
		t.Fatal(err)
	}
	if want := (ext.TextObjectResolveResult{OK: true, Version: 1, Range: r}); res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	if p.snapshot.Text() != "abc" {
		t.Errorf("snapshot = %q", p.snapshot.Text())
	}
	h.ping(t)
	if !strings.Contains(h.log.String(), "reply called more than once") {
		t.Errorf("ErrorLog = %q", h.log.String())
	}
}

// フックの中から、渡された ctx で Edit を呼ぶと、待たずに ErrEditInHandler を返し、後続の問い合わせは詰まらない。
// フックが起こした goroutine からは、同じ ctx でも Edit が送られる。
func TestEditInHook(t *testing.T) {
	s := server.New()
	s.Motion("m", func(context.Context, *server.Buffer, *ext.MotionResolveParams) (ext.MotionResolveResult, error) {
		return ext.MotionResolveResult{OK: true}, nil
	})
	inHook := make(chan error, 1)
	background := make(chan error, 1)
	s.OnBufferChange(func(ctx context.Context, e *server.BufferChangeEvent) {
		p := ext.BufferEditParams{Buffer: e.Buffer.ID(), Transaction: ext.Transaction{BaseVersion: e.ToVersion}}
		_, err := s.Edit(ctx, p)
		inHook <- err
		go func() {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
			defer cancel()
			_, err := s.Edit(bctx, p)
			background <- err
		}()
	})
	h := start(t, s)
	h.notify(t, ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: "a"})
	h.notify(t, ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 1, ToVersion: 2,
		Edits: []ext.Edit{{Text: "b"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := h.conn.Call(ctx, ext.MethodMotionResolve, ext.MotionResolveParams{Buffer: 1, Version: 2, ID: "m"}, nil); err != nil {
		t.Errorf("motion/resolve after Edit in hook: %v", err)
	}
	if err := <-inHook; !errors.Is(err, server.ErrEditInHandler) {
		t.Errorf("Edit in hook = %v, want ErrEditInHandler", err)
	}
	if err := <-background; err != nil {
		t.Errorf("Edit from a goroutine = %v", err)
	}
	if p := <-h.edits; p.Transaction.BaseVersion != 2 {
		t.Errorf("host received %+v", p)
	}
}

// shutdown を受け取ると、OnShutdown のフックを呼び、フックが送った通知を書き出してから Serve が nil で戻る。
// shutdown より後に届いたメッセージは処理しない。
func TestShutdown(t *testing.T) {
	for _, withHook := range []bool{true, false} {
		t.Run(fmt.Sprintf("hook=%v", withHook), func(t *testing.T) {
			s := server.New()
			var mu sync.Mutex
			var calls []string
			record := func(name string) {
				mu.Lock()
				calls = append(calls, name)
				mu.Unlock()
			}
			// sent は、shutdown の後の buffer/open を本体の側で書き出し終えたら閉じる。
			sent := make(chan struct{})
			if withHook {
				s.OnShutdown(func(ctx context.Context, _ *server.ShutdownEvent) {
					record("shutdown")
					// buffer/open が拡張機能へ届くまで接続を開けておく。
					select {
					case <-sent:
					case <-time.After(timeout):
						t.Error("buffer/open was not sent while the shutdown hook was running")
					}
					if _, err := s.Edit(ctx, ext.BufferEditParams{}); !errors.Is(err, server.ErrEditInHandler) {
						t.Errorf("Edit in shutdown hook = %v, want ErrEditInHandler", err)
					}
					// 子プロセスの片付けに時間がかかる場合を演じる。
					time.Sleep(50 * time.Millisecond)
					_ = s.ShowMessage(ext.MessageInfo, "bye")
				})
			}
			s.OnBufferOpen(func(context.Context, *server.BufferOpenEvent) { record("open") })
			h := start(t, s)
			h.notify(t, ext.MethodShutdown, ext.ShutdownParams{})
			// shutdown より後のメッセージは処理しない、を確かめるために buffer/open を送る。
			// Serve は shutdown を処理すると接続を閉じるので、フックが無いと、この送信を書き出すより先に閉じ終えることがある（#82）。
			// その場合は送信が ErrClosed で失敗するが、届かなかったので処理されないことに変わりはない。
			// フックがある場合は、フックが書き出しの終わりを待つので、届いた上で処理されないことを確かめられる。
			err := h.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: "a"})
			if err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				err = h.conn.Flush(ctx)
				cancel()
			}
			close(sent)
			if err != nil && (withHook || !errors.Is(err, rpc.ErrClosed)) {
				t.Fatalf("send buffer/open: %v", err)
			}

			select {
			case err := <-h.served:
				if err != nil {
					t.Errorf("Serve = %v, want nil", err)
				}
				h.served <- nil // Cleanup でもう一度読む。
			case <-time.After(timeout):
				t.Fatal("Serve did not return after shutdown")
			}
			if withHook {
				n := h.nextNote(t)
				var p ext.MessageShowParams
				_ = json.Unmarshal(n.Params, &p)
				if n.Method != ext.MethodMessageShow || p.Text != "bye" {
					t.Errorf("note = %s %s, want message/show bye", n.Method, n.Params)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			want := []string{}
			if withHook {
				want = []string{"shutdown"}
			}
			if fmt.Sprint(calls) != fmt.Sprint(want) {
				t.Errorf("calls = %v, want %v", calls, want)
			}
		})
	}
}
