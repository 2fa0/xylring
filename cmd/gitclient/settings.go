package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
)

type appPreferences struct {
	Language         string `json:"language,omitempty"`
	RestoreDisabled  bool   `json:"restore_disabled,omitempty"`
	FileView         int    `json:"file_view,omitempty"`
	DiffLayout       int    `json:"diff_layout,omitempty"`
	IgnoreWhitespace bool   `json:"ignore_whitespace,omitempty"`
	AutoFetch        string `json:"auto_fetch,omitempty"`
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
	return appPreferences{RestoreDisabled: a.restoreDisabled, FileView: a.fileView, DiffLayout: a.diffLayout, IgnoreWhitespace: a.diffIgnoreWhitespace, AutoFetch: a.autoFetch, Language: a.language}
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

func (a *app) openSettings() {
	if a.settings.open {
		return
	}
	a.settings = settingsState{open: true, gen: a.settings.gen + 1}
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
				target.notice = "已保存；下方显示当前生效值及来源"
			}
		})
	}()
}

// Choice controls share menu semantics, keyboard access and checked state.
func settingsChoice(c *ui.Context, key, label, value string, options []configOption, choose func(string)) {
	selected := value
	for _, o := range options {
		if o.value == value {
			selected = o.label
		}
	}
	if selected == "" {
		selected = "未设置"
	}
	ui.MenuButton(c.Key(key), selected, func(m *ui.Menu) {
		for _, o := range options {
			if m.Item(o.label).Checked(value == o.value).Chosen() {
				choose(o.value)
			}
		}
	}).Label(label).FillWidth().Tooltip(label)
}
func settingsLabel(c *ui.Context, label string) {
	ui.Text(c, label).FontSize(12).Bold()
}
func settingsHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(11).TextColor(c.Theme().TextMuted)
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
	appModal(c.Key("settings-modal"), &visible, "设置", 680, busy, func() {
		if d.confirmClose {
			ui.Text(c, "有未应用的 Git 修改。可以继续编辑，或放弃修改并关闭。").TextColor(c.Theme().Warning).FontSize(12)
			if ui.Button(c.Key("settings-continue"), "继续编辑").Clicked() {
				d.confirmClose = false
			}
		}
		ui.Tabs(c.Key("settings-pages"), &d.page, "通用", "Git", "当前项目").FillWidth()
		switch d.page {
		case 0:
			a.generalSettings(c)
		case 1:
			a.gitSettingsPage(c, false)
		case 2:
			if d.repo == "" {
				settingsHint(c, "打开一个仓库后，可以设置项目覆盖。")
				return
			}
			settingsHint(c, "当前项目："+homePath(d.repo))
			settingsLabel(c, "自动获取远程更新")
			value := a.repoAutoFetch[d.repo]
			options := append([]configOption{{"", "继承应用默认"}}, autoFetchOptions...)
			settingsChoice(c, "project-auto-fetch", "项目自动获取", value, options, func(v string) {
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
			delay := a.autoFetchDelay(d.repo)
			if delay == 0 {
				settingsHint(c, "当前生效：关闭")
			} else {
				settingsHint(c, fmt.Sprintf("当前生效：每 %d 分钟", int(delay/time.Minute)))
			}
			if t := a.tabByPath(d.repo); t != nil {
				if ui.Button(c.Key("settings-ring"), "本仓库年轮设置…").Disabled(dirty || busy).Clicked() {
					d.open = false
					t.ring.open = true
					a.refreshRing(t)
				}
			}
			a.gitSettingsPage(c, true)
		}
	}, func() {
		dirty = gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
		if dirty {
			settingsHint(c, "Git 修改尚未应用")
		}
		if dirty {
			if ui.Button(c.Key("settings-discard"), "放弃修改并关闭").Disabled(busy).Clicked() {
				discard = true
				visible = false
			}
		}
		if d.page > 0 {
			local := d.page == 2
			s := a.settingsScope(local)
			if ui.Button(c.Key("settings-apply"), "应用").Disabled(busy || a.settingsGitWriteBlocked() || !s.loaded || !gitSettingsDirty(s)).Clicked() {
				a.loadSettingsGit(local, true)
			}
		}
		if ui.Button(c.Key("settings-close"), "完成").Disabled(busy || dirty).Clicked() {
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
	settingsHint(c, "应用偏好即时生效，并自动保存。")
	settingsLabel(c, "主题")
	settingsChoice(c, "settings-theme", "外观", a.themeMode, []configOption{{"", "跟随系统"}, {"light", "浅色"}, {"dark", "深色"}}, func(v string) { a.themeMode = v })
	restore := !a.restoreDisabled
	ui.Checkbox(c.Key("settings-restore"), &restore, "启动时恢复上次打开的仓库").OnClick(func() { a.restoreDisabled = !restore })
	settingsLabel(c, "默认差异视图")
	settingsChoice(c, "settings-diff", "默认差异视图", strconv.Itoa(a.diffLayout), []configOption{{"0", "左右对照"}, {"1", "统一视图"}}, func(v string) { a.diffLayout, _ = strconv.Atoi(v) })
	settingsLabel(c, "默认文件视图")
	settingsChoice(c, "settings-files", "默认文件视图", strconv.Itoa(a.fileView), []configOption{{"0", "列表"}, {"1", "目录树"}}, func(v string) { a.fileView, _ = strconv.Atoi(v) })
	ui.Checkbox(c.Key("settings-whitespace"), &a.diffIgnoreWhitespace, "差异默认忽略空白").OnChange(func() {
		if t := a.activeTab(); t != nil && t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			a.loadFileDiff(t, t.fileSel)
		}
	})
	ui.Checkbox(c.Key("settings-bottom-width"), &a.bottomFullWidth, "底部详情面板全宽")
	settingsLabel(c, "自动获取远程更新")
	value := a.autoFetch
	if value == "" {
		value = "2"
	}
	settingsChoice(c, "settings-auto-fetch", "默认自动获取", value, autoFetchOptions, func(v string) {
		a.autoFetch = v
		for _, t := range a.tabs {
			if a.autoFetchDelay(t.path) == 0 {
				t.stopAutoFetch()
			}
		}
	})
	settingsHint(c, "自动获取仅在窗口活跃时检查当前仓库，不自动拉取或合并代码。")
}

func (a *app) gitSettingsPage(c *ui.Context, local bool) {
	s := a.settingsScope(local)
	if local {
		settingsHint(c, "项目覆盖写入当前仓库配置；恢复继承会删除本地覆盖，点击应用后生效。")
	} else {
		settingsHint(c, "修改 Git 全局配置，会影响其他仓库和 Git 客户端。未设置的项目继续继承。")
	}
	if local {
		settingsHint(c, "分支专属的拉取配置、工作树配置或命令行配置仍可能优先于这里的设置。")
	}
	if !s.loaded && !s.busy && s.err == "" {
		a.loadSettingsGit(local, false)
	}
	if s.busy {
		busyRow(c, "settings-git-loading", "正在处理 Git 配置…", "")
	}
	if s.err != "" {
		ui.Text(c, s.err).TextColor(c.Theme().Danger).FontSize(12)
		if ui.Button(c.Key("settings-reload"), "重新读取（放弃本页编辑）").Disabled(s.busy).Clicked() {
			a.loadSettingsGit(local, false)
		}
	}
	if s.notice != "" {
		ui.Text(c, s.notice).TextColor(c.Theme().Success).FontSize(12)
	}
	if !s.loaded {
		return
	}
	for i, def := range settingsGitKeys {
		if local && def.key == "init.defaultBranch" {
			continue
		}
		f := &s.fields[i]
		ui.Column(c.Key(def.key)).Gap(5).FillWidth().Children(func() {
			settingsLabel(c, def.label)
			effective := f.effective
			if f.origin == "" {
				effective = "未配置，使用 Git 默认行为"
			}
			settingsHint(c, "当前生效："+effective)
			if f.origin != "" {
				settingsHint(c, fmt.Sprintf("来源：%s · %s", f.scope, f.origin))
			}
			label := "设置全局值"
			if local {
				label = "为当前项目单独设置"
			}
			ui.Checkbox(c.Key("override"), &f.enabled, label).Disabled(s.busy)
			if f.enabled {
				if len(def.options) == 0 {
					ui.TextInput(c.Key("value"), &f.value).Label(def.label).FillWidth().Disabled(s.busy)
				} else {
					options := slices.Clone(def.options)
					if !slices.ContainsFunc(options, func(o configOption) bool { return o.value == f.value }) {
						options = append(options, configOption{f.value, "保留当前值：" + f.value})
					}
					ui.Box(c).FillWidth().Disabled(s.busy).Children(func() { settingsChoice(c, "value", def.label, f.value, options, func(v string) { f.value = v }) })
				}
			}
			if len(f.original) > 0 && f.enabled {
				if ui.Button(c.Key("inherit"), "恢复继承").Disabled(s.busy).Clicked() {
					f.enabled = false
				}
			}
		})
	}
}
