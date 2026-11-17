//go:build windows

package headless

import (
	"os/exec"
	"strconv"
)

func setProcessGroup(*exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd) { killGroup(cmd) }

func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
