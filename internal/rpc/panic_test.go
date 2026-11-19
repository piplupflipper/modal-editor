package rpc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// envPanicChild は、TestPanicHook が自分自身を子プロセスとして起動し、ハンドラーを panic させるための環境変数である。
// panic は他の goroutine の recover で捕まえられずにプロセスを終わらせるので、子プロセスで起こす。
const envPanicChild = "VI_IMITATION_RPC_PANIC_CHILD"

// TestPanicHook は、ハンドラー（読み取りの goroutine）の panic で、WithPanicHook の関数を呼んでから panic を続けることを確かめる（#114）。
func TestPanicHook(t *testing.T) {
	if os.Getenv(envPanicChild) == "1" {
		panicChild(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPanicHook$") //nolint:gosec // テストの実行ファイル自身を起動する
	cmd.Env = append(os.Environ(), envPanicChild+"=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		t.Fatalf("子プロセスの終了 = %v, want panic の終了コード 2。出力:\n%s", err, out)
	}
	s := string(out)
	hook, pnc := strings.Index(s, "hook called"), strings.Index(s, "panic: handler panic")
	if hook < 0 || pnc < 0 || hook > pnc {
		t.Errorf("フックの出力が panic の出力より前に無い:\n%s", out)
	}
}

func panicChild(t *testing.T) {
	h := HandlerFuncs{Notification: func(context.Context, *Notification) { panic("handler panic") }}
	hook := WithPanicHook(func() { fmt.Fprintln(os.Stderr, "hook called") })
	a, _ := newPair(t, nil, h, hook)
	if err := a.Notify("boom", nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	t.Fatal("panic で終わらなかった")
}
