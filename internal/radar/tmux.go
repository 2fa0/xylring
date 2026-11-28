package radar

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	tmuxCmdTimeout  = 1500 * time.Millisecond
	tmuxRunningSpan = 10 * time.Second
	tmuxWaitingSpan = 30 * time.Minute
)

// runTmuxCmd is overridable in unit tests.
var runTmuxCmd = func(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, tmuxCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "tmux", args...)
	out, err := cmd.Output()
	return string(out), err
}

var tmuxAgentPatterns = []struct {
	token string
	label string
}{
	{token: "codex", label: "Codex"},
	{token: "claude", label: "Claude Code"},
	{token: "gemini", label: "Gemini CLI"},
	{token: "opencode", label: "OpenCode"},
	{token: "aider", label: "Aider"},
	{token: "goose", label: "Goose"},
	{token: "amp", label: "Amp"},
	{token: "cline", label: "Cline"},
	{token: "antigravity", label: "Antigravity"},
	{token: "agy", label: "Antigravity"},
	{token: "cursor-agent", label: "Cursor"},
}

func detectTmuxAgent(command, title string) (string, bool) {
	cmdLower := strings.ToLower(strings.TrimSpace(command))
	titleLower := strings.ToLower(strings.TrimSpace(title))
	for _, pat := range tmuxAgentPatterns {
		if matchesAgentToken(cmdLower, pat.token) || matchesAgentToken(titleLower, pat.token) {
			return pat.label, true
		}
	}
	return "", false
}

func matchesAgentToken(haystack, token string) bool {
	if haystack == "" {
		return false
	}
	if haystack == token {
		return true
	}
	for _, field := range strings.FieldsFunc(haystack, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '/' || r == ':' || r == '|' || r == '-' && token != "cursor-agent"
	}) {
		if field == token {
			return true
		}
	}
	return strings.Contains(haystack, token)
}

// CollectTmuxSessions inspects live tmux panes to detect running or waiting CLI agents.
func CollectTmuxSessions(ctx context.Context, knownRepos []string, now time.Time) []Session {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	format := "#{pane_id}\t#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_title}\t#{pane_activity}\t#{pane_dead}\t#{pane_dead_status}"
	out, err := runTmuxCmd(ctx, "list-panes", "-a", "-F", format)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	var sessions []Session
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}
		paneID := strings.TrimSpace(fields[0])
		paneTarget := strings.TrimSpace(fields[1])
		cmdName := strings.TrimSpace(fields[2])
		panePath := strings.TrimSpace(fields[3])
		paneTitle := strings.TrimSpace(fields[4])
		actRaw := strings.TrimSpace(fields[5])
		deadRaw := strings.TrimSpace(fields[6])
		deadStatus := strings.TrimSpace(fields[7])

		agent, ok := detectTmuxAgent(cmdName, paneTitle)
		if !ok {
			continue
		}
		var actAt time.Time
		if sec, perr := strconv.ParseInt(actRaw, 10, 64); perr == nil && sec > 0 {
			actAt = time.Unix(sec, 0).UTC()
		} else {
			actAt = now.UTC()
		}

		state := StateIdle
		switch {
		case deadRaw == "1" && deadStatus != "" && deadStatus != "0":
			state = StateError
		case now.Sub(actAt) <= tmuxRunningSpan:
			state = StateRunning
		case now.Sub(actAt) <= tmuxWaitingSpan:
			state = StateWaiting
		default:
			state = StateIdle
		}

		repo := ""
		if panePath != "" {
			repo = matchKnownOrCleanPath(panePath, knownRepos)
		}
		summary := "tmux " + paneTarget + " · " + strings.ToLower(agent)
		if cmdName != "" && !strings.EqualFold(cmdName, agent) {
			summary = "tmux " + paneTarget + " · " + cmdName
		}

		sessions = append(sessions, Session{
			ID:        "tmux-" + paneID,
			Source:    SourceTmux,
			State:     state,
			Agent:     agent,
			Repo:      repo,
			Summary:   summary,
			UpdatedAt: actAt,
			Target: FocusTarget{
				Kind:       "tmux",
				TmuxPane:   paneID,
				TmuxTarget: paneTarget,
			},
		})
	}
	return sessions
}

func matchKnownOrCleanPath(panePath string, knownRepos []string) string {
	cleanPane := filepath.Clean(panePath)
	best := ""
	for _, r := range knownRepos {
		cr := filepath.Clean(r)
		if cr == "" {
			continue
		}
		if cleanPane == cr || strings.HasPrefix(cleanPane, cr+string(filepath.Separator)) {
			if len(cr) > len(best) {
				best = cr
			}
		}
	}
	if best != "" {
		return best
	}
	return cleanPane
}
