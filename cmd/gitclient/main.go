// Xylring：导入一个 Git 仓库，画出它的提交图谱。
//
//	go test
//	go tool mygo dev
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/2fa0/xylring/internal/crashlog"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

const (
	laneGap     = 18
	lanePad     = 10
	graphStroke = 2.5
	// 年轮环不能越出泳道：左右各只有 laneGap/2 的位置。
	maxRingRadius = 8.2
	// 表格单元格会 Clip，并把内容垫进 Space(1.5)/Space(2.5)。
	// 图谱要画进这块内边距，线才能接到下一行的格线上。
	graphCellPadY   = 1.5
	graphCellPadX   = 2.5
	chromeMinHeight = 40
	chromePad       = 12
)

// 默认泳道色。明快高饱和的年轻化配色，兼顾浅色与深色背景辨识度。
const graphColorDefault = "#3B82F6,#10B981,#F59E0B,#8B5CF6,#EC4899,#06B6D4,#F97316,#6366F1"

var laneColors []ui.Color

func init() {
	laneColors, _ = parsePalette(graphColorDefault)
}

type app struct {
	recentProvenance []provenance
	ringSkipNotice   bool

	settings        settingsState
	restoreDisabled bool
	autoFetch       string
	gitExecutable   string
	gitPathDraft    string
	git             gitState
	gitCheckGen     uint64
	repoAutoFetch   map[string]string
	themeMode       string
	language        string
	win             *mygo.Window
	menu            *mygo.Menu
	dispatch        func(func())

	tabs              []*repoTab
	active            int
	nextID            int
	recent            []string
	recentSearch      string
	recentSearchInput ui.Handle
	persist           bool
	picking           bool
	banner            string
	bannerPath        string
	pendingToast      string
	found             []string
	foundFrom         string
	foundGen          int
	closed            bool
	watchCancel       context.CancelFunc
	scanCancel        context.CancelFunc
	scanPath          string
	scanCache         map[string]scannedDir
	recentWrites      chan []string
	repoListState     ui.ListState
	repoSel           int
	repoOrder         []string
	repoSplit         float32
	sidebarHidden     bool
	bottomFullWidth   bool
	showHome          bool
	summaryMu         sync.Mutex
	repoSummaries     map[string]repoSummary
	summaryRunning    map[string]bool
	summaryCancel     context.CancelFunc

	showCompare          bool
	compareFrom          string
	compareTo            string
	compareErr           string
	compareThree         bool
	fileView             int // 0 list, 1 tree
	diffLayout           int // 0 split, 1 unified
	diffIgnoreWhitespace bool
	diffWrap             bool

	showWrite bool
	write     commitWrite

	showFetch bool
	fetch     fetchDialog

	sessionReady   bool
	sessionDone    chan struct{}
	sessionWrites  chan savedSession
	lastSession    *savedSession
	sessionResults chan error
	showOperation  bool
	op             repoOperation
	opCancel       context.CancelFunc
	showDiscard    bool
	discard        discardDialog
}

type discardDialog struct {
	open bool
	file fileChange
}

func newApp() *app {
	session := loadSession()
	mode := session.ThemeMode
	if mode != "light" && mode != "dark" {
		mode = ""
	}
	a := &app{
		themeMode:       mode,
		recent:          loadRecent(),
		persist:         true,
		repoSummaries:   make(map[string]repoSummary),
		summaryRunning:  make(map[string]bool),
		bottomFullWidth: true,
	}
	if p := session.Preferences; p != nil {
		if validLanguage(p.Language) {
			a.language = p.Language
		}
		a.restoreDisabled, a.fileView, a.diffLayout = p.RestoreDisabled, p.FileView, p.DiffLayout
		a.diffIgnoreWhitespace = p.IgnoreWhitespace
		a.diffWrap = p.Wrap
		if validAutoFetch(p.AutoFetch) {
			a.autoFetch = p.AutoFetch
		}
		a.gitExecutable = strings.TrimSpace(p.GitExecutable)
		if a.fileView != 1 {
			a.fileView = 0
		}
		if a.diffLayout != 1 {
			a.diffLayout = 0
		}
	}
	a.repoAutoFetch = session.RepoAutoFetch
	a.gitExecutable = setGitExecutablePath(a.gitExecutable)
	a.gitPathDraft = a.gitExecutable
	for path, value := range a.repoAutoFetch {
		if !validAutoFetch(value) {
			delete(a.repoAutoFetch, path)
		}
	}
	if session.BottomFullWidth != nil {
		a.bottomFullWidth = *session.BottomFullWidth
	}
	return a
}

func (a *app) view(c *ui.Context) {
	switch a.themeMode {
	case "light":
		c.SetTheme(ui.LightTheme())
	case "dark":
		c.SetTheme(ui.DarkTheme())
	}
	defer a.queueSession()
	if !a.settings.open {
		a.handleShortcuts(c)
	}
	a.syncMenu()
	if a.pendingToast != "" {
		c.Toast(trMessage(a.pendingToast))
		a.pendingToast = ""
	}
	t := c.Theme()
	root := ui.Column(c).Fill().Background(t.Background)
	dropped := root.DroppedFiles()
	isDropOver := root.FileDragOver()
	dropOpts := transfer.DropOptions{
		Formats:    []transfer.Format{transfer.FileList, transfer.URIList},
		Operations: transfer.Copy | transfer.Move,
	}
	if _, over := ui.DataDragOver(root, dropOpts); over {
		isDropOver = true
	}
	if drop, ok := ui.DropData(root, dropOpts); ok {
		if files, err := drop.Data.Files(); err == nil && len(files) > 0 {
			dropped = append(dropped, files...)
		}
	}
	if len(dropped) > 0 {
		a.openPath(dropped[0])
	}
	root.Children(func() {
		a.chrome(c)
		if a.showHome || (a.activeTab() == nil && len(a.found) <= 1) {
			ui.Column(c.Key("page-home")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
				a.emptyState(c)
			})
		} else {
			ui.Column(c.Key("page-graph")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
				a.graphScreen(c)
			})
		}
		if isDropOver {
			ui.Box(c.Key("drop-overlay")).Absolute().Fill().
				Background(t.Background.Alpha(0.92)).
				Border(2, t.Accent).
				Radius(t.Radius).
				Transition(fadeEnter()).
				Children(func() {
					ui.Column(c).Absolute().Fill().Center().Gap(10).Children(func() {
						ui.Box(c).Size(56, 56).Radius(28).Background(t.Accent.Alpha(0.15)).Center().Children(func() {
							ui.Icon(c, iconFolderPlus).Size(28, 28).TextColor(t.Accent)
						})
						ui.Text(c, tr("释放以打开仓库")).FontSize(18).Bold().TextColor(t.Text)
						ui.Text(c, tr("拖拽 Git 仓库文件夹直接在此窗口打开")).FontSize(13).TextColor(t.TextMuted)
					})
				})
		}
	})
	a.comparisonModal(c)
	a.commitWriteModal(c)
	a.fetchModal(c)
	a.discardModal(c)
	a.ringModal(c)
	a.operationModal(c)
	a.settingsModal(c)
	a.gitLogModal(c)
}

