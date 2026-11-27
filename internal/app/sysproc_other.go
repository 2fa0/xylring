//go:build !windows

package app

import "os/exec"

func hideConsoleWindow(cmd *exec.Cmd) {
}
