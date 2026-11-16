package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStdout string
		wantErr    bool
	}{
		{name: "version フラグ", args: []string{"--version"}, wantStdout: "dev\n"},
		{name: "端末でなければエラー", args: nil, wantErr: true},
		{name: "未知のフラグはエラー", args: []string{"--unknown"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := run(tt.args, strings.NewReader(""), &stdout, &stderr)

			if (err != nil) != tt.wantErr {
				t.Fatalf("run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

// TestRunPipe は、標準入力と標準出力がファイルでも、端末でなければ起動しないことを確かめる。
func TestRunPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close() // テストの後始末
		_ = w.Close() // テストの後始末
	})
	var stderr bytes.Buffer
	if err := run(nil, r, w, &stderr); !errors.Is(err, errNotTerminal) {
		t.Errorf("run() error = %v, want errNotTerminal", err)
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)

	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run() error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(stderr.String(), "-version") {
		t.Errorf("stderr に使い方が出力されていない: %q", stderr.String())
	}
}

// TestGuardPanic は、panic したときに端末を戻してから panic を続けることを確かめる（基本設計 8 章）。
func TestGuardPanic(t *testing.T) {
	restored := 0
	defer func() {
		p := recover()
		if p != "boom" {
			t.Errorf("recover() = %v, want boom", p)
		}
		if restored != 1 {
			t.Errorf("restore を %d 回呼んだ, want 1", restored)
		}
	}()
	_ = guard(func() error { restored++; return nil }, func() error { panic("boom") })
	t.Error("panic が続かなかった")
}

// TestGuardError は、f の誤りを restore の誤りより優先して返し、f が成功したら restore の誤りを返すことを確かめる。
func TestGuardError(t *testing.T) {
	errF, errR := errors.New("f"), errors.New("r")
	if err := guard(func() error { return errR }, func() error { return errF }); !errors.Is(err, errF) {
		t.Errorf("guard = %v, want f の誤り", err)
	}
	if err := guard(func() error { return errR }, func() error { return nil }); !errors.Is(err, errR) {
		t.Errorf("guard = %v, want restore の誤り", err)
	}
}
