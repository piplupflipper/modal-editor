//go:build !linux

package main

import (
	"os/exec"
	"syscall"
)

// startAgent は、エージェントの子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、拡張機能と同じプロセスグループのまま起動する（procattr_linux.go の doc）。
func startAgent(cmd *exec.Cmd) error {
	return cmd.Start()
}

// signalGroup は、エージェントのプロセスへ sig を送る。SIGKILL 以外は送れないことがある。
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if sig == syscall.SIGKILL {
		_ = cmd.Process.Kill()
		return
	}
	_ = cmd.Process.Signal(sig)
}
