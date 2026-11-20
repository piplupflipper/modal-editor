package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
)

// buildStdExt は、標準の拡張機能 cmd/<name> を一時ディレクトリへビルドし、拡張機能の定義を返す。
func buildStdExt(t *testing.T, name string) exthost.Spec {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	src := filepath.Join("..", "..", "cmd", name)
	dir := t.TempDir()
	out, err := exec.Command("go", "build", "-o", filepath.Join(dir, name), "./"+src).CombinedOutput() //nolint:gosec // テストのために一時ディレクトリへビルドする
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir}
}

// filerTree は、ファイラの木の部品 f の中身と、カーソルの行の項目の ID を返す。イベントループの goroutine で呼ぶ。
func filerTree(f *floatState) (text, item string) {
	if f == nil || f.kind != floatTree {
		return "", ""
	}
	return f.bs.buf.Text(), floatItem(f)
}

// filerDir は、ファイラの試験のディレクトリを作る。git があれば、git のリポジトリにして .gitignore を置く。
func filerDir(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git が無い")
	}
	dir := writeFiles(t, map[string]string{
		".gitignore": "ignored/\n*.log\n", ".hidden": "h\n", "a.txt": "a\n", "z.log": "log\n",
		"sub/b.txt": "b\n", "sub/deep/c.txt": "c\n", "sub/deep/d.txt": "d\n", "ignored/x.txt": "x\n",
	})
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:gosec // テストの一時ディレクトリを git のリポジトリにする
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

