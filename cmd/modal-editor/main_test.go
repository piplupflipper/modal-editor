package main

import (
	"bytes"
	"errors"
	"flag"
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
		{name: "引数なしは未実装のエラー", args: nil, wantErr: true},
		{name: "未知のフラグはエラー", args: []string{"--unknown"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := run(tt.args, &stdout, &stderr)

			if (err != nil) != tt.wantErr {
				t.Fatalf("run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := stdout.String(); got != tt.wantStdout {
				t.Errorf("stdout = %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer

	err := run([]string{"--help"}, &stdout, &stderr)

	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("run() error = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(stderr.String(), "-version") {
		t.Errorf("stderr に使い方が出力されていない: %q", stderr.String())
	}
}
