package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// ErrNotRunning は、Serve の前か後に、本体へメッセージを送ろうとしたことを表す。
var ErrNotRunning = errors.New("server: not serving")

// codeRequestCancelled は、取り消されたリクエストへの応答のエラーコードである（LSP の RequestCancelled と同じ値）。
const codeRequestCancelled = -32800

// MotionHandler は、motion/resolve に答えるハンドラーである。
// b は p.Buffer の複製で、そのバージョン番号は p.Version と一致する。
// 返した result の Version は、Server が p.Version で上書きする。
type MotionHandler func(ctx context.Context, b *Buffer, p *ext.MotionResolveParams) (ext.MotionResolveResult, error)

// TextObjectHandler は、textObject/resolve に答えるハンドラーである。
// b は p.Buffer の複製で、そのバージョン番号は p.Version と一致する。
// 返した result の Version は、Server が p.Version で上書きする。
type TextObjectHandler func(ctx context.Context, b *Buffer, p *ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error)

// OperatorHandler は、operator/apply に答えるハンドラーである。
// b は p.Buffer の複製で、そのバージョン番号は p.Version と一致する。
// 返した result の Transaction.BaseVersion は、Server が p.Version で上書きする。
type OperatorHandler func(ctx context.Context, b *Buffer, p *ext.OperatorApplyParams) (ext.OperatorApplyResult, error)

// CommandHandler は、command/execute に答えるハンドラーである。
// b は p.Buffer の複製で、そのバージョン番号は p.Version と一致する。
// 返した result に Transaction があれば、その BaseVersion は、Server が p.Version で上書きする。
type CommandHandler func(ctx context.Context, b *Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error)

// Server は、拡張機能の側のメッセージループである。
// 本体との JSON-RPC の接続、バッファの複製の管理、登録したハンドラーの呼び出しを行う。
//
// New で作り、Motion などでハンドラーを登録してから、Run（標準入出力）か Serve で動かす。
// 登録は Serve の前に済ませる。Serve の後に登録すると panic する。
//
// # 処理の順序
//
// 本体から届いたリクエストと通知は、届いた順に、Server の持つ 1 つの goroutine が 1 件ずつ処理する。
// ハンドラーとフック（OnInitialize、OnBufferChange、OnBufferClose）は、すべてこの goroutine から呼ばれる。
// このため、ハンドラーが読む複製は、本体がそのリクエストより前に送った buffer/change をすべて適用した内容になる。
// ハンドラーが戻るまで次のメッセージを処理しないので、ハンドラーは速やかに戻る。
// 時間のかかる処理は、Buffer.Clone した複製を別の goroutine に渡して行い、結果は SetDecorations などで送る。
//
// 受信そのものは別の goroutine が続けるので、ハンドラーの実行中に届いた $/cancel で、ハンドラーの ctx が取り消される。
//
// # 補助が処理するメッセージ
//
//   - initialize: ext.APIVersion を応答する。OnInitialize を登録していれば、応答の前に呼ぶ
//   - ping: 空の結果を応答する。メッセージを処理する goroutine が応答するので、ハンドラーが止まっていれば ping も応答しない
//   - $/cancel: 対象のリクエストのハンドラーの ctx を取り消す。まだ処理していないリクエストなら、ハンドラーを呼ばずに失敗を応答する
//   - buffer/open、buffer/change、buffer/close: 複製を作る、更新する、捨てる
//
// buffer/change の FromVersion が複製の番号と一致しないか、編集の位置が複製の外を指す場合は、差分が欠けたと判断し、
// buffer/resync を送る（基本設計 5.3）。次の buffer/open が届くまで、そのバッファの差分は捨て、
// そのバッファへの問い合わせには失敗を応答する。
//
// # 失敗の応答
//
// 次の場合、Server はハンドラーを呼ばずに、または呼んだ後に、JSON-RPC のエラーを応答する。
// 本体はどれも操作の失敗として扱う。
//
//   - ID が登録されていない
//   - バッファを開いていないか、複製のバージョン番号が params の Version と一致しない
//   - ハンドラーが誤りを返した
//   - ハンドラーが panic した。panic は回復し、拡張機能のプロセスは動き続ける。スタックは ErrorLog に書く
//
// # 標準出力
//
// Run は標準出力を本体とのメッセージに使う。拡張機能は標準出力へ他のものを書いてはならない。
// ログは標準エラー出力へ書く。本体はそれをログとして記録する（基本設計 5.3）。
type Server struct {
	// ErrorLog は、panic と内部の誤りを書くロガーである。nil なら標準エラー出力へ書く。
	ErrorLog *log.Logger

	motions     map[string]MotionHandler
	textObjects map[string]TextObjectHandler
	operators   map[string]OperatorHandler
	commands    map[string]CommandHandler
	onInit      func(ctx context.Context, settings json.RawMessage) error
	onChange    func(b *Buffer)
	onClose     func(id ext.BufferID)

	started atomic.Bool
	conn    atomic.Pointer[rpc.Conn]

	// 以下は、メッセージを処理する goroutine だけが触る。
	buffers map[ext.BufferID]*Buffer
	// stale は、buffer/resync を送り、buffer/open を待っているバッファである。
	stale map[ext.BufferID]bool
}

