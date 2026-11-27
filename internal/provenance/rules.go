package provenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// RuleTargets lists supported project rule files for provenance instructions.
var RuleTargets = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursor/rules/xylring-provenance.mdc",
}

// RuleFileStatus describes whether a rule file exists and has the current provenance snippet.
type RuleFileStatus struct {
	Target   string
	Exists   bool
	HasRule  bool
	Outdated bool
}

// RulePreview holds a staged change to a project rule file before saving.
type RulePreview struct {
	Target        string
	Remove        bool
	Path          string
	Dir           string
	Before, After []byte
	Diff          string
}

// RuleSnippet returns the managed markdown snippet inserted into project rule files.
func RuleSnippet() string {
	return `<!-- xylring:provenance:start v1 -->
## Commit provenance
When you run git commit, append these Git trailers after a blank line:

AI-Agent: <your agent/product name>
AI-Model: <your exact model name>

AI-assisted commits require exactly one nonempty AI-Agent and one nonempty AI-Model.
Use your actual model name; if unknown, confirm it before committing. Never guess.
Assisted-by and Co-authored-by do not replace the AI-Agent trailer.
After committing, inspect git log -1 --format=%B and fix missing or invalid
trailers on this task's own commit before reporting completion.
If a provenance hook rejects the message, follow its template and retry normally.
Never bypass it with --no-verify, core.hooksPath overrides, or hook changes.
Never declare AI-Agent: human for AI-assisted work. Strict mode reserves that
value for explicitly human-only commits, which must omit AI-Model and AI-Attribution.
Do not add these trailers to commits the user authored without your help.
If you ran xylring stamp during this task, run xylring stamp --clear when finished.
<!-- xylring:provenance:end -->
`
}

func rejectRuleSymlinks(dir, targetPath string) error {
	// Reject symlinks along the write path, including the selected file.
	for path := targetPath; path != filepath.Clean(dir); path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("规则路径包含符号链接，未修改")
		}
	}
	return nil
}

// BuildRuleContent inserts or removes the provenance snippet in existing rule file text.
func BuildRuleContent(before string, remove bool) (after string, hasRule bool, err error) {
	const start = "<!-- xylring:provenance:start"
	const end = "<!-- xylring:provenance:end -->"
	text := before
	if i := strings.Index(text, start); i >= 0 {
		j := strings.Index(text[i:], end)
		if j < 0 {
			return "", false, errors.New("规则片段标记不完整，请先手动修复")
		}
		hasRule = true
		text = text[:i] + text[i+j+len(end):]
	}
	if !remove {
		trimmed := strings.TrimRight(text, "\n")
		if strings.TrimSpace(trimmed) == "" {
			text = RuleSnippet()
		} else {
			text = trimmed + "\n\n" + RuleSnippet()
		}
	} else if strings.TrimSpace(text) == "" {
		text = ""
	}
	return text, hasRule, nil
}

// InspectRuleFiles inspects all supported rule files in dir.
func InspectRuleFiles(dir string) []RuleFileStatus {
	statuses := make([]RuleFileStatus, 0, len(RuleTargets))
	absDir, err := filepath.Abs(dir)
	if err != nil {
		for _, target := range RuleTargets {
			statuses = append(statuses, RuleFileStatus{Target: target})
		}
		return statuses
	}
	for _, target := range RuleTargets {
		st := RuleFileStatus{Target: target}
		full := filepath.Join(absDir, filepath.FromSlash(target))
		if rejectRuleSymlinks(absDir, full) == nil {
			if before, err := os.ReadFile(full); err == nil {
				st.Exists = true
				after, hasRule, err := BuildRuleContent(string(before), false)
				st.HasRule = hasRule
				if err == nil && hasRule && after != string(before) {
					st.Outdated = true
				}
			}
		}
		statuses = append(statuses, st)
	}
	return statuses
}

// PreviewRuleSnippet computes a diff preview for adding or removing the provenance snippet.
func PreviewRuleSnippet(dir, target string, remove bool) (RulePreview, error) {
	p := RulePreview{Target: target, Remove: remove}
	switch target {
	case "AGENTS.md", "CLAUDE.md", ".cursor/rules/xylring-provenance.mdc":
	default:
		return p, errors.New("请选择项目规则文件")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return p, err
	}
	p.Dir = dir
	p.Path = filepath.Join(dir, filepath.FromSlash(target))
	if err := rejectRuleSymlinks(dir, p.Path); err != nil {
		return p, err
	}
	p.Before, err = os.ReadFile(p.Path)
	if err != nil && !os.IsNotExist(err) {
		return p, err
	}
	text, _, err := BuildRuleContent(string(p.Before), remove)
	if err != nil {
		return p, err
	}
	p.After = []byte(text)
	if string(p.Before) == text {
		return p, nil
	}
	p.Diff = "--- " + target + "\n+++ " + target + "\n"
	if len(p.Before) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(string(p.beforeOrEmpty()), "\n"), "\n") {
			p.Diff += "-" + line + "\n"
		}
	}
	if len(p.After) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			p.Diff += "+" + line + "\n"
		}
	}
	return p, nil
}

func (p RulePreview) beforeOrEmpty() []byte {
	return p.Before
}

// SaveRulePreview writes a previously previewed rule file change after re-checking symlinks and staleness.
func SaveRulePreview(p RulePreview) error {
	for path := p.Path; path != p.Dir; path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("规则路径已变为符号链接，请重新预览")
		}
	}
	current, err := os.ReadFile(p.Path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(current) != string(p.Before) {
		return errors.New("规则文件已变化，请重新预览")
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0755); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if info, err := os.Stat(p.Path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(p.Path, p.After, mode); err != nil {
		return err
	}
	return nil
}
