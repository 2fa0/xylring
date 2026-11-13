package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type appPreferences struct {
	Language         string `json:"language,omitempty"`
	RestoreDisabled  bool   `json:"restore_disabled,omitempty"`
	FileView         int    `json:"file_view,omitempty"`
	DiffLayout       int    `json:"diff_layout,omitempty"`
	IgnoreWhitespace bool   `json:"ignore_whitespace,omitempty"`
	Wrap             bool   `json:"diff_wrap,omitempty"`
	AutoFetch        string `json:"auto_fetch,omitempty"`
	GitExecutable    string `json:"git_executable,omitempty"`
}

type settingsState struct {
	open          bool
	confirmClose  bool
	page          int
	repo          string
	gen           int
	global, local gitSettingsScope
}

var autoFetchOptions = []configOption{{"off", "关闭"}, {"2", "每 2 分钟"}, {"5", "每 5 分钟"}, {"10", "每 10 分钟"}, {"15", "每 15 分钟"}}

func (a *app) preferences() appPreferences {
	return appPreferences{RestoreDisabled: a.restoreDisabled, FileView: a.fileView, DiffLayout: a.diffLayout, IgnoreWhitespace: a.diffIgnoreWhitespace, Wrap: a.diffWrap, AutoFetch: a.autoFetch, Language: a.language, GitExecutable: a.gitExecutable}
}
func (a *app) autoFetchDelay(path string) time.Duration {
	value := a.autoFetch
	if v := a.repoAutoFetch[path]; v != "" {
		value = v
	}
	if value == "off" {
		return 0
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return autoFetchInterval
	}
	return time.Duration(n) * time.Minute
}
func validAutoFetch(value string) bool {
	if value == "" {
		return true
	}
	return slices.ContainsFunc(autoFetchOptions, func(o configOption) bool { return o.value == value })
}

// autoFetchDetail names the delay in force only when it differs from the choice
// the row already shows, so a matching value is not printed twice.
func autoFetchDetail(chosen string, delay time.Duration) string {
	if delay <= 0 {
		if chosen == "off" {
			return ""
		}
		return "当前生效：关闭"
	}
	minutes := strconv.Itoa(int(delay / time.Minute))
	if minutes == chosen {
		return ""
	}
	return fmt.Sprintf(tr("当前生效：每 %d 分钟"), int(delay/time.Minute))
}

func (a *app) openSettings() {
	if a.settings.open {
		return
	}
	a.settings = settingsState{open: true, gen: a.settings.gen + 1}
	a.gitPathDraft = a.gitExecutable
	if t := a.activeTab(); t != nil && !t.unavailable {
		a.settings.repo = t.path
	}
}
func (a *app) settingsScope(local bool) *gitSettingsScope {
	if local {
		return &a.settings.local
	}
	return &a.settings.global
}
func (a *app) settingsGitWriteBlocked() bool {
	if a.write.busy || a.fetch.busy || a.op.busy {
		return true
	}
	for _, t := range a.tabs {
		if t.commitBusy || t.syncBusy || t.fetching {
			return true
		}
	}
	return false
}

// gitSettingsHasEmptyOverride reports a key turned on with no value: the row's
// field marks itself invalid, and the page cannot be applied until it is filled.
func gitSettingsHasEmptyOverride(s *gitSettingsScope) bool {
	for _, f := range s.fields {
		if f.enabled && strings.TrimSpace(f.value) == "" {
			return true
		}
	}
	return false
}

func (a *app) loadSettingsGit(local, save bool) {
	d := a.settingsScope(local)
	if save && a.settingsGitWriteBlocked() {
		d.err = "请等待当前 Git 操作完成后再应用设置"
		return
	}
	if d.busy {
		return
	}
	d.busy, d.err, d.notice = true, "", ""
	gen, repo := a.settings.gen, a.settings.repo
	scope := "--global"
	if local {
		scope = "--local"
	}
	fields := slices.Clone(d.fields)
	refreshProject := save && !local && a.settings.local.loaded
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var err error
		if save {
			err = saveGitSettings(ctx, repo, scope, fields)
		}
		var fresh []gitSetting
		if err == nil {
			fresh, err = readGitSettings(ctx, repo, scope)
		}
		var project []gitSetting
		var projectErr error
		if err == nil && refreshProject {
			project, projectErr = readGitSettings(ctx, repo, "--local")
		}
		a.post(func() {
			if a.closed || a.settings.gen != gen {
				return
			}
			target := a.settingsScope(local)
			target.busy = false
			if err != nil {
				if save {
					target.fields = fields
				}
				target.err = err.Error()
				return
			}
			target.fields, target.loaded = fresh, true
			if refreshProject {
				a.settings.local.err = ""
				a.settings.local.notice = ""
				if projectErr != nil {
					a.settings.local.err = "刷新项目配置失败：" + projectErr.Error()
				} else {
					for i, f := range project {
						dst := &a.settings.local.fields[i]
						dst.effective, dst.origin, dst.scope = f.effective, f.origin, f.scope
					}
				}
			}
			if save {
				target.notice = "已保存"
			}
		})
	}()
}

