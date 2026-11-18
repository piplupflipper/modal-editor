package config

import (
	"reflect"
	"testing"
)

func TestParseExtensionInstall(t *testing.T) {
	src := `{
  "extensions": {
    "install": [
      // コメントと末尾のカンマを許す
      { "name": "surround", "repo": "https://example.com/surround.git", "version": "v0.1.0" },
      { "name": "local", "repo": "/srv/git/local", "version": "0123abcd", },
    ],
  },
}`
	cfg, errs := Parse("c", []byte(src))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	want := []ExtensionInstall{
		{Name: "surround", Repo: "https://example.com/surround.git", Version: "v0.1.0"},
		{Name: "local", Repo: "/srv/git/local", Version: "0123abcd"},
	}
	if !reflect.DeepEqual(cfg.ExtensionInstall, want) {
		t.Errorf("ExtensionInstall = %+v, want %+v", cfg.ExtensionInstall, want)
	}
}

// 誤りのある項目は使わず、位置と内容を返す。他の項目は読む。
func TestParseExtensionInstallInvalid(t *testing.T) {
	src := `{"extensions": {"install": [
1,
{"name": "../x", "repo": "r", "version": "v"},
{"name": ".hidden", "repo": "r", "version": "v"},
{"name": "a", "repo": "--upload-pack=x", "version": "v"},
{"name": "a", "repo": "r", "version": ""},
{"name": "a", "repo": "r"},
{"name": "a", "repo": "r", "version": 1},
{"name": "a", "repo": "r", "version": "v", "extra": 1},
{"name": "a", "repo": "r2", "version": "v2"},
]}}`
	cfg, errs := Parse("c", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 1, "extensions.install の項目 はオブジェクトにする"},
		{3, 10, `extensions.install.name "../x" は英数字で始め`},
		{4, 10, `extensions.install.name ".hidden" は英数字で始め`},
		{5, 23, "extensions.install.repo は空にせず、- で始めない"},
		{6, 39, "extensions.install.version は空にせず、- で始めない"},
		{7, 1, "name、repo、version を書く"},
		{8, 39, "extensions.install.version は文字列にする"},
		{9, 44, "未知の項目 extensions.install.extra"},
		{10, 1, `名前 "a" が先の項目と重なる`},
	})
	want := []ExtensionInstall{{Name: "a", Repo: "r", Version: "v"}}
	if !reflect.DeepEqual(cfg.ExtensionInstall, want) {
		t.Errorf("ExtensionInstall = %+v, want %+v", cfg.ExtensionInstall, want)
	}

	cfg, errs = Parse("c", []byte(`{"extensions": {"install": {}}}`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{{1, 28, "extensions.install は配列にする"}})
	if cfg.ExtensionInstall != nil {
		t.Errorf("ExtensionInstall = %+v, want nil", cfg.ExtensionInstall)
	}
}
