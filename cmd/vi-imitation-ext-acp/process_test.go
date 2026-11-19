//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// gone は、pid のプロセスが無いか、終わってゾンビになっていることを返す。
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// 3 つ目の欄が状態である。2 つ目の欄（実行ファイルの名前）は括弧で囲まれ、空白を含みうるので、最後の ')' の後から読む。
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

// waitGone は、pid のプロセスが終わるまで待つ。
func waitGone(t *testing.T, what string, pid int) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if gone(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("%s (pid %d) remains", what, pid)
}

var pidPattern = regexp.MustCompile(`pid=(\d+)`)

// readPID は、fakeagent の pid の操作の出力からプロセス ID を読む。
func readPID(t *testing.T, out string) int {
	t.Helper()
	m := pidPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("pid not found in %q", out)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// TestStopEndsAgent は、本体の shutdown で、エージェントを終わらせ、異常終了として知らせないことを確かめる。
//
// req: FR-34
func TestStopEndsAgent(t *testing.T) {
	h := newHost(t, fakeSettings(), nil)
	h.prompt(map[string]any{"op": "pid"})
	pid := readPID(t, h.waitOutput("[end_turn]"))
	if err := h.conn.Notify(ext.MethodShutdown, ext.ShutdownParams{}); err != nil {
		t.Fatal(err)
	}
	waitGone(t, "agent", pid)
	select {
	case <-h.conn.Done():
	case <-time.After(testTimeout):
		t.Fatal("the extension did not close the connection after shutdown")
	}
	if msgs := h.messages(); len(msgs) != 0 {
		t.Errorf("messages = %v, want none for a normal stop", msgs)
	}
}

// TestExtensionKilled は、拡張機能のプロセスが SIGKILL で終わったとき（本体の強制終了。internal/exthost の process.kill）、
// 別のプロセスグループで起動したエージェントが Pdeathsig の SIGTERM で終わり、孤児で残らないことを確かめる。
//
// req: FR-34
func TestExtensionKilled(t *testing.T) {
	h := newHostState(t)
	cmd := exec.Command(extBinary)
	// 本体と同じく、拡張機能を別のプロセスグループで起動する（internal/exthost の startProcess）。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h.conn = rpc.NewConn(stdout, stdin, rpc.HandlerFuncs{Notification: h.notification, Request: h.request})
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = h.conn.Close()
		_ = cmd.Wait()
	})
	h.initialize(fakeSettings())
	h.prompt(map[string]any{"op": "pid"})
	pid := readPID(t, h.waitOutput("[end_turn]"))

	// 本体の強制終了と同じく、拡張機能のプロセスグループへ SIGKILL を送る。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitGone(t, "agent", pid)
}
