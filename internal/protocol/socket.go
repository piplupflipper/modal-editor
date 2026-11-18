package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// ソケットの上のメッセージのメソッドの名前（仮）。どれも JSON-RPC 2.0 の通知で、応答を求めない。
const (
	methodHello     = "client/hello"  // client → server。Hello
	methodEvent     = "client/event"  // client → server。Event
	methodDraw      = "screen/draw"   // server → client。Draw（格子の差分。screen.go）
	methodClipboard = "clipboard/set" // server → client。SetClipboard
	methodExit      = "server/exit"   // server → client。Exit（接続を閉じる前の、終わりの知らせ）
)

// maxSocketMessageSize は、ソケットから受ける 1 件のメッセージの本文の上限である（拡張機能との接続と同じ）。
const maxSocketMessageSize = rpc.DefaultMaxMessageSize

// DialTimeout は、Dial が server のソケットに繋ぐのを待つ上限である。
const DialTimeout = 5 * time.Second

// NewServerSocket は、server が受け付けたソケットの接続 c を、ServerConn にする（基本設計 1.2）。
//
// メッセージは JSON-RPC 2.0 の通知で、区切りは拡張機能との接続（基本設計 5.3）と同じ Content-Length のヘッダーである。
// 画面（Draw）は、前回送った格子との差分にして送る（screen.go）。
// Close は c を閉じる。
func NewServerSocket(c net.Conn) ServerConn {
	return &serverSocket{s: newSocket(c)}
}

// NewClientSocket は、server のソケットに繋いだ接続 c を、ClientConn にする。形は NewServerSocket と同じである。
func NewClientSocket(c net.Conn) ClientConn {
	return &clientSocket{s: newSocket(c)}
}

// Dial は、path の server のソケットに繋ぎ、ClientConn を返す。
func Dial(path string) (ClientConn, error) {
	c, err := net.DialTimeout("unix", path, DialTimeout)
	if err != nil {
		return nil, fmt.Errorf("protocol: dial: %w", err)
	}
	return NewClientSocket(c), nil
}

type socket struct {
	c net.Conn
	r *bufio.Reader

	// wmu は、書き込みと、書き込みの順に覚える状態（screenEncoder）を守る。
	wmu sync.Mutex
	// rmu は、読み取りと、読み取りの順に覚える状態（screenDecoder）を守る。
	rmu sync.Mutex

	closed    atomic.Bool
	closeOnce sync.Once
}

func newSocket(c net.Conn) *socket {
	return &socket{c: c, r: bufio.NewReader(c)}
}

// wireMessage は、送る通知と受ける通知の形である。
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// send は、method の通知を params で送る。呼び出し側が wmu を持つ。
func (s *socket) send(method string, params any) error {
	if s.closed.Load() {
		return ErrClosed
	}
	p, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("protocol: %s: %w", method, err)
	}
	body, err := json.Marshal(wireMessage{JSONRPC: rpc.Version, Method: method, Params: p})
	if err != nil {
		return fmt.Errorf("protocol: %s: %w", method, err)
	}
	if err := rpc.WriteMessage(s.c, body); err != nil {
		if s.closed.Load() || peerGone(err) {
			return ErrClosed
		}
		return fmt.Errorf("protocol: send: %w", err)
	}
	return nil
}

// recv は、通知を 1 件読む。呼び出し側が rmu を持つ。
func (s *socket) recv() (wireMessage, error) {
	if s.closed.Load() {
		return wireMessage{}, ErrClosed
	}
	body, err := rpc.ReadMessage(s.r, maxSocketMessageSize)
	if err != nil {
		switch {
		case s.closed.Load():
			return wireMessage{}, ErrClosed
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), peerGone(err):
			return wireMessage{}, io.EOF
		}
		return wireMessage{}, fmt.Errorf("protocol: recv: %w", err)
	}
	var m wireMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return wireMessage{}, fmt.Errorf("protocol: recv: %w", err)
	}
	return m, nil
}

func (s *socket) close() error {
	var err error
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		err = s.c.Close()
	})
	if err != nil {
		return fmt.Errorf("protocol: close: %w", err)
	}
	return nil
}

// peerGone は、err が、相手が接続を閉じたことによる書き込みか読み取りの誤りかを返す。
func peerGone(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed)
}

// decodeParams は、m の引数を v に読む。
func decodeParams(m wireMessage, v any) error {
	if err := json.Unmarshal(m.Params, v); err != nil {
		return fmt.Errorf("protocol: %s: %w", m.Method, err)
	}
	return nil
}

