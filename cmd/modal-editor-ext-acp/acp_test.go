package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// TestPromptUpdates は、プロンプトを送ると、エージェントを起動してセッションを作り、session/update の更新を
// acp の欄へ書き足すことを確かめる。本文の塊はそのまま、tool_call、tool_call_update、plan は 1 行の要約になり、
// 表示しない種類（agent_thought_chunk）は書かない。
//
// req: FR-34
func TestPromptUpdates(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "hello"})
	out := h.waitOutput("[end_turn]")
	want := `> {"op":"hello"}
Hello, world.
[tool] Read main.go (read, pending) /work/main.go:3
[tool call_1] (completed)
[plan] Read (completed); Fix (pending)
Bye.
[end_turn]
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

// TestSessionSetup は、initialize でファイルの読み書きの能力を申し出て端末を申し出ないことと、
// session/new で作業ディレクトリと MCP の中継コマンドを渡すことを確かめる（基本設計 6.5）。
//
// req: FR-34
func TestSessionSetup(t *testing.T) {
	bridge := filepath.Join(t.TempDir(), mcpBridgeName)
	s := fakeSettings()
	s.MCPBridge = bridge
	h := newHost(t, s, func(k string) string {
		if k == envMCPSocket {
			return "/run/modal-editor/test.sock"
		}
		return ""
	})
	h.prompt(map[string]any{"op": "session"})
	out := h.waitOutput("[end_turn]")
	var got struct {
		Session sessionNewParams   `json:"session"`
		Caps    clientCapabilities `json:"caps"`
	}
	line := out[strings.Index(out, "session=")+len("session=") : strings.Index(out, "\n[end_turn]")]
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Cwd != wd {
		t.Errorf("cwd = %q, want %q", got.Session.Cwd, wd)
	}
	want := []mcpServer{{Name: mcpServerName, Command: bridge, Args: []string{"--socket", "/run/modal-editor/test.sock"}, Env: []envVariable{}}}
	if len(got.Session.MCPServers) != 1 || got.Session.MCPServers[0].Command != want[0].Command ||
		!slices.Equal(got.Session.MCPServers[0].Args, want[0].Args) || got.Session.MCPServers[0].Name != want[0].Name {
		t.Errorf("mcpServers = %+v, want %+v", got.Session.MCPServers, want)
	}
	if !got.Caps.FS.ReadTextFile || !got.Caps.FS.WriteTextFile || got.Caps.Terminal {
		t.Errorf("clientCapabilities = %+v, want fs read and write without terminal", got.Caps)
	}
}

// TestSessionWithoutSocket は、本体が MCP のソケットを渡していなければ、MCP サーバーを渡さないことを確かめる。
func TestSessionWithoutSocket(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "session"})
	out := h.waitOutput("[end_turn]")
	if !strings.Contains(out, `"mcpServers":[]`) {
		t.Errorf("output = %q, want empty mcpServers", out)
	}
}

// TestMCPBridgePath は、中継コマンドを、拡張機能の実行ファイルと同じディレクトリと、配布物の形（2 つ上）から探すことを確かめる。
func TestMCPBridgePath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bridge string
	}{
		{"same directory", filepath.Join("extensions", "acp", mcpBridgeName)},
		{"dist layout", mcpBridgeName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			exe := filepath.Join(dir, "extensions", "acp", "modal-editor-ext-acp")
			if err := os.MkdirAll(filepath.Dir(exe), 0o750); err != nil {
				t.Fatal(err)
			}
			bridge := filepath.Join(dir, tc.bridge)
			if err := os.WriteFile(bridge, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			c := newClient()
			c.executable = func() (string, error) { return exe, nil }
			if got := c.mcpBridgePath(); got != bridge {
				t.Errorf("mcpBridgePath() = %q, want %q", got, bridge)
			}
		})
	}
}

// TestProtocolVersionMismatch は、エージェントが対応しない ACP の版を返したら、プロンプトを失敗として知らせ、エージェントを終わらせることを確かめる。
func TestProtocolVersionMismatch(t *testing.T) {
	t.Setenv("FAKEAGENT_PROTOCOL_VERSION", "2")
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "hello"})
	h.waitMessage("ACP の版 2")
	h.waitIdle()
	h.c.mu.Lock()
	ag := h.c.agent
	h.c.mu.Unlock()
	if ag != nil {
		t.Error("the agent remains after initialize failed")
	}
}

// TestPermission は、session/request_permission を choice/ask に写し、利用者の選択か取り消しを outcome で返すことを確かめる。
//
// req: FR-34
func TestPermission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer ext.ChoiceAskResult
		want   string
	}{
		{"selected", ext.ChoiceAskResult{Selected: "reject-once"}, `permission={"outcome":{"outcome":"selected","optionId":"reject-once"}}`},
		{"cancelled", ext.ChoiceAskResult{Cancelled: true}, `permission={"outcome":{"outcome":"cancelled"}}`},
		{"unknown choice", ext.ChoiceAskResult{Selected: "nope"}, `permission={"outcome":{"outcome":"cancelled"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHost(t, fakeSettings(), nil)
			h.prompt(map[string]any{"op": "permission"})
			ask := h.waitAsk()
			if want := "エージェントが権限を求めている: Edit main.go (edit)"; ask.Title != want {
				t.Errorf("title = %q, want %q", ask.Title, want)
			}
			wantChoices := []ext.Choice{{ID: "allow-once", Label: "Allow once"}, {ID: "reject-once", Label: "Reject"}}
			if !slices.Equal(ask.Choices, wantChoices) {
				t.Errorf("choices = %v, want %v", ask.Choices, wantChoices)
			}
			h.answers <- tc.answer
			out := h.waitOutput("[end_turn]")
			if !strings.Contains(out, tc.want) {
				t.Errorf("output = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestCancelDuringPermission は、acp.cancel で、session/cancel を送り、応答を待っている権限の確認を取り下げて cancelled を返し、
// エージェントの cancelled の stopReason を欄へ書くことを確かめる（ACP の Prompt Turn の Cancellation）。
//
// req: FR-34
func TestCancelDuringPermission(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "permission"})
	h.waitAsk()
	if res := h.exec(cmdCancel, nil); !res.OK {
		t.Fatalf("%s: ok = false", cmdCancel)
	}
	out := h.waitOutput("[cancelled]")
	if !strings.Contains(out, `permission={"outcome":{"outcome":"cancelled"}}`) {
		t.Errorf("output = %q, want the cancelled outcome", out)
	}
	select {
	case <-h.withdrawn:
	default:
		t.Error("choice/ask was not withdrawn")
	}
}

// TestCancelAndBusy は、処理中のプロンプトがあれば次の acp.prompt を失敗させ、acp.cancel で取り消せることと、
// 処理中のプロンプトが無ければ acp.cancel が失敗することを確かめる。
//
// req: FR-34
func TestCancelAndBusy(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	if res := h.exec(cmdCancel, nil); res.OK {
		t.Errorf("%s without a prompt: ok = true", cmdCancel)
	}
	h.prompt(map[string]any{"op": "hang"})
	h.waitOutput(`> {"op":"hang"}`)
	if res := h.exec(cmdPrompt, promptArgs{Text: `{"op":"hello"}`}); res.OK {
		t.Errorf("%s while busy: ok = true", cmdPrompt)
	}
	// session/new の応答の前に取り消すと、session/cancel を送れないので、セッションができるまで待つ。
	waitSession(t, h)
	if res := h.exec(cmdCancel, nil); !res.OK {
		t.Fatalf("%s: ok = false", cmdCancel)
	}
	h.waitOutput("[cancelled]")
	h.waitIdle()
	h.prompt(map[string]any{"op": "hello"})
	h.waitOutput("[end_turn]")
}

// TestCancelStubbornAgent は、acp.cancel の後、期限までにプロンプトに応答しないエージェントを止め、
// プロンプトを cancelled で終えて、次の acp.prompt で起動し直すことを確かめる（PR #94 のレビューの推奨 1）。
//
// req: FR-34
func TestCancelStubbornAgent(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "stubborn"})
	waitSession(t, h)
	if res := h.exec(cmdCancel, nil); !res.OK {
		t.Fatalf("%s: ok = false", cmdCancel)
	}
	h.waitOutput("[cancelled]")
	h.waitMessage("取り消しに応じなかった")
	h.waitIdle()
	for _, m := range h.messages() {
		if strings.Contains(m.Text, "エージェントが終了した") {
			t.Errorf("message = %q, want no abnormal-exit message for a stopped agent", m.Text)
		}
	}
	h.prompt(map[string]any{"op": "hello"})
	h.waitOutput("[end_turn]")
}

