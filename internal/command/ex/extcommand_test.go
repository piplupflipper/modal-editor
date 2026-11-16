package ex

import (
	"reflect"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// 拡張機能のコマンドは、ID と完全に一致する名前で呼べ、ID の後の行末までを引数の文字列にする。
// 本体のコマンドの名前か省略形と同じ ID は、本体のコマンドを優先する。範囲と "!" は取らず、:g の中では使えない。
//
// req: FR-08, FR-11
func TestExtCommand(t *testing.T) {
	cmds := map[string]string{"acp.prompt": "acp", "dap.start": "dap", "w": "bad", "edit-x": "x"}
	st := &State{ExtCommand: func(id string) (string, bool) {
		e, ok := cmds[id]
		return e, ok
	}}
	ext := func(e, id, text string) Action {
		return Action{Kind: ActionExtCommand, Extension: e, Command: id, Text: text}
	}
	tests := []struct {
		name     string
		cmdline  string
		want     []Action
		wantCode string
	}{
		{name: "引数", cmdline: "acp.prompt この関数を説明して", want: []Action{ext("acp", "acp.prompt", "この関数を説明して")}},
		{name: "先頭の : と空白", cmdline: ": acp.prompt  hi ", want: []Action{ext("acp", "acp.prompt", "hi ")}},
		{name: "引数なし", cmdline: "dap.start", want: []Action{ext("dap", "dap.start", "")}},
		{name: "| と \" は引数に含める", cmdline: `acp.prompt a|q "b"`, want: []Action{ext("acp", "acp.prompt", `a|q "b"`)}},
		{name: "バッファの中で済むコマンドの後", cmdline: "s/a/X/|acp.prompt x", want: []Action{ext("acp", "acp.prompt", "x")}},
		{name: "動作を返すコマンドの後", cmdline: "w|acp.prompt x", wantCode: "E319"},
		{name: "動作を返すコマンドの後（:e）", cmdline: "e foo|dap.start", wantCode: "E319"},
		{name: "本体のコマンドを優先", cmdline: "w", want: []Action{{Kind: ActionWrite}}},
		{name: "本体の省略形と始まりが同じ ID", cmdline: "edit-x y", want: []Action{ext("x", "edit-x", "y")}},
		{name: "省略形では呼べない", cmdline: "acp.prom x", wantCode: "E492"},
		{name: "知らない ID", cmdline: "acp.other x", wantCode: "E492"},
		{name: "範囲", cmdline: "2acp.prompt x", wantCode: "E481"},
		{name: "!", cmdline: "acp.prompt! x", wantCode: "E477"},
		{name: ":g の中", cmdline: "g/a/acp.prompt x", wantCode: "E319"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := buffer.New("a\nb\n")
			res, err := Execute(b, st, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
				t.Errorf("actions = %+v, want %+v", res.Actions, tt.want)
			}
		})
	}
	// ExtCommand が nil なら、拡張機能のコマンドは呼べない。
	if _, err := Execute(buffer.New("a\n"), nil, 0, "acp.prompt x"); errCode(t, err) != "E492" {
		t.Errorf("ExtCommand が nil: err = %v", err)
	}
}
