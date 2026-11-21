//go:build !linux

package main

import "os/exec"

// startFormatter は、整形ツールの子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、属性を付けずに起動する（procattr_linux.go の doc）。
func startFormatter(cmd *exec.Cmd) error {
	return cmd.Start()
}

// killFormatter は、整形ツールのプロセスを強制終了する。Linux 以外では、整形ツールの子プロセスまでは止めない。
func killFormatter(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
