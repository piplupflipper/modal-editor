//go:build !linux

package extmgr

import "os/exec"

// start は、git と go の子プロセスを起動する。
// Linux 以外では Pdeathsig が無いので、本体と同じプロセスグループのまま起動する（procattr_linux.go の doc）。
func start(cmd *exec.Cmd) error {
	return cmd.Start()
}

// startTerm は、start と同じく起動する。
func startTerm(cmd *exec.Cmd) error {
	return cmd.Start()
}
