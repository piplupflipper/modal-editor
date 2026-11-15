package distcheck

import (
	"debug/buildinfo"
	"debug/elf"
	"os"
	"path/filepath"
	"testing"
)

// EnvBuildDir は、`make build` が実行ファイルを置いたディレクトリを渡す環境変数の名前である。
// 指定が無ければ、生成物を読むテストは飛ばす。
const EnvBuildDir = "MODAL_EDITOR_BUILD_DIR"

// buildDir は、検査する実行ファイルのディレクトリを返す。
// 環境変数の指定が無ければテストを飛ばす。
func buildDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv(EnvBuildDir)
	if dir == "" {
		t.Skipf("%s が指定されていない（make build-check で実行する）", EnvBuildDir)
	}
	return dir
}

// buildSetting は、実行ファイルに埋め込まれた build settings から key の値を返す。
func buildSetting(t *testing.T, path, key string) (string, bool) {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("%s の build info を読めない: %v", path, err)
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value, true
		}
	}
	return "", false
}

// assertStaticELF は、path の ELF が動的リンカー（PT_INTERP）と共有ライブラリ（DT_NEEDED）を要求しないことを確かめる。
// どちらも無ければ、C の標準ライブラリを含め、実行時に他のファイルを読み込まずに起動する。
func assertStaticELF(t *testing.T, path string) {
	t.Helper()
	f, err := elf.Open(path)
	if err != nil {
		t.Fatalf("%s を ELF として開けない: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Errorf("%s は動的リンカーを要求する（PT_INTERP がある）", path)
		}
	}
	libs, err := f.ImportedLibraries()
	if err != nil {
		t.Fatalf("%s の共有ライブラリの一覧を読めない: %v", path, err)
	}
	if len(libs) > 0 {
		t.Errorf("%s は共有ライブラリ %v を要求する", path, libs)
	}
}

// TestBuildMainStatic は、`make build` の本体が CGO_ENABLED=0 でビルドされ、
// 静的にリンクされていることを確かめる。
//
// req: NFR-02
func TestBuildMainStatic(t *testing.T) {
	path := filepath.Join(buildDir(t), "modal-editor")

	cgo, ok := buildSetting(t, path, "CGO_ENABLED")
	if !ok || cgo != "0" {
		t.Errorf("%s の CGO_ENABLED = %q（ok=%v）, want \"0\"", path, cgo, ok)
	}
	assertStaticELF(t, path)
}