// 標準のファイラの拡張機能の <Space>e は、作業ディレクトリの木を中央のポップアップに出し、今のファイルまで開いてカーソルを置く。
// 隠しファイルは見せ、gitignore されたものは隠す（H で切り替える）。l と <CR> はディレクトリを開閉し、ファイルなら選ぶ。
// h は開いたディレクトリを閉じ、それ以外なら親を閉じてそこへ移る。木の中でも / の検索が効く（FR-71）。
// ファイルを選ぶとポップアップが閉じ（FR-147 の closeOnSelect）、元のタブでそのファイルを開く。
//
// req: FR-105, FR-147
func TestStandardFilerPopup(t *testing.T) {
	spec := buildStdExt(t, "vi-imitation-ext-filer")
	dir := filerDir(t)
	// アイコン（FR-152）は TestStandardFilerIcons で確かめ、ここでは木の形だけを見るので切る。
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}, Options: noIcons}, filepath.Join(dir, "sub", "deep", "c.txt"))
	cursorItem := func() string {
		var item string
		h.e.do(func() { _, item = filerTree(h.e.float) })
		return item
	}
	waitTree := func(what, want, item string) {
		t.Helper()
		h.eventually(what, func() bool {
			text, it := filerTree(h.e.float)
			return text == want && it == item
		})
	}
	p := func(rel string) string { return filepath.Join(dir, rel) }

	h.keys("<Space>e")
	waitTree("今のファイルまで開いた木", strings.Join([]string{
		"▸ .git",
		"▾ sub",
		"  ▾ deep",
		"      c.txt",
		"      d.txt",
		"    b.txt",
		"  .gitignore",
		"  .hidden",
		"  a.txt",
	}, "\n"), p("sub/deep/c.txt"))
	if fi, _ := h.float(); !fi.center || !fi.focused {
		t.Errorf("filer.popup: %+v, want a focused center popup", fi)
	}

	// h は、ファイルでは親を閉じてそこへ移り、閉じたディレクトリでも親を閉じてそこへ移る。根の子では何もしない。
	h.keys("h")
	waitTree("h で閉じた deep", "▸ .git\n▾ sub\n  ▸ deep\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p("sub/deep"))
	h.keys("h")
	waitTree("h で閉じた sub", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("sub"))
	h.keys("h")
	h.settle()
	if item := cursorItem(); item != p("sub") {
		t.Errorf("h on a top-level item moved to %q", item)
	}
	// l はディレクトリを開き（開いていれば閉じ）、h は開いたディレクトリを閉じる。
	h.keys("l")
	waitTree("l で開いた sub", "▸ .git\n▾ sub\n  ▸ deep\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p("sub"))
	h.keys("l")
	waitTree("l で閉じた sub", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("sub"))
	h.keys("<CR>")
	waitTree("<CR> で開いた sub", "▸ .git\n▾ sub\n  ▸ deep\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p("sub"))
	h.keys("h")
	waitTree("h で閉じた開いた sub", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("sub"))

	// H で gitignore されたものも見せ、もう一度で隠す。
	h.keys("H")
	waitTree("H で見せた gitignore されたもの", "▸ .git\n▸ ignored\n▸ sub\n  .gitignore\n  .hidden\n  a.txt\n  z.log", p("sub"))
	h.keys("H")
	waitTree("H で隠した gitignore されたもの", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("sub"))

	// 木の中でも / の検索が効き、l でファイルを選ぶと、ポップアップを閉じて元のタブで開く。
	h.keys("/a\\.t<CR>")
	h.settle()
	if item := cursorItem(); item != p("a.txt") {
		t.Fatalf("/ in the tree: cursor on %q", item)
	}
	h.keys("l")
	h.eventually("選んだファイルのタブ", func() bool { return h.e.float == nil && h.e.cur.path == p("a.txt") })

	// 開くたびに今のファイルまで開く。<CR> でファイルを選んでも閉じる。<Space>e をもう一度打つと閉じる。
	h.keys("<Space>e")
	waitTree("開き直した木", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("a.txt"))
	h.keys("<Space>e")
	h.eventually("もう一度の <Space>e で閉じた木", func() bool { return h.e.float == nil })
	h.keys("<Space>e")
	waitTree("3 回目の木", "▸ .git\n▸ sub\n  .gitignore\n  .hidden\n  a.txt", p("a.txt"))
	h.keys("k<CR>")
	h.eventually("<CR> で選んだファイルのタブ", func() bool { return h.e.float == nil && h.e.cur.path == p(".hidden") })
}

// 標準のファイラの拡張機能の <Space>E は、端末多重化ソフトの中では分割したペインに木を出し、出しっぱなしにする。
// ペインで選んだファイルは、ペインを開いた client のタブに開き、ペインは残り、端末多重化ソフトのフォーカスを元の client へ戻す。
// 元の client のタブが替わると、そのファイルまで木を開いてカーソルを置く（追従）。もう一度の <Space>E でペインを閉じる。
// 端末多重化ソフトの外では、<Space>E も中央のポップアップに出し（FR-123）、選んだら閉じる。
//
// req: FR-105, FR-147
func TestStandardFilerPane(t *testing.T) {
	spec := buildStdExt(t, "vi-imitation-ext-filer")
	dir := filerDir(t)
	p := func(rel string) string { return filepath.Join(dir, rel) }
	var calls muxCalls
	cfg := Config{WorkDir: dir, Extensions: []exthost.Spec{spec}, Options: noIcons}
	calls.config(&cfg)
	var mu sync.Mutex
	var focused [][2]string
	cfg.FocusPane = func(from, pane string) error {
		mu.Lock()
		defer mu.Unlock()
		focused = append(focused, [2]string{from, pane})
		return nil
	}
	cfg.PaneFocused = func(string) (bool, error) { return true, nil }
	h := start(t, cfg, p("a.txt"))
	peer := h.attach(ClientInfo{ID: 2, Pane: "%9", Files: []string{p("sub/b.txt")}})

	peer.keys("<Space>E")
	h.eventually("OpenPane", func() bool { _, ok := calls.last(true); return ok })
	call, _ := calls.last(true)
	if call.from != "%9" || call.opts != (PaneOptions{Size: 30, Focus: true}) {
		t.Errorf("OpenPane = %+v", call)
	}
	comp := h.attachComponent(call.view, true)
	comp.waitScreen("b.txt")
	pane := func() *floatState {
		ps := h.e.viewOf(h.clientOf(peer.term)).panes
		if len(ps) != 1 {
			return nil
		}
		return ps[0]
	}
	waitPane := func(what, want, item string) {
		t.Helper()
		h.eventually(what, func() bool {
			text, it := filerTree(pane())
			return text == want && it == item
		})
	}
	waitPane("今のファイルまで開いたペイン", "▸ .git\n▾ sub\n  ▸ deep\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p("sub/b.txt"))

	// ペインで選ぶと、ペインを開いた client のタブに開き、ペインは残り、フォーカスを元の client のペインへ戻す。
	comp.keys("/hidden<CR>l")
	h.eventually("ペインで選んだファイルのタブ", func() bool { return h.e.viewOf(h.clientOf(peer.term)).cur.path == p(".hidden") })
	h.eventually("フォーカスの移動", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(focused) == 1 && focused[0] == [2]string{"%9", "%9"}
	})
	var first string
	h.e.do(func() {
		if pane() == nil {
			t.Error("the pane closed after selecting a file")
		}
		first = h.e.viewOf(h.clientOf(h.term)).cur.path
	})
	if first != p("a.txt") {
		t.Errorf("the first client's tab changed to %q", first)
	}

	// 元の client のタブが替わると、そのファイルまで木を開いてカーソルを置く。
	peer.keys(":e sub/deep/d.txt<CR>")
	waitPane("追従した木", "▸ .git\n▾ sub\n  ▾ deep\n      c.txt\n      d.txt\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p("sub/deep/d.txt"))
	peer.keys("<S-Tab>")
	waitPane("前のタブへの追従", "▸ .git\n▾ sub\n  ▾ deep\n      c.txt\n      d.txt\n    b.txt\n  .gitignore\n  .hidden\n  a.txt", p(".hidden"))

	// もう一度の <Space>E で閉じる（toggle）。
	peer.keys("<Space>E")
	h.eventually("閉じたペインの部品の client の終わり", func() bool { return len(comp.term.exits()) == 1 })
	h.e.do(func() {
		if n := len(h.e.viewOf(h.clientOf(peer.term)).panes); n != 0 {
			t.Errorf("panes after the second <Space>E = %d", n)
		}
	})

	// 端末多重化ソフトの外の client（ペインを知らない）では、中央のポップアップに出し、選んだら閉じる。
	h.keys("<Space>E")
	h.eventually("外の <Space>E の中央のポップアップ", func() bool {
		f := h.e.float
		text, item := filerTree(f)
		return f != nil && f.center && !f.pane && item == p("a.txt") && strings.HasSuffix(text, "  a.txt")
	})
	calls.mu.Lock()
	n := len(calls.panes)
	calls.mu.Unlock()
	if n != 1 {
		t.Errorf("OpenPane calls = %d, want 1 (none for the client outside the multiplexer)", n)
	}
	// .hidden は peer のタブなので（FR-62）、.gitignore を選ぶ。
	h.keys("kk<CR>")
	h.eventually("外の <Space>E で選んだファイル", func() bool {
		return h.e.float == nil && h.e.cur.path == p(".gitignore") &&
			slices.ContainsFunc(h.e.view.tabs, func(b *bufferState) bool { return b.path == p(".gitignore") })
	})
}
