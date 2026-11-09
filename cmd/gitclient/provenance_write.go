package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func applyProvenanceTrailers(ctx context.Context, dir, msg string, p provenance) (string, error) {
	for _, s := range []string{p.agent, p.model, p.attribution} {
		if strings.ContainsAny(s, "\r\n\x00") {
			return "", errors.New("年轮字段不能包含换行")
		}
	}
	if p.attribution == "native" {
		p.attribution = ""
	}
	if p.agent == "" {
		p = provenance{}
	}
	// Git determines trailer boundaries; strip duplicate AI keys in separate
	// passes because replace only replaces the last occurrence of each key.
	parsed, err := runGitInput(ctx, dir, msg, "interpret-trailers", "--parse")
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	for _, line := range strings.Split(string(parsed), "\n") {
		key, _, _ := strings.Cut(line, ":")
		counts[strings.ToLower(key)]++
	}
	duplicates := max(counts["ai-agent"], counts["ai-model"], counts["ai-attribution"])
	for i := 1; i < duplicates; i++ {
		clean, err := runGitInput(ctx, dir, msg, "interpret-trailers", "--trim-empty", "--if-exists", "replace", "--if-missing", "doNothing", "--trailer", agentTrailer+":", "--trailer", modelTrailer+":", "--trailer", attributionTrailer+":")
		if err != nil {
			return "", err
		}
		msg = string(clean)
	}

	args := []string{"interpret-trailers", "--if-exists", "replace", "--if-missing", "add", "--trim-empty"}
	for _, pair := range [][2]string{{agentTrailer, p.agent}, {modelTrailer, p.model}, {attributionTrailer, p.attribution}} {
		args = append(args, "--trailer", pair[0]+": "+pair[1])
	}
	out, err := runGitInput(ctx, dir, msg, args...)
	if err != nil {
		return "", fmt.Errorf("合成年轮信息失败：%w", err)
	}
	return string(out), nil
}

type ringStamp struct {
	p       provenance
	expires int64
	once    bool
}

