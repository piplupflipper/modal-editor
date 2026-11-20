//go:build !linux

package main

import "os/exec"

// startChild は、git の子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、拡張機能と同じプロセスグループのまま起動するだけである（procattr_linux.go の doc）。
func startChild(cmd *exec.Cmd) error {
	return cmd.Start()
}
