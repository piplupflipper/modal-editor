package distcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makefileVar は、リポジトリの Makefile の `NAME := a b c` の形の変数を、空白で区切った語の列として返す。
// 検査する実行ファイルと拡張機能の一覧を Makefile から求め、拡張機能を足す PR がこのパッケージの一覧を書き換えずに済むようにする。
// 変数が無いか空なら、テストを失敗させる。
func makefileVar(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		lhs, rhs, ok := strings.Cut(line, ":=")
		if !ok || strings.TrimSpace(lhs) != name {
			continue
		}
		if words := strings.Fields(rhs); len(words) > 0 {
			return words
		}
	}
	t.Fatalf("Makefile に変数 %s が無いか、空である", name)
	return nil
}

// pureGoBinaries は、cgo を使わずに作る実行ファイルである（Makefile の BINARY、MCP_BRIDGE、EXTENSIONS）。
func pureGoBinaries(t *testing.T) []string {
	t.Helper()
	return append([]string{"modal-editor", "modal-editor-mcp-bridge"}, makefileVar(t, "EXTENSIONS")...)
}

// cgoBinaries は、cgo を使う実行ファイルである（Makefile の CGO_EXTENSIONS）。
func cgoBinaries(t *testing.T) []string {
	t.Helper()
	return makefileVar(t, "CGO_EXTENSIONS")
}

// manifestExtensions は、Makefile の拡張機能のうち、宣言ファイル（cmd/<名前>/manifest.json）を持つものの名前を返す。
// make dist は、これらを extensions/<名前から modal-editor-ext- を除いたもの>/ に置く。
func manifestExtensions(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, e := range append(makefileVar(t, "EXTENSIONS"), makefileVar(t, "CGO_EXTENSIONS")...) {
		if _, err := os.Stat(filepath.Join("..", "..", "cmd", e, "manifest.json")); err == nil {
			out = append(out, e)
		}
	}
	return out
}

// TestMakefileLists は、Makefile から実行ファイルの一覧を読めることを確かめる。
// 配布物の検査（make build-check、make dist-check）は環境変数が無いと飛ばすので、読み方の誤りをここで見つける。
func TestMakefileLists(t *testing.T) {
	for _, name := range append(pureGoBinaries(t), cgoBinaries(t)...) {
		if _, err := os.Stat(filepath.Join("..", "..", "cmd", name)); err != nil {
			t.Errorf("Makefile の %s に対応する cmd/%s が無い: %v", name, name, err)
		}
	}
	if len(manifestExtensions(t)) == 0 {
		t.Error("宣言ファイルを持つ拡張機能が無い")
	}
}