func (a *app) handleShortcuts(c *ui.Context) {
	if a.menu == nil && c.Shortcut(commandModifier(), ui.KeyComma) {
		a.openSettings()
		return
	}
	tab := a.activeTab()
	empty := a.showHome || (tab == nil && len(a.found) <= 1)
	if a.menu == nil {
		if c.Shortcut(commandModifier(), ui.KeyO) {
			a.pickRepo()
		}
		if !empty && c.Shortcut(commandModifier(), ui.KeyB) {
			a.toggleSidebar()
		}
		if tab != nil && !empty {
			if c.Shortcut(commandModifier(), ui.KeyR) {
				a.refresh(tab)
			}
			if c.Shortcut(commandModifier(), ui.KeyW) {
				a.closeCurrentTab()
				return
			}
			if tab.unavailable {
				return
			}
			if c.Shortcut(commandModifier(), ui.KeyEnter) && strings.TrimSpace(tab.commitMsg) != "" {
				a.runCommitWorktree(tab)
			}
			if !tab.loading && !a.showCompare && c.Shortcut(commandModifier(), ui.KeyD) {
				a.openCompareModal(tab)
			}
		}
	}
	tab = a.activeTab()
	if tab != nil && tab.unavailable {
		return
	}
	if !empty && tab != nil && !a.showCompare {
		if c.Shortcut(commandModifier(), ui.KeyF) {
			if tab.contentTab > 0 && tab.contentTab <= len(tab.fileTabs) {
				a.openFileTabFind(tab.fileTabs[tab.contentTab-1])
				return
			}
			if a.hasInspectBottom(tab) && (tab.diff != nil || tab.simpleDiff != nil) {
				a.openDiffFind(tab)
				return
			}
			if tab.query.filePath != "" {
				a.leaveFileHistory(tab)
			}
			tab.searchSuggestOpen = true
			tab.searchInput.Focus(c)
			return
		}
		if c.Shortcut(commandModifier(), ui.KeyP) || c.Shortcut(commandModifier(), ui.KeyK) {
			if tab.contentTab > 0 {
				tab.contentTab = 0
			}
			if tab.query.filePath != "" {
				a.leaveFileHistory(tab)
			}
			tab.searchSuggestOpen = true
			tab.searchInput.Focus(c)
			return
		}
	}
	if !empty && tab != nil && tab.contentTab > 0 {
		return
	}
	if empty && len(a.recent) > 8 && !a.showCompare && (c.Shortcut(commandModifier(), ui.KeyF) || c.Shortcut(commandModifier(), ui.KeyP) || c.Shortcut(commandModifier(), ui.KeyK)) {
		a.recentSearchInput.Focus(c)
	}
	if empty || tab == nil || a.showCompare || !c.Shortcut(0, ui.KeyEscape) {
		if empty && c.Shortcut(0, ui.KeyEscape) {
			if a.recentSearch != "" {
				a.recentSearch = ""
			} else if a.showHome && (tab != nil || len(a.found) > 1) {
				a.showHome = false
			}
		}
		return
	}
	if tab.searchSuggestOpen {
		tab.searchSuggestOpen = false
		return
	}
	if tab.locateErr != "" {
		tab.locateErr = ""
		return
	}
	if tab.fileFilter != "" {
		tab.fileFilter = ""
		return
	}
	if tab.detailsOpen {
		tab.detailsOpen = false
		return
	}
	if tab.diffMaximized {
		tab.diffMaximized = false
		return
	}
	if tab.comparison != nil {
		a.endComparison(tab)
	} else if tab.worktree {
		tab.selected = -1
		tab.selection.Clear()
		a.closeWorktree(tab)
	} else if tab.inspecting != zeroHash {
		tab.selected = -1
		tab.selection.Clear()
		a.closeInspection(tab)
	} else if tab.query.filePath != "" {
		a.leaveFileHistory(tab)
	} else if tab.query.term != "" {
		a.leaveSearch(tab)
	}
}

func (a *app) chrome(c *ui.Context) {
	bar := c.TitleBar()
	h := max(bar.Height, chromeMinHeight)
	padL, padR := bar.Left, bar.Right
	if bar.Left > 0 {
		padL = bar.Left + 8
	} else if padL < chromePad {
		padL = chromePad
	}
	if padR < chromePad {
		padR = chromePad
	}
	tab := a.activeTab()
	empty := a.showHome || (tab == nil && len(a.found) <= 1)
	sidebarOpen := !empty && a.showRepoSidebar() && a.repoSplit > 0 && (tab == nil || !tab.diffMaximized)
	ui.Box(c).Height(h).Shrink(0).DragWindow().Children(func() {
		if sidebarOpen {
			ui.Box(c).Absolute().Left(a.repoSplit - 1).Top(0).Bottom(0).Width(1).Background(c.Theme().Border)
		}
		btnY := (h - 26) / 2
		if runtime.GOOS == "darwin" && bar.Left > 0 {
			btnY = 13
		}
		if sidebarOpen {
			sidebarW := a.repoSplit - padL - 6
			if sidebarW > 0 {
				ui.Row(c.Key("sidebar-chrome-header")).
					Absolute().Left(padL).Top(btnY).Width(sidebarW).Height(26).
					Gap(4).AlignItems(ui.Center).Children(func() {
					if chromeIconButton(c, "toggle-sidebar", "切换侧边栏", iconPanelLeft).
						Tooltip(sidebarToggleTip(a.showRepoSidebar())).
						Clicked() {
						a.toggleSidebar()
					}
					if chromeIconButton(c, "open-settings", "设置", iconRingSettings).Clicked() {
						a.openSettings()
					}
					ui.Box(c).Grow(1)
					if chromeIconButton(c, "sidebar-import-btn", "导入仓库", iconFolderPlus).
						Tooltip(tr("导入本地仓库 (⌘O)")).
						Clicked() {
						a.pickRepo()
					}
					homeBtn := chromeIconButton(c, "sidebar-home-btn", "返回首页", iconHome).
						Tooltip(tr("返回首页（右键查看最近仓库）"))
					if len(a.recent) > 0 {
						homeBtn.ContextMenu(func(m *ui.Menu) {
							for _, rp := range a.recent {
								repoPath := rp
								if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Chosen() {
									a.openRepo(repoPath)
								}
							}
						})
					}
					if homeBtn.Clicked() {
						a.goHome()
					}
				})
			}
		} else if !empty {
			btn := chromeIconButton(c, "toggle-sidebar", "切换侧边栏", iconPanelLeft).
				Absolute().Left(padL).Top(btnY).
				Tooltip(sidebarToggleTip(a.showRepoSidebar()))
			if btn.Clicked() {
				a.toggleSidebar()
			}
		}
		if !sidebarOpen {
			x := padL
			if !empty {
				x += 30
			}
			if chromeIconButton(c, "open-settings", "设置", iconRingSettings).Absolute().Left(x).Top(btnY).Clicked() {
				a.openSettings()
			}
		}
		rightPadL := padL
		if sidebarOpen {
			rightPadL = max(padL, a.repoSplit+8)
		}
		ui.Row(c).Fill().Padding(0, padR, 0, rightPadL).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			if empty {
				return
			}
			if tab != nil {
				disabled := tab.unavailable || tab.loading || a.write.busy || a.fetch.busy || tab.syncBusy || tab.commitBusy
				ui.MenuButton(c.Key("topbar-sync"), tr("同步"), func(m *ui.Menu) {
					if m.Item(tr("获取远程更新")).Disabled(disabled).
						Tooltip(tr("git fetch --all：更新远程分支信息，不修改本地代码")).Chosen() {
						a.quickFetch(tab)
					}
					pullLabel := tr("拉取到当前分支")
					if tab.behind > 0 {
						pullLabel += fmt.Sprintf(tr("（落后 %d 个提交）"), tab.behind)
					}
					if m.Item(pullLabel).Disabled(disabled).
						Tooltip(tr("git pull：获取并整合远程更新，会修改本地代码")).Chosen() {
						a.quickPull(tab)
					}
					pushLabel := tr("推送当前分支")
					if tab.ahead > 0 {
						pushLabel += fmt.Sprintf(tr("（领先 %d 个提交）"), tab.ahead)
					}
					if m.Item(pushLabel).Disabled(disabled).
						Tooltip(tr("git push：将当前分支的本地提交推送到远程")).Chosen() {
						a.quickPush(tab)
					}
					m.Separator()
					if m.Item(tr("获取选项…")).Disabled(disabled).Chosen() {
						a.openFetch(tab)
					}
				}).Height(26).Padding(0, 8).FontSize(12).Shrink(0).Disabled(disabled)
			}
			if tab == nil {
				return
			}
		})
		if empty || tab == nil {
			return
		}
		label := tab.title
		if tab.branch != "" {
			label += "  ·  " + displayBranch(tab.branch)
			if sync := syncBadge(tab.ahead, tab.behind); sync != "" {
				label += " " + sync
			}
		}
		ui.Text(c, label).Bold().SingleLine().MinWidth(0).MaxWidthPercent(50).Tooltip(tab.path).
			ContentTransition(contentFade()).
			Attach(ui.AnchorCenter, ui.AnchorCenter)
	})
}

