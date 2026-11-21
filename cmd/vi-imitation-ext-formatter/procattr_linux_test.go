//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// gone は、pid のプロセスが無いか、終わってゾンビになっていることを返す（cmd/vi-imitation-ext-filer の試験と同じ）。
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

// readPIDs は、偽の整形ツールが 1 行に 1 つずつ書いたプロセスの番号を読む。
func readPIDs(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

// waitGone は、pids のプロセスがすべて終わるまで待ち、残ったものがあれば失敗にする。残ったプロセスは止めて片付ける。
func waitGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for !gone(pid) {
			if time.Now().After(deadline) {
				t.Errorf("整形ツールの子プロセス %d が残った", pid)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// 時間の上限か本体の取り消し（$/cancel）で整形ツールを止めると、整形ツールが起こした子プロセスもグループごと止まり、
// 何度走らせても残らない。整形ツールが終わっても標準出力を持ったまま子プロセスが残ったときも、待つのをやめた後に止める
// （PR #341 のレビューの必須 1）。
//
// req: FR-175
func TestRunFormatterKillsChildren(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", filepath.Join(root, "bin")+":/usr/bin:/bin")
	t.Setenv("VIRTUAL_ENV", "")
	pidFile := filepath.Join(root, "pids")
	// node_modules/.bin/biome（node が biome の本体を子プロセスで起こす）と同じく、子プロセスが働く整形ツール。
	writeScript(t, root, "bin/spawner", `sleep 300 & echo $! >> "`+pidFile+`"; wait`)
	// 自分は終わるが、標準出力を持ったまま子プロセスを残す整形ツール。
	writeScript(t, root, "bin/leaver", `sleep 300 & echo $! >> "`+pidFile+`"; echo out`)
	cfg, err := parseSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.timeout = 200 * time.Millisecond
	path := filepath.Join(root, "a.txt")

	for range 3 {
		_, err := runFormatter(context.Background(), cfg, formatterConfig{Name: "spawner", Command: "spawner"}, path, "x\n")
		var fe *formatError
		if !errors.As(err, &fe) || !strings.Contains(fe.Error(), "で終わらなかった") {
			t.Errorf("spawner: %v, want 時間の上限の誤り", err)
		}
	}
	cfg.timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := runFormatter(ctx, cfg, formatterConfig{Name: "spawner", Command: "spawner"}, path, "x\n"); !errors.Is(err, context.Canceled) {
		t.Errorf("取り消した spawner: %v, want context.Canceled", err)
	}
	if _, err := runFormatter(context.Background(), cfg, formatterConfig{Name: "leaver", Command: "leaver"}, path, "x\n"); err == nil {
		t.Error("leaver: 誤りが無い, want 子プロセスが標準出力を持ったまま残った誤り")
	}
	pids := readPIDs(t, pidFile)
	if len(pids) != 5 {
		t.Fatalf("子プロセスの数 = %d, want 5", len(pids))
	}
	waitGone(t, pids)
}
