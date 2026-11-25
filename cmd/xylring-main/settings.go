package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type appPreferences struct {
	Language         string `json:"language,omitempty"`
	RestoreDisabled  bool   `json:"restore_disabled,omitempty"`
	FileView         int    `json:"file_view,omitempty"`
	DiffLayout       int    `json:"diff_layout,omitempty"`
	RefsInline       bool   `json:"refs_inline,omitempty"`
	IgnoreWhitespace bool   `json:"ignore_whitespace,omitempty"`
	Wrap             bool   `json:"diff_wrap,omitempty"`
	AutoFetch        string `json:"auto_fetch,omitempty"`
	GitExecutable    string `json:"git_executable,omitempty"`
	ExternalEditor   string `json:"external_editor,omitempty"`
}

const (
	settingsPageGeneral = iota
	settingsPageDiff
	settingsPageGit
	settingsPageProject
	settingsPageDataAbout
)

type settingsState struct {
	open          bool
	confirmClose  bool
	customEditor  bool
	page          int
	repo          string
	gen           int
	navHandles    [5]ui.Handle
	global, local gitSettingsScope
}

var autoFetchOptions = []configOption{{"off", "关闭"}, {"2", "每 2 分钟"}, {"5", "每 5 分钟"}, {"10", "每 10 分钟"}, {"15", "每 15 分钟"}}

func (a *app) preferences() appPreferences {
	return appPreferences{RestoreDisabled: a.restoreDisabled, FileView: a.fileView, DiffLayout: a.diffLayout, RefsInline: a.refsInline, IgnoreWhitespace: a.diffIgnoreWhitespace, Wrap: a.diffWrap, AutoFetch: a.autoFetch, Language: a.language, GitExecutable: a.gitExecutable, ExternalEditor: a.externalEditor}
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
	a.openSettingsPage(settingsPageGeneral)
}

func (a *app) openSettingsPage(page int) {
	if a.settings.open {
		a.settings.page = page
		return
	}
	a.settings = settingsState{open: true, page: page, gen: a.settings.gen + 1, customEditor: isCustomEditorValue(a.externalEditor)}
	a.gitPathDraft = a.gitExecutable
	if a.settings.customEditor {
		a.editorPathDraft = a.externalEditor
	} else {
		a.editorPathDraft = ""
	}
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

// gitSettingsHasEmptyOverride reports a modified key turned on with no value:
// only changed keys block Apply, matching saveGitSettings validation.
func gitSettingsHasEmptyOverride(s *gitSettingsScope) bool {
	for _, f := range s.fields {
		if f.changed() && f.enabled && strings.TrimSpace(f.value) == "" {
			return true
		}
	}
	return false
}

func isWSLRepoPath(path string) bool {
	_, _, ok := parseWSLUNCPath(path)
	return ok
}

func settingsGitRepoDir(repo string, local bool) string {
	// 全局 Git 设置页同时管理本机 Git 可执行文件路径；当前打开容器或 WSL 仓库时，
	// 全局配置必须固定读写本机环境，避免同一页混用主机与容器/WSL 两套配置。
	if !local && (isDockerRepoPath(repo) || isWSLRepoPath(repo)) {
		return ""
	}
	return repo
}

func (a *app) loadSettingsGit(local, save bool) {
	d := a.settingsScope(local)
	if local && isDockerRepoPath(a.settings.repo) {
		d.err = tr("容器仓库暂不支持修改 Git 配置")
		return
	}
	if save && a.settingsGitWriteBlocked() {
		d.err = "请等待当前 Git 操作完成后再应用设置"
		return
	}
	if d.busy {
		return
	}
	d.busy, d.err, d.notice = true, "", ""
	gen, repo := a.settings.gen, settingsGitRepoDir(a.settings.repo, local)
	scope := "--global"
	if local {
		scope = "--local"
	}
	fields := slices.Clone(d.fields)
	refreshProject := save && !local && a.settings.local.loaded && !isDockerRepoPath(a.settings.repo) && !isWSLRepoPath(a.settings.repo)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// 写成功但读回失败时，真正出错的是读取；前缀要跟上失败的那一步。
		writing := save
		var err error
		if writing {
			err = saveGitSettings(ctx, repo, scope, fields)
		}
		var fresh []gitSetting
		if err == nil {
			writing = false
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
				if writing {
					target.err = tr("保存 Git 配置失败：") + err.Error()
				} else {
					target.err = tr("读取 Git 配置失败：") + err.Error()
				}
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
						dst.inherited, dst.inheritedOrigin, dst.inheritedScope = f.inherited, f.inheritedOrigin, f.inheritedScope
					}
				}
			}
			if save {
				target.notice = "已保存"
				if !a.settingsHasUnsavedEdits() {
					a.settings.confirmClose = false
				}
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
	return ""
}

