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
// 「実物:」を付けたケースは、2026-11-22 に同じ入力を biome 2.4.15（format --stdin-file-path=<絶対パス>）、
// ruff 0.15.16（check --show-settings）、prettier 3.6.2（--find-config-path と整形の結果）に与えて確かめた結果と一致させた
// （PR #352 のレビュー）。
//
// req: FR-193
func TestProjectIndent(t *testing.T) {
	c := indentCfg(t)
	for _, tc := range []struct {
		name    string
		files   map[string]string
		path    string
		ft      string
		want    string // indent.useSpaces indent.width tab.width
		sources []string
	}{
		{"biome のタブ", map[string]string{"biome.json": `{"formatter": {"indentStyle": "tab"}}`}, "src/a.js", "javascript", "false 0 null", []string{"biome.json"}},
		{"biome の言語ごとの上書き", map[string]string{
			"biome.jsonc": "// 注釈\n{\"formatter\": {\"indentStyle\": \"space\", \"indentWidth\": 3,}, \"javascript\": {\"formatter\": {\"indentWidth\": 5}}}",
		}, "a.js", "javascript", "true 5 5", nil},
		{"biome の言語ごとの上書きは他の言語に効かない", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 3}, "javascript": {"formatter": {"indentWidth": 5}}}`,
		}, "a.json", "json", "null 3 3", nil},
		{"biome は同じディレクトリでは biome.json が勝つ", map[string]string{
			"biome.json": `{"formatter": {"indentStyle": "space"}}`, "biome.jsonc": `{"formatter": {"indentStyle": "tab"}}`,
		}, "a.css", "css", "true null null", nil},
		{"biome は近い設定だけを使う", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 6}}`, "sub/biome.json": `{"formatter": {"indentStyle": "tab"}}`,
		}, "sub/a.js", "javascript", "false 0 null", nil},
		{"実物: biome は .biome.json も読む", map[string]string{
			".biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`,
		}, "src/a.js", "javascript", "true 3 3", nil},
		{"実物: biome は biome.jsonc を .biome.json より先に使う", map[string]string{
			"biome.jsonc": `{"formatter":{"indentStyle":"space","indentWidth":5}}`, ".biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`,
		}, "a.js", "javascript", "true 5 5", nil},
		{"実物: biome は .biome.json を .biome.jsonc より先に使う", map[string]string{
			".biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`, ".biome.jsonc": `{"formatter":{"indentStyle":"space","indentWidth":6}}`,
		}, "a.js", "javascript", "true 3 3", nil},
		{`実物: biome の "extends": "//" は根の設定を引き継ぐ`, map[string]string{
			"biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`, "src/biome.json": `{"root":false,"extends":"//"}`,
		}, "src/deep/a.js", "javascript", "true 3 3", []string{"src/biome.json", "biome.json"}},
		{`実物: biome の "extends": "//" の上に近い設定を重ねる`, map[string]string{
			"biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`, "src/biome.json": `{"root":false,"extends":"//","formatter":{"indentWidth":5}}`,
		}, "src/deep/a.js", "javascript", "true 5 5", nil},
		{`実物: biome の "root": false だけでは根の設定を引き継がない`, map[string]string{
			"biome.json": `{"formatter":{"indentStyle":"space","indentWidth":3}}`, "src/biome.json": `{"root":false}`,
		}, "src/deep/a.js", "javascript", "null null null", nil},
		{"prettier の .prettierrc の YAML", map[string]string{".prettierrc": "useTabs: true\ntabWidth: 4\n"}, "a.md", "markdown", "false 4 4", nil},
		{"prettier の .prettierrc の JSON", map[string]string{".prettierrc": `{"tabWidth": 2}`}, "a.md", "markdown", "null 2 2", nil},
		{"実物: prettier の .prettierrc の YAML の流れの形", map[string]string{".prettierrc": "{ useTabs: true, tabWidth: 4 }\n"}, "a.md", "markdown", "false 4 4", nil},
		{"実物: prettier の .prettierrc の単引用符の流れの形", map[string]string{".prettierrc": "{'useTabs': true}\n"}, "a.md", "markdown", "false 0 null", nil},
		{"実物: prettier の .prettierrc.yaml の流れの形", map[string]string{".prettierrc.yaml": "{useTabs: true, tabWidth: 4}\n"}, "a.md", "markdown", "false 4 4", nil},
		{"実物: prettier は .prettierrc.yaml を .prettierrc.yml より先に使う", map[string]string{
			".prettierrc.yml": "tabWidth: 3\n", ".prettierrc.yaml": "tabWidth: 5\n",
		}, "a.md", "markdown", "null 5 5", nil},
		{"prettier の package.json", map[string]string{"package.json": `{"name": "x", "prettier": {"useTabs": false, "tabWidth": 3}}`}, "a.yaml", "yaml", "true 3 3", nil},
		{"prettier は prettier の無い package.json を飛ばす", map[string]string{
			"package.json": `{"name": "x"}`, ".prettierrc.json": `{"tabWidth": 8}`,
		}, "a.md", "markdown", "null 8 8", nil},
		{"実物: prettier は package.json を .prettierrc より先に使う", map[string]string{
			"package.json": `{"prettier": {"tabWidth": 3}}`, ".prettierrc": "tabWidth: 5\n",
		}, "a.md", "markdown", "null 3 3", nil},
		{"prettier の overrides のインデントでない項目は読み飛ばす", map[string]string{
			".prettierrc.json": `{"tabWidth": 4, "overrides": [{"files": "*.md", "options": {"proseWrap": "always"}}]}`,
		}, "a.md", "markdown", "null 4 4", nil},
		{"prettier の .prettierrc.toml", map[string]string{".prettierrc.toml": "useTabs = true\n"}, "a.md", "markdown", "false 0 null", nil},
		{"ruff の pyproject.toml", map[string]string{
			"pyproject.toml": "[project]\nname = \"x\"\n[tool.ruff]\nindent-width = 2\n[tool.ruff.format]\nindent-style = \"space\"\n",
		}, "pkg/a.py", "python", "true 2 2", nil},
		{"ruff は [tool.ruff] の無い pyproject.toml を飛ばす", map[string]string{
			"ruff.toml": "indent-width = 3\nformat.indent-style = \"tab\"\n", "pkg/pyproject.toml": "[project]\nname = \"x\"\n",
		}, "pkg/a.py", "python", "false 3 3", nil},
		{"実物: ruff は鍵の無い [tool.ruff] の pyproject.toml を使う", map[string]string{
			"ruff.toml": "indent-width = 2\n", "sub/pyproject.toml": "[tool.ruff]\n",
		}, "sub/b.py", "python", "null null null", []string{"sub/pyproject.toml"}},
		{"実物: ruff は複数行のインラインの表（TOML 1.1）を読む", map[string]string{
			"pyproject.toml": "[tool.ruff]\nformat = {\n  indent-style = \"tab\",\n}\n",
		}, "a.py", "python", "false 0 null", nil},
		{`実物: ruff は文字列の \e（TOML 1.1）を読む`, map[string]string{
			"pyproject.toml": "[project]\ndescription = \"\\e\"\n[tool.ruff]\nindent-width = 2\n",
		}, "a.py", "python", "null 2 2", nil},
		{"ruff は .ruff.toml、ruff.toml、pyproject.toml の順", map[string]string{
			".ruff.toml": "indent-width = 7\n", "ruff.toml": "indent-width = 5\n", "pyproject.toml": "[tool.ruff]\nindent-width = 3\n",
		}, "a.py", "python", "null 7 7", nil},
		{"設定を読む整形ツールでなければ読まない", map[string]string{"biome.json": `{"formatter": {"indentWidth": 3}}`}, "a.sh", "sh", "null null null", nil},
		{"ファイルの種類の整形ツールの設定だけを読む", map[string]string{
			"biome.json": `{"formatter": {"indentWidth": 3}}`, "ruff.toml": "indent-width = 6\n",
		}, "a.py", "python", "null 6 6", nil},
		{"設定ファイルが無い", nil, "a.py", "python", "null null null", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, tc.files)
			ic, sources, err := c.projectIndent(filepath.Join(root, tc.path), tc.ft)
			if err != nil {
				t.Fatal(err)
			}
			if got := optionsString(ic); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if tc.sources != nil {
				var want []string
				for _, s := range tc.sources {
					want = append(want, filepath.Join(root, s))
				}
				if strings.Join(sources, " ") != strings.Join(want, " ") {
					t.Errorf("sources = %q, want %q", sources, want)
				}
			}
		})
	}
}

