//go:build windows

package app

import (
	"fmt"
	"syscall"
	"unsafe"
)

var procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

const swShowNormal = 1

// openWithWindowsDefaultApp opens targetPath using Windows file association
// directly via ShellExecuteW without invoking cmd.exe.
func openWithWindowsDefaultApp(targetPath string) error {
	verb, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	r, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		0,
		0,
		swShowNormal,
	)
	if r <= 32 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("打开默认程序失败 (code %d): %w", r, callErr)
		}
		return fmt.Errorf("打开默认程序失败 (code %d)", r)
	}
	return nil
}
