package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/piplupflipper/modal-editor/internal/version"
)

// MCP の版（パッケージの doc の「MCP の版」）。
const (
	// modernVersion は、リクエストごとに _meta で版を示す版である。
	// 仕様: https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning
	modernVersion = "2026-07-28"
	// legacyVersion は、initialize で版を取り決める版のうち、この拡張機能が扱うものである。
	// 仕様: https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle
	legacyVersion = "2025-11-25"
)

// serverName は、serverInfo の name である。
const serverName = "modal-editor"

// serverInstructions は、言語モデル向けの使い方の説明である（server/discover と initialize の instructions）。
const serverInstructions = "Tools to read and edit the buffers of a running modal-editor instance. " +
	"Positions are 1-based: line counts from 1, column counts bytes from the start of the line from 1. " +
	"Call read_buffer to get the text and its version before edit_buffer; " +
	"edit_buffer is rejected when the buffer has changed since that version."

// JSON-RPC と MCP のエラーコード。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
	// codeUnsupportedProtocolVersion は、2026-07-28 の UnsupportedProtocolVersionError である。
	codeUnsupportedProtocolVersion = -32022
)

// _meta の予約されたキー（2026-07-28 の basic/index の「_meta」）。
const (
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaServerInfo         = "io.modelcontextprotocol/serverInfo"
)

// maxMessageSize は、1 行（1 つのメッセージ）の大きさの上限である（仮）。
// 超えた行は読み捨て、接続を閉じる。改行の無い大きな入力で拡張機能のメモリーを使い果たさないためである。
const maxMessageSize = 64 << 20

// wireMessage は、クライアントから届いた 1 つの JSON-RPC のメッセージである。
type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// wireResponse は、クライアントへ送る応答である。
type wireResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError は、JSON-RPC のエラーである。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// implementation は、MCP の Implementation（serverInfo）である。
type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func serverInfo() implementation { return implementation{Name: serverName, Version: version.String()} }

// mcpSession は、1 本の接続の上の MCP のサーバーの状態である。
//
// 2026-07-28 の版は状態を持たない（リクエストごとに _meta で版を示す）。
// 2025-11-25 の版のクライアントは initialize を最初に送り、以後のリクエストに _meta の版を載せない。
// このため、initialize を受け取った接続だけを legacy として覚え、_meta の版の無いリクエストを受け付ける。
// initialize を受け取っていない接続で、_meta の版の無いリクエストは、2026-07-28 の規則どおり -32602 で拒む。
type mcpSession struct {
	tools *toolbox
	w     io.Writer
	log   *log.Logger

	wmu sync.Mutex

	mu     sync.Mutex
	legacy bool
	// inflight は、処理中のリクエストの取り消しの関数である。キーは ID の JSON の表記である。
	inflight map[string]context.CancelFunc
}

// serveMCP は、conn の上で MCP のサーバーとして振る舞い、conn の読み取りが終わるまで戻らない。
// conn は、MCP の stdio と同じく、1 行に 1 つの JSON-RPC のメッセージを書く改行区切りの接続である。
// 接続の受け取り方（ソケットの待ち受け、引き継いだファイル記述子）には依らない。
//
// 読み取りが EOF になったら、処理中のリクエストの応答を書き終えてから conn を閉じて nil を返す。
// 中継コマンドは標準入力の EOF で書き込み側だけを閉じ、最後の応答を待つためである（cmd/modal-editor-mcp-bridge の bridge）。
// ctx が終わったら、処理中のリクエストを取り消し、conn を閉じて戻る。
//
// リクエストは 1 件ずつ別の goroutine で処理するので、時間のかかるツールの呼び出しが、後のリクエストと取り消しの通知を止めない。
//
// req: FR-32
func serveMCP(ctx context.Context, conn io.ReadWriteCloser, tools *toolbox, logger *log.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	s := &mcpSession{tools: tools, w: conn, log: logger, inflight: map[string]context.CancelFunc{}}
	var wg sync.WaitGroup
	r := bufio.NewReader(conn)
	var readErr error
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			s.receive(ctx, &wg, line)
		}
		if err != nil {
			readErr = err
			break
		}
	}
	wg.Wait()
	_ = conn.Close()
	if errors.Is(readErr, io.EOF) || ctx.Err() != nil {
		return nil
	}
	return readErr
}

