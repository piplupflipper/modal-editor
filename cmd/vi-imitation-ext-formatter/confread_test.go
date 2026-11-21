package main

import (
	"reflect"
	"strings"
	"testing"
)

// TOML の小さな読み手は、表の見出しと、文字列、整数、真偽の値を完全な鍵で読み、配列、複数行の文字列、日時などは読み飛ばす。
// pyproject.toml によく書く形（複数行の配列、インラインの表、点で区切った鍵、注釈）を読める。
func TestParseTOML(t *testing.T) {
	src := bom + `# 注釈
[project]
name = "x" # 名前
version = '1.0'
authors = [
  { name = "a", email = "a@example.com" },  # ] を含む注釈
  "b]",
]
description = """
複数行の
"説明" """
date = 1979-05-27 07:32:00Z
ratio = 1.5
big = 1_000
hex = 0xff

[tool.ruff]
line-length = 100
format.indent-style = "tab"
lint = { select = ["E"], fixable = true }
"quoted.key" = -3

[[tool.other]]
x = true
[tool.ruff.format]
quote-style = 'single'
`
	got, err := parseTOML(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"project.name": "x", "project.version": "1.0", "project.authors": otherValue{}, "project.description": otherValue{},
		"project.date": otherValue{}, "project.ratio": otherValue{}, "project.big": int64(1000), "project.hex": int64(255),
		"tool.ruff.line-length": int64(100), "tool.ruff.format.indent-style": "tab",
		"tool.ruff.lint.select": otherValue{}, "tool.ruff.lint.fixable": true, "tool.ruff.quoted.key": int64(-3),
		"tool.other[].x": true, "tool.ruff.format.quote-style": "single",
		// 表の見出しの印（鍵の無い表を見分けるため）。
		"project.": otherValue{}, "tool.ruff.": otherValue{}, "tool.other[].": otherValue{}, "tool.ruff.format.": otherValue{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTOML =\n%v\nwant\n%v", got, want)
	}

	// TOML 1.1 の書き方（複数行のインラインの表、閉じる前のカンマ、\e と \xHH）を読む（ruff 0.15.16 が受け付ける）。
	got, err = parseTOML("a = \"x\\e\\x41\\u00e9\"\nformat = {\n  # 注釈\n  indent-style = \"tab\",\n  b = { c = 1, },\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"a": "x\x1bAé", "format.indent-style": "tab", "format.b.c": int64(1)}; !reflect.DeepEqual(got, want) {
		t.Errorf("TOML 1.1 = %v, want %v", got, want)
	}
	for _, bad := range []string{
		"[tool.ruff\nx = 1\n",
		"x = \n",
		"x = 1 2\n",
		"x = \"abc\n",
		"x = [1,\n",
		"just words\n",
		"x = \"\"\"abc\n",
		"x = 0123\n",
		"x = \"\\q\"\n",
		"x = \"\\x4\"\n",
		"x = { a = 1 b = 2 }\n",
	} {
		if _, err := parseTOML(bad); err == nil {
			t.Errorf("parseTOML(%q) は誤りにならない", bad)
		}
	}
	if _, err := parseTOML("a = 1\nb = oops\n"); err == nil || !strings.Contains(err.Error(), "2 行目") {
		t.Errorf("誤りの行の番号: %v", err)
	}
}

// YAML の小さな読み手は、最上位の「鍵: 値」だけを読み、入れ子と列は読み飛ばす。最上位が列なら誤りにする。
func TestParseYAMLTop(t *testing.T) {
	src := `---
# 注釈
useTabs: true
tabWidth: 4 # 幅
semi: False
singleQuote: "yes"
'printWidth': 80
plugins:
  - a
  - b
overrides:
  - files: "*.md"
    options:
      tabWidth: 2
endOfLine: lf
proseWrap: |
  text
key:with-colon: x
`
	got, err := parseYAMLTop(src)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"useTabs": true, "tabWidth": int64(4), "semi": false, "singleQuote": "yes", "printWidth": int64(80),
		"plugins": otherValue{}, "overrides": otherValue{}, "endOfLine": "lf", "proseWrap": otherValue{}, "key:with-colon": "x",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseYAMLTop =\n%v\nwant\n%v", got, want)
	}
	for _, bad := range []string{
		"- a\n- b\n", "useTabs true\n", "tabWidth: \"4\n",
		"tabWidth: 2\n{useTabs: true}\n", "[a, b]\n", "{useTabs: true\n", "{useTabs: true} x\n", "{useTabs true}\n", "  useTabs: true\n",
	} {
		if _, err := parseYAMLTop(bad); err == nil {
			t.Errorf("parseYAMLTop(%q) は誤りにならない", bad)
		}
	}
}

// YAML の流れの形のマッピング（prettier 3.6.2 が .prettierrc で受け付ける、鍵を引用しない形と単引用符の形）を読む。
func TestParseYAMLFlow(t *testing.T) {
	got, err := parseYAMLTop("# 注釈\n{\n  useTabs: true,  # タブ\n  'tabWidth': 4,\n  \"semi\": false,\n  plugins: [a, \"b]\"],\n  endOfLine: 'l''f',\n  o: {x: 1},\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"useTabs": true, "tabWidth": int64(4), "semi": false, "plugins": otherValue{}, "endOfLine": "l'f", "o": otherValue{}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flow = %v, want %v", got, want)
	}
}
