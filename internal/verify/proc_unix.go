//go:build !windows

package verify

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func newCommand(command string) *exec.Cmd {
	cmd := exec.Command("sh", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// waitGone returns once no process remains in the command's process group.
func waitGone(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	deadline := time.Now().Add(reapWindow)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-cmd.Process.Pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}
