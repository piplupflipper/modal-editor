package main

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// fakeEditor は、テストで本体の役をする editorAPI である。バッファは行の列で持ち、buffer/edit を逐次の数え方で適用する。
type fakeEditor struct {
	mu      sync.Mutex
	state   ext.EditorStateResult
	lines   map[ext.BufferID][]string
	diags   ext.DiagnosticListResult
	edits   []ext.BufferEditParams
	saved   []ext.BufferSaveParams
	opened  []ext.EditorOpenParams
	notSent map[ext.BufferID]bool
	// editErr が nil でなければ、Edit はそれを返す（本体の誤りの応答）。
	editErr error
	// block が nil でなければ、EditorState は ctx が終わるまで待ち、blocked へ ctx の誤りを送る。
	block   chan struct{}
	blocked chan error
}

func newFakeEditor() *fakeEditor {
	return &fakeEditor{
		state: ext.EditorStateResult{
			Buffer: 1, Cursor: ext.Position{Line: 2, Byte: 4}, Mode: ext.ModeVisual,
			Selections: []ext.Range{{Start: ext.Position{Line: 0, Byte: 1}, End: ext.Position{Line: 2, Byte: 5}}},
			Buffers: []ext.BufferInfo{
				{Buffer: 1, Path: "/tmp/a.go", FileType: "go", Version: 3, Modified: true},
				{Buffer: 2, Path: "/tmp/bin", Version: 1},
			},
		},
		lines:   map[ext.BufferID][]string{1: {"package a", "", "func f() {}"}, 2: {"\xff"}},
		notSent: map[ext.BufferID]bool{2: true},
		blocked: make(chan error, 1),
	}
}

func (f *fakeEditor) EditorState(ctx context.Context) (ext.EditorStateResult, error) {
	f.mu.Lock()
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			f.blocked <- ctx.Err()
			return ext.EditorStateResult{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state
	st.Buffers = slices.Clone(st.Buffers)
	return st, nil
}

func (f *fakeEditor) ListDiagnostics(_ context.Context, p ext.DiagnosticListParams) (ext.DiagnosticListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := ext.DiagnosticListResult{Entries: []ext.DiagnosticEntry{}}
	for _, e := range f.diags.Entries {
		if p.Buffer == nil || *p.Buffer == e.Buffer {
			res.Entries = append(res.Entries, e)
		}
	}
	return res, nil
}

func (f *fakeEditor) info(id ext.BufferID) *ext.BufferInfo {
	for i := range f.state.Buffers {
		if f.state.Buffers[i].Buffer == id {
			return &f.state.Buffers[i]
		}
	}
	return nil
}

func (f *fakeEditor) Edit(_ context.Context, p ext.BufferEditParams) (ext.BufferEditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, p)
	if f.editErr != nil {
		return ext.BufferEditResult{}, f.editErr
	}
	b := f.info(p.Buffer)
	if b == nil {
		return ext.BufferEditResult{}, errors.New("unknown buffer")
	}
	if p.Transaction.BaseVersion != b.Version {
		return ext.BufferEditResult{Applied: false, Version: b.Version}, nil
	}
	text := strings.Join(f.lines[p.Buffer], "\n")
	text = applyEdits(text, p.Transaction.Edits)
	f.lines[p.Buffer] = strings.Split(text, "\n")
	b.Version++
	return ext.BufferEditResult{Applied: true, Version: b.Version}, nil
}

func (f *fakeEditor) OpenEditor(_ context.Context, p ext.EditorOpenParams) (ext.EditorOpenResult, error) {
	if err := p.Validate(); err != nil {
		return ext.EditorOpenResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened = append(f.opened, p)
	return ext.EditorOpenResult{Buffer: 7, Version: 1, Moved: p.Position != nil}, nil
}

func (f *fakeEditor) SaveBuffer(_ context.Context, p ext.BufferSaveParams) (ext.BufferSaveResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, p)
	b := f.info(p.Buffer)
	if p.Version != nil && *p.Version != b.Version {
		return ext.BufferSaveResult{Saved: false, Version: b.Version}, nil
	}
	b.Modified = false
	return ext.BufferSaveResult{Saved: true, Version: b.Version}, nil
}

