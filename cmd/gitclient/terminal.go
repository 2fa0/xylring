package main

import (
	"os/exec"
	"runtime"
)

func (a *app) openTerminal(path string) {
	go func() {
		var cmd *exec.Cmd
		if runtime.GOOS == "darwin" {
			cmd = exec.Command("open", "-a", "Terminal", path)
		} else if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", "start", "cmd", "/K", "cd", "/d", path)
		} else {
			cmd = exec.Command("x-terminal-emulator")
			cmd.Dir = path
		}
		if err := cmd.Run(); err != nil {
			a.post(func() { a.banner = "打开终端失败：" + err.Error() })
		}
	}()
}
