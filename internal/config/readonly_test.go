package config

import (
	"reflect"
	"testing"
)

// readonlyPaths は、"~" をホームのディレクトリに展開した絶対パスの列として読み、相対パスや空のパスは誤りとして除く。
//
// req: FR-120
func TestParseReadOnlyPaths(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	src := `{
  "readonlyPaths": ["~/go/pkg/mod", "~", "/usr/include/", "node_modules", "", 3, "~other/x"]
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	want := []string{"/home/u/go/pkg/mod", "/home/u", "/usr/include"}
	if !reflect.DeepEqual(cfg.ReadOnlyPaths, want) {
		t.Errorf("ReadOnlyPaths = %q, want %q", cfg.ReadOnlyPaths, want)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 59, "絶対パスか ~ で始まる"},
		{2, 75, "パスが空"},
		{2, 79, "文字列にする"},
		{2, 82, "絶対パスか ~ で始まる"},
	})
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
