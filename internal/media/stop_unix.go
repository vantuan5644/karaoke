//go:build unix

package media

import (
	"os/exec"
	"syscall"
)

// stopTree runs the command in its own process group, so stopping it kills the group.
func stopTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
