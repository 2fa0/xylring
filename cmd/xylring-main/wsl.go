package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// parseWSLUNCPath parses a Windows WSL UNC path such as
// \\wsl.localhost\Ubuntu\home\user\repo or \\wsl$\Ubuntu\home\user\repo.
func parseWSLUNCPath(p string) (distro, linuxPath string, ok bool) {
	s := strings.ReplaceAll(strings.TrimSpace(p), "/", `\`)
	lower := strings.ToLower(s)
	var rest string
	switch {
	case strings.HasPrefix(lower, `\\wsl.localhost\`):
		rest = s[len(`\\wsl.localhost\`):]
	case strings.HasPrefix(lower, `\\wsl$\`):
		rest = s[len(`\\wsl$\`):]
	default:
		return "", "", false
	}
	rest = strings.TrimLeft(rest, `\`)
	if rest == "" {
		return "", "", false
	}
	idx := strings.IndexByte(rest, '\\')
	if idx < 0 {
		return rest, "/", true
	}
	distro = rest[:idx]
	sub := strings.ReplaceAll(rest[idx:], `\`, "/")
	linuxPath = path.Clean(sub)
	if !strings.HasPrefix(linuxPath, "/") {
		linuxPath = "/" + linuxPath
	}
	return distro, linuxPath, true
}

// formatWSLUNCPath formats a WSL distro and Linux path as a Windows UNC path.
func formatWSLUNCPath(distro, linuxPath string) string {
	clean := path.Clean("/" + strings.ReplaceAll(strings.TrimSpace(linuxPath), `\`, "/"))
	if clean == "/" {
		return `\\wsl.localhost\` + distro + `\`
	}
	return `\\wsl.localhost\` + distro + strings.ReplaceAll(clean, "/", `\`)
}

// wslGitEnv builds the Linux environment assignments passed to `env` inside WSL.
func wslGitEnv(distro, linuxDir string, extra []string) []string {
	envMap := map[string]string{
		"LC_ALL":             "C",
		"LANG":               "C",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_PAGER":          "cat",
		"PAGER":              "cat",
		"GIT_OPTIONAL_LOCKS": "0",
	}
	var keys []string
	for k := range envMap {
		keys = append(keys, k)
	}
	if parent := path.Dir(linuxDir); linuxDir != "/" && parent != "" && parent != linuxDir {
		envMap["GIT_CEILING_DIRECTORIES"] = parent
		keys = append(keys, "GIT_CEILING_DIRECTORIES")
	}
	for _, entry := range extra {
		k, v, ok := strings.Cut(entry, "=")
		if !ok || k == "" {
			continue
		}
		if d, lp, isWSL := parseWSLUNCPath(v); isWSL && strings.EqualFold(d, distro) {
			v = lp
		}
		if _, exists := envMap[k]; !exists {
			keys = append(keys, k)
		}
		envMap[k] = v
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+envMap[k])
	}
	return out
}

var (
	wslPathMu  sync.Mutex
	wslPathVal string
)

func wslPath() (string, error) {
	wslPathMu.Lock()
	cached := wslPathVal
	wslPathMu.Unlock()
	if cached != "" {
		return cached, nil
	}
	bin, err := exec.LookPath("wsl.exe")
	if err != nil {
		return "", &gitUnavailableError{
			message: "未找到 wsl.exe，无法访问 WSL 仓库",
			err:     err,
		}
	}
	wslPathMu.Lock()
	wslPathVal = bin
	wslPathMu.Unlock()
	return bin, nil
}

// resolveRepoGitPath maps a path returned by Git (e.g. from
// `rev-parse --absolute-git-dir` or `--git-path`) into a host-accessible path.
// For WSL repositories, Linux paths are mapped back to `\\wsl.localhost\...`.
func resolveRepoGitPath(repoDir, gitOutPath string) string {
	if target, ok := parseDockerRepoKey(repoDir); ok {
		// 容器内的路径就是容器内的路径，不映射成主机路径。
		return containerPath(target.path, strings.TrimSuffix(gitOutPath, "\n"))
	}
	p := strings.TrimSpace(gitOutPath)
	if p == "" {
		return ""
	}
	if distro, repoLinux, ok := parseWSLUNCPath(repoDir); ok {
		clean := strings.ReplaceAll(p, `\`, "/")
		if strings.HasPrefix(clean, "/") {
			return formatWSLUNCPath(distro, clean)
		}
		return formatWSLUNCPath(distro, path.Join(repoLinux, clean))
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(repoDir, filepath.FromSlash(p))
}

func wslCommandArgs(distro, linuxDir string, extraEnv []string, fullArgs ...string) []string {
	if linuxDir == "" {
		linuxDir = "/"
	}
	wslArgs := []string{"-d", distro, "--cd", linuxDir, "--exec", "env"}
	wslArgs = append(wslArgs, wslGitEnv(distro, linuxDir, extraEnv)...)
	wslArgs = append(wslArgs, "git")
	return append(wslArgs, fullArgs...)
}

func wslCommand(ctx context.Context, distro, linuxDir string, extraEnv []string, fullArgs ...string) (*exec.Cmd, string, error) {
	wslBin, err := wslPath()
	if err != nil {
		return nil, "", err
	}
	cmd := exec.CommandContext(ctx, wslBin, wslCommandArgs(distro, linuxDir, extraEnv, fullArgs...)...)
	hideConsoleWindow(cmd)
	cmd.Env = append(cmd.Environ(), "WSL_UTF8=1")
	cmd.WaitDelay = 2 * time.Second
	return cmd, wslBin, nil
}

func decodeWSLCommandOutput(dir string, raw []byte) []byte {
	if len(raw) == 0 {
		return raw
	}
	if _, _, ok := parseWSLUNCPath(dir); !ok {
		return raw
	}
	hasUTF16BOM := len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE
	if !hasUTF16BOM && (len(raw)%2 != 0 || bytes.IndexByte(raw, 0) < 0) {
		return raw
	}
	return []byte(strings.TrimPrefix(decodeWSLOutput(raw), "\uFEFF"))
}

func decodeWSLOutput(raw []byte) string {
	hasUTF16BOM := len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE
	if hasUTF16BOM || (len(raw)%2 == 0 && bytes.IndexByte(raw, 0) >= 0) {
		if hasUTF16BOM {
			raw = raw[2:]
		}
		u16 := make([]uint16, len(raw)/2)
		for i := range u16 {
			u16[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
		}
		return string(utf16.Decode(u16))
	}
	return string(raw)
}

func parseWSLDistroList(raw []byte) []string {
	cleaned := strings.TrimPrefix(decodeWSLOutput(raw), "\uFEFF")
	var out []string
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func listWSLDistros() []string {
	wslBin, err := wslPath()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wslBin, "-l", "-q")
	hideConsoleWindow(cmd)
	cmd.Env = append(cmd.Environ(), "WSL_UTF8=1")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseWSLDistroList(out)
}

func parseWSLHomeOutput(raw []byte) string {
	home := strings.TrimSpace(strings.TrimPrefix(decodeWSLOutput(raw), "\uFEFF"))
	if !strings.HasPrefix(home, "/") {
		return ""
	}
	return path.Clean(home)
}

func wslUserHome(distro string) string {
	wslBin, err := wslPath()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, wslBin, "-d", distro, "--cd", "~", "--exec", "pwd")
	hideConsoleWindow(cmd)
	cmd.Env = append(cmd.Environ(), "WSL_UTF8=1")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseWSLHomeOutput(out)
}

func resolveDefaultWSLRepoRoot(distros []string, homeFn func(string) string) string {
	if len(distros) == 0 {
		return `\\wsl.localhost\`
	}
	distro := distros[0]
	if homeFn != nil {
		if home := homeFn(distro); home != "" {
			return formatWSLUNCPath(distro, home)
		}
	}
	return `\\wsl.localhost\` + distro
}

func defaultWSLRepoRoot() string {
	return resolveDefaultWSLRepoRoot(listWSLDistros(), wslUserHome)
}

func wslFindGitReposArgs(distro, linuxDir string) []string {
	if linuxDir == "" {
		linuxDir = "/"
	}
	args := []string{
		"-d", distro, "--cd", linuxDir, "--exec",
		"find", ".", "-xdev",
		"-name", ".git", "(", "-type", "d", "-o", "-type", "f", ")", "-print", "-prune",
		"-o", "(",
	}
	pruneDirs := skippedRepoDirs
	if linuxDir == "/" {
		pruneDirs = append(append([]string(nil), skippedRepoDirs...), "proc", "sys", "dev", "run", "mnt")
	}
	for i, name := range pruneDirs {
		if i > 0 {
			args = append(args, "-o")
		}
		args = append(args, "-name", name)
	}
	return append(args, ")", "-prune")
}

func parseWSLFindGitOutput(distro, linuxDir string, raw []byte) []string {
	cleaned := strings.TrimPrefix(decodeWSLOutput(raw), "\uFEFF")
	seen := make(map[string]struct{})
	var roots []string
	for _, line := range strings.Split(cleaned, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		line = path.Clean(strings.ReplaceAll(line, `\`, "/"))
		var relRepo string
		switch {
		case line == ".git":
			relRepo = "."
		case strings.HasSuffix(line, "/.git"):
			relRepo = strings.TrimSuffix(line, "/.git")
		default:
			continue
		}
		unc := formatWSLUNCPath(distro, path.Join(linuxDir, relRepo))
		if _, ok := seen[unc]; ok {
			continue
		}
		seen[unc] = struct{}{}
		roots = append(roots, unc)
	}
	sort.Strings(roots)
	return roots
}

func classifyWSLFindError(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false, err
	}
	code := exitErr.ExitCode()
	if code == 126 || code == 127 {
		return false, err
	}
	stderr := strings.TrimSpace(strings.TrimPrefix(decodeWSLOutput(exitErr.Stderr), "\uFEFF"))
	if code == 1 && !strings.HasPrefix(strings.ToLower(stderr), "wsl:") {
		// `find` exits 1 when any unreadable subdirectory causes permission denied.
		return true, nil
	}
	if stderr != "" {
		return true, errors.New(trimMsg(stderr))
	}
	return true, fmt.Errorf("WSL 扫描仓库失败（退出码 %d）", code)
}

func scanWSLGitRepos(ctx context.Context, distro, linuxDir string) ([]string, bool, error) {
	wslBin, err := wslPath()
	if err != nil {
		return nil, false, err
	}
	cmd := exec.CommandContext(ctx, wslBin, wslFindGitReposArgs(distro, linuxDir)...)
	hideConsoleWindow(cmd)
	cmd.Env = append(cmd.Environ(), "WSL_UTF8=1")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, true, ctx.Err()
	}
	roots := parseWSLFindGitOutput(distro, linuxDir, out)
	if len(roots) > 0 {
		return roots, true, nil
	}
	handled, outErr := classifyWSLFindError(err)
	return nil, handled, outErr
}

func resolveWindowsWSLTerminalCommand(abs string, lookPathFn func(string) (string, error)) (bin string, args []string, ok bool) {
	distro, linuxPath, isWSL := parseWSLUNCPath(abs)
	if !isWSL {
		return "", nil, false
	}
	wslArgs := []string{"wsl.exe", "-d", distro, "--cd", linuxPath}
	if wt, err := lookPathFn("wt.exe"); err == nil {
		return wt, wslArgs, true
	}
	return "cmd", append([]string{"/c", "start", ""}, wslArgs...), true
}

func resolveWindowsWSLEditorArgs(presetID, targetPath string) ([]string, bool) {
	distro, linuxPath, ok := parseWSLUNCPath(targetPath)
	if !ok {
		return nil, false
	}
	switch presetID {
	case "vscode", "cursor":
		return []string{"--remote", "wsl+" + distro, linuxPath}, true
	default:
		return nil, false
	}
}
