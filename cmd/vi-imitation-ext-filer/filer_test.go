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
	"slices"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// testTimeout は、本体の役が拡張機能の知らせを待つ上限である。
const testTimeout = 20 * time.Second

// envTestMain が "1" なら、テストの実行ファイルを拡張機能として動かす（拡張機能のプロセスを強制終了するテスト。procattr_linux_test.go）。
const envTestMain = "VI_IMITATION_FILER_TEST_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(envTestMain) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// host は、拡張機能と JSON-RPC で繋いだ本体の役である。editor/state、pane/open、editor/open に答え、知らせを溜める。
type host struct {
	t       *testing.T
	conn    *rpc.Conn
	workDir string
	current string

	panes   chan ext.PaneOpenParams
	updates chan ext.ViewUpdateParams
	opens   chan ext.EditorOpenParams
	closes  chan ext.ViewID
	msgs    chan ext.MessageShowParams
}

// newHostState は、接続の無い本体の役を作る。workDir は作業ディレクトリ、current はカーソルのあるバッファのファイルである。
func newHostState(t *testing.T, workDir, current string) *host {
	return &host{
		t: t, workDir: workDir, current: current,
		panes: make(chan ext.PaneOpenParams, 16), updates: make(chan ext.ViewUpdateParams, 1024),
		opens: make(chan ext.EditorOpenParams, 16), closes: make(chan ext.ViewID, 16), msgs: make(chan ext.MessageShowParams, 1024),
	}
}

// connect は、r と w で拡張機能と繋ぎ、initialize を送る。
func (h *host) connect(r io.Reader, w io.WriteCloser) {
	h.conn = rpc.NewConn(r, w, rpc.HandlerFuncs{Notification: h.notification, Request: h.request})
	var res ext.InitializeResult
	if err := h.conn.Call(h.ctx(), ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion}, &res); err != nil {
		h.t.Fatalf("initialize: %v", err)
	}
}

// newHost は、同じプロセスの中で拡張機能を動かし、本体の役と繋ぐ。
func newHost(t *testing.T, workDir, current string) *host {
	t.Helper()
	h := newHostState(t, workDir, current)
	ctx, cancel := context.WithCancel(context.Background())
	f := newFiler(ctx)
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- f.srv.Serve(ctx, extR, extW) }()
	h.connect(hostR, hostW)
	t.Cleanup(func() {
		cancel()
		f.closeAll()
		_ = h.conn.Close()
		select {
		case err := <-served:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("Serve = %v", err)
			}
		case <-time.After(testTimeout):
			t.Error("Serve did not return after the host closed")
		}
	})
	return h
}

func (h *host) ctx() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	h.t.Cleanup(cancel)
	return ctx
}

func decode[T any](t *testing.T, raw json.RawMessage) T {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Errorf("decode %T: %v", v, err)
	}
	return v
}

func (h *host) notification(_ context.Context, n *rpc.Notification) {
	switch n.Method {
	case ext.MethodViewUpdate:
		h.updates <- decode[ext.ViewUpdateParams](h.t, n.Params)
	case ext.MethodViewClose:
		h.closes <- decode[ext.ViewCloseParams](h.t, n.Params).View
	case ext.MethodMessageShow:
		h.msgs <- decode[ext.MessageShowParams](h.t, n.Params)
	}
}

// testView は、本体の役が pane/open に答える部品の ID である。
const testView ext.ViewID = 7

func (h *host) request(_ context.Context, req *rpc.Request, reply rpc.Replier) {
	switch req.Method {
	case ext.MethodEditorState:
		_ = reply(ext.EditorStateResult{Buffer: 1, Buffers: []ext.BufferInfo{{Buffer: 1, Path: h.current}}, WorkDir: h.workDir}, nil)
	case ext.MethodPaneOpen:
		h.panes <- decode[ext.PaneOpenParams](h.t, req.Params)
		_ = reply(ext.PaneOpenResult{View: testView}, nil)
	case ext.MethodEditorOpen:
		h.opens <- decode[ext.EditorOpenParams](h.t, req.Params)
		_ = reply(ext.EditorOpenResult{Buffer: 2, Version: 1, Moved: false}, nil)
	default:
		_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: req.Method})
	}
}

// command は、コマンド id を command/execute で呼ぶ。ファイラはバッファを読まないので、バッファを送らない形（withheld）で呼ぶ。
func (h *host) command(id string) {
	h.t.Helper()
	var res ext.CommandExecuteResult
	if err := h.conn.Call(h.ctx(), ext.MethodCommandExecute, ext.CommandExecuteParams{ID: id, Withheld: true}, &res); err != nil || !res.OK {
		h.t.Fatalf("command/execute %s: %+v %v", id, res, err)
	}
}

