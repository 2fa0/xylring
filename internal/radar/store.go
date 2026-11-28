package radar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// EnvDirKey overrides the storage directory for radar pulses and dismissed state.
	EnvDirKey       = "XYLRING_RADAR_DIR"
	defaultPulseTTL = 4 * time.Hour
)

// HomeDir is overridable in tests.
var HomeDir = os.UserHomeDir

// DataDir returns the root directory for radar state files.
func DataDir() (string, error) {
	if env := strings.TrimSpace(os.Getenv(EnvDirKey)); env != "" {
		return filepath.Clean(env), nil
	}
	home, err := HomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "xylring", "radar"), nil
}

func sessionsDir() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions"), nil
}

func dismissedPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dismissed.json"), nil
}

func sessionFileName(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8]) + ".json"
}

func atomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// DetectEnvTarget captures deterministic terminal/IDE focus metadata from standard env vars.
func DetectEnvTarget(getenv func(string) string) FocusTarget {
	if getenv == nil {
		return FocusTarget{}
	}
	termApp := normalizeTerminalApp(getenv("TERM_PROGRAM"))
	if pane := strings.TrimSpace(getenv("TMUX_PANE")); pane != "" {
		return FocusTarget{
			Kind:        "tmux",
			TmuxPane:    pane,
			TerminalApp: termApp,
		}
	}
	if app := detectIDEAppFromEnv(getenv); app != "" {
		return FocusTarget{
			Kind:        "app",
			AppName:     app,
			TerminalApp: termApp,
		}
	}
	if termApp != "" {
		return FocusTarget{
			Kind:        "app",
			AppName:     termApp,
			TerminalApp: termApp,
		}
	}
	return FocusTarget{}
}

func normalizeTerminalApp(termProg string) string {
	raw := strings.TrimSpace(termProg)
	switch strings.ToLower(raw) {
	case "":
		return ""
	case "iterm.app", "iterm2":
		return "iTerm2"
	case "apple_terminal", "terminal.app":
		return "Terminal"
	case "ghostty":
		return "Ghostty"
	case "warpterminal", "warp":
		return "Warp"
	case "wezterm":
		return "WezTerm"
	case "vscode":
		return "Visual Studio Code"
	default:
		return raw
	}
}

func detectIDEAppFromEnv(getenv func(string) string) string {
	switch {
	case getenv("ANTIGRAVITY_AGENT") != "" || getenv("ANTIGRAVITY_CLI_ALIAS") != "" || strings.EqualFold(getenv("TERM_PROGRAM"), "antigravity"):
		return "Antigravity"
	case getenv("CURSOR_TRACE_ID") != "" || getenv("CURSOR_CLI") != "" || strings.EqualFold(getenv("TERM_PROGRAM"), "cursor"):
		return "Cursor"
	case getenv("VSCODE_IPC_HOOK_CLI") != "" || getenv("VSCODE_GIT_IPC_HANDLE") != "" || strings.EqualFold(getenv("TERM_PROGRAM"), "vscode"):
		return "Visual Studio Code"
	default:
		return ""
	}
}

func stablePulseID(in PulseInput, target FocusTarget) string {
	agent := strings.ToLower(strings.TrimSpace(in.Agent))
	if agent == "" {
		agent = "agent"
	}
	repo := strings.TrimSpace(in.Repo)
	if repo != "" {
		repo = filepath.Clean(repo)
	}
	key := agent + "|" + repo + "|" + target.TmuxPane + "|" + target.AppName
	sum := sha256.Sum256([]byte(key))
	return "pulse-" + hex.EncodeToString(sum[:6])
}

// WritePulse persists a session pulse event atomically with TTL.
func WritePulse(in PulseInput) (Session, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = defaultPulseTTL
	}
	state := in.State
	if state == "" {
		state = StateRunning
	}
	agent := strings.TrimSpace(in.Agent)
	if agent == "" {
		agent = "Agent"
	}
	repo := strings.TrimSpace(in.Repo)
	if repo != "" {
		repo = filepath.Clean(repo)
	}
	target := in.Target
	if target.Kind == "" {
		target = DetectEnvTarget(os.Getenv)
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = stablePulseID(in, target)
	}

	dir, err := sessionsDir()
	if err != nil {
		return Session{}, err
	}
	filePath := filepath.Join(dir, sessionFileName(id))

	startedAt := now
	if raw, rerr := os.ReadFile(filePath); rerr == nil {
		var prev Session
		if json.Unmarshal(raw, &prev) == nil && !prev.StartedAt.IsZero() {
			startedAt = prev.StartedAt
		}
	}

	s := Session{
		ID:        id,
		Source:    SourcePulse,
		State:     state,
		Agent:     agent,
		Model:     strings.TrimSpace(in.Model),
		Repo:      repo,
		Branch:    strings.TrimSpace(in.Branch),
		Summary:   strings.TrimSpace(in.Summary),
		Target:    target,
		StartedAt: startedAt,
		UpdatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	payload, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return Session{}, err
	}
	if err := atomicWriteFile(filePath, payload); err != nil {
		return Session{}, err
	}
	return s, nil
}

// ClearPulse removes a single pulse file by session ID.
func ClearPulse(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, sessionFileName(id)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ClearAllPulses removes all stored pulse files.
func ClearAllPulses() error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ListPulses reads all non-expired pulse files. Corrupt or unreadable entries are skipped.
func ListPulses(now time.Time) ([]Session, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(raw, &s); err != nil || s.ID == "" {
			continue
		}
		if !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt) {
			continue
		}
		if s.Source == "" {
			s.Source = SourcePulse
		}
		if s.State == "" {
			s.State = StateRunning
		}
		out = append(out, s)
	}
	return out, nil
}

// LoadDismissed returns the map of dismissed session IDs to dismissal timestamps.
func LoadDismissed() map[string]time.Time {
	p, err := dismissedPath()
	if err != nil {
		return map[string]time.Time{}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return map[string]time.Time{}
	}
	var out map[string]time.Time
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]time.Time{}
	}
	return out
}

// DismissSession records that a session was acknowledged at the given time.
func DismissSession(id string, at time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("session id is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	p, err := dismissedPath()
	if err != nil {
		return err
	}
	m := LoadDismissed()
	m[id] = at.UTC()
	// Prune old dismissals older than 7 days to keep the map small.
	cutoff := at.Add(-7 * 24 * time.Hour)
	for k, ts := range m {
		if ts.Before(cutoff) {
			delete(m, k)
		}
	}
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(p, payload)
}

// ClearDismissed removes a session's dismissal record.
func ClearDismissed(id string) error {
	p, err := dismissedPath()
	if err != nil {
		return err
	}
	m := LoadDismissed()
	if _, ok := m[id]; !ok {
		return nil
	}
	delete(m, id)
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(p, payload)
}