// readLine は、改行までの 1 行を読む。maxMessageSize を超えたら誤りを返す。
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxMessageSize {
			return nil, fmt.Errorf("message exceeds %d bytes", maxMessageSize)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return buf, err
	}
}

// receive は、1 行のメッセージを解釈し、リクエストなら別の goroutine で処理する。
func (s *mcpSession) receive(ctx context.Context, wg *sync.WaitGroup, line []byte) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// 2025-06-18 以降の版は JSON-RPC のバッチを扱わない。
		s.send(wireResponse{ID: json.RawMessage("null"), Error: &rpcError{Code: codeInvalidRequest, Message: "batch requests are not supported"}})
		return
	}
	var m wireMessage
	if err := json.Unmarshal(trimmed, &m); err != nil {
		s.send(wireResponse{ID: json.RawMessage("null"), Error: &rpcError{Code: codeParseError, Message: "parse error: " + err.Error()}})
		return
	}
	isRequest := len(m.ID) > 0
	switch {
	case m.Method == "" && isRequest:
		// クライアントからの応答。このサーバーはリクエストを送らないので捨てる。
		return
	case m.JSONRPC != "2.0" || m.Method == "" || (isRequest && !validID(m.ID)):
		id := m.ID
		if !isRequest || !validID(id) {
			id = json.RawMessage("null")
		}
		s.send(wireResponse{ID: id, Error: &rpcError{Code: codeInvalidRequest, Message: "invalid request"}})
		return
	case !isRequest:
		s.notification(&m)
		return
	}

	rctx, cancel := context.WithCancel(ctx)
	key := string(normalizeID(m.ID))
	s.mu.Lock()
	if _, dup := s.inflight[key]; dup {
		s.mu.Unlock()
		cancel()
		s.send(wireResponse{ID: m.ID, Error: &rpcError{Code: codeInvalidRequest, Message: "duplicate request id " + key}})
		return
	}
	s.inflight[key] = cancel
	s.mu.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		result, rerr := s.request(rctx, &m)
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		cancelled := rctx.Err() != nil
		cancel()
		if cancelled && ctx.Err() == nil {
			// 取り消されたリクエストには何も送らない（MCP の Cancellation）。
			return
		}
		resp := wireResponse{ID: m.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		s.send(resp)
	}()
}

// validID は、ID が文字列か整数であることを確かめる。MCP は null の ID を許さない。
func validID(id json.RawMessage) bool {
	var v any
	dec := json.NewDecoder(bytes.NewReader(id))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	switch v := v.(type) {
	case string:
		return true
	case json.Number:
		_, err := v.Int64()
		return err == nil
	}
	return false
}

// notification は、クライアントからの通知を処理する。知らない通知は捨てる。
func (s *mcpSession) notification(m *wireMessage) {
	if m.Method != "notifications/cancelled" {
		// notifications/initialized などは、何もしなくてよい。
		return
	}
	var p struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil || len(p.RequestID) == 0 {
		return
	}
	key := string(normalizeID(p.RequestID))
	s.mu.Lock()
	cancel := s.inflight[key]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// normalizeID は、ID の JSON の表記から余分な空白を除き、inflight のキーと比べられるようにする。
func normalizeID(id json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, id); err != nil {
		return id
	}
	return buf.Bytes()
}

