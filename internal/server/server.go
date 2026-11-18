// Package server は、編集の状態を持つ側（server）である（基本設計 1.2、2 章。FR-58）。
//
// server は internal/editor のイベントループを持ち、client との接続（protocol.ServerConn）から届いたキーをイベントループへ渡し、
// イベントループが組み立てた画面とクリップボードへの書き込みを client へ送る。
// 端末は持たない。端末の raw モード、キーの解読、画面の出力は client（internal/client）が持つ。
//
// # 段階 3 の 2 段目の範囲
//
// 編集の client は 1 つだけ繋ぐ。1 つのプロセスの中の接続（protocol.Pipe。FR-60 の構成）でも、
// server のソケット（protocol.NewServerSocket。Listen で待ち受ける）でもよい（Serve）。
// 1 つのプロセスの構成でも server のソケットを待ち受けるが（基本設計 1.2）、2 つ目からの接続は、受け付けてすぐ閉じる。
// 複数の編集の client、部品の client、待つだけの client、client ごとのタブの並びは後の段で足す。
// それに備えて、client ごとに識別子（editor.ClientID）を割り当て、キーのイベントに付けてイベントループへ渡す（基本設計 2.1）。
//
// 今の Server は、editor.Editor が持つ唯一の editor.Terminal を、1 つの接続で演じる形である（段階 3 の仮の形）。
// 複数の編集の client の issue では、接続の受け付けと切断をイベントループへ送ること、client ごとの画面の大きさと描画、
// editor.Terminal の口の client 別への置き換えが要る（PR #187 のレビューの推奨 4）。
//
// # 並行処理
//
// 編集の状態は、Run と Serve を呼んだ goroutine（イベントループ）だけが触る（基本設計 2.1）。
// client の接続の読み取りは別の goroutine で行い、イベントをチャネルでイベントループへ送る。
// client への送信も別の goroutine で行う。イベントループは画面とクリップボードへの書き込みを client ごとの置き場に入れるだけで、送り終えるのを待たない。
// 画面は最新の 1 つだけを置き、送る前に次の画面が来たら古い方を捨てる。クリップボードへの書き込みは捨てずに順に送る。
// こうすると、読み取りの遅い client があっても、イベントループは止まらない（PR #187 のレビューの推奨 2）。
//
// # 層
//
// internal/editor と internal/protocol を import するので、その上の層に置く。
// internal/client と同じ層で、互いに import しない。両者をつなぐのは cmd/modal-editor である。
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// flushTimeout は、Run が終わるときに、client へまだ送っていないクリップボードへの書き込みを送り終えるのを待つ上限である。
// 超えたら接続を閉じる（読まない client で server が終われなくならないため）。
const flushTimeout = time.Second

// helloTimeout は、server のソケットで受け付けた接続が Hello を送るのを待つ上限である（仮）。
// 超えたら接続を閉じ、次の接続を待つ（Hello を送らない接続で、編集の client を受け付けられなくならないため）。
const helloTimeout = 5 * time.Second

// ErrUnexpectedHello は、client の最初のメッセージが、編集の client の Hello でなかったことを表す。
var ErrUnexpectedHello = errors.New("server: first message is not a hello from an edit client")

// Server は、編集の状態を持つ側である。New で作り、Open で起動の引数のファイルを開き、Run か Serve で client を繋いで動かし、Close で拡張機能を止める。
type Server struct {
	ed *editor.Editor
	// client は、繋いだ編集の client である。今は 1 つだけで、Run か Serve で繋ぐ。
	client *clientTerm
	nextID editor.ClientID
	log    *slog.Logger
}

// New は、cfg でイベントループ（editor.Editor）を作り、Server を返す。
// cfg.OnPanic は、server が起こす goroutine（client の接続の読み取りと送信、ソケットの受け付け）の panic でも呼ぶ。
func New(cfg editor.Config) (*Server, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	t := &clientTerm{onPanic: cfg.OnPanic, log: logger, events: make(chan editor.TermEvent)}
	ed, err := editor.New(t, cfg)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return &Server{ed: ed, client: t, log: logger}, nil
}

// Open は、path のファイルを開き、カーソルのあるバッファにする（editor.Editor.Open）。Run の前に呼ぶ。
func (s *Server) Open(path string) error {
	return s.ed.Open(path)
}

// Run は、conn を編集の client として繋ぎ、イベントループを動かす。Serve(ctx, nil, conn) と同じである。
func (s *Server) Run(ctx context.Context, conn protocol.ServerConn) error {
	return s.Serve(ctx, nil, conn)
}