// Git settings that belong together share a section, so the page reads as a
// few groups instead of one list of keys.
var settingsGitGroups = []struct {
	id, title string
	keys      []string
}{
	{"identity", "提交身份", []string{"user.name", "user.email"}},
	{"pull", "拉取与合并", []string{"pull.rebase", "pull.ff"}},
	{"defaults", "获取与默认值", []string{"fetch.prune", "init.defaultBranch"}},
}

func settingsGitIndex(key string) int {
	return slices.IndexFunc(settingsGitKeys, func(def configDefinition) bool { return def.key == key })
}

func settingsScopeLabel(scope string) string {
	switch scope {
	case "system":
		return tr("系统")
	case "global":
		return tr("全局")
	case "local":
		return tr("项目")
	case "worktree":
		return tr("工作树")
	case "command":
		return tr("命令行")
	}
	return scope
}

func settingsNotice(c *ui.Context, key, text string, color ui.Color) {
	ui.Row(c.Key(key)).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).Radius(8).
		Background(color.Alpha(0.1)).Border(1, color.Alpha(0.35)).Children(func() {
		ui.Text(c, trMessage(text)).FontSize(11).TextColor(color)
	})
}

func (a *app) settingsModal(c *ui.Context) {
	d := &a.settings
	if !d.open {
		return
	}
	dirty := gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
	busy := d.global.busy || d.local.busy
	visible := true
	discard := false
	appModal(c.Key("settings-modal"), &visible, tr("设置"), 720, busy, func() {
		if d.confirmClose {
			settingsNotice(c, "settings-confirm-close", "有未应用的 Git 修改。", c.Theme().Warning)
			ui.Row(c).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if ui.Button(c.Key("settings-continue"), tr("继续编辑")).Clicked() {
					d.confirmClose = false
				}
			})
		}
		ui.Tabs(c.Key("settings-pages"), &d.page, tr("通用"), "Git", tr("当前项目")).FillWidth()
		ui.Divider(c)
		ui.Column(c.Key("settings-page")).FillWidth().Gap(18).Children(func() {
			switch d.page {
			case 0:
				a.generalSettings(c)
			case 1:
				a.gitSettingsPage(c, false)
			case 2:
				a.projectSettings(c)
			}
		})
	}, func() {
		dirty = gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
		ui.Box(c).Grow(1)
		if dirty {
			if ui.Button(c.Key("settings-discard"), tr("放弃修改并关闭")).Disabled(busy).Clicked() {
				discard = true
				visible = false
			}
		}
		if d.page > 0 {
			local := d.page == 2
			s := a.settingsScope(local)
			if ui.PrimaryButton(c.Key("settings-apply"), tr("应用")).Disabled(busy || a.settingsGitWriteBlocked() || !s.loaded || !gitSettingsDirty(s) || gitSettingsHasEmptyOverride(s)).Clicked() {
				a.loadSettingsGit(local, true)
			}
		}
		if ui.Button(c.Key("settings-close"), tr("完成")).Disabled(busy || dirty).Clicked() {
			visible = false
		}
	})
	dirty = gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
	if !visible {
		if dirty && !discard {
			d.confirmClose = true
		} else {
			d.open = false
		}
	}
}

