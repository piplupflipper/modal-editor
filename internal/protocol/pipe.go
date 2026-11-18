package protocol

import (
	"io"
	"sync"
)

// 1 つのプロセスの中の接続で、送った側を待たせずに溜めておく数。
// イベントは端末の読み取りの列（internal/term）と同じ 64 にした。
// 画面は溜めても古いものから順に描くだけなので、少なくした（仮）。
const (
	pipeEventBuffer   = 64
	pipeMessageBuffer = 4
)

// Pipe は、1 つのプロセスの中で client と server をつなぐ接続の両端を返す（FR-60 の構成。基本設計 1.2）。
// チャネルでつなぎ、メッセージを写さずにそのまま渡す。
// どちらかの Close の後、相手の Recv は届いていたものを返し終えてから io.EOF を返す。
func Pipe() (ClientConn, ServerConn) {
	p := &pipe{
		events:     make(chan ClientMessage, pipeEventBuffer),
		messages:   make(chan ServerMessage, pipeMessageBuffer),
		clientDone: make(chan struct{}),
		serverDone: make(chan struct{}),
	}
	return pipeClient{p}, pipeServer{p}
}

type pipe struct {
	events   chan ClientMessage
	messages chan ServerMessage
	// clientDone と serverDone は、それぞれの側の Close で閉じる。データのチャネルは閉じない（Send と Close が並んでも panic しない）。
	clientDone, serverDone chan struct{}
	clientOnce, serverOnce sync.Once
}

type pipeClient struct{ p *pipe }

func (c pipeClient) Send(m ClientMessage) error {
	return send(c.p.events, c.p.clientDone, c.p.serverDone, m)
}

func (c pipeClient) Recv() (ServerMessage, error) {
	return recv(c.p.messages, c.p.clientDone, c.p.serverDone)
}

func (c pipeClient) Close() error {
	c.p.clientOnce.Do(func() { close(c.p.clientDone) })
	return nil
}

type pipeServer struct{ p *pipe }

func (s pipeServer) Send(m ServerMessage) error {
	return send(s.p.messages, s.p.serverDone, s.p.clientDone, m)
}

func (s pipeServer) Recv() (ClientMessage, error) {
	return recv(s.p.events, s.p.serverDone, s.p.clientDone)
}

func (s pipeServer) Close() error {
	s.p.serverOnce.Do(func() { close(s.p.serverDone) })
	return nil
}

// send は、v を ch へ送る。self（自分の Close）か peer（相手の Close）が閉じていれば、送らずに ErrClosed を返す。
func send[T any](ch chan<- T, self, peer <-chan struct{}, v T) error {
	select {
	case <-self:
		return ErrClosed
	case <-peer:
		return ErrClosed
	default:
	}
	select {
	case ch <- v:
		return nil
	case <-self:
		return ErrClosed
	case <-peer:
		return ErrClosed
	}
}

// recv は、ch から 1 件受ける。self が閉じていれば ErrClosed を返す。
// peer が閉じていても、ch に残っているものを先に返し、空になってから io.EOF を返す。
func recv[T any](ch <-chan T, self, peer <-chan struct{}) (T, error) {
	var zero T
	select {
	case <-self:
		return zero, ErrClosed
	default:
	}
	select {
	case v := <-ch:
		return v, nil
	case <-self:
		return zero, ErrClosed
	case <-peer:
		select {
		case v := <-ch:
			return v, nil
		default:
			return zero, io.EOF
		}
	}
}