func sidebarToggleTip(visible bool) string {
	mod := "Ctrl"
	if runtime.GOOS == "darwin" {
		mod = "⌘"
	}
	if visible {
		return tr("隐藏侧边栏 (") + mod + "+B)"
	}
	return tr("显示侧边栏 (") + mod + "+B)"
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).FillWidth().Padding(36, 40, 28, 40).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Width(460).MaxWidthPercent(100).Gap(18).Children(func() {
				ui.Text(c, "Xylring").FontSize(26).Bold().LetterSpacing(-0.4)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c.Key("import-empty"), "").Label(tr("导入仓库")).Children(func() {
						ui.Icon(c, iconFolderPlus).Size(16, 16)
						ui.Text(c, tr("导入仓库"))
					}).Clicked() {
						a.pickRepo()
					}
					if a.activeTab() != nil || len(a.found) > 1 {
						if ui.Button(c.Key("return-open-repo"), tr("返回当前仓库")).Clicked() {
							a.showHome = false
						}
					}
				})
				if a.banner != "" && !a.bannerOnRecentVisible() {
					a.globalAlertBanner(c, nil)
				}
				a.gitNoticeCard(c)
				if len(a.recent) == 0 {
					return
				}
				ui.Column(c).Gap(4).FillWidth().Children(func() {
					ui.Text(c, tr("最近的仓库")).FontSize(12).TextColor(t.TextMuted).Padding(4, 2, 8, 2)
					if len(a.recent) > 8 {
						search := ui.SearchField(c.Key("search-recent"), &a.recentSearch).
							Placeholder(tr("查找最近仓库")).
							Label(tr("查找最近仓库")).
							Bind(&a.recentSearchInput).
							FillWidth().
							Margin(0, 0, 4, 0)
						if search.Submitted() {
							for _, p := range a.recent {
								if matchRecentRepo(p, a.recentSearch) {
									a.openRepo(p)
									break
								}
							}
						}
					} else {
						a.recentSearch = ""
					}
					forget := ""
					matches := 0
					for i, p := range a.recent {
						if !matchRecentRepo(p, a.recentSearch) {
							continue
						}
						matches++
						if a.recentRepoRow(c, i, p) {
							forget = p
						}
					}
					if a.recentSearch != "" && matches == 0 {
						emptyHint(c, "recent-empty", emptyHintProps{
							Icon:  iconSearch,
							Title: tr("未找到匹配的仓库"),
							Action: func() {
								if ui.Button(c.Key("recent-clear-search"), tr("清空搜索")).Clicked() {
									a.recentSearch = ""
								}
							},
						})
					}
					if forget != "" {
						a.forget(forget)
					}
				})
			})
		})
	})
}

func (a *app) recentRepoRow(c *ui.Context, i int, path string) bool {
	t := c.Theme()
	title := repoTitle(path)
	row := ui.ButtonBase(c.Key(fmt.Sprintf("recent-%d", i))).Label(title).Tooltip(path).FillWidth().Padding(8, 10).Gap(10).Radius(t.Radius).Justify(ui.Start).AlignItems(ui.Center).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case row.Pressed():
		row.Background(t.SurfacePressed)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	remove := false
	row.ContextMenu(func(m *ui.Menu) {
		revealName := "在访达中显示"
		if runtime.GOOS == "windows" {
			revealName = "在资源管理器中显示"
		} else if runtime.GOOS != "darwin" {
			revealName = "在文件管理器中显示"
		}
		if m.Item(tr(revealName)).Chosen() {
			if err := revealInFileManager(path); err != nil {
				a.setBanner("定位失败: "+err.Error(), path)
			}
		}
		if m.Item(tr("在终端中打开")).Chosen() {
			if err := openInTerminal(path); err != nil {
				a.setBanner("打开终端失败: "+err.Error(), path)
			}
		}
		if m.Item(tr("复制路径")).Chosen() {
			c.Services().WriteClipboard(path)
			c.Toast(tr("已复制路径"))
		}
		m.Separator()
		if m.Item(tr("从最近列表中移除")).Chosen() {
			remove = true
		}
	})
	row.Children(func() {
		ui.Icon(c, iconFolder).Size(16, 16).TextColor(t.TextMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).SingleLine().MinWidth(0)
			ui.Text(c, homePath(path)).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0)
			if a.banner != "" && a.bannerPath == path {
				ui.Text(c, trMessage(a.banner)).FontSize(12).TextColor(t.Danger)
			}
		})
		if closeIconButton(c, fmt.Sprintf("close-recent-%d", i), tr("关闭 ")+title).Clicked() {
			remove = true
		}
	})
	if remove {
		return true
	}
	if row.Clicked() {
		a.openRepo(path)
	}
	return false
}

func (a *app) bannerOnRecentVisible() bool {
	if !a.bannerOnRecent() {
		return false
	}
	if len(a.recent) > 8 && a.recentSearch != "" {
		return matchRecentRepo(a.bannerPath, a.recentSearch)
	}
	return true
}

func matchRecentRepo(path, query string) bool {
	q := strings.TrimSpace(strings.ToLower(query))
	if q == "" {
		return true
	}
	words := strings.Fields(q)
	title := strings.ToLower(repoTitle(path))
	home := strings.ToLower(homePath(path))
	full := strings.ToLower(path)
	for _, word := range words {
		if !strings.Contains(title, word) && !strings.Contains(home, word) && !strings.Contains(full, word) {
			return false
		}
	}
	return true
}

func (a *app) toggleSidebar() {
	a.sidebarHidden = !a.sidebarHidden
}

func (a *app) toggleBottomFullWidth() {
	a.bottomFullWidth = !a.bottomFullWidth
}

func (a *app) hasInspectBottom(tab *repoTab) bool {
	return tab != nil && (tab.inspecting != zeroHash || tab.comparison != nil || tab.worktree)
}

func (a *app) showRepoSidebar() bool {
	if a.sidebarHidden {
		return false
	}
	return len(a.found) > 0 || len(a.tabs) > 0
}

