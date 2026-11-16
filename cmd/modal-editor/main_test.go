package main

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/config"
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

// TestLoadConfig は、設定ファイルの項目を editor.Config へ写し、誤りのパスを短くすることを確かめる。
func TestLoadConfig(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	dir := filepath.Join(xdg, "config", "modal-editor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	text := `{
  "keymap": { "normal": { "Q": ":q<CR>" } },
  "fixendofline": false,
  "fileTypes": { ".tmpl": "html" },
  "extensions": { "syncTimeoutMs": 30, "startSyncTimeoutMs": 2500 },
  "typo": 1,
}`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(slog.New(slog.DiscardHandler))
	if cfg.StartSyncTimeout != 2500*time.Millisecond {
		t.Errorf("StartSyncTimeout = %v, want 2.5s", cfg.StartSyncTimeout)
	}
	if cfg.SyncTimeout == nil || *cfg.SyncTimeout != 30*time.Millisecond {
		t.Errorf("SyncTimeout = %v, want 30ms", cfg.SyncTimeout)
	}
	if cfg.FixEndOfLine == nil || *cfg.FixEndOfLine {
		t.Errorf("FixEndOfLine = %v, want false", cfg.FixEndOfLine)
	}
	if cfg.Keymap["normal"]["Q"] != ":q<CR>" || cfg.FileTypes[".tmpl"] != "html" {
		t.Errorf("Keymap = %v, FileTypes = %v", cfg.Keymap, cfg.FileTypes)
	}
	if len(cfg.Problems) != 1 || !strings.HasPrefix(cfg.Problems[0].Error(), "config.jsonc:6:") {
		t.Errorf("Problems = %v, want config.jsonc:6: で始まる 1 件", cfg.Problems)
	}
}

// TestShortProblem は、宣言ファイルの誤りを、拡張機能のディレクトリの名前とファイル名にすることを確かめる。
func TestShortProblem(t *testing.T) {
	err := shortProblem(&config.Error{Path: "/a/b/extensions/line/manifest.json", Line: 2, Column: 3, Msg: "x"})
	if got := err.Error(); got != "line/manifest.json:2:3: x" {
		t.Errorf("shortProblem = %q", got)
	}
	plain := errors.New("plain")
	if got := shortProblem(plain); got.Error() != "plain" {
		t.Error("config.Error でない誤りを変えた")
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
