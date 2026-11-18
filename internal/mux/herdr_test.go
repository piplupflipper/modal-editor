package mux

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// herdrRequest は、偽の herdr が受けたリクエストである。
type herdrRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

// fakeHerdr は、herdr の socket API の偽物である。受けたリクエストを記録し、reply が返す値を応答する。
// reply が返す値は、result か error の値を持つ map である。nil を返すと応答しない。
type fakeHerdr struct {
	socket string
	reply  func(req herdrRequest) map[string]any

	mu   sync.Mutex
	reqs []herdrRequest
}

// newFakeHerdr は、偽の herdr を一時ディレクトリのソケットで待ち受けさせる。
// ソケットのパスの長さの上限（108 バイト）に収めるため、t.TempDir ではなく短い名前の一時ディレクトリを使う。
func newFakeHerdr(t *testing.T, reply func(req herdrRequest) map[string]any) *fakeHerdr {
	t.Helper()
	dir, err := os.MkdirTemp("", "mux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeHerdr{socket: filepath.Join(dir, "h.sock"), reply: reply}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeHerdr) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req herdrRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, req)
	f.mu.Unlock()
	resp := f.reply(req)
	if resp == nil {
		// 応答しないまま、相手が閉じるまで待つ。
		_, _ = conn.Read(make([]byte, 1))
		return
	}
	if _, ok := resp["id"]; !ok {
		resp["id"] = req.ID
	}
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}

// requests は、受けたリクエストの写しを返す。
func (f *fakeHerdr) requests() []herdrRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]herdrRequest(nil), f.reqs...)
}

// only は、受けたリクエストがちょうど 1 つであることを確かめて返す。
func (f *fakeHerdr) only(t *testing.T) herdrRequest {
	t.Helper()
	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("リクエストの数 = %d, want 1: %+v", len(reqs), reqs)
	}
	return reqs[0]
}

func ok(result map[string]any) map[string]any {
	return map[string]any{"result": result}
}

func herdrErr(code, message string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code, "message": message}}
}

// paneInfo は、pane.get などが返す PaneInfo の偽物である。
func paneInfo(pane, tab string) map[string]any {
	return map[string]any{
		"pane_id": pane, "tab_id": tab, "workspace_id": "wW", "terminal_id": "term_1",
		"focused": false, "agent_status": "unknown", "revision": 0,
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestHerdrCurrentPaneAndUnit(t *testing.T) {
	// HERDR_PANE_ID の古い ID（wT:p90）で引くと、herdr は今の ID（wW:p7）のペインを返す。
	f := newFakeHerdr(t, func(req herdrRequest) map[string]any {
		if req.Method != "pane.get" || req.Params["pane_id"] != "wT:p90" {
			return herdrErr("pane_not_found", "pane not found")
		}
		return ok(map[string]any{"type": "pane_info", "pane": paneInfo("wW:p7", "wW:t4")})
	})
	h := Detect(envOf(map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "wT:p90", "HERDR_SOCKET_PATH": f.socket}))
	ctx := testCtx(t)
	pane, err := h.CurrentPane(ctx)
	if err != nil || pane != "wW:p7" {
		t.Errorf("CurrentPane = %q, %v; want wW:p7", pane, err)
	}
	unit, err := h.Unit(ctx)
	if want := f.socket + ":wW:t4"; err != nil || unit != want {
		t.Errorf("Unit = %q, %v; want %q", unit, err, want)
	}
}

func TestHerdrSplitPane(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any {
		return ok(map[string]any{"type": "plugin_pane_opened", "plugin_pane": map[string]any{
			"plugin_id": HerdrPluginID, "entrypoint": HerdrPluginEntrypoint, "pane": paneInfo("wW:p1W", "wW:t4"),
		}})
	})
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	pane, err := h.SplitPane(testCtx(t), SplitOptions{
		Target:    "wW:p8",
		Direction: Down,
		Size:      30,
		Focus:     true,
		Command: Command{
			Args: []string{"/opt/modal-editor", "--pane", "it's"},
			Env:  map[string]string{"MODAL_EDITOR_SOCKET": "/run/me/server.sock"},
			Dir:  "/work",
		},
	})
	if err != nil || pane != "wW:p1W" {
		t.Fatalf("SplitPane = %q, %v; want wW:p1W", pane, err)
	}
	req := f.only(t)
	want := map[string]any{
		"plugin_id":      HerdrPluginID,
		"entrypoint":     HerdrPluginEntrypoint,
		"placement":      "split",
		"direction":      "down",
		"target_pane_id": "wW:p8",
		"focus":          true,
		"cwd":            "/work",
		"env": map[string]any{
			"MODAL_EDITOR_SOCKET": "/run/me/server.sock",
			HerdrCommandEnv:       `/opt/modal-editor --pane 'it'\''s'`,
		},
	}
	if req.Method != "plugin.pane.open" || !reflect.DeepEqual(req.Params, want) {
		t.Errorf("リクエスト = %s %v\nwant plugin.pane.open %v", req.Method, req.Params, want)
	}
}

