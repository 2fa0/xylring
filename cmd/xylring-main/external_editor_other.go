//go:build !windows

package main

import "errors"

func openWithWindowsDefaultApp(string) error {
	return errors.New("windows ShellExecuteW is unavailable on this platform")
}
