//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startAgent は、エージェントの子プロセスを起動する。
//
// 子プロセスは別のプロセスグループにし（Setpgid）、拡張機能が終わったら SIGTERM を受け取るようにする（Pdeathsig）。
// 本体は、拡張機能を終わらせるときに、拡張機能のプロセスグループへすぐに SIGKILL を送る（internal/exthost の process.kill）。
// エージェントは、ツールの実行や MCP サーバーとして子プロセスを起動するので、拡張機能と同じプロセスグループのまま
// SIGKILL で死ぬと、エージェントが後始末をできずに孤児が残りうる（DAP の拡張機能と同じ理由。cmd/modal-editor-ext-dap の startAdapter）。
// Setpgid だけにすると本体の SIGKILL が届かなくなるので、Pdeathsig と必ず組にする。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（spawnThread）の上で行う。
func startAgent(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	return onSpawnThread(cmd.Start)
}

// signalGroup は、エージェントのプロセスグループ（startAgent で作ったもの）へ sig を送る。
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

var (
	spawnOnce sync.Once
	spawnReqs chan func()
)

// onSpawnThread は、f を、起動のためのスレッドの上で呼び、戻り値を返す。
func onSpawnThread(f func() error) error {
	spawnOnce.Do(func() {
		spawnReqs = make(chan func())
		go func() {
			// このスレッドは、拡張機能が終わるまで終わらせない（UnlockOSThread しない）。
			runtime.LockOSThread()
			for req := range spawnReqs {
				req()
			}
		}()
	})
	done := make(chan error, 1)
	spawnReqs <- func() { done <- f() }
	return <-done
}
