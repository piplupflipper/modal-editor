package config

import (
	"path/filepath"
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
      { "name": "ssh", "repo": "git@example.com:a/b.git", "version": "main" },
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
		{Name: "ssh", Repo: "git@example.com:a/b.git", Version: "main"},
	}
	if !reflect.DeepEqual(cfg.ExtensionInstall, want) {
		t.Errorf("ExtensionInstall = %+v, want %+v", cfg.ExtensionInstall, want)
	}
	if !cfg.ExtensionInstallValid {
		t.Error("ExtensionInstallValid = false, want true")
	}
}

// 誤りのある項目は使わず、位置と内容を返す。他の項目は読む。
func TestParseExtensionInstallInvalid(t *testing.T) {
	src := `{"extensions": {"install": [
1,
{"name": "../x", "repo": "/r", "version": "v"},
{"name": ".hidden", "repo": "/r", "version": "v"},
{"name": "a", "repo": "--upload-pack=x", "version": "v"},
{"name": "a", "repo": "/r", "version": ""},
{"name": "a", "repo": "/r"},
{"name": "a", "repo": "/r", "version": 1},
{"name": "a", "repo": "/r", "version": "v", "extra": 1},
{"name": "a", "repo": "/r2", "version": "v2"},
{"name": "b", "repo": "../b", "version": "v"},
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
		{6, 40, "extensions.install.version は空にせず、- で始めない"},
		{7, 1, "name、repo、version を書く"},
		{8, 40, "extensions.install.version は文字列にする"},
		{9, 45, "未知の項目 extensions.install.extra"},
		{10, 1, `名前 "a" が先の項目と重なる`},
		{11, 23, `extensions.install.repo "../b" は相対パスにしない`},
	})
	want := []ExtensionInstall{{Name: "a", Repo: "/r", Version: "v"}}
	if !reflect.DeepEqual(cfg.ExtensionInstall, want) {
		t.Errorf("ExtensionInstall = %+v, want %+v", cfg.ExtensionInstall, want)
	}
	if cfg.ExtensionInstallValid {
		t.Error("ExtensionInstallValid = true, want false")
	}

	cfg, errs = Parse("c", []byte(`{"extensions": {"install": {}}}`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{{1, 28, "extensions.install は配列にする"}})
	if cfg.ExtensionInstall != nil || cfg.ExtensionInstallValid {
		t.Errorf("ExtensionInstall = %+v, Valid = %v, want nil, false", cfg.ExtensionInstall, cfg.ExtensionInstallValid)
	}
}

// extensions.install を誤りなく読めたかを ExtensionInstallValid で返す。
// 呼び出し側はこれが false なら削除をしない（書き損じで拡張機能を消さない。PR #195 のレビューの入力）。
func TestExtensionInstallValid(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"項目なし", `{}`, true},
		{"空", ``, true},
		{"コメントだけ", "// x\n", true},
		{"空の配列", `{"extensions": {"install": []}}`, true},
		{"他の項目の誤りは関係しない", `{"keymap": 1, "extensions": {"syncTimeoutMs": -1, "install": []}}`, true},
		{"version が空", `{"extensions": {"install": [{"name": "hello", "repo": "/r", "version": ""}, {"name": "keep", "repo": "/r", "version": "v"}]}}`, false},
		{"閉じ括弧の欠け", `{"extensions": {"install": [{"name": "keep", "repo": "/r", "version": "v"}]}`, false},
		{"extensions がオブジェクトでない", `{"extensions": []}`, false},
		{"最上位がオブジェクトでない", `[]`, false},
	} {
		cfg, _ := Parse("c", []byte(tc.src))
		if cfg.ExtensionInstallValid != tc.want {
			t.Errorf("%s: ExtensionInstallValid = %v, want %v", tc.name, cfg.ExtensionInstallValid, tc.want)
		}
	}
	cfg, errs := Load(filepath.Join(t.TempDir(), "none.jsonc"))
	if len(errs) != 0 || !cfg.ExtensionInstallValid {
		t.Errorf("設定ファイルが無いとき: Valid = %v, errs = %v; want true", cfg.ExtensionInstallValid, errs)
	}
}

func TestIsRelativeRepo(t *testing.T) {
	for repo, want := range map[string]bool{
		"https://example.com/a.git": false, "file:///srv/a": false, "/srv/a": false,
		"git@example.com:a/b": false, "example.com:a": false,
		"a": true, "../a": true, "./a:b": true, "a/b:c": true,
	} {
		if got := isRelativeRepo(repo); got != want {
			t.Errorf("isRelativeRepo(%q) = %v, want %v", repo, got, want)
		}
	}
}
