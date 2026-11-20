package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/server"
	"github.com/piplupflipper/vi-imitation/internal/term/ptytest"
)

// 標準入力をパイプにして - で起動すると、読む前の行を出してから、読んだ中身を名前の無いバッファで開き、キーは制御端末から読む。
// 読んだバッファは未保存の変更がある扱いで、:q は E37 で断られる。:w ファイル で書き出し、:q で終わり、端末を戻す。
//
// req: FR-158
func TestStdinInPTY(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	p := startEditorStdin(t, 80, 24, t.TempDir(), nil, strings.NewReader("hoge\nあいう\n"), "-")
	before := ptytest.Termios(t, p.tty)
	p.out.WaitForString(t, "あいう")
	if o := p.out.String(); !strings.Contains(o, stdinMessage) || strings.Index(o, stdinMessage) > strings.Index(o, "\x1b[?1049h") {
		t.Errorf("代替画面に入る前に %q が無い: %q", stdinMessage, o)
	}
	p.out.WaitForString(t, "[No Name] [+]")
	p.write(t, ":q\r")
	p.out.WaitForString(t, "E37")
	p.write(t, ":w "+out+"\r")
	p.out.WaitForString(t, "out.txt")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "hoge\nあいう\n" {
		t.Errorf("書き出した中身 = %q, %v", got, err)
	}
	p.checkRestored(t, before)
}

// -R と一緒に書いた - のバッファは編集を禁止し（FR-119）、Vim 9.1 と同じく変更の無い扱いなので、:q で終われる。
//
// req: FR-158
func TestStdinReadOnlyInPTY(t *testing.T) {
	p := startEditorStdin(t, 80, 24, t.TempDir(), nil, strings.NewReader("abc\n"), "-R", "-")
	p.out.WaitForString(t, "abc")
	p.write(t, "x")
	p.out.WaitForString(t, "E21")
	p.write(t, ":q\r")
	if err := p.wait(t); err != nil {
		t.Fatalf("エディタの終了 = %v, want 正常終了。出力: %q", err, p.out.String())
	}
}

// - を書かずに標準入力をパイプにしたときは、今までどおり起動しない。- をほかのファイルと一緒に書いたときも起動しない。
//
// req: FR-158
func TestStdinErrorsInPTY(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, errNotTerminal.Error()},
		{[]string{"-", "a.txt"}, "編集の引数が多すぎる"},
	} {
		p := startEditorStdin(t, 80, 24, t.TempDir(), nil, strings.NewReader("abc\n"), tt.args...)
		var exitErr *exec.ExitError
		if err := p.wait(t); !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Errorf("%q: エディタの終了 = %v, want 終了コード 1", tt.args, err)
		}
		p.out.WaitForString(t, tt.want)
	}
}

// 既にある server（1 つのプロセスの構成の server のソケット）に -connect で繋いだ client の - の中身は、その client のタブとして開く。
// :! の中で起動したときと同じく VI_IMITATION_SOCKET を渡した - は、待つだけの client として、その server の client のタブに開かせ、
// タブを閉じたら終了コード 0 で終わる。
//
// req: FR-158
func TestStdinExistingServerInPTY(t *testing.T) {
	runtime := t.TempDir()
	a := startEditorIn(t, t.TempDir(), []string{"XDG_RUNTIME_DIR=" + runtime})
	a.out.WaitForString(t, "NORMAL")
	matches, err := filepath.Glob(filepath.Join(runtime, "vi-imitation", strconv.Itoa(a.cmd.Process.Pid)+"-*", server.SocketName))
	if err != nil || len(matches) != 1 {
		t.Fatalf("server のソケット = %q, %v", matches, err)
	}
	sock := matches[0]

	b := startEditorStdin(t, 80, 24, t.TempDir(), nil, strings.NewReader("second\n"), "-connect", sock, "-")
	b.out.WaitForString(t, "second")
	b.out.WaitForString(t, "[No Name] [+]")
	b.write(t, ":q!\r")
	if err := b.wait(t); err != nil {
		t.Fatalf("繋いだ client の終了 = %v, want 正常終了。出力: %q", err, b.out.String())
	}

	w := exec.Command(os.Args[0], "-") //nolint:gosec // テストの実行ファイル自身を待つだけの client として起動する
	w.Env = append(muxFreeEnv(), envRunMain+"=1", envSocket+"="+sock)
	w.Stdin = strings.NewReader("piped-by-wait\n")
	var wout strings.Builder
	w.Stdout, w.Stderr = &wout, &wout
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	a.out.WaitForString(t, "piped-by-wait")
	a.write(t, ":q!\r")
	if err := w.Wait(); err != nil {
		t.Errorf("待つだけの client の終了 = %v, want 正常終了。出力: %q", err, wout.String())
	}
	a.write(t, ":q\r")
	if err := a.wait(t); err != nil {
		t.Fatalf("最初の client の終了 = %v, want 正常終了。出力: %q", err, a.out.String())
	}
}
