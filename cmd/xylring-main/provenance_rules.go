package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var provenanceRuleTargets = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursor/rules/xylring-provenance.mdc",
}

type ruleFileStatus struct {
	target   string
	exists   bool
	hasRule  bool
	outdated bool
}

type rulePreview struct {
	target        string
	remove        bool
	path          string
	dir           string
	before, after []byte
	diff          string
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

func buildRuleContent(before string, remove bool) (after string, hasRule bool, err error) {
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
			text = provenanceRuleSnippet()
		} else {
			text = trimmed + "\n\n" + provenanceRuleSnippet()
		}
	} else if strings.TrimSpace(text) == "" {
		text = ""
	}
	return text, hasRule, nil
}

func inspectRuleFiles(dir string) []ruleFileStatus {
	statuses := make([]ruleFileStatus, 0, len(provenanceRuleTargets))
	absDir, err := filepath.Abs(dir)
	if err != nil {
		for _, target := range provenanceRuleTargets {
			statuses = append(statuses, ruleFileStatus{target: target})
		}
		return statuses
	}
	for _, target := range provenanceRuleTargets {
		st := ruleFileStatus{target: target}
		full := filepath.Join(absDir, filepath.FromSlash(target))
		if rejectRuleSymlinks(absDir, full) == nil {
			if before, err := os.ReadFile(full); err == nil {
				st.exists = true
				after, hasRule, err := buildRuleContent(string(before), false)
				st.hasRule = hasRule
				if err == nil && hasRule && after != string(before) {
					st.outdated = true
				}
			}
		}
		statuses = append(statuses, st)
	}
	return statuses
}

func previewRuleSnippet(dir, target string, remove bool) (rulePreview, error) {
	p := rulePreview{target: target, remove: remove}
	switch target {
	case "AGENTS.md", "CLAUDE.md", ".cursor/rules/xylring-provenance.mdc":
	default:
		return p, errors.New("请选择项目规则文件")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return p, err
	}
	p.dir = dir
	p.path = filepath.Join(dir, filepath.FromSlash(target))
	if err := rejectRuleSymlinks(dir, p.path); err != nil {
		return p, err
	}
	p.before, err = os.ReadFile(p.path)
	if err != nil && !os.IsNotExist(err) {
		return p, err
	}
	text, _, err := buildRuleContent(string(p.before), remove)
	if err != nil {
		return p, err
	}
	p.after = []byte(text)
	if string(p.before) == text {
		return p, nil
	}
	p.diff = "--- " + target + "\n+++ " + target + "\n"
	if len(p.before) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(string(p.before), "\n"), "\n") {
			p.diff += "-" + line + "\n"
		}
	}
	if len(p.after) > 0 {
		for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
			p.diff += "+" + line + "\n"
		}
	}
	return p, nil
}
func saveRulePreview(p rulePreview) error {
	for path := p.path; path != p.dir; path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("规则路径已变为符号链接，请重新预览")
		}
	}
	current, err := os.ReadFile(p.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(current) != string(p.before) {
		return errors.New("规则文件已变化，请重新预览")
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0755); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if info, err := os.Stat(p.path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(p.path, p.after, mode); err != nil {
		return err
	}
	return nil
}
