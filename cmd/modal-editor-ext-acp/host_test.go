package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

const testTimeout = 10 * time.Second

// fakeAgent は、TestMain が testdata/fakeagent をビルドした実行ファイルのパスである。
var fakeAgent string

// extBinary は、TestMain がこの拡張機能をビルドした実行ファイルのパスである。拡張機能のプロセスを強制終了するテストで使う。
var extBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ext-acp-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeAgent = filepath.Join(dir, "fakeagent")
	out, err := exec.Command("go", "build", "-o", fakeAgent, "./testdata/fakeagent").CombinedOutput() //nolint:gosec // テスト用のエージェントを一時ディレクトリへビルドする
	if err != nil {
		fmt.Fprintf(os.Stderr, "build fakeagent: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	extBinary = filepath.Join(dir, "modal-editor-ext-acp")
	out, err = exec.Command("go", "build", "-o", extBinary, ".").CombinedOutput() //nolint:gosec // テストのために拡張機能を一時ディレクトリへビルドする
	if err != nil {
		fmt.Fprintf(os.Stderr, "build extension: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// hostBuffer は、本体の役が持つバッファである。
type hostBuffer struct {
	path         string
	text         string
	finalNewline bool
	version      ext.Version
}

// host は、本体の役である。拡張機能の Server と io.Pipe でつなぎ、editor/open、buffer/edit、buffer/save、choice/ask に答える。
// バッファの編集は、本体と同じく、適用したら buffer/change を送ってから応答する。
type host struct {
	t    *testing.T
	conn *rpc.Conn
	c    *client

	mu      sync.Mutex
	bufs    map[ext.BufferID]*hostBuffer
	nextBuf ext.BufferID
	output  strings.Builder
	msgs    []ext.MessageShowParams
	opens   []ext.EditorOpenParams
	edits   []ext.BufferEditParams
	saves   []ext.BufferSaveParams
	// asks は、届いた choice/ask である。answers に積んだ応答で答える。
	asks    chan ext.ChoiceAskParams
	answers chan ext.ChoiceAskResult
	// withdrawn は、本体の役が答える前に取り下げられた（ctx が取り消された）choice/ask の数を送る。
	withdrawn chan struct{}
}

// newHost は、settings で拡張機能を初期化した本体の役を返す。getenv を nil にすると環境変数は空とする。
func newHost(t *testing.T, s settings, getenv func(string) string) *host {
	t.Helper()
	c := newClient()
	if !testing.Verbose() {
		c.logger.SetOutput(io.Discard)
	}
	c.timeouts = timeouts{start: testTimeout, request: testTimeout, stopGrace: 500 * time.Millisecond}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	c.getenv = getenv
	h := newHostState(t)
	h.c = c
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- c.srv.Serve(context.Background(), extR, extW) }()
	h.conn = rpc.NewConn(hostR, hostW, rpc.HandlerFuncs{Notification: h.notification, Request: h.request})
	t.Cleanup(func() {
		c.stop()
		_ = h.conn.Close()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve = %v, want nil", err)
			}
		case <-time.After(testTimeout):
			t.Error("Serve did not return after the host closed")
		}
	})
	h.initialize(s)
	return h
}

// newHostState は、接続の無い本体の役を作る。
func newHostState(t *testing.T) *host {
	return &host{
		t:    t,
		bufs: map[ext.BufferID]*hostBuffer{}, nextBuf: 100,
		asks: make(chan ext.ChoiceAskParams, 16), answers: make(chan ext.ChoiceAskResult, 16), withdrawn: make(chan struct{}, 16),
	}
}

// initialize は、s を settings にして initialize を送る。
func (h *host) initialize(s settings) {
	h.t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		h.t.Fatal(err)
	}
	var res ext.InitializeResult
	if err := h.conn.Call(h.ctx(), ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion, Settings: raw}, &res); err != nil {
		h.t.Fatalf("initialize: %v", err)
	}
}

func fakeSettings() settings { return settings{Agent: agentConfig{Command: fakeAgent}} }

func (h *host) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	h.t.Cleanup(cancel)
	return ctx
}

func decodeJSON[T any](t *testing.T, raw json.RawMessage) T {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Errorf("decode %T: %v", v, err)
	}
	return v
}

func (h *host) notification(_ context.Context, n *rpc.Notification) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch n.Method {
	case ext.MethodOutputAppend:
		p := decodeJSON[ext.OutputAppendParams](h.t, n.Params)
		if p.Channel != outputChannel {
			h.t.Errorf("output/append channel = %q, want %q", p.Channel, outputChannel)
		}
		h.output.WriteString(p.Text)
	case ext.MethodMessageShow:
		h.msgs = append(h.msgs, decodeJSON[ext.MessageShowParams](h.t, n.Params))
	}
}

