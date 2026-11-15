package distcheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// EnvDistDir は、`make dist` が配布物をまとめたディレクトリを渡す環境変数の名前である。
// 指定が無ければ、配布物を読むテストは飛ばす。
const EnvDistDir = "MODAL_EDITOR_DIST_DIR"

// distDir は、検査する配布物のディレクトリを返す。
// 環境変数の指定が無ければテストを飛ばす。
func distDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(EnvDistDir)
	if dir == "" {
		t.Skipf("%s が指定されていない（make dist-check で実行する）", EnvDistDir)
	}
	return dir
}

// TestDistLayout は、配布物が本体と拡張機能を別のファイルとして含み、
// 拡張機能ごとに宣言ファイルと、その command が指す実行ファイルを同じディレクトリに置いていることを確かめる。
// 実行ファイルは全て静的にリンクされていて、ファイルを置くだけで起動する。
//
// req: NFR-01, NFR-06
func TestDistLayout(t *testing.T) {
	dir := distDir(t)

	for _, name := range []string{"modal-editor", "modal-editor-mcp-bridge", "modal-editor-ext-script"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			assertExecutable(t, path)
			assertStaticELF(t, path)
		})
	}

	manifests, err := filepath.Glob(filepath.Join(dir, "extensions", "*", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	// 宣言ファイルを持つ標準の拡張機能は line、dap、lsp、treesitter の 4 つである。
	if len(manifests) != 4 {
		t.Errorf("宣言ファイルの数 = %d, want 4: %v", len(manifests), manifests)
	}
	for _, m := range manifests {
		t.Run(filepath.Base(filepath.Dir(m)), func(t *testing.T) {
			data, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				ID      string `json:"id"`
				Command string `json:"command"`
			}
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatalf("%s を読めない: %v", m, err)
			}
			if manifest.ID == "" || manifest.Command == "" {
				t.Fatalf("%s の id か command が空: %+v", m, manifest)
			}
			if filepath.IsAbs(manifest.Command) {
				t.Fatalf("%s の command が絶対パス %q（配布物を置く場所に依存する）", m, manifest.Command)
			}
			// command の相対パスは、宣言ファイルのあるディレクトリから解決する（基本設計 5.1）。
			path := filepath.Join(filepath.Dir(m), manifest.Command)
			assertExecutable(t, path)
			assertStaticELF(t, path)
		})
	}
}

// TestDistLicenses は、配布物が、実行ファイルに静的にリンクするサードパーティのライセンスの文面を含むことを確かめる。
// Go の標準ライブラリとランタイム、musl、tree-sitter とその文法は、配布するときに著作権の表示とライセンスの文面を添える必要がある。
func TestDistLicenses(t *testing.T) {
	dir := distDir(t)
	for _, rel := range []string{
		"licenses/go/LICENSE",
		"licenses/musl/copyright",
		"licenses/github.com/tree-sitter/go-tree-sitter/LICENSE",
		"licenses/github.com/tree-sitter/tree-sitter-go/LICENSE",
		"licenses/github.com/tree-sitter/tree-sitter-json/LICENSE",
		"licenses/github.com/traefik/yaegi/LICENSE",
	} {
		info, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Errorf("%s が無い: %v", rel, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s が空", rel)
		}
	}
}

// TestSmokeAPIVersion は、make dist-smoke の確認のスクリプトが送る initialize の apiVersion が、
// 拡張機能 API のバージョンと一致することを確かめる。
// API のバージョンを上げたときに、スクリプトの直し忘れを make check で見つける。
func TestSmokeAPIVersion(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "smoke.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := `"apiVersion":"` + ext.APIVersion + `"`
	if !strings.Contains(string(data), want) {
		t.Errorf("testdata/smoke.sh に %s が無い", want)
	}
}

// assertExecutable は、path が実行の許可のある通常のファイルであることを確かめる。
func assertExecutable(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("%s が無い: %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s が実行できる通常のファイルでない（mode=%v）", path, info.Mode())
	}
}