// 読めない設定ファイル（壊れた JSON、TOML、YAML、型と範囲の合わない値）と、小さな読み手で扱えない書き方
// （overrides の字下げ、"//" でない extends、ruff の extend、prettier の JavaScript の設定など）は、そのパスを付けた誤りにする。
// 誤った値を送らないためである。「実物:」のケースは、実物がその設定で、拡張機能の読み方と違う値で整形することを確かめた。
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
		// 実物: biome 2.4.15 は a.js を 2 つのスペースで字下げする（overrides が勝つ）。
		{"biome.json", `{"formatter":{"indentStyle":"tab"},"overrides":[{"includes":["**/*.js"],"formatter":{"indentStyle":"space","indentWidth":2}}]}`, "a.js", "javascript", "overrides[0]"},
		{"biome.json", `{"overrides":[{"includes":["*.css"],"css":{"formatter":{"indentWidth":2}}}]}`, "a.js", "javascript", "overrides[0]"},
		// 実物: biome 2.4.15 は extends のファイル（ここでは無いファイル）の設定を使う。
		{"biome.json", `{"extends":["./base.json"]}`, "a.js", "javascript", "extends"},
		{".prettierrc", "- a\n", "a.md", "markdown", "最上位"},
		{".prettierrc", "useTabs: maybe\n", "a.md", "markdown", "useTabs は true か false"},
		{".prettierrc", "tabWidth: 2\n{useTabs: true}\n", "a.md", "markdown", "最上位"},
		// 実物: prettier 3.6.2 は overrides の useTabs: false で a.js をスペースで字下げする。YAML の overrides は中を読めない。
		{".prettierrc.yaml", "useTabs: true\noverrides:\n  - files: \"*.md\"\n    options:\n      useTabs: false\n", "a.md", "markdown", "overrides"},
		{".prettierrc.json", `{"overrides": [{"files": "*.md", "options": {"tabWidth": 8}}]}`, "a.md", "markdown", "overrides[0]"},
		{"prettier.config.js", "export default {tabWidth: 2}", "a.md", "markdown", "読めない"},
		{"package.json", `{"prettier": "@company/prettier-config"}`, "a.md", "markdown", "共有の設定"},
		{"package.yaml", "prettier:\n  tabWidth: 3\n", "a.md", "markdown", "読めない"},
		{"package.json", `{"prettier": `, "a.md", "markdown", "package.json"},
		{"pyproject.toml", "[tool.ruff\n", "a.py", "python", "pyproject.toml"},
		{"ruff.toml", "indent-width = 2.5\n", "a.py", "python", "indent-width は整数"},
		{"ruff.toml", "[format]\nindent-style = \"tabs\"\n", "a.py", "python", `indent-style は "tab" か "space"`},
		// 実物: ruff 0.15.16 は extend の先（base.toml）の indent-width を使う。
		{"ruff.toml", "extend = \"base.toml\"\n", "a.py", "python", "extend"},
	} {
		root := t.TempDir()
		writeTree(t, root, map[string]string{tc.file: tc.body})
		_, _, err := c.projectIndent(filepath.Join(root, tc.path), tc.ft)
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), filepath.Join(root, tc.file)) {
			t.Errorf("%s %q: %v, want %q とパス", tc.file, tc.body, err, tc.want)
		}
	}
}

