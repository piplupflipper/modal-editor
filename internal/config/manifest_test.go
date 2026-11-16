package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// writeManifest は、dir/name/manifest.json に src を書き、そのパスを返す。
func writeManifest(t *testing.T, dir, name, src string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, ManifestFileName)
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// surroundManifest は、基本設計 5.1 の例の宣言ファイルである。
const surroundManifest = `{
  // 拡張機能の ID。ドットリピートの記録と設定で使う
  "id": "surround",
  "command": "./modal-editor-ext-surround",
  // 起動の契機。どれか 1 つが成り立った時点で起動する
  "activation": [
    { "on": "operator", "id": "surround.add" },
    { "on": "filetype", "pattern": "markdown" },
  ],
  // 追加する操作。キーとの対応は利用者の設定で上書きできる
  "contributes": {
    "operators": [{ "id": "surround.add", "keys": "ys" }],
    "textObjects": [{ "id": "surround.inner", "keys": "is" }],
  },
}
`

// コメントと末尾のカンマを含む宣言ファイルを読み、検査を通す。
//
// req: FR-40
func TestLoadManifestJSONC(t *testing.T) {
	p := writeManifest(t, t.TempDir(), "surround", surroundManifest)
	m, warns, err := LoadManifest(p)
	if err != nil || len(warns) != 0 {
		t.Fatalf("LoadManifest: warns = %v, err = %v", warns, err)
	}
	want := ext.Manifest{
		ID:      "surround",
		Command: "./modal-editor-ext-surround",
		Activation: []ext.Activation{
			{On: ext.ActivationOperator, ID: "surround.add"},
			{On: ext.ActivationFileType, Pattern: "markdown"},
		},
		Contributes: ext.Contributes{
			Operators:   []ext.Contribution{{ID: "surround.add", Keys: "ys"}},
			TextObjects: []ext.Contribution{{ID: "surround.inner", Keys: "is"}},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("Manifest = %+v, want %+v", m, want)
	}
}

// リポジトリの標準の拡張機能の宣言ファイルは、すべて読めて検査を通る。
func TestLoadManifestRepository(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "cmd", "*", ManifestFileName))
	if err != nil || len(paths) == 0 {
		t.Fatalf("宣言ファイルが見つからない: %v", err)
	}
	for _, p := range paths {
		if _, warns, err := LoadManifest(p); err != nil || len(warns) != 0 {
			t.Errorf("%s: warns = %v, err = %v", p, warns, err)
		}
	}
}

func TestLoadManifestErrors(t *testing.T) {
	cases := []struct {
		name, src string
		// line は誤りの行で、0 なら位置を問わない。
		line int
		msg  string
	}{
		{"構文", "{\n  \"id\": \"a\"\n  \"command\": \"x\"\n}", 3, "JSONC として読めない"},
		{"最上位が配列", "[]", 1, "最上位はオブジェクト"},
		{"型の違い", "{\n  \"id\": \"a\",\n  \"command\": 1,\n  \"activation\": []\n}", 3, "command の型が違う"},
		{"検査の誤り", `{"id": "a", "command": "x", "activation": []}`, 0, "宣言ファイルの誤り"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeManifest(t, t.TempDir(), "a", c.src)
			_, _, err := LoadManifest(p)
			var pr *Error
			if !errors.As(err, &pr) {
				t.Fatalf("err = %v, want *Error", err)
			}
			if (c.line != 0 && pr.Line != c.line) || !strings.Contains(pr.Msg, c.msg) || pr.Path != p {
				t.Errorf("err = %v, want %d 行目に %q", err, c.line, c.msg)
			}
		})
	}
}

// 未知の項目と未知の契機は、誤りにせず警告にする。
func TestLoadManifestWarnings(t *testing.T) {
	src := `{
  "id": "a",
  "command": "x",
  "future": true,
  "activation": [{ "on": "socket" }, { "on": "command", "id": "a.run" }],
  "contributes": { "commands": [{ "id": "a.run" }] }
}`
	p := writeManifest(t, t.TempDir(), "a", src)
	m, warns, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 2 || !strings.HasPrefix(warns[0].Error(), p+":4:3: 未知の項目 future") || !strings.Contains(warns[1].Error(), "socket") {
		t.Errorf("warns = %v", warns)
	}
	if m.ID != "a" {
		t.Errorf("ID = %q", m.ID)
	}
}

