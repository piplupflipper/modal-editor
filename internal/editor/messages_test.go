package editor

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// chattySpecs は、ファイルの種類 go のバッファを開くと message/show を続けて送る拡張機能（言語サーバーの拡張機能の代役）を返す。
func chattySpecs(t *testing.T) []exthost.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m := ext.Manifest{
		ID: "chatty", Command: exe, Args: []string{fakeExtFlag, fakeChatty},
		Activation: []ext.Activation{{On: ext.ActivationFileType, Pattern: "go"}},
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return []exthost.Spec{{Manifest: m, Dir: t.TempDir()}}
}

// 起動したときの設定の誤り（古いコマンドを keymap に書いたもの）は、Go のファイルを開いた直後から届く拡張機能の知らせで消えない（#211）。
// 知らせは履歴に残り、利用者が次のキーを打った後は、これまでどおりメッセージの行に出る。
// :messages は、設定の誤りと拡張機能の知らせを古い順に出す。
func TestConfigErrorSurvivesExtMessages(t *testing.T) {
	dir := writeFiles(t, map[string]string{"main.go": "package main\n"})
	km := map[string]map[string]string{"normal": {"<Space>d": ":dap.toggleBreakpoint<CR>"}}
	h := start(t, Config{WorkDir: dir, Keymap: km, Extensions: chattySpecs(t)}, "main.go")
	inHistory := func(text string) bool {
		return slices.ContainsFunc(h.e.msgHistory, func(m ui.Message) bool { return m.Text == text })
	}
	h.eventually("拡張機能の知らせ", func() bool { return inHistory("chatty: loading 3") })
	if msg := h.message(); !strings.Contains(msg, ":dap.toggleBreakpoint は無くなったコマンド") {
		t.Fatalf("起動したときの誤りが消えた: message = %q", msg)
	}

	// 次のキーで誤りが消えた後は、拡張機能の知らせが出る。
	h.keys("l")
	h.settle()
	h.e.do(func() {
		h.e.handleEvent(&exthost.MessageShow{Extension: "chatty", Params: ext.MessageShowParams{Level: ext.MessageInfo, Text: "chatty: ready"}})
	})
	if msg := h.message(); msg != "chatty: ready" {
		t.Errorf("キーの後の知らせ: message = %q", msg)
	}

	// 偽の端末は小さいので、履歴の全件が画面の半分に収まる高さにする（収まらないときは TestMessagesKeepsNewest）。
	h.e.do(func() { h.e.renderer.Height = 40 })
	h.keys(":messages<CR>")
	h.settle()
	want := []string{"keymap.normal の <Space>d の右辺の :dap.toggleBreakpoint は無くなったコマンド", "chatty: loading 1", "chatty: loading 3", "chatty: ready"}
	msg := h.message()
	last := -1
	for _, w := range want {
		i := strings.Index(msg, w)
		if i <= last {
			t.Fatalf(":messages に %q が古い順に無い:\n%s", w, msg)
		}
		last = i
	}
	h.keys("<CR>:mes<CR>")
	h.settle()
	if got := h.message(); got != msg {
		t.Errorf(":mes = %q, want %q", got, msg)
	}
}

// :messages は、画面の半分に収まらなければ新しい側を残し、古い側を 1 行にまとめる（#229 のレビューの推奨 3）。
// :ext sync の失敗も履歴に残る（推奨 6）。
func TestMessagesKeepsNewest(t *testing.T) {
	h := start(t, Config{})
	h.e.do(func() {
		for i := range 40 {
			h.e.noteMessage(ui.Message{Text: fmt.Sprintf("m%d", i+1), Level: ui.MessageError})
		}
		h.e.extSyncFinished(extSyncResult{err: errors.New("boom")})
	})
	h.keys("<CR>:messages<CR>")
	h.settle()
	msg := h.message()
	lines := strings.Split(msg, "\n")
	var height int
	h.e.do(func() { height = h.e.renderer.Height })
	if len(lines) > max(height/2, 2) || !strings.HasPrefix(lines[0], "（前の ") ||
		lines[len(lines)-1] != "拡張機能の取得に失敗しました: boom" || lines[len(lines)-2] != "m40" {
		t.Errorf("height = %d, :messages =\n%s", height, msg)
	}
}

// 拡張機能の知らせは、メッセージの行に出ている本体の誤りを上書きしない。拡張機能の誤りどうしは上書きする。
func TestExtMessageKeepsEditorError(t *testing.T) {
	h := start(t, Config{})
	show := func(level ext.MessageLevel, text string) {
		h.e.do(func() {
			h.e.handleEvent(&exthost.MessageShow{Extension: "x", Params: ext.MessageShowParams{Level: level, Text: text}})
		})
	}
	h.keys(":nosuch<CR>")
	h.settle()
	show(ext.MessageInfo, "info")
	if msg := h.message(); !strings.HasPrefix(msg, "E492") {
		t.Errorf("本体の誤りの後の知らせ: message = %q", msg)
	}
	h.keys("l")
	h.settle()
	show(ext.MessageError, "ext error 1")
	show(ext.MessageInfo, "ext info 2")
	if msg := h.message(); msg != "ext info 2" {
		t.Errorf("拡張機能の誤りの後の知らせ: message = %q", msg)
	}
}
