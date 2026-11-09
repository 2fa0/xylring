package main

import (
	"context"
	"strings"
	"sync"
	"unsafe"
)

const (
	agentTrailer       = "AI-Agent"
	modelTrailer       = "AI-Model"
	attributionTrailer = "AI-Attribution"
)

type provenance struct{ agent, model, attribution, badge string }
type provKey struct{ agent, model, attribution string }

// The repository owns this bounded pool. A history worker may intern while the
// UI reads a previously delivered page; callers receive values, never slices.
type provenanceTable struct {
	mu     sync.RWMutex
	values []provenance
	index  map[provKey]uint16
}

func (t *provenanceTable) intern(p provenance) uint16 {
	if p.agent == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := provKey{p.agent, p.model, p.attribution}
	if id := t.index[key]; id != 0 {
		return id
	}
	if len(t.values) == 0 {
		t.values = append(t.values, provenance{}, provenance{agent: "其他", badge: "◎ 其他"})
	}
	if len(t.values) >= 65536 {
		return 1
	}
	if t.index == nil {
		t.index = make(map[provKey]uint16)
	}
	p.agent, p.model, p.attribution = strings.Clone(p.agent), strings.Clone(p.model), strings.Clone(p.attribution)
	key = provKey{p.agent, p.model, p.attribution}
	p.badge = "◎ " + p.agent
	if strings.EqualFold(p.agent, "human") {
		p.badge = "人工"
	}
	if p.model != "" {
		p.badge += " · " + normalizeModel(p.model)
	}
	id := uint16(len(t.values))
	t.values = append(t.values, p)
	t.index[key] = id
	return id
}
func (t *provenanceTable) get(id uint16) provenance {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if int(id) < len(t.values) {
		return t.values[id]
	}
	return provenance{}
}
func (t *provenanceTable) snapshot() []provenance {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]provenance(nil), t.values...)
}
func (t *provenanceTable) bytes() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := int64(cap(t.values))*int64(unsafe.Sizeof(provenance{})) + int64(len(t.index))*(int64(unsafe.Sizeof(provKey{}))+32)
	for _, p := range t.values {
		n += int64(len(p.agent) + len(p.model) + len(p.attribution) + len(p.badge))
	}
	return n
}

// Only signatures backed by checked-in real commit samples are recognized.
// In particular a human coauthor's display name is never an identity signal.
func parseProvenance(trailers string) provenance {
	var p, native provenance
	for _, line := range strings.FieldsFunc(trailers, func(r rune) bool { return r == '\n' || r == '\r' || r == '\x1d' }) {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "ai-agent":
			p.agent = value
		case "ai-model":
			p.model = value
		case "ai-attribution":
			p.attribution = value
		case "assisted-by":
			if value == "Cindy (https://cindy.app)" {
				native = provenance{agent: "Cindy", attribution: "native"}
			}
		}
	}
	if p.agent != "" {
		return p
	}
	return native
}
func normalizeModel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "gpt-5":
		return "GPT-5"
	case "gpt-5-codex":
		return "GPT-5 Codex"
	case "claude-opus-4-1", "claude-opus-4-1-20250805":
		return "Claude Opus 4.1"
	case "claude-opus-4-6":
		return "Claude Opus 4.6"
	}
	return raw
}
func (p provenance) description() string {
	if p.agent == "" {
		return ""
	}
	label := p.agent
	if strings.EqualFold(p.agent, "human") {
		label = "人工"
	}
	if p.model != "" {
		label += " · " + normalizeModel(p.model)
	}
	source := "显式"
	switch p.attribution {
	case "native":
		source = "原生签名"
	case "stamp":
		source = "印章"
	case "inferred":
		source = "推断"
	case "":
	default:
		source = p.attribution
	}
	return label + "（" + source + "）"
}
func (q historyQuery) matchesProvenance(p provenance) bool {
	if p.agent == "" {
		return false
	}
	value := p.agent
	if q.kind == "模型" {
		value = normalizeModel(p.model)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(q.term))
}

// Probe the actual pretty-format capability, once per executable lifetime.
// Old Git prints the unsupported placeholder literally. Empty repositories
// fall back until a subsequent stream can probe a real commit.
var trailerCapability struct {
	sync.Mutex
	known, supported bool
}

func historyCommitFormat(ctx context.Context, dir string) (string, bool) {
	trailerCapability.Lock()
	defer trailerCapability.Unlock()
	if !trailerCapability.known {
		out, err := runGit(ctx, dir, "log", "-1", "--format="+provenanceFormat, "HEAD", "--")
		if err == nil {
			trailerCapability.known = true
			trailerCapability.supported = !strings.Contains(string(out), "%(trailers:")
		}
	}
	if trailerCapability.supported {
		return commitFormat + "%x1f" + provenanceFormat, true
	}
	return commitFormat, false
}

func provenanceReadStatus() string {
	trailerCapability.Lock()
	defer trailerCapability.Unlock()
	if trailerCapability.known && !trailerCapability.supported {
		return "当前 Git 不支持白名单 Trailer 格式，历史年轮展示已降级。请升级 Git。"
	}
	return ""
}