// request は、拡張機能からのリクエストに答える。internal/rpc はハンドラーを読み取りの goroutine から呼ぶので、
// 利用者の応答を待つ choice/ask は別の goroutine で待つ。
func (h *host) request(ctx context.Context, req *rpc.Request, reply rpc.Replier) {
	switch req.Method {
	case ext.MethodEditorOpen:
		p := decodeJSON[ext.EditorOpenParams](h.t, req.Params)
		id, v, err := h.editorOpen(p)
		if err != nil {
			_ = reply(nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()})
			return
		}
		_ = reply(ext.EditorOpenResult{Buffer: id, Version: v}, nil)
	case ext.MethodBufferEdit:
		p := decodeJSON[ext.BufferEditParams](h.t, req.Params)
		_ = reply(h.edit(p), nil)
	case ext.MethodBufferSave:
		p := decodeJSON[ext.BufferSaveParams](h.t, req.Params)
		res, err := h.save(p)
		if err != nil {
			_ = reply(nil, &rpc.Error{Code: rpc.CodeInvalidParams, Message: err.Error()})
			return
		}
		_ = reply(res, nil)
	case ext.MethodChoiceAsk:
		h.asks <- decodeJSON[ext.ChoiceAskParams](h.t, req.Params)
		go func() {
			select {
			case a := <-h.answers:
				_ = reply(a, nil)
			case <-ctx.Done():
				h.withdrawn <- struct{}{}
				_ = reply(nil, ctx.Err())
			}
		}()
	default:
		_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: req.Method})
	}
}

// open は、バッファを開き、拡張機能へ buffer/open を送る。text は末尾の改行を含まない全文である。
func (h *host) open(id ext.BufferID, path, text string) {
	h.t.Helper()
	h.mu.Lock()
	h.bufs[id] = &hostBuffer{path: path, text: text, finalNewline: true, version: 1}
	h.mu.Unlock()
	if err := h.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: id, Path: path, Version: 1, Text: text}); err != nil {
		h.t.Fatalf("buffer/open: %v", err)
	}
}

// change は、利用者の編集の役として、バッファ id の全文を text にし、拡張機能へ buffer/change を送る。
func (h *host) change(id ext.BufferID, text string) {
	h.t.Helper()
	h.mu.Lock()
	b := h.bufs[id]
	edit := ext.Edit{Range: wholeRange(b.text), Text: text}
	from := b.version
	b.text = text
	b.version++
	to := b.version
	h.mu.Unlock()
	err := h.conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{Buffer: id, FromVersion: from, ToVersion: to, Edits: []ext.Edit{edit}})
	if err != nil {
		h.t.Fatalf("buffer/change: %v", err)
	}
}

func wholeRange(text string) ext.Range {
	lines := strings.Split(text, "\n")
	return ext.Range{End: ext.Position{Line: len(lines) - 1, Byte: len(lines[len(lines)-1])}}
}

// editorOpen は、本体の editor/open の役である。開いていないファイルは読み込み（無ければ空のバッファにし）、応答より先に buffer/open を送る。
func (h *host) editorOpen(p ext.EditorOpenParams) (ext.BufferID, ext.Version, error) {
	h.mu.Lock()
	h.opens = append(h.opens, p)
	for id, b := range h.bufs {
		if b.path == p.Path {
			h.mu.Unlock()
			return id, b.version, nil
		}
	}
	b := &hostBuffer{path: p.Path, finalNewline: true, version: 1}
	data, err := os.ReadFile(p.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// 本体と同じく、無いファイルは行の無いバッファにし、末尾の改行を付けない（ext.BufferOpenParams の FinalNewline）。
		b.finalNewline = false
	case err != nil:
		h.mu.Unlock()
		return 0, 0, err
	default:
		s := string(data)
		b.finalNewline = strings.HasSuffix(s, "\n")
		b.text = strings.TrimSuffix(s, "\n")
	}
	h.nextBuf++
	id := h.nextBuf
	h.bufs[id] = b
	h.mu.Unlock()
	fn := b.finalNewline
	err = h.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: id, Path: p.Path, Version: 1, Text: b.text, FinalNewline: &fn})
	return id, 1, err
}

// edit は、本体の buffer/edit の役である。BaseVersion が一致すれば適用し、応答より先に buffer/change を送る。
func (h *host) edit(p ext.BufferEditParams) ext.BufferEditResult {
	h.mu.Lock()
	h.edits = append(h.edits, p)
	b := h.bufs[p.Buffer]
	if b == nil || b.version != p.Transaction.BaseVersion {
		var v ext.Version
		if b != nil {
			v = b.version
		}
		h.mu.Unlock()
		return ext.BufferEditResult{Applied: false, Version: v}
	}
	for _, e := range p.Transaction.Edits {
		start, end := offset(b.text, e.Range.Start), offset(b.text, e.Range.End)
		b.text = b.text[:start] + e.Text + b.text[end:]
	}
	from := b.version
	b.version++
	to := b.version
	// 本体と同じく、行の無いバッファに入力したら末尾の改行を付けるようにし、buffer/open を送り直す。
	reopen := !b.finalNewline && b.text != ""
	if reopen {
		b.finalNewline = true
	}
	text, path := b.text, b.path
	h.mu.Unlock()
	if reopen {
		_ = h.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: p.Buffer, Path: path, Version: to, Text: text})
	} else {
		_ = h.conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{Buffer: p.Buffer, FromVersion: from, ToVersion: to, Edits: p.Transaction.Edits})
	}
	return ext.BufferEditResult{Applied: true, Version: to}
}

