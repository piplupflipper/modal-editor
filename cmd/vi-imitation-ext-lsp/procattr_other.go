//go:build !linux

package main

import "os/exec"

// startProcess は、子プロセス（言語サーバーと mise）を起動する。
// Linux 以外では Pdeathsig が無いので、属性を付けずに起動する（procattr_linux.go の doc）。
func startProcess(cmd *exec.Cmd) error {
	return cmd.Start()
}
