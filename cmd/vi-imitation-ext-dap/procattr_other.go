//go:build !linux

package main

import "os/exec"

// startAdapter は、デバッグアダプターの子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、拡張機能と同じプロセスグループのまま起動する（procattr_linux.go の doc）。
func startAdapter(cmd *exec.Cmd) error {
	return cmd.Start()
}
