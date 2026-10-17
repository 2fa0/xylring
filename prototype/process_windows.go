package main

import (
	"os/exec"
	"time"
)

func configureWriteProcess(cmd *exec.Cmd) { cmd.WaitDelay = 2 * time.Second }