func TestLoadExtensions(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	writeManifest(t, first, "lsp", `{"id": "lsp", "command": "./lsp", "activation": [{"on": "filetype", "pattern": "go"}],}`)
	writeManifest(t, first, "broken", `{"id": "broken", "command": "x", "activation": []}`)
	if err := os.MkdirAll(filepath.Join(first, "no-manifest"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dup := writeManifest(t, second, "lsp2", `{"id": "lsp", "command": "./other", "activation": [{"on": "filetype", "pattern": "c"}]}`)
	writeManifest(t, second, "acp", `{"id": "acp", "command": "/usr/bin/acp", "activation": [{"on": "command", "id": "acp.ask"}], "contributes": {"commands": [{"id": "acp.ask"}]}}`)

	cfg, errs := Parse("config.jsonc", []byte(`{
  "extensions": {
    "settings": { "lsp": { "servers": [] }, "nothing": {} },
    "activation": { "lsp": [{ "on": "filetype", "pattern": "python" }] },
  },
}`))
	if len(errs) != 0 {
		t.Fatal(errs)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	exts, problems := LoadExtensions([]string{first, missing, second}, cfg)

	if len(exts) != 2 {
		t.Fatalf("exts = %+v", exts)
	}
	lsp, acp := exts[0], exts[1]
	if lsp.Manifest.ID != "lsp" || lsp.Dir != filepath.Join(first, "lsp") || string(lsp.Settings) != `{"servers":[]}` {
		t.Errorf("lsp = %+v", lsp)
	}
	wantAct := []ext.Activation{{On: ext.ActivationFileType, Pattern: "go"}, {On: ext.ActivationFileType, Pattern: "python"}}
	if !reflect.DeepEqual(lsp.Manifest.Activation, wantAct) {
		t.Errorf("lsp の契機 = %+v, want %+v", lsp.Manifest.Activation, wantAct)
	}
	if acp.Manifest.ID != "acp" || acp.Dir != filepath.Join(second, "acp") || acp.Settings != nil {
		t.Errorf("acp = %+v", acp)
	}

	// broken の検査の誤り、lsp2 の ID の重なり、設定にある見つからない ID の 3 件。
	ps := problemsOf(t, problems)
	if len(ps) != 3 {
		t.Fatalf("problems = %v", problems)
	}
	if !strings.Contains(ps[0].Path, "broken") || ps[1].Path != dup || !strings.Contains(ps[1].Msg, "重なる") {
		t.Errorf("problems = %v", problems)
	}
	if ps[2].Path != "config.jsonc" || ps[2].Line != 3 || ps[2].Column != 45 || !strings.Contains(ps[2].Msg, "nothing") {
		t.Errorf("見つからない ID の誤り = %v", ps[2])
	}
}

// Extension の項目は exthost.Spec と同じ並びと型にする（呼び出し側が型の変換で写せるように）。
// exthost は同じ層なので import できず、ここでは項目の名前と型を固定して確かめる。
func TestExtensionFields(t *testing.T) {
	typ := reflect.TypeFor[Extension]()
	want := []struct {
		name string
		typ  reflect.Type
	}{
		{"Manifest", reflect.TypeFor[ext.Manifest]()},
		{"Dir", reflect.TypeFor[string]()},
		{"Settings", reflect.TypeFor[json.RawMessage]()},
	}
	if typ.NumField() != len(want) {
		t.Fatalf("項目の数 = %d", typ.NumField())
	}
	for i, w := range want {
		f := typ.Field(i)
		if f.Name != w.name || f.Type != w.typ {
			t.Errorf("項目 %d = %s %v, want %s %v", i, f.Name, f.Type, w.name, w.typ)
		}
	}
}
