package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// socketEnv は、本体が待ち受けるソケットのパスを受け取る環境変数の名前である（仮。cmd/modal-editor-mcp-bridge と同じ）。
// 本体（internal/editor）が起動ごとにパスを決め、すべての拡張機能へ渡す（基本設計 6.2）。
// この拡張機能は、パスを利用者へ知らせるのに使うだけで、自分では待ち受けない。
const socketEnv = ext.EnvMCPSocket

// cmdStart は、待ち受けているパスを利用者へ知らせるコマンドである（仮。パッケージの doc の「MCP の接続」）。
const cmdStart = "mcp.start"

// errNotInherited は、本体から待ち受けのソケットを引き継がなかったことを表す。
// 本体がソケットのパスを決められなかったか、待ち受けられなかったときに、socket 以外の契機（mcp.start）で起動された場合である。
var errNotInherited = errors.New("本体から待ち受けのソケットを引き継いでいません")

// stopTimeout は、終わるときに、MCP の接続の処理が終わるのを待つ上限である（仮）。
// 本体は shutdown を送ってから 3 秒（仮）で SIGTERM を送るので（基本設計 5.2）、それより短くする。
const stopTimeout = time.Second

// notSentWait は、複製を持っていないバッファを読むときに、buffer/open の処理を待つ上限である（仮。hostEditor.ReadBuffer）。
const notSentWait = time.Second

// extension は、拡張機能の全体である。ext/server の Server、バッファの複製の一覧、本体から引き継いだ待ち受けと接続を持つ。
type extension struct {
	srv      *server.Server
	replicas *replicaSet
	tools    *toolbox
	log      *log.Logger
	// path は、本体が待ち受けるソケットのパス（環境変数 socketEnv）である。利用者へ知らせるのに使う。
	path string

	mu sync.Mutex
	// ln は、本体から引き継いだ待ち受けである。引き継がなかったか、止めた後は nil。
	ln net.Listener
	// conns は、本体が受け付けて引き継いだ接続のうち、まだ処理を始めていないものである。
	conns []net.Conn
	// listenErr は、待ち受けを引き継げなかった理由である。
	listenErr error
	serving   bool
	// cancel は、MCP の接続の処理を取り消す。
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool
}

// newExtension は、本体から引き継いだ待ち受け ln と接続 conns（server.InheritedSocket の結果）で拡張機能を作る。
// ln が nil なら、inheritErr（nil なら errNotInherited）を、引き継げなかった理由として mcp.start で利用者へ知らせる。
// path は、本体が待ち受けるソケットのパスで、利用者へ知らせるのに使う。
func newExtension(path string, ln net.Listener, conns []net.Conn, inheritErr error) *extension {
	x := &extension{
		srv: server.New(), replicas: newReplicaSet(), path: path, ln: ln, conns: conns,
		log: log.New(os.Stderr, "modal-editor-ext-mcp: ", log.LstdFlags),
	}
	if ln == nil {
		x.listenErr = inheritErr
		if x.listenErr == nil {
			x.listenErr = errNotInherited
		}
	}
	x.srv.ErrorLog = x.log
	x.tools = newToolbox(&hostEditor{Server: x.srv, replicas: x.replicas})

	x.srv.OnInitialize(func(context.Context, *server.InitializeEvent) error {
		// 引き継げなくても initialize は失敗させない。失敗させると本体が再起動を繰り返す。
		// 理由は mcp.start で利用者へ知らせる。
		x.start()
		return nil
	})
	x.srv.OnBufferOpen(func(_ context.Context, e *server.BufferOpenEvent) { x.replicas.open(e.Buffer.ID()) })
	x.srv.OnBufferClose(func(_ context.Context, e *server.BufferCloseEvent) { x.replicas.close(e.Buffer) })
	x.srv.OnShutdown(func(context.Context, *server.ShutdownEvent) { x.stop() })
	x.srv.Command(cmdStart, func(context.Context, *server.Buffer, *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		return x.startCommand(), nil
	})
	return x
}

// start は、本体から引き継いだ接続の処理と、引き継いだ待ち受けでの受け付けを始める（基本設計 6.2）。
// 接続ごとに serveMCP を動かす。既に始めたか、止めた後か、待ち受けを引き継いでいなければ何もしない。
// ツールが本体へ問い合わせられるように、initialize を受け取ってから呼ぶ。
func (x *extension) start() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ln == nil || x.serving || x.stopped {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	x.serving, x.cancel = true, cancel
	for _, c := range x.conns {
		x.wg.Add(1)
		go func() {
			defer x.wg.Done()
			x.serve(ctx, c)
		}()
	}
	x.conns = nil
	ln := x.ln
	x.wg.Add(1)
	go func() {
		defer x.wg.Done()
		x.acceptLoop(ctx, ln)
	}()
}