// Serve は、編集の client を 1 つ繋ぎ、イベントループを動かす。Run と Serve は、1 つの Server で合わせて 1 回だけ呼べる。
//
// first が nil でなければ、それを編集の client にする（1 つのプロセスの構成）。
// first が nil なら、ln で受け付けた接続のうち、最初に編集の client の Hello を送ったものを編集の client にする（server だけのプロセス）。
// ln が nil でなければ、Serve の間 ln で待ち受け、編集の client を繋いだ後に届いた接続は受け付けてすぐ閉じる（今は 2 つ目の client に応えない）。
// Serve は戻る前に ln を閉じる。
//
// 戻り値は editor.Editor.Run と同じで、:q で nil、client が接続を閉じたら editor.ErrTerminalLost、ctx の終わりで ctx.Err() である。
// client が最初のメッセージを送る前に ctx が終わっても、ctx.Err() で戻る。
// first の最初のメッセージが編集の client の Hello でなければ、ErrUnexpectedHello で戻る。
//
// 戻る前に編集の client の接続を閉じる。client はそれを見て端末を戻す。
// 拡張機能はまだ止めないので、呼び出し側は client が端末を戻すのを待ってから Close を呼ぶ（editor.Editor.Close の doc の順序）。
func (s *Server) Serve(ctx context.Context, ln net.Listener, first protocol.ServerConn) error {
	var a *acceptor
	if ln != nil {
		a = s.startAccept(ln, first != nil)
		defer a.stop()
	}
	conn, hello, err := s.firstClient(ctx, a, first)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() // 閉じる誤りは、client の側の Recv の誤りで分かる
	s.nextID++
	s.client.attach(s.nextID, conn, hello)
	err = s.ed.Run(ctx)
	s.client.finish(exitReason(ctx, err))
	return err
}

// exitReason は、イベントループが err で終わったときに、client へ知らせる終わりの理由を返す。:q などの正常な終わりは空である。
func exitReason(ctx context.Context, err error) string {
	switch {
	case err == nil, errors.Is(err, editor.ErrTerminalLost):
		// client の側が終わった。知らせる相手はもう読んでいない
		return ""
	case ctx.Err() != nil:
		return "server が止められた: " + context.Cause(ctx).Error()
	}
	return "server が誤りで終わった: " + err.Error()
}

// firstClient は、編集の client の接続と、その Hello を返す。
// first が nil でなければ first の Hello を待つ。nil なら a が受け付けた接続から、編集の client の Hello を送ったものを選ぶ。
func (s *Server) firstClient(ctx context.Context, a *acceptor, first protocol.ServerConn) (protocol.ServerConn, protocol.Hello, error) {
	if first != nil {
		h, err := handshake(ctx, first)
		if errors.Is(err, errPeerClosed) {
			// 大きさを知らせる前に client が閉じた。イベントループに ErrTerminalLost で戻らせる
			_ = first.Close()
			return nil, protocol.Hello{}, editor.ErrTerminalLost
		}
		if err != nil {
			_ = first.Close()
			return nil, protocol.Hello{}, err
		}
		return first, h, nil
	}
	if a == nil {
		return nil, protocol.Hello{}, errors.New("server: no client and no listener")
	}
	for {
		select {
		case <-ctx.Done():
			return nil, protocol.Hello{}, ctx.Err()
		case conn := <-a.candidates:
			hctx, cancel := context.WithTimeout(ctx, helloTimeout)
			h, err := handshake(hctx, conn)
			cancel()
			if err == nil {
				return conn, h, nil
			}
			// 閉じる前に次の接続を受け付けられるようにする（閉じたのを見た client がすぐ繋ぎ直しても断らない）。
			a.busy.Store(false)
			_ = conn.Close()
			if ctx.Err() != nil {
				return nil, protocol.Hello{}, ctx.Err()
			}
			s.log.Warn("client の接続を断った", "err", err)
		}
	}
}

// errPeerClosed は、handshake の間に client が接続を閉じたことを表す。
var errPeerClosed = errors.New("server: client closed before hello")

// handshake は、conn の最初のメッセージを待ち、編集の client の Hello なら返す。
// 待つ間に ctx が終わったら ctx.Err() を返す。そのとき最初のメッセージを待つ goroutine は、呼び出し側が conn を閉じると戻る。
func handshake(ctx context.Context, conn protocol.ServerConn) (protocol.Hello, error) {
	type received struct {
		m   protocol.ClientMessage
		err error
	}
	ch := make(chan received, 1)
	go func() {
		m, err := conn.Recv()
		ch <- received{m, err}
	}()
	var r received
	select {
	case r = <-ch:
	case <-ctx.Done():
		return protocol.Hello{}, ctx.Err()
	}
	if r.err != nil {
		return protocol.Hello{}, errPeerClosed
	}
	h, ok := r.m.(protocol.Hello)
	if !ok || h.Kind != protocol.KindEdit {
		return protocol.Hello{}, fmt.Errorf("%w: %#v", ErrUnexpectedHello, r.m)
	}
	return h, nil
}

