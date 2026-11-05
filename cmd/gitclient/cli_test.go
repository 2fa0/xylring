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
}

func TestInstallAndUninstallCLIScriptInDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "xylring-cli-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	appBundle := "/Applications/Xylring.app"
	appBin := "/Applications/Xylring.app/Contents/MacOS/Xylring"
	script := generateCLIScriptContent(appBundle, appBin)

	cmdName := "xylring"
	if runtime.GOOS == "windows" {
		cmdName = "xylring.cmd"
		script = generateWindowsCmdContent(appBin)
	}

	targetFile := filepath.Join(tmpDir, cmdName)
	if err := os.WriteFile(targetFile, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("script file does not exist: %v", err)
	}
	if runtime.GOOS != "windows" && (info.Mode().Perm()&0o111) == 0 {
		t.Errorf("script file is not executable: %v", info.Mode().Perm())
	}

	// 模拟卸载检查
	data, err := os.ReadFile(targetFile)
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if !strings.Contains(string(data), "Xylring") {
		t.Errorf("content does not contain Xylring identifier")
	}
	if err := os.Remove(targetFile); err != nil {
		t.Fatalf("failed to remove script file: %v", err)
	}
}
