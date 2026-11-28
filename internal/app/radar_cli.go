package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2fa0/xylring/internal/radar"
)

func runPulseCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			printPulseCLIHelp(out)
			return nil
		case "list":
			return runPulseListCLI(ctx, args[1:], out)
		case "clear":
			return runPulseClearCLI(args[1:])
		case "hook":
			return runPulseHookCLI(args[1:], out)
		}
	}

	parsed, err := parseBoardFlags(args, map[string]bool{
		"session":           true,
		"id":                true,
		"repo":              true,
		"agent":             true,
		"model":             true,
		"state":             true,
		"summary":           true,
		"ttl":               true,
		"app":               true,
		"branch":            true,
		"from-codex-notify": true,
	}, map[string]bool{
		"json": true,
	})
	if err != nil {
		return err
	}

	var in radar.PulseInput
	if payloadArg, ok := parsed.lastValue("from-codex-notify"); ok {
		raw := []byte(payloadArg)
		if strings.TrimSpace(payloadArg) == "-" {
			b, rerr := io.ReadAll(os.Stdin)
			if rerr != nil {
				return rerr
			}
			raw = b
		}
		in, err = radar.ParseCodexNotifyPayload(raw)
		if err != nil {
			return fmt.Errorf("解析 Codex notify 载荷失败: %w", err)
		}
	}

	if v, ok := parsed.lastValue("session"); ok {
		in.ID = v
	} else if v, ok := parsed.lastValue("id"); ok {
		in.ID = v
	}
	if v, ok := parsed.lastValue("agent"); ok {
		in.Agent = v
	}
	if v, ok := parsed.lastValue("model"); ok {
		in.Model = v
	}
	if v, ok := parsed.lastValue("summary"); ok {
		in.Summary = v
	}
	if v, ok := parsed.lastValue("branch"); ok {
		in.Branch = v
	}
	if v, ok := parsed.lastValue("state"); ok {
		st, valid := radar.ParseState(v)
		if !valid {
			return fmt.Errorf("无效状态: %s（可选: running, waiting, review, error, idle）", v)
		}
		in.State = st
	}
	if v, ok := parsed.lastValue("ttl"); ok {
		d, derr := time.ParseDuration(strings.TrimSpace(v))
		if derr != nil || d <= 0 {
			return fmt.Errorf("无效 TTL: %s", v)
		}
		in.TTL = d
	}
	if v, ok := parsed.lastValue("app"); ok && strings.TrimSpace(v) != "" {
		in.Target = radar.FocusTarget{Kind: "app", AppName: strings.TrimSpace(v)}
	}

	repoArg, hasRepo := parsed.lastValue("repo")
	if hasRepo {
		in.Repo = resolvePulseRepo(ctx, dir, repoArg)
	} else if in.Repo == "" && dir != "" {
		in.Repo = resolvePulseRepo(ctx, dir, dir)
	} else if in.Repo != "" {
		in.Repo = resolvePulseRepo(ctx, dir, in.Repo)
	}
	if in.Branch == "" && in.Repo != "" {
		if bOut, bErr := runGit(ctx, in.Repo, "symbolic-ref", "-q", "--short", "HEAD"); bErr == nil {
			in.Branch = strings.TrimSpace(string(bOut))
		}
	}

	sess, err := radar.WritePulse(in)
	if err != nil {
		return err
	}
	if parsed.bools["json"] {
		return json.NewEncoder(out).Encode(sess)
	}
	fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", sess.ID, sess.State, sess.Agent, sess.Repo)
	return nil
}

func resolvePulseRepo(ctx context.Context, baseDir, raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && baseDir != "" {
		p = filepath.Join(baseDir, p)
	}
	p = filepath.Clean(p)
	if root, err := repoRoot(ctx, p); err == nil && root != "" {
		return canonicalRepoDir(root)
	}
	return p
}

func runPulseListCLI(ctx context.Context, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args, nil, map[string]bool{
		"json":    true,
		"no-tmux": true,
	})
	if err != nil {
		return err
	}
	snap := radar.CollectSnapshot(ctx, radar.ScanOptions{
		CanonicalPath: canonicalRepoDir,
		SkipTmux:      parsed.bools["no-tmux"],
	})
	if parsed.bools["json"] {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}
	if len(snap.Sessions) == 0 {
		fmt.Fprintln(out, "暂无活跃的 Agent 会话")
		return nil
	}
	for _, s := range snap.Sessions {
		repoLabel := s.Repo
		if repoLabel != "" {
			repoLabel = filepath.Base(repoLabel)
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", s.ID, s.State, s.Agent, repoLabel, s.Summary)
	}
	return nil
}

func runPulseClearCLI(args []string) error {
	parsed, err := parseBoardFlags(args, nil, map[string]bool{"all": true})
	if err != nil {
		return err
	}
	if parsed.bools["all"] {
		return radar.ClearAllPulses()
	}
	if len(parsed.positional) == 0 {
		return errors.New("请指定会话 ID 或使用 --all")
	}
	return radar.ClearPulse(parsed.positional[0])
}

func runPulseHookCLI(args []string, out io.Writer) error {
	kind := "codex"
	if len(args) > 0 {
		kind = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch kind {
	case "codex":
		fmt.Fprintln(out, radar.CodexNotifyConfig())
	case "claude":
		fmt.Fprintln(out, radar.ClaudeHooksConfig())
	case "shell", "gemini":
		fmt.Fprintln(out, radar.GenericShellConfig())
	default:
		return fmt.Errorf("未知 Hook 类型: %s（可选: codex, claude, shell）", kind)
	}
	return nil
}

func printPulseCLIHelp(out io.Writer) {
	fmt.Fprint(out, `Xylring Agent 雷达命令行用法:
  xylring pulse [--agent NAME] [--state running|waiting|review|error] [--repo PATH] [--summary TEXT] [--session ID] [--model NAME] [--ttl 4h] [--app NAME] [--json]
  xylring pulse --from-codex-notify '<json>'
  xylring pulse list [--json] [--no-tmux]
  xylring pulse clear <id> | --all
  xylring pulse hook [codex|claude|shell]
`)
}