func TestHerdrSplitPaneDefaults(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any {
		return ok(map[string]any{"plugin_pane": map[string]any{"pane": paneInfo("wW:p2", "wW:t4")}})
	})
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	if _, err := h.SplitPane(testCtx(t), SplitOptions{Target: "wW:p8", Direction: Right, Command: Command{Args: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	p := f.only(t).Params
	if p["target_pane_id"] != "wW:p8" || p["direction"] != "right" || p["focus"] != false {
		t.Errorf("params = %v, want target wW:p8, right, focus false", p)
	}
	if _, ok := p["cwd"]; ok {
		t.Errorf("Dir が空なのに cwd を送った: %v", p)
	}
}

// TestHerdrEmptyTarget は、元のペインが空なら、server の環境変数のペインを既定にせず ErrNoTarget を返すことを確かめる。
func TestHerdrEmptyTarget(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return ok(map[string]any{"type": "ok"}) })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	cmd := Command{Args: []string{"x"}}
	if _, err := h.SplitPane(testCtx(t), SplitOptions{Command: cmd}); !errors.Is(err, ErrNoTarget) {
		t.Errorf("SplitPane: err = %v, want ErrNoTarget", err)
	}
	if err := h.OpenPopup(testCtx(t), PopupOptions{Command: cmd}); !errors.Is(err, ErrNoTarget) {
		t.Errorf("OpenPopup: err = %v, want ErrNoTarget", err)
	}
	if n := len(f.requests()); n != 0 {
		t.Errorf("リクエストを %d 件送った", n)
	}
}

func TestHerdrOpenPopupBusy(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return herdrErr("ui_busy", "a popup pane is already open") })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	err := h.OpenPopup(testCtx(t), PopupOptions{Target: "wW:p7", Command: Command{Args: []string{"x"}}})
	if !errors.Is(err, ErrPopupBusy) {
		t.Errorf("err = %v, want ErrPopupBusy", err)
	}
	if he, ok := errors.AsType[*HerdrError](err); !ok || he.Code != "ui_busy" {
		t.Errorf("err = %v, want HerdrError ui_busy も含む", err)
	}
}

func TestHerdrSplitPaneWithoutPaneID(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return ok(map[string]any{"type": "ok"}) })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	if _, err := h.SplitPane(testCtx(t), SplitOptions{Target: "wW:p7", Command: Command{Args: []string{"x"}}}); err == nil {
		t.Error("ペインの ID の無い応答を受け付けた")
	}
}

func TestHerdrOpenPopup(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return ok(map[string]any{"type": "ok"}) })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	err := h.OpenPopup(testCtx(t), PopupOptions{
		Target: "wW:p7", Width: 80, Height: 60,
		Command: Command{Args: []string{"modal-editor", "--popup"}, Env: map[string]string{"A": "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"plugin_id":  HerdrPluginID,
		"entrypoint": HerdrPluginEntrypoint,
		"placement":  "popup",
		"width":      "80%",
		"height":     "60%",
		"env":        map[string]any{"A": "1", HerdrCommandEnv: "modal-editor --popup"},
	}
	// herdr 0.9.0 はポップアップの target_pane_id を invalid_params で断るので、送らない。
	if req := f.only(t); req.Method != "plugin.pane.open" || !reflect.DeepEqual(req.Params, want) {
		t.Errorf("リクエスト = %s %v\nwant plugin.pane.open %v", req.Method, req.Params, want)
	}
}

func TestHerdrClosePopup(t *testing.T) {
	code := "popup_not_open"
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return herdrErr(code, "no popup is open") })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	ctx := testCtx(t)
	if err := h.ClosePopup(ctx, ""); err != nil {
		t.Errorf("開いていないポップアップを閉じて誤り: %v", err)
	}
	code = "internal"
	if err := h.ClosePopup(ctx, ""); err == nil {
		t.Error("popup_not_open でない誤りを捨てた")
	}
	for _, req := range f.requests() {
		if req.Method != "popup.close" {
			t.Errorf("method = %s, want popup.close", req.Method)
		}
	}
}