// TestPermissionFillsToolCall は、権限の確認の toolCall が toolCallId だけでも、同じプロンプトの tool_call で届いた
// title、kind、locations で問いの文を補うことを確かめる（PR #94 のレビューの推奨 2）。
//
// req: FR-34
func TestPermissionFillsToolCall(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "bare-permission"})
	ask := h.waitAsk()
	if want := "エージェントが権限を求めている: Write notes.txt (edit) /work/notes.txt"; ask.Title != want {
		t.Errorf("title = %q, want %q", ask.Title, want)
	}
	h.answers <- ext.ChoiceAskResult{Selected: "allow-once"}
	out := h.waitOutput("[end_turn]")
	if want := `permission={"outcome":{"outcome":"selected","optionId":"allow-once"}}`; !strings.Contains(out, want) {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestWriteInvalidUTF8 は、不正な UTF-8 を含むファイルへの fs/write_text_file が、開かずにすぐ誤りで答えることを確かめる
// （PR #94 のレビューの推奨 3）。
//
// req: FR-34
func TestWriteInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin.txt")
	if err := os.WriteFile(path, []byte("a\xffb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHost(t, fakeSettings(), nil)
	// 複製を待つ期限（テストでは testTimeout）より十分短く答えることを確かめる。
	start := time.Now()
	h.prompt(map[string]any{"op": "write", "path": path, "content": "x\n"})
	out := h.waitOutput("[end_turn]")
	if !strings.Contains(out, "writeerr=") || !strings.Contains(out, "not valid UTF-8") {
		t.Errorf("output = %q, want a UTF-8 error", out)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v, want an immediate error", d)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.opens) != 0 {
		t.Errorf("editor/open = %v, want none", h.opens)
	}
	assertFile(t, path, "a\xffb\n")
}

func waitSession(t *testing.T, h *host) {
	t.Helper()
	h.waitFor(func() bool {
		h.c.mu.Lock()
		defer h.c.mu.Unlock()
		return h.c.agent != nil && h.c.agent.sessionID != ""
	})
}

// TestPromptArgs は、本文が空のプロンプトと、エージェントの設定が無いときに、acp.prompt が失敗することを確かめる。
func TestPromptArgs(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	if res := h.exec(cmdPrompt, promptArgs{Text: " "}); res.OK {
		t.Error("empty prompt: ok = true")
	}
	if res := h.exec(cmdPrompt, nil); res.OK {
		t.Error("no args: ok = true")
	}
	h2 := newHost(t, settings{}, nil)
	if res := h2.exec(cmdPrompt, promptArgs{Text: "hi"}); res.OK {
		t.Error("no agent: ok = true")
	}
	h2.waitMessage("agent.command")
}

// TestReadTextFile は、fs/read_text_file が、開いているファイルは未保存の変更を含む複製から、
// 開いていないファイルはファイルから読み、line と limit の範囲を返すことを確かめる（基本設計 6.5）。
//
// req: FR-34
func TestReadTextFile(t *testing.T) {
	dir := t.TempDir()
	openPath := filepath.Join(dir, "open.txt")
	if err := os.WriteFile(openPath, []byte("on disk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	closedPath := filepath.Join(dir, "closed.txt")
	if err := os.WriteFile(closedPath, []byte("a\nb\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	one, two := 1, 2
	for _, tc := range []struct {
		name  string
		op    map[string]any
		wants string
	}{
		{"open buffer", map[string]any{"op": "read", "path": openPath}, `read="unsaved\nedit\n"`},
		{"open buffer line", map[string]any{"op": "read", "path": openPath, "line": two}, `read="edit\n"`},
		{"file", map[string]any{"op": "read", "path": closedPath}, `read="a\nb\nc\n"`},
		{"file line limit", map[string]any{"op": "read", "path": closedPath, "line": two, "limit": one}, `read="b\n"`},
		{"file past end", map[string]any{"op": "read", "path": closedPath, "line": 9}, `read=""`},
		{"relative", map[string]any{"op": "read", "path": "closed.txt"}, `readerr=`},
		{"missing", map[string]any{"op": "read", "path": filepath.Join(dir, "missing.txt")}, `readerr=`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHost(t, fakeSettings(), nil)
			h.open(2, openPath, "on disk")
			h.change(2, "unsaved\nedit")
			h.prompt(tc.op)
			out := h.waitOutput("[end_turn]")
			if !strings.Contains(out, tc.wants) {
				t.Errorf("output = %q, want %q", out, tc.wants)
			}
		})
	}
}

// TestWriteOpenFile は、開いているファイルへの fs/write_text_file が、バッファの全文を置き換え、
// 保存の前の整形を通さずに（skipHooks）書き出すことを確かめる（基本設計 6.5）。
//
// req: FR-34
func TestWriteOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHost(t, fakeSettings(), nil)
	h.open(2, path, "old")
	h.prompt(map[string]any{"op": "write", "path": path, "content": "package main\n\nfunc main() {}\n"})
	out := h.waitOutput("[end_turn]")
	if !strings.Contains(out, "write={}") {
		t.Fatalf("output = %q, want write={}", out)
	}
	if text, _ := h.bufferText(2); text != "package main\n\nfunc main() {}" {
		t.Errorf("buffer = %q", text)
	}
	assertFile(t, path, "package main\n\nfunc main() {}\n")
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.opens) != 0 {
		t.Errorf("editor/open = %v, want none for an open file", h.opens)
	}
	if len(h.saves) != 1 || !h.saves[0].SkipHooks || h.saves[0].Buffer != 2 {
		t.Errorf("buffer/save = %+v, want one with skipHooks", h.saves)
	}
}

// TestWriteUnopenedFile は、開いていないファイルへの fs/write_text_file が、editor/open でカーソルを移さずに開き、
// バッファを通して書き出すこと（#43 の 3 の案 A）を、既存のファイルと無いファイルで確かめる。
//
// req: FR-34
func TestWriteUnopenedFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		initial *string
	}{
		{"existing", ptr("old line\nsecond\n")},
		{"new", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file.txt")
			if tc.initial != nil {
				if err := os.WriteFile(path, []byte(*tc.initial), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := newHost(t, fakeSettings(), nil)
			h.prompt(map[string]any{"op": "write", "path": path, "content": "new\ncontent\n"})
			out := h.waitOutput("[end_turn]")
			if !strings.Contains(out, "write={}") {
				t.Fatalf("output = %q, want write={}", out)
			}
			assertFile(t, path, "new\ncontent\n")
			h.mu.Lock()
			opens, saves := h.opens, h.saves
			h.mu.Unlock()
			if len(opens) != 1 || opens[0].Path != path || opens[0].Focus || opens[0].Position != nil {
				t.Errorf("editor/open = %+v, want one without focus", opens)
			}
			if len(saves) != 1 || !saves[0].SkipHooks {
				t.Errorf("buffer/save = %+v, want one with skipHooks", saves)
			}
			// 書いた後は開いているファイルなので、次の読み取りは複製から読む。
			h.prompt(map[string]any{"op": "read", "path": path})
			out = h.waitOutput(`read="new\ncontent\n"`)
			_ = out
		})
	}
}

// TestWriteFinalNewline は、fs/write_text_file で書き出したファイルの末尾の改行が、バッファの FinalNewline によらず
// content と一致することを確かめる。本体の役は、末尾の改行の無いファイルを FinalNewline が false のバッファとして開く
// （末尾の改行を補う設定を無効にした本体と同じ）。
//
// req: FR-34
func TestWriteFinalNewline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initial  *string
		content  string
		wantFile string
	}{
		// FinalNewline が false のバッファへ、改行で終わる content を書く。
		{"no eol, content with eol", ptr("old"), "a\nb\n", "a\nb\n"},
		{"no eol, content without eol", ptr("old"), "a\nb", "a\nb"},
		{"no eol, content of one newline", ptr("old"), "\n", "\n"},
		// FinalNewline が true のバッファ。
		{"eol, content with eol", ptr("old\n"), "a\nb\n", "a\nb\n"},
		{"eol, content with two eols", ptr("old\n"), "a\n\n", "a\n\n"},
		// 行の無いバッファ（無いファイル）は、編集で行ができると改行を補うので、true と同じに扱う。
		{"new file, content with eol", nil, "a\nb\n", "a\nb\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file.txt")
			if tc.initial != nil {
				if err := os.WriteFile(path, []byte(*tc.initial), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			h := newHost(t, fakeSettings(), nil)
			h.prompt(map[string]any{"op": "write", "path": path, "content": tc.content})
			out := h.waitOutput("[end_turn]")
			if !strings.Contains(out, "write={}") {
				t.Fatalf("output = %q, want write={}", out)
			}
			assertFile(t, path, tc.wantFile)
		})
	}
}

