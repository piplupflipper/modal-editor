//go:build linux

package editor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/exthost"
)

// formatterChildGone は、pid のプロセスが無いか、終わってゾンビになっていることを返す。
func formatterChildGone(pid int) bool {
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

// 保存の前の整形が期限（WillSaveTimeout）を超えて止められても、整形ツールが起こした子プロセスは残らない。
// :w を繰り返しても増えない（PR #341 のレビューの必須 1）。
//
// req: FR-175, FR-177
func TestStandardFormatterKillsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pids")
	spawner := formatterScript(t, dir, "spawner", `sleep 300 & echo $! >> "`+pidFile+`"; wait`)
	spec := formatterSpec(t, []map[string]any{{"command": spawner, "fileTypes": []string{"python"}}})
	py := filepath.Join(dir, "a.py")
	if err := os.WriteFile(py, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}, Host: exthost.Config{WillSaveTimeout: 100 * time.Millisecond}}, py)
	h.eventually("整形ツールの拡張機能の起動", func() bool { return h.state("formatter") == exthost.StateRunning })
	pids := func() []int {
		data, _ := os.ReadFile(pidFile)
		var out []int
		for _, f := range strings.Fields(string(data)) {
			if pid, err := strconv.Atoi(f); err == nil {
				out = append(out, pid)
			}
		}
		return out
	}
	for i := range 3 {
		h.keys(":w<CR>")
		h.eventually(fmt.Sprintf("%d 回目の整形ツール", i+1), func() bool { return len(pids()) == i+1 })
		h.settle()
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids() {
		for !formatterChildGone(pid) {
			if time.Now().After(deadline) {
				t.Errorf("整形ツールの子プロセス %d が残った", pid)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}
