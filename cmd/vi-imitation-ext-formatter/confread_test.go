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
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseTOML =\n%v\nwant\n%v", got, want)
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
	for _, bad := range []string{"- a\n- b\n", "useTabs true\n", "tabWidth: \"4\n"} {
		if _, err := parseYAMLTop(bad); err == nil {
			t.Errorf("parseYAMLTop(%q) は誤りにならない", bad)
		}
	}
}
