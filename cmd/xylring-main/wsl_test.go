package main

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
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
	// With UTF-16LE BOM:
	withBOM := append([]byte{0xFF, 0xFE}, raw...)
	if got := parseWSLDistroList(withBOM); !slices.Equal(got, []string{"Ubuntu", "Debian"}) {
		t.Fatalf("parseWSLDistroList(BOM) = %v", got)
	}
}

func TestResolveRepoGitPath(t *testing.T) {
	repo := `\\wsl.localhost\Ubuntu\home\u\repo`
	if got := resolveRepoGitPath(repo, "/home/u/repo/.git\n"); got != `\\wsl.localhost\Ubuntu\home\u\repo\.git` {
		t.Fatalf("abs linux git-dir = %q", got)
	}
	if got := resolveRepoGitPath(repo, ".git/index\n"); got != `\\wsl.localhost\Ubuntu\home\u\repo\.git\index` {
		t.Fatalf("rel linux git-path = %q", got)
	}
	if got := resolveRepoGitPath(repo, "/home/u/main/.git/worktrees/wt/index"); got != `\\wsl.localhost\Ubuntu\home\u\main\.git\worktrees\wt\index` {
		t.Fatalf("worktree linux git-path = %q", got)
	}
}

func TestWindowsWSLTerminalAndEditor(t *testing.T) {
	bin, args, ok := resolveWindowsWSLTerminalCommand(`\\wsl.localhost\Ubuntu\home\u\repo`, fakeLookPath(map[string]string{"wt.exe": `C:\wt.exe`}))
	if !ok || bin != `C:\wt.exe` || !slices.Equal(args, []string{"wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) {
		t.Fatalf("wt.exe terminal: got (%q, %v, %v)", bin, args, ok)
	}

	bin, args, ok = resolveWindowsWSLTerminalCommand(`\\wsl$\Ubuntu\home\u\repo`, fakeLookPath(nil))
	if !ok || bin != "cmd" || !slices.Equal(args, []string{"/c", "start", "", "wsl.exe", "-d", "Ubuntu", "--cd", "/home/u/repo"}) {
		t.Fatalf("cmd fallback terminal: got (%q, %v, %v)", bin, args, ok)
	}

	noStat := func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	codeCmd := `C:\Users\u\AppData\Local\Programs\Microsoft VS Code\bin\code.cmd`
	bin, args, err := resolveEditorCommand("windows", `C:\Users\u`, "vscode", `\\wsl.localhost\Ubuntu\home\u\repo`, noStat,
		fakeLookPath(map[string]string{"code": codeCmd}))
	wantArgs := []string{"/d", "/c", "call", codeCmd, "--remote", "wsl+Ubuntu", "/home/u/repo"}
	if err != nil || bin != "cmd.exe" || !slices.Equal(args, wantArgs) {
		t.Fatalf("vscode wsl remote: got (%q, %v, %v)", bin, args, err)
	}
}

func TestDecodeWSLCommandOutput(t *testing.T) {
	// UTF-16LE with BOM from wsl.exe stderr should be decoded for WSL repos.
	utf16Err := []byte{
		0xFF, 0xFE,
		'w', 0, 's', 0, 'l', 0, ':', 0, ' ', 0, 'n', 0, 'o', 0, 't', 0, ' ', 0, 'f', 0, 'o', 0, 'u', 0, 'n', 0, 'd', 0,
	}
	got := decodeWSLCommandOutput(`\\wsl.localhost\debian13\home\u\repo`, utf16Err)
	if string(got) != "wsl: not found" {
		t.Fatalf("decodeWSLCommandOutput = %q, want %q", got, "wsl: not found")
	}

	// Non-WSL paths leave raw bytes untouched.
	nonWSL := decodeWSLCommandOutput(`C:\repo`, utf16Err)
	if len(nonWSL) != len(utf16Err) {
		t.Fatalf("non-WSL output should stay untouched")
	}

	// Normal UTF-8 stderr on WSL repos stays untouched.
	utf8Err := []byte("fatal: detected dubious ownership\n")
	if got := decodeWSLCommandOutput(`\\wsl.localhost\debian13\home\u\repo`, utf8Err); string(got) != string(utf8Err) {
		t.Fatalf("utf8 stderr = %q, want %q", got, utf8Err)
	}
}

func TestSummarizeLogStderr(t *testing.T) {
	raw := []byte("fatal: unable to access 'https://user:secret@example.com/repo.git/': Failed")
	got := summarizeLogStderr(raw)
	if strings.Contains(got, "secret") {
		t.Fatalf("summarizeLogStderr leaked credentials: %q", got)
	}
	if !strings.Contains(got, "https://***@example.com") {
		t.Fatalf("summarizeLogStderr unexpected output: %q", got)
	}
}

func TestWSLCommandArgs(t *testing.T) {
	args := wslCommandArgs("Debian", "/home/u/repo", nil, "log", "--format=%(trailers:key=AI-Agent)")
	wantPrefix := []string{"-d", "Debian", "--cd", "/home/u/repo", "--exec", "env"}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("wslCommandArgs prefix = %v, want %v", args, wantPrefix)
	}
	if args[len(args)-2] != "log" || args[len(args)-1] != "--format=%(trailers:key=AI-Agent)" {
		t.Fatalf("wslCommandArgs tail = %v", args[len(args)-2:])
	}
}

