//go:build windows

package relay

import (
	"os/exec"
	"strconv"
)

func configureProcess(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/PID", stringPID(cmd.Process.Pid), "/T", "/F").Run()
}

func stringPID(pid int) string {
	if pid < 0 {
		return "0"
	}
	// Avoid importing strconv in the platform-specific process helper's hot path.
	return strconv.Itoa(pid)
}
