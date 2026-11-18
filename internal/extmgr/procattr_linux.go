//go:build linux

package extmgr

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// start は、git と go の子プロセスを起動する（基本設計 5.6）。
//
// 本体の他の子プロセスと同じく、子プロセスは別のプロセスグループにし（Setpgid）、
// 本体が終わったら SIGKILL を受け取るようにする（Pdeathsig）。
// go build は compile や link を孫のプロセスとして起動するので、取り消し（ctx の終わり）では
// プロセスグループ全体へ SIGKILL を送る（cmd.Cancel）。
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（onSpawnThread）の上で行う。
func start(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
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
			// このスレッドは、本体が終わるまで終わらせない（UnlockOSThread しない）。
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
