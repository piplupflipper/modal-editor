package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 10 * time.Second

// mcpClient は、テストの中の簡易な MCP のクライアントである。1 行に 1 つのメッセージを書き、応答を ID で待つ。
type mcpClient struct {
	t *testing.T
	w io.Writer

	mu      sync.Mutex
	nextID  int
	pending map[string]chan map[string]any
	// others は、ID の無いメッセージか、待っていない ID の応答である。
	others chan map[string]any
	done   chan struct{}
}

func newClient(t *testing.T, r io.Reader, w io.Writer) *mcpClient {
	t.Helper()
	c := &mcpClient{t: t, w: w, pending: map[string]chan map[string]any{}, others: make(chan map[string]any, 64), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1<<20), 1<<26)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("サーバーが JSON でない行を書いた: %q", sc.Text())
				continue
			}
			key := fmt.Sprint(m["id"])
			c.mu.Lock()
			ch := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
				continue
			}
			c.others <- m
		}
	}()
	return c
}

// meta は、2026-07-28 の版の、リクエストに必須の _meta である。
func meta() map[string]any {
	return map[string]any{
		metaProtocolVersion:                  modernVersion,
		metaClientCapabilities:               map[string]any{},
		"io.modelcontextprotocol/clientInfo": map[string]any{"name": "test", "version": "0"},
	}
}

// withMeta は、params に 2026-07-28 の _meta を足す。
func withMeta(params map[string]any) map[string]any {
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = meta()
	return params
}

func (c *mcpClient) write(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatalf("書き込みに失敗: %v", err)
	}
}

// start は、リクエストを送り、応答を受け取るチャネルと ID を返す。
func (c *mcpClient) start(method string, params any) (int, chan map[string]any) {
	c.t.Helper()
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan map[string]any, 1)
	c.pending[fmt.Sprint(float64(id))] = ch
	c.mu.Unlock()
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	c.write(msg)
	return id, ch
}

// call は、リクエストを送り、応答を返す。
func (c *mcpClient) call(method string, params any) map[string]any {
	c.t.Helper()
	_, ch := c.start(method, params)
	select {
	case m := <-ch:
		return m
	case <-time.After(testTimeout):
		c.t.Fatalf("%s の応答が来ない", method)
		return nil
	}
}

// result は、応答の result を返す。誤りの応答ならテストを失敗させる。
func (c *mcpClient) result(method string, params any) map[string]any {
	c.t.Helper()
	m := c.call(method, params)
	if m["error"] != nil {
		c.t.Fatalf("%s が誤りを返した: %v", method, m["error"])
	}
	r, _ := m["result"].(map[string]any)
	return r
}

// errorCode は、応答の誤りのコードを返す。誤りでなければテストを失敗させる。
func errorCode(t *testing.T, m map[string]any) int {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("誤りの応答でない: %v", m)
	}
	return int(e["code"].(float64))
}

// tool は、ツールを呼び、isError と、text の内容を返す。
// modern が true なら 2026-07-28 の _meta を付ける。
func (c *mcpClient) tool(modern bool, name string, args any) (isError bool, text string) {
	c.t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	if modern {
		params = withMeta(params)
	}
	r := c.result("tools/call", params)
	content, _ := r["content"].([]any)
	if len(content) != 1 {
		c.t.Fatalf("%s の content が 1 つでない: %v", name, r)
	}
	item := content[0].(map[string]any)
	if item["type"] != "text" {
		c.t.Fatalf("%s の content が text でない: %v", name, item)
	}
	isErr, _ := r["isError"].(bool)
	return isErr, item["text"].(string)
}

// toolOK は、ツールを 2026-07-28 の版で呼び、成功の結果を out へ読む。
func (c *mcpClient) toolOK(name string, args, out any) {
	c.t.Helper()
	isErr, text := c.tool(true, name, args)
	if isErr {
		c.t.Fatalf("%s(%v) が失敗した: %s", name, args, text)
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		c.t.Fatalf("%s の結果を読めない: %v: %s", name, err, text)
	}
}

// toolErr は、ツールを 2026-07-28 の版で呼び、失敗の本文を返す。成功したらテストを失敗させる。
func (c *mcpClient) toolErr(name string, args any, want string) {
	c.t.Helper()
	isErr, text := c.tool(true, name, args)
	if !isErr {
		c.t.Fatalf("%s(%v) が成功した: %s", name, args, text)
	}
	if !strings.Contains(text, want) {
		c.t.Errorf("%s(%v) の誤り = %q, want %q を含む", name, args, text, want)
	}
}

// pos と rng は、ツールの位置と範囲の引数を作る。
func pos(line, col int) map[string]any { return map[string]any{"line": line, "column": col} }

func rng(sl, sc, el, ec int) map[string]any {
	return map[string]any{"start": pos(sl, sc), "end": pos(el, ec)}
}
