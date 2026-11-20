//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone は、pid のプロセスが無いか、終わってゾンビになっていることを返す（cmd/vi-imitation-ext-dap の試験と同じ）。
func gone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] == "Z"
}

// 拡張機能のプロセスだけが SIGKILL で終わっても（プロセスグループへの SIGKILL でなくても）、待っていた git の子プロセスは残らない（Pdeathsig）。
// 本体の強制終了（プロセスグループへの SIGKILL）では、git は同じプロセスグループなので一緒に終わる。
//
// req: FR-105
func TestGitKilledWithExtension(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("group=%v", group), func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, dir, map[string]string{"a.txt": ""})
			pidFile := filepath.Join(t.TempDir(), "git.pid")
			fakeGit(t, `echo $$ > "$FILER_TEST_PIDFILE"`+"\nexec sleep 60")

			cmd := exec.Command(os.Args[0]) //nolint:gosec // テストの実行ファイルを拡張機能として動かす
			cmd.Env = append(os.Environ(), envTestMain+"=1", "FILER_TEST_PIDFILE="+pidFile)
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
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			h := newHostState(t, dir, "")
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				if h.conn != nil {
					_ = h.conn.Close()
				}
				_ = cmd.Wait()
			})
			h.connect(stdout, stdin)
			// コマンドはすぐに応答し、木の根を読む間に git を待つ。
			h.command("filer.popup")
			var pid int
			deadline := time.Now().Add(testTimeout)
			for pid == 0 && time.Now().Before(deadline) {
				if b, rerr := os.ReadFile(pidFile); rerr == nil {
					pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("git did not start")
			}
			target := cmd.Process.Pid
			if group {
				target = -target
			}
			if err = syscall.Kill(target, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			for !gone(pid) {
				if time.Now().After(deadline) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Fatalf("git (pid %d) remains after the extension was killed", pid)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}
