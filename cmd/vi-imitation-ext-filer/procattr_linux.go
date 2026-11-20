//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startChild は、git の子プロセスを起動する。
//
// 子プロセスは拡張機能と同じプロセスグループのままにし、拡張機能が終わったら SIGKILL を受け取るようにする（Pdeathsig）。
// 本体が拡張機能を強制終了するとき（プロセスグループへの SIGKILL。internal/exthost の process.kill）は、同じプロセスグループなので一緒に終わる。
// 拡張機能のプロセスだけが SIGKILL で終わったとき（OOM の強制終了など）は、Pdeathsig で終わる。
// git check-ignore は読むだけなので、後始末を待たずに SIGKILL で終わらせてよい（DAP のデバッグアダプターと違い、SIGTERM にしない）。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（spawnThread）の上で行う（cmd/vi-imitation-ext-dap と同じ）。
func startChild(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return onSpawnThread(cmd.Start)
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