func (f *fakeEditor) ReadBuffer(_ context.Context, id ext.BufferID, _ ext.Version) (snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notSent[id] {
		return snapshot{}, errNotSent
	}
	return snapshot{Version: f.info(id).Version, Lines: slices.Clone(f.lines[id])}, nil
}

// applyEdits は、編集の列を逐次の数え方でテキストへ適用する（本体の buffer/edit と同じ）。
func applyEdits(text string, edits []ext.Edit) string {
	for _, e := range edits {
		s, en := offset(text, e.Range.Start), offset(text, e.Range.End)
		text = text[:s] + e.Text + text[en:]
	}
	return text
}

func offset(text string, p ext.Position) int {
	off := 0
	for range p.Line {
		off += strings.IndexByte(text[off:], '\n') + 1
	}
	return off + p.Byte
}

// pipeConn は、テストの接続である。読み取りは io.Pipe、書き込みは別の io.Pipe で、Close は読み取り側を閉じる。
type pipeConn struct {
	io.Reader
	io.Writer
	closeOnce sync.Once
	closeFn   func()
}

func (c *pipeConn) Close() error {
	c.closeOnce.Do(c.closeFn)
	return nil
}

// session は、fakeEditor に繋いだ serveMCP と、それに話すクライアントである。
type session struct {
	*mcpClient
	ed *fakeEditor
	// in は、サーバーの入力の書き込み口である。閉じると、サーバーには EOF が届く。
	in   *io.PipeWriter
	done chan error
}

func startSession(t *testing.T) *session {
	t.Helper()
	ed := newFakeEditor()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	conn := &pipeConn{Reader: inR, Writer: outW, closeFn: func() {
		_ = inR.Close()
		_ = outW.Close()
	}}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- serveMCP(ctx, conn, newToolbox(ed), log.New(io.Discard, "", 0)) }()
	s := &session{mcpClient: newClient(t, outR, inW), ed: ed, in: inW, done: done}
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveMCP = %v", err)
			}
		case <-time.After(testTimeout):
			t.Error("serveMCP が終わらない")
		}
	})
	return s
}