func (a *app) repoItems() []string {
	if len(a.found) > 0 {
		items := append([]string{}, a.found...)
		seen := map[string]bool{}
		for _, p := range items {
			seen[p] = true
		}
		for _, tab := range a.tabs {
			if !seen[tab.path] {
				items = append(items, tab.path)
			}
		}
		return a.orderRepoItems(items)
	}
	items := make([]string, 0, len(a.tabs))
	for _, tab := range a.tabs {
		items = append(items, tab.path)
	}
	return a.orderRepoItems(items)
}

func (a *app) repoList(c *ui.Context) {
	ui.Themed(c, listTheme(c)).Grow(1).MinWidth(0).FillHeight().MinHeight(0).Padding(6, 8, 8, 8).Children(func() {
		a.sidebarRepoSection(c)
		if tab := a.activeTab(); tab != nil && !tab.unavailable {
			a.ringStatusControl(c, tab, "sidebar-ring-settings")
		}
	})
}

func (a *app) sidebarRepoSection(c *ui.Context) {
	active := ""
	if tab := a.activeTab(); tab != nil {
		active = tab.path
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		items := a.repoItems()
		a.repoSel = -1
		for i, p := range items {
			if p == active {
				a.repoSel = i
				break
			}
		}
		a.repoListState.Selected = &a.repoSel
		a.repoListState.Key = func(i int) any { return items[i] }
		a.repoListState.Reorder = func(rows []int, to int) {
			a.reorderRepos(items, rows, to)
		}
		closeIdx := -1
		hideFound := ""
		list := ui.List(c.Key("repositories"), &a.repoListState, len(items), func(i int) {
			p := items[i]
			label := repoListLabel(a.foundFrom, items, p)
			level := repoNestLevel(items, p)
			tabIdx := -1
			for j, tab := range a.tabs {
				if tab.path == p {
					tabIdx = j
					break
				}
			}
			padL := float32(10) + float32(level)*c.Theme().Space(4)
			branch, dirty, ahead, behind := a.repoStatus(p)
			if tabIdx < 0 || !a.tabs[tabIdx].loaded {
				if !a.isRepoSummaryFresh(p) {
					a.ensureRepoSummary(p)
				}
			}
			branchDisplay := sidebarBranchDisplay(branch, dirty)
			if sync := syncBadge(ahead, behind); sync != "" {
				branchDisplay += " " + sync
			}
			tip := tr("当前分支：") + displayBranch(branch)
			if dirty {
				tip += tr("（工作区有未提交或未跟踪的代码）")
			}
			row := ui.Row(c).FillWidth().MinHeight(36).Padding(8, 10, 8, padL).Gap(6).AlignItems(ui.Center)
			row.ContextMenu(func(m *ui.Menu) {
				revealName := "在访达中显示"
				if runtime.GOOS == "windows" {
					revealName = "在资源管理器中显示"
				} else if runtime.GOOS != "darwin" {
					revealName = "在文件管理器中显示"
				}
				if m.Item(tr(revealName)).Chosen() {
					if err := revealInFileManager(p); err != nil {
						a.setBanner("定位失败: "+err.Error(), p)
					}
				}
				if m.Item(tr("在终端中打开")).Chosen() {
					if err := openInTerminal(p); err != nil {
						a.setBanner("打开终端失败: "+err.Error(), p)
					}
				}
				if m.Item(tr("复制路径")).Chosen() {
					c.Services().WriteClipboard(p)
					c.Toast(tr("已复制路径"))
				}
				m.Separator()
				if m.Item(tr("关闭仓库")).Chosen() {
					if tabIdx >= 0 {
						closeIdx = tabIdx
					} else {
						hideFound = p
					}
				}
			})
			row.Children(func() {
				ui.Text(c, label).FontSize(13).SingleLine().MinWidth(0).Grow(1).Tooltip(p)
				if branchDisplay != "" {
					ui.Row(c).Gap(3).AlignItems(ui.Center).Shrink(0).MaxWidth(130).Tooltip(tip).Children(func() {
						ui.Icon(c, iconGitBranch).Size(12, 12).TextColor(c.Theme().TextMuted)
						ui.Text(c, branchDisplay).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
					})
				}
				if closeIconButton(c, "close-repo:"+p, tr("关闭 ")+label).Clicked() {
					if tabIdx >= 0 {
						closeIdx = tabIdx
					} else {
						hideFound = p
					}
				}
			})
		}).Grow(1).MinHeight(0).Gap(2)
		if closeIdx >= 0 {
			a.closeTab(closeIdx)
		} else if hideFound != "" {
			a.found = slices.DeleteFunc(a.found, func(p string) bool { return p == hideFound })
		} else if list.Changed() && a.repoSel >= 0 && a.repoSel < len(items) {
			a.openRoot(items[a.repoSel])
		}
	})
}

func listTheme(c *ui.Context) *ui.Theme {
	theme := *c.Theme()
	theme.Accent = theme.SurfacePressed
	theme.AccentText = theme.Text
	return &theme
}

func (a *app) globalAlertBanner(c *ui.Context, tab *repoTab) {
	if a.banner == "" {
		return
	}
	if tab != nil && a.bannerPath != "" && a.bannerPath != tab.path {
		return
	}
	t := c.Theme()
	ui.Row(c.Key("global-alert-banner")).FillWidth().Padding(8, 14).Gap(10).AlignItems(ui.Center).
		Background(t.Danger.Alpha(0.1)).Border(1, t.Danger.Alpha(0.28)).Radius(t.Radius).Shrink(0).Transition(fadeEnter()).Children(func() {
		ui.Icon(c, iconAlertCircle).Size(16, 16).TextColor(t.Danger).Shrink(0)
		ui.Text(c, trMessage(a.banner)).FontSize(13).TextColor(t.Danger).Grow(1).MinWidth(0)
		a.gitLogBannerButton(c)
		copyIconButton(c, "banner-copy-err", tr("复制错误信息"), a.banner)
		if closeBtn := closeIconButton(c, "banner-close", tr("关闭提示")); closeBtn.Clicked() {
			a.setBanner("", "")
		}
	})
}

// defaultSidebarWidth 是仓库侧栏的初始宽度，也是底栏文件列表的默认分栏位置。
const defaultSidebarWidth float32 = 320

func (a *app) defaultFileSplit() float32 {
	if a.repoSplit > 0 {
		return a.repoSplit
	}
	return defaultSidebarWidth
}

func (a *app) inspectBottomPane(c *ui.Context, tab *repoTab) {
	if tab == nil {
		return
	}
	ui.Column(c.Key("inspect-bottom")).Grow(1).MinWidth(0).MinHeight(0).Padding(6, 0, 0, 0).Transition(fadeEnter()).Children(func() {
		if tab.fileSplit <= 100 {
			tab.fileSplit = a.defaultFileSplit()
		}
		ui.Split(c.Key(fmt.Sprintf("inspect-h-split-%d", tab.id)), &tab.fileSplit, func() {
			a.inspectFilesPane(c, tab)
		}, func() {
			a.fileDiffPane(c, tab)
		}).SplitReset(a.defaultFileSplit()).Grow(1).MinHeight(0)
	})
}

