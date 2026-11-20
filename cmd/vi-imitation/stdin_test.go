package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// - はほかのファイルと一緒に書けず、2 つ書くこともできない（Vim 9.1 の Too many edit arguments）。
//
// req: FR-158
func TestHasStdinArg(t *testing.T) {
	for _, tt := range []struct {
		files   []string
		want    bool
		wantErr string
	}{
		{nil, false, ""},
		{[]string{"a.txt"}, false, ""},
		{[]string{"-"}, true, ""},
		{[]string{"./-"}, false, ""},
		{[]string{"-", "a.txt"}, false, `"a.txt"`},
		{[]string{"a.txt", "-"}, false, `"-"`},
		{[]string{"-", "-"}, false, `"-"`},
	} {
		got, err := hasStdinArg(tt.files)
		switch {
		case tt.wantErr == "" && (err != nil || got != tt.want):
			t.Errorf("hasStdinArg(%q) = %v, %v; want %v", tt.files, got, err, tt.want)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("hasStdinArg(%q) の誤り = %v, want %s を示す誤り", tt.files, err, tt.wantErr)
		}
	}
}

// 標準入力を終わりまで読み、読む前に 1 行を出す。ファイルをつないだときも同じ中身になる。
// SIGINT（<C-c>）を受けたら、読んだ中身を捨てて終わりまで読み、空の中身を返す（Vim 9.1 と同じ）。
//
// req: FR-158
func TestReadStdin(t *testing.T) {
	var msg bytes.Buffer
	got, err := readStdinUntil(strings.NewReader("hoge\nあいう\n\xff"), &msg, nil)
	if err != nil || got != "hoge\nあいう\n\xff" {
		t.Errorf("readStdinUntil = %q, %v", got, err)
	}
	if msg.String() != stdinMessage+"\n" {
		t.Errorf("読む前の行 = %q, want %q", msg.String(), stdinMessage+"\n")
	}

	path := filepath.Join(t.TempDir(), "in.txt")
	if err = os.WriteFile(path, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if got, err = readStdinUntil(f, io.Discard, nil); err != nil || got != "from file\n" {
		t.Errorf("ファイルをつないだ readStdinUntil = %q, %v", got, err)
	}

	pr, pw := io.Pipe()
	intr := make(chan os.Signal, 1)
	done := make(chan string, 1)
	go func() {
		s, rerr := readStdinUntil(pr, io.Discard, intr)
		if rerr != nil {
			t.Error(rerr)
		}
		done <- s
	}()
	_, _ = pw.Write([]byte("before\n"))
	intr <- syscall.SIGINT
	_, _ = pw.Write([]byte("after\n")) // 受けた後も、終わりまで読む（書く側は止まらない）
	_ = pw.Close()
	if s := <-done; s != "" {
		t.Errorf("SIGINT の後の readStdinUntil = %q, want 空", s)
	}
}

// - を書いたときは、標準出力が端末でなければ、標準入力を読まずに誤りにする。
// - をほかのファイルと一緒に書いたときと、-listen と一緒に書いたときも、読まずに誤りにする。
//
// req: FR-158
func TestRunStdinErrors(t *testing.T) {
	stale := filepath.Join(t.TempDir(), "gone.sock")
	for _, tt := range []struct {
		name   string
		socket string
		args   []string
		want   error
	}{
		{"標準出力が端末でない", "", []string{"-"}, errNotTerminal},
		// :! の中で起動したシェルが server より長く残り、VI_IMITATION_SOCKET の server がもういない（PR #304 のレビューの推奨 4）。
		{"古い VI_IMITATION_SOCKET が残り、標準出力が端末でない", stale, []string{"-"}, errNotTerminal},
		{"ほかのファイルと一緒", "", []string{"-", "a.txt"}, nil},
		{"-listen と一緒", "", []string{"-listen", "x.sock", "-"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envSocket, tt.socket)
			in := &countReader{r: strings.NewReader("x\n")}
			var stdout, stderr bytes.Buffer
			err := run(tt.args, in, &stdout, &stderr)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Errorf("run(%q) = %v, want %v", tt.args, err, tt.want)
			}
			if in.n != 0 {
				t.Errorf("誤りの前に標準入力を %d 回読んだ", in.n)
			}
		})
	}
}

// countReader は、Read を呼んだ回数を数える。
type countReader struct {
	r io.Reader
	n int
}

func (c *countReader) Read(p []byte) (int, error) {
	c.n++
	return c.r.Read(p)
}