// 整形ツールの見分けは、name、command のファイル名、npx などの後の最初の引数の順に見る（PR #352 のレビューの推奨 8）。
func TestToolKind(t *testing.T) {
	for _, tc := range []struct {
		f    formatterConfig
		want string
	}{
		{formatterConfig{Command: "biome"}, "biome"},
		{formatterConfig{Command: "/opt/bin/ruff"}, "ruff"},
		{formatterConfig{Command: "prettierd"}, "prettier"},
		{formatterConfig{Command: "npx", Args: []string{"prettier", "--stdin-filepath", "{path}"}}, "prettier"},
		{formatterConfig{Command: "npx", Args: []string{"--yes", "prettier@3.6.2", "--stdin-filepath", "{path}"}}, "prettier"},
		{formatterConfig{Command: "pnpm", Args: []string{"dlx", "@biomejs/biome@2.4.15", "format"}}, "biome"},
		{formatterConfig{Command: "uvx", Args: []string{"ruff", "format", "-"}}, "ruff"},
		{formatterConfig{Command: "uv", Args: []string{"tool", "run", "ruff==0.15.16", "format"}}, "ruff"},
		{formatterConfig{Command: "npx", Args: []string{"some-wrapper", "prettier"}}, ""},
		{formatterConfig{Name: "prettier", Command: "npx", Args: []string{"some-wrapper", "prettier"}}, "prettier"},
		{formatterConfig{Command: "shfmt", Args: []string{"ruff"}}, ""},
	} {
		if got := toolKind(tc.f); got != tc.want {
			t.Errorf("toolKind(%+v) = %q, want %q", tc.f, got, tc.want)
		}
	}
}