func (a *app) graphScreen(c *ui.Context) {
	tab := a.activeTab()
	hasInspect := a.hasInspectBottom(tab)

	if a.bottomFullWidth && hasInspect && (tab == nil || tab.contentTab == 0) {
		if tab != nil && tab.diffMaximized {
			ui.Column(c.Key("workspace-main")).Grow(1).MinWidth(0).MinHeight(0).Transition(workspaceMainMotion()).Children(func() {
				a.fileTabBar(c, tab)
				a.globalAlertBanner(c, tab)
				a.inspectBottomPane(c, tab)
			})
			a.maybeInspectOrCompare(tab)
			return
		}

		topSection := func() {
			topMain := func() {
				ui.Column(c.Key("workspace-main")).Grow(1).MinWidth(0).MinHeight(0).Transition(workspaceMainMotion()).Children(func() {
					if tab != nil {
						a.fileTabBar(c, tab)
						a.globalAlertBanner(c, tab)
					}
					ui.Column(c.Key("history-pane")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
						a.graphMain(c, false)
					})
				})
			}
			if !a.showRepoSidebar() {
				topMain()
				return
			}
			if a.repoSplit <= 0 {
				a.repoSplit = defaultSidebarWidth
			}
			ui.Split(c.Key("repo-split"), &a.repoSplit, func() {
				a.repoList(c)
			}, topMain).Grow(1).MinHeight(0)
		}

		bottomSection := func() {
			a.inspectBottomPane(c, tab)
		}

		if tab != nil && tab.detailSplit == 0 {
			tab.detailSplit = 52
		}
		detailSplit := float32(52)
		splitKey := "inspect-split-0"
		splitPtr := &detailSplit
		if tab != nil {
			detailSplit = tab.detailSplit
			splitKey = fmt.Sprintf("inspect-split-%d", tab.id)
			splitPtr = &tab.detailSplit
		}
		ui.SplitVerticalPercent(c.Key(splitKey), splitPtr, topSection, bottomSection).Grow(1).MinHeight(0)
		return
	}

	main := func() {
		ui.Column(c.Key("workspace-main")).Grow(1).MinWidth(0).MinHeight(0).Transition(workspaceMainMotion()).Children(func() {
			if tab := a.activeTab(); tab != nil {
				a.fileTabBar(c, tab)
				a.globalAlertBanner(c, tab)
				if tab.contentTab > 0 && tab.contentTab <= len(tab.fileTabs) {
					fileTab := tab.fileTabs[tab.contentTab-1]
					ui.Column(c.Key(fmt.Sprintf("file-pane-%d", fileTab.id))).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
						a.fileTabPane(c, tab, fileTab)
					})
					return
				}
			}
			ui.Column(c.Key("history-pane")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
				a.graphMain(c, true)
			})
		})
	}
	if !a.showRepoSidebar() {
		main()
		return
	}
	if a.repoSplit <= 0 {
		a.repoSplit = defaultSidebarWidth
	}
	ui.Split(c.Key("repo-split"), &a.repoSplit, func() {
		a.repoList(c)
	}, main).Grow(1).MinHeight(0)
}

func (a *app) graphMain(c *ui.Context, includeBottom bool) {
	t := c.Theme()
	tab := a.activeTab()
	if tab == nil {
		ui.Box(c).Grow(1)
		return
	}
	if tab.unavailable {
		ui.Column(c.Key("repo-unavailable")).Grow(1).Center().Padding(24).Gap(12).Children(func() {
			ui.Text(c, tr("仓库不可用")).Bold().TextColor(t.Danger)
			ui.Text(c, trMessage(tab.err)).FontSize(13).FillWidth().MaxWidth(560)
			if ui.Button(c.Key("repo-retry"), tr("重新检查")).Disabled(tab.loading).Clicked() {
				a.reload(tab)
			}
		})
		return
	}
	if tab.comparison != nil {
		a.comparisonBanner(c, tab)
	}
	a.operationBanner(c, tab)
	a.historyToolbar(c, tab)
	a.commitPane(c, tab, includeBottom)
	ui.Row(c).Padding(4, 12).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
		if tab.loaded && !tab.loading && tab.inspecting == zeroHash && tab.comparison == nil && !tab.worktree {
			ui.Textf(c, tr("%d 条提交"), len(tab.commits)).FontSize(12).TextColor(t.TextMuted)
		}
		if tab.err != "" {
			ui.Text(c, trMessage(tab.err)).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		} else if tab.notice != "" {
			ui.Text(c, trMessage(tab.notice)).FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
		}
	})
}

func (a *app) commitPane(c *ui.Context, tab *repoTab, includeBottom bool) {
	t := c.Theme()
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		busyBlock(c, "history-loading", tr("正在读取提交图谱…"), "").Grow(1)
		return
	}
	if tab.err != "" && len(tab.commits) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Gap(12).Children(func() {
			ui.Text(c, trMessage(tab.err)).TextColor(t.Danger).TextAlign(ui.Center)
			// 仓库读不出来是因为 Git 本身不可用时，这里直接给重试入口。
			if !a.gitReady() {
				if ui.PrimaryButton(c.Key("git-retry-pane"), tr("重新检测")).Disabled(a.git.checking).Clicked() {
					a.checkGit(true)
				}
			}
		})
		return
	}
	if tab.holdAuto {
		tab.holdAuto = false
	} else if tab.hasMore && !tab.loading && tab.err == "" && len(tab.commits) > 0 && tab.list.AtEnd() && tab.canLoadHistory() {
		a.loadMore(tab)
	}
	tab.list.Selected = &tab.selected
	tab.list.Selection = &tab.selection
	tab.list.Key = func(i int) any {
		if commit := tab.rowData(i); commit != nil {
			return commit.hash
		}
		return i
	}
	tab.list.Label = func(i int) string {
		if commit := tab.rowData(i); commit != nil {
			return commit.subject
		}
		return ""
	}
	tab.list.MenuKeepsSelection = true
	tab.list.CellMenu = func(m *ui.Menu, row, col int) {
		a.commitRowMenu(c, m, tab, row)
	}
	columns := a.commitColumns(c, tab)
	if tab.query.filePath != "" {
		columns = columns[1:]
	}
	var pointerY float32
	var pointerOver bool
	rowHovered := func(r ui.Rect) bool {
		return pointerOver && pointerY >= r.Y && pointerY < r.Y+r.H
	}
	graph := func() {
		ui.Column(c.Key("commit-graph-host")).Grow(1).MinWidth(0).MinHeight(0).Transition(commitGraphMotion(tab.id)).Children(func() {
			if tab.rows() == 0 && !tab.loading {
				a.commitListEmpty(c, tab).Grow(1)
				return
			}
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				if tab.query.term != "" {
					a.searchFilterBanner(c, tab)
				}
				ui.Themed(c.Key("commit-selection-theme"), listTheme(c)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
					table := ui.Table(c.Key(fmt.Sprintf("commits-%d", tab.id)), &tab.list, columns, tab.rows(), func(row, col int) {
						a.commitCell(c, tab, row, columns[col].ID, rowHovered)
					}).Grow(1).MinHeight(0).Padding(0, 12)
					_, y, over := table.PointerPosition()
					pointerY, pointerOver = table.Bounds().Y+y, over
					if table.Submitted() && tab.selected >= 0 {
						a.selectCommit(tab, tab.selected)
					}
				})
				a.commitTableFooter(c, tab)
			})
		})
	}
	if includeBottom && a.hasInspectBottom(tab) {
		bottom := func() {
			a.inspectBottomPane(c, tab)
		}
		if tab.diffMaximized {
			bottom()
		} else {
			if tab.detailSplit == 0 {
				tab.detailSplit = 52
			}
			ui.SplitVerticalPercent(c.Key(fmt.Sprintf("inspect-split-%d", tab.id)), &tab.detailSplit, graph, bottom).Grow(1).MinHeight(0)
		}
	} else {
		graph()
	}
	a.maybeInspectOrCompare(tab)
}

