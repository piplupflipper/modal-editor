package mux

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// envOf は、map を getenv の形の関数にする。
func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want Mux
	}{
		{
			name: "tmux",
			env:  map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0", "TMUX_PANE": "%3"},
			want: &Tmux{Bin: "tmux", Socket: "/tmp/tmux-1000/default", Pane: "%3"},
		},
		{
			name: "tmux が herdr より先",
			env: map[string]string{
				"TMUX": "/tmp/tmux-1000/default,1234,0", "TMUX_PANE": "%3",
				"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1", "HERDR_SOCKET_PATH": "/run/herdr.sock",
			},
			want: &Tmux{Bin: "tmux", Socket: "/tmp/tmux-1000/default", Pane: "%3"},
		},
		{
			name: "TMUX_PANE が無い tmux は使わない",
			env:  map[string]string{"TMUX": "/tmp/tmux-1000/default,1234,0"},
			want: None{},
		},
		{
			name: "herdr",
			env:  map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1", "HERDR_SOCKET_PATH": "/run/herdr.sock"},
			want: &Herdr{Socket: "/run/herdr.sock", Pane: "w1:p1"},
		},
		{
			name: "HERDR_PANE_ID が無い herdr は使わない（ポップアップの中のプロセス）",
			env:  map[string]string{"HERDR_ENV": "1", "HERDR_SOCKET_PATH": "/run/herdr.sock"},
			want: None{},
		},
		{
			name: "どちらでもない",
			env:  map[string]string{},
			want: None{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(envOf(tt.env))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Detect = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestNoneIsUnsupported(t *testing.T) {
	ctx := context.Background()
	n := Detect(envOf(nil))
	if n.Kind() != KindNone {
		t.Fatalf("Kind = %v, want none", n.Kind())
	}
	cmd := Command{Args: []string{"true"}}
	_, err1 := n.CurrentPane(ctx)
	_, err2 := n.Unit(ctx)
	_, err3 := n.SplitPane(ctx, SplitOptions{Command: cmd})
	errs := []error{
		err1, err2, err3,
		n.OpenPopup(ctx, PopupOptions{Command: cmd}),
		n.ClosePopup(ctx, ""),
		n.FocusPane(ctx, "%0", "%1"),
		n.ClosePane(ctx, "%1"),
	}
	for i, err := range errs {
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("操作 %d: err = %v, want ErrUnsupported", i, err)
		}
	}
}

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{KindNone: "none", KindTmux: "tmux", KindHerdr: "herdr", Kind(9): "unknown"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

// shellArgs は、sh に cmdline を実行させ、各引数を 1 行ずつ出力させた結果を返す。
func shellArgs(t *testing.T, cmdline string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//nolint:gosec // テストで shellJoin の結果を sh に読ませる
	out, err := exec.CommandContext(ctx, "sh", "-c", `args() { for a in "$@"; do printf '%s\n' "$a"; done; }; args `+cmdline).Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

func TestShellJoinRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh が無い")
	}
	args := []string{
		"plain", "with space", "it's", `"double"`, "$HOME", "`date`", "a=b", "*", "~", "back\\slash",
		"semi;colon", "-dash", "日本語", "tab\there", "%1", "",
	}
	if got := shellArgs(t, shellJoin(args)); !reflect.DeepEqual(got, args) {
		t.Errorf("sh が読んだ引数 = %q, want %q", got, args)
	}
}

func TestShellQuoteLeavesSafeWords(t *testing.T) {
	for _, s := range []string{"vi-imitation", "/usr/bin/x", "--popup", "%1", "w1:p2", "a,b+c@d"} {
		if got := shellQuote(s); got != s {
			t.Errorf("shellQuote(%q) = %q, want unchanged", s, got)
		}
	}
	if got := shellQuote("A=b"); got != "'A=b'" {
		t.Errorf("shellQuote(A=b) = %q, want quoted (代入と読まれないように)", got)
	}
}

func TestCheckOptions(t *testing.T) {
	if err := checkCommand("%1", Command{}); err == nil {
		t.Error("空のコマンドを受け付けた")
	}
	if err := checkCommand("", Command{Args: []string{"x"}}); !errors.Is(err, ErrNoTarget) {
		t.Errorf("空の元のペイン: err = %v, want ErrNoTarget", err)
	}
	for _, v := range []int{-1, 101} {
		if err := checkPercent("size", v); err == nil {
			t.Errorf("百分率 %d を受け付けた", v)
		}
	}
	for _, v := range []int{0, 1, 100} {
		if err := checkPercent("size", v); err != nil {
			t.Errorf("百分率 %d を断った: %v", v, err)
		}
	}
}
