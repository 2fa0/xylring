package radar

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// FocusSession brings the terminal pane, IDE window, or workspace directory of a session to the front.
func FocusSession(ctx context.Context, s Session, openEditorFn func(string) error, openTerminalFn func(string) error) error {
	target := s.Target
	switch target.Kind {
	case "tmux":
		if target.TmuxPane != "" {
			if err := focusTmuxPane(ctx, target); err != nil {
				return err
			}
			if runtime.GOOS == "darwin" {
				activateTerminalApp(ctx, target.TerminalApp)
			}
			return nil
		}
	case "app":
		if runtime.GOOS == "darwin" && target.AppName != "" {
			args := []string{"-a", target.AppName}
			if s.Repo != "" {
				args = append(args, s.Repo)
			}
			return exec.CommandContext(ctx, "open", args...).Run()
		}
	}
	if s.Repo != "" {
		if openEditorFn != nil {
			return openEditorFn(s.Repo)
		}
		if openTerminalFn != nil {
			return openTerminalFn(s.Repo)
		}
	}
	return errors.New("no focus target available for this session")
}

func focusTmuxPane(ctx context.Context, target FocusTarget) error {
	pane := target.TmuxPane
	if _, err := runTmuxCmd(ctx, "switch-client", "-t", pane); err == nil {
		return nil
	}
	if _, err := runTmuxCmd(ctx, "select-window", "-t", pane); err != nil {
		return err
	}
	_, err := runTmuxCmd(ctx, "select-pane", "-t", pane)
	return err
}

func activateTerminalApp(ctx context.Context, preferred string) {
	candidates := []string{}
	if strings.TrimSpace(preferred) != "" {
		candidates = append(candidates, preferred)
	}
	candidates = append(candidates, "Ghostty", "iTerm2", "Warp", "WezTerm", "Terminal")
	for _, appName := range candidates {
		if preferred != "" && appName == preferred {
			if exec.CommandContext(ctx, "open", "-a", appName).Run() == nil {
				return
			}
			continue
		}
		if exec.CommandContext(ctx, "pgrep", "-ix", appName).Run() == nil {
			_ = exec.CommandContext(ctx, "open", "-a", appName).Run()
			return
		}
	}
}
