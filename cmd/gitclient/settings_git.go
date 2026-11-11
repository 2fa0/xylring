package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type configOption struct{ value, label string }
type configDefinition struct {
	key, label string
	options    []configOption
}

var settingsGitKeys = []configDefinition{
	{"user.name", "提交姓名", nil},
	{"user.email", "提交邮箱", nil},
	{"pull.rebase", "拉取方式", []configOption{{"false", "合并"}, {"true", "变基"}, {"merges", "变基并保留合并"}}},
	{"pull.ff", "快进策略", []configOption{{"true", "允许快进"}, {"false", "始终创建合并提交"}, {"only", "仅快进"}}},
	{"fetch.prune", "清理已删除的远程分支", []configOption{{"false", "关闭"}, {"true", "开启"}}},
	{"init.defaultBranch", "新仓库默认分支名", nil},
}

type gitSetting struct {
	enabled                  bool
	value                    string
	original                 []string
	effective, origin, scope string
}
type gitSettingsScope struct {
	fields       []gitSetting
	loaded, busy bool
	err, notice  string
}

// Keep exit codes: config uses 1 for an absent key and 5 for an absent unset.
func configCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if err := acquireGit(ctx); err != nil {
		return nil, err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"config"}, args...)...)
	hideConsoleWindow(cmd)
	if dir == "" {
		dir = os.TempDir()
	}
	cmd.Dir = dir
	cmd.Env = repoGitEnv(dir, nil)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		var e *exec.ExitError
		if errors.As(err, &e) {
			return out, fmt.Errorf("%s: %w", strings.TrimSpace(string(e.Stderr)), err)
		}
	}
	return out, err
}
func configExit(err error, code int) bool {
	var e *exec.ExitError
	return errors.As(err, &e) && e.ExitCode() == code
}
func readConfigDirect(ctx context.Context, dir, scope, key string) ([]string, error) {
	b, err := configCommand(ctx, dir, scope, "--no-includes", "--null", "--get-all", key)
	if configExit(err, 1) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00"), nil
}
func readGitSettings(ctx context.Context, dir, scope string) ([]gitSetting, error) {
	fields := make([]gitSetting, len(settingsGitKeys))
	for i, def := range settingsGitKeys {
		values, err := readConfigDirect(ctx, dir, scope, def.key)
		if err != nil {
			return nil, fmt.Errorf("读取 %s：%w", def.key, err)
		}
		f := gitSetting{original: values, enabled: len(values) > 0}
		if f.enabled {
			f.value = values[len(values)-1]
		}
		args := []string{"--includes", "--null", "--show-origin", "--show-scope", "--get-all", def.key}
		b, err := configCommand(ctx, dir, args...)
		if err != nil && !configExit(err, 1) {
			return nil, fmt.Errorf("读取 %s 来源：%w", def.key, err)
		}
		if err == nil {
			parts := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
			if len(parts)%3 != 0 {
				return nil, fmt.Errorf("无法解析 %s 的配置来源", def.key)
			}
			for j := 0; j < len(parts); j += 3 {
				if scope == "--global" && (parts[j] == "local" || parts[j] == "worktree") {
					continue
				}
				f.scope, f.origin, f.effective = parts[j], parts[j+1], parts[j+2]
			}
		}
		fields[i] = f
	}
	return fields, nil
}
func (f gitSetting) changed() bool {
	if !f.enabled {
		return len(f.original) > 0
	}
	return len(f.original) == 0 || f.value != f.original[len(f.original)-1]
}
func gitSettingsDirty(s *gitSettingsScope) bool {
	for _, f := range s.fields {
		if f.changed() {
			return true
		}
	}
	return false
}
func saveGitSettings(ctx context.Context, dir, scope string, fields []gitSetting) error {
	// Check every changed key before writing, so stale forms do not overwrite edits.
	for i, f := range fields {
		if !f.changed() {
			continue
		}
		key := settingsGitKeys[i].key
		if f.enabled && (strings.ContainsAny(f.value, "\x00\r\n") || strings.TrimSpace(f.value) == "") {
			return fmt.Errorf("%s 不能为空或包含换行", settingsGitKeys[i].label)
		}
		if f.enabled && key == "init.defaultBranch" {
			checked, err := runGit(ctx, dir, "check-ref-format", "--branch", f.value)
			if err != nil {
				return fmt.Errorf("默认分支名无效：%w", err)
			}
			if strings.TrimSuffix(string(checked), "\n") != f.value {
				return errors.New("默认分支名必须是实际名称，不能使用历史分支简写")
			}
		}
		values, err := readConfigDirect(ctx, dir, scope, key)
		if err != nil {
			return err
		}
		if !slices.Equal(values, f.original) {
			return fmt.Errorf("%s 已被其他程序修改，请重新读取后再编辑", key)
		}
	}
	for i, f := range fields {
		if !f.changed() {
			continue
		}
		key := settingsGitKeys[i].key
		args := []string{scope, "--replace-all", key, f.value}
		if !f.enabled {
			args = []string{scope, "--unset-all", key}
		}
		_, err := configCommand(ctx, dir, args...)
		if err != nil && !(!f.enabled && configExit(err, 5)) {
			return fmt.Errorf("保存 %s 失败（此前的配置项可能已保存）：%w", key, err)
		}
		values, err := readConfigDirect(ctx, dir, scope, key)
		if err != nil {
			return err
		}
		fields[i].original = values
		if !f.enabled && len(values) > 0 {
			return fmt.Errorf("%s 仍由其他全局配置文件提供，请重新读取查看来源", key)
		}
	}
	return nil
}
