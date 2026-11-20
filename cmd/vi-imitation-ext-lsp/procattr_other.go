//go:build !linux

package main

import (
	"os"
	"os/exec"
)

// startProcess は、言語サーバーの子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、属性を付けずに起動する（procattr_linux.go の doc）。
func startProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}

// startMise は、mise where と mise install の子プロセスを起動する。属性は付けない（startProcess と同じ）。
func startMise(cmd *exec.Cmd) error {
	return cmd.Start()
}

// interruptMise は、mise に SIGINT を送る。
func interruptMise(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(os.Interrupt)
}

// killMise は、mise を強制終了する。
func killMise(cmd *exec.Cmd) {
	_ = cmd.Process.Kill()
}
