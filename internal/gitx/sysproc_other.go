//go:build !windows

package gitx

import "os/exec"

func HideConsoleWindow(cmd *exec.Cmd) {
}
