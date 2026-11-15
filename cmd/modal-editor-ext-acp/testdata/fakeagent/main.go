// Command fakeagent は、modal-editor-ext-acp のテストのための、ACP を話す偽のエージェントである。
//
// 標準入出力で、1 件を 1 行にした JSON-RPC 2.0 を話す（ACP の Transports）。
// session/prompt のプロンプトの本文を JSON の操作として読み、操作ごとに決まった更新とリクエストを送る。
// 結果は agent_message_chunk の本文として返すので、テストは acp の欄の内容で確かめる。
//
// 操作（op）は次のとおりである。
//
//   - hello: 本文の塊、tool_call、tool_call_update、plan を送る
//   - session: session/new で受け取った cwd と mcpServers と、initialize の clientCapabilities を返す
//   - permission: session/request_permission を送り、結果を返す
//   - read: fs/read_text_file を送り、結果を返す
//   - write: fs/write_text_file を送り、結果を返す
//   - hang: session/cancel が届くまで応答しない
//   - crash: 終了コード 3 で終わる
//   - pid: 自分のプロセス ID を返す
//
// session/cancel が届いた後に終わったプロンプトは、stopReason を cancelled で返す。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type op struct {
	Op      string `json:"op"`
	Path    string `json:"path,omitempty"`
	Line    *int   `json:"line,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
	Content string `json:"content,omitempty"`
}

type agent struct {
	wmu sync.Mutex
	out *bufio.Writer

	mu        sync.Mutex
	nextID    int
	pending   map[string]chan message
	cancelled chan struct{}
	caps      json.RawMessage
	session   json.RawMessage
}

const sessionID = "sess-1"

func main() {
	a := &agent{out: bufio.NewWriter(os.Stdout), pending: map[string]chan message{}, cancelled: make(chan struct{})}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			fmt.Fprintf(os.Stderr, "bad line: %v\n", err)
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go a.request(m)
		case m.Method == "session/cancel":
			a.mu.Lock()
			select {
			case <-a.cancelled:
			default:
				close(a.cancelled)
			}
			a.mu.Unlock()
		case len(m.ID) > 0:
			a.mu.Lock()
			ch := a.pending[string(m.ID)]
			delete(a.pending, string(m.ID))
			a.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

func (a *agent) write(m any) {
	data, _ := json.Marshal(m)
	a.wmu.Lock()
	defer a.wmu.Unlock()
	_, _ = a.out.Write(append(data, '\n'))
	_ = a.out.Flush()
}

func (a *agent) reply(id json.RawMessage, result any) {
	raw, _ := json.Marshal(result)
	a.write(message{JSONRPC: "2.0", ID: id, Result: raw})
}

func (a *agent) call(method string, params any) message {
	raw, _ := json.Marshal(params)
	ch := make(chan message, 1)
	a.mu.Lock()
	a.nextID++
	id := json.RawMessage(strconv.Quote("agent-" + strconv.Itoa(a.nextID)))
	a.pending[string(id)] = ch
	a.mu.Unlock()
	a.write(message{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
	return <-ch
}

func (a *agent) update(u map[string]any) {
	raw, _ := json.Marshal(map[string]any{"sessionId": sessionID, "update": u})
	a.write(message{JSONRPC: "2.0", Method: "session/update", Params: raw})
}

func (a *agent) chunk(text string) {
	a.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

func (a *agent) request(m message) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion    int             `json:"protocolVersion"`
			ClientCapabilities json.RawMessage `json:"clientCapabilities"`
		}
		_ = json.Unmarshal(m.Params, &p)
		a.mu.Lock()
		a.caps = p.ClientCapabilities
		a.mu.Unlock()
		v := 1
		if s := os.Getenv("FAKEAGENT_PROTOCOL_VERSION"); s != "" {
			v, _ = strconv.Atoi(s)
		}
		a.reply(m.ID, map[string]any{"protocolVersion": v, "agentCapabilities": map[string]any{}, "authMethods": []any{}})
	case "session/new":
		a.mu.Lock()
		a.session = m.Params
		a.mu.Unlock()
		a.reply(m.ID, map[string]any{"sessionId": sessionID})
	case "session/prompt":
		a.prompt(m)
	default:
		raw, _ := json.Marshal(map[string]any{"code": -32601, "message": "method not found"})
		a.write(message{JSONRPC: "2.0", ID: m.ID, Error: raw})
	}
}

func (a *agent) prompt(m message) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(m.Params, &p)
	a.mu.Lock()
	a.cancelled = make(chan struct{})
	cancelled := a.cancelled
	a.mu.Unlock()
	var o op
	if len(p.Prompt) == 1 {
		_ = json.Unmarshal([]byte(p.Prompt[0].Text), &o)
	}
	switch o.Op {
	case "hello":
		a.chunk("Hello, ")
		a.chunk("world.")
		a.update(map[string]any{
			"sessionUpdate": "tool_call", "toolCallId": "call_1", "title": "Read main.go", "kind": "read", "status": "pending",
			"locations": []any{map[string]any{"path": "/work/main.go", "line": 3}},
		})
		a.update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call_1", "status": "completed"})
		a.update(map[string]any{"sessionUpdate": "plan", "entries": []any{
			map[string]any{"content": "Read", "priority": "high", "status": "completed"},
			map[string]any{"content": "Fix", "priority": "medium", "status": "pending"},
		}})
		a.update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "hidden"}})
		a.chunk("Bye.")
	case "session":
		a.mu.Lock()
		raw, _ := json.Marshal(map[string]any{"session": a.session, "caps": a.caps})
		a.mu.Unlock()
		a.chunk("session=" + string(raw) + "\n")
	case "permission":
		res := a.call("session/request_permission", map[string]any{
			"sessionId": sessionID,
			"toolCall":  map[string]any{"toolCallId": "call_2", "title": "Edit main.go", "kind": "edit"},
			"options": []any{
				map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
				map[string]any{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
			},
		})
		a.chunk("permission=" + string(res.Result) + "\n")
	case "read":
		res := a.call("fs/read_text_file", map[string]any{"sessionId": sessionID, "path": o.Path, "line": o.Line, "limit": o.Limit})
		if len(res.Error) > 0 {
			a.chunk("readerr=" + string(res.Error) + "\n")
			break
		}
		var r struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(res.Result, &r)
		q, _ := json.Marshal(r.Content)
		a.chunk("read=" + string(q) + "\n")
	case "write":
		res := a.call("fs/write_text_file", map[string]any{"sessionId": sessionID, "path": o.Path, "content": o.Content})
		if len(res.Error) > 0 {
			a.chunk("writeerr=" + string(res.Error) + "\n")
			break
		}
		a.chunk("write=" + string(res.Result) + "\n")
	case "hang":
		<-cancelled
	case "crash":
		os.Exit(3)
	case "pid":
		a.chunk("pid=" + strconv.Itoa(os.Getpid()) + "\n")
	default:
		a.chunk("unknown op\n")
	}
	stop := "end_turn"
	select {
	case <-cancelled:
		stop = "cancelled"
	default:
	}
	a.reply(m.ID, map[string]any{"stopReason": stop})
}
