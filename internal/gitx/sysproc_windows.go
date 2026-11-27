//go:build windows

package gitx

import (
	"os/exec"
	"syscall"
)

// HideConsoleWindow prevents console windows from popping up when spawning CUI tools
// like git.exe or cmd.exe from a GUI process on Windows.
func HideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= 0x08000000 // CREATE_NO_WINDOW
}