func TestHerdrFocusAndClosePane(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any {
		return ok(map[string]any{"type": "pane_info", "pane": paneInfo("wW:p1", "wW:t1")})
	})
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	ctx := testCtx(t)
	if err := h.FocusPane(ctx, "wW:p7", "wW:p1"); err != nil {
		t.Fatal(err)
	}
	if err := h.ClosePane(ctx, "wW:p2"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range f.requests() {
		got = append(got, r.Method+" "+r.Params["pane_id"].(string))
	}
	if want := []string{"pane.focus wW:p1", "pane.close wW:p2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("リクエスト = %q, want %q", got, want)
	}
}

func TestHerdrError(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return herdrErr("pane_not_found", "pane wW:p9 not found") })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	err := h.FocusPane(testCtx(t), "wW:p7", "wW:p9")
	he, ok := errors.AsType[*HerdrError](err)
	if !ok || he.Code != "pane_not_found" || he.Method != "pane.focus" {
		t.Errorf("err = %v, want HerdrError pane_not_found", err)
	}
}

func TestHerdrResponseIDMismatch(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any {
		return map[string]any{"id": "other", "result": map[string]any{"type": "ok"}}
	})
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	if err := h.ClosePane(testCtx(t), "wW:p1"); err == nil {
		t.Error("ID の違う応答を受け付けた")
	}
}

func TestHerdrContextCanceled(t *testing.T) {
	f := newFakeHerdr(t, func(herdrRequest) map[string]any { return nil })
	h := &Herdr{Socket: f.socket, Pane: "wW:p7"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	err := h.FocusPane(ctx, "wW:p7", "wW:p1")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("取り消してから戻るまで %v かかった", d)
	}
}

func TestHerdrNoServer(t *testing.T) {
	h := &Herdr{Socket: filepath.Join(t.TempDir(), "none.sock"), Pane: "wW:p7"}
	if err := h.ClosePane(testCtx(t), "wW:p1"); err == nil {
		t.Error("herdr のソケットが無いのに誤りにならなかった")
	}
}

// manifestCommand は、宣言ファイルの [[panes]] の command の配列を取り出す。
// TOML の基本の文字列の配列は、ここで使う範囲では JSON の配列と同じ書き方である。
func manifestCommand(t *testing.T) []string {
	t.Helper()
	for line := range strings.SplitSeq(string(HerdrPluginManifest), "\n") {
		if rest, found := strings.CutPrefix(line, "command = "); found {
			var cmd []string
			if err := json.Unmarshal([]byte(rest), &cmd); err != nil {
				t.Fatalf("command を読めない: %v", err)
			}
			return cmd
		}
	}
	t.Fatal("宣言ファイルに command が無い")
	return nil
}

func TestHerdrPluginManifest(t *testing.T) {
	m := string(HerdrPluginManifest)
	for _, want := range []string{
		`id = "` + HerdrPluginID + `"`,
		`id = "` + HerdrPluginEntrypoint + `"`,
		"$" + HerdrCommandEnv,
		`placement = "popup"`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("宣言ファイルに %q が無い", want)
		}
	}
}

// TestHerdrPluginCommandRunsArgs は、宣言ファイルの command が、HerdrCommandEnv に入れたコマンドを
// 引数を崩さずに起動することを、sh で確かめる。
func TestHerdrPluginCommandRunsArgs(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh が無い")
	}
	cmd := manifestCommand(t)
	args := []string{"printf", `%s\n`, "with space", "it's", "$HOME", "a=b"}
	params := (&Herdr{}).openParams("popup", Command{Args: args})
	env := params["env"].(map[string]string)
	ctx := testCtx(t)
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...) //nolint:gosec // 同梱する宣言ファイルの command を試す
	c.Env = append(os.Environ(), HerdrCommandEnv+"="+env[HerdrCommandEnv])
	out, err := c.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "with space\nit's\n$HOME\na=b\n"
	if !bytes.Equal(out, []byte(want)) {
		t.Errorf("出力 = %q, want %q", out, want)
	}
}