func settingsNotice(c *ui.Context, key, text string, color ui.Color) {
	ui.Row(c.Key(key)).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Radius(8).
		Background(color.Alpha(0.1)).Border(1, color.Alpha(0.35)).Children(func() {
		ui.Text(c, trMessage(text)).FontSize(12).TextColor(color)
	})
}

// 设置页内容长短不一，读取 Git 配置时还会只剩一行提示；给对话框一个固定的
// 高度上限（窗口更矮时按窗口来），切页签时就不会先缩短再长回去。
const (
	settingsModalWidth  = 760
	settingsModalHeight = 720
)

func (a *app) gitPathDraftDirty() bool {
	return strings.TrimSpace(a.gitPathDraft) != a.gitExecutable
}

func (a *app) editorPathDraftDirty() bool {
	if !a.settings.customEditor && !isCustomEditorValue(a.externalEditor) {
		return false
	}
	trimmed := strings.TrimSpace(a.editorPathDraft)
	if !isCustomEditorValue(a.externalEditor) {
		return trimmed != ""
	}
	return trimmed != a.externalEditor
}

func (a *app) settingsHasUnsavedEdits() bool {
	return gitSettingsDirty(&a.settings.global) || gitSettingsDirty(&a.settings.local) || a.gitPathDraftDirty() || a.editorPathDraftDirty()
}