// acceptor は、server のソケットで接続を受け付ける goroutine である。
type acceptor struct {
	ln net.Listener
	// busy は、編集の client が繋いでいる（か、繋ぐ手続きの途中）かである。true の間に届いた接続は閉じる。
	busy atomic.Bool
	// candidates は、busy が false のときに届いた接続を、編集の client の候補として渡す。
	candidates chan protocol.ServerConn
	done       chan struct{}
}

func (s *Server) startAccept(ln net.Listener, busy bool) *acceptor {
	a := &acceptor{ln: ln, candidates: make(chan protocol.ServerConn), done: make(chan struct{})}
	a.busy.Store(busy)
	go func() {
		defer s.client.recoverPanic()
		for {
			c, err := ln.Accept()
			if err != nil {
				return // ln を閉じた
			}
			conn := protocol.NewServerSocket(c)
			if !a.busy.CompareAndSwap(false, true) {
				s.log.Info("2 つ目の client には応えないので、接続を閉じた")
				_ = conn.Close()
				continue
			}
			select {
			case a.candidates <- conn:
			case <-a.done:
				_ = conn.Close()
				return
			}
		}
	}()
	return a
}

func (a *acceptor) stop() {
	close(a.done)
	_ = a.ln.Close() // 受け付けの goroutine を止める。閉じる誤りは扱えない
}

// Close は、拡張機能を止める（editor.Editor.Close）。Run が戻り、client が端末を戻した後に呼ぶ。
func (s *Server) Close(ctx context.Context) error {
	return s.ed.Close(ctx)
}

// clientTerm は、1 つの client との接続を editor.Terminal の口に合わせる。
// イベントループからは端末に見え、Draw と SetClipboard を client へのメッセージにする。
type clientTerm struct {
	onPanic func()
	log     *slog.Logger
	events  chan editor.TermEvent

	id   editor.ClientID
	conn protocol.ServerConn
	// width と height は、client が Hello で知らせた端末の大きさである。
	width, height int

	out outbox
}

// outbox は、client へ送るのを待つ画面とクリップボードへの書き込みの置き場である。
// イベントループが入れ、送る goroutine（clientTerm.send）が取り出す。
//
// 画面もクリップボードへの書き込みも、まだ送っていない最新の 1 つだけを置く。
// クリップボードは OSC 52 でホストのクリップボードに残るのが最後の 1 つなので、まとめても利用者から見て変わらない。
// 読まない client（端末の出力を <C-s> で止めた、固まったペイン）の間にヤンクを繰り返しても、置き場は大きくならない（PR #200 のレビューの推奨 7）。
//
// 送る順序は「クリップボード、画面」だけを気にしている。画面と他のメッセージの前後を保つ必要のあるメッセージ
// （:! の端末の受け渡し。FR-80、FR-117）を足すときは、置き場を順序を保つ列にし、画面だけを列の中の直前の画面と置き換える形に変える
// （PR #200 のレビューの推奨 4）。
type outbox struct {
	mu     sync.Mutex
	screen *ui.Screen // まだ送っていない最新の画面。nil なら無い
	clip   *string    // まだ送っていない最新のクリップボードへの書き込み。nil なら無い
	exit   string     // finish で決めた、終わりの知らせ（protocol.Exit）の理由

	wake chan struct{} // 置き場に入れたことを送る goroutine に知らせる。溜めは 1
	stop chan struct{} // Run の終わりで閉じる
	done chan struct{} // 送る goroutine が終わったら閉じる
}

// attach は、Hello を受けた conn を繋ぎ、イベントを読む goroutine と、client へ送る goroutine を起こす。
func (t *clientTerm) attach(id editor.ClientID, conn protocol.ServerConn, hello protocol.Hello) {
	t.id, t.conn = id, conn
	t.width, t.height = defaultSize(hello.Width, hello.Height)
	t.out.wake = make(chan struct{}, 1)
	t.out.stop = make(chan struct{})
	t.out.done = make(chan struct{})
	go t.read()
	go t.send()
}

// defaultWidth と defaultHeight は、client が Hello で端末の大きさを知らせなかったときの大きさである（term.Terminal.Size と同じ）。
const defaultWidth, defaultHeight = 80, 24

// defaultSize は、Hello の大きさを格子の大きさにする。知らせない（0 以下）なら既定の大きさにし、
// 上限を超えれば断らずに上限へ切り詰める（protocol.ClampSize。大きな端末でも起動できるようにする）。
func defaultSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return defaultWidth, defaultHeight
	}
	return protocol.ClampSize(w, h)
}