// notify は、本体の役から拡張機能へ知らせを送る。
func (h *host) notify(method string, params any) {
	h.t.Helper()
	if err := h.conn.Notify(method, params); err != nil {
		h.t.Fatalf("%s: %v", method, err)
	}
}

// pane は、pane/open を待つ。
func (h *host) pane() ext.PaneOpenParams {
	h.t.Helper()
	select {
	case p := <-h.panes:
		return p
	case <-time.After(testTimeout):
		h.t.Fatal("no pane/open")
	}
	return ext.PaneOpenParams{}
}

// update は、cond を満たす view/update を待つ。
func (h *host) update(what string, cond func(ext.ViewComponent) bool) ext.ViewComponent {
	h.t.Helper()
	deadline := time.After(testTimeout)
	for {
		select {
		case u := <-h.updates:
			if u.View != testView {
				h.t.Errorf("view/update view = %d", u.View)
			}
			if cond(u.Component) {
				return u.Component
			}
		case <-deadline:
			h.t.Fatalf("no view/update: %s", what)
		}
	}
}

// ping は、ping の応答までの時間を返す。
func (h *host) ping() time.Duration {
	h.t.Helper()
	start := time.Now()
	if err := h.conn.Call(h.ctx(), ext.MethodPing, nil, nil); err != nil {
		h.t.Fatalf("ping: %v", err)
	}
	return time.Since(start)
}

// labels は、木の項目 items の表示の文字列を、字下げ（"  " を深さごと）を付けて並べる。
func labels(items []ext.ViewItem) []string {
	var out []string
	var walk func([]ext.ViewItem, string)
	walk = func(its []ext.ViewItem, indent string) {
		for _, it := range its {
			out = append(out, indent+it.Label)
			walk(it.Children, indent+"  ")
		}
	}
	walk(items, "")
	return out
}

// find は、木の項目 items から ID が id のものを探す。
func find(items []ext.ViewItem, id string) *ext.ViewItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
		if it := find(items[i].Children, id); it != nil {
			return it
		}
	}
	return nil
}

// writeTree は、dir に files（相対パスから内容）を置く。
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeGit は、PATH の先頭に、script を中身にした git を置く。
func fakeGit(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil { //nolint:gosec // テストの偽の git は実行できるようにする
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ディレクトリの子は、ディレクトリを先に、その中を名前のバイトの順に並べる（neo-tree の既定）。
// ディレクトリを指すシンボリックリンクはディレクトリとして扱い、名前が不正な UTF-8 の子は除く。
// git のリポジトリの中では、gitignore されたものに印を付ける（追跡しているファイルは付けない）。外では付けない。
//
// req: FR-105
func TestReadDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore": "*.log\nbuild/\n", "B.txt": "", "a.txt": "", "x.log": "", "kept.log": "", "build/o": "", "src/m.go": "", ".hidden": "",
	})
	if err := os.Symlink(filepath.Join(dir, "src"), filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad\xff"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	show := func(es []entry) []string {
		var out []string
		for _, e := range es {
			s := e.name
			if e.dir {
				s += "/"
			}
			if e.ignored {
				s += " (ignored)"
			}
			out = append(out, s)
		}
		return out
	}
	es, err := readDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"build/", "link/", "src/", ".gitignore", ".hidden", "B.txt", "a.txt", "kept.log", "x.log"}; !slices.Equal(show(es), want) {
		t.Errorf("outside a repository: %q, want %q", show(es), want)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-f", "kept.log"}} {
		if out, gerr := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); gerr != nil { //nolint:gosec // テストのリポジトリを作る
			t.Fatalf("git %v: %v\n%s", args, gerr, out)
		}
	}
	es, err = readDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	// .git は gitignore されたものではないので見せる（neo-tree の hide_dotfiles を false にしたときと同じ）。
	want := []string{".git/", "build/ (ignored)", "link/", "src/", ".gitignore", ".hidden", "B.txt", "a.txt", "kept.log", "x.log (ignored)"}
	if !slices.Equal(show(es), want) {
		t.Errorf("in a repository: %q, want %q", show(es), want)
	}
	// gitignore されたディレクトリの中も gitignore されたものとして扱う。
	es, err = readDir(context.Background(), filepath.Join(dir, "build"))
	if err != nil || !slices.Equal(show(es), []string{"o (ignored)"}) {
		t.Errorf("in an ignored directory: %q %v", show(es), err)
	}
}