func (a *app) settingsModal(c *ui.Context) {
	d := &a.settings
	if !d.open {
		return
	}
	generalDirty := a.editorPathDraftDirty()
	globalDirty := gitSettingsDirty(&d.global) || a.gitPathDraftDirty()
	localDirty := gitSettingsDirty(&d.local)
	dirty := generalDirty || globalDirty || localDirty
	busy := d.global.busy || d.local.busy
	visible := true
	discard := false
	navItems := []settingsNavItem{
		{page: settingsPageGeneral, key: "settings-nav-general", label: "通用", icon: iconRingSettings, dirty: generalDirty, handle: &d.navHandles[0]},
		{page: settingsPageDiff, key: "settings-nav-diff", label: "差异与视图", icon: iconGitCompare, handle: &d.navHandles[1]},
		{page: settingsPageGit, key: "settings-nav-git", label: "Git", icon: iconGitBranch, dirty: globalDirty, handle: &d.navHandles[2]},
		{page: settingsPageProject, key: "settings-nav-project", label: "当前项目", icon: iconFolderGit, dirty: localDirty, handle: &d.navHandles[3]},
		{page: settingsPageDataAbout, key: "settings-nav-data", label: "数据与关于", icon: iconKanban, handle: &d.navHandles[4]},
	}
	settingsDialog(c.Key("settings-modal"), &visible, tr("设置"), settingsModalWidth, settingsModalHeight, busy, &d.page, navItems, func() {
		if d.confirmClose {
			confirmMsg := "有未应用的 Git 修改。"
			if !globalDirty && !localDirty && generalDirty {
				confirmMsg = "有未应用的设置修改。"
			}
			ui.Column(c.Key("settings-confirm-wrap")).FillWidth().Shrink(0).Padding(12, 16, 0, 16).Gap(8).Children(func() {
				settingsNotice(c, "settings-confirm-close", confirmMsg, c.Theme().Warning)
				ui.Row(c).Justify(ui.End).AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("settings-continue"), tr("继续编辑")).Clicked() {
						d.confirmClose = false
					}
				})
			})
		}
	}, func() {
		switch d.page {
		case settingsPageGeneral:
			a.generalSettings(c)
		case settingsPageDiff:
			a.diffSettings(c)
		case settingsPageGit:
			a.gitSettingsPage(c, false)
		case settingsPageProject:
			a.projectSettings(c)
		case settingsPageDataAbout:
			a.dataAboutSettings(c)
		}
	}, func() {
		dirty = a.settingsHasUnsavedEdits()
		ui.Box(c).Grow(1)
		if dirty {
			if ui.Button(c.Key("settings-discard"), tr("放弃修改并关闭")).Disabled(busy).Clicked() {
				discard = true
				visible = false
			}
		}
		if d.page == settingsPageGit || d.page == settingsPageProject {
			local := d.page == settingsPageProject
			s := a.settingsScope(local)
			if ui.PrimaryButton(c.Key("settings-apply"), tr("应用")).Disabled(busy || a.settingsGitWriteBlocked() || !s.loaded || !gitSettingsDirty(s) || gitSettingsHasEmptyOverride(s)).Clicked() {
				a.loadSettingsGit(local, true)
			}
		}
		if ui.Button(c.Key("settings-close"), tr("完成")).Disabled(busy || dirty).Clicked() {
			visible = false
		}
	})
	dirty = a.settingsHasUnsavedEdits()
	if !visible {
		if dirty && !discard {
			d.confirmClose = true
		} else {
			if discard {
				a.gitPathDraft = a.gitExecutable
				if isCustomEditorValue(a.externalEditor) {
					a.editorPathDraft = a.externalEditor
					d.customEditor = true
				} else {
					a.editorPathDraft = ""
					d.customEditor = false
				}
			}
			d.confirmClose = false
			d.open = false
		}
	}
}

func (a *app) generalSettings(c *ui.Context) {
	settingsSection(c, "settings-appearance", "界面",
		func() {
			settingsChoiceRow(c, "settings-language", "界面语言", "", a.language,
				[]configOption{{"", "跟随系统"}, {"zh-Hans", "简体中文"}, {"en", "English"}}, a.setLanguage)
		},
		func() {
			settingsChoiceRow(c, "settings-theme", "外观", "", a.themeMode,
				[]configOption{{"", "跟随系统"}, {"light", "浅色"}, {"dark", "深色"}},
				func(v string) { a.themeMode = v })
		},
		func() {
			settingsSwitchRow(c, "settings-bottom-width", "底部详情面板全宽", "", &a.bottomFullWidth, false, nil)
		},
	)
	startupRows := []func(){
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
	}
	startupRows = append(startupRows, a.editorSettingsRows(c)...)
	settingsSection(c, "settings-startup", "启动与工具", startupRows...)
}

func (a *app) diffSettings(c *ui.Context) {
	settingsSection(c, "settings-view", "默认视图",
		func() {
			value := "0"
			if a.refsInline {
				value = "1"
			}
			settingsChoiceRow(c, "settings-refs-position", "引用位置", "", value,
				[]configOption{{"0", "图谱左侧"}, {"1", "描述行内"}},
				func(v string) { a.refsInline = v == "1" })
		},
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
	)
	settingsSection(c, "settings-diff", "差异显示",
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
	)
}

func (a *app) dataAboutSettings(c *ui.Context) {
	a.boardSettings(c)
	a.updateSettings(c)
	a.aboutSettings(c)
}

// projectURL 是这个应用自己的仓库，设置页的「关于」分组把它展示出来。
const projectURL = "https://github.com/2fa0/xylring"

