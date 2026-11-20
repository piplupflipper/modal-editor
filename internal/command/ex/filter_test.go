package ex

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
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

// TestTerminalPopup は、:terminal.popup {cmd} が、:!{cmd} と同じ読み方（"|" を含めて行末まで、先頭の空白を除き、% を展開しない）で、
// 端末を渡す実行の求めに Popup を付けて返し、編集を禁止したバッファでも使え、Finish でバッファを変えないことを確かめる。
// 引数が無ければ E471、範囲と "!" と省略形は使えず、:g の中では使えない。
//
// req: FR-168
func TestTerminalPopup(t *testing.T) {
	b := buffer.New("a\nb\n")
	b.SetReadOnly(true)
	res, err := Execute(b, nil, 0, "terminal.popup   git log --oneline % | cat ")
	if err != nil {
		t.Fatal(err)
	}
	r := res.Shell
	if r == nil || !r.Terminal || !r.Popup || r.Cmd != "git log --oneline % | cat " || r.Input != nil {
		t.Fatalf("Shell = %+v, want a popup request", r)
	}
	v := b.Version()
	fin, err := r.Finish(b, nil, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if b.Version() != v || fin.Notice.Text != "" {
		t.Errorf("Finish がバッファを変えたか知らせた: version %d -> %d, %+v", v, b.Version(), fin.Notice)
	}
	// :! の求めには Popup が付かない。
	if res, err := Execute(b, nil, 0, "!ls"); err != nil || res.Shell == nil || res.Shell.Popup {
		t.Errorf(":!ls = %+v, %v; want a request without Popup", res.Shell, err)
	}
	for _, tc := range []struct{ cmdline, code string }{
		{"terminal.popup", "E471"},
		{"terminal.popup   ", "E471"},
		{"1terminal.popup ls", "E481"},
		{"terminal.popup! ls", "E477"},
		{"terminal.popu ls", "E492"},
		{"terminal ls", "E492"},
		{"g/a/terminal.popup ls", "E319"},
	} {
		if res, err := Execute(b, nil, 0, tc.cmdline); errCode(t, err) != tc.code || res.Shell != nil {
			t.Errorf(":%s = %+v, %v; want %s", tc.cmdline, res.Shell, err, tc.code)
		}
	}
}