// New は、ハンドラーを登録していない Server を作る。
func New() *Server {
	return &Server{
		motions:     make(map[string]MotionHandler),
		textObjects: make(map[string]TextObjectHandler),
		operators:   make(map[string]OperatorHandler),
		commands:    make(map[string]CommandHandler),
		buffers:     make(map[ext.BufferID]*Buffer),
		stale:       make(map[ext.BufferID]bool),
	}
}

// Motion は、宣言ファイルの contributes.motions に書いた id のモーションのハンドラーを登録する。
// 同じ id を 2 回登録するか、Serve の後に呼ぶと panic する。
//
// req: FR-11
func (s *Server) Motion(id string, h MotionHandler) { register(s, s.motions, "motion", id, h) }

// TextObject は、宣言ファイルの contributes.textObjects に書いた id のテキストオブジェクトのハンドラーを登録する。
// 同じ id を 2 回登録するか、Serve の後に呼ぶと panic する。
//
// req: FR-11
func (s *Server) TextObject(id string, h TextObjectHandler) {
	register(s, s.textObjects, "text object", id, h)
}

// Operator は、宣言ファイルの contributes.operators に書いた id のオペレーターのハンドラーを登録する。
// 同じ id を 2 回登録するか、Serve の後に呼ぶと panic する。
//
// req: FR-11
func (s *Server) Operator(id string, h OperatorHandler) { register(s, s.operators, "operator", id, h) }

// Command は、宣言ファイルの contributes.commands に書いた id のコマンドのハンドラーを登録する。
// 同じ id を 2 回登録するか、Serve の後に呼ぶと panic する。
//
// req: FR-11
func (s *Server) Command(id string, h CommandHandler) { register(s, s.commands, "command", id, h) }

// OnInitialize は、initialize を受け取ったときに呼ぶ関数を登録する。
// settings は、利用者の設定ファイルのうち、この拡張機能に向けた部分である（無ければ nil）。
// f が誤りを返すと、initialize に失敗を応答する。本体はそれを起動の失敗として扱う。
// Serve の後に呼ぶと panic する。
func (s *Server) OnInitialize(f func(ctx context.Context, settings json.RawMessage) error) {
	s.mustNotStarted()
	s.onInit = f
}

// OnBufferChange は、複製を作ったとき（buffer/open）と、差分を適用したとき（buffer/change）に呼ぶ関数を登録する。
// b は更新した後の複製である。構文の解析のように、変更のたびに計算し直す拡張機能が使う。
// f の panic は回復して ErrorLog に書く。Serve の後に呼ぶと panic する。
func (s *Server) OnBufferChange(f func(b *Buffer)) {
	s.mustNotStarted()
	s.onChange = f
}

// OnBufferClose は、buffer/close を受け取って複製を捨てたときに呼ぶ関数を登録する。
// f の panic は回復して ErrorLog に書く。Serve の後に呼ぶと panic する。
func (s *Server) OnBufferClose(f func(id ext.BufferID)) {
	s.mustNotStarted()
	s.onClose = f
}

