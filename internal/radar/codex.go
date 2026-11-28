package radar

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	codexLookback   = 6 * time.Hour
	codexMaxFiles   = 20
	codexHeadBytes  = 16 * 1024
	codexTailBytes  = 32 * 1024
	codexActiveSpan = 12 * time.Second
	codexIdleSpan   = 2 * time.Hour
)

type codexCandidate struct {
	path    string
	modTime time.Time
}

// CollectCodexSessions passively inspects recent Codex session JSONL files without
// requiring any cooperation from the LLM.
func CollectCodexSessions(ctx context.Context, codexHome string, now time.Time) []Session {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(codexHome) == "" {
		home, err := HomeDir()
		if err != nil || home == "" {
			return nil
		}
		codexHome = filepath.Join(home, ".codex")
	}
	sessionsRoot := filepath.Join(codexHome, "sessions")
	info, err := os.Stat(sessionsRoot)
	if err != nil || !info.IsDir() {
		return nil
	}

	cutoff := now.Add(-codexLookback)
	var candidates []codexCandidate
	_ = filepath.WalkDir(sessionsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			return nil
		}
		if fi.ModTime().Before(cutoff) {
			return nil
		}
		candidates = append(candidates, codexCandidate{path: path, modTime: fi.ModTime()})
		return nil
	})
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	if len(candidates) > codexMaxFiles {
		candidates = candidates[:codexMaxFiles]
	}

	var out []Session
	for _, cand := range candidates {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if s, ok := parseCodexSessionFile(cand.path, cand.modTime, now); ok {
			out = append(out, s)
		}
	}
	return out
}

func parseCodexSessionFile(path string, modTime, now time.Time) (Session, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return Session{}, false
	}
	size := stat.Size()
	if size == 0 {
		return Session{}, false
	}

	headLen := min(size, int64(codexHeadBytes))
	headBuf := make([]byte, headLen)
	if _, err := io.ReadFull(f, headBuf); err != nil && err != io.ErrUnexpectedEOF {
		return Session{}, false
	}

	var tailBuf []byte
	if size <= codexTailBytes {
		if size > headLen {
			all := make([]byte, size)
			copy(all, headBuf)
			if _, err := io.ReadFull(f, all[headLen:]); err == nil {
				tailBuf = all
			} else {
				tailBuf = headBuf
			}
		} else {
			tailBuf = headBuf
		}
	} else {
		offset := size - codexTailBytes
		if _, err := f.Seek(offset, io.SeekStart); err == nil {
			raw := make([]byte, codexTailBytes)
			if n, rerr := io.ReadFull(f, raw); rerr == nil || rerr == io.ErrUnexpectedEOF {
				raw = raw[:n]
				// Drop partial first line after mid-file seek.
				if nl := bytes.IndexByte(raw, '\n'); nl >= 0 && nl+1 < len(raw) {
					tailBuf = raw[nl+1:]
				} else {
					tailBuf = raw
				}
			}
		}
	}

	var sessionID, cwd, model string
	var startedAt time.Time
	for _, line := range bytes.Split(headBuf, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec codexLine
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if rec.Type == "session_meta" && len(rec.Payload) > 0 {
			var meta codexSessionMeta
			if json.Unmarshal(rec.Payload, &meta) == nil {
				if meta.ID != "" {
					sessionID = meta.ID
				}
				if meta.Cwd != "" {
					cwd = filepath.Clean(meta.Cwd)
				}
				if meta.Model != "" {
					model = meta.Model
				} else if meta.ModelProvider != "" {
					model = meta.ModelProvider
				}
				if ts, ok := parseCodexTime(meta.Timestamp); ok {
					startedAt = ts
				}
			}
			break
		}
	}

	if sessionID == "" {
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if base == "" {
			return Session{}, false
		}
		sessionID = "codex-" + base
	} else {
		sessionID = "codex-" + sessionID
	}

	var lastMsg string
	var lastEventType string
	var hasError bool
	var needsApproval bool
	var lastTS time.Time

	for _, line := range bytes.Split(tailBuf, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec codexLine
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if ts, ok := parseCodexTime(rec.Timestamp); ok {
			lastTS = ts
		}
		switch rec.Type {
		case "event_msg":
			var ev codexEventPayload
			if json.Unmarshal(rec.Payload, &ev) == nil {
				lastEventType = ev.Type
				switch ev.Type {
				case "agent_message":
					if text := strings.TrimSpace(ev.Message); text != "" {
						lastMsg = text
						hasError = false
						needsApproval = endsWithQuestion(text)
					}
				case "user_message":
					hasError = false
					needsApproval = false
				case "turn_aborted", "error":
					hasError = true
					if ev.Message != "" {
						lastMsg = ev.Message
					} else if ev.Reason != "" {
						lastMsg = ev.Reason
					}
				case "exec_approval_request", "apply_patch_approval_request", "request_user_input":
					needsApproval = true
					if ev.Reason != "" {
						lastMsg = ev.Reason
					}
				}
			}
		case "response_item":
			var item codexResponseItem
			if json.Unmarshal(rec.Payload, &item) == nil && item.Type == "message" && item.Role == "assistant" {
				if text := item.extractText(); text != "" {
					lastMsg = text
					hasError = false
					needsApproval = endsWithQuestion(text)
				}
			}
		case "turn_context":
			var tc struct {
				Cwd   string `json:"cwd"`
				Model string `json:"model"`
			}
			if json.Unmarshal(rec.Payload, &tc) == nil {
				if cwd == "" && tc.Cwd != "" {
					cwd = filepath.Clean(tc.Cwd)
				}
				if tc.Model != "" {
					model = tc.Model
				}
			}
		}
	}

	updatedAt := modTime.UTC()
	if !lastTS.IsZero() && lastTS.After(updatedAt) {
		updatedAt = lastTS.UTC()
	}

	age := now.Sub(modTime)
	state := StateReview
	switch {
	case age <= codexActiveSpan:
		if needsApproval {
			state = StateWaiting
		} else {
			state = StateRunning
		}
	case hasError || lastEventType == "turn_aborted":
		state = StateError
	case needsApproval:
		state = StateWaiting
	case age > codexIdleSpan:
		state = StateIdle
	default:
		state = StateReview
	}

	return Session{
		ID:        sessionID,
		Source:    SourceCodex,
		State:     state,
		Agent:     "Codex",
		Model:     model,
		Repo:      cwd,
		Summary:   truncateSummary(lastMsg, 120),
		StartedAt: startedAt,
		UpdatedAt: updatedAt,
	}, true
}

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	Model         string `json:"model"`
	ModelProvider string `json:"model_provider"`
}

type codexEventPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Reason  string `json:"reason"`
}

type codexResponseItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (item codexResponseItem) extractText() string {
	for _, part := range item.Content {
		if txt := strings.TrimSpace(part.Text); txt != "" {
			return txt
		}
	}
	return ""
}

func parseCodexTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func endsWithQuestion(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasSuffix(trimmed, "?") || strings.HasSuffix(trimmed, "？")
}

func truncateSummary(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if idx := strings.IndexAny(s, "\r\n"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}
	runes := []rune(s)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return s
}
