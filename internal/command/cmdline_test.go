package command

import (
	"errors"
	"reflect"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/command/ex"
)

// コマンドラインモードは、入力中の文字列を CommandLine で見せ、<CR> で ex.Execute に渡す。
// :w のようなエディタの動作は Result.Actions で返る。
//
// req: FR-01
func TestCommandLine(t *testing.T) {
	s := setup(t, "|abc")
	feedKeys(s, ":wq foo<BS>")
	if s.Mode() != ModeCommandLine || s.CommandLine() != "wq fo" {
		t.Fatalf("mode = %v, line = %q", s.Mode(), s.CommandLine())
	}
	rs := feedKeys(s, "<CR>")
	want := []ex.Action{{Kind: ex.ActionWrite, File: "fo"}, {Kind: ex.ActionQuit}}
	if got := rs[0].Actions; !reflect.DeepEqual(got, want) {
		t.Errorf("Actions = %+v", got)
	}
	if s.Mode() != ModeNormal || s.CommandLine() != "" {
		t.Errorf("mode = %v, line = %q", s.Mode(), s.CommandLine())
	}
}

// 行の編集を呼び出し側が持つ場合は、確定した文字列を ExecuteCommandLine に渡す。
// Ex の誤りは Err で返り、:d が消した行はレジスターに入る。
//
// req: FR-01, FR-04
func TestExecuteCommandLine(t *testing.T) {
	s := setup(t, "|a\nb\nc")
	feedKeys(s, "x")
	var exErr *ex.Error
	if r := s.ExecuteCommandLine("q"); !errors.As(r.Err, &exErr) || exErr.Code != ex.CodeNoWrite {
		t.Errorf("q: err = %v", r.Err)
	}
	r := s.ExecuteCommandLine("2,3d x")
	if r.Err != nil || len(r.Changes) != 1 {
		t.Fatalf("2,3d: %+v", r)
	}
	if reg, _ := s.registers.Get('x'); reg.Text != "b\nc" || !reg.Linewise {
		t.Errorf("x = %+v", reg)
	}
	if got := s.Cursor().String(); got != "0:0" {
		t.Errorf("cursor = %s", got)
	}
}

// ビジュアルモードの : は、選択した行の範囲 '<,'> を入力した状態で始まり、その範囲に働く。
//
// req: FR-01
func TestVisualCommandLine(t *testing.T) {
	s := setup(t, "a\n|b\nc\nd")
	feedKeys(s, "vj:")
	if s.CommandLine() != "'<,'>" {
		t.Fatalf("line = %q", s.CommandLine())
	}
	feedKeys(s, "s/$/!/<CR>")
	if got := s.buf.Text(); got != "a\nb!\nc!\nd" {
		t.Errorf("text = %q", got)
	}
}

// ビジュアルモードの選択は Selection で読める。o で端を入れ替える。
//
// req: FR-01
func TestSelection(t *testing.T) {
	s := setup(t, "ab|c\ndef")
	if _, _, _, ok := s.Selection(); ok {
		t.Error("ノーマルモードで選択がある")
	}
	feedKeys(s, "vjh")
	start, end, linewise, ok := s.Selection()
	if !ok || start != pos(0, 2) || end != pos(1, 1) || linewise {
		t.Errorf("Selection = %v %v %v %v", start, end, linewise, ok)
	}
	feedKeys(s, "oV")
	start, end, linewise, _ = s.Selection()
	if start != pos(0, 2) || end != pos(1, 1) || !linewise || s.Cursor() != pos(0, 2) {
		t.Errorf("Selection = %v %v %v, cursor %v", start, end, linewise, s.Cursor())
	}
}
