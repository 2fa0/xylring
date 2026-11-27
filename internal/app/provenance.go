package app

import (
	"context"
	"strings"
	"sync"

	prov "github.com/2fa0/xylring/internal/provenance"
)

const (
	agentTrailer       = prov.AgentTrailer
	modelTrailer       = prov.ModelTrailer
	attributionTrailer = prov.AttributionTrailer
)

type provenance = prov.Provenance
type provenanceTable = prov.Table

var (
	parseProvenance         = prov.Parse
	normalizeModel          = prov.NormalizeModel
	validateProvenanceModel = prov.ValidateModel

	provenanceRuleTargets = prov.RuleTargets
	provenanceRuleSnippet = prov.RuleSnippet
	inspectRuleFiles      = prov.InspectRuleFiles
	previewRuleSnippet    = prov.PreviewRuleSnippet
	saveRulePreview       = prov.SaveRulePreview
)

type ruleFileStatus = prov.RuleFileStatus
type rulePreview = prov.RulePreview

func (q historyQuery) matchesProvenance(p provenance) bool {
	if p.Agent == "" {
		return false
	}
	value := p.Agent
	if q.kind == "模型" {
		value = normalizeModel(p.Model)
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(q.term))
}

// Probe the actual pretty-format capability for the resolved Git executable.
// Old Git prints the unsupported placeholder literally. Empty repositories
// fall back until a subsequent stream can probe a real commit.
var trailerCapability struct {
	sync.Mutex
	known, supported bool
	git              gitProbe
}

func historyCommitFormat(ctx context.Context, dir string) (string, bool) {
	probe, err := resolveGit()
	if err != nil {
		return commitFormat, false
	}
	trailerCapability.Lock()
	defer trailerCapability.Unlock()
	if trailerCapability.git != probe {
		trailerCapability.git = probe
		trailerCapability.known, trailerCapability.supported = false, false
	}
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
		return tr("当前 Git 不支持白名单 Trailer 格式，历史年轮展示已降级。请升级 Git。")
	}
	return ""
}
