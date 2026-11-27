package provenance

import (
	"errors"
	"strings"
	"sync"
	"unsafe"

	"github.com/2fa0/xylring/internal/i18n"
)

const (
	AgentTrailer       = "AI-Agent"
	ModelTrailer       = "AI-Model"
	AttributionTrailer = "AI-Attribution"
)

// Provenance holds AI/human authorship metadata parsed from commit trailers.
type Provenance struct {
	Agent       string
	Model       string
	Attribution string
	Badge       string
}

type provKey struct{ agent, model, attribution string }

// Table is a bounded intern pool of Provenance values owned by a repository.
// A history worker may intern while the UI reads a previously delivered page;
// callers receive values, never slices.
type Table struct {
	mu     sync.RWMutex
	values []Provenance
	index  map[provKey]uint16
}

// Intern stores p in the table and returns its compact uint16 ID.
func (t *Table) Intern(p Provenance) uint16 {
	if p.Agent == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := provKey{p.Agent, p.Model, p.Attribution}
	if id := t.index[key]; id != 0 {
		return id
	}
	if len(t.values) == 0 {
		t.values = append(t.values, Provenance{}, Provenance{Agent: "其他", Badge: "◎ 其他"})
	}
	if len(t.values) >= 65536 {
		return 1
	}
	if t.index == nil {
		t.index = make(map[provKey]uint16)
	}
	p.Agent, p.Model, p.Attribution = strings.Clone(p.Agent), strings.Clone(p.Model), strings.Clone(p.Attribution)
	key = provKey{p.Agent, p.Model, p.Attribution}
	p.Badge = "◎ " + p.Agent
	if strings.EqualFold(p.Agent, "human") {
		p.Badge = "人工"
	}
	if p.Model != "" {
		p.Badge += " · " + NormalizeModel(p.Model)
	}
	id := uint16(len(t.values))
	t.values = append(t.values, p)
	t.index[key] = id
	return id
}

// Get returns the Provenance for id with localized labels applied.
func (t *Table) Get(id uint16) Provenance {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if int(id) < len(t.values) {
		p := t.values[id]
		if id == 1 {
			p.Agent = i18n.Tr("其他")
			p.Badge = "◎ " + i18n.Tr("其他")
		} else if strings.EqualFold(p.Agent, "human") {
			p.Badge = i18n.Tr("人工")
			if p.Model != "" {
				p.Badge += " · " + NormalizeModel(p.Model)
			}
		}
		return p
	}
	return Provenance{}
}

// Snapshot returns a copy of all interned Provenance entries.
func (t *Table) Snapshot() []Provenance {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Provenance(nil), t.values...)
}

// Bytes returns the approximate memory footprint of the table.
func (t *Table) Bytes() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	n := int64(cap(t.values))*int64(unsafe.Sizeof(Provenance{})) + int64(len(t.index))*(int64(unsafe.Sizeof(provKey{}))+32)
	for _, p := range t.values {
		n += int64(len(p.Agent) + len(p.Model) + len(p.Attribution) + len(p.Badge))
	}
	return n
}

// Parse extracts Provenance from git commit trailers.
// Only signatures backed by checked-in real commit samples are recognized.
// In particular a human coauthor's display name is never an identity signal.
func Parse(trailers string) Provenance {
	var p, native Provenance
	for _, line := range strings.FieldsFunc(trailers, func(r rune) bool { return r == '\n' || r == '\r' || r == '\x1d' }) {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "ai-agent":
			p.Agent = value
		case "ai-model":
			p.Model = value
		case "ai-attribution":
			p.Attribution = value
		case "assisted-by":
			if value == "Cindy (https://cindy.app)" {
				native = Provenance{Agent: "Cindy", Attribution: "native"}
			}
		}
	}
	if p.Agent != "" {
		return p
	}
	return native
}

// NormalizeModel formats known model identifiers into display names.
func NormalizeModel(raw string) string {
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

// Description formats Provenance into a human-readable summary string.
func (p Provenance) Description() string {
	if p.Agent == "" {
		return ""
	}
	label := p.Agent
	if strings.EqualFold(p.Agent, "human") {
		label = i18n.Tr("人工")
	}
	if p.Model != "" {
		label += " · " + NormalizeModel(p.Model)
	}
	source := i18n.Tr("显式")
	switch p.Attribution {
	case "native":
		source = i18n.Tr("原生签名")
	case "stamp":
		source = i18n.Tr("印章")
	case "inferred":
		source = i18n.Tr("推断")
	case "":
	default:
		source = p.Attribution
	}
	if i18n.EnglishUI.Load() {
		return label + " (" + source + ")"
	}
	return label + "（" + source + "）"
}

// ValidateModel checks that human/AI agent and model fields are consistent.
func ValidateModel(p Provenance) error {
	if strings.EqualFold(strings.TrimSpace(p.Agent), "human") {
		if p.Model != "" {
			return errors.New("人工提交不能填写 AI 模型")
		}
	} else if strings.TrimSpace(p.Agent) != "" && strings.TrimSpace(p.Model) == "" {
		return errors.New("AI 提交必须填写实际模型名，模型未知时请先确认")
	}
	return nil
}