// aboutSettings 是设置页的「关于」分组：仓库地址和去点 Star 的入口。版本号上面的
// 「更新」分组已经写过一次，这里不再重复。
func (a *app) aboutSettings(c *ui.Context) {
	settingsSection(c, "settings-about", "关于",
		func() {
			settingsRow(c, "settings-about-repo", func() {
				settingsName(c, "项目主页")
				// 地址本身就是链接，去掉协议头就短到不用悬停也能读全。
				ui.Link(c.Key("settings-about-url"), strings.TrimPrefix(projectURL, "https://"), projectURL).
					FontSize(12).SingleLine().MinWidth(0)
			}, func() {
				if paneIconButton(c, "settings-about-star", "在 GitHub 上点 Star", iconStar).Clicked() {
					c.OpenURL(projectURL)
				}
			})
		},
	)
}

func (a *app) boardSettings(c *ui.Context) {
	t := c.Theme()
	info := a.currentBoardStorageInfo()
	displayDir := homePath(info.ActiveDir)
	settingsSection(c, "settings-board", "看板",
		func() {
			settingsRow(c, "settings-board-dir", func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					settingsName(c, "数据目录")
					if info.IsCustom {
						chip(c, "custom", tr("自定义"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
					}
				})
				if displayDir != "" {
					ui.Text(c, displayDir).FontSize(12).TextColor(t.TextMuted).SingleLine().Tooltip(info.ActiveDir)
				}
			}, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("settings-board-open"), tr("打开")).Clicked() {
						a.openBoardFolder()
					}
					if info.IsCustom {
						if ui.Button(c.Key("settings-board-reset"), tr("恢复默认")).Disabled(info.FromEnv || a.picking).Clicked() {
							a.beginSwitchBoardDir(info.DefaultDir)
						}
					}
					if ui.Button(c.Key("settings-board-change"), tr("更改…")).Disabled(info.FromEnv || a.picking).Clicked() {
						a.pickBoardStorageDir()
					}
				})
			})
		},
		func() {
			settingsRow(c, "settings-board-backup", func() {
				settingsName(c, "备份与导入")
			}, func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("settings-board-export"), tr("导出…")).Disabled(a.picking).Clicked() {
						a.startBoardExport()
					}
					if ui.Button(c.Key("settings-board-import"), tr("导入…")).Disabled(a.picking).Clicked() {
						a.startBoardImport()
					}
					if ui.Button(c.Key("settings-board-restore"), tr("恢复…")).Clicked() {
						a.openBoardBackupsModal()
					}
				})
			})
		},
	)
}