// filer.pane は、作業ディレクトリの木を、今のファイルまで開いてカーソルを置いた形で pane/open に渡す（分割したペイン。closeOnSelect は
// 本体が代わりに出す中央のポップアップのため）。filer.popup は placement を center にする。
// 大きなディレクトリを開いて読む間と、git の応答を待つ間も、ping にすぐ答える（dev-cycle 2.2）。
// 項目には、プレビューに出すものとして絶対パスを付ける（FR-149）。アイコンのためのファイル（FR-152）として、絶対パスと
// ディレクトリかどうかを付ける。
//
// req: FR-105, FR-147, FR-149, FR-152
func TestOpenAndLargeDirectory(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "", "sub/b.txt": ""})
	big := filepath.Join(dir, "big")
	if err := os.Mkdir(big, 0o700); err != nil {
		t.Fatal(err)
	}
	const n = 20000
	for i := range n {
		f, err := os.Create(filepath.Join(big, fmt.Sprintf("f%05d", i)))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	// git は 1 秒待ってから「どれも無視されていない」で終わる。
	fakeGit(t, "sleep 1\nexit 1")
	h := newHost(t, dir, filepath.Join(dir, "sub", "b.txt"))

	h.command("filer.pane")
	if d := h.ping(); d > 300*time.Millisecond {
		t.Errorf("ping while reading the root took %v", d)
	}
	p := h.pane()
	if p.Placement != "" || !p.CloseOnSelect || !p.Focus || p.Size != paneSize {
		t.Errorf("pane/open = %+v", p)
	}
	c := p.Component
	if want := []string{"big", "sub", "  b.txt", "a.txt"}; !slices.Equal(labels(c.Items), want) || c.Cursor != filepath.Join(dir, "sub", "b.txt") {
		t.Errorf("tree = %q cursor %q, want %q", labels(c.Items), c.Cursor, want)
	}
	if s := find(c.Items, filepath.Join(dir, "sub")); s == nil || s.Expanded == nil || !*s.Expanded || !s.HasChildren {
		t.Errorf("sub = %+v, want expanded with children", s)
	}
	if b := find(c.Items, big); b == nil || !b.HasChildren || b.Expanded != nil || len(b.Children) != 0 {
		t.Errorf("big = %+v, want an unread directory", b)
	}
	// どの項目にも、プレビューに出すもの（その絶対パス）を付け、部品はプレビューの欄を持つ（FR-149）。
	if b := find(c.Items, filepath.Join(dir, "sub", "b.txt")); !c.Preview || b == nil || b.Preview == nil || b.Preview.Path != filepath.Join(dir, "sub", "b.txt") || b.Preview.Line != nil {
		t.Errorf("preview: component %v, b.txt %+v", c.Preview, b)
	}
	if b := find(c.Items, big); b.Preview == nil || b.Preview.Path != big {
		t.Errorf("directory preview = %+v", b.Preview)
	}
	// どの項目にも、本体がアイコンを付けるファイルかディレクトリ（その絶対パスと、ディレクトリかどうか）を付ける（FR-152）。
	if b := find(c.Items, filepath.Join(dir, "sub", "b.txt")); b.File == nil || *b.File != (ext.ViewFile{Path: filepath.Join(dir, "sub", "b.txt")}) {
		t.Errorf("file of b.txt = %+v", b.File)
	}
	if b := find(c.Items, big); b.File == nil || *b.File != (ext.ViewFile{Path: big, Dir: true}) {
		t.Errorf("file of big = %+v", b.File)
	}

	h.notify(ext.MethodViewExpand, ext.ViewExpandParams{View: testView, Item: big, Expanded: true})
	if d := h.ping(); d > 300*time.Millisecond {
		t.Errorf("ping while reading the large directory took %v", d)
	}
	got := h.update("大きなディレクトリの子", func(c ext.ViewComponent) bool {
		b := find(c.Items, big)
		return b != nil && len(b.Children) == n
	})
	if b := find(got.Items, big); b.Children[0].Label != "f00000" || b.Expanded != nil {
		t.Errorf("big = %+v...", b.Children[0])
	}

	// view/select でファイルを開き、view/tabChange で追従する（分割したペインの木）。
	h.notify(ext.MethodViewSelect, ext.ViewSelectParams{View: testView, Item: filepath.Join(dir, "a.txt"), Selection: 1})
	select {
	case o := <-h.opens:
		if o.Path != filepath.Join(dir, "a.txt") || !o.Focus {
			t.Errorf("editor/open = %+v", o)
		}
	case <-time.After(testTimeout):
		t.Fatal("no editor/open")
	}
	h.notify(ext.MethodViewTabChange, ext.ViewTabChangeParams{View: testView, Buffer: 3, Path: filepath.Join(dir, "big", "f00042")})
	h.update("追従", func(c ext.ViewComponent) bool { return c.Cursor == filepath.Join(dir, "big", "f00042") })

	// filer.pane をもう一度呼ぶと閉じる。
	h.command("filer.pane")
	select {
	case v := <-h.closes:
		if v != testView {
			t.Errorf("view/close %d", v)
		}
	case <-time.After(testTimeout):
		t.Fatal("no view/close")
	}

	h.command("filer.popup")
	if p := h.pane(); p.Placement != ext.PaneCenter || !p.CloseOnSelect {
		t.Errorf("filer.popup pane/open = %+v", p)
	}
}
