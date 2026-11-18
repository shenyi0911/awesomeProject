//go:build windows

package runtime

import (
	"os/exec"
	"strconv"
)

// setProcessGroup is a no-op on Windows; killProcessTree uses taskkill /T instead.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessTree terminates the whole process tree on Windows.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
