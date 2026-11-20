package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// ソケットの上のメッセージのメソッドの名前（仮）。どれも JSON-RPC 2.0 の通知で、応答を求めない。
const (
	methodHello     = "client/hello"  // client → server。Hello
	methodStdin     = "client/stdin"  // client → server。Hello の Stdin の塊（Hello の通知の直後に続ける）
	methodEvent     = "client/event"  // client → server。Event
	methodDraw      = "screen/draw"   // server → client。Draw（格子の差分。screen.go）
	methodClipboard = "clipboard/set" // server → client。SetClipboard
	methodExit      = "server/exit"   // server → client。Exit（接続を閉じる前の、終わりの知らせ）

	// 端末の受け渡し（FR-79、FR-80、FR-117）。問いと応答の組は、JSON-RPC の要求と応答ではなく、id を引数に持つ通知の組にした（仮）。
	methodTerminalRun      = "terminal/run"      // server → client。TerminalRun
	methodTerminalReclaim  = "terminal/reclaim"  // server → client。TerminalReclaim
	methodTerminalRelease  = "terminal/release"  // server → client。TerminalRelease
	methodTerminalDone     = "terminal/done"     // client → server。TerminalDone
	methodTerminalReleased = "terminal/released" // client → server。TerminalReleased
)

// maxSocketMessageSize は、ソケットから受ける 1 件のメッセージの本文の上限である（拡張機能との接続と同じ）。
const maxSocketMessageSize = rpc.DefaultMaxMessageSize

// stdinChunkSize は、Hello の Stdin を分けて送るときの、1 件の通知に載せるバイト数の上限である（仮）。
// base64 で約 1.4 MB になり、maxSocketMessageSize（64 MiB）より十分小さい。1 件ごとの写しを小さく保ち、送る側と受ける側のメモリを中身の大きさの程度に抑える。
const stdinChunkSize = 1 << 20

// maxStdinPrealloc は、Hello の Stdin を受けるときに、client が知らせた大きさで先に取っておくバイト数の上限である（仮）。
// これ以下の中身は、受けた塊をつなぐときに写し直さず、中身の大きさだけのメモリで受ける。
// 壊れた client が巨大な大きさを知らせても、受けた分を超えて取っておくのはこの大きさまでにする（server が落ちると、他の client の未保存の変更を失う）。
const maxStdinPrealloc = 1 << 30

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
	case TerminalRun:
		return ss.s.send(methodTerminalRun, toWireRun(m))
	case TerminalReclaim:
		return ss.s.send(methodTerminalReclaim, wireID(m))
	case TerminalRelease:
		return ss.s.send(methodTerminalRelease, wireID(m))
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
			var h helloMessage
			if err := decodeParams(m, &h); err != nil {
				return nil, err
			}
			hello := Hello(h.wireHello)
			if h.StdinFollows {
				text, err := ss.s.recvStdin()
				if err != nil {
					return nil, err
				}
				hello.Stdin = &text
			}
			return hello, nil
		case methodEvent:
			var ev wireEvent
			if err := decodeParams(m, &ev); err != nil {
				return nil, err
			}
			return ev.event(), nil
		case methodTerminalDone:
			var d wireDone
			if err := decodeParams(m, &d); err != nil {
				return nil, err
			}
			return TerminalDone(d), nil
		case methodTerminalReleased:
			var d wireID
			if err := decodeParams(m, &d); err != nil {
				return nil, err
			}
			return TerminalReleased(d), nil
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
		if err := cs.s.send(methodHello, helloMessage{wireHello: wireHello(m), StdinFollows: m.Stdin != nil}); err != nil || m.Stdin == nil {
			return err
		}
		return cs.s.sendStdin(*m.Stdin)
	case Event:
		return cs.s.send(methodEvent, toWireEvent(m))
	case TerminalDone:
		return cs.s.send(methodTerminalDone, wireDone(m))
	case TerminalReleased:
		return cs.s.send(methodTerminalReleased, wireID(m))
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
		case methodTerminalRun:
			var r wireTerminalRun
			if err := decodeParams(m, &r); err != nil {
				return nil, err
			}
			return r.run(), nil
		case methodTerminalReclaim:
			var d wireID
			if err := decodeParams(m, &d); err != nil {
				return nil, err
			}
			return TerminalReclaim(d), nil
		case methodTerminalRelease:
			var d wireID
			if err := decodeParams(m, &d); err != nil {
				return nil, err
			}
			return TerminalRelease(d), nil
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
	// Stdin は、Hello の通知には載せず、続く methodStdin の通知で送る（helloMessage.StdinFollows）。
	// Hello と型の変換で行き来するので、Hello と同じ位置に置く。
	Stdin    *string  `json:"-"`
	Pane     string   `json:"pane,omitempty"`
	WorkDir  string   `json:"workDir,omitempty"`
	Env      []string `json:"env,omitempty"`
	Parent   uint64   `json:"parent,omitempty"`
	Features []string `json:"features,omitempty"`
	View     uint64   `json:"view,omitempty"`
}

