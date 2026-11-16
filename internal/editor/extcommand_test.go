package editor

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
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

	// :w の後には置けない（E319）。置けば :w が拡張機能の編集の後の内容を書き出すためである。
	// Vim の :w|<誤りのコマンド> と同じく、誤りの前の :w は行う。拡張機能のコマンドは呼ばないので、書き出すのは元の内容である。
	h.keys(":w|fake.upper<CR>")
	h.settle()
	if got := h.text(); got != "abc\ndef" {
		t.Errorf(":w|fake.upper: text = %q", got)
	}
	if disk, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(disk) != "abc\ndef\n" {
		t.Errorf(":w|fake.upper: disk = %q, %v", disk, err)
	}
	// :s のようにバッファの中で済むコマンドの後には置け、両方の編集が u 1 回で戻る（Vim 9.1 の :s/a/X/|Dup y と同じ）。
	h.keys(":s/b/X/|fake.upper<CR>")
	h.settle()
	if got := h.text(); got != "AXC\ndef" {
		t.Errorf(":s|fake.upper: text = %q", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != "abc\ndef" {
		t.Errorf(":s|fake.upper の後の u: text = %q", got)
	}

	// 拡張機能が無効なら、exthost の内部の文言ではなく、日本語の 1 行で知らせる。
	if err := h.e.host.Disable("fake"); err != nil {
		t.Fatal(err)
	}
	h.eventually("無効", func() bool { st, _ := h.e.host.State("fake"); return st == exthost.StateDisabled })
	h.keys(":fake.echo x<CR>")
	h.settle()
	if want := "拡張機能 fake の fake.echo は使えません: 拡張機能が動いていません（無効か、異常終了の後の再起動の待ち）"; h.message() != want {
		t.Errorf("無効: message = %q, want %q", h.message(), want)
	}
}

// 2 つの拡張機能が同じ ID のコマンドを宣言していたら、先の拡張機能のものを呼び、設定の誤りとして返す。
func TestBuildExCommandsDuplicate(t *testing.T) {
	spec := func(id string, cmds ...string) exthost.Spec {
		var cs []ext.Contribution
		for _, c := range cmds {
			cs = append(cs, ext.Contribution{ID: c})
		}
		return exthost.Spec{Manifest: ext.Manifest{ID: id, Contributes: ext.Contributes{Commands: cs}}}
	}
	cmds, errs := buildExCommands([]exthost.Spec{spec("a", "x.run", "a.only"), spec("b", "x.run", "b.only")})
	want := map[string]string{"x.run": "a", "a.only": "a", "b.only": "b"}
	if !maps.Equal(cmds, want) {
		t.Errorf("cmds = %v, want %v", cmds, want)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "拡張機能 b のコマンド x.run は拡張機能 a のものと重なる") {
		t.Errorf("errs = %v", errs)
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
