package main

import (
	"errors"
	"os"
	"slices"
	"testing"
)

func fakeLookPath(available map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := available[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
}

func fakeWSLPath(p string) (string, error) {
	return `\\wsl.localhost\Ubuntu` + p, nil
}

func TestResolveDefaultOpenCommandWSL(t *testing.T) {
	noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	bin, args, err := resolveEditorCommandWithEnv("linux", "/home/u", "", "/home/u/repo", true, noStat,
		fakeLookPath(map[string]string{"wslview": "/usr/bin/wslview", "xdg-open": "/usr/bin/xdg-open"}), fakeWSLPath)
	if err != nil || bin != "/usr/bin/wslview" || !slices.Equal(args, []string{"/home/u/repo"}) {
		t.Fatalf("wslview preferred: got (%q, %v, %v)", bin, args, err)
	}

	bin, args, err = resolveEditorCommandWithEnv("linux", "/home/u", "", "/home/u/repo", true, noStat,
		fakeLookPath(map[string]string{"xdg-open": "/usr/bin/xdg-open", "explorer.exe": "/mnt/c/Windows/explorer.exe"}), fakeWSLPath)
	if err != nil || bin != "/usr/bin/xdg-open" || !slices.Equal(args, []string{"/home/u/repo"}) {
		t.Fatalf("xdg-open fallback: got (%q, %v, %v)", bin, args, err)
	}

	bin, args, err = resolveEditorCommandWithEnv("linux", "/home/u", "", "/home/u/repo", true, noStat,
		fakeLookPath(map[string]string{"explorer.exe": "/mnt/c/Windows/explorer.exe"}), fakeWSLPath)
	if err != nil || bin != "/mnt/c/Windows/explorer.exe" || !slices.Equal(args, []string{`\\wsl.localhost\Ubuntu/home/u/repo`}) {
		t.Fatalf("explorer.exe fallback: got (%q, %v, %v)", bin, args, err)
	}
}

func TestResolveFileManagerCommandsWSL(t *testing.T) {
	bin, args := resolveRevealCommand("linux", "/home/u/repo/main.go", false, true,
		fakeLookPath(map[string]string{"explorer.exe": "/mnt/c/Windows/explorer.exe"}), fakeWSLPath)
	if bin != "/mnt/c/Windows/explorer.exe" || !slices.Equal(args, []string{"/select,", `\\wsl.localhost\Ubuntu/home/u/repo/main.go`}) {
		t.Fatalf("reveal explorer.exe: got (%q, %v)", bin, args)
	}

	bin, args = resolveRevealCommand("linux", "/home/u/repo/main.go", false, true,
		fakeLookPath(map[string]string{"wslview": "/usr/bin/wslview"}), fakeWSLPath)
	if bin != "/usr/bin/wslview" || !slices.Equal(args, []string{"/home/u/repo"}) {
		t.Fatalf("reveal wslview parent dir: got (%q, %v)", bin, args)
	}

	bin, args = resolveOpenFolderCommand("linux", "/home/u/repo", true,
		fakeLookPath(map[string]string{"explorer.exe": "/mnt/c/Windows/explorer.exe"}), fakeWSLPath)
	if bin != "/mnt/c/Windows/explorer.exe" || !slices.Equal(args, []string{`\\wsl.localhost\Ubuntu/home/u/repo`}) {
		t.Fatalf("open folder explorer.exe: got (%q, %v)", bin, args)
	}

	bin, args = resolveOpenFolderCommand("linux", "/home/u/repo", false,
		fakeLookPath(map[string]string{"explorer.exe": "/mnt/c/Windows/explorer.exe"}), fakeWSLPath)
	if bin != "xdg-open" || !slices.Equal(args, []string{"/home/u/repo"}) {
		t.Fatalf("non-WSL linux keeps xdg-open: got (%q, %v)", bin, args)
	}
}

func TestResolveTerminalCommandWSL(t *testing.T) {
	// Native Linux terminal takes precedence when available.
	bin, args, dir, err := resolveTerminalCommand("linux", "/home/u/repo", "", true, "Ubuntu",
		fakeLookPath(map[string]string{"gnome-terminal": "/usr/bin/gnome-terminal", "wt.exe": "/mnt/c/wt.exe"}), fakeWSLPath)
	if err != nil || bin != "/usr/bin/gnome-terminal" || len(args) != 0 || dir != "/home/u/repo" {
		t.Fatalf("linux terminal preferred: got (%q, %v, %q, %v)", bin, args, dir, err)
	}

	// WSL falls back to Windows Terminal with distro and --cd.
	bin, args, dir, err = resolveTerminalCommand("linux", "/home/u/repo", "", true, "Ubuntu",
		fakeLookPath(map[string]string{"wt.exe": "/mnt/c/wt.exe", "cmd.exe": "/mnt/c/cmd.exe"}), fakeWSLPath)
	if err != nil || bin != "/mnt/c/wt.exe" || !slices.Equal(args, []string{"wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) || dir != "" {
		t.Fatalf("wt.exe fallback: got (%q, %v, %q, %v)", bin, args, dir, err)
	}

	// Without distro name, wt.exe uses Windows path via wslpath.
	bin, args, dir, err = resolveTerminalCommand("linux", "/home/u/repo", "", true, "",
		fakeLookPath(map[string]string{"wt.exe": "/mnt/c/wt.exe"}), fakeWSLPath)
	if err != nil || bin != "/mnt/c/wt.exe" || !slices.Equal(args, []string{"-d", `\\wsl.localhost\Ubuntu/home/u/repo`}) || dir != "" {
		t.Fatalf("wt.exe without distro: got (%q, %v, %q, %v)", bin, args, dir, err)
	}

	// Fallback to cmd.exe when wt.exe is absent.
	bin, args, dir, err = resolveTerminalCommand("linux", "/home/u/repo", "", true, "Ubuntu",
		fakeLookPath(map[string]string{"cmd.exe": "/mnt/c/cmd.exe"}), fakeWSLPath)
	if err != nil || bin != "/mnt/c/cmd.exe" || !slices.Equal(args, []string{"/c", "start", "wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) || dir != "" {
		t.Fatalf("cmd.exe fallback: got (%q, %v, %q, %v)", bin, args, dir, err)
	}

	// Error when no terminal is found.
	if _, _, _, err := resolveTerminalCommand("linux", "/home/u/repo", "", true, "Ubuntu", fakeLookPath(nil), fakeWSLPath); err == nil {
		t.Fatal("expected error when no terminal is available")
	}
}