// TestBufferText は、fs/write_text_file の content から作るバッファのテキストを、FinalNewline ごとに確かめる。
// FinalNewline が true のバッファへ改行で終わらない content を書くと、本体が書き出すときに改行を足す（Vim の 'fixendofline'）。
func TestBufferText(t *testing.T) {
	for _, tc := range []struct {
		content, cur string
		finalNewline bool
		want         string
	}{
		{"a\nb\n", "x", true, "a\nb"},
		{"a\nb", "x", true, "a\nb"},
		{"", "x", true, ""},
		{"a\nb\n", "x", false, "a\nb\n"},
		{"a\nb", "x", false, "a\nb"},
		{"a\nb\n", "", false, "a\nb"},
		{"a\nb\n", "", true, "a\nb"},
	} {
		if got := bufferText(tc.content, tc.cur, tc.finalNewline); got != tc.want {
			t.Errorf("bufferText(%q, %q, %v) = %q, want %q", tc.content, tc.cur, tc.finalNewline, got, tc.want)
		}
	}
}

// TestWriteRetriesOnConflict は、拡張機能の複製が本体より古く、buffer/edit が適用されなかったときに、
// 新しい複製を待って書き直すことを確かめる。
//
// req: FR-34
func TestWriteRetriesOnConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.txt")
	h := newHost(t, fakeSettings(), nil)
	h.open(2, path, "v1")
	// 本体の役のバッファだけを進め、buffer/change を後から送る。拡張機能の最初の buffer/edit は版の不一致で適用されない。
	h.mu.Lock()
	b := h.bufs[2]
	b.text, b.version = "v2", 2
	h.mu.Unlock()
	h.prompt(map[string]any{"op": "write", "path": path, "content": "agent\n"})
	h.waitFor(func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.edits) > 0
	})
	h.mu.Lock()
	if len(h.edits) != 1 || h.edits[0].Transaction.BaseVersion != 1 {
		t.Errorf("buffer/edit = %+v, want one based on version 1", h.edits)
	}
	h.mu.Unlock()
	_ = h.conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 2, FromVersion: 1, ToVersion: 2,
		Edits: []ext.Edit{{Range: ext.Range{End: ext.Position{Byte: 2}}, Text: "v2"}},
	})
	out := h.waitOutput("[end_turn]")
	if !strings.Contains(out, "write={}") {
		t.Fatalf("output = %q, want write={}", out)
	}
	assertFile(t, path, "agent\n")
}

