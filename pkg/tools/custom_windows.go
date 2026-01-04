//go:build windows

package tools

import (
	"os"
	"os/exec"
)

func setSysProcAttr(_ *exec.Cmd) {
	// No equivalent to Setpgid on Windows
}

func setCancelFunc(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Kill)
	}
}
