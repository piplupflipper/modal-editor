package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 設定の formatters は、ファイルの種類ごとに、書いた順に整形ツールを選ぶ。省いた項目は既定値になる。
//
// req: FR-175
func TestParseSettings(t *testing.T) {
	cfg, err := parseSettings(nil)
	if err != nil || len(cfg.formatters) != 0 || !cfg.formatOnSave || cfg.timeout != defaultTimeout || !reflect.DeepEqual(cfg.localBinDirs, defaultLocalBinDirs) {
		t.Fatalf("parseSettings(nil) = %+v, %v", cfg, err)
	}

	cfg, err = parseSettings(json.RawMessage(`{
		"formatters": [
			{ "command": "biome", "args": ["check", "--write", "--stdin-file-path", "{path}"], "fileTypes": ["json", "typescript"] },
			{ "name": "imports", "command": "/opt/bin/isort", "fileTypes": ["python"] },
			{ "command": "~/bin/ruff", "args": ["format", "-"], "fileTypes": ["python"] }
		],
		"formatOnSave": false,
		"localBinDirs": [],
		"timeoutMs": 1500
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.formatOnSave || cfg.timeout != 1500*time.Millisecond || cfg.localBinDirs == nil || len(cfg.localBinDirs) != 0 {
		t.Errorf("cfg = %+v", cfg)
	}
	names := func(ft string) []string {
		var out []string
		for _, f := range cfg.forFileType(ft) {
			out = append(out, f.Name)
		}
		return out
	}
	for ft, want := range map[string][]string{"python": {"imports", "ruff"}, "json": {"biome"}, "go": nil, "": nil} {
		if got := names(ft); !reflect.DeepEqual(got, want) {
			t.Errorf("forFileType(%q) = %v, want %v", ft, got, want)
		}
	}
}

// 設定の誤り（知らない項目、空の command、パスの形の誤り、空の fileTypes、絶対パスの localBinDirs、負の timeoutMs）は、
// すべてまとめて 1 つの誤りにし、initialize を失敗させる。
func TestParseSettingsErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`{"formatter": []}`, `unknown field "formatter"`},
		{`{"formatters": [{"command": "x", "fileType": ["go"]}]}`, `unknown field "fileType"`},
		{`{"formatters": [{"fileTypes": ["go"]}]}`, "formatters[0] の command が空"},
		{`{"formatters": [{"command": "bin/x", "fileTypes": ["go"]}]}`, `command "bin/x" は、名前か絶対パス`},
		{`{"formatters": [{"command": "x"}]}`, "formatters[0] の fileTypes に"},
		{`{"formatters": [{"command": "x", "fileTypes": [""]}]}`, "formatters[0] の fileTypes に"},
		{`{"localBinDirs": ["/abs"]}`, `localBinDirs の "/abs"`},
		{`{"timeoutMs": -1}`, "timeoutMs は 0 以上"},
	} {
		if _, err := parseSettings(json.RawMessage(c.src)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q を含む誤り", c.src, err, c.want)
		}
	}
}
