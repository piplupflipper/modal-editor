package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// root は、このパッケージのテストから見たリポジトリのルートである。
const root = "../.."

// docs/agent の文書の印の間が、コードの表から生成したものと同じである。
// 表（設定、操作、Ex コマンド、拡張機能 API）を変えて make docs を打ち忘れると、ここと make docs-check が落ちる。
//
// req: FR-172
func TestDocsUpToDate(t *testing.T) {
	gen, err := newGenerator(root)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, agentDir, "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("文書が無い: %v", err)
	}
	used := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fill(string(data), func(name string) (string, error) {
			used[name] = true
			return gen.block(name)
		})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got != string(data) {
			t.Errorf("%s の一覧がコードの表とずれている。make docs で生成し直す", p)
		}
	}
	// 生成の関数を足したのに、どの文書にも印を置き忘れたら気づけるようにする。
	for _, name := range []string{
		"options", "config-keys", "actions", "ex-commands", "host-commands", "provider-commands",
		"api-version", "env", "manifest", "methods", "types",
	} {
		if !used[name] {
			t.Errorf("印 %s がどの文書にも無い", name)
		}
	}
}

// -check は、一覧がコードの表とずれた文書で失敗し、ファイルを書き換えない。ずれが無ければ成功する。
//
// req: FR-172
func TestCheckDetectsStale(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(filepath.Join(root, "ext"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(abs, filepath.Join(dir, "ext")); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, agentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, agentDir, "a.md")
	stale := "# 例\n\n<!-- generated:options:begin -->\n| 古い表 |\n<!-- generated:options:end -->\n"
	if err = os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"-root", dir, "-check"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "make docs") {
		t.Fatalf("ずれた文書の -check の誤り = %v, want make docs の案内", err)
	}
	if data, _ := os.ReadFile(path); string(data) != stale {
		t.Errorf("-check がファイルを書き換えた:\n%s", data)
	}
	if err := run([]string{"-root", dir}, io.Discard); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "古い表") || !strings.Contains(string(data), "| `lineNumber.show` |") {
		t.Errorf("生成し直した文書:\n%s", data)
	}
	if err := run([]string{"-root", dir, "-check"}, io.Discard); err != nil {
		t.Errorf("生成し直した後の -check = %v", err)
	}
}

// 印の誤り（知らない名前、終わりの無い始まり、始まりの無い終わり、入れ子）は誤りにする。印の外はそのまま残す。
func TestFill(t *testing.T) {
	gen := func(name string) (string, error) {
		if name != "a" && name != "b" {
			return (&generator{}).block(name)
		}
		return "<" + name + ">\n", nil
	}
	got, err := fill("x\n<!-- generated:a:begin -->\nold\n<!-- generated:a:end -->\ny\n<!-- generated:b:begin -->\n<!-- generated:b:end -->\n", gen)
	if want := "x\n<!-- generated:a:begin -->\n<a>\n<!-- generated:a:end -->\ny\n<!-- generated:b:begin -->\n<b>\n<!-- generated:b:end -->\n"; err != nil || got != want {
		t.Errorf("fill = %q, %v, want %q", got, err, want)
	}
	for _, bad := range []string{
		"<!-- generated:zzz:begin -->\n<!-- generated:zzz:end -->\n",
		"<!-- generated:a:begin -->\n",
		"<!-- generated:a:end -->\n",
		"<!-- generated:a:begin -->\n<!-- generated:b:begin -->\n<!-- generated:b:end -->\n<!-- generated:a:end -->\n",
	} {
		if _, err := fill(bad, gen); err == nil {
			t.Errorf("fill(%q) が誤りにならない", bad)
		}
	}
}

// 括弧の中の設計の文書と issue と PR への参照を除き、ほかの断片は残す。
func TestDropRefs(t *testing.T) {
	for in, want := range map[string]string{
		"名前である（名前は仮。基本設計 5.1）。":            "名前である（名前は仮）。",
		"操作の列である（基本設計 5.1、5.5。FR-94）。":     "操作の列である。",
		"読む（#9 の裁定）":                       "読む",
		"選ぶ（選び方は Manifest.Provides の doc）": "選ぶ（選び方は Manifest.Provides の doc）",
		"キー（PR #249 のレビュー。ファインダー）":         "キー（ファインダー）",
	} {
		if got := dropRefs(in); got != want {
			t.Errorf("dropRefs(%q) = %q, want %q", in, got, want)
		}
	}
}

// メソッドの doc の最初の文から向きと種類を読めなければ、生成を誤りにする（書き方の違うメソッドを黙って落とさない）。
func TestMethodsRequireDirection(t *testing.T) {
	api := &extAPI{consts: []constInfo{{name: "MethodX", value: "x/y", doc: "MethodX は、何かである。"}}}
	if _, err := api.methods(); err == nil {
		t.Error("向きの無いメソッドが誤りにならない")
	}
	api.consts[0].doc = "MethodX は、本体から拡張機能への通知である。params は XParams。"
	ms, err := api.methods()
	if err != nil || len(ms) != 1 || !ms[0].fromHost || ms[0].request || ms[0].params != "XParams" {
		t.Errorf("methods = %+v, %v", ms, err)
	}
}
