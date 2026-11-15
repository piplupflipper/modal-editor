package editor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/exthost"
)

// lineExtension は、段階 1 の標準の拡張機能 modal-editor-ext-line を一時ディレクトリへビルドし、
// リポジトリの宣言ファイル（manifest.json）を検査して Spec にする（internal/exthost の buildLineExtension にならう）。
func lineExtension(t *testing.T) exthost.Spec {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "modal-editor-ext-line")
	//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "modal-editor-ext-line"), "./"+filepath.ToSlash(src))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir}
}

// 実物の modal-editor-ext-line と宣言ファイルで、拡張機能のテキストオブジェクトを、キー入力、ドットリピート、マクロから使う。
// 既定の期限（100 ms）で、最初の dil は拡張機能の起動を含む。
//
// req: FR-12, FR-17
func TestLineExtension(t *testing.T) {
	if testing.Short() {
		t.Skip("拡張機能のビルドが要る")
	}
	dir := writeFiles(t, map[string]string{"a.txt": "  one  \n  two\nthree\nfour\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{lineExtension(t)}}, "a.txt")
	h.keys("dil")
	h.eventually("dil", func() bool { return h.e.cur.buf.Text() == "    \n  two\nthree\nfour" })
	h.keys("j.")
	h.settle()
	if got := h.text(); got != "    \n  \nthree\nfour" {
		t.Fatalf("j.: text = %q", got)
	}
	h.e.do(func() { h.e.cur.state.Registers().Set('q', command.Register{Text: "jcilX\x1b"}) })
	h.keys("2@q")
	h.settle()
	if got := h.text(); got != "    \n  \nX\nX" {
		t.Errorf("2@q: text = %q", got)
	}
	if st, _ := h.e.host.State("line"); st != exthost.StateRunning {
		t.Errorf("state = %v", st)
	}
}
