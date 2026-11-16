package editor

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// 拡張機能のコマンドは、コマンドラインから ID で呼べ、ID の後の文字列を command/execute の args の text で渡す。
// 返った編集は、ふだんのコマンドと同じく適用し、u で戻せる。知らない ID は E492 にする。
//
// req: FR-11
func TestExtCommandLine(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\ndef\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.keys(":fake.echo こんにちは | \"世界\"<CR>")
	h.eventually("args の表示", func() bool { return h.e.message.Text == `args={"text":"こんにちは | \"世界\""}` })
	h.keys(":fake.echo<CR>")
	// 引数が無ければ args に text を入れない（表示は空か null）。
	h.eventually("args の無い表示", func() bool { return h.e.message.Text == "args=" || h.e.message.Text == "args=null" })
	h.keys(":fake.upper<CR>")
	h.settle()
	if got := h.text(); got != "ABC\ndef" {
		t.Errorf(":fake.upper: text = %q", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc\ndef" {
		t.Errorf("u: text = %q", got)
	}
	h.keys(":fake.nothing x<CR>")
	h.settle()
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf("知らない ID: message = %q", msg)
	}
}

// 期限 0 の結果の破棄の知らせは、同じ拡張機能の同じ操作が続けば、行を増やさずに回数を書き添える。
// 別の操作か、知らせが他のメッセージで消えた後は、1 回目から数え直す。
//
// req: FR-18
func TestNoteDiscard(t *testing.T) {
	e := &Editor{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	slow := editcmd.Ref{Extension: "fake", ID: "fake.slow"}
	e.noteDiscard(slow)
	e.noteDiscard(slow)
	if want := "拡張機能 fake の fake.slow の結果を破棄しました（問い合わせの後にバッファかカーソルが変わったため）（2 回）"; e.message.Text != want {
		t.Errorf("message = %q, want %q", e.message.Text, want)
	}
	e.noteDiscard(editcmd.Ref{Extension: "fake", ID: "fake.line"})
	if strings.Contains(e.message.Text, "回）") {
		t.Errorf("別の操作で数え直していない: %q", e.message.Text)
	}
	e.message = noMessage
	e.noteDiscard(slow)
	if strings.Contains(e.message.Text, "回）") {
		t.Errorf("消えた後に数え直していない: %q", e.message.Text)
	}
}

// buildACP は、ACP の拡張機能と、そのテスト用のエージェント（cmd/modal-editor-ext-acp/testdata/fakeagent）を
// 一時ディレクトリへビルドし、拡張機能の定義を返す。設定の agent.command にテスト用のエージェントを書く。
func buildACP(t *testing.T) exthost.Spec {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	src := filepath.Join("..", "..", "cmd", "modal-editor-ext-acp")
	dir := t.TempDir()
	agent := filepath.Join(dir, "fakeagent")
	for _, b := range []struct{ out, pkg string }{
		{filepath.Join(dir, "modal-editor-ext-acp"), "./" + src},
		{agent, "./" + filepath.Join(src, "testdata", "fakeagent")},
	} {
		out, err := exec.Command("go", "build", "-o", b.out, b.pkg).CombinedOutput() //nolint:gosec // テストのために一時ディレクトリへビルドする
		if err != nil {
			t.Fatalf("build %s: %v\n%s", b.pkg, err, out)
		}
	}
	raw, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{"agent": map[string]any{"command": agent}})
	if err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir, Settings: settings}
}

// :acp.prompt の後の本文は、ACP の拡張機能を通ってテスト用のエージェントへプロンプトとして届き、
// エージェントの応答が acp の欄に書き足される。引数を取らない :acp.cancel に引数を付けると失敗を知らせる。
//
// req: FR-34
func TestACPPromptFromCommandLine(t *testing.T) {
	spec := buildACP(t)
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, "a.txt")
	output := func() string {
		var sb strings.Builder
		for _, lines := range h.e.outputs {
			sb.WriteString(strings.Join(lines, "\n"))
		}
		return sb.String()
	}
	h.keys(`:acp.prompt {"op":"hello"}<CR>`)
	h.eventually("エージェントの応答", func() bool { return strings.Contains(output(), "[end_turn]") })
	var out string
	h.e.do(func() { out = output() })
	for _, want := range []string{`> {"op":"hello"}`, "Hello, world.", "Bye."} {
		if !strings.Contains(out, want) {
			t.Errorf("output に %q が無い:\n%s", want, out)
		}
	}
	h.keys(":acp.cancel now<CR>")
	h.eventually("引数の誤りの知らせ", func() bool { return h.e.message.Text == "acp.cancel: 引数を取らない" })
}
