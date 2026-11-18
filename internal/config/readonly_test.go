package config

import (
	"reflect"
	"testing"
)

// readonlyPaths は、"~" をホームのディレクトリに展開した絶対パスと、"/" を含まないディレクトリの名前の列として読む。
// "/" を含む相対パス、空のパス、"~user" の形は誤りとして除く。
//
// req: FR-120
func TestParseReadOnlyPaths(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	src := `{
  "readonlyPaths": ["~/go/pkg/mod", "~", "/usr/include/", "node_modules", "", 3, "~other/x", "a/b", ".."]
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	want := []string{"/home/u/go/pkg/mod", "/home/u", "/usr/include", "node_modules"}
	if !reflect.DeepEqual(cfg.ReadOnlyPaths, want) {
		t.Errorf("ReadOnlyPaths = %q, want %q", cfg.ReadOnlyPaths, want)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 75, "パスが空"},
		{2, 79, "文字列にする"},
		{2, 82, "~user の形"},
		{2, 94, "/ を含む相対パス"},
		{2, 101, ". と .."},
	})
}

// 末尾に "/" を付けたディレクトリの名前（"node_modules/"）は、付けないものと同じに扱う。
//
// req: FR-120
func TestParseReadOnlyPathsTrailingSlash(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{"readonlyPaths": ["node_modules/", "vendor//", "./", "a/b/"]}`))
	if want := []string{"node_modules", "vendor"}; !reflect.DeepEqual(cfg.ReadOnlyPaths, want) {
		t.Errorf("ReadOnlyPaths = %q, want %q", cfg.ReadOnlyPaths, want)
	}
	if len(errs) != 2 {
		t.Errorf("errs = %v, want 2 (./ and a/b/)", errs)
	}
}

// HOME が無いときの "~" と、配列でない値は誤りにする。
//
// req: FR-120
func TestParseReadOnlyPathsInvalid(t *testing.T) {
	t.Setenv("HOME", "")
	cfg, errs := Parse("config.jsonc", []byte(`{"readonlyPaths": ["~/x", "/ok"]}`))
	if !reflect.DeepEqual(cfg.ReadOnlyPaths, []string{"/ok"}) {
		t.Errorf("ReadOnlyPaths = %q", cfg.ReadOnlyPaths)
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v", errs)
	}
	cfg, errs = Parse("config.jsonc", []byte(`{"readonlyPaths": "/x"}`))
	if cfg.ReadOnlyPaths != nil || len(errs) != 1 {
		t.Errorf("ReadOnlyPaths = %q, errs = %v", cfg.ReadOnlyPaths, errs)
	}
}