// helloMessage は、ソケットの上の Hello の通知の形である。
// StdinFollows は、Hello.Stdin が nil でなく、この通知の直後に methodStdin の通知が続くことを表す（FR-158）。
type helloMessage struct {
	wireHello
	StdinFollows bool `json:"stdin,omitempty"`
}

// wireStdin は、Hello.Stdin の塊 1 つである。Rest は、この塊の後に続くバイト数で、0 なら最後の塊である。
// 中身が空でも、Rest が 0 の空の塊を 1 つ送る。
type wireStdin struct {
	Data []byte `json:"data,omitempty"`
	Rest int64  `json:"rest,omitempty"`
}

// sendStdin は、text を stdinChunkSize ごとの methodStdin の通知に分けて送る。呼び出し側が wmu を持つ（Hello の通知と間を空けずに続ける）。
func (s *socket) sendStdin(text string) error {
	for i := 0; ; {
		j := min(i+stdinChunkSize, len(text))
		if err := s.send(methodStdin, wireStdin{Data: []byte(text[i:j]), Rest: int64(len(text) - j)}); err != nil {
			return err
		}
		if j == len(text) {
			return nil
		}
		i = j
	}
}

// recvStdin は、Hello の通知に続く methodStdin の通知を最後の塊まで読み、つないだ中身を返す。呼び出し側が rmu を持つ。
// 最初の塊で知らせた大きさ（maxStdinPrealloc まで）を先に取っておき、受けた塊を写し直さずにつなぐ。
// 塊の間に別の通知が来るか、Rest が前の塊と合わなければ誤りにする。
func (s *socket) recvStdin() (string, error) {
	var b strings.Builder
	rest := int64(-1) // 最初の塊の前は、続くバイト数を知らない
	for {
		m, err := s.recv()
		if err != nil {
			return "", err
		}
		if m.Method != methodStdin {
			return "", fmt.Errorf("protocol: %s: got %q before the end of stdin", methodStdin, m.Method)
		}
		var w wireStdin
		if err := decodeParams(m, &w); err != nil {
			return "", err
		}
		n := int64(len(w.Data))
		if w.Rest < 0 || w.Rest > math.MaxInt64-n || (rest >= 0 && n+w.Rest != rest) {
			return "", fmt.Errorf("protocol: %s: size mismatch", methodStdin)
		}
		if rest < 0 {
			b.Grow(int(min(n+w.Rest, maxStdinPrealloc)))
		}
		b.Write(w.Data)
		rest = w.Rest
		if rest == 0 {
			return b.String(), nil
		}
	}
}

type wireID struct {
	ID uint64 `json:"id"`
}

type wireDone struct {
	ID     uint64 `json:"id"`
	Status int    `json:"status,omitempty"`
	Err    string `json:"err,omitempty"`
}

type wireTerminalRun struct {
	ID       uint64        `json:"id"`
	Commands []wireCommand `json:"commands"`
	Env      []string      `json:"env,omitempty"`
	Prompt   bool          `json:"prompt,omitempty"`
}

// wireCommand は、ソケットの上の TerminalCommand である。Input は JSON の []byte と同じく base64 の文字列にする。
type wireCommand struct {
	Shell         string   `json:"shell,omitempty"`
	Argv          []string `json:"argv,omitempty"`
	Input         []byte   `json:"input,omitempty"`
	PipeInput     bool     `json:"pipeInput,omitempty"`
	DiscardOutput bool     `json:"discardOutput,omitempty"`
}

func toWireRun(r TerminalRun) wireTerminalRun {
	w := wireTerminalRun{ID: r.ID, Env: r.Env, Prompt: r.Prompt, Commands: make([]wireCommand, len(r.Commands))}
	for i, c := range r.Commands {
		w.Commands[i] = wireCommand(c)
	}
	return w
}

func (w wireTerminalRun) run() TerminalRun {
	r := TerminalRun{ID: w.ID, Env: w.Env, Prompt: w.Prompt, Commands: make([]TerminalCommand, len(w.Commands))}
	for i, c := range w.Commands {
		r.Commands[i] = TerminalCommand(c)
	}
	return r
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