func gitFile(ctx context.Context, dir, name string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return path, nil
}
func readStamp(ctx context.Context, dir string) (ringStamp, error) {
	var s ringStamp
	path, err := gitFile(ctx, dir, "xylring-stamp")
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, _ := strings.Cut(line, "=")
		switch k {
		case "agent":
			s.p.agent = v
		case "model":
			s.p.model = v
		case "expires":
			s.expires, _ = strconv.ParseInt(v, 10, 64)
		case "once":
			s.once = v == "1"
		}
	}
	if s.expires <= time.Now().Unix() {
		return ringStamp{}, nil
	}
	s.p.attribution = "stamp"
	return s, nil
}
func writeStamp(ctx context.Context, dir string, s ringStamp) error {
	if strings.TrimSpace(s.p.agent) == "" || strings.ContainsAny(s.p.agent+s.p.model, "\r\n\x00") {
		return errors.New("请填写有效 Agent，字段不能包含换行")
	}
	path, err := gitFile(ctx, dir, "xylring-stamp")
	if err != nil {
		return err
	}
	once := 0
	if s.once {
		once = 1
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".xylring-stamp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = fmt.Fprintf(f, "agent=%s\nmodel=%s\nexpires=%d\nonce=%d\n", s.p.agent, s.p.model, s.expires, once)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func clearStamp(ctx context.Context, dir string) error {
	path, err := gitFile(ctx, dir, "xylring-stamp")
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

const ringHookMarker = "# xylring-ring-hook v1"

// Custom hooksPath directories always require manual integration. This also
// avoids silently changing a shared or externally managed hook directory.
func ringHookPath(ctx context.Context, dir string) (string, error) {
	out, _ := runGit(ctx, dir, "config", "--get", "core.hooksPath")
	if strings.TrimSpace(string(out)) != "" {
		return "", errors.New("需手动集成：已设置 core.hooksPath，请在现有 Hook 中调用年轮脚本")
	}
	return gitFile(ctx, dir, "hooks/prepare-commit-msg")
}
func ringHookStatus(ctx context.Context, dir string) string {
	path, err := ringHookPath(ctx, dir)
	if err != nil {
		return err.Error()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "未开启"
	}
	if strings.Contains(string(b), ringHookMarker) {
		return "已开启（v1）"
	}
	if strings.Contains(string(b), "# xylring-ring-hook") {
		return "版本过旧"
	}
	return "未开启"
}
func installRingHook(ctx context.Context, dir string) error {
	path, err := ringHookPath(ctx, dir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	existed := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(b), ringHookMarker) {
		return nil
	}
	if _, err := os.Lstat(path + ".xylring-orig"); err == nil {
		return errors.New("备份 Hook 已存在，请先检查 prepare-commit-msg.xylring-orig")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if existed {
		if err := os.Rename(path, path+".xylring-orig"); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, []byte(ringHookScript()), 0755); err != nil {
		if existed {
			_ = os.Rename(path+".xylring-orig", path)
		}
		return err
	}
	return nil
}
func uninstallRingHook(ctx context.Context, dir string) error {
	path, err := ringHookPath(ctx, dir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), "# xylring-ring-hook") {
		return errors.New("现有 Hook 不属于 Xylring，未修改")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path + ".xylring-orig"); err == nil {
		return os.Rename(path+".xylring-orig", path)
	}
	return nil
}
func ringHookScript() string {
	return `#!/bin/sh
# xylring-ring-hook v1
orig="$0.xylring-orig"
if [ -x "$orig" ]; then
    "$orig" "$@" || exit $?
fi
# Attribution failures must never prevent a commit.
(
    [ "${XYLRING_GUI:-}" != 1 ] || exit 0
    [ "${XYLRING_RING:-}" != 0 ] || exit 0
    case "${2:-}" in ''|message|template) ;; *) exit 0 ;; esac
    trailers=$(git interpret-trailers --parse "$1") || exit 0
    if printf '%s\n' "$trailers" | grep -Eiq '^(AI-Agent:|Assisted-by: Cindy \(https://cindy.app\))'; then exit 0; fi
    stamp=$(git rev-parse --git-path xylring-stamp) || exit 0
    [ -f "$stamp" ] || exit 0
    agent= model= expires= once=0
    while IFS= read -r line; do
        case "$line" in agent=*) agent=${line#agent=} ;; model=*) model=${line#model=} ;; expires=*) expires=${line#expires=} ;; once=*) once=${line#once=} ;; esac
    done < "$stamp"
    case "$expires" in ''|*[!0-9]*) exit 0 ;; esac
    now=$(date +%s) || exit 0
    [ "$expires" -gt "$now" ] && [ -n "$agent" ] || exit 0
    if [ -n "$model" ]; then
        git interpret-trailers --in-place --if-exists replace --trailer "AI-Agent: $agent" --trailer "AI-Model: $model" --trailer 'AI-Attribution: stamp' "$1" || exit 0
    else
        git interpret-trailers --in-place --if-exists replace --trailer "AI-Agent: $agent" --trailer 'AI-Attribution: stamp' "$1" || exit 0
    fi
    [ "$once" != 1 ] || rm -f "$stamp"
) 2>/dev/null
exit 0
`
}
func provenanceRuleSnippet() string {
	return `<!-- xylring:provenance:start v1 -->
## Commit provenance
When you run git commit, append these Git trailers after a blank line:

AI-Agent: <your agent/product name>
AI-Model: <your exact model name>

Only include AI-Model if you are certain of your exact model name; never guess.
Do not add these trailers to commits the user authored without your help.
If you ran xylring stamp during this task, run xylring stamp --clear when finished.
<!-- xylring:provenance:end -->
`
}

func runGitInput(ctx context.Context, dir, input string, args ...string) ([]byte, error) {
	if err := acquireGit(ctx); err != nil {
		return nil, err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, gitArgs(args...)...)
	hideConsoleWindow(cmd)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(input)
	cmd.WaitDelay = 2 * time.Second
	return cmd.Output()
}
func messageProvenance(ctx context.Context, dir, msg string) (provenance, error) {
	out, err := runGitInput(ctx, dir, msg, "interpret-trailers", "--parse")
	if err != nil {
		return provenance{}, err
	}
	return parseProvenance(string(out)), nil
}
