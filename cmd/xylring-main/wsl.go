package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// isWSL reports whether the process is running inside Windows Subsystem for Linux.
func isWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	return err == nil
}

func wslDistroName() string {
	return strings.TrimSpace(os.Getenv("WSL_DISTRO_NAME"))
}

// wslPathToWindows converts a Linux path inside WSL to its Windows path via wslpath.
func wslPathToWindows(path string) (string, error) {
	out, err := exec.Command("wslpath", "-w", path).Output()
	if err != nil {
		return "", err
	}
	winPath := strings.TrimRight(string(out), "\r\n")
	if winPath == "" {
		return "", errors.New("wslpath 返回空路径")
	}
	return winPath, nil
}

func resolveDefaultOpenCommand(
	goos, targetPath string,
	inWSL bool,
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{targetPath}
	case "windows":
		return "cmd", []string{"/c", "start", "", targetPath}
	default:
		if inWSL {
			if bin, err := lookPathFn("wslview"); err == nil {
				return bin, []string{targetPath}
			}
			if bin, err := lookPathFn("xdg-open"); err == nil {
				return bin, []string{targetPath}
			}
			if bin, err := lookPathFn("explorer.exe"); err == nil && wslPathFn != nil {
				if winPath, err := wslPathFn(targetPath); err == nil {
					return bin, []string{winPath}
				}
			}
		}
		return "xdg-open", []string{targetPath}
	}
}

func resolveWSLOpenFolder(
	dir string,
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (string, []string) {
	if bin, err := lookPathFn("explorer.exe"); err == nil && wslPathFn != nil {
		if winDir, err := wslPathFn(dir); err == nil {
			return bin, []string{winDir}
		}
	}
	if bin, err := lookPathFn("wslview"); err == nil {
		return bin, []string{dir}
	}
	return "xdg-open", []string{dir}
}

func resolveRevealCommand(
	goos, path string,
	isDir, inWSL bool,
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{"-R", path}
	case "windows":
		return "explorer", []string{"/select,", path}
	default:
		dir := path
		if !isDir {
			dir = filepath.Dir(path)
		}
		if inWSL {
			if bin, err := lookPathFn("explorer.exe"); err == nil && wslPathFn != nil {
				if winPath, err := wslPathFn(path); err == nil {
					return bin, []string{"/select,", winPath}
				}
			}
			return resolveWSLOpenFolder(dir, lookPathFn, wslPathFn)
		}
		return "xdg-open", []string{dir}
	}
}

func resolveOpenFolderCommand(
	goos, dir string,
	inWSL bool,
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{dir}
	case "windows":
		return "explorer", []string{dir}
	default:
		if inWSL {
			return resolveWSLOpenFolder(dir, lookPathFn, wslPathFn)
		}
		return "xdg-open", []string{dir}
	}
}

func resolveTerminalCommand(
	goos, abs, termEnv string,
	inWSL bool,
	distro string,
	lookPathFn func(string) (string, error),
	wslPathFn func(string) (string, error),
) (bin string, args []string, cmdDir string, err error) {
	switch goos {
	case "darwin":
		return "open", []string{"-a", "Terminal", abs}, "", nil
	case "windows":
		return "cmd", []string{"/c", "start", "cmd.exe"}, abs, nil
	default:
		if term := strings.TrimSpace(termEnv); term != "" {
			return term, nil, abs, nil
		}
		for _, name := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "alacritty", "xterm"} {
			if p, err := lookPathFn(name); err == nil {
				return p, nil, abs, nil
			}
		}
		if inWSL {
			wslArgs := []string{"wsl.exe"}
			if distro != "" {
				wslArgs = append(wslArgs, "-d", distro)
			}
			wslArgs = append(wslArgs, "--cd", abs)
			if p, err := lookPathFn("wt.exe"); err == nil {
				if distro != "" || wslPathFn == nil {
					return p, wslArgs, "", nil
				}
				if winDir, err := wslPathFn(abs); err == nil {
					return p, []string{"-d", winDir}, "", nil
				}
				return p, wslArgs, "", nil
			}
			if p, err := lookPathFn("cmd.exe"); err == nil {
				return p, append([]string{"/c", "start"}, wslArgs...), "", nil
			}
		}
		return "", nil, "", errors.New("未找到可用的终端应用")
	}
}
