package radar

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// CodexNotifyConfig returns the snippet to add to ~/.codex/config.toml.
func CodexNotifyConfig() string {
	return `# ~/.codex/config.toml
notify = ["xylring", "pulse", "--from-codex-notify"]`
}

// ClaudeHooksConfig returns a deterministic ~/.claude/settings.json hooks snippet.
func ClaudeHooksConfig() string {
	return `{
  "hooks": {
    "UserPromptSubmit": [{
      "hooks": [{ "type": "command", "command": "xylring pulse --agent 'Claude Code' --state running" }]
    }],
    "Notification": [{
      "hooks": [{ "type": "command", "command": "xylring pulse --agent 'Claude Code' --state waiting --summary 'Needs confirmation'" }]
    }],
    "Stop": [{
      "hooks": [{ "type": "command", "command": "xylring pulse --agent 'Claude Code' --state review --summary 'Turn completed'" }]
    }]
  }
}`
}

// GenericShellConfig returns shell commands for Gemini CLI, OpenCode, or custom wrappers.
func GenericShellConfig() string {
	return `# Start or update a session
xylring pulse --agent "Gemini CLI" --state running --repo ~/projects/demo-app

# Mark waiting for input or ready for review
xylring pulse --agent "Gemini CLI" --state waiting --summary "Approve migration?"
xylring pulse --agent "Gemini CLI" --state review  --summary "Updated auth handler"`
}

type codexNotifyPayload struct {
	Type                 string   `json:"type"`
	ThreadID             string   `json:"thread-id"`
	SessionID            string   `json:"session-id"`
	Cwd                  string   `json:"cwd"`
	Model                string   `json:"model"`
	LastAssistantMessage string   `json:"last-assistant-message"`
	InputMessages        []string `json:"input-messages"`
}

// ParseCodexNotifyPayload converts Codex's `notify` JSON payload into a PulseInput.
func ParseCodexNotifyPayload(raw []byte) (PulseInput, error) {
	var p codexNotifyPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return PulseInput{}, err
	}
	id := strings.TrimSpace(p.ThreadID)
	if id == "" {
		id = strings.TrimSpace(p.SessionID)
	}
	if id != "" && !strings.HasPrefix(id, "codex-") {
		id = "codex-" + id
	}
	state := StateReview
	msg := strings.TrimSpace(p.LastAssistantMessage)
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "agent-turn-complete", "":
		if endsWithQuestion(msg) {
			state = StateWaiting
		} else {
			state = StateReview
		}
	case "error", "turn-aborted":
		state = StateError
	default:
		if parsed, ok := ParseState(p.Type); ok {
			state = parsed
		}
	}
	summary := truncateSummary(msg, 120)
	if summary == "" && len(p.InputMessages) > 0 {
		summary = truncateSummary(p.InputMessages[len(p.InputMessages)-1], 120)
	}
	repo := strings.TrimSpace(p.Cwd)
	if repo != "" {
		repo = filepath.Clean(repo)
	}
	return PulseInput{
		ID:      id,
		State:   state,
		Agent:   "Codex",
		Model:   strings.TrimSpace(p.Model),
		Repo:    repo,
		Summary: summary,
	}, nil
}
