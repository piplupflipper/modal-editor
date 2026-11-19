package ext

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStatusLineText(t *testing.T) {
	for in, want := range map[string]string{
		"!12 3/10":       "!12 3/10",
		"a\nb\tc\rd":     "a b c d",
		"x\x1b[31my\x7f": "x [31my ",
		"日本語\u0085":      "日本語 ",
	} {
		if got := StatusLineText(in); got != want {
			t.Errorf("StatusLineText(%q) = %q, want %q", in, got, want)
		}
	}
}

// 宣言ファイルの contributes.statusline と statusline/set の params の JSON の形（基本設計 5.1、5.3）。
func TestStatusLineJSON(t *testing.T) {
	var m Manifest
	src := `{"id": "mrreview", "command": "./x", "contributes": {"statusline": [{"id": "mrreview.status", "description": "レビューの状態"}]}}`
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		t.Fatal(err)
	}
	if want := []StatusLinePart{{ID: "mrreview.status", Description: "レビューの状態"}}; !reflect.DeepEqual(m.Contributes.StatusLine, want) {
		t.Errorf("StatusLine = %+v, want %+v", m.Contributes.StatusLine, want)
	}
	if _, err := m.Validate(); err != nil {
		t.Errorf("Validate = %v", err)
	}
	b, err := json.Marshal(StatusLineSetParams{Part: "mrreview.status", Text: "!12"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"part":"mrreview.status","text":"!12"}`; got != want {
		t.Errorf("params = %s, want %s", got, want)
	}
}