// offset は、位置をテキストの先頭からのバイト数にする。
func offset(text string, p ext.Position) int {
	off := 0
	for range p.Line {
		off += strings.IndexByte(text[off:], '\n') + 1
	}
	return off + p.Byte
}

// save は、本体の buffer/save の役である。Version が一致すればファイルへ書き出す。
func (h *host) save(p ext.BufferSaveParams) (ext.BufferSaveResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saves = append(h.saves, p)
	b := h.bufs[p.Buffer]
	if b == nil {
		return ext.BufferSaveResult{}, fmt.Errorf("unknown buffer %d", p.Buffer)
	}
	if p.Version != nil && *p.Version != b.version {
		return ext.BufferSaveResult{Saved: false, Version: b.version}, nil
	}
	data := b.text
	if b.finalNewline {
		data += "\n"
	}
	if err := os.WriteFile(b.path, []byte(data), 0o600); err != nil {
		return ext.BufferSaveResult{}, err
	}
	return ext.BufferSaveResult{Saved: true, Version: b.version}, nil
}

// exec は、コマンド id を args で実行する。バッファ 1 を開いていなければ開く。
func (h *host) exec(id string, args any) ext.CommandExecuteResult {
	h.t.Helper()
	h.mu.Lock()
	b, ok := h.bufs[1]
	h.mu.Unlock()
	if !ok {
		h.open(1, "", "")
		h.mu.Lock()
		b = h.bufs[1]
		h.mu.Unlock()
	}
	p := ext.CommandExecuteParams{Buffer: 1, Version: b.version, ID: id}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			h.t.Fatal(err)
		}
		p.Args = raw
	}
	var res ext.CommandExecuteResult
	if err := h.conn.Call(h.ctx(), ext.MethodCommandExecute, p, &res); err != nil {
		h.t.Fatalf("command/execute %s: %v", id, err)
	}
	return res
}

// prompt は、fakeagent の操作 o をプロンプトとして送り、受け付けられたことを確かめる。
func (h *host) prompt(o map[string]any) {
	h.t.Helper()
	text, err := json.Marshal(o)
	if err != nil {
		h.t.Fatal(err)
	}
	if res := h.exec(cmdPrompt, promptArgs{Text: string(text)}); !res.OK {
		h.t.Fatalf("%s %s: ok = false; messages = %v", cmdPrompt, text, h.messages())
	}
}

func (h *host) outputText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.output.String()
}

func (h *host) messages() []ext.MessageShowParams {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ext.MessageShowParams(nil), h.msgs...)
}

// waitOutput は、acp の欄の内容が text を含むまで待ち、その時点の内容を返す。
func (h *host) waitOutput(text string) string {
	h.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if out := h.outputText(); strings.Contains(out, text) {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("output %q did not arrive; output = %q", text, h.outputText())
	return ""
}

// waitFor は、cond が true を返すまで待つ。
func (h *host) waitFor(cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("the condition did not hold before the timeout")
}

// waitIdle は、処理中のプロンプトが無くなるまで待つ。
func (h *host) waitIdle() {
	h.t.Helper()
	h.waitFor(func() bool {
		h.c.mu.Lock()
		defer h.c.mu.Unlock()
		return h.c.turn == nil
	})
}

// waitMessage は、text を含む message/show が届くまで待つ。
func (h *host) waitMessage(text string) ext.MessageShowParams {
	h.t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		for _, m := range h.messages() {
			if strings.Contains(m.Text, text) {
				return m
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("message/show %q did not arrive; seen = %v", text, h.messages())
	return ext.MessageShowParams{}
}

// waitAsk は、choice/ask が届くまで待つ。
func (h *host) waitAsk() ext.ChoiceAskParams {
	h.t.Helper()
	select {
	case p := <-h.asks:
		return p
	case <-time.After(testTimeout):
		h.t.Fatal("choice/ask did not arrive")
		return ext.ChoiceAskParams{}
	}
}

// bufferText は、本体の役が持つバッファ id の全文と版を返す。
func (h *host) bufferText(id ext.BufferID) (string, ext.Version) {
	h.mu.Lock()
	defer h.mu.Unlock()
	b := h.bufs[id]
	return b.text, b.version
}
