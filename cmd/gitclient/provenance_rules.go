package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type rulePreview struct {
	path          string
	dir           string
	before, after []byte
	diff          string
}

func previewRuleSnippet(dir, target string, remove bool) (rulePreview, error) {
	var p rulePreview
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
	// Reject symlinks along the write path, including the selected file.
	for path := p.path; path != filepath.Clean(dir); path = filepath.Dir(path) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return p, errors.New("规则路径包含符号链接，未修改")
		}
	}
	p.before, err = os.ReadFile(p.path)
	if err != nil && !os.IsNotExist(err) {
		return p, err
	}
	text := string(p.before)
	const start = "<!-- xylring:provenance:start"
	const end = "<!-- xylring:provenance:end -->"
	if i := strings.Index(text, start); i >= 0 {
		j := strings.Index(text[i:], end)
		if j < 0 {
			return p, errors.New("规则片段标记不完整，请先手动修复")
		}
		text = text[:i] + text[i+j+len(end):]
	}
	if !remove {
		text = strings.TrimRight(text, "\n") + "\n\n" + provenanceRuleSnippet()
	}
	p.after = []byte(text)
	if string(p.before) == text {
		return p, nil
	}
	p.diff = "--- " + target + "\n+++ " + target + "\n"
	for _, line := range strings.Split(strings.TrimSuffix(string(p.before), "\n"), "\n") {
		p.diff += "-" + line + "\n"
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		p.diff += "+" + line + "\n"
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
		return fmt.Errorf("保存规则失败：%w", err)
	}
	return nil
}
