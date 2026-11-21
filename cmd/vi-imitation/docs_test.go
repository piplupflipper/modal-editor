package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/docs"
)

// runDocsArgs は、端末でない入出力で run に --docs と args を渡し、標準出力と誤りを返す。
func runDocsArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err := run(append([]string{"--docs"}, args...), strings.NewReader(""), &out, &errOut)
	return out.String(), err
}

// --docs は端末を要さずに、話題の一覧、話題の全体、話題の 1 つの節を標準出力に出す。
//
// req: FR-172
func TestDocsTopics(t *testing.T) {
	list, err := runDocsArgs(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config", "keymap", "commands", "extensions", "extension-types", "debugging", "usage"} {
		tp, ok := docs.LookupTopic(name)
		if !ok {
			t.Fatalf("話題 %s が無い", name)
		}
		if !strings.Contains(list, "| "+name+" | ") {
			t.Errorf("話題の一覧に %s が無い:\n%s", name, list)
		}
		got, gerr := runDocsArgs(t, name)
		if gerr != nil || got != tp.Text || got == "" {
			t.Errorf("--docs %s = %d バイト, %v, want %d バイト", name, len(got), gerr, len(tp.Text))
		}
	}

	got, err := runDocsArgs(t, "usage", "Ex コマンドの一覧")
	if err != nil || !strings.HasPrefix(got, "## Ex コマンドの一覧\n") || strings.Contains(got, "\n## ") {
		t.Errorf("--docs usage 'Ex コマンドの一覧' = %q, %v", got, err)
	}
	if got, err := runDocsArgs(t, "extension-types", "BufferOpenParams"); err != nil || !strings.Contains(got, "`text`") {
		t.Errorf("--docs extension-types BufferOpenParams = %q, %v", got, err)
	}
}

// 知らない話題、知らない節、多すぎる引数は誤りにし、標準出力に何も出さない。
//
// req: FR-172
func TestDocsErrors(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"config", "無い節"}, {"usage", "a", "b"}} {
		out, err := runDocsArgs(t, args...)
		if err == nil || out != "" {
			t.Errorf("--docs %q = %q, %v, want 誤り", args, out, err)
		}
	}
	if _, err := runDocsArgs(t, "nope"); err == nil || !strings.Contains(err.Error(), "vi-imitation --docs") {
		t.Errorf("知らない話題の誤り = %v, want 一覧の出し方の案内", err)
	}
}
