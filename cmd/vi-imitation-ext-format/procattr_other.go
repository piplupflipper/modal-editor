//go:build !linux

package main

import "os/exec"

// startFormatter は、整形ツールの子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、属性を付けずに起動する（procattr_linux.go の doc）。
func startFormatter(cmd *exec.Cmd) error {
	return cmd.Start()
}
