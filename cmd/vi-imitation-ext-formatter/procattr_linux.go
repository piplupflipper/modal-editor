//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// startFormatter は、整形ツールの子プロセスを、整形ツールごとの新しいプロセスグループ（Setpgid）で起動する。
//
// 別のプロセスグループにするのは、時間の上限か本体の $/cancel で止めるときに、整形ツールが起こした子プロセス
// （node_modules/.bin/biome の node が起動する biome の本体など）までグループごと止めるためである（killFormatter。PR #341 のレビューの必須 1）。
// 起動したプロセスにだけ SIGKILL を送ると、子プロセスは親を失って走り続け、:w のたびに増えた。
//
// 拡張機能が終わったら、整形ツールのプロセスは SIGKILL を受け取る（Pdeathsig）。本体が拡張機能を強制終了したときにも、
// 終わらない整形ツールが孤児で残らないためである（基本設計 2.2 の規約）。ただし Pdeathsig は起動したプロセスにだけ届き、
// 別のグループなので本体が拡張機能のグループへ送る SIGKILL（internal/exthost の process.kill）も届かない。
// このため、整形の途中で拡張機能が強制終了されると、整形ツールのプロセスは Pdeathsig で止まるが、整形ツールが起こした子プロセスは
// 止まらず、自分で終わるまで残る。止まらない子プロセス（応答しない biome の本体など）は、終わらないまま残り続ける。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（onSpawnThread）の上で行う。
func startFormatter(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	return onSpawnThread(cmd.Start)
}

// killFormatter は、startFormatter で起動した整形ツールのプロセスグループ全体に SIGKILL を送る。
// グループへ送れなければ（グループに誰も残っていないなど）、起動したプロセスにだけ送る。
func killFormatter(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
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