// 2026-07-28 の版では、各リクエストが _meta に版と能力を持つ。持たないリクエストは -32602、
// 扱わない版は -32022（UnsupportedProtocolVersionError）で、扱う版の一覧を data に付けて拒む。
//
// req: FR-32
func TestModernVersionCheck(t *testing.T) {
	s := startSession(t)

	if code := errorCode(t, s.call("tools/list", map[string]any{})); code != codeInvalidParams {
		t.Errorf("_meta の無い tools/list の誤り = %d, want %d", code, codeInvalidParams)
	}
	noCaps := map[string]any{"_meta": map[string]any{metaProtocolVersion: modernVersion}}
	if code := errorCode(t, s.call("tools/list", noCaps)); code != codeInvalidParams {
		t.Errorf("clientCapabilities の無い tools/list の誤り = %d, want %d", code, codeInvalidParams)
	}

	old := map[string]any{"_meta": map[string]any{metaProtocolVersion: "1900-01-01", metaClientCapabilities: map[string]any{}}}
	m := s.call("tools/list", old)
	if code := errorCode(t, m); code != codeUnsupportedProtocolVersion {
		t.Fatalf("扱わない版の誤り = %d, want %d", code, codeUnsupportedProtocolVersion)
	}
	data := m["error"].(map[string]any)["data"].(map[string]any)
	if data["requested"] != "1900-01-01" || !slices.Equal(toStrings(data["supported"]), []string{modernVersion}) {
		t.Errorf("誤りの data = %v", data)
	}
	// initialize を受け取っていない接続では、2025-11-25 を _meta で示しても扱わない。
	legacyMeta := map[string]any{"_meta": map[string]any{metaProtocolVersion: legacyVersion, metaClientCapabilities: map[string]any{}}}
	if code := errorCode(t, s.call("tools/list", legacyMeta)); code != codeUnsupportedProtocolVersion {
		t.Errorf("initialize の前の 2025-11-25 の誤り = %d, want %d", code, codeUnsupportedProtocolVersion)
	}

	r := s.result("server/discover", withMeta(nil))
	if !slices.Equal(toStrings(r["supportedVersions"]), []string{modernVersion}) || r["resultType"] != "complete" {
		t.Errorf("server/discover = %v", r)
	}
	if _, ok := r["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("server/discover の capabilities に tools が無い: %v", r)
	}
	info := r["_meta"].(map[string]any)[metaServerInfo].(map[string]any)
	if info["name"] != serverName {
		t.Errorf("serverInfo = %v", info)
	}

	r = s.result("tools/list", withMeta(nil))
	if r["resultType"] != "complete" {
		t.Errorf("tools/list の resultType = %v", r["resultType"])
	}
	var names []string
	for _, x := range r["tools"].([]any) {
		tl := x.(map[string]any)
		names = append(names, tl["name"].(string))
		if schema, ok := tl["inputSchema"].(map[string]any); !ok || schema["type"] != "object" {
			t.Errorf("%s の inputSchema = %v", tl["name"], tl["inputSchema"])
		}
	}
	want := []string{"list_buffers", "get_editor_state", "read_buffer", "get_diagnostics", "edit_buffer", "open_file", "save_buffer"}
	if !slices.Equal(names, want) {
		t.Errorf("tools/list の名前 = %v, want %v", names, want)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// 2025-11-25 の版のクライアントは initialize で版を取り決め、以後のリクエストに _meta を付けない。
// 扱わない版を求められたら、扱う版（2025-11-25）を返す。
//
// req: FR-32
func TestLegacyInitialize(t *testing.T) {
	s := startSession(t)
	if code := errorCode(t, s.call("initialize", map[string]any{})); code != codeInvalidParams {
		t.Errorf("protocolVersion の無い initialize の誤り = %d", code)
	}
	r := s.result("initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "old", "version": "1"},
	})
	if r["protocolVersion"] != legacyVersion {
		t.Errorf("initialize の protocolVersion = %v, want %s", r["protocolVersion"], legacyVersion)
	}
	if _, ok := r["capabilities"].(map[string]any)["tools"]; !ok {
		t.Errorf("initialize の capabilities に tools が無い: %v", r)
	}
	if r["serverInfo"].(map[string]any)["name"] != serverName {
		t.Errorf("serverInfo = %v", r["serverInfo"])
	}
	s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	r = s.result("tools/list", nil)
	if len(r["tools"].([]any)) != 7 {
		t.Errorf("tools/list = %v", r)
	}
	if _, ok := r["resultType"]; ok {
		t.Errorf("2025-11-25 の結果に resultType がある: %v", r)
	}
	isErr, text := s.tool(false, "list_buffers", map[string]any{})
	if isErr || !strings.Contains(text, `"path":"/tmp/a.go"`) {
		t.Errorf("list_buffers = %v %s", isErr, text)
	}
	if s.result("ping", nil) == nil {
		t.Error("ping の result が無い")
	}
}

// JSON-RPC として正しくないメッセージ、知らないメソッド、知らないツールは JSON-RPC の誤りで返し、接続は続ける。
func TestProtocolErrors(t *testing.T) {
	s := startSession(t)
	if _, err := io.WriteString(s.in, "{not json\n[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]\n"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{codeParseError, codeInvalidRequest} {
		select {
		case m := <-s.others:
			if code := errorCode(t, m); code != want || m["id"] != nil {
				t.Errorf("誤り = %d（id %v）, want %d（id null）", code, m["id"], want)
			}
		case <-time.After(testTimeout):
			t.Fatal("誤りの応答が来ない")
		}
	}
	s.write(map[string]any{"jsonrpc": "2.0", "id": nil, "method": "ping"})
	select {
	case m := <-s.others:
		if code := errorCode(t, m); code != codeInvalidRequest {
			t.Errorf("null の ID の誤り = %d", code)
		}
	case <-time.After(testTimeout):
		t.Fatal("null の ID への応答が来ない")
	}
	if code := errorCode(t, s.call("resources/list", withMeta(nil))); code != codeMethodNotFound {
		t.Errorf("知らないメソッドの誤り = %d", code)
	}
	if code := errorCode(t, s.call("tools/call", withMeta(map[string]any{"name": "no_such_tool"}))); code != codeInvalidParams {
		t.Errorf("知らないツールの誤り = %d", code)
	}
	// 文字列の ID も使える。
	s.mu.Lock()
	ch := make(chan map[string]any, 1)
	s.pending["abc"] = ch
	s.mu.Unlock()
	s.write(map[string]any{"jsonrpc": "2.0", "id": "abc", "method": "ping"})
	select {
	case m := <-ch:
		if m["error"] != nil {
			t.Errorf("文字列の ID の ping = %v", m)
		}
	case <-time.After(testTimeout):
		t.Fatal("文字列の ID の ping の応答が来ない")
	}
}

// ツールの位置は 1 から数え、拡張機能 API の 0 から数えた位置と変換する。
//
// req: FR-32
func TestReadTools(t *testing.T) {
	s := startSession(t)
	s.ed.diags = ext.DiagnosticListResult{Entries: []ext.DiagnosticEntry{{
		Buffer: 1, Extension: "lsp", Version: 3,
		Diagnostics: []ext.Diagnostic{{Range: ext.Range{Start: ext.Position{Line: 2, Byte: 5}, End: ext.Position{Line: 2, Byte: 6}}, Severity: ext.SeverityError, Message: "boom", Source: "gopls"}},
	}}}

	var st struct {
		Buffer     int         `json:"buffer"`
		Path       string      `json:"path"`
		Version    int         `json:"version"`
		Cursor     mcpPosition `json:"cursor"`
		Mode       string      `json:"mode"`
		Selections []mcpRange  `json:"selections"`
	}
	s.toolOK("get_editor_state", map[string]any{}, &st)
	wantSel := []mcpRange{{Start: mcpPosition{1, 2}, End: mcpPosition{3, 6}}}
	if st.Buffer != 1 || st.Path != "/tmp/a.go" || st.Version != 3 || st.Cursor != (mcpPosition{3, 5}) || st.Mode != "visual" || !slices.Equal(st.Selections, wantSel) {
		t.Errorf("get_editor_state = %+v", st)
	}

	var rb struct {
		Version   int    `json:"version"`
		LineCount int    `json:"lineCount"`
		StartLine int    `json:"startLine"`
		EndLine   int    `json:"endLine"`
		Text      string `json:"text"`
	}
	s.toolOK("read_buffer", map[string]any{"buffer": 1}, &rb)
	if rb.Version != 3 || rb.LineCount != 3 || rb.StartLine != 1 || rb.EndLine != 3 || rb.Text != "package a\n\nfunc f() {}" {
		t.Errorf("read_buffer = %+v", rb)
	}
	s.toolOK("read_buffer", map[string]any{"path": "/tmp/a.go", "startLine": 2, "endLine": 99}, &rb)
	if rb.StartLine != 2 || rb.EndLine != 3 || rb.Text != "\nfunc f() {}" {
		t.Errorf("read_buffer の行の範囲 = %+v", rb)
	}
	s.toolErr("read_buffer", map[string]any{"buffer": 1, "startLine": 4}, "out of range")
	s.toolErr("read_buffer", map[string]any{"buffer": 1, "startLine": 3, "endLine": 2}, "before startLine")
	s.toolErr("read_buffer", map[string]any{"buffer": 2}, "invalid UTF-8")
	s.toolErr("read_buffer", map[string]any{"buffer": 9}, "not open")
	s.toolErr("read_buffer", map[string]any{"path": "/nope"}, "open_file")
	s.toolErr("read_buffer", map[string]any{"path": "tmp/a.go"}, "not an absolute path")
	// パスは filepath.Clean で整えてから比べる。
	for _, p := range []string{"/tmp/./a.go", "/tmp/x/../a.go", "//tmp/a.go"} {
		s.toolOK("read_buffer", map[string]any{"path": p}, &rb)
		if rb.Version != 3 {
			t.Errorf("read_buffer(%q) = %+v", p, rb)
		}
	}
	s.toolErr("read_buffer", map[string]any{}, "required")
	s.toolErr("read_buffer", map[string]any{"buffer": 1, "path": "/tmp/a.go"}, "not both")
	s.toolErr("read_buffer", map[string]any{"buffer": 1, "lines": 3}, "unknown field")

	var diags struct {
		Entries []struct {
			Buffer      int    `json:"buffer"`
			Path        string `json:"path"`
			Extension   string `json:"extension"`
			Version     int    `json:"version"`
			Diagnostics []struct {
				Range    mcpRange `json:"range"`
				Severity string   `json:"severity"`
				Message  string   `json:"message"`
				Source   string   `json:"source"`
			} `json:"diagnostics"`
		} `json:"entries"`
	}
	s.toolOK("get_diagnostics", map[string]any{}, &diags)
	if len(diags.Entries) != 1 {
		t.Fatalf("get_diagnostics = %+v", diags)
	}
	e := diags.Entries[0]
	if e.Path != "/tmp/a.go" || e.Extension != "lsp" || e.Version != 3 || len(e.Diagnostics) != 1 ||
		e.Diagnostics[0].Range != (mcpRange{Start: mcpPosition{3, 6}, End: mcpPosition{3, 7}}) || e.Diagnostics[0].Message != "boom" {
		t.Errorf("get_diagnostics = %+v", e)
	}
	s.toolOK("get_diagnostics", map[string]any{"buffer": 2}, &diags)
	if len(diags.Entries) != 0 {
		t.Errorf("バッファ 2 の get_diagnostics = %+v", diags)
	}
}

// 編集を禁止したバッファへの edit_buffer は、本体の E21 の誤りをそのまま返し、位置の数え方の注記を付けない。
//
// req: FR-118
func TestEditBufferReadOnly(t *testing.T) {
	s := startSession(t)
	s.ed.mu.Lock()
	s.ed.editErr = &rpc.Error{Code: ext.ErrorCodeReadOnly, Message: "E21: 編集を禁止したバッファは変更できません"}
	s.ed.mu.Unlock()
	isErr, text := s.tool(true, "edit_buffer", map[string]any{"buffer": 1, "version": 3, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 1), "text": "x"},
	}})
	if !isErr || !strings.Contains(text, "E21") || strings.Contains(text, "0-based") {
		t.Errorf("edit_buffer on a read-only buffer = %v %q", isErr, text)
	}
}