// acceptLoop は、ln が閉じるまで接続を受け付け、接続ごとに別の goroutine で serveMCP を動かす。
func (x *extension) acceptLoop(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				x.log.Printf("accept: %v", err)
			}
			return
		}
		x.wg.Add(1)
		go func() {
			defer x.wg.Done()
			x.serve(ctx, conn)
		}()
	}
}

// serve は、1 本の MCP の接続を処理する。本体から引き継いだ接続も、引き継いだ待ち受けで受け付けた接続も、ここへ渡す。
func (x *extension) serve(ctx context.Context, conn io.ReadWriteCloser) {
	if err := serveMCP(ctx, conn, x.tools, x.log); err != nil {
		x.log.Printf("mcp connection: %v", err)
	}
}

// startCommand は、mcp.start を処理する。待ち受けているパスか、引き継げなかった理由を利用者へ知らせる。
func (x *extension) startCommand() ext.CommandExecuteResult {
	x.start()
	x.mu.Lock()
	ln, lerr, path := x.ln, x.listenErr, x.path
	x.mu.Unlock()
	if ln == nil {
		if lerr == nil {
			lerr = errors.New("拡張機能は終了処理中です")
		}
		_ = x.srv.ShowMessage(ext.MessageError, "MCP のサーバーを始められません: "+lerr.Error())
		return ext.CommandExecuteResult{OK: false}
	}
	_ = x.srv.ShowMessage(ext.MessageInfo, "MCP のサーバーは "+path+" で接続を待っています")
	return ext.CommandExecuteResult{OK: true}
}

// stop は、待ち受けをやめ、MCP の接続を閉じ、処理が終わるのを stopTimeout まで待つ。何度呼んでもよい。
// 待ち受けは本体と共有しているので、閉じても本体の待ち受けは続く（本体は拡張機能が再起動を待つ間に受け付ける）。
func (x *extension) stop() {
	x.mu.Lock()
	x.stopped = true
	ln, cancel, conns := x.ln, x.cancel, x.conns
	x.ln, x.cancel, x.conns = nil, nil, nil
	x.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	for _, c := range conns {
		_ = c.Close()
	}
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		x.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(stopTimeout):
		x.log.Printf("mcp connections did not finish within %v", stopTimeout)
	}
}

// replicaSet は、拡張機能が複製を持っているバッファの番号の集合である。
// フック（ループの上）が書き、ツール（別の goroutine）が読むので、ロックで守る。
// 本体は不正な UTF-8 を含むバッファを拡張機能へ送らないので（基本設計 5.3）、editor/state にあっても複製が無いことがある。
type replicaSet struct {
	mu  sync.Mutex
	ids map[ext.BufferID]bool
}

func newReplicaSet() *replicaSet { return &replicaSet{ids: map[ext.BufferID]bool{}} }

func (r *replicaSet) open(id ext.BufferID) {
	r.mu.Lock()
	r.ids[id] = true
	r.mu.Unlock()
}

func (r *replicaSet) close(id ext.BufferID) {
	r.mu.Lock()
	delete(r.ids, id)
	r.mu.Unlock()
}

func (r *replicaSet) held(id ext.BufferID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ids[id]
}

// hostEditor は、editorAPI の実物である。本体への問い合わせは ext/server の Server がそのまま実装する。
type hostEditor struct {
	*server.Server
	replicas *replicaSet
}

// ReadBuffer は、複製が version 以降になるまで WaitBuffer で待ち、その時点の行の列を返す。
//
// version は、直前の editor/state で得た現在のバージョン番号である。
// 本体は buffer/change を editor/state の応答より先に送るが、ext/server は通知をループで処理するので、
// 応答を受け取った時点で複製がまだ古いことがある。待つことで、editor/state の時点以降の内容を読む。
//
// 複製を持っていないバッファは、送られていない（不正な UTF-8 を含む）か、open_file の直後で buffer/open をまだ処理していないかである。
// 後者のために notSentWait だけ待ち、それでも複製ができなければ errNotSent を返す。
func (h *hostEditor) ReadBuffer(ctx context.Context, id ext.BufferID, version ext.Version) (snapshot, error) {
	wctx := ctx
	if !h.replicas.held(id) {
		var cancel context.CancelFunc
		wctx, cancel = context.WithTimeout(ctx, notSentWait)
		defer cancel()
	}
	b, err := h.WaitBuffer(wctx, id, version)
	if errors.Is(err, server.ErrBufferClosed) || (err != nil && ctx.Err() == nil && wctx.Err() != nil) {
		return snapshot{}, errNotSent
	}
	if err != nil {
		return snapshot{}, err
	}
	lines := make([]string, b.LineCount())
	for i := range lines {
		lines[i] = b.Line(i)
	}
	return snapshot{Version: b.Version(), Lines: lines}, nil
}
