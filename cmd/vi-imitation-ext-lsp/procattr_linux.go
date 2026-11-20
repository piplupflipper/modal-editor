//go:build linux

package main

import (
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// 子プロセスの起動の属性は、子プロセスが孫プロセスを自分で後始末するかで分ける（PR #281 のレビュー）。
//
//   - 言語サーバー（直接と mise exec）: 孫プロセスを後始末しない。拡張機能と同じプロセスグループに置き、Pdeathsig を SIGKILL にする（startProcess）
//   - mise where と mise install: mise は SIGINT を受け取ると、自分で起動した npm などを終わらせてから終わる。
//     別のプロセスグループにし、Pdeathsig を SIGINT にする（startMise）。DAP の拡張機能が dlv に SIGTERM を受け取らせるのと同じ考え方である
//
// Pdeathsig は、子プロセスを起動したスレッドが終わったときにも送られる（golang/go#27505）。
// このため、起動は、ロックしたまま終わらない 1 つのスレッド（onSpawnThread）の上で行う。

// startProcess は、言語サーバーの子プロセスを起動する。
//
// 拡張機能が終わったら SIGKILL を受け取るようにする（Pdeathsig）。
// 本体が拡張機能のプロセスだけを強制終了したときにも、言語サーバーが孤児で残らないためである。
// mise exec は言語サーバーの実行ファイルに置き換わる（exec する）ので、mise 経由で起動した言語サーバーにも Pdeathsig が残る
// （2026-11-20 に mise 2026.5.2 で、mise exec の下のコマンドの親が mise を呼んだプロセスであることを確かめた）。
//
// 別のプロセスグループにはしない（Setpgid を使わない）。
// 本体は拡張機能を終わらせるとき、拡張機能のプロセスグループへ SIGKILL を送る（internal/exthost の process.kill）。
// 同じグループにいれば、言語サーバーが起こした孫プロセス（vtsls の tsserver など）にも届く。
// 別のグループにすると、Pdeathsig の届く言語サーバーだけが終わり、孫プロセスが孤児で残る。
func startProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return onSpawnThread(cmd.Start)
}

// startMise は、mise where と mise install の子プロセスを起動する。
//
// mise 2026.5.2 の mise install npm:… は、npm を自分の新しいプロセスグループで起動する（PR #281 のレビューで確かめた）。
// このため、本体の拡張機能のグループへの SIGKILL も、mise への SIGKILL も、npm には届かない。
// mise は SIGINT を受け取ると npm を終わらせてから終わる（同じレビュー）ので、拡張機能が終わったら SIGINT を受け取るようにし（Pdeathsig）、
// 本体のグループへの SIGKILL で先に終わらないように、別のプロセスグループにする（Setpgid）。
// 拡張機能が止めるときも、interruptMise で SIGINT を送る。
func startMise(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGINT}
	return onSpawnThread(cmd.Start)
}

// interruptMise は、startMise で起動した mise のプロセスグループへ SIGINT を送る。
func interruptMise(cmd *exec.Cmd) {
	signalGroup(cmd, syscall.SIGINT)
}

// killMise は、startMise で起動した mise のプロセスグループへ SIGKILL を送る。SIGINT で終わらなかったときに使う。
func killMise(cmd *exec.Cmd) {
	signalGroup(cmd, syscall.SIGKILL)
}

// signalGroup は、cmd のプロセスグループへ sig を送る。グループへ送れなければ cmd のプロセスだけに送る。
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
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