func register[H any](s *Server, m map[string]H, kind, id string, h H) {
	s.mustNotStarted()
	if _, dup := m[id]; dup {
		panic(fmt.Sprintf("server: %s %q registered twice", kind, id))
	}
	m[id] = h
}

func (s *Server) mustNotStarted() {
	if s.started.Load() {
		panic("server: register after Serve")
	}
}

// Run は、標準入力と標準出力で本体と通信し、Serve と同じく動く。
// 拡張機能の main から呼ぶ。
func (s *Server) Run(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

// Serve は、r から本体のメッセージを読み、w へ書き、接続が終わるまでメッセージを処理する。
// r と w が io.Closer を実装していれば、終わるときに閉じる。
//
// 本体が接続を閉じたとき（r が EOF になったとき）と、ctx が終わったときは nil を返す。
// JSON-RPC として解釈できないメッセージを受け取ったときなど、それ以外の理由で終わったときは、その誤りを返す。
// 1 つの Server で Serve を呼べるのは 1 回だけで、2 回目は誤りを返す。
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("server: Serve called twice")
	}
	q := newQueue()
	conn := rpc.NewConn(r, w, rpc.HandlerFuncs{
		Request: func(ctx context.Context, req *rpc.Request, reply rpc.Replier) {
			q.push(inbound{ctx: ctx, req: req, reply: reply})
		},
		Notification: func(ctx context.Context, n *rpc.Notification) {
			q.push(inbound{ctx: ctx, note: n})
		},
	}, rpc.WithCancelMethod(ext.MethodCancel))
	s.conn.Store(conn)
	defer s.conn.Store(nil)

	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	for {
		batch := q.take()
		for _, m := range batch {
			if conn.Err() != nil {
				break
			}
			s.dispatch(m)
		}
		if len(batch) > 0 {
			continue
		}
		select {
		case <-q.wake:
		case <-conn.Done():
			_ = conn.Close()
			err := conn.Err()
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return nil
			}
			return err
		}
	}
}

// Edit は、本体へ buffer/edit を送り、応答を待つ。
// 補完の確定や整形のように、拡張機能の側から始める編集に使う。
// 適用されると、本体は buffer/change を送り、複製はそれを処理した時点で更新される（Edit が戻った時点ではまだ古いことがある）。
//
// ハンドラーの中で Edit の応答を待ってはならない。
// 本体はハンドラーの応答を待つ間、buffer/edit を処理できないので、本体の期限まで応答が来ない。
// ハンドラーの編集は、OperatorApplyResult と CommandExecuteResult の Transaction で返す。
func (s *Server) Edit(ctx context.Context, p ext.BufferEditParams) (ext.BufferEditResult, error) {
	var res ext.BufferEditResult
	conn := s.conn.Load()
	if conn == nil {
		return res, ErrNotRunning
	}
	err := conn.Call(ctx, ext.MethodBufferEdit, p, &res)
	return res, err
}

// SetDecorations は、本体へ decoration/set を送る。応答は待たない。
func (s *Server) SetDecorations(p ext.DecorationSetParams) error {
	return s.notify(ext.MethodDecorationSet, p)
}

// ShowMessage は、本体へ message/show を送り、利用者へメッセージを表示させる。応答は待たない。
func (s *Server) ShowMessage(level ext.MessageLevel, text string) error {
	return s.notify(ext.MethodMessageShow, ext.MessageShowParams{Level: level, Text: text})
}

func (s *Server) notify(method string, params any) error {
	conn := s.conn.Load()
	if conn == nil {
		return ErrNotRunning
	}
	return conn.Notify(method, params)
}

// dispatch は、1 件のメッセージを処理する。
func (s *Server) dispatch(m inbound) {
	if m.note != nil {
		s.handleNotification(m.note)
		return
	}
	if m.ctx.Err() != nil {
		// 処理する前に取り消されたか、接続が終わった。
		_ = m.reply(nil, &rpc.Error{Code: codeRequestCancelled, Message: "request cancelled: " + m.req.Method})
		return
	}
	result, err := s.handleRequest(m.ctx, m.req)
	if rerr := m.reply(result, err); rerr != nil {
		s.logf("reply to %s: %v", m.req.Method, rerr)
	}
}