// finish は、イベントループが終わった後に呼ぶ。まだ送っていないクリップボードへの書き込みと、終わりの知らせ（理由 reason）を
// 送り終えるのを、flushTimeout まで待つ。画面は送らない（client はこの後に端末を戻す）。
func (t *clientTerm) finish(reason string) {
	t.out.mu.Lock()
	t.out.exit = reason
	t.out.mu.Unlock()
	close(t.out.stop)
	select {
	case <-t.out.done:
	case <-time.After(flushTimeout):
		t.log.Warn("client へのクリップボードの書き込みを送り終えないまま接続を閉じる")
	}
}

// read は、client から届いたイベントを、client の識別子を付けてイベントループへ送る。
// 接続が閉じたら events を閉じる。Hello がもう一度届いたら、無視する。
func (t *clientTerm) read() {
	defer close(t.events)
	defer t.recoverPanic()
	for {
		m, err := t.conn.Recv()
		if err != nil {
			if !errors.Is(err, protocol.ErrClosed) && !isEOF(err) {
				t.log.Error("client からの受信に失敗", "err", err)
			}
			return
		}
		ev, ok := m.(protocol.Event)
		if !ok {
			t.log.Warn("client から 2 回目の Hello が届いた。無視する")
			continue
		}
		if ev.Resize {
			if ev.Width <= 0 || ev.Height <= 0 {
				t.log.Warn("client が知らせた端末の大きさが 0 以下なので、無視する", "width", ev.Width, "height", ev.Height)
				continue
			}
			// 上限を超える大きさは、断らずに上限へ切り詰める（protocol.ClampSize）
			ev.Width, ev.Height = protocol.ClampSize(ev.Width, ev.Height)
		}
		t.events <- t.termEvent(ev)
	}
}

// send は、置き場の画面とクリップボードへの書き込みを client へ送る。
// Run が終わったら、残ったクリップボードへの書き込みと終わりの知らせを送って終わる。接続が閉じても終わる。
func (t *clientTerm) send() {
	defer close(t.out.done)
	defer t.recoverPanic()
	for {
		stopping := false
		select {
		case <-t.out.wake:
		case <-t.out.stop:
			stopping = true
		}
		t.out.mu.Lock()
		scr, clip, reason := t.out.screen, t.out.clip, t.out.exit
		t.out.screen, t.out.clip = nil, nil
		t.out.mu.Unlock()
		if clip != nil && !t.sent(t.conn.Send(protocol.SetClipboard{Text: *clip})) {
			return
		}
		if stopping {
			t.sent(t.conn.Send(protocol.Exit{Reason: reason}))
			return
		}
		if scr != nil && !t.sent(t.conn.Send(protocol.Draw{Screen: *scr})) {
			return
		}
	}
}

// sent は、送った結果 err を見て、送り続けるかを返す。接続が閉じていれば false を返す。それ以外の誤りは記録して続ける。
func (t *clientTerm) sent(err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, protocol.ErrClosed):
		return false
	}
	t.log.Error("client への送信に失敗", "err", err)
	return true
}

func isEOF(err error) bool { return errors.Is(err, io.EOF) }

func (t *clientTerm) wakeSender() {
	select {
	case t.out.wake <- struct{}{}:
	default:
	}
}

func (t *clientTerm) termEvent(ev protocol.Event) editor.TermEvent {
	return editor.TermEvent{
		Key:    ev.Key,
		Paste:  ev.Paste,
		Resize: ev.Resize,
		Width:  ev.Width,
		Height: ev.Height,
		Client: t.id,
	}
}

// recoverPanic は、server が起こす goroutine の始めに defer で呼ぶ。panic していれば onPanic を呼んでから panic を続ける（基本設計 8 章）。
func (t *clientTerm) recoverPanic() {
	if p := recover(); p != nil {
		if t.onPanic != nil {
			t.onPanic()
		}
		panic(p)
	}
}

// Input は、client から届いたイベントを送るチャネルを返す。client が接続を閉じると閉じる。
func (t *clientTerm) Input() <-chan editor.TermEvent { return t.events }

// Size は、client が Hello で知らせた端末の大きさを返す。
func (t *clientTerm) Size() (width, height int) { return t.width, t.height }

// Draw は、画面 s を client へ送る置き場に入れる。送り終えるのを待たない。
// まだ送っていない前の画面があれば、s で置き換える（client には最新の画面だけが届く）。
func (t *clientTerm) Draw(s ui.Screen) error {
	t.out.mu.Lock()
	t.out.screen = &s
	t.out.mu.Unlock()
	t.wakeSender()
	return nil
}

// SetClipboard は、text をホストのクリップボードへ送るよう client に頼む（FR-56）。送り終えるのを待たない。
// まだ送っていない前の書き込みがあれば、text で置き換える（outbox の doc）。
func (t *clientTerm) SetClipboard(text string) error {
	t.out.mu.Lock()
	t.out.clip = &text
	t.out.mu.Unlock()
	t.wakeSender()
	return nil
}
