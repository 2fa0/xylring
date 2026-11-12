package main

// Git 是外部依赖，不是随应用打包的库：解析它、解释它缺席的原因，
// 以及把用户选择的路径记下来，都集中在这里。
import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// gitProbeTimeout 限制单次 `git --version`：卡住的包装脚本不能拖住解析。
	gitProbeTimeout = 5 * time.Second
	// gitProbeBudget 限制一次忙碌的候选路径的总时长，避免个别卡死的脚本
	// 把持着解析锁不放。
	gitProbeBudget = 10 * time.Second
	// gitRetryFailedAfter 是失败结论的复用时长。装好 Git 之后不该需要重启应用。
	gitRetryFailedAfter = 15 * time.Second
)

// gitUnavailableError 表示系统 Git 找不到或根本跑不起来，
// 因此问题不在仓库本身，不该报成“不是 Git 仓库”。
type gitUnavailableError struct {
	message string
	err     error
}

func (e *gitUnavailableError) Error() string { return e.message }
func (e *gitUnavailableError) Unwrap() error { return e.err }

func isGitUnavailable(err error) bool {
	var e *gitUnavailableError
	return errors.As(err, &e)
}

// gitProbe 是一次成功解析的结果。
type gitProbe struct {
	path    string
	version string // `git --version` 的第一行，去掉 "git version " 前缀
}

type gitResolution struct {
	sync.Mutex
	checked  bool
	override string
	probe    gitProbe
	failure  *gitUnavailableError
	failedAt time.Time
}

var gitResolve gitResolution

// gitCommonPaths lists the install locations worth trying when PATH does not
// lead to Git: macOS applications launched from Finder inherit a minimal PATH,
// so a Homebrew Git is installed yet invisible.
var gitCommonPaths = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	join := func(parts ...string) []string {
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	if runtime.GOOS == "windows" {
		var paths []string
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles(x86)")} {
			if root != "" {
				paths = append(paths, filepath.Join(root, "Git", "cmd", "git.exe"))
			}
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			paths = append(paths, filepath.Join(local, "Programs", "Git", "cmd", "git.exe"))
		}
		if home != "" {
			paths = append(paths,
				filepath.Join(home, "scoop", "shims", "git.exe"),
				filepath.Join(home, "AppData", "Local", "Programs", "Git", "cmd", "git.exe"))
		}
		return paths
	}
	return join(
		"/opt/homebrew/bin/git",
		"/usr/local/bin/git",
		"/opt/local/bin/git",
		"/run/current-system/sw/bin/git",
		"/nix/var/nix/profiles/default/bin/git",
		"/snap/bin/git",
		"/usr/bin/git",
		filepath.Join(home, ".local", "bin", "git"),
		filepath.Join(home, ".nix-profile", "bin", "git"),
	)
}

// setGitExecutablePath remembers the binary chosen in settings and forces the
// next Git call to resolve again. An empty path restores automatic discovery.
func setGitExecutablePath(path string) string {
	path = strings.TrimSpace(path)
	if path != "" {
		if !strings.ContainsAny(path, `/\`) {
			if found, err := exec.LookPath(path); err == nil {
				path = found
			}
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	gitResolve.Lock()
	defer gitResolve.Unlock()
	if path == gitResolve.override {
		return path
	}
	gitResolve.override = path
	gitResolve.checked = false
	gitResolve.failure = nil
	return path
}

// resetGitResolution forgets the cached answer so the next call probes again.
func resetGitResolution() {
	gitResolve.Lock()
	defer gitResolve.Unlock()
	gitResolve.checked = false
	gitResolve.failure = nil
}

// gitPath resolves the Git binary to run. A failed answer expires, so
// installing Git or fixing a broken install is picked up without a restart.
func gitPath() (string, error) {
	probe, err := resolveGit()
	if err != nil {
		return "", err
	}
	return probe.path, nil
}

func resolveGit() (gitProbe, error) {
	gitResolve.Lock()
	defer gitResolve.Unlock()
	if gitResolve.checked {
		if gitResolve.failure == nil {
			return gitResolve.probe, nil
		}
		if time.Since(gitResolve.failedAt) < gitRetryFailedAfter {
			return gitResolve.probe, gitResolve.failure
		}
	}
	probe, failure := probeGit(gitResolve.override)
	gitResolve.checked, gitResolve.probe, gitResolve.failure = true, probe, failure
	if failure != nil {
		gitResolve.failedAt = time.Now()
		return probe, failure
	}
	return probe, nil
}

// probeGit prefers an explicit path from settings, then PATH, then the usual
// install locations, and reports why nothing worked when it finds nothing.
func probeGit(override string) (gitProbe, *gitUnavailableError) {
	if override != "" {
		version, err := gitVersion(context.Background(), override)
		if err != nil {
			return gitProbe{}, &gitUnavailableError{
				message: fmt.Sprintf("设置的 Git 路径不可用：%s", gitFailureText(err)),
				err:     err,
			}
		}
		return gitProbe{path: override, version: version}, nil
	}
	var candidates []string
	if p, err := exec.LookPath("git"); err == nil {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, gitCommonPaths()...)
	budget, cancel := context.WithTimeout(context.Background(), gitProbeBudget)
	defer cancel()
	seen := make(map[string]bool, len(candidates))
	var broken []string
	for _, candidate := range candidates {
		if budget.Err() != nil {
			break
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		version, err := gitVersion(budget, abs)
		if err != nil {
			if len(broken) < 3 {
				broken = append(broken, fmt.Sprintf("%s（%s）", abs, gitFailureText(err)))
			}
			continue
		}
		return gitProbe{path: abs, version: version}, nil
	}
	if len(broken) > 0 {
		return gitProbe{}, &gitUnavailableError{
			message: "Git 无法运行：" + strings.Join(broken, "；") + "。请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径",
		}
	}
	return gitProbe{}, &gitUnavailableError{
		message: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径",
	}
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// gitVersion runs the binary once: a path that exists but cannot run (such as
// the macOS /usr/bin/git shim without developer tools) is not a usable Git.
func gitVersion(parent context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, gitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	hideConsoleWindow(cmd)
	cmd.Env = gitBinaryEnv(bin, mergeGitEnv(nil))
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	line := firstLine(string(out))
	if line == "" {
		return "", errors.New("没有输出版本信息")
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "git version ")), nil
}

// gitFailureText keeps a failed probe to one readable line for the interface.
func gitFailureText(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := firstLine(string(exitErr.Stderr)); msg != "" {
			return msg
		}
	}
	return firstLine(err.Error())
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	return strings.ReplaceAll(text, "\r", "")
}

// Only child processes need the selected Git's directory. Changing the app's
// PATH would make automatic discovery keep finding a previous override.
func gitBinaryEnv(bin string, env []string) []string {
	dir := filepath.Dir(bin)
	for i, entry := range env {
		key, path, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(key, "PATH") {
			if path != "" {
				dir += string(os.PathListSeparator) + path
			}
			env[i] = "PATH=" + dir
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// gitExecError turns "the binary we resolved is gone" into an unavailable-Git
// error, so uninstalling Git while the app runs reads as one cause, not as a
// broken repository, and the next call resolves again.
func gitExecError(bin string, err error) error {
	if err == nil || bin == "" {
		return err
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || !samePath(pathErr.Path, bin) {
		return err
	}
	resetGitResolution()
	return &gitUnavailableError{
		message: fmt.Sprintf("Git 无法运行：%s 已不存在，请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径", bin),
		err:     err,
	}
}