// commitListEmpty says why the graph has no rows, and how to widen a filter
// that hid them, instead of leaving one sentence in the middle of the pane.
func (a *app) commitListEmpty(c *ui.Context, tab *repoTab) ui.Element {
	p := emptyHintProps{
		Icon:  iconGitCommit,
		Title: tr("这个仓库还没有提交"),
	}
	switch {
	case tab.query.term != "":
		p = emptyHintProps{
			Icon:  iconSearch,
			Title: tr("没有匹配的提交"),
			Action: func() {
				if ui.Button(c.Key("empty-leave-search"), tr("退出筛选")).Clicked() {
					a.leaveSearch(tab)
				}
			},
		}
	case tab.query.filePath != "":
		p = emptyHintProps{
			Icon:  iconHistory,
			Title: tr("这个文件在所选版本之前没有历史"),
		}
	case tab.query.ref != "" || tab.query.hideRemote:
		p = emptyHintProps{
			Icon:  iconGitBranch,
			Title: tr("当前范围没有提交"),
		}
	}
	return emptyHint(c, "commits-empty", p)
}

func (a *app) searchFilterBanner(c *ui.Context, tab *repoTab) {
	th := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("search-banner-%d", tab.id))).
		Margin(0, 12, 6, 12).
		Padding(6, 12).
		Transition(fadeEnter()).
		Background(th.Accent.Alpha(0.08)).
		Border(1, th.Accent.Alpha(0.2)).
		Radius(th.Radius).
		Gap(8).
		AlignItems(ui.Center).
		Shrink(0).
		Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(th.Accent).Shrink(0)
			msg := fmt.Sprintf(tr("正在按%s筛选：“%s”"), tr(tab.query.kind), tab.query.term)
			ui.Text(c, msg).FontSize(12).TextColor(th.Text).Grow(1).SingleLine()
		})
}

func (a *app) commitTableFooter(c *ui.Context, tab *repoTab) {
	if tab == nil || len(tab.commits) == 0 {
		return
	}
	theme := c.Theme()
	if tab.loading {
		ui.Row(c.Key("commit-footer-loading")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Surface.Alpha(0.6)).Transition(fadeEnter()).Children(func() {
			busyRow(c, "history-footer-busy", tr("正在加载更多历史提交…"), "")
		})
		return
	}
	if tab.hasMore && !tab.canLoadHistory() {
		ui.Row(c.Key("commit-footer-budget")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Surface.Alpha(0.6)).Children(func() {
			ui.Text(c, tr("还有更多历史")).FontSize(12).TextColor(theme.TextMuted)
			if ui.Button(c.Key("history-continue"), tr("继续加载")).Shrink(0).Clicked() {
				tab.historyBudget += historyMemoryStep
				tab.notice = ""
				a.loadMore(tab)
			}
		})
		return
	}
	if tab.err != "" && len(tab.commits) > 0 {
		ui.Row(c.Key("commit-footer-err")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Danger.Alpha(0.1)).Children(func() {
			ui.Text(c, trMessage(tab.err)).FontSize(12).TextColor(theme.Danger)
			if ui.Button(c.Key("history-retry"), tr("重试")).Shrink(0).Clicked() {
				tab.err = ""
				a.loadMore(tab)
			}
		})
		return
	}
}

type commitRefBadge struct {
	kind    refKind
	name    string
	remotes []string
}

func groupCommitRefs(refs []refInfo, hideRemote bool) []commitRefBadge {
	var locals []string
	localRemotes := map[string][]string{}
	localSet := map[string]bool{}
	for _, ref := range refs {
		if ref.kind == refBranch {
			locals = append(locals, ref.name)
			localSet[ref.name] = true
		}
	}
	var standaloneRemotes []string
	var tags []string
	for _, ref := range refs {
		switch ref.kind {
		case refRemote:
			if hideRemote {
				continue
			}
			remote, local, ok := strings.Cut(ref.name, "/")
			if ok && local != "" && local != "HEAD" && localSet[local] {
				localRemotes[local] = append(localRemotes[local], remote)
			} else {
				standaloneRemotes = append(standaloneRemotes, ref.name)
			}
		case refTag:
			tags = append(tags, ref.name)
		}
	}
	var out []commitRefBadge
	for _, name := range locals {
		out = append(out, commitRefBadge{
			kind:    refBranch,
			name:    name,
			remotes: localRemotes[name],
		})
	}
	for _, name := range standaloneRemotes {
		out = append(out, commitRefBadge{
			kind: refRemote,
			name: name,
		})
	}
	for _, name := range tags {
		out = append(out, commitRefBadge{
			kind: refTag,
			name: name,
		})
	}
	return out
}

func (b commitRefBadge) label() string {
	switch b.kind {
	case refTag:
		return tr("标签 ") + b.name
	case refBranch:
		if len(b.remotes) > 0 {
			return b.name + " · " + strings.Join(b.remotes, ",")
		}
		return b.name
	default:
		return b.name
	}
}

// iconBadge 是只有图标的类型徽章，行里用来标同步状态。
func iconBadge(c *ui.Context, key string, icon *ui.SVG, color ui.Color, tip string) ui.Element {
	e := ui.Row(c.Key(key)).AlignItems(ui.Center).Shrink(0).Padding(1, 2).Radius(4).
		Background(color.Alpha(0.14)).Border(1, color.Alpha(0.45)).Tooltip(tip)
	e.Children(func() {
		ui.Icon(c, icon).Size(12, 12).TextColor(color)
	})
	return e
}

