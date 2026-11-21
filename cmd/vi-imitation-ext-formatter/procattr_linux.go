//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startFormatter は、整形ツールの子プロセスを起動する。
//
// 拡張機能が終わったら SIGKILL を受け取るようにする（Pdeathsig）。本体が拡張機能のプロセスだけを強制終了したときにも、
// 終わらない整形ツールが孤児で残らないためである（基本設計 2.2 の規約。LSP の拡張機能の言語サーバーと同じ）。
// 別のプロセスグループにはしない。本体は拡張機能を終わらせるとき、拡張機能のプロセスグループへ SIGKILL を送る
// （internal/exthost の process.kill）ので、同じグループにいれば、整形ツールが起こした孫プロセスにも届く。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（onSpawnThread）の上で行う。
func startFormatter(cmd *exec.Cmd) error {
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
