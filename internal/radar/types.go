package radar

import (
	"strings"
	"time"
)

// SessionState represents the normalized operational state of an Agent session.
type SessionState string

const (
	StateWaiting SessionState = "waiting"
	StateError   SessionState = "error"
	StateReview  SessionState = "review"
	StateRunning SessionState = "running"
	StateIdle    SessionState = "idle"
)

// ParseState normalizes user or hook supplied state strings into a SessionState.
func ParseState(raw string) (SessionState, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "waiting", "wait", "needs_input", "needs-input", "blocked", "prompt", "permission":
		return StateWaiting, true
	case "error", "err", "failed", "fail":
		return StateError, true
	case "review", "done", "ready", "completed", "complete":
		return StateReview, true
	case "running", "run", "working", "active", "busy":
		return StateRunning, true
	case "idle", "stale", "paused":
		return StateIdle, true
	default:
		return "", false
	}
}

// NeedsAttention reports whether a session requires human inspection or action.
func (s SessionState) NeedsAttention() bool {
	switch s {
	case StateWaiting, StateError, StateReview:
		return true
	default:
		return false
	}
}

// Rank orders states by urgency (lower comes first).
func (s SessionState) Rank() int {
	switch s {
	case StateWaiting:
		return 0
	case StateError:
		return 1
	case StateReview:
		return 2
	case StateRunning:
		return 3
	default:
		return 4
	}
}

// SessionSource identifies where a session signal originated.
type SessionSource string

const (
	SourcePulse    SessionSource = "pulse"
	SourceCodex    SessionSource = "codex"
	SourceTmux     SessionSource = "tmux"
	SourceWorktree SessionSource = "worktree"
)

func (src SessionSource) priority() int {
	switch src {
	case SourcePulse:
		return 4
	case SourceCodex:
		return 3
	case SourceTmux:
		return 2
	case SourceWorktree:
		return 1
	default:
		return 0
	}
}

// FocusTarget describes how to jump back to the terminal pane or IDE window running a session.
type FocusTarget struct {
	Kind        string `json:"kind,omitempty"`         // "tmux", "app", "path"
	TmuxPane    string `json:"tmux_pane,omitempty"`    // e.g. "%12"
	TmuxTarget  string `json:"tmux_target,omitempty"`  // e.g. "work:2.1"
	TerminalApp string `json:"terminal_app,omitempty"` // e.g. "Ghostty", "iTerm2"
	AppName     string `json:"app_name,omitempty"`     // e.g. "Antigravity", "Cursor"
}

// Session represents a unified Agent or worktree activity item shown in Agent Radar.
type Session struct {
	ID           string        `json:"id"`
	Source       SessionSource `json:"source"`
	State        SessionState  `json:"state"`
	Agent        string        `json:"agent"`
	Model        string        `json:"model,omitempty"`
	Repo         string        `json:"repo,omitempty"`
	Branch       string        `json:"branch,omitempty"`
	Summary      string        `json:"summary,omitempty"`
	ChangedFiles int           `json:"changed_files,omitempty"`
	DirtyFiles   []string      `json:"dirty_files,omitempty"`
	OverlapWith  []string      `json:"overlap_with,omitempty"`
	OverlapFiles []string      `json:"overlap_files,omitempty"`
	Target       FocusTarget   `json:"target,omitempty"`
	StartedAt    time.Time     `json:"started_at,omitempty"`
	UpdatedAt    time.Time     `json:"updated_at"`
	ExpiresAt    time.Time     `json:"expires_at,omitempty"`
}

// PulseInput describes a deterministic pulse event written via `xylring pulse`.
type PulseInput struct {
	ID      string
	State   SessionState
	Agent   string
	Model   string
	Repo    string
	Branch  string
	Summary string
	TTL     time.Duration
	Target  FocusTarget
	Now     time.Time
}

// WorktreeHint supplies repository/worktree state gathered by Xylring.
type WorktreeHint struct {
	Path       string
	CommonDir  string
	Branch     string
	DirtyFiles []string
	UpdatedAt  time.Time
}

// ScanOptions configures multi-source session aggregation.
type ScanOptions struct {
	Now           time.Time
	CodexHome     string
	KnownRepos    []string
	Worktrees     []WorktreeHint
	CanonicalPath func(string) string
	SkipTmux      bool
	SkipCodex     bool
}

// Snapshot is the aggregated, sorted view produced for the UI or CLI.
type Snapshot struct {
	Sessions       []Session `json:"sessions"`
	AttentionCount int       `json:"attention_count"`
	RunningCount   int       `json:"running_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}