func (s *Server) handleRequest(ctx context.Context, req *rpc.Request) (any, error) {
	switch req.Method {
	case ext.MethodInitialize:
		return s.initialize(ctx, req)
	case ext.MethodPing:
		return ext.PingResult{}, nil
	case ext.MethodMotionResolve:
		return query(ctx, s, req, s.motions, func(p *ext.MotionResolveParams) (ext.BufferID, ext.Version, string) {
			return p.Buffer, p.Version, p.ID
		}, func(r *ext.MotionResolveResult, v ext.Version) { r.Version = v })
	case ext.MethodTextObjectResolve:
		return query(ctx, s, req, s.textObjects, func(p *ext.TextObjectResolveParams) (ext.BufferID, ext.Version, string) {
			return p.Buffer, p.Version, p.ID
		}, func(r *ext.TextObjectResolveResult, v ext.Version) { r.Version = v })
	case ext.MethodOperatorApply:
		return query(ctx, s, req, s.operators, func(p *ext.OperatorApplyParams) (ext.BufferID, ext.Version, string) {
			return p.Buffer, p.Version, p.ID
		}, func(r *ext.OperatorApplyResult, v ext.Version) { r.Transaction.BaseVersion = v })
	case ext.MethodCommandExecute:
		return query(ctx, s, req, s.commands, func(p *ext.CommandExecuteParams) (ext.BufferID, ext.Version, string) {
			return p.Buffer, p.Version, p.ID
		}, func(r *ext.CommandExecuteResult, v ext.Version) {
			if r.Transaction != nil {
				r.Transaction.BaseVersion = v
			}
		})
	default:
		return nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: "method not found: " + req.Method}
	}
}

func (s *Server) initialize(ctx context.Context, req *rpc.Request) (any, error) {
	var p ext.InitializeParams
	if err := decodeParams(req, &p); err != nil {
		return nil, err
	}
	if s.onInit != nil {
		if err := s.protect(req.Method, func() error { return s.onInit(ctx, p.Settings) }); err != nil {
			return nil, err
		}
	}
	return ext.InitializeResult{APIVersion: ext.APIVersion}, nil
}

// query は、キー入力ごとの問い合わせ（とコマンド）を、id のハンドラーへ渡す。
// バッファの複製が params の Version と一致する場合だけハンドラーを呼び、結果のバージョン番号を stamp で埋める。
func query[P, R any, H ~func(context.Context, *Buffer, *P) (R, error)](
	ctx context.Context, s *Server, req *rpc.Request,
	handlers map[string]H,
	meta func(*P) (ext.BufferID, ext.Version, string),
	stamp func(*R, ext.Version),
) (any, error) {
	var p P
	if err := decodeParams(req, &p); err != nil {
		return nil, err
	}
	bufID, version, id := meta(&p)
	h, ok := handlers[id]
	if !ok {
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("%s: unknown id %q", req.Method, id)}
	}
	b, err := s.bufferAt(bufID, version)
	if err != nil {
		return nil, err
	}
	var res R
	err = s.protect(req.Method+" "+id, func() error {
		var herr error
		res, herr = h(ctx, b, &p)
		return herr
	})
	if err != nil {
		return nil, err
	}
	stamp(&res, version)
	return res, nil
}

// bufferAt は、id のバッファの複製が version の時点にあれば返す。
func (s *Server) bufferAt(id ext.BufferID, version ext.Version) (*Buffer, error) {
	b, ok := s.buffers[id]
	switch {
	case !ok:
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("buffer %d is not open", id)}
	case s.stale[id]:
		return nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("buffer %d is waiting for resync", id)}
	case b.version != version:
		return nil, &rpc.Error{
			Code:    rpc.CodeInvalidParams,
			Message: fmt.Sprintf("buffer %d: version %d does not match the replica %d", id, version, b.version),
		}
	}
	return b, nil
}

