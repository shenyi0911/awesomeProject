//go:build !windows && !unix

package runtime

import "os/exec"

// setProcessGroup is a no-op on unsupported platforms.
func setProcessGroup(cmd *exec.Cmd) {}

// killProcessTree falls back to killing only the direct child.
func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