// edit_buffer の位置はすべて version の時点のテキストで数える。拡張機能は後ろから並べて、逐次の数え方のトランザクションにする。
//
// req: FR-32
func TestEditBuffer(t *testing.T) {
	s := startSession(t)
	var res struct {
		Applied bool `json:"applied"`
		Version int  `json:"version"`
	}
	s.toolOK("edit_buffer", map[string]any{"buffer": 1, "version": 3, "edits": []any{
		map[string]any{"range": rng(1, 9, 1, 10), "text": "b"},
		map[string]any{"range": rng(3, 6, 3, 7), "text": "g"},
		map[string]any{"range": rng(1, 1, 1, 1), "text": "// x\n"},
		map[string]any{"range": rng(2, 1, 2, 1), "text": "var v = 1"},
	}}, &res)
	if !res.Applied || res.Version != 4 {
		t.Errorf("edit_buffer = %+v", res)
	}
	if got := strings.Join(s.ed.lines[1], "\n"); got != "// x\npackage b\nvar v = 1\nfunc g() {}" {
		t.Errorf("編集の後のテキスト = %q", got)
	}

	s.toolErr("edit_buffer", map[string]any{"path": "/tmp/a.go", "version": 3, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 1), "text": "x"},
	}}, "call read_buffer again")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "edits": []any{map[string]any{"range": rng(1, 1, 1, 1), "text": "x"}}}, "version is required")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{}}, "empty")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 3), "text": "a"},
		map[string]any{"range": rng(1, 2, 1, 4), "text": "b"},
	}}, "overlap")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{
		map[string]any{"range": rng(1, 0, 1, 1), "text": "a"},
	}}, "start at 1")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{
		map[string]any{"range": rng(2, 1, 1, 1), "text": "a"},
	}}, "before its start")
	// 位置の誤りは、送る前に 1 始まりの数え方で返す。いまのテキストは "// x\npackage b\nvar v = 1\nfunc g() {}"。
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{
		map[string]any{"range": rng(1, 1, 1, 1), "text": "a"},
		map[string]any{"range": rng(5, 1, 5, 1), "text": "a"},
	}}, "edits[1]: line 5 is out of range: the buffer has lines 1 to 4")
	s.toolErr("edit_buffer", map[string]any{"buffer": 1, "version": 4, "edits": []any{
		map[string]any{"range": rng(1, 2, 1, 6), "text": "a"},
	}}, "edits[0]: column 6 of line 1 is out of range: the line has 4 bytes, so the column is 1 to 5")
	if len(s.ed.edits) != 2 {
		t.Errorf("本体へ送った buffer/edit の数 = %d, want 2（引数の誤りは送らない）", len(s.ed.edits))
	}
}

