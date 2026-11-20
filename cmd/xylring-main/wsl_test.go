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

func TestParseAndFormatWSLUNCPath(t *testing.T) {
	for _, tc := range []struct {
		in        string
		wantOK    bool
		wantDist  string
		wantLinux string
		wantUNC   string
	}{
		{`\\wsl.localhost\Ubuntu\home\user\repo`, true, "Ubuntu", "/home/user/repo", `\\wsl.localhost\Ubuntu\home\user\repo`},
		{`\\wsl$\Ubuntu-22.04\home\user\repo\`, true, "Ubuntu-22.04", "/home/user/repo", `\\wsl.localhost\Ubuntu-22.04\home\user\repo`},
		{`//WSL.LOCALHOST/Debian/var/log`, true, "Debian", "/var/log", `\\wsl.localhost\Debian\var\log`},
		{`\\wsl.localhost\Ubuntu`, true, "Ubuntu", "/", `\\wsl.localhost\Ubuntu\`},
		{`C:\Users\me\repo`, false, "", "", ""},
		{`/home/user/repo`, false, "", "", ""},
	} {
		distro, linuxPath, ok := parseWSLUNCPath(tc.in)
		if ok != tc.wantOK || distro != tc.wantDist || linuxPath != tc.wantLinux {
			t.Fatalf("parseWSLUNCPath(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.in, distro, linuxPath, ok, tc.wantDist, tc.wantLinux, tc.wantOK)
		}
		if ok {
			if got := formatWSLUNCPath(distro, linuxPath); got != tc.wantUNC {
				t.Fatalf("formatWSLUNCPath(%q, %q) = %q, want %q", distro, linuxPath, got, tc.wantUNC)
			}
			if got := canonicalRepoDir(tc.in); got != tc.wantUNC {
				t.Fatalf("canonicalRepoDir(%q) = %q, want %q", tc.in, got, tc.wantUNC)
			}
		}
	}
}

func TestWSLGitEnvAndWorktreeList(t *testing.T) {
	env := wslGitEnv("Ubuntu", "/home/u/repo", []string{
		`GIT_INDEX_FILE=\\wsl$\Ubuntu\home\u\repo\.git\index`,
		"CUSTOM_VAR=1",
	})
	for _, want := range []string{
		"LC_ALL=C",
		"GIT_CEILING_DIRECTORIES=/home/u",
		"GIT_INDEX_FILE=/home/u/repo/.git/index",
		"CUSTOM_VAR=1",
	} {
		if !slices.Contains(env, want) {
			t.Fatalf("missing env %q in %v", want, env)
		}
	}

	raw := []byte("worktree /home/u/repo\x00HEAD 0123456789abcdef0123456789abcdef01234567\x00branch refs/heads/main\x00\x00worktree /home/u/repo-wt\x00HEAD 0123456789abcdef0123456789abcdef01234567\x00branch refs/heads/feat\x00\x00")
	wts := parseWorktreeListForRepo(`\\wsl.localhost\Ubuntu\home\u\repo`, raw)
	if len(wts) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(wts))
	}
	if wts[0].path != `\\wsl.localhost\Ubuntu\home\u\repo` || wts[1].path != `\\wsl.localhost\Ubuntu\home\u\repo-wt` {
		t.Fatalf("unexpected worktree paths: %+v", wts)
	}
}

func TestParseWSLDistroList(t *testing.T) {
	// Simulate UTF-16LE output from `wsl.exe -l -q` when WSL_UTF8 is unavailable.
	raw := []byte("U\x00b\x00u\x00n\x00t\x00u\x00\r\x00\n\x00D\x00e\x00b\x00i\x00a\x00n\x00\r\x00\n\x00")
	got := parseWSLDistroList(raw)
	if !slices.Equal(got, []string{"Ubuntu", "Debian"}) {
		t.Fatalf("parseWSLDistroList = %v", got)
	}
}

func TestWindowsWSLTerminalAndEditor(t *testing.T) {
	bin, args, ok := resolveWindowsWSLTerminalCommand(`\\wsl.localhost\Ubuntu\home\u\repo`, fakeLookPath(map[string]string{"wt.exe": `C:\wt.exe`}))
	if !ok || bin != `C:\wt.exe` || !slices.Equal(args, []string{"wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) {
		t.Fatalf("wt.exe terminal: got (%q, %v, %v)", bin, args, ok)
	}

	bin, args, ok = resolveWindowsWSLTerminalCommand(`\\wsl$\Ubuntu\home\u\repo`, fakeLookPath(nil))
	if !ok || bin != "cmd" || !slices.Equal(args, []string{"/c", "start", "wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) {
		t.Fatalf("cmd fallback terminal: got (%q, %v, %v)", bin, args, ok)
	}

	noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	bin, args, err := resolveEditorCommand("windows", `C:\Users\u`, "vscode", `\\wsl.localhost\Ubuntu\home\u\repo`, noStat,
		fakeLookPath(map[string]string{"code": `C:\Users\u\AppData\Local\Programs\Microsoft VS Code\bin\code.cmd`}))
	if err != nil || !slices.Equal(args, []string{"--remote", "wsl+Ubuntu", "/home/u/repo"}) {
		t.Fatalf("vscode wsl remote: got (%q, %v, %v)", bin, args, err)
	}
}
