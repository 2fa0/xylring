package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizePaths(t *testing.T) {
	wd := "/mock/workspace"
	if runtime.GOOS == "windows" {
		wd = `C:\mock\workspace`
	}

	tests := []struct {
		name     string
		args     []string
		expected []string
	}{
		{
			name:     "empty",
			args:     []string{},
			expected: nil,
		},
		{
			name:     "flags filtered out",
			args:     []string{"-v", "--help", "-f"},
			expected: nil,
		},
		{
			name:     "current dot directory",
			args:     []string{"."},
			expected: []string{wd},
		},
		{
			name:     "relative subdirectory",
			args:     []string{"sub/repo"},
			expected: []string{filepath.Join(wd, "sub", "repo")},
		},
		{
			name:     "parent directory",
			args:     []string{".."},
			expected: []string{filepath.Dir(wd)},
		},
		{
			name:     "mixed paths and flags",
			args:     []string{"-v", ".", "--flag", "sub/repo"},
			expected: []string{wd, filepath.Join(wd, "sub", "repo")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePaths(tt.args, wd)
			if len(got) != len(tt.expected) {
				t.Fatalf("expected %d paths, got %d: %v", len(tt.expected), len(got), got)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("[%d] expected %q, got %q", i, tt.expected[i], got[i])
				}
			}
		})
	}
}

func TestHandleCLIArgsHelpAndVersion(t *testing.T) {
	// 基础选项应返回 true，表示无需进入 GUI
	for _, flag := range []string{"-v", "--version", "version", "-h", "--help", "help"} {
		if !handleCLIArgs([]string{flag}) {
			t.Errorf("handleCLIArgs([%q]) expected true, got false", flag)
		}
	}

	// 路径或空参数应返回 false，继续 GUI 模式
	if handleCLIArgs([]string{}) {
		t.Errorf("handleCLIArgs([]) expected false, got true")
	}
	if handleCLIArgs([]string{"."}) {
		t.Errorf("handleCLIArgs([\".\"]) expected false, got true")
	}
	if handleCLIArgs([]string{"/path/to/repo"}) {
		t.Errorf("handleCLIArgs([\"/path/to/repo\"]) expected false, got true")
	}
}

func TestGenerateCLIScriptContent(t *testing.T) {
	appBundle := "/Applications/Xylring.app"
	appBin := "/Applications/Xylring.app/Contents/MacOS/Xylring"
	script := generateCLIScriptContent(appBundle, appBin)

	if !strings.HasPrefix(script, "#!/usr/bin/env bash") {
		t.Errorf("script should start with bash shebang")
	}
	if !strings.Contains(script, appBundle) {
		t.Errorf("script should contain appBundle path")
	}
	if !strings.Contains(script, appBin) {
		t.Errorf("script should contain appBin path")
	}
	if !strings.Contains(script, "open -a") {
		t.Errorf("script on macOS should use open -a")
	}
	if !strings.Contains(script, "ABS_DIR=") {
		t.Errorf("script should resolve directory to absolute path")
	}
}

func TestGenerateWindowsCmdContent(t *testing.T) {
	appBin := `C:\Program Files\Xylring\Xylring.exe`
	content := generateWindowsCmdContent(appBin)

	if !strings.Contains(content, "@echo off") {
		t.Errorf("windows cmd should have @echo off")
	}
	if !strings.Contains(content, appBin) {
		t.Errorf("windows cmd should contain appBin path")
	}
	if !strings.Contains(content, "%*") {
		t.Errorf("windows cmd should forward arguments")
	}
	if !strings.Contains(content, "goto cli") {
		t.Errorf("windows cmd should route CLI subcommands straight to the binary")
	}
}

// TestInstallAndUninstallCLITool 真正走一遍安装与卸载：HOME 与 PATH 都指向临时目录，
// 既不读也不改用户已有的 ~/.local/bin、~/bin 或 /usr/local/bin。
func TestInstallAndUninstallCLITool(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)

	cmdName := "xylring"
	if runtime.GOOS == "windows" {
		cmdName = "xylring.cmd"
	}

	installed, err := installCLITool()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(installed) != cmdName {
		t.Fatalf("installed %q, want a launcher named %q", installed, cmdName)
	}
	if !strings.HasPrefix(filepath.Clean(installed), filepath.Clean(home)+string(filepath.Separator)) {
		t.Fatalf("installed outside the test home: %q", installed)
	}
	info, err := os.Stat(installed)
	if err != nil || info.IsDir() {
		t.Fatalf("launcher missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("launcher is not executable: %v", info.Mode().Perm())
	}

	// 卸载会清理所有候选目录里带 Xylring 标记的同名文件；若别的目录已有真实安装，
	// 就只验证安装结果，避免删掉本机已有的命令行工具。
	for _, dir := range getCandidateBinDirs() {
		if other := filepath.Join(dir, cmdName); other != installed {
			if _, err := os.Stat(other); err == nil {
				t.Logf("skipping uninstall assertion: %s already exists", other)
				return
			}
		}
	}

	removed, err := uninstallCLITool()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(removed, installed) {
		t.Fatalf("uninstalled %q, want %q", removed, installed)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("launcher still present after uninstall: %v", err)
	}
}