// toTransaction は、同じ版のテキストで数えた範囲の重ならない置き換えを、逐次の数え方で同じ結果になる列にする。
func TestToTransaction(t *testing.T) {
	const text = "abc\ndef\nghi"
	e := func(sl, sc, el, ec int, s string) mcpEdit {
		return mcpEdit{Range: mcpRange{Start: mcpPosition{sl, sc}, End: mcpPosition{el, ec}}, Text: s}
	}
	cases := []struct {
		name  string
		edits []mcpEdit
		want  string
	}{
		{"1 つ", []mcpEdit{e(2, 2, 2, 3, "E")}, "abc\ndEf\nghi"},
		{"順不同の 2 つ", []mcpEdit{e(3, 1, 3, 2, "G"), e(1, 1, 1, 2, "A")}, "Abc\ndef\nGhi"},
		{"行をまたぐ削除と後ろの置き換え", []mcpEdit{e(1, 3, 2, 2, ""), e(3, 3, 3, 4, "I")}, "abef\nghI"},
		{"同じ位置への挿入は引数の順", []mcpEdit{e(2, 1, 2, 1, "1"), e(2, 1, 2, 1, "2"), e(2, 1, 2, 2, "D")}, "abc\n12Def\nghi"},
		{"隣り合う範囲", []mcpEdit{e(1, 1, 1, 2, "x"), e(1, 2, 1, 3, "y")}, "xyc\ndef\nghi"},
		{"改行を足す", []mcpEdit{e(1, 4, 1, 4, "\nnew"), e(2, 4, 2, 4, "!")}, "abc\nnew\ndef!\nghi"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tx, err := toTransaction(5, c.edits)
			if err != nil {
				t.Fatal(err)
			}
			if tx.BaseVersion != 5 {
				t.Errorf("BaseVersion = %d", tx.BaseVersion)
			}
			if got := applyEdits(text, tx.Edits); got != c.want {
				t.Errorf("適用の結果 = %q, want %q", got, c.want)
			}
		})
	}
	if _, err := toTransaction(1, []mcpEdit{e(1, 1, 1, 2, "x"), e(1, 1, 1, 1, "y")}); err == nil {
		t.Error("範囲の開始と同じ位置への、後に並んだ挿入が重なりとして拒まれない")
	}
}

