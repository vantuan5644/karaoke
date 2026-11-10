//go:build windows

package media

import (
	"os/exec"
	"strconv"
)

// stopTree makes stopping the command end its child processes too.
func stopTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		pid := strconv.Itoa(cmd.Process.Pid)
		if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
