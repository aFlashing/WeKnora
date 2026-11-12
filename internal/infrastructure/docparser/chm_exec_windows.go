//go:build windows

package docparser

import (
	"os/exec"
	"syscall"
)

// hideCHMConvertWindow keeps the desktop build (compiled with -H windowsgui)
// from flashing a console window every time ebook-convert starts.
func hideCHMConvertWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
