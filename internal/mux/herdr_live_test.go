package mux

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHerdrLive は、実物の herdr で、この層の操作を 1 回ずつ動かす。
// 利用者の herdr のセッションに一時的なペインとポップアップが出て、フォーカスが一瞬移るので、
// 環境変数 MODAL_EDITOR_TEST_HERDR=1 を付けて herdr の中で打ったときだけ動かす（CI では動かない）。
//
// 同梱するプラグインの宣言ファイルを一時ディレクトリに書いて herdr に登録し、終わったら登録を消す。
// 作ったペインとポップアップは閉じ、フォーカスは元のペインへ戻す。
func TestHerdrLive(t *testing.T) {
	if os.Getenv("MODAL_EDITOR_TEST_HERDR") != "1" {
		t.Skip("MODAL_EDITOR_TEST_HERDR=1 のときだけ動かす")
	}
	m := Detect(os.Getenv)
	h, ok := m.(*Herdr)
	if !ok {
		t.Skip("herdr の中ではない")
	}
	ctx := testCtx(t)

	// 同じ ID のプラグインが既に登録されていたら、利用者のものを壊さないように止める。
	var list struct {
		Plugins []struct {
			PluginID string `json:"plugin_id"`
		} `json:"plugins"`
	}
	if err := h.call(ctx, "plugin.list", map[string]any{}, &list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Plugins {
		if p.PluginID == HerdrPluginID {
			t.Skipf("プラグイン %s が既に登録されている", HerdrPluginID)
		}
	}
	pluginDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pluginDir, "herdr-plugin.toml"), HerdrPluginManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.call(ctx, "plugin.link", map[string]any{"path": pluginDir}, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.call(testCtx(t), "plugin.unlink", map[string]any{"plugin_id": HerdrPluginID}, nil); err != nil {
			t.Errorf("plugin.unlink: %v", err)
		}
	})

	pane, err := h.CurrentPane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unit, err := h.Unit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CurrentPane = %s, Unit = %s", pane, unit)

	// ペインを分割して開く。
	out := filepath.Join(t.TempDir(), "split")
	split, err := h.SplitPane(ctx, SplitOptions{
		Target:    pane,
		Direction: Right,
		Command:   Command{Args: writeCommand(out), Env: map[string]string{"V": "it's split"}, Dir: os.TempDir()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.ClosePane(testCtx(t), split) })
	lines := strings.Split(waitFile(t, out), "\n")
	t.Logf("SplitPane = %s, V = %q, pwd = %s", split, lines[0], lines[1])
	if lines[0] != "it's split" {
		t.Errorf("V = %q", lines[0])
	}

	// フォーカスを移し、利用者のフォーカスを元へ戻す。
	var cur struct {
		Pane herdrPane `json:"pane"`
	}
	if err = h.call(ctx, "pane.current", map[string]any{}, &cur); err != nil {
		t.Fatal(err)
	}
	if err = h.FocusPane(ctx, split); err != nil {
		t.Fatal(err)
	}
	var after struct {
		Pane herdrPane `json:"pane"`
	}
	if err = h.call(ctx, "pane.current", map[string]any{}, &after); err != nil {
		t.Fatal(err)
	}
	if err = h.FocusPane(ctx, cur.Pane.PaneID); err != nil {
		t.Errorf("フォーカスを %s へ戻せない: %v", cur.Pane.PaneID, err)
	}
	t.Logf("FocusPane: フォーカスされたペイン %s → %s → %s", cur.Pane.PaneID, after.Pane.PaneID, cur.Pane.PaneID)
	if after.Pane.PaneID != split {
		t.Errorf("FocusPane の後のフォーカス = %s, want %s", after.Pane.PaneID, split)
	}

	// ペインを閉じる。
	if err = h.ClosePane(ctx, split); err != nil {
		t.Fatal(err)
	}
	if _, err = h.pane(ctx, split); err == nil {
		t.Errorf("ClosePane の後もペイン %s がある", split)
	}

	// ポップアップを開いて、外から閉じる。
	out = filepath.Join(t.TempDir(), "popup")
	if err = h.OpenPopup(ctx, PopupOptions{
		Target: pane, Width: 60, Height: 50,
		Command: Command{Args: writeCommand(out), Env: map[string]string{"V": "popup"}},
	}); err != nil {
		t.Fatal(err)
	}
	lines = strings.Split(waitFile(t, out), "\n")
	pid, err := strconv.Atoi(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	// 開いている間の 2 つ目は ErrPopupBusy になる。
	second := filepath.Join(t.TempDir(), "second")
	err = h.OpenPopup(ctx, PopupOptions{Target: pane, Command: Command{Args: writeCommand(second)}})
	t.Logf("2 つ目の OpenPopup: %v", err)
	if !errors.Is(err, ErrPopupBusy) {
		t.Errorf("2 つ目の OpenPopup: err = %v, want ErrPopupBusy", err)
	}
	if err = h.ClosePopup(ctx, pane); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("ClosePopup の後もポップアップの中のプロセス %d が残っている", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("OpenPopup: V = %q, ClosePopup で中のプロセス %d が終わった", lines[0], pid)
}