// open_file と save_buffer は、引数を editor/open と buffer/save に変換する。
//
// req: FR-32
func TestOpenAndSave(t *testing.T) {
	s := startSession(t)
	var open struct {
		Buffer int  `json:"buffer"`
		Moved  bool `json:"moved"`
	}
	s.toolOK("open_file", map[string]any{"path": "/tmp/c.go", "line": 2, "column": 3}, &open)
	s.toolOK("open_file", map[string]any{"path": "/tmp/d.go", "focus": false}, &open)
	if open.Buffer != 7 || open.Moved {
		t.Errorf("open_file = %+v", open)
	}
	s.toolErr("open_file", map[string]any{"path": "rel.go"}, "absolute")
	s.toolErr("open_file", map[string]any{"path": "/tmp/c.go", "column": 3}, "requires line")
	if len(s.ed.opened) != 2 {
		t.Fatalf("editor/open の数 = %d", len(s.ed.opened))
	}
	p := s.ed.opened[0]
	if p.Path != "/tmp/c.go" || p.Position == nil || *p.Position != (ext.Position{Line: 1, Byte: 2}) || !p.Focus {
		t.Errorf("editor/open の params = %+v", p)
	}
	if s.ed.opened[1].Focus || s.ed.opened[1].Position != nil {
		t.Errorf("2 つ目の editor/open の params = %+v", s.ed.opened[1])
	}

	var saved struct {
		Saved   bool `json:"saved"`
		Version int  `json:"version"`
	}
	s.toolErr("save_buffer", map[string]any{"buffer": 1, "version": 2}, "does not match")
	s.toolOK("save_buffer", map[string]any{"path": "/tmp/a.go", "version": 3}, &saved)
	if !saved.Saved || saved.Version != 3 {
		t.Errorf("save_buffer = %+v", saved)
	}
}

