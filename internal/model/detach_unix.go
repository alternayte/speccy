//go:build !windows

package model

import (
	"os/exec"
	"syscall"
)

// detach gives the CLI its own session, so it has no controlling terminal. A CLI that asks a
// question on /dev/tty then fails at once, with a cause Speccy can report, instead of waiting
// for an answer that no person can give: a review run has no terminal at either end.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
