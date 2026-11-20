//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startProcess は、子プロセス（言語サーバーと mise）を起動する。
//
// 子プロセスには、拡張機能が終わったら SIGKILL を受け取るようにする（Pdeathsig）。
// 本体が拡張機能のプロセスだけを強制終了したときにも、言語サーバーが孤児で残らないためである。
// mise exec は言語サーバーの実行ファイルに置き換わる（exec する）ので、mise 経由で起動した言語サーバーにも Pdeathsig が残る
// （2026-11-20 に mise 2026.5.2 で、mise exec の下のコマンドの親が mise を呼んだプロセスであることを確かめた）。
//
// DAP の拡張機能（cmd/vi-imitation-ext-dap の startAdapter）と違い、子プロセスを別のプロセスグループにしない（Setpgid を使わない）。
// 本体は拡張機能を終わらせるとき、拡張機能のプロセスグループへ SIGKILL を送る（internal/exthost の process.kill）。
// 同じグループにいれば、言語サーバーが起こした孫プロセス（vtsls の tsserver、mise install の下の npm など）にも届く。
// 別のグループにすると、Pdeathsig の届く子プロセスだけが終わり、孫プロセスが孤児で残る。
// DAP の拡張機能が別のグループにしたのは、dlv に SIGKILL でなく SIGTERM を受け取らせて、デバッグ対象を後始末させるためで、
// 言語サーバーにはその必要が無い。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（onSpawnThread）の上で行う。
func startProcess(cmd *exec.Cmd) error {
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