type serverSocket struct {
	s   *socket
	enc screenEncoder // wmu で守る
}

func (ss *serverSocket) Send(m ServerMessage) error {
	ss.s.wmu.Lock()
	defer ss.s.wmu.Unlock()
	switch m := m.(type) {
	case Draw:
		return ss.s.send(methodDraw, ss.enc.encode(m.Screen))
	case SetClipboard:
		return ss.s.send(methodClipboard, wireClipboard(m))
	case Exit:
		return ss.s.send(methodExit, wireExit(m))
	}
	return fmt.Errorf("protocol: unknown server message %T", m)
}

func (ss *serverSocket) Recv() (ClientMessage, error) {
	ss.s.rmu.Lock()
	defer ss.s.rmu.Unlock()
	for {
		m, err := ss.s.recv()
		if err != nil {
			return nil, err
		}
		switch m.Method {
		case methodHello:
			var h wireHello
			if err := decodeParams(m, &h); err != nil {
				return nil, err
			}
			return Hello(h), nil
		case methodEvent:
			var ev wireEvent
			if err := decodeParams(m, &ev); err != nil {
				return nil, err
			}
			return ev.event(), nil
		}
		// 知らないメソッドの通知は読み飛ばす（JSON-RPC 2.0 の通知と同じく応答しない）。
		// 新しい client が古い server に繋いでも、接続を切らずに編集を続けられるようにする。
	}
}

func (ss *serverSocket) Close() error { return ss.s.close() }

type clientSocket struct {
	s   *socket
	dec screenDecoder // rmu で守る
}

func (cs *clientSocket) Send(m ClientMessage) error {
	cs.s.wmu.Lock()
	defer cs.s.wmu.Unlock()
	switch m := m.(type) {
	case Hello:
		return cs.s.send(methodHello, wireHello(m))
	case Event:
		return cs.s.send(methodEvent, toWireEvent(m))
	}
	return fmt.Errorf("protocol: unknown client message %T", m)
}

func (cs *clientSocket) Recv() (ServerMessage, error) {
	cs.s.rmu.Lock()
	defer cs.s.rmu.Unlock()
	for {
		m, err := cs.s.recv()
		if err != nil {
			return nil, err
		}
		switch m.Method {
		case methodDraw:
			var d wireDraw
			if err := decodeParams(m, &d); err != nil {
				return nil, err
			}
			scr, err := cs.dec.decode(d)
			if err != nil {
				return nil, err
			}
			return Draw{Screen: scr}, nil
		case methodClipboard:
			var c wireClipboard
			if err := decodeParams(m, &c); err != nil {
				return nil, err
			}
			return SetClipboard(c), nil
		case methodExit:
			var e wireExit
			if err := decodeParams(m, &e); err != nil {
				return nil, err
			}
			return Exit(e), nil
		}
		// 知らないメソッドの通知は読み飛ばす（serverSocket.Recv と同じ）。
	}
}

func (cs *clientSocket) Close() error { return cs.s.close() }

type wireHello struct {
	Kind     ClientKind `json:"kind"`
	Width    int        `json:"width,omitempty"`
	Height   int        `json:"height,omitempty"`
	Files    []string   `json:"files,omitempty"`
	ReadOnly bool       `json:"readOnly,omitempty"`
}

type wireClipboard struct {
	Text string `json:"text"`
}

type wireExit struct {
	Reason string `json:"reason,omitempty"`
}

type wireKey struct {
	Code editcmd.Code `json:"code"`
	Rune rune         `json:"rune,omitempty"`
	Mod  editcmd.Mod  `json:"mod,omitempty"`
}

type wireEvent struct {
	Key    wireKey `json:"key,omitzero"`
	Paste  string  `json:"paste,omitempty"`
	Resize bool    `json:"resize,omitempty"`
	Width  int     `json:"width,omitempty"`
	Height int     `json:"height,omitempty"`
}

func toWireEvent(ev Event) wireEvent {
	return wireEvent{
		Key:   wireKey{Code: ev.Key.Code, Rune: ev.Key.Rune, Mod: ev.Key.Mod},
		Paste: ev.Paste, Resize: ev.Resize, Width: ev.Width, Height: ev.Height,
	}
}

func (w wireEvent) event() Event {
	return Event{
		Key:   editcmd.Key{Code: w.Key.Code, Rune: w.Key.Rune, Mod: w.Key.Mod},
		Paste: w.Paste, Resize: w.Resize, Width: w.Width, Height: w.Height,
	}
}
