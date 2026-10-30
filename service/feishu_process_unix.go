//go:build !windows

package service

import (
	"os"
	"os/exec"
	"syscall"
)

func runFeishuCLI(process *exec.Cmd) error {
	if process.SysProcAttr == nil {
		process.SysProcAttr = &syscall.SysProcAttr{}
	}
	process.SysProcAttr.Setpgid = true
	process.Cancel = func() error {
		if process.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-process.Process.Pid, syscall.SIGKILL)
	}
	return process.Run()
}