func (a *app) generalSettings(c *ui.Context) {
	settingsSection(c, "settings-appearance", "语言与外观",
		func() {
			settingsChoiceRow(c, "settings-language", "界面语言", "", a.language,
				[]configOption{{"", "跟随系统"}, {"zh-Hans", "简体中文"}, {"en", "English"}}, a.setLanguage)
		},
		func() {
			settingsChoiceRow(c, "settings-theme", "外观", "", a.themeMode,
				[]configOption{{"", "跟随系统"}, {"light", "浅色"}, {"dark", "深色"}},
				func(v string) { a.themeMode = v })
		},
	)
	settingsSection(c, "settings-diff", "差异与文件",
		func() {
			settingsChoiceRow(c, "settings-diff-layout", "默认差异视图", "", strconv.Itoa(a.diffLayout),
				[]configOption{{"0", "左右对照"}, {"1", "统一视图"}},
				func(v string) { a.diffLayout, _ = strconv.Atoi(v) })
		},
		func() {
			settingsChoiceRow(c, "settings-file-view", "默认文件视图", "", strconv.Itoa(a.fileView),
				[]configOption{{"0", "列表"}, {"1", "目录树"}},
				func(v string) { a.fileView, _ = strconv.Atoi(v) })
		},
		func() {
			settingsSwitchRow(c, "settings-whitespace", "差异默认忽略空白", "", &a.diffIgnoreWhitespace, false, func() {
				if t := a.activeTab(); t != nil && t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
					a.loadFileDiff(t, t.fileSel)
				}
			})
		},
		func() {
			settingsSwitchRow(c, "settings-diff-wrap", "差异默认折行", "", &a.diffWrap, false, nil)
		},
		func() {
			settingsSwitchRow(c, "settings-bottom-width", "底部详情面板全宽", "", &a.bottomFullWidth, false, nil)
		},
	)
	settingsSection(c, "settings-startup", "启动与同步",
		func() {
			restore := !a.restoreDisabled
			settingsSwitchRow(c, "settings-restore", "启动时恢复上次打开的仓库", "", &restore, false, func() {
				a.restoreDisabled = !restore
			})
		},
		func() {
			value := a.autoFetch
			if value == "" {
				value = "2"
			}
			detail := autoFetchDetail(value, a.autoFetchDelay(demoActivePath(a)))
			settingsChoiceRow(c, "settings-auto-fetch", "自动获取远程更新", detail, value, autoFetchOptions, func(v string) {
				a.autoFetch = v
				for _, t := range a.tabs {
					if a.autoFetchDelay(t.path) == 0 {
						t.stopAutoFetch()
					}
				}
			})
		},
	)
	a.gitBinarySettings(c)
}

// demoActivePath names the repository the preferences page speaks about: the
// open tab, so its own automatic-fetch delay shows here too.
func demoActivePath(a *app) string {
	if t := a.activeTab(); t != nil {
		return t.path
	}
	return ""
}

func (a *app) projectSettings(c *ui.Context) {
	d := &a.settings
	if d.repo == "" {
		ui.Column(c).FillWidth().Padding(18).Center().Children(func() {
			ui.Text(c, tr("打开一个仓库后，可以设置项目覆盖。")).FontSize(12).TextColor(c.Theme().TextMuted).TextAlign(ui.Center)
		})
		return
	}
	t := c.Theme()
	ui.Row(c.Key("project-path")).FillWidth().Padding(8, 11).Gap(8).AlignItems(ui.Center).Radius(8).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Icon(c, iconFolder).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, homePath(d.repo)).FontSize(12).TextColor(t.Text).SingleLine().Grow(1).MinWidth(0).Tooltip(d.repo)
	})
	settingsSection(c, "project-fetch", "自动获取",
		func() {
			value := a.repoAutoFetch[d.repo]
			options := append([]configOption{{"", "继承应用默认"}}, autoFetchOptions...)
			detail := autoFetchDetail(value, a.autoFetchDelay(d.repo))
			settingsChoiceRow(c, "project-auto-fetch", "检查频率", detail, value, options, func(v string) {
				if a.repoAutoFetch == nil {
					a.repoAutoFetch = make(map[string]string)
				}
				if v == "" {
					delete(a.repoAutoFetch, d.repo)
				} else {
					a.repoAutoFetch[d.repo] = v
				}
				if v == "off" {
					if t := a.tabByPath(d.repo); t != nil {
						t.stopAutoFetch()
					}
				}
			})
		},
	)
	settingsSection(c, "project-ring", "提交规范",
		func() {
			dirty := gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
			settingsRow(c, "project-ring-row", func() {
				settingsName(c, "年轮记录")
				settingsDetail(c, "提交时检查 AI Agent 与模型的溯源信息")
			}, func() {
				if tab := a.tabByPath(d.repo); tab != nil {
					if ui.Button(c.Key("settings-ring"), tr("本仓库年轮设置…")).Disabled(dirty || d.global.busy || d.local.busy).Clicked() {
						d.open = false
						tab.ring.open = true
						a.refreshRing(tab)
					}
				}
			})
		},
	)
	a.gitSettingsState(c, true)
	a.gitSettingsGroups(c, true)
}

