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

// 範囲の ";" で移したカーソルは、誤りで終わっても残り、列は元のバイトの位置を行に収めて保つ（Vim 9.1 と同じ、issue #57）。
//
// req: FR-01
func TestCommandLineSemicolonCursor(t *testing.T) {
	s := setup(t, "  aaaa\n  bbbbbbb\n  ccc|cccc\n  dd\ne")
	if r := s.ExecuteCommandLine("2;9d"); r.Err == nil {
		t.Fatal("2;9d: want E16")
	}
	if got := s.Cursor().String(); got != "1:5" {
		t.Errorf("2;9d: cursor = %s, want 1:5", got)
	}
	if r := s.ExecuteCommandLine("4;/zzz/d"); r.Err == nil {
		t.Fatal("4;/zzz/d: want E486")
	}
	if got := s.Cursor().String(); got != "3:3" {
		t.Errorf("4;/zzz/d: cursor = %s, want 3:3", got)
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

// 逆順の範囲の :3,1d は確認を求め（Result.Confirm）、y の答えで続きを実行する。n なら何もしない。
//
// req: FR-08
func TestCommandLineConfirm(t *testing.T) {
	s := setup(t, "|a\nb\nc\nd")
	r := s.ExecuteCommandLine("3,1d")
	if r.Confirm == nil || r.Confirm.Prompt == "" {
		t.Fatalf("3,1d: Confirm = %+v, err = %v", r.Confirm, r.Err)
	}
	if got := s.buf.Text(); got != "a\nb\nc\nd" {
		t.Fatalf("確認の前に消した: %q", got)
	}
	if r := s.Confirm(r.Confirm, false); r.Err != nil || len(r.Changes) != 0 {
		t.Errorf("n: %+v", r)
	}
	r = s.ExecuteCommandLine("3,1d")
	r = s.Confirm(r.Confirm, true)
	if r.Err != nil || len(r.Changes) != 1 {
		t.Fatalf("y: %+v", r)
	}
	if got := s.buf.Text(); got != "d" {
		t.Errorf("text = %q", got)
	}
	if reg, _ := s.registers.Get('"'); reg.Text != "a\nb\nc" {
		t.Errorf("\" = %+v", reg)
	}
}