func (a *app) commitCell(c *ui.Context, tab *repoTab, i int, column string, rowHovered func(ui.Rect) bool) {
	t := c.Theme()
	item := tab.rowData(i)
	if item == nil {
		return
	}
	selected := tab.selection.Has(item.hash)
	muted := t.TextMuted
	if selected {
		muted = t.Text.Alpha(0.75)
	}
	worktree := item.hash == worktreeHash
	if column == "graph" {
		padX, padY := c.Theme().Space(graphCellPadX), c.Theme().Space(graphCellPadY)
		background := t.Background
		if selected {
			background = t.Accent
		}
		ui.Box(c).Grow(1).MinHeight(28).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
			full := ui.Rect{X: r.X - padX, Y: r.Y - padY, W: r.W + padX*2, H: r.H + padY*2}
			bg := background
			if !selected && rowHovered(full) {
				bg = t.SurfaceHover
			}
			if worktree {
				paintWorktreeCommit(p, full, item, bg)
				return
			}
			paintCommit(p, full, item, tab.worktreeConnectsFromTop(i), nil, bg, tab.provenance.get(item.prov).attribution == "inferred")
			if tab.worktreeThrough(i) {
				if h := tab.worktreeHead(); h != nil {
					x := laneCenter(full, h.nodeCol)
					p.Line(x, full.Y-1, x, full.Y+full.H+1, graphStroke, graphColor(h.nodeColor, nil))
				}
			}
		})
		return
	}
	padEnd := float32(8)
	if column == "hash" {
		padEnd = 16
	}
	cellRow := ui.Row(c).Fill().MinHeight(28).Padding(2, padEnd, 2, 8).Gap(6).AlignItems(ui.Center)
	if !worktree && (tab.query.term != "" || tab.query.filePath != "") {
		commitHash := item.hash
		if cellRow.RowDoubleClicked() {
			a.locateCommitInGraph(tab, commitHash)
		}
	}
	cellRow.Children(func() {
		color := graphColor(item.nodeColor, nil)
		switch column {
		case "subject":
			if worktree {
				ui.Text(c, tr("未提交改动")).SingleLine().Shrink(0)
				for _, label := range worktreeBadges(tab.worktreeFiles) {
					chip(c, "worktree-badge-"+label, label, chipStyle{Text: color, Background: color.Alpha(0.12)})
				}
				ui.Box(c).Grow(1).MinWidth(0)
				return
			}
			if item.hash == tab.head && tab.head != zeroHash {
				chip(c, "chip-head", "HEAD", chipStyle{Text: ui.RGBA(255, 255, 255, 1), Background: color, Bold: true}).
					Tooltip(tr("当前检出位置 (HEAD)"))
			}
			badges := groupCommitRefs(tab.refs[item.hash], tab.query.hideRemote)
			for idx, badge := range badges {
				if idx == 4 {
					var rest []string
					for _, b := range badges[4:] {
						rest = append(rest, b.label())
					}
					ui.Text(c, "…").TextColor(muted).Shrink(0).Tooltip(strings.Join(rest, "\n"))
					break
				}
				b := badge
				commitCopy := *item
				switch b.kind {
				case refBranch:
					tip := tr("本地分支 ") + b.name + tr("（右键更多操作）")
					if len(b.remotes) > 0 {
						tip = tr("本地分支 ") + b.name + tr(" · 同步远端 ") + strings.Join(b.remotes, ", ") + tr("（右键更多操作）")
					}
					pill := chipSoft(c, fmt.Sprintf("ref-branch-%s-%s", hashHex(item.hash), b.name), b.name, color, true).
						Cursor(ui.CursorPointer).Tooltip(tip)
					pill.ContextMenu(func(m *ui.Menu) {
						a.branchBadgeMenu(c, m, tab, commitCopy, b.name)
					})
					if len(b.remotes) > 0 {
						pill.Children(func() {
							ui.Text(c, "· "+strings.Join(b.remotes, ",")).FontSize(11).SingleLine().TextColor(muted)
						})
					}
				case refRemote:
					pill := chip(c, fmt.Sprintf("ref-remote-%s-%s", hashHex(item.hash), b.name), b.name,
						chipStyle{Text: muted, Background: t.Surface, Border: t.Border}).
						Cursor(ui.CursorPointer).Tooltip(tr("远程分支 ") + b.name + tr("（双击检出跟踪分支，右键更多操作）"))
					if pill.RowDoubleClicked() {
						a.openCommitWrite(tab, writeBranch, commitCopy)
						a.write.track = true
						a.write.trackRemote = b.name
						if local := remoteLocalName(b.name); local != "" {
							a.write.name = local
						}
						a.write.checkout = true
					}
					pill.ContextMenu(func(m *ui.Menu) {
						a.remoteBadgeMenu(c, m, tab, commitCopy, b.name)
					})
				case refTag:
					pill := chipSoft(c, fmt.Sprintf("ref-tag-%s-%s", hashHex(item.hash), b.name), tr("标签 ")+b.name, t.Warning, false).
						Cursor(ui.CursorPointer).Tooltip(tr("标签 ") + b.name + tr("（右键更多操作）"))
					pill.ContextMenu(func(m *ui.Menu) {
						a.tagBadgeMenu(c, m, tab, commitCopy, b.name)
					})
				}
			}
			if tab.outgoing[item.hash] {
				iconBadge(c, "sync-out-"+hashHex(item.hash), iconUpload, t.Accent, tr("待推送"))
			} else if tab.incoming[item.hash] {
				iconBadge(c, "sync-in-"+hashHex(item.hash), iconDownload, t.TextMuted, tr("远端独有"))
			}
			if prov := tab.provenance.get(item.prov); prov.agent != "" {
				label := prov.badge
				if cW, _ := c.Size(); cW < 900 {
					label = "◎ " + prov.agent
					if item.prov == 1 {
						label = prov.badge
					}
				}
				ui.Text(c, label).SingleLine().MaxWidth(220).Shrink(0).FontSize(11).TextColor(muted).Tooltip(prov.description()).ContextMenu(func(m *ui.Menu) {
					if m.Item(tr("按此 Agent 筛选")).Chosen() {
						tab.searchKind = "Agent"
						tab.searchText = prov.agent
						a.searchHistory(tab)
					}
					if prov.model != "" && m.Item(tr("按此模型筛选")).Chosen() {
						tab.searchKind = "模型"
						tab.searchText = normalizeModel(prov.model)
						a.searchHistory(tab)
					}
				})
			}
			text := item.subject
			if text == "" {
				text = tr("（无说明）")
			}
			e := ui.Text(c, text).SingleLine().Grow(1).MinWidth(0)
			if len(item.parents) > 1 {
				e.TextColor(muted)
			}
		case "date":
			text := item.whenText()
			if worktree {
				text = tr("现在")
			}
			ui.Text(c, text).SingleLine().TextColor(muted)
		case "author":
			ui.Text(c, item.author).SingleLine().TextColor(muted)
		case "hash":
			if worktree {
				ui.Text(c, tr("工作区")).SingleLine().TextColor(muted)
				return
			}
			full := hashHex(item.hash)
			btn := ui.ButtonBase(c.Key("copy-hash-" + full)).Label(tr("复制哈希")).Tooltip(full).Cursor(ui.CursorPointer).Transition(colorMotion())
			btn.Children(func() {
				ui.Text(c, shortHash(item.hash)).Font("monospace").SingleLine().TextColor(muted)
			})
			if btn.Clicked() {
				c.Services().WriteClipboard(full)
				c.Toast(tr("已复制哈希"))
			}
		}
	})
}

func parsePalette(s string) ([]ui.Color, error) {
	var colors []ui.Color
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimPrefix(strings.TrimSpace(part), "#")
		b, err := hex.DecodeString(part)
		if err != nil || len(b) != 3 {
			return nil, fmt.Errorf("图谱颜色须为逗号分隔的 #RRGGBB")
		}
		colors = append(colors, ui.RGBA(b[0], b[1], b[2], 1))
	}
	if len(colors) == 0 {
		return nil, fmt.Errorf("没有可用的图谱颜色")
	}
	return colors, nil
}

func (a *app) commitColumns(c *ui.Context, tab *repoTab) []ui.TableColumn {
	winW, _ := c.Size()
	mainW := winW
	if a.showRepoSidebar() {
		split := a.repoSplit
		if split <= 0 {
			split = 320
		}
		mainW -= split
	}
	if mainW > 0 && mainW < 700 {
		return []ui.TableColumn{
			{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 40},
			{ID: "subject", Title: tr("描述 / 引用"), MinWidth: 160},
			{ID: "date", Title: tr("日期"), Width: 128, MinWidth: 100},
			{ID: "author", Title: tr("作者"), Width: 96, MinWidth: 60},
			{ID: "hash", Title: tr("哈希"), Width: 84, MinWidth: 70},
		}
	}
	return []ui.TableColumn{
		{ID: "graph", Title: tr("图谱"), Width: graphWidth(tab.graph.lanes()), MinWidth: 40},
		{ID: "subject", Title: tr("描述 / 引用"), MinWidth: 200},
		{ID: "date", Title: tr("日期"), Width: 172, MinWidth: 128},
		{ID: "author", Title: tr("作者"), Width: 156, MinWidth: 96},
		{ID: "hash", Title: tr("哈希"), Width: 120, MinWidth: 96},
	}
}

func graphWidth(lanes int) float32 {
	if lanes < 1 {
		lanes = 1
	}
	return lanePad*2 + float32(lanes)*laneGap
}

