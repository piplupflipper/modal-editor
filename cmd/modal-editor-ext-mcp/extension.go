package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// socketEnv は、待ち受けるソケットのパスを受け取る環境変数の名前である（仮。cmd/modal-editor-mcp-bridge と同じ）。
const socketEnv = "MODAL_EDITOR_MCP_SOCKET"

// cmdStart は、拡張機能を起動する契機のコマンドである（仮。パッケージの doc の「MCP の接続」）。
const cmdStart = "mcp.start"

// stopTimeout は、終わるときに、MCP の接続の処理が終わるのを待つ上限である（仮）。
// 本体は shutdown を送ってから 3 秒（仮）で SIGTERM を送るので（基本設計 5.2）、それより短くする。
const stopTimeout = time.Second

// notSentWait は、複製を持っていないバッファを読むときに、buffer/open の処理を待つ上限である（仮。hostEditor.ReadBuffer）。
const notSentWait = time.Second

// extension は、拡張機能の全体である。ext/server の Server、バッファの複製の一覧、ソケットの待ち受けを持つ。
type extension struct {
	srv      *server.Server
	replicas *replicaSet
	tools    *toolbox
	getenv   func(string) string
	log      *log.Logger

	mu sync.Mutex
	// path は、待ち受けているソケットのパスである。待ち受けていなければ空。
	path string
	// listenErr は、待ち受けを始められなかった理由である。
	listenErr error
	ln        net.Listener
	// cancel は、MCP の接続の処理を取り消す。
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool
}

func newExtension(getenv func(string) string) *extension {
	x := &extension{srv: server.New(), replicas: newReplicaSet(), getenv: getenv, log: log.New(os.Stderr, "modal-editor-ext-mcp: ", log.LstdFlags)}
	x.srv.ErrorLog = x.log
	x.tools = newToolbox(&hostEditor{Server: x.srv, replicas: x.replicas})

	x.srv.OnInitialize(func(context.Context, *server.InitializeEvent) error {
		// 待ち受けられなくても initialize は失敗させない。失敗させると本体が再起動を繰り返す。
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

// start は、環境変数 socketEnv のパスで待ち受けを始め、受け付けた接続ごとに serveMCP を動かす。
// 既に待ち受けているか、止めた後なら何もしない。
func (x *extension) start() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.ln != nil || x.stopped {
		return
	}
	path := x.getenv(socketEnv)
	if path == "" {
		x.listenErr = fmt.Errorf("環境変数 %s にソケットのパスが設定されていません", socketEnv)
		return
	}
	ln, err := listenSocket(path)
	if err != nil {
		x.listenErr = err
		x.log.Printf("listen: %v", err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	x.ln, x.path, x.listenErr, x.cancel = ln, path, nil, cancel
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

// serve は、1 本の MCP の接続を処理する。接続の受け取り方に依らない差し込みの口で、
// `socket` の契機の裁定の後は、本体から引き継いだ接続（net.FileConn）もここへ渡す。
func (x *extension) serve(ctx context.Context, conn io.ReadWriteCloser) {
	if err := serveMCP(ctx, conn, x.tools, x.log); err != nil {
		x.log.Printf("mcp connection: %v", err)
	}
}

// startCommand は、mcp.start を処理する。待ち受けていなければ始め、結果を利用者へ知らせる。
func (x *extension) startCommand() ext.CommandExecuteResult {
	x.start()
	x.mu.Lock()
	path, lerr := x.path, x.listenErr
	x.mu.Unlock()
	if path == "" {
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
func (x *extension) stop() {
	x.mu.Lock()
	x.stopped = true
	ln, cancel := x.ln, x.cancel
	x.ln, x.cancel, x.path = nil, nil, ""
	x.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
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

// listenSocket は、path に Unix ドメインソケットを作って待ち受ける。
// path に前回の起動で残ったソケットがあり、接続を受け付けるものがいなければ、消してから作る。
// 他のエディタが待ち受けているソケットと、ソケットでないファイルは消さずに誤りを返す。
// ソケットのファイルは所有者だけが読み書きできるようにする。接続した相手はバッファを読み書きできるためである。
func listenSocket(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s はソケットでないファイルとして既にあります", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("%s は他のプロセスが待ち受けています", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("残っていたソケット %s を消せません: %w", path, err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("%s で待ち受けられません: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("%s の権限を変えられません: %w", path, err)
	}
	return ln, nil
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
