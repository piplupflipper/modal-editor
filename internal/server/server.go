// Package server は、編集の状態を持つ側（server）である（基本設計 1.2、2 章。FR-58 の 1 段目）。
//
// server は internal/editor のイベントループを持ち、client との接続（protocol.ServerConn）から届いたキーをイベントループへ渡し、
// イベントループが組み立てた画面とクリップボードへの書き込みを client へ送る。
// 端末は持たない。端末の raw モード、キーの解読、画面の出力は client（internal/client）が持つ。
//
// # 段階 3 の 1 段目の範囲
//
// 今は、1 つのプロセスの中で編集の client 1 つだけを繋ぐ（FR-60 の構成。基本設計 1.2）。
// ソケットの待ち受け、複数の client、client ごとのタブの並びは後の段で足す。
// それに備えて、client ごとに識別子（editor.ClientID）を割り当て、キーのイベントに付けてイベントループへ渡す（基本設計 2.1）。
//
// # 並行処理
//
// 編集の状態は、Run を呼んだ goroutine（イベントループ）だけが触る（基本設計 2.1）。
// client の接続の読み取りは別の goroutine で行い、イベントをチャネルでイベントループへ送る。
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

	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// defaultWidth と defaultHeight は、client が最初に端末の大きさを知らせなかったときの大きさである（term.Terminal.Size と同じ）。
const defaultWidth, defaultHeight = 80, 24

// Server は、編集の状態を持つ側である。New で作り、Open で起動の引数のファイルを開き、Run で client を繋いで動かし、Close で拡張機能を止める。
type Server struct {
	ed *editor.Editor
	// client は、繋いだ編集の client である。今は 1 つだけで、Run で繋ぐ。
	client *clientTerm
	nextID editor.ClientID
}

// New は、cfg でイベントループ（editor.Editor）を作り、Server を返す。
// cfg.OnPanic は、server が起こす goroutine（client の接続の読み取り）の panic でも呼ぶ。
func New(cfg editor.Config) (*Server, error) {
	t := &clientTerm{onPanic: cfg.OnPanic, events: make(chan editor.TermEvent)}
	ed, err := editor.New(t, cfg)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return &Server{ed: ed, client: t}, nil
}

// Open は、path のファイルを開き、カーソルのあるバッファにする（editor.Editor.Open）。Run の前に呼ぶ。
func (s *Server) Open(path string) error {
	return s.ed.Open(path)
}

// Run は、conn を編集の client として繋ぎ、イベントループを動かす。1 つの Server で 1 回だけ呼べる。
// 戻り値は editor.Editor.Run と同じで、:q で nil、client が接続を閉じたら editor.ErrTerminalLost、ctx の終わりで ctx.Err() である。
// client が最初のメッセージ（端末の大きさ）を送る前に ctx が終わっても、ctx.Err() で戻る。
//
// 戻る前に conn を閉じる。client はそれを見て端末を戻す。
// 拡張機能はまだ止めないので、呼び出し側は client が端末を戻すのを待ってから Close を呼ぶ（editor.Editor.Close の doc の順序）。
func (s *Server) Run(ctx context.Context, conn protocol.ServerConn) error {
	defer func() { _ = conn.Close() }() // 閉じる誤りは、client の側の Recv の誤りで分かる
	s.nextID++
	if err := s.client.attach(ctx, s.nextID, conn); err != nil {
		return err
	}
	return s.ed.Run(ctx)
}

// Close は、拡張機能を止める（editor.Editor.Close）。Run が戻り、client が端末を戻した後に呼ぶ。
func (s *Server) Close(ctx context.Context) error {
	return s.ed.Close(ctx)
}

// clientTerm は、1 つの client との接続を editor.Terminal の口に合わせる。
// イベントループからは端末に見え、Draw と SetClipboard を client へのメッセージにする。
type clientTerm struct {
	onPanic func()
	events  chan editor.TermEvent

	id   editor.ClientID
	conn protocol.ServerConn
	enc  protocol.ScreenEncoder
	// width と height は、client が接続の最初に知らせた端末の大きさである。
	width, height int
}

// attach は、conn を繋ぐ。最初のイベントで端末の大きさを受け取り、残りのイベントを読む goroutine を起こす。
// 最初のイベントが大きさでなければ、既定の大きさにして、そのイベントもイベントループへ渡す。
// 最初のイベントを待つ間に ctx が終わったら、ctx.Err() を返す（client が何も送らなくても Run が戻れるようにする）。
// そのとき最初のイベントを待つ goroutine は、呼び出し側（Run）が conn を閉じると戻る。
func (t *clientTerm) attach(ctx context.Context, id editor.ClientID, conn protocol.ServerConn) error {
	t.id, t.conn = id, conn
	t.width, t.height = defaultWidth, defaultHeight
	type received struct {
		ev  protocol.Event
		err error
	}
	firstCh := make(chan received, 1)
	go func() {
		defer t.recoverPanic()
		ev, err := conn.Recv()
		firstCh <- received{ev, err}
	}()
	var r received
	select {
	case r = <-firstCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	if r.err != nil {
		// 大きさを知らせる前に client が閉じた。誤りにはせず、イベントループに ErrTerminalLost で戻らせる
		close(t.events)
		return nil //nolint:nilerr // client の切断は Run の戻り値の ErrTerminalLost で知らせる
	}
	first := r.ev
	var pending []protocol.Event
	if first.Resize && first.Width > 0 && first.Height > 0 {
		t.width, t.height = first.Width, first.Height
	} else {
		pending = append(pending, first)
	}
	go t.read(pending)
	return nil
}

// read は、pending と、client から届いたイベントを、client の識別子を付けてイベントループへ送る。
// 接続が閉じたら events を閉じる。
func (t *clientTerm) read(pending []protocol.Event) {
	defer close(t.events)
	defer t.recoverPanic()
	for _, ev := range pending {
		t.events <- t.termEvent(ev)
	}
	for {
		ev, err := t.conn.Recv()
		if err != nil {
			return
		}
		t.events <- t.termEvent(ev)
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

// Size は、client が接続の最初に知らせた端末の大きさを返す。
func (t *clientTerm) Size() (width, height int) { return t.width, t.height }

// Draw は、画面 s を client へ送る。client が接続を閉じた後は、送らずに nil を返す（イベントループは間もなく ErrTerminalLost で戻る）。
func (t *clientTerm) Draw(s ui.Screen) error {
	return ignoreClosed(t.conn.Send(t.enc.Encode(s)))
}

// SetClipboard は、text をホストのクリップボードへ送るよう client に頼む（FR-56）。
// client が接続を閉じた後は、送らずに nil を返す。
func (t *clientTerm) SetClipboard(text string) error {
	return ignoreClosed(t.conn.Send(protocol.SetClipboard{Text: text}))
}

func ignoreClosed(err error) error {
	if errors.Is(err, protocol.ErrClosed) {
		return nil
	}
	return err
}