// updateSettings 是设置页的「更新」分组：一个后台检查开关，一行版本与手动检查。
// 版本号只出现一次；有新版本时说能更新到哪一版，否则说上次检查是什么时候。
func (a *app) updateSettings(c *ui.Context) {
	usable := updatesUsable()
	settingsSection(c, "settings-update", "更新",
		func() {
			auto := automaticUpdates()
			settingsSwitchRow(c, "settings-auto-updates", "自动检查更新", "", &auto, !usable, func() {
				setAutomaticUpdates(auto)
			})
		},
		func() {
			status := currentUpdatesStatus()
			detail := ""
			switch {
			case !updaterReady.Load() || mygo.IsDev():
				detail = "这个构建不能自更新"
			case !usable:
				detail = "当前安装位置不可写，不能自动更新"
			case status.version != "":
				detail = fmt.Sprintf("可更新到 %s", status.version)
			case !status.lastCheck.IsZero():
				detail = fmt.Sprintf("上次检查：%s", status.lastCheck.Local().Format("2006-01-02 15:04"))
			}
			settingsRow(c, "settings-updates", func() {
				ui.Text(c, fmt.Sprintf(tr("当前版本 %s"), getAppVersion())).FontSize(14).FontWeight(500).TextColor(c.Theme().Text)
				if detail != "" {
					settingsDetail(c, detail)
				}
			}, func() {
				if ui.Button(c.Key("settings-check-updates"), tr("检查更新")).Disabled(!usable).Clicked() {
					checkUpdates()
				}
			})
		},
	)
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
	if isDockerRepoPath(d.repo) {
		settingsNotice(c, "docker-project-config", tr("容器仓库暂不支持修改 Git 配置"), c.Theme().TextMuted)
		return
	}
	t := c.Theme()
	ui.Row(c.Key("project-path")).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Radius(8).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Icon(c, iconFolder).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
		ui.Text(c, homePath(d.repo)).FontSize(12).TextColor(t.Text).SingleLine().Grow(1).MinWidth(0).Tooltip(d.repo)
	})
	settingsSection(c, "project-behavior", "仓库行为",
		func() {
			value := a.repoAutoFetch[d.repo]
			options := append([]configOption{{"", "继承应用默认"}}, autoFetchOptions...)
			detail := autoFetchDetail(value, a.autoFetchDelay(d.repo))
			settingsChoiceRow(c, "project-auto-fetch", "自动获取", detail, value, options, func(v string) {
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
		func() {
			dirty := gitSettingsDirty(&d.global) || gitSettingsDirty(&d.local)
			settingsRow(c, "project-ring-row", func() {
				settingsName(c, "年轮记录")
				settingsDetail(c, "提交时检查 AI Agent 与模型的溯源信息")
			}, func() {
				if tab := a.tabByPath(d.repo); tab != nil {
					if ui.Button(c.Key("settings-ring"), tr("管理…")).Disabled(dirty || d.global.busy || d.local.busy).Clicked() {
						d.open = false
						tab.ring.open = true
						tab.ring.err = ""
						a.refreshRing(tab)
						a.refreshRingRules(tab)
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
	if !local {
		a.gitBinarySettings(c)
	}
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

func gitScopePrecedence(scope string) int {
	switch scope {
	case "system":
		return 1
	case "global":
		return 2
	case "local":
		return 3
	case "worktree":
		return 4
	case "command":
		return 5
	}
	return 0
}

func isHigherPrecedenceScope(scope, pageScope string) bool {
	return gitScopePrecedence(scope) > gitScopePrecedence(pageScope)
}

// gitSettingRow is one Git key: choice keys use a menu, text keys use an inline
// text input where an empty value means no override in this scope.
func (a *app) gitSettingRow(c *ui.Context, s *gitSettingsScope, i int, local bool) {
	def := settingsGitKeys[i]
	if len(def.options) > 0 {
		a.gitChoiceSettingRow(c, s, i, local)
		return
	}
	f := &s.fields[i]
	t := c.Theme()
	pageScope := "global"
	placeholder := tr("未设置")
	clearTip := "清除"
	if local {
		pageScope = "local"
		placeholder = tr("继承全局设置")
		clearTip = "恢复继承"
	}
	overriddenByHigher := isHigherPrecedenceScope(f.scope, pageScope) && f.effective != ""
	previewScope, previewOrigin := "", ""
	if !f.enabled {
		if f.changed() {
			if f.inherited != "" {
				placeholder = f.inherited
				previewScope, previewOrigin = f.inheritedScope, f.inheritedOrigin
			}
		} else if !overriddenByHigher && f.effective != "" {
			placeholder = f.effective
			previewScope, previewOrigin = f.scope, f.origin
		}
	}
	settingsRow(c, "git-"+def.key, func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			settingsName(c, def.label)
			if f.changed() {
				chip(c, "changed", tr("已修改"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
			} else if !f.enabled && previewOrigin != "" && previewScope != pageScope {
				if scopeLabel := settingsScopeLabel(previewScope); scopeLabel != "" {
					chip(c, "scope", scopeLabel, chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10}).Tooltip(previewOrigin)
				}
			}
		})
		if overriddenByHigher {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
				if scopeLabel := settingsScopeLabel(f.scope); scopeLabel != "" {
					chip(c, "scope-effective", scopeLabel, chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10}).Tooltip(f.origin)
				}
				ui.Text(c, f.effective).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
			})
		}
	}, func() {
		input := ui.TextInput(c.Key("git-"+def.key+"-value"), &f.value).
			Label(tr(def.label)).Placeholder(placeholder).
			Width(settingsInputWidth(c)).Disabled(s.busy).
			Invalid(f.changed() && f.enabled && strings.TrimSpace(f.value) == "")
		if input.Changed() {
			s.notice = ""
			f.enabled = f.value != ""
		}
		if f.enabled {
			if paneIconButton(c, "git-"+def.key+"-clear", clearTip, iconClose).Disabled(s.busy).Clicked() {
				s.notice = ""
				f.enabled = false
				f.value = ""
			}
		}
	})
}

// gitChoiceSettingRow 是取值固定的 Git 键（拉取方式、快进策略、清理已删除的远程
// 分支）：菜单本身就是控件，选一个值就是在这个范围里覆盖它，选第一项就是不覆盖。
// 这样打开一项只要点一次，也不用把 git 的原始值（true / false / only）摊给用户看。
func (a *app) gitChoiceSettingRow(c *ui.Context, s *gitSettingsScope, i int, local bool) {
	def, f := settingsGitKeys[i], &s.fields[i]
	t := c.Theme()
	pageScope := "global"
	inherit := "未设置"
	if local {
		pageScope = "local"
		inherit = "继承全局设置"
	}
	choices := slices.Clone(def.options)
	if f.enabled && !slices.ContainsFunc(choices, func(o configOption) bool { return o.value == f.value }) {
		choices = append(choices, configOption{f.value, tr("保留当前值：") + f.value})
	}
	choices = append([]configOption{{"", inherit}}, choices...)
	selected := ""
	if f.enabled {
		selected = f.value
	}
	// 区分正在编辑的值与实际生效的值：更高优先级（如 worktree）覆盖时始终显示生效值；
	// 清除当前范围覆盖但尚未保存时，只展示确定的继承值，不把旧覆盖值当成继承值。
	overriddenByHigher := isHigherPrecedenceScope(f.scope, pageScope) && f.effective != ""
	rawPreview, previewScope, previewOrigin := "", "", ""
	if overriddenByHigher {
		rawPreview, previewScope, previewOrigin = f.effective, f.scope, f.origin
	} else if !f.enabled {
		if f.changed() {
			rawPreview, previewScope, previewOrigin = f.inherited, f.inheritedScope, f.inheritedOrigin
		} else {
			rawPreview, previewScope, previewOrigin = f.effective, f.scope, f.origin
		}
	}
	value := ""
	if rawPreview != "" {
		if label, ok := configOptionLabel(def, rawPreview); ok {
			value = tr(label)
		} else {
			value = rawPreview
		}
	}
	settingsRow(c, "git-"+def.key, func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			settingsName(c, def.label)
			if f.changed() {
				chip(c, "changed", tr("已修改"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
			}
		})
		if value != "" {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
				if previewScope != pageScope {
					if scopeLabel := settingsScopeLabel(previewScope); scopeLabel != "" {
						chip(c, "scope", scopeLabel, chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10}).Tooltip(previewOrigin)
					}
				}
				ui.Text(c, value).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
			})
		}
	}, func() {
		settingsChoice(c, "git-"+def.key+"-choice", def.label, selected, choices, func(v string) {
			s.notice = ""
			if v == "" {
				f.enabled, f.value = false, ""
				return
			}
			f.enabled, f.value = true, v
		}).Disabled(s.busy)
	})
}

// configOptionLabel 是配置文件里的原始值对应的说明文字，例如 pull.rebase 的 true
// 在界面上是“变基”。值不在候选里时（例如手工写的别名）由调用方照原样显示。
func configOptionLabel(def configDefinition, value string) (string, bool) {
	for _, o := range def.options {
		if o.value == value {
			return o.label, true
		}
	}
	return "", false
}
