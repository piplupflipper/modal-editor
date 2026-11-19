package ex

import (
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// TestTerminalShell は、範囲の無い :!{cmd} が、端末を渡す実行（ShellRequest.Terminal）を求め、
// "|" を含めて行末までを cmd にし、編集を禁止したバッファでも使え、Finish でバッファを変えずに終了コードだけを知らせることを確かめる。
//
// req: FR-80
func TestTerminalShell(t *testing.T) {
	b := buffer.New("a\nb\n")
	b.SetReadOnly(true)
	res, err := Execute(b, nil, 0, "! git commit | cat ")
	if err != nil {
		t.Fatal(err)
	}
	r := res.Shell
	if r == nil || !r.Terminal || r.Cmd != "git commit | cat " || r.Input != nil {
		t.Fatalf("Shell = %+v, want a terminal request", r)
	}
	v := b.Version()
	fin, err := r.Finish(b, nil, 0, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if b.Version() != v || b.Text() != "a\nb" {
		t.Errorf("バッファが変わった: %q", b.Text())
	}
	if fin.Notice.Text != "シェルが 2 を返しました" || !fin.Notice.Warning {
		t.Errorf("Notice = %+v", fin.Notice)
	}

	// コマンドが空なら何もしない。範囲のある :{range}! は、編集を禁止したバッファでは E21 で断る。
	if res, err := Execute(b, nil, 0, "!"); err != nil || res.Shell != nil {
		t.Errorf(":! = %+v, %v; want nothing", res.Shell, err)
	}
	if _, err := Execute(b, nil, 0, "%!sort"); errCode(t, err) != "E21" {
		t.Errorf(":%%!sort on a read-only buffer = %v, want E21", err)
	}
}