func (s *Server) handleNotification(n *rpc.Notification) {
	switch n.Method {
	case ext.MethodBufferOpen:
		var p ext.BufferOpenParams
		if err := decodeParams(n, &p); err != nil {
			s.logf("%v", err)
			return
		}
		b := newBuffer(&p)
		s.buffers[p.Buffer] = b
		delete(s.stale, p.Buffer)
		s.changed(b)
	case ext.MethodBufferChange:
		var p ext.BufferChangeParams
		if err := decodeParams(n, &p); err != nil {
			s.logf("%v", err)
			return
		}
		s.bufferChange(&p)
	case ext.MethodBufferClose:
		var p ext.BufferCloseParams
		if err := decodeParams(n, &p); err != nil {
			s.logf("%v", err)
			return
		}
		_, open := s.buffers[p.Buffer]
		delete(s.buffers, p.Buffer)
		delete(s.stale, p.Buffer)
		if open && s.onClose != nil {
			_ = s.protect("buffer close hook", func() error { s.onClose(p.Buffer); return nil })
		}
	default:
		// 知らない通知は捨てる（JSON-RPC 2.0 では通知に応答しない）。
	}
}

// bufferChange は、buffer/change の差分を複製に適用する。
// 差分が欠けたと判断したら buffer/resync を送り、buffer/open が届くまで以後の差分を捨てる。
//
// req: FR-16
func (s *Server) bufferChange(p *ext.BufferChangeParams) {
	if s.stale[p.Buffer] {
		return
	}
	b, ok := s.buffers[p.Buffer]
	var cause error
	switch {
	case !ok:
		cause = errors.New("buffer is not open")
	case b.version != p.FromVersion:
		cause = fmt.Errorf("fromVersion %d does not match the replica %d", p.FromVersion, b.version)
	default:
		cause = b.applyChange(p)
	}
	if cause == nil {
		s.changed(b)
		return
	}
	s.logf("buffer %d: %v; requesting resync", p.Buffer, cause)
	s.stale[p.Buffer] = true
	var have ext.Version
	if ok {
		have = b.version
	}
	if err := s.notify(ext.MethodBufferResync, ext.BufferResyncParams{Buffer: p.Buffer, Version: have}); err != nil {
		s.logf("send buffer/resync: %v", err)
	}
}

func (s *Server) changed(b *Buffer) {
	if s.onChange != nil {
		_ = s.protect("buffer change hook", func() error { s.onChange(b); return nil })
	}
}

// protect は f を呼び、f の panic を回復して誤りに変える。
// 回復した panic は、スタックと一緒に ErrorLog に書く。
func (s *Server) protect(name string, f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("panic in %s: %v\n%s", name, r, debug.Stack())
			err = &rpc.Error{Code: rpc.CodeInternalError, Message: fmt.Sprintf("panic in %s: %v", name, r)}
		}
	}()
	return f()
}

func (s *Server) logf(format string, args ...any) {
	l := s.ErrorLog
	if l == nil {
		l = log.New(os.Stderr, "", log.LstdFlags)
	}
	l.Printf(format, args...)
}

// decodeParams は、リクエストか通知の params を v へ読み込む。読めなければ CodeInvalidParams の誤りを返す。
func decodeParams(m any, v any) error {
	var method string
	var params json.RawMessage
	switch m := m.(type) {
	case *rpc.Request:
		method, params = m.Method, m.Params
	case *rpc.Notification:
		method, params = m.Method, m.Params
	}
	if len(params) == 0 {
		params = []byte("{}")
	}
	if err := json.Unmarshal(params, v); err != nil {
		return &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf("%s: invalid params: %v", method, err)}
	}
	return nil
}

// inbound は、本体から届いた 1 件のメッセージである。req と note のどちらか一方だけが nil でない。
type inbound struct {
	ctx   context.Context
	req   *rpc.Request
	reply rpc.Replier
	note  *rpc.Notification
}

// queue は、接続の読み取りの goroutine から、メッセージを処理する goroutine へメッセージを渡す、上限の無い列である。
// 読み取りの goroutine を止めない（$/cancel と応答を読み続ける）ために、積む側は待たない。
type queue struct {
	mu    sync.Mutex
	items []inbound
	wake  chan struct{}
}

func newQueue() *queue { return &queue{wake: make(chan struct{}, 1)} }

func (q *queue) push(m inbound) {
	q.mu.Lock()
	q.items = append(q.items, m)
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *queue) take() []inbound {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}