func TestResolveDefaultWSLRepoRoot(t *testing.T) {
	if got := resolveDefaultWSLRepoRoot(nil, nil); got != `\\wsl.localhost\` {
		t.Fatalf("no distros = %q", got)
	}
	if got := resolveDefaultWSLRepoRoot([]string{"Debian"}, func(string) string { return "/root" }); got != `\\wsl.localhost\Debian\root` {
		t.Fatalf("root user = %q", got)
	}
	if got := resolveDefaultWSLRepoRoot([]string{"Debian"}, func(string) string { return "/home/alice" }); got != `\\wsl.localhost\Debian\home\alice` {
		t.Fatalf("normal user = %q", got)
	}
	if got := resolveDefaultWSLRepoRoot([]string{"Debian"}, func(string) string { return "" }); got != `\\wsl.localhost\Debian` {
		t.Fatalf("fallback on empty home = %q", got)
	}

	// UTF-16LE with BOM and trailing CRLF from wsl.exe.
	utf16Home := []byte{0xFF, 0xFE, '/', 0, 'h', 0, 'o', 0, 'm', 0, 'e', 0, '/', 0, 'a', 0, 'l', 0, 'i', 0, 'c', 0, 'e', 0, '\r', 0, '\n', 0}
	if got := parseWSLHomeOutput(utf16Home); got != "/home/alice" {
		t.Fatalf("parseWSLHomeOutput(UTF-16) = %q", got)
	}
	if got := parseWSLHomeOutput([]byte("relative/path\n")); got != "" {
		t.Fatalf("parseWSLHomeOutput(relative) = %q, want empty", got)
	}
}

func TestWSLFindGitReposArgsAndOutput(t *testing.T) {
	args := wslFindGitReposArgs("Debian", "/home/alice/projects/demo-app")
	wantPrefix := []string{"-d", "Debian", "--cd", "/home/alice/projects/demo-app", "--exec", "find", ".", "-xdev"}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("wslFindGitReposArgs prefix = %v", args)
	}
	for _, dir := range []string{"node_modules", "vendor", ".venv", "target"} {
		if !slices.Contains(args, dir) {
			t.Fatalf("wslFindGitReposArgs missing prune dir %q: %v", dir, args)
		}
	}

	rootArgs := wslFindGitReposArgs("Debian", "/")
	for _, dir := range []string{"proc", "sys", "dev", "run", "mnt"} {
		if !slices.Contains(rootArgs, dir) {
			t.Fatalf("root wslFindGitReposArgs missing prune dir %q: %v", dir, rootArgs)
		}
	}

	raw := []byte(".git\n./sub/nested/.git\r\n./sub/nested/.git\n")
	got := parseWSLFindGitOutput("Debian", "/home/alice/projects/demo-app", raw)
	want := []string{
		`\\wsl.localhost\Debian\home\alice\projects\demo-app`,
		`\\wsl.localhost\Debian\home\alice\projects\demo-app\sub\nested`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("parseWSLFindGitOutput = %v, want %v", got, want)
	}
}

func makeExitError(t *testing.T, code int, stderr []byte) *exec.ExitError {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "exit", strconv.Itoa(code))
	} else {
		cmd = exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
	}
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError for code %d, got %v", code, err)
	}
	exitErr.Stderr = stderr
	return exitErr
}

func TestClassifyWSLFindError(t *testing.T) {
	if handled, err := classifyWSLFindError(nil); !handled || err != nil {
		t.Fatalf("nil: got (%v, %v)", handled, err)
	}
	if handled, err := classifyWSLFindError(os.ErrNotExist); handled || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-ExitError: got (%v, %v)", handled, err)
	}

	exit127 := makeExitError(t, 127, []byte("find: command not found\n"))
	if handled, err := classifyWSLFindError(exit127); handled || err == nil {
		t.Fatalf("exit 127: got (%v, %v), want fallback", handled, err)
	}

	exit1Perm := makeExitError(t, 1, []byte("find: './root': Permission denied\n"))
	if handled, err := classifyWSLFindError(exit1Perm); !handled || err != nil {
		t.Fatalf("exit 1 permission denied: got (%v, %v)", handled, err)
	}

	utf16WSL := []byte{0xFF, 0xFE, 'w', 0, 's', 0, 'l', 0, ':', 0, ' ', 0, 'b', 0, 'a', 0, 'd', 0}
	exit1WSL := makeExitError(t, 1, utf16WSL)
	if handled, err := classifyWSLFindError(exit1WSL); !handled || err == nil || !strings.Contains(err.Error(), "wsl: bad") {
		t.Fatalf("exit 1 wsl error: got (%v, %v)", handled, err)
	}

	exit2 := makeExitError(t, 2, nil)
	if handled, err := classifyWSLFindError(exit2); !handled || err == nil {
		t.Fatalf("exit 2: got (%v, %v)", handled, err)
	}
}
