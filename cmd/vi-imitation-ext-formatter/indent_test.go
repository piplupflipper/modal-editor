package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
)

// writeTree は、root の下に files（相対パスから中身）を書く。
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// indentCfg は、ファイルの種類ごとに整形ツールを 1 つずつ並べた設定である。
func indentCfg(t *testing.T) *config {
	t.Helper()
	c, err := parseSettings(json.RawMessage(`{"formatters": [
		{"command": "biome", "args": ["format"], "fileTypes": ["javascript", "json", "css"]},
		{"command": "prettier", "fileTypes": ["markdown", "yaml"]},
		{"command": "ruff", "fileTypes": ["python"]},
		{"command": "shfmt", "fileTypes": ["sh"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// optionsString は、indentConfig.options の値を、indentNames の順に空白で並べた文字列にする。
func optionsString(ic indentConfig) string {
	opts := ic.options()
	var out []string
	for _, n := range indentNames {
		out = append(out, string(opts[n]))
	}
	return strings.Join(out, " ")
}

// ファイルの種類に設定した整形ツールの設定ファイルを、ファイルのディレクトリから上へ探し、インデントの文字と幅を読む。
// 探す順と、どの設定ファイルを使うかは、それぞれの整形ツールに合わせる（indent.go の doc）。
//
// req: FR-193
func TestProjectIndent(t *testing.T) {
	c := indentCfg(t)
	for _, tc := range []struct {
		name  string
		files map[string]string
		path  string
		ft    string
		want  string // indent.useSpaces indent.width tab.width
	}{
		{"biome のタブ", map[string]string{"biome.json": `{"formatter": {"indentStyle": "tab"}}`}, "src/a.js", "javascript", "false 0 null"},
		{"biome の言語ごとの上書き", map[string]string{
			"biome.jsonc": "// 注釈\n{\"formatter\": {\"indentStyle\": \"space\", \"indentWidth\": 3,}, \"javascript\": {\"formatter\": {\"indentWidth\": 5}}}",
		}, "a.js", "javascript", "true 5 5"},
		{"biome の言語ごとの上書きは他の言語に効かない", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 3}, "javascript": {"formatter": {"indentWidth": 5}}}`,
		}, "a.json", "json", "null 3 3"},
		{"biome は同じディレクトリでは biome.json が勝つ", map[string]string{
			"biome.json": `{"formatter": {"indentStyle": "space"}}`, "biome.jsonc": `{"formatter": {"indentStyle": "tab"}}`,
		}, "a.css", "css", "true null null"},
		{"biome は近い設定だけを使う", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 6}}`, "sub/biome.json": `{"formatter": {"indentStyle": "tab"}}`,
		}, "sub/a.js", "javascript", "false 0 null"},
		{"prettier の .prettierrc の YAML", map[string]string{".prettierrc": "useTabs: true\ntabWidth: 4\n"}, "a.md", "markdown", "false 4 4"},
		{"prettier の .prettierrc の JSON", map[string]string{".prettierrc": `{"tabWidth": 2}`}, "a.md", "markdown", "null 2 2"},
		{"prettier の package.json", map[string]string{"package.json": `{"name": "x", "prettier": {"useTabs": false, "tabWidth": 3}}`}, "a.yaml", "yaml", "true 3 3"},
		{"prettier は prettier の無い package.json を飛ばす", map[string]string{
			"package.json": `{"name": "x"}`, ".prettierrc.json": `{"tabWidth": 8}`,
		}, "a.md", "markdown", "null 8 8"},
		{"prettier は package.json を .prettierrc より先に使う", map[string]string{
			"package.json": `{"prettier": {"tabWidth": 3}}`, ".prettierrc": "tabWidth: 5\n",
		}, "a.md", "markdown", "null 3 3"},
		{"prettier は読めない形の設定で止まる", map[string]string{
			".prettierrc.json": `{"tabWidth": 8}`, "sub/prettier.config.js": "export default {tabWidth: 2}",
		}, "sub/a.md", "markdown", "null null null"},
		{"prettier の共有の設定の名前", map[string]string{"package.json": `{"prettier": "@company/prettier-config"}`}, "a.md", "markdown", "null null null"},
		{"prettier の .prettierrc.toml", map[string]string{".prettierrc.toml": "useTabs = true\n"}, "a.md", "markdown", "false 0 null"},
		{"ruff の pyproject.toml", map[string]string{
			"pyproject.toml": "[project]\nname = \"x\"\n[tool.ruff]\nindent-width = 2\n[tool.ruff.format]\nindent-style = \"space\"\n",
		}, "pkg/a.py", "python", "true 2 2"},
		{"ruff は [tool.ruff] の無い pyproject.toml を飛ばす", map[string]string{
			"ruff.toml": "indent-width = 3\nformat.indent-style = \"tab\"\n", "pkg/pyproject.toml": "[project]\nname = \"x\"\n",
		}, "pkg/a.py", "python", "false 3 3"},
		{"ruff は .ruff.toml、ruff.toml、pyproject.toml の順", map[string]string{
			".ruff.toml": "indent-width = 7\n", "ruff.toml": "indent-width = 5\n", "pyproject.toml": "[tool.ruff]\nindent-width = 3\n",
		}, "a.py", "python", "null 7 7"},
		{"設定を読む整形ツールでなければ読まない", map[string]string{"biome.json": `{"formatter": {"indentWidth": 3}}`}, "a.sh", "sh", "null null null"},
		{"ファイルの種類の整形ツールの設定だけを読む", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 3}}`, "ruff.toml": "indent-width = 6\n",
		}, "a.py", "python", "null 6 6"},
		{"設定ファイルが無い", nil, "a.py", "python", "null null null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, tc.files)
			ic, err := c.projectIndent(filepath.Join(root, tc.path), tc.ft)
			if err != nil {
				t.Fatal(err)
			}
			if got := optionsString(ic); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// 読めない設定ファイル（壊れた JSON、TOML、YAML、型と範囲の合わない値）は、そのパスを付けた誤りにする。
//
// req: FR-193
func TestProjectIndentBroken(t *testing.T) {
	c := indentCfg(t)
	for _, tc := range []struct {
		file, body, path, ft, want string
	}{
		{"biome.json", `{"formatter": {`, "a.js", "javascript", "biome.json"},
		{"biome.json", `{"formatter": {"indentStyle": "tabs"}}`, "a.js", "javascript", `indentStyle は "tab" か "space"`},
		{"biome.json", `{"formatter": {"indentWidth": 0}}`, "a.js", "javascript", "1 から 9999"},
		{"biome.json", `{"javascript": {"formatter": {"indentWidth": "2"}}}`, "a.js", "javascript", "javascript.formatter.indentWidth は整数"},
		{".prettierrc", "- a\n", "a.md", "markdown", "最上位"},
		{".prettierrc", "useTabs: maybe\n", "a.md", "markdown", "useTabs は true か false"},
		{"package.json", `{"prettier": `, "a.md", "markdown", "package.json"},
		{"pyproject.toml", "[tool.ruff\n", "a.py", "python", "pyproject.toml"},
		{"ruff.toml", "indent-width = 2.5\n", "a.py", "python", "indent-width は整数"},
		{"ruff.toml", "[format]\nindent-style = \"tabs\"\n", "a.py", "python", `indent-style は "space" か "tab"`},
	} {
		root := t.TempDir()
		writeTree(t, root, map[string]string{tc.file: tc.body})
		_, err := c.projectIndent(filepath.Join(root, tc.path), tc.ft)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), filepath.Join(root, tc.file)) {
			t.Errorf("%s %q: %v, want %q とパス", tc.file, tc.body, err, tc.want)
		}
	}
}

