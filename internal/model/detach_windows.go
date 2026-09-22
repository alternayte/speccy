//go:build windows

package model

import "os/exec"

// detach does nothing on Windows: a process there has no controlling terminal to leave, and
// the CLI already reads a pipe for its standard streams.
func detach(_ *exec.Cmd) {}