func laneCenter(r ui.Rect, col int) float32 {
	return r.X + lanePad + float32(col)*laneGap + laneGap/2
}

func laneColor(col int) ui.Color {
	if col < 0 {
		col = 0
	}
	return laneColors[col%len(laneColors)]
}

func graphColor(chain int, palette []ui.Color) ui.Color {
	if len(palette) > 0 {
		return palette[max(0, chain)%len(palette)]
	}
	return laneColor(chain)
}

func paintCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool, palette []ui.Color, background ui.Color, inferred ...bool) {
	mid := r.Y + r.H/2
	// 长泳道在下，短曲线带背景色衬底跨过泳道，最后再画节点。
	for _, bends := range []bool{false, true} {
		for _, s := range c.segs {
			if (s.bend != 0) != bends {
				continue
			}
			path := lanePath(r, s)
			if bends {
				p.StrokePath(&path, graphStroke+3, background)
			}
			p.StrokePath(&path, graphStroke, graphColor(s.color, palette))
		}
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y-1, x, mid, graphStroke, graphColor(c.nodeColor, palette))
	}
	radius := commitRadius(c)
	if c.prov != 0 {
		var ring ui.Path
		x, y, rr := laneCenter(r, c.nodeCol), mid, min(radius+2.5, maxRingRadius)
		if len(inferred) > 0 && inferred[0] {
			for i := 0; i < 12; i++ {
				for j := 0; j <= 4; j++ {
					angle := (float64(i) + float64(j)/8) * math.Pi / 6
					px, py := x+rr*float32(math.Cos(angle)), y+rr*float32(math.Sin(angle))
					if j == 0 {
						ring.MoveTo(px, py)
					} else {
						ring.LineTo(px, py)
					}
				}
			}
		} else {
			ring.Circle(x, y, rr)
		}
		p.StrokePath(&ring, 1.2, graphColor(c.nodeColor, palette).Alpha(0.45))
	}

	paintGraphNode(p, laneCenter(r, c.nodeCol), mid, radius, graphColor(c.nodeColor, palette), background, len(c.parents) > 1)
}

// commitRadius 用新增+删除的行数表示改动量。
// 合并提交和文件历史拿不到 shortstat，用中间档的基准大小。
func commitRadius(c *commit) float32 {
	switch {
	case c.lines < 0:
		return 4
	case c.lines == 0:
		return 3
	case c.lines <= 10:
		return 3.5
	case c.lines <= 50:
		return 4
	case c.lines <= 200:
		return 4.5
	case c.lines <= 1000:
		return 5
	default:
		return 6
	}
}

// 每次换道用一个行高的三次贝塞尔。两行分别画下半段和上半段，
// 行边界处位置和切线相同，节点处则沿泳道竖直进出。
func lanePath(r ui.Rect, s seg) ui.Path {
	x0, x1 := laneCenter(r, s.a), laneCenter(r, s.b)
	mid, bottom := r.Y+r.H/2, r.Y+r.H
	var path ui.Path
	switch s.bend {
	case 1:
		path.MoveTo(x0, mid)
		path.CubeTo(x0, mid+r.H/4, x0+(x1-x0)/4, bottom-r.H/8, (x0+x1)/2, bottom)
	case 2:
		path.MoveTo((x0+x1)/2, r.Y)
		path.CubeTo(x1-(x1-x0)/4, r.Y+r.H/8, x1, r.Y+r.H/4, x1, mid)
	default:
		y0, y1 := mid, mid
		if s.top {
			y0 = r.Y - 1
		}
		if s.bot {
			y1 = bottom + 1
		}
		path.MoveTo(x0, y0).LineTo(x1, y1)
	}
	return path
}

func paintWorktreeCommit(p *ui.Painter, r ui.Rect, c *commit, background ui.Color) {
	mid := r.Y + r.H/2
	x := laneCenter(r, c.nodeCol)
	color := graphColor(c.nodeColor, nil)
	if len(c.segs) > 0 && c.segs[0].bot {
		p.Line(x, mid, x, r.Y+r.H+1, graphStroke, color)
	}
	paintWorktreeNode(p, x, mid, 5, color, background)
}

func paintWorktreeNode(p *ui.Painter, x, y, radius float32, color, background ui.Color) {
	var halo ui.Path
	halo.Circle(x, y, radius+2)
	p.FillPath(&halo, background)
	size := radius * 1.65
	p.Fill(ui.Rect{X: x - size/2, Y: y - size/2, W: size, H: size}, color, 2)
}

func paintGraphNode(p *ui.Painter, x, y, radius float32, color, background ui.Color, hollow bool) {
	var halo ui.Path
	halo.Circle(x, y, radius+2)
	p.FillPath(&halo, background)
	var node ui.Path
	if hollow {
		node.Circle(x, y, radius-1)
		p.StrokePath(&node, 2, color)
	} else {
		node.Circle(x, y, radius)
		p.FillPath(&node, color)
	}
}

func main() {
	if handleCLIArgs(os.Args[1:]) {
		return
	}
	// 保证单实例运行：若已有正在运行的实例，将命令行参数与工作目录转发并立即退出
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	startLogging()
	a := newApp()

	// 监听后续实例发送的命令行唤醒（如终端中输入 xylring .）
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		a.post(func() {
			if a.win != nil {
				a.win.Focus()
			}
			mygo.App.Focus()
			paths := normalizePaths(args, workingDir)
			for _, p := range paths {
				a.openPath(p)
			}
		})
	})

	// 监听系统级文件/目录打开事件（如 macOS open -a Xylring <path> 或访达拖拽）
	mygo.App.OnOpenFile(func(path string) {
		a.post(func() {
			if a.win != nil {
				a.win.Focus()
			}
			mygo.App.Focus()
			if path != "" {
				a.openPath(path)
			}
		})
	})

	mygo.App.WhenReady(func() {
		a.configureLanguages()
		a.installMenu()
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "Xylring",
			Width:          1200,
			Height:         800,
			MinWidth:       860,
			MinHeight:      560,
			StateKey:       "main",
			TitleBarStyle:  mygo.TitleBarHiddenInset,
			TitleBarHeight: chromeMinHeight,
			Content:        ui.View(a.view),
		})
		paths := normalizePaths(os.Args[1:], "")
		if len(paths) == 0 {
			a.restoreSession()
		} else {
			a.sessionReady = true
		}
		// 先把 Git 探明白，缺 Git 时首页直接说清楚原因。
		a.checkGit(false)
		for _, path := range paths {
			a.openPath(path)
		}
		a.startAutoRefresh()
		a.win.OnFocus(a.focusRepo)
		a.win.OnClosed(func() {
			a.queueSession()
			if a.sessionWrites != nil {
				close(a.sessionWrites)
				a.sessionWrites = nil
			}
			a.closed = true
			if a.scanCancel != nil {
				a.scanCancel()
			}
			if a.recentWrites != nil {
				close(a.recentWrites)
				a.recentWrites = nil
			}
			if a.summaryCancel != nil {
				a.summaryCancel()
			}
			if a.watchCancel != nil {
				a.watchCancel()
			}
			for _, tab := range a.tabs {
				tab.closed = true
				tab.cancel()
				a.closeAllFileTabs(tab)
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	if a.sessionWrites != nil {
		a.queueSession()
		close(a.sessionWrites)
		a.sessionWrites = nil
	}
	if a.sessionDone != nil {
		<-a.sessionDone
	}
	log.Print("stopped")
	crashlog.MarkClean()
}

func commandModifier() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl
}
