package gitx

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
	// GitProbeTimeout 限制单次 `git --version`：卡住的包装脚本不能拖住解析。
	GitProbeTimeout = 5 * time.Second
	// GitProbeBudget 限制一次忙碌的候选路径的总时长，避免个别卡死的脚本
	// 把持着解析锁不放。
	GitProbeBudget = 10 * time.Second
	// GitRetryFailedAfter 是失败结论的复用时长。装好 Git 之后不该需要重启应用。
	GitRetryFailedAfter = 15 * time.Second
)

// GitUnavailableError 表示系统 Git 找不到或根本跑不起来，
// 因此问题不在仓库本身，不该报成“不是 Git 仓库”。
type GitUnavailableError struct {
	Message string
	Err     error
}

func (e *GitUnavailableError) Error() string { return e.Message }
func (e *GitUnavailableError) Unwrap() error { return e.Err }

func IsGitUnavailable(err error) bool {
	var e *GitUnavailableError
	return errors.As(err, &e)
}

// GitProbe 是一次成功解析的结果。
type GitProbe struct {
	Path    string
	Version string // `git --version` 的第一行，去掉 "git version " 前缀
}

type GitResolution struct {
	sync.Mutex
	Checked  bool
	Override string
	Probe    GitProbe
	Failure  *GitUnavailableError
	FailedAt time.Time
}

var GitResolve GitResolution

// GitCommonPaths lists the install locations worth trying when PATH does not
// lead to Git: macOS applications launched from Finder inherit a minimal PATH,
// so a Homebrew Git is installed yet invisible.
var GitCommonPaths = func() []string {
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

// SetGitExecutablePath remembers the binary chosen in settings and forces the
// next Git call to resolve again. An empty path restores automatic discovery.
func SetGitExecutablePath(path string) string {
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
	GitResolve.Lock()
	defer GitResolve.Unlock()
	if path == GitResolve.Override {
		return path
	}
	GitResolve.Override = path
	GitResolve.Checked = false
	GitResolve.Failure = nil
	return path
}

// ResetGitResolution forgets the cached answer so the next call probes again.
func ResetGitResolution() {
	GitResolve.Lock()
	defer GitResolve.Unlock()
	GitResolve.Checked = false
	GitResolve.Failure = nil
}

// GitPath resolves the Git binary to run. A failed answer expires, so
// installing Git or fixing a broken install is picked up without a restart.
func GitPath() (string, error) {
	probe, err := ResolveGit()
	if err != nil {
		return "", err
	}
	return probe.Path, nil
}

func ResolveGit() (GitProbe, error) {
	GitResolve.Lock()
	defer GitResolve.Unlock()
	if GitResolve.Checked {
		if GitResolve.Failure == nil {
			return GitResolve.Probe, nil
		}
		if time.Since(GitResolve.FailedAt) < GitRetryFailedAfter {
			return GitResolve.Probe, GitResolve.Failure
		}
	}
	probe, failure := ProbeGit(GitResolve.Override)
	GitResolve.Checked, GitResolve.Probe, GitResolve.Failure = true, probe, failure
	if failure != nil {
		GitResolve.FailedAt = time.Now()
		return probe, failure
	}
	return probe, nil
}

// ProbeGit prefers an explicit path from settings, then PATH, then the usual
// install locations, and reports why nothing worked when it finds nothing.
func ProbeGit(override string) (GitProbe, *GitUnavailableError) {
	if override != "" {
		version, err := GitVersion(context.Background(), override)
		if err != nil {
			return GitProbe{}, &GitUnavailableError{
				Message: fmt.Sprintf("设置的 Git 路径不可用：%s", gitFailureText(err)),
				Err:     err,
			}
		}
		return GitProbe{Path: override, Version: version}, nil
	}
	var candidates []string
	if p, err := exec.LookPath("git"); err == nil {
		candidates = append(candidates, p)
	}
	candidates = append(candidates, GitCommonPaths()...)
	budget, cancel := context.WithTimeout(context.Background(), GitProbeBudget)
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
		version, err := GitVersion(budget, abs)
		if err != nil {
			if len(broken) < 3 {
				broken = append(broken, fmt.Sprintf("%s（%s）", abs, gitFailureText(err)))
			}
			continue
		}
		return GitProbe{Path: abs, Version: version}, nil
	}
	if len(broken) > 0 {
		return GitProbe{}, &GitUnavailableError{
			Message: "Git 无法运行：" + strings.Join(broken, "；") + "。请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径",
		}
	}
	return GitProbe{}, &GitUnavailableError{
		Message: "未找到 Git。请先安装 Git，或在设置中指定 Git 可执行文件路径",
	}
}

func SamePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// GitVersion runs the binary once: a path that exists but cannot run (such as
// the macOS /usr/bin/git shim without developer tools) is not a usable Git.
func GitVersion(parent context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, GitProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	HideConsoleWindow(cmd)
	cmd.Env = GitBinaryEnv(bin, MergeGitEnv(nil))
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	line := FirstLine(string(out))
	if line == "" {
		return "", errors.New("没有输出版本信息")
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "git version ")), nil
}

// gitFailureText keeps a failed probe to one readable line for the interface.
func gitFailureText(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := FirstLine(string(exitErr.Stderr)); msg != "" {
			return msg
		}
	}
	return FirstLine(err.Error())
}

func FirstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	return strings.ReplaceAll(text, "\r", "")
}

// Only child processes need the selected Git's directory. Changing the app's
// PATH would make automatic discovery keep finding a previous override.
func GitBinaryEnv(bin string, env []string) []string {
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

// GitExecError turns "the binary we resolved is gone" into an unavailable-Git
// error, so uninstalling Git while the app runs reads as one cause, not as a
// broken repository, and the next call resolves again.
func GitExecError(bin string, err error) error {
	if err == nil || bin == "" {
		return err
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || !SamePath(pathErr.Path, bin) {
		return err
	}
	ResetGitResolution()
	return &GitUnavailableError{
		Message: fmt.Sprintf("Git 无法运行：%s 已不存在，请重新安装 Git，或在设置中指定可用的 Git 可执行文件路径", bin),
		Err:     err,
	}
}
