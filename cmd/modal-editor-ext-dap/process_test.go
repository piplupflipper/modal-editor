//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/rpc"
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

// waitPID は、"<prefix> <pid>\n" の出力から pid を読む。
func (h *host) waitPID(prefix string) int {
	h.t.Helper()
	seen := h.waitOutput(prefix + " ")
	var pid int
	for _, s := range seen {
		if i := strings.Index(s, prefix+" "); i >= 0 {
			if _, err := fmt.Sscanf(s[i:], prefix+" %d", &pid); err == nil {
				return pid
			}
		}
	}
	h.t.Fatalf("pid of %s not found in %q", prefix, seen)
	return 0
}

// TestKillTerminatesDebuggee は、session.kill がデバッグアダプターに先に SIGTERM を送り、
// デバッグアダプターが別のプロセスグループで起動したデバッグ対象を終わらせられることを確かめる（PR #58 のレビューの必須 1）。
//
// req: FR-31
func TestKillTerminatesDebuggee(t *testing.T) {
	h := newHost(t, fakeSettings(t, nil, map[string]any{"child": true}))
	h.open(1, srcPath, srcText)
	h.mustExec(cmdToggleBreakpoint, 2)
	h.mustExec(cmdStart, 0)
	child := h.waitPID("child")
	h.waitSigns(bp2, exec2)
	h.d.mu.Lock()
	s := h.d.sess
	h.d.mu.Unlock()
	s.kill()
	waitGone(t, "debuggee", child)
	if ws, ok := s.cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || ws.Signaled() {
		t.Errorf("adapter exit = %v, want exit by itself on SIGTERM", s.cmd.ProcessState)
	}
}

// TestExtensionKilled は、拡張機能のプロセスが SIGKILL で終わったとき（本体の強制終了。internal/exthost の process.kill）、
// デバッグアダプターが SIGTERM を受け取り、デバッグ対象と一緒に終わることを確かめる（PR #58 のレビューの必須 1）。
//
// req: FR-31
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
	h.initialize(fakeSettings(t, nil, map[string]any{"child": true, "pid": true}))
	h.open(1, srcPath, srcText)
	h.mustExec(cmdToggleBreakpoint, 2)
	h.mustExec(cmdStart, 0)
	adapter := h.waitPID("adapter")
	child := h.waitPID("child")
	h.waitSigns(bp2, exec2)

	// 本体の強制終了と同じく、拡張機能のプロセスグループへ SIGKILL を送る。
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitGone(t, "adapter", adapter)
	waitGone(t, "debuggee", child)
}
