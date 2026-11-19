package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/piplupflipper/modal-editor/ext"
)

// wordAt は、位置の前後の識別子を返す。位置が識別子の外なら空を返す。
func TestWordAt(t *testing.T) {
	for _, c := range []struct {
		line string
		at   int
		want string
	}{
		{"x := fooBar(1)", 6, "fooBar"},
		{"x := fooBar(1)", 5, "fooBar"},
		{"x := fooBar(1)", 11, "fooBar"},
		{"x := fooBar(1)", 2, ""},
		{"名前_1 + 2", 0, "名前_1"},
		{"abc", 99, "abc"},
	} {
		if got := wordAt(c.line, c.at); got != c.want {
			t.Errorf("wordAt(%q, %d) = %q, want %q", c.line, c.at, got, c.want)
		}
	}
}

// documentSymbol の木は深さ優先で平らにし、親の名前を前に付ける。SymbolInformation の形も読む。
//
// req: FR-94
func TestSymbolTargets(t *testing.T) {
	r := &request{enc: encUTF8, buffer: 4, version: 9, lines: lines{"package p", "type T struct{}", "func (T) M() {}"}}
	var tree []documentSymbol
	if err := json.Unmarshal([]byte(`[
  {"name": "T", "kind": 23, "range": {"start": {"line": 1, "character": 0}, "end": {"line": 1, "character": 15}},
   "selectionRange": {"start": {"line": 1, "character": 5}, "end": {"line": 1, "character": 6}},
   "children": [{"name": "M", "kind": 6, "selectionRange": {"start": {"line": 2, "character": 9}, "end": {"line": 2, "character": 10}}}]}
]`), &tree); err != nil {
		t.Fatal(err)
	}
	got := symbolTargets(r, tree)
	if len(got) != 2 || got[0].label != "T (struct) 2" || got[1].label != "T.M (method) 3" {
		t.Fatalf("labels = %+v", got)
	}
	if p := got[1].open.Position; p == nil || *p != (ext.Position{Line: 2, Byte: 9}) || *got[1].open.Buffer != 4 || *got[1].open.Version != 9 {
		t.Errorf("open = %+v", got[1].open)
	}

	var flat []documentSymbol
	if err := json.Unmarshal([]byte(`[{"name": "M", "kind": 6, "containerName": "T",
  "location": {"uri": "file:///p.go", "range": {"start": {"line": 2, "character": 9}, "end": {"line": 2, "character": 10}}}}]`), &flat); err != nil {
		t.Fatal(err)
	}
	if got := symbolTargets(r, flat); len(got) != 1 || got[0].label != "T.M (method) 3" {
		t.Errorf("SymbolInformation: %+v", got)
	}
}

// 名前の変更の WorkspaceEdit は、changes と documentChanges の両方を文書ごとの編集にまとめ、
// 同期していないファイルかファイルの操作を含めば適用しない。
//
// req: FR-94
func TestRenameEdits(t *testing.T) {
	c := &client{docs: map[ext.BufferID]*document{}, cfg: &serverConfig{Command: "fakels"}}
	for _, d := range []struct {
		id   ext.BufferID
		path string
		text []string
	}{{1, "/m/a.go", []string{"var foo = 1", "_ = foo"}}, {2, "/m/b.go", []string{"x := foo"}}} {
		c.docs[d.id] = &document{
			buffer: d.id, path: d.path, uri: pathToURI(d.path), finalNewline: true,
			history: []snapshot{{version: ext.Version(d.id * 10), lines: d.text}},
		}
	}
	parse := func(s string) *workspaceEdit {
		t.Helper()
		var w workspaceEdit
		if err := json.Unmarshal([]byte(s), &w); err != nil {
			t.Fatal(err)
		}
		return &w
	}
	rg := func(l, s, e int) string {
		return `{"start":{"line":` + itoa(l) + `,"character":` + itoa(s) + `},"end":{"line":` + itoa(l) + `,"character":` + itoa(e) + `}}`
	}
	w := parse(`{"changes": {"file:///m/a.go": [{"range": ` + rg(0, 4, 7) + `, "newText": "bar"}, {"range": ` + rg(1, 4, 7) + `, "newText": "bar"}]},
  "documentChanges": [{"textDocument": {"uri": "file:///m/b.go", "version": 20}, "edits": [{"range": ` + rg(0, 5, 8) + `, "newText": "bar"}]}]}`)
	targets, _, err := c.renameEdits(w, encUTF8)
	if err != nil {
		t.Fatal(err)
	}
	var got []ext.BufferEditParams
	for _, tg := range targets {
		got = append(got, tg.params)
		if tg.opened || len(tg.before) == 0 {
			t.Errorf("target %d: opened %v, before %q", tg.params.Buffer, tg.opened, tg.before)
		}
	}
	want := []ext.BufferEditParams{
		{Buffer: 1, Transaction: ext.Transaction{BaseVersion: 10, Edits: []ext.Edit{
			{Range: ext.Range{Start: ext.Position{Line: 1, Byte: 4}, End: ext.Position{Line: 1, Byte: 7}}, Text: "bar"},
			{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 4}, End: ext.Position{Line: 0, Byte: 7}}, Text: "bar"},
		}}},
		{Buffer: 2, Transaction: ext.Transaction{BaseVersion: 20, Edits: []ext.Edit{
			{Range: ext.Range{Start: ext.Position{Line: 0, Byte: 5}, End: ext.Position{Line: 0, Byte: 8}}, Text: "bar"},
		}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("renameEdits =\n%+v\nwant\n%+v", got, want)
	}

	w = parse(`{"changes": {"file:///m/a.go": [{"range": ` + rg(0, 4, 7) + `, "newText": "bar"}], "file:///m/c.go": [{"range": ` + rg(0, 0, 1) + `, "newText": "bar"}]}}`)
	// 同期していないファイルの編集は、パスごとに分けて返す（呼び出し側が本体に開かせる。FR-109）。
	if got, unopened, err := c.renameEdits(w, encUTF8); err != nil || len(got) != 1 || len(unopened) != 1 || len(unopened["/m/c.go"]) != 1 {
		t.Errorf("unopened: edits %v, unopened %v, %v", got, unopened, err)
	}
	w = parse(`{"changes": {"untitled:x": [{"range": ` + rg(0, 0, 1) + `, "newText": "bar"}]}}`)
	if _, _, err := c.renameEdits(w, encUTF8); err == nil {
		t.Error("non-file URI: err = nil")
	}
	w = parse(`{"documentChanges": [{"kind": "rename", "oldUri": "file:///m/a.go", "newUri": "file:///m/d.go"}]}`)
	if _, _, err := c.renameEdits(w, encUTF8); err == nil {
		t.Error("resource operation: err = nil")
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