// 取り消しの通知を受け取ったら、処理中のツールの ctx を取り消し、そのリクエストには応答しない。
// 他のリクエストは止まらずに処理する。
func TestCancel(t *testing.T) {
	s := startSession(t)
	s.ed.mu.Lock()
	s.ed.block = make(chan struct{})
	s.ed.mu.Unlock()
	id, ch := s.start("tools/call", withMeta(map[string]any{"name": "list_buffers", "arguments": map[string]any{}}))
	// 待っている間も、他のリクエストに応答する。
	if s.result("ping", nil) == nil {
		t.Fatal("ping の result が無い")
	}
	s.write(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id, "reason": "test"}})
	select {
	case err := <-s.ed.blocked:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("ツールの ctx の誤り = %v", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("ツールの ctx が取り消されない")
	}
	if s.result("ping", nil) == nil {
		t.Fatal("ping の result が無い")
	}
	select {
	case m := <-ch:
		t.Errorf("取り消したリクエストに応答した: %v", m)
	default:
	}
}

// 入力が EOF になったら、処理中のリクエストの応答を書き終えてから接続を閉じる（中継コマンドの終わり方に合わせる）。
func TestEOFFlushesResponses(t *testing.T) {
	s := startSession(t)
	s.ed.mu.Lock()
	block := make(chan struct{})
	s.ed.block = block
	s.ed.mu.Unlock()
	_, ch := s.start("tools/call", withMeta(map[string]any{"name": "list_buffers", "arguments": map[string]any{}}))
	_ = s.in.Close()
	time.Sleep(50 * time.Millisecond)
	close(block)
	select {
	case m := <-ch:
		if m["error"] != nil {
			t.Errorf("応答 = %v", m)
		}
	case <-time.After(testTimeout):
		t.Fatal("EOF の後に応答が来ない")
	}
	// serveMCP は応答を書いた後に接続を閉じるので、クライアントの読み取りが終わる。
	select {
	case <-s.mcpClient.done:
	case <-time.After(testTimeout):
		t.Fatal("serveMCP が接続を閉じない")
	}
}