func (a *app) gitSettingsPage(c *ui.Context, local bool) {
	a.gitSettingsState(c, local)
	a.gitSettingsGroups(c, local)
}

// gitSettingsState shows reading, failure and the last save of one scope.
func (a *app) gitSettingsState(c *ui.Context, local bool) {
	s := a.settingsScope(local)
	if !s.loaded && !s.busy && s.err == "" {
		a.loadSettingsGit(local, false)
	}
	if s.busy {
		busyRow(c, "settings-git-loading", "正在读取 Git 配置…", "")
	}
	if s.err != "" {
		settingsNotice(c, "settings-git-error", s.err, c.Theme().Danger)
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			if ui.Button(c.Key("settings-reload"), tr("重新读取（放弃本页编辑）")).Disabled(s.busy).Clicked() {
				a.loadSettingsGit(local, false)
			}
		})
	}
	if s.notice != "" {
		settingsNotice(c, "settings-git-notice", s.notice, c.Theme().Success)
	}
}

func (a *app) gitSettingsGroups(c *ui.Context, local bool) {
	s := a.settingsScope(local)
	if !s.loaded {
		return
	}
	for _, group := range settingsGitGroups {
		rows := make([]func(), 0, len(group.keys))
		for _, key := range group.keys {
			if local && key == "init.defaultBranch" {
				continue
			}
			i := settingsGitIndex(key)
			if i < 0 {
				continue
			}
			rows = append(rows, func() { a.gitSettingRow(c, s, i, local) })
		}
		if len(rows) > 0 {
			settingsSection(c, "git-"+group.id, group.title, rows...)
		}
	}
}

// gitSettingRow is one Git key: its name and current effective value, a switch
// for writing the key in this scope, and the value editor while it is on.
func (a *app) gitSettingRow(c *ui.Context, s *gitSettingsScope, i int, local bool) {
	def, f := settingsGitKeys[i], &s.fields[i]
	t := c.Theme()
	tip := "在全局配置中设置"
	// 当前页面本身就是这个范围，所以只有生效值来自更高优先级的范围时才需要括出来源。
	pageScope := "global"
	if local {
		tip = "为当前项目单独设置"
		pageScope = "local"
	}
	ui.Column(c.Key("git-" + def.key)).FillWidth().Children(func() {
		row := ui.SwitchBase(c.Key("git-"+def.key+"-switch"), &f.enabled).Label(tr(def.label)).FillWidth().
			Padding(10, 12).Gap(12).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Disabled(s.busy).Tooltip(tr(tip)).Transition(colorMotion())
		if row.Hovered() && !s.busy {
			row.Background(t.SurfaceHover)
		}
		row.Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Padding(0, 42, 0, 0).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
					settingsName(c, def.label)
					if f.changed() {
						chip(c, "changed", tr("已修改"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
					}
				})
				if f.origin != "" {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
						if f.scope != pageScope {
							chip(c, "scope", settingsScopeLabel(f.scope), chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10}).Tooltip(f.origin)
						}
						ui.Text(c, f.effective).FontSize(11).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0).Tooltip(f.effective)
					})
				}
			})
		})
		pos := row.AnimateTo("switch", boolFloat(f.enabled), ui.Animation{Duration: motionColor, Ease: ui.EaseOut})
		row.DrawOver(func(p *ui.Painter, r ui.Rect) { settingsSwitch(p, r, pos, t, s.busy) })
		row.OnClick(func() { s.notice = "" })
		if f.enabled {
			ui.Row(c.Key("git-"+def.key+"-editor")).FillWidth().Padding(0, 12, 10, 40).Gap(8).AlignItems(ui.Center).Children(func() {
				if len(def.options) == 0 {
					ui.TextInput(c.Key("value"), &f.value).Label(tr(def.label)).Placeholder(tr("填写值")).FillWidth().Disabled(s.busy).
						Invalid(strings.TrimSpace(f.value) == "")
				} else {
					options := slices.Clone(def.options)
					if !slices.ContainsFunc(options, func(o configOption) bool { return o.value == f.value }) {
						options = append(options, configOption{f.value, tr("保留当前值：") + f.value})
					}
					settingsChoice(c, "value", def.label, f.value, options, func(v string) { f.value = v }).Disabled(s.busy)
				}
			})
		}
	})
}