// indentSync は、開いたバッファのインデントを option/set で送り、同じ値は送り直さない。
// 設定ファイルを保存したら開いているすべてのバッファを読み直す。読めない設定ファイルは送らずに 1 回だけ知らせる。
// ファイルの種類が変わって整形ツールが無くなったら、前に送った値を null で取り下げる。
//
// req: FR-193
func TestIndentSync(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"biome.json": `{"formatter": {"indentStyle": "tab"}}`, "a.js": "", "b.js": "", "c.txt": ""})
	var sent []string
	var warned []string
	s := newIndentSync(func() *config { return indentCfg(t) }, func(_ context.Context, p ext.OptionSetParams) error {
		b, _ := json.Marshal(p)
		sent = append(sent, string(b))
		return nil
	}, func(text string) { warned = append(warned, text) }, t.Logf)
	drain := func() {
		for s.step(context.Background()) {
		}
	}
	js := func(name string) bufferInfo {
		return bufferInfo{path: filepath.Join(root, name), fileType: "javascript"}
	}
	want := func(what string, w ...string) {
		t.Helper()
		if strings.Join(sent, "\n") != strings.Join(w, "\n") {
			t.Errorf("%s: sent\n%s\nwant\n%s", what, strings.Join(sent, "\n"), strings.Join(w, "\n"))
		}
		sent = nil
	}
	tabs := `"options":{"indent.useSpaces":false,"indent.width":0,"tab.width":null}}`

	s.opened(1, js("a.js"))
	s.opened(2, js("b.js"))
	s.opened(3, bufferInfo{path: filepath.Join(root, "c.txt"), fileType: "text"})
	drain()
	want("開いたとき", `{"buffer":1,`+tabs, `{"buffer":2,`+tabs)

	s.opened(1, js("a.js"))
	drain()
	want("同じ値は送り直さない")

	writeTree(t, root, map[string]string{"biome.json": `{"formatter": {"indentStyle": "space", "indentWidth": 2}}`})
	s.saved(3)
	drain()
	want("設定ファイルでないファイルの保存は、そのバッファだけを読み直す")
	s.opened(4, bufferInfo{path: filepath.Join(root, "biome.json"), fileType: "json"})
	drain()
	sent = nil
	s.saved(4)
	drain()
	spaces := `"options":{"indent.useSpaces":true,"indent.width":2,"tab.width":2}}`
	want("設定ファイルの保存は、すべてのバッファを読み直す", `{"buffer":1,`+spaces, `{"buffer":2,`+spaces)

	writeTree(t, root, map[string]string{"biome.json": `{"formatter": `})
	s.saved(4)
	drain()
	s.saved(4)
	drain()
	want("読めない設定ファイルは送らない")
	if len(warned) != 1 || !strings.Contains(warned[0], "biome.json") {
		t.Errorf("知らせ = %q, want 1 回", warned)
	}

	s.opened(1, bufferInfo{path: filepath.Join(root, "a.js"), fileType: "text"})
	drain()
	want("整形ツールの無いファイルの種類になったら取り下げる",
		`{"buffer":1,"options":{"indent.useSpaces":null,"indent.width":null,"tab.width":null}}`)

	s.closed(2)
	s.saved(4)
	drain()
	want("閉じたバッファには送らない")
}