// indentSync は、開いたバッファのインデントを option/set で送り、同じ値は送り直さない。
// 設定ファイルを保存したら開いているすべてのバッファを読み直す。読めない設定ファイルは 1 回だけ知らせ、
// 前の値をその設定ファイルから読んだなら送らずに保ち、別の設定ファイルから読んだなら null で取り下げる。
// ファイルの種類が変わったら、値が同じでも新しい種類を付けて送り直す。
//
// req: FR-193
func TestIndentSync(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"biome.json": `{"formatter": {"indentStyle": "tab"}}`, "pyproject.toml": "[tool.ruff]\nindent-width = 2\n",
		"a.js": "", "b.js": "", "c.txt": "", "d.py": "",
	})
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
	buf := func(name, ft string) bufferInfo { return bufferInfo{path: filepath.Join(root, name), fileType: ft} }
	want := func(what string, w ...string) {
		t.Helper()
		if strings.Join(sent, "\n") != strings.Join(w, "\n") {
			t.Errorf("%s: sent\n%s\nwant\n%s", what, strings.Join(sent, "\n"), strings.Join(w, "\n"))
		}
		sent = nil
	}
	tabs := `"options":{"indent.useSpaces":false,"indent.width":0,"tab.width":null}`
	nulls := `"options":{"indent.useSpaces":null,"indent.width":null,"tab.width":null}`
	ruff2 := `"options":{"indent.useSpaces":null,"indent.width":2,"tab.width":2}`

	s.opened(1, buf("a.js", "javascript"))
	s.opened(2, buf("b.js", "javascript"))
	s.opened(3, buf("c.txt", "text"))
	drain()
	want("開いたとき", `{"buffer":1,`+tabs+`,"fileType":"javascript"}`, `{"buffer":2,`+tabs+`,"fileType":"javascript"}`)

	s.opened(1, buf("a.js", "javascript"))
	drain()
	want("同じ値は送り直さない")

	s.opened(1, buf("a.js", "json"))
	drain()
	want("ファイルの種類が変われば、同じ値でも送り直す", `{"buffer":1,`+tabs+`,"fileType":"json"}`)
	s.opened(1, buf("a.js", "javascript"))
	drain()
	sent = nil

	writeTree(t, root, map[string]string{"biome.json": `{"formatter": {"indentStyle": "space", "indentWidth": 2}}`})
	s.saved(3)
	drain()
	want("設定ファイルでないファイルの保存は、そのバッファだけを読み直す")
	s.opened(4, buf("biome.json", "json"))
	drain()
	sent = nil
	s.saved(4)
	drain()
	spaces := `"options":{"indent.useSpaces":true,"indent.width":2,"tab.width":2}`
	want("設定ファイルの保存は、すべてのバッファを読み直す",
		`{"buffer":1,`+spaces+`,"fileType":"javascript"}`, `{"buffer":2,`+spaces+`,"fileType":"javascript"}`)

	writeTree(t, root, map[string]string{"biome.json": `{"formatter": `})
	s.saved(4)
	drain()
	s.saved(4)
	drain()
	want("前の値を読んだ設定ファイルを読めなければ送らない")
	if len(warned) != 1 || !strings.Contains(warned[0], "biome.json") {
		t.Errorf("知らせ = %q, want 1 回", warned)
	}

	s.opened(5, buf("d.py", "python"))
	drain()
	want("ruff の値", `{"buffer":5,`+ruff2+`,"fileType":"python"}`)
	s.opened(5, buf("d.py", "javascript"))
	drain()
	want("別の設定ファイルを読めなければ、前の値を取り下げる", `{"buffer":5,`+nulls+`,"fileType":"javascript"}`)

	s.opened(1, buf("a.js", "text"))
	drain()
	want("整形ツールの無いファイルの種類になったら取り下げる", `{"buffer":1,`+nulls+`,"fileType":"text"}`)

	s.closed(2)
	s.saved(4)
	drain()
	want("閉じたバッファには送らない")
}