// TestAgentCrash は、エージェントの異常終了を利用者へ知らせ、次のプロンプトでエージェントを起動し直すことを確かめる。
//
// req: FR-34
func TestAgentCrash(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "crash"})
	m := h.waitMessage("エージェントが終了した")
	if m.Level != ext.MessageError {
		t.Errorf("level = %q, want error", m.Level)
	}
	h.waitOutput("[エージェントが終了した: exit status 3]")
	h.waitIdle()
	h.prompt(map[string]any{"op": "hello"})
	h.waitOutput("[end_turn]")
}

func TestSliceLines(t *testing.T) {
	one, two, three, zero := 1, 2, 3, 0
	for _, tc := range []struct {
		content     string
		line, limit *int
		want        string
	}{
		{"a\nb\nc\n", nil, nil, "a\nb\nc\n"},
		{"a\nb\nc\n", &two, nil, "b\nc\n"},
		{"a\nb\nc", &three, nil, "c"},
		{"a\nb\nc\n", nil, &two, "a\nb\n"},
		{"a\nb\nc\n", &one, &zero, ""},
		{"a\nb\nc\n", &three, &two, "c\n"},
		{"", &one, nil, ""},
	} {
		if got := sliceLines(tc.content, tc.line, tc.limit); got != tc.want {
			t.Errorf("sliceLines(%q, %v, %v) = %q, want %q", tc.content, deref(tc.line), deref(tc.limit), got, tc.want)
		}
	}
}

func TestToolSummary(t *testing.T) {
	line := 7
	got := toolSummary("Edit", "edit", "in_progress", []toolCallLocation{{Path: "/a.go", Line: &line}, {Path: "/b.go"}})
	if want := "Edit (edit, in_progress) /a.go:7 /b.go"; got != want {
		t.Errorf("toolSummary = %q, want %q", got, want)
	}
	if got := toolSummary("", "", "failed", nil); got != "(failed)" {
		t.Errorf("toolSummary = %q, want (failed)", got)
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Errorf("file %s = %q, want %q", path, data, want)
	}
}