func (s *mcpSession) send(resp wireResponse) {
	resp.JSONRPC = "2.0"
	if len(resp.ID) > 0 {
		resp.ID = normalizeID(resp.ID)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		s.logf("marshal response: %v", err)
		b, _ = json.Marshal(wireResponse{JSONRPC: "2.0", ID: resp.ID, Error: &rpcError{Code: codeInternalError, Message: "internal error"}})
	}
	// json.Marshal は文字列の中の改行を \n にするので、b は改行を含まない。
	b = append(b, '\n')
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.w.Write(b); err != nil {
		s.logf("write response: %v", err)
	}
}

func (s *mcpSession) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Printf(format, args...)
	}
}

// requestMeta は、リクエストの params._meta のうち、版の取り決めに使う項目である。
type requestMeta struct {
	ProtocolVersion    *string         `json:"io.modelcontextprotocol/protocolVersion"`
	ClientCapabilities json.RawMessage `json:"io.modelcontextprotocol/clientCapabilities"`
}

// request は、1 件のリクエストを処理し、result か誤りを返す。
func (s *mcpSession) request(ctx context.Context, m *wireMessage) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		return s.initialize(m.Params)
	case "ping":
		return map[string]any{}, nil
	}

	modern, rerr := s.checkVersion(m.Params)
	if rerr != nil {
		return nil, rerr
	}
	var result map[string]any
	switch m.Method {
	case "server/discover":
		result = map[string]any{
			"supportedVersions": []string{modernVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      serverInstructions,
		}
	case "tools/list":
		result = map[string]any{"tools": s.tools.list()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "tools/call: params must have a tool name"}
		}
		res, rerr := s.tools.call(ctx, p.Name, p.Arguments)
		if rerr != nil {
			return nil, rerr
		}
		result = res
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + m.Method}
	}
	if modern {
		result["resultType"] = "complete"
		result["_meta"] = map[string]any{metaServerInfo: serverInfo()}
	}
	return result, nil
}

// checkVersion は、リクエストの _meta の版を確かめ、2026-07-28 の規則で処理するか（modern）を返す。
//
//   - _meta の版が 2026-07-28 なら modern。clientCapabilities が無ければ -32602
//   - _meta の版が他の値なら、initialize で 2025-11-25 を取り決めた接続でその版なら legacy、それ以外は -32022
//   - _meta の版が無ければ、initialize を受け取った接続なら legacy、それ以外は -32602
func (s *mcpSession) checkVersion(params json.RawMessage) (modern bool, _ *rpcError) {
	var p struct {
		Meta requestMeta `json:"_meta"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return false, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
	}
	s.mu.Lock()
	legacy := s.legacy
	s.mu.Unlock()
	v := p.Meta.ProtocolVersion
	switch {
	case v == nil && legacy:
		return false, nil
	case v == nil:
		return false, &rpcError{Code: codeInvalidParams, Message: "missing _meta." + metaProtocolVersion}
	case *v == modernVersion:
		if len(p.Meta.ClientCapabilities) == 0 || string(p.Meta.ClientCapabilities) == "null" {
			return false, &rpcError{Code: codeInvalidParams, Message: "missing _meta." + metaClientCapabilities}
		}
		return true, nil
	case *v == legacyVersion && legacy:
		return false, nil
	}
	return false, &rpcError{
		Code:    codeUnsupportedProtocolVersion,
		Message: "Unsupported protocol version",
		Data:    map[string]any{"supported": []string{modernVersion}, "requested": *v},
	}
}

// initialize は、2025-11-25 までの版の初期化を処理する。
// 求められた版が 2025-11-25 ならその版を、他の版なら 2025-11-25 を返す（2025-11-25 の Lifecycle の Version Negotiation）。
// クライアントは返した版を扱えなければ切断する。
func (s *mcpSession) initialize(params json.RawMessage) (any, *rpcError) {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.ProtocolVersion == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "initialize: params must have protocolVersion"}
	}
	s.mu.Lock()
	s.legacy = true
	s.mu.Unlock()
	return map[string]any{
		"protocolVersion": legacyVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      serverInfo(),
		"instructions":    serverInstructions,
	}, nil
}
