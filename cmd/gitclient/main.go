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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

const (
	laneGap     = 18
	lanePad     = 10
	graphStroke = 2.5
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
	win      *mygo.Window
	menu     *mygo.Menu
	dispatch func(func())

	tabs              []*repoTab
	active            int
	nextID            int
	recent            []string
	recentSearch      string
	recentSearchInput ui.Handle
	persist           bool
	picking           bool
	banner        string
	bannerPath    string
	pendingToast  string
	found         []string
	foundFrom     string
	foundGen      int
	closed        bool
	watchCancel   context.CancelFunc
	scanCancel    context.CancelFunc
	scanPath      string
	scanCache     map[string]scannedDir
	recentWrites  chan []string
	repoListState  ui.ListState
	repoSel        int
	repoSplit      float32
	sidebarSplit   float32
	sidebarHidden  bool
	showHome       bool
	summaryMu      sync.Mutex
	repoSummaries  map[string]repoSummary
	summaryRunning map[string]bool

	showCompare  bool
	compareFrom  string
	compareTo    string
	compareErr   string
	compareThree bool
	fileView        int // 0 list, 1 tree
	sidebarFileView int // 0 list, 1 tree
	diffLayout      int // 0 split, 1 unified
	diffIgnoreWhitespace bool

	showWrite bool
	write     commitWrite

	showFetch bool
	fetch     fetchDialog

	showDiscard bool
	discard     discardDialog
}

type discardDialog struct {
	open bool
	file fileChange
}

func newApp() *app {
	return &app{
		recent:         loadRecent(),
		persist:        true,
		repoSummaries:  make(map[string]repoSummary),
		summaryRunning: make(map[string]bool),
	}
}

func (a *app) view(c *ui.Context) {
	a.handleShortcuts(c)
	a.syncMenu()
	if a.pendingToast != "" {
		c.Toast(a.pendingToast)
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
			a.emptyState(c)
		} else {
			a.graphScreen(c)
		}
		if isDropOver {
			ui.Box(c.Key("drop-overlay")).Absolute().Fill().
				Background(t.Background.Alpha(0.92)).
				Border(2, t.Accent).
				Radius(t.Radius).
				Children(func() {
					ui.Column(c).Absolute().Fill().Center().Gap(10).Children(func() {
						ui.Box(c).Size(56, 56).Radius(28).Background(t.Accent.Alpha(0.15)).Center().Children(func() {
							ui.Icon(c, iconFolderPlus).Size(28, 28).TextColor(t.Accent)
						})
						ui.Text(c, "释放以打开仓库").FontSize(18).Bold().TextColor(t.Text)
						ui.Text(c, "拖拽 Git 仓库文件夹直接在此窗口打开").FontSize(13).TextColor(t.TextMuted)
					})
				})
		}
	})
	a.comparisonModal(c)
	a.commitWriteModal(c)
	a.fetchModal(c)
	a.discardModal(c)
}

func (a *app) handleShortcuts(c *ui.Context) {
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
			if c.Shortcut(commandModifier(), ui.KeyEnter) && strings.TrimSpace(tab.commitMsg) != "" {
				a.runCommitWorktree(tab)
			}
			if c.Shortcut(commandModifier(), ui.KeyW) {
				a.closeCurrentTab()
				return
			}
			if !tab.loading && !a.showCompare && c.Shortcut(commandModifier(), ui.KeyD) {
				a.openCompareModal(tab)
			}
		}
	}
	tab = a.activeTab()
	if !empty && tab != nil && tab.contentTab > 0 {
		return
	}
	if !empty && tab != nil && !a.showCompare && (c.Shortcut(commandModifier(), ui.KeyF) || c.Shortcut(commandModifier(), ui.KeyP) || c.Shortcut(commandModifier(), ui.KeyK)) {
		if tab.query.filePath != "" {
			a.leaveFileHistory(tab)
		}
		tab.searchSuggestOpen = true
		tab.searchInput.Focus(c)
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
	ui.Box(c).Height(h).Shrink(0).DragWindow().Children(func() {
		if !empty && a.showRepoSidebar() && a.repoSplit > 0 {
			ui.Box(c).Absolute().Left(a.repoSplit - 1).Top(0).Bottom(0).Width(1).Background(c.Theme().Border)
		}
		if !empty {
			btnY := (h - 26) / 2
			if runtime.GOOS == "darwin" && bar.Left > 0 {
				btnY = 13
			}
			btnX := padL
			btn := chromeIconButton(c, "toggle-sidebar", "切换侧边栏", iconPanelLeft).
				Absolute().Left(btnX).Top(btnY).
				Tooltip(sidebarToggleTip(a.showRepoSidebar()))
			if btn.Clicked() {
				a.toggleSidebar()
			}
		}
		ui.Row(c).Fill().Padding(0, padR, 0, padL).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Grow(1)
			if empty {
				return
			}
			if tab != nil {
				pullTip := "从远程拉取更新 (git pull)"
				if tab.behind > 0 {
					pullTip = fmt.Sprintf("从远程拉取更新 (git pull) [落后 %d 个提交]", tab.behind)
				}
				if chromeIconButton(c, "topbar-pull", "Pull", iconArrowDown).
					Disabled(tab.loading || a.write.busy || tab.syncBusy).
					Tooltip(pullTip).
					Clicked() {
					a.quickPull(tab)
				}

				pushTip := "推送到远程 (git push)"
				if tab.ahead > 0 {
					pushTip = fmt.Sprintf("推送到远程 (git push) [领先 %d 个提交]", tab.ahead)
				}
				if chromeIconButton(c, "topbar-push", "Push", iconUpload).
					Disabled(tab.loading || a.write.busy || tab.syncBusy).
					Tooltip(pushTip).
					Clicked() {
					a.quickPush(tab)
				}
			}
			if chromeIconButton(c, "import", "导入仓库", iconFolderPlus).Clicked() {
				a.pickRepo()
			}
			if tab == nil {
				return
			}
			if chromeIconButton(c, "home", "返回首页", iconHome).Clicked() {
				a.goHome()
			}
		})
		if empty || tab == nil {
			return
		}
		label := tab.title
		if tab.branch != "" {
			label += "  ·  " + tab.branch
			if sync := syncBadge(tab.ahead, tab.behind); sync != "" {
				label += " " + sync
			}
		}
		ui.Text(c, label).Bold().SingleLine().MinWidth(0).MaxWidthPercent(50).Tooltip(tab.path).
			Attach(ui.AnchorCenter, ui.AnchorCenter)
	})
}

func sidebarToggleTip(visible bool) string {
	mod := "Ctrl"
	if runtime.GOOS == "darwin" {
		mod = "⌘"
	}
	if visible {
		return "隐藏侧边栏 (" + mod + "+B)"
	}
	return "显示侧边栏 (" + mod + "+B)"
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).FillWidth().Padding(36, 40, 28, 40).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Width(460).MaxWidthPercent(100).Gap(18).Children(func() {
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, "Xylring").FontSize(26).Bold().LetterSpacing(-0.4)
					ui.Text(c, "打开本地仓库，浏览提交图谱").TextColor(t.TextMuted)
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c.Key("import-empty"), "").Label("导入仓库").Children(func() {
						ui.Icon(c, iconFolderPlus).Size(16, 16)
						ui.Text(c, "导入仓库")
					}).Clicked() {
						a.pickRepo()
					}
					if a.activeTab() != nil || len(a.found) > 1 {
						if ui.Button(c.Key("return-open-repo"), "返回当前仓库").Clicked() {
							a.showHome = false
						}
					}
					ui.Text(c, "支持直接拖拽文件夹至窗口").FontSize(12).TextColor(t.TextMuted)
				})
				if a.banner != "" && !a.bannerOnRecentVisible() {
					a.globalAlertBanner(c, nil)
				}
				if len(a.recent) == 0 {
					return
				}
				ui.Column(c).Gap(4).FillWidth().Children(func() {
					ui.Text(c, "最近的仓库").FontSize(12).TextColor(t.TextMuted).Padding(4, 2, 8, 2)
					if len(a.recent) > 8 {
						search := ui.SearchField(c.Key("search-recent"), &a.recentSearch).
							Placeholder("查找最近仓库").
							Label("查找最近仓库").
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
						ui.Text(c, "未找到匹配的仓库").FontSize(13).TextColor(t.TextMuted).Padding(12, 10)
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
	row := ui.ButtonBase(c.Key(fmt.Sprintf("recent-%d", i))).Label(title).Tooltip(path).FillWidth().Padding(8, 10).Gap(10).Radius(t.Radius).Justify(ui.Start).AlignItems(ui.Center).Cursor(ui.CursorPointer)
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
		if m.Item(revealName).Chosen() {
			if err := revealInFileManager(path); err != nil {
				a.setBanner("定位失败: "+err.Error(), path)
			}
		}
		if m.Item("在终端中打开").Chosen() {
			if err := openInTerminal(path); err != nil {
				a.setBanner("打开终端失败: "+err.Error(), path)
			}
		}
		if m.Item("复制路径").Chosen() {
			c.Services().WriteClipboard(path)
			c.Toast("已复制路径")
		}
		m.Separator()
		if m.Item("从最近列表中移除").Chosen() {
			remove = true
		}
	})
	row.Children(func() {
		ui.Icon(c, iconFolder).Size(16, 16).TextColor(t.TextMuted).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).SingleLine().MinWidth(0)
			ui.Text(c, homePath(path)).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0)
			if a.banner != "" && a.bannerPath == path {
				ui.Text(c, a.banner).FontSize(12).TextColor(t.Danger)
			}
		})
		if closeIconButton(c, fmt.Sprintf("close-recent-%d", i), "关闭 "+title).Clicked() {
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
		return items
	}
	items := make([]string, 0, len(a.tabs))
	for _, tab := range a.tabs {
		items = append(items, tab.path)
	}
	return items
}

func (a *app) repoList(c *ui.Context) {
	tab := a.activeTab()
	if a.sidebarSplit <= 0 {
		a.sidebarSplit = 35
	}
	ui.Themed(c, listTheme(c)).Grow(1).MinWidth(0).FillHeight().MinHeight(0).Padding(12, 10).Children(func() {
		ui.SplitVerticalPercent(c.Key("sidebar-vertical-split"), &a.sidebarSplit, func() {
			a.sidebarRepoSection(c)
		}, func() {
			a.sidebarInspectSection(c, tab)
		}).Grow(1).MinHeight(0)
	})
}

func (a *app) sidebarInspectSection(c *ui.Context, tab *repoTab) {
	if tab == nil {
		ui.Box(c).Grow(1)
		return
	}
	a.sidebarWorktreeSection(c, tab)
}

func (a *app) sidebarRepoSection(c *ui.Context) {
	active := ""
	if tab := a.activeTab(); tab != nil {
		active = tab.path
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		ui.Row(c).FillWidth().Padding(4, 10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "仓库").FontSize(12).TextColor(c.Theme().TextMuted)
			ui.Box(c).Grow(1)
			if len(a.recent) > 0 {
				btn := ui.ButtonBase(c.Key("sidebar-recent-btn")).Padding(2, 6).Radius(c.Theme().Radius).Cursor(ui.CursorPointer).
					Tooltip("打开最近的仓库（右键快速选择）")
				if btn.Hovered() {
					btn.Background(c.Theme().SurfaceHover)
				}
				btn.ContextMenu(func(m *ui.Menu) {
					for _, rp := range a.recent {
						repoPath := rp
						if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Chosen() {
							a.openRepo(repoPath)
						}
					}
				})
				btn.Children(func() {
					ui.Text(c, "最近").FontSize(11).TextColor(c.Theme().TextMuted)
				})
				if btn.Clicked() {
					a.goHome()
				}
			}
		})
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
			tip := "当前分支：" + branch
			if dirty {
				tip += "（工作区有未提交或未跟踪的代码）"
			}
			row := ui.Row(c).FillWidth().MinHeight(36).Padding(8, 10, 8, padL).Gap(6).AlignItems(ui.Center)
			row.ContextMenu(func(m *ui.Menu) {
				revealName := "在访达中显示"
				if runtime.GOOS == "windows" {
					revealName = "在资源管理器中显示"
				} else if runtime.GOOS != "darwin" {
					revealName = "在文件管理器中显示"
				}
				if m.Item(revealName).Chosen() {
					if err := revealInFileManager(p); err != nil {
						a.setBanner("定位失败: "+err.Error(), p)
					}
				}
				if m.Item("在终端中打开").Chosen() {
					if err := openInTerminal(p); err != nil {
						a.setBanner("打开终端失败: "+err.Error(), p)
					}
				}
				if m.Item("复制路径").Chosen() {
					c.Services().WriteClipboard(p)
					c.Toast("已复制路径")
				}
				m.Separator()
				if m.Item("关闭仓库").Chosen() {
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
				if closeIconButton(c, "close-repo:"+p, "关闭 "+label).Clicked() {
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

func (a *app) openWorktreeFileHistory(t *repoTab, f fileChange) {
	if t.query.filePath != "" || t.head == zeroHash {
		return
	}
	t.fileBack = t.location()
	q := historyQuery{filePath: f.path, fileAt: t.head}
	a.changeHistory(t, q)
}

func (a *app) sidebarWorktreeHoverActions(c *ui.Context, t *repoTab, f fileChange, isSelected bool, hovered bool) {
	if !hovered {
		return
	}
	theme := c.Theme()
	bg := theme.SurfaceHover
	if isSelected {
		bg = theme.Accent
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Padding(4, 6).Background(bg).Radius(0, theme.Radius, theme.Radius, 0).Label("文件操作").Absolute().Top(0).Right(0).Bottom(0).Children(func() {
		if f.extra == "已暂存" || f.extra == "重命名" {
			fileIconButton(c, "sidebar-unstage-"+f.path, "取消暂存", iconMinus).Tooltip("取消暂存 (Unstage)").OnClick(func() {
				a.unstageWorktreeFile(t, f.path)
			})
		} else if f.extra == "已暂存并继续改" {
			fileIconButton(c, "sidebar-stage-"+f.path, "暂存改动", iconPlus).Tooltip("暂存改动 (Stage)").OnClick(func() {
				a.stageWorktreeFile(t, f.path)
			})
			fileIconButton(c, "sidebar-unstage-"+f.path, "取消暂存", iconMinus).Tooltip("取消暂存 (Unstage)").OnClick(func() {
				a.unstageWorktreeFile(t, f.path)
			})
		} else {
			fileIconButton(c, "sidebar-stage-"+f.path, "暂存文件", iconPlus).Tooltip("暂存文件 (Stage)").OnClick(func() {
				a.stageWorktreeFile(t, f.path)
			})
		}
		fileIconButton(c, "sidebar-discard-"+f.path, "丢弃改动", iconTrash2).Tooltip("丢弃改动 (Discard)").OnClick(func() {
			a.promptDiscardWorktreeFile(t, f)
		})
		copy := fileIconButton(c, "sidebar-copy-path-"+f.path, "复制路径", iconCopy).Tooltip(copyPathTip(c))
		if copy.Clicked() {
			copyFilePath(c, t.path, f.path, copy.ClickModifiers())
		}
		open := fileIconButton(c, "sidebar-open-tab-"+f.path, "在新标签页打开", iconOpenTab)
		if open.Clicked() {
			a.openWorktreeFileTab(t, f)
		}
		if t.query.filePath == "" && t.head != zeroHash {
			fileIconButton(c, "sidebar-file-history-"+f.path, "文件历史", iconHistory).OnClick(func() {
				a.openWorktreeFileHistory(t, f)
			})
		}
	})
}

func (a *app) sidebarWorktreeRowMenu(c *ui.Context, m *ui.Menu, t *repoTab, f fileChange) {
	if m.Item("在新标签页打开").Chosen() {
		a.openWorktreeFileTab(t, f)
	}
	if m.Item("查看完整文件").Chosen() {
		a.openFullFileTab(t, f.path, "worktree", "工作区")
	}
	if t.query.filePath == "" && t.head != zeroHash {
		if m.Item("查看文件历史").Chosen() {
			a.openWorktreeFileHistory(t, f)
		}
	}
	m.Separator()
	if f.extra == "已暂存" || f.extra == "重命名" {
		if m.Item("取消暂存").Chosen() {
			a.unstageWorktreeFile(t, f.path)
		}
	} else if f.extra == "已暂存并继续改" {
		if m.Item("暂存工作区改动").Chosen() {
			a.stageWorktreeFile(t, f.path)
		}
		if m.Item("取消暂存").Chosen() {
			a.unstageWorktreeFile(t, f.path)
		}
	} else {
		if m.Item("暂存文件").Chosen() {
			a.stageWorktreeFile(t, f.path)
		}
	}
	if m.Item("丢弃改动…").Chosen() {
		a.promptDiscardWorktreeFile(t, f)
	}
	m.Separator()
	if m.Item("复制相对路径").Chosen() {
		copyFilePath(c, t.path, f.path, 0)
	}
	if m.Item("复制绝对路径").Chosen() {
		copyFilePath(c, t.path, f.path, ui.Alt)
	}
	m.Separator()
	fullPath := repoFilePath(t.path, f.path, true)
	if m.Item("使用默认编辑器打开").Chosen() {
		if _, err := os.Stat(fullPath); err != nil {
			c.Toast("工作区中不存在此文件")
		} else if err := openWithDefaultEditor(fullPath); err != nil {
			c.Toast("打开失败: " + err.Error())
		}
	}
	revealName := "在访达中显示"
	if runtime.GOOS != "darwin" {
		revealName = "在文件管理器中显示"
	}
	if m.Item(revealName).Chosen() {
		if _, err := os.Stat(fullPath); err != nil {
			c.Toast("工作区中不存在此文件")
		} else if err := revealInFileManager(fullPath); err != nil {
			c.Toast("定位失败: " + err.Error())
		}
	}
}

func (a *app) sidebarWorktreeFilterBar(c *ui.Context, t *repoTab) {
	if len(t.worktreeFiles) == 0 && !t.loading {
		return
	}
	theme := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("sidebar-worktree-filter-bar-%d", t.id))).FillWidth().Padding(2, 8, 4, 8).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.TextInput(c.Key(fmt.Sprintf("sidebar-worktree-filter-input-%d", t.id)), &t.worktreeFilter).
			Label("过滤文件 (Filter files...)").
			Placeholder("过滤文件 (Filter files...)").
			Bind(&t.worktreeFilterInput).
			Grow(1).MinWidth(60)
		if t.worktreeFilter != "" {
			indices := t.filteredWorktreeIndices()
			ui.Text(c, fmt.Sprintf("%d/%d", len(indices), len(t.worktreeFiles))).
				FontSize(11).TextColor(theme.TextMuted).Shrink(0)
			clearBtn := ui.ButtonBase(c.Key(fmt.Sprintf("clear-sidebar-worktree-filter-%d", t.id))).Label("清除过滤").Tooltip("清除过滤").Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0)
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(12, 12).TextColor(theme.TextMuted)
			})
			if clearBtn.Clicked() {
				t.worktreeFilter = ""
			}
		}
	})
}

func (a *app) sidebarWorktreeSection(c *ui.Context, tab *repoTab) {
	theme := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(8, 0, 0, 0).Children(func() {
		ui.Row(c).FillWidth().Padding(4, 10).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "工作区代码").FontSize(12).TextColor(theme.TextMuted).Tooltip("工作区代码（相对 HEAD）")
			if tab != nil && tab.worktreeCount > 0 {
				ui.Textf(c, "%d", tab.worktreeCount).FontSize(10).FontWeight(600).
					TextColor(theme.Warning).
					Background(theme.SurfaceHover).Padding(1, 5).Radius(8).
					Tooltip(strings.Join(worktreeBadges(tab.worktreeFiles), "  "))
			}
			ui.Box(c).Grow(1)
			if tab != nil && tab.worktreeCount > 0 {
				paneIconButton(c, "sidebar-stage-all", "全部暂存", iconPlus).Tooltip("全部暂存 (Stage All)").OnClick(func() {
					a.stageAllWorktree(tab)
				})
				paneIconButton(c, "sidebar-unstage-all", "全部取消暂存", iconMinus).Tooltip("全部取消暂存 (Unstage All)").OnClick(func() {
					a.unstageAllWorktree(tab)
				})
				a.sidebarFileViewToggle(c)
				if a.sidebarFileView == 1 {
					if paneIconButton(c, "sidebar-tree-expand-all", "全部展开", iconExpandAll).Clicked() {
						tab.expandAllWorktreeTree()
					}
					if paneIconButton(c, "sidebar-tree-collapse-all", "全部收起", iconCollapseAll).Clicked() {
						tab.collapseAllWorktreeTree()
					}
				}
			}
		})

		if tab == nil {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "未选择仓库").FontSize(12).TextColor(theme.TextMuted)
			})
			return
		}

		if tab.loading && len(tab.worktreeFiles) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "正在读取改动…").FontSize(12).TextColor(theme.TextMuted)
			})
			return
		}

		if len(tab.worktreeFiles) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "工作区无改动").FontSize(12).TextColor(theme.TextMuted)
			})
			return
		}

		a.sidebarWorktreeFilterBar(c, tab)

		if a.sidebarFileView == 1 {
			a.sidebarWorktreeTreeList(c, tab)
		} else {
			a.sidebarWorktreeFlatList(c, tab)
		}
		a.worktreeCommitCard(c, tab)
	})
}

func (a *app) sidebarWorktreeFlatList(c *ui.Context, tab *repoTab) {
	theme := c.Theme()
	scrolling := takeScrollMove(&tab.worktreeListScroll, &tab.worktreeListScrollY)
	indices := tab.filteredWorktreeIndices()
	if len(indices) == 0 && len(tab.worktreeFiles) > 0 {
		ui.Text(c, "无匹配文件").Padding(12).TextColor(theme.TextMuted)
		return
	}
	currentSel := -1
	for row, origIdx := range indices {
		if origIdx == tab.worktreeSel {
			currentSel = row
			break
		}
	}
	selectedRow := currentSel
	tab.worktreeList.Selected = &selectedRow
	tab.worktreeList.Key = func(i int) any {
		if i >= 0 && i < len(indices) {
			return tab.worktreeFiles[indices[i]].path
		}
		return i
	}
	list := ui.List(c.Key(fmt.Sprintf("sidebar-worktree-%d", tab.id)), &tab.worktreeList, len(indices), func(i int) {
		if i < 0 || i >= len(indices) {
			return
		}
		origIdx := indices[i]
		f := tab.worktreeFiles[origIdx]
		muted := theme.TextMuted
		if origIdx == tab.worktreeSel {
			muted = theme.Text.Alpha(0.75)
		}
		dir := filepath.Dir(f.path)
		if dir == "." {
			dir = "仓库根目录"
		}
		row := ui.Column(c).FillWidth().MinHeight(48).Padding(6, 10).Gap(4)
		hovered := row.Hovered()
		path := f.fullPath()
		row.Tooltip(filePathTip(path, scrolling || tab.worktreeRowMoving(c, row, "sidebar-list:"+path, hovered)))
		if row.RowDoubleClicked() {
			a.openWorktreeFileTab(tab, f)
		}
		row.ContextMenu(func(m *ui.Menu) {
			a.sidebarWorktreeRowMenu(c, m, tab, f)
		})
		row.Children(func() {
			ui.Row(c).Gap(6).MinWidth(0).AlignItems(ui.Center).Children(func() {
				staged := f.extra == "已暂存" || f.extra == "重命名"
				stageBtn := ui.ButtonBase(c.Key(fmt.Sprintf("wt-stage-btn-%d-%s", tab.id, f.path))).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0)
				if staged {
					stageBtn.Tooltip("已暂存 (点击取消暂存)")
				} else {
					stageBtn.Tooltip("未暂存 (点击暂存)")
				}
				stageBtn.Children(func() {
					if staged {
						ui.Icon(c, iconCheck).Size(12, 12).TextColor(theme.Accent)
					} else {
						ui.Box(c).Size(10, 10).Border(1, theme.TextMuted.Alpha(0.6)).Radius(2)
					}
				})
				if stageBtn.Clicked() {
					if staged {
						a.unstageWorktreeFile(tab, f.path)
					} else {
						a.stageWorktreeFile(tab, f.path)
					}
				}
				ui.Text(c, filepath.Base(f.path)).FontSize(13).SingleLine().Grow(1).MinWidth(0)
				fileTrailingMeta(c, f, hovered)
			})
			ui.Text(c, dir).FontSize(11).TextColor(muted).SingleLine().Grow(1).MinWidth(0)
			a.sidebarWorktreeHoverActions(c, tab, f, origIdx == tab.worktreeSel, hovered)
		})
	}).Grow(1).MinHeight(0).Padding(4, 8).Gap(2).TrackScroll(&tab.worktreeListScroll)

	if list.Changed() && selectedRow >= 0 && selectedRow < len(indices) {
		tab.worktreeSel = indices[selectedRow]
		a.selectWorktreeFile(tab, tab.worktreeSel)
	}
}

func (a *app) sidebarWorktreeTreeList(c *ui.Context, tab *repoTab) {
	theme := c.Theme()
	scrolling := takeScrollMove(&tab.worktreeTreeScroll, &tab.worktreeTreeScrollY)
	tree := tab.prepareWorktreeTree()
	if len(tree.roots) == 0 && len(tab.worktreeFiles) > 0 {
		ui.Text(c, "无匹配文件").Padding(12).TextColor(theme.TextMuted)
		return
	}
	tab.worktreeOutline.Indent = theme.Space(4)
	tab.syncWorktreeOutlineSel(tree)
	tab.worktreeOutline.List.Selected = &tab.worktreeOutlineSel
	tab.worktreeOutline.List.Label = func(i int) string {
		if i < 0 || i >= tab.worktreeOutline.Rows() {
			return ""
		}
		n := tree.nodes[tab.worktreeOutline.Item(i)]
		if n.dir || n.file < 0 || n.file >= len(tab.worktreeFiles) {
			return n.name
		}
		return tab.worktreeFiles[n.file].label()
	}
	outline := ui.Outline(c.Key(fmt.Sprintf("sidebar-worktree-tree-%d", tab.id)), &tab.worktreeOutline, tree.roots, tree.children, func(key string) {
		n := tree.nodes[key]
		tip := key
		if !n.dir && n.file >= 0 && n.file < len(tab.worktreeFiles) {
			tip = tab.worktreeFiles[n.file].fullPath()
		}
		row := ui.Row(c).Gap(8).MinWidth(0).Grow(1).MinHeight(32).Padding(6, 4).AlignItems(ui.Center)
		hovered := row.Hovered()
		row.Tooltip(filePathTip(tip, scrolling || tab.worktreeRowMoving(c, row, "sidebar-tree:"+tip, hovered)))
		if !n.dir && n.file >= 0 && n.file < len(tab.worktreeFiles) {
			f := tab.worktreeFiles[n.file]
			if row.RowDoubleClicked() {
				a.openWorktreeFileTab(tab, f)
			}
			row.ContextMenu(func(m *ui.Menu) {
				a.sidebarWorktreeRowMenu(c, m, tab, f)
			})
		}
		row.Children(func() {
			if !n.dir && n.file >= 0 && n.file < len(tab.worktreeFiles) {
				f := tab.worktreeFiles[n.file]
				staged := f.extra == "已暂存" || f.extra == "重命名"
				stageBtn := ui.ButtonBase(c.Key(fmt.Sprintf("wt-tree-stage-btn-%d-%s", tab.id, f.path))).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0)
				if staged {
					stageBtn.Tooltip("已暂存 (点击取消暂存)")
				} else {
					stageBtn.Tooltip("未暂存 (点击暂存)")
				}
				stageBtn.Children(func() {
					if staged {
						ui.Icon(c, iconCheck).Size(12, 12).TextColor(theme.Accent)
					} else {
						ui.Box(c).Size(10, 10).Border(1, theme.TextMuted.Alpha(0.6)).Radius(2)
					}
				})
				if stageBtn.Clicked() {
					if staged {
						a.unstageWorktreeFile(tab, f.path)
					} else {
						a.stageWorktreeFile(tab, f.path)
					}
				}
			}
			ui.Text(c, n.name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
			if !n.dir && n.file >= 0 && n.file < len(tab.worktreeFiles) {
				f := tab.worktreeFiles[n.file]
				fileTrailingMeta(c, f, hovered)
				a.sidebarWorktreeHoverActions(c, tab, f, n.file == tab.worktreeSel, hovered)
			}
		})
	}).Grow(1).MinHeight(0).Padding(2, 6).Gap(2).TrackScroll(&tab.worktreeTreeScroll)

	if outline.Changed() {
		if tab.worktreeOutlineSel < 0 || tab.worktreeOutlineSel >= tab.worktreeOutline.Rows() {
			return
		}
		n := tree.nodes[tab.worktreeOutline.Item(tab.worktreeOutlineSel)]
		if n.dir || n.file < 0 || n.file >= len(tab.worktreeFiles) {
			return
		}
		tab.worktreeSel = n.file
		a.selectWorktreeFile(tab, tab.worktreeSel)
	}
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
		Background(t.Danger.Alpha(0.1)).Border(1, t.Danger.Alpha(0.28)).Radius(t.Radius).Shrink(0).Children(func() {
		ui.Icon(c, iconAlertCircle).Size(16, 16).TextColor(t.Danger).Shrink(0)
		ui.Text(c, a.banner).FontSize(13).TextColor(t.Danger).Grow(1).MinWidth(0)
		if copyBtn := ui.ButtonBase(c.Key("banner-copy-err")).Label("复制错误信息").Tooltip("复制错误信息到剪贴板").Padding(3, 8).Radius(t.Radius).Cursor(ui.CursorPointer); copyBtn.Clicked() {
			c.Services().WriteClipboard(a.banner)
			c.Toast("已复制错误信息")
		} else {
			if copyBtn.Hovered() {
				copyBtn.Background(t.Danger.Alpha(0.15))
			}
			copyBtn.Children(func() {
				ui.Text(c, "复制").FontSize(11).TextColor(t.Danger)
			})
		}
		if closeBtn := closeIconButton(c, "banner-close", "关闭提示"); closeBtn.Clicked() {
			a.setBanner("", "")
		}
	})
}

func (a *app) graphScreen(c *ui.Context) {
	main := func() {
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if tab := a.activeTab(); tab != nil {
				a.fileTabBar(c, tab)
				a.globalAlertBanner(c, tab)
				if tab.contentTab > 0 && tab.contentTab <= len(tab.fileTabs) {
					a.fileTabPane(c, tab, tab.fileTabs[tab.contentTab-1])
					return
				}
			}
			a.graphMain(c)
		})
	}
	if !a.showRepoSidebar() {
		main()
		return
	}
	if a.repoSplit <= 0 {
		a.repoSplit = 320
	}
	ui.Split(c.Key("repo-split"), &a.repoSplit, func() {
		a.repoList(c)
	}, main).Grow(1).MinHeight(0)
}

func (a *app) graphMain(c *ui.Context) {
	t := c.Theme()
	tab := a.activeTab()
	if tab == nil {
		ui.Box(c).Grow(1)
		return
	}
	if tab.comparison != nil {
		a.comparisonBanner(c, tab)
	}
	a.historyToolbar(c, tab)
	a.commitPane(c, tab)
	ui.Row(c).Padding(4, 12).Gap(12).AlignItems(ui.Center).Shrink(0).Children(func() {
		if tab.loaded && !tab.loading && tab.inspecting == zeroHash && tab.comparison == nil && !tab.worktree {
			ui.Textf(c, "%d 条提交", len(tab.commits)).FontSize(12).TextColor(t.TextMuted)
		}
		if tab.err != "" {
			ui.Text(c, tab.err).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		} else if tab.notice != "" {
			ui.Text(c, tab.notice).FontSize(12).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
		}
	})
}

func (a *app) commitPane(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "正在读取提交图谱…").TextColor(t.TextMuted)
		})
		return
	}
	if tab.err != "" && len(tab.commits) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			ui.Text(c, tab.err).TextColor(t.Danger)
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
		if tab.rows() == 0 && !tab.loading {
			text := "这个仓库还没有提交"
			if tab.query.term != "" {
				text = "没有匹配的提交"
			} else if tab.query.filePath != "" {
				text = "这个文件在所选版本之前没有历史"
			} else if tab.query.ref != "" || tab.query.hideRemote {
				text = "当前范围没有提交"
			}
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, text).TextColor(t.TextMuted)
			})
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
	}
	if tab.inspecting != zeroHash || tab.comparison != nil || tab.worktree {
		bottom := func() {
			ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(6, 0, 0, 0).Children(func() {
				if tab.worktree {
					a.fileDiffPane(c, tab)
				} else {
					if tab.fileSplit <= 0 {
						tab.fileSplit = 28
					}
					ui.SplitPercent(c.Key(fmt.Sprintf("inspect-h-split-%d", tab.id)), &tab.fileSplit, func() {
						a.inspectFilesPane(c, tab)
					}, func() {
						a.fileDiffPane(c, tab)
					}).Grow(1).MinHeight(0)
				}
			})
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

func (a *app) searchFilterBanner(c *ui.Context, tab *repoTab) {
	th := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("search-banner-%d", tab.id))).
		Margin(0, 12, 6, 12).
		Padding(6, 12).
		Background(th.Accent.Alpha(0.08)).
		Border(1, th.Accent.Alpha(0.2)).
		Radius(th.Radius).
		Gap(8).
		AlignItems(ui.Center).
		Shrink(0).
		Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(th.Accent).Shrink(0)
			msg := fmt.Sprintf("正在按%s筛选：“%s” · 匹配 %d 个提交", tab.query.kind, tab.query.term, len(tab.commits))
			ui.Text(c, msg).FontSize(12).TextColor(th.Text).Grow(1).SingleLine()
			btn := ui.Button(c.Key("search-banner-clear"), "退出筛选").
				Tooltip("返回完整提交历史 (Esc)").
				Padding(2, 8).
				FontSize(12).
				Shrink(0)
			if btn.Clicked() {
				a.leaveSearch(tab)
			}
		})
}

func (a *app) commitTableFooter(c *ui.Context, tab *repoTab) {
	if tab == nil || len(tab.commits) == 0 {
		return
	}
	theme := c.Theme()
	if tab.loading {
		ui.Row(c.Key("commit-footer-loading")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Surface.Alpha(0.6)).Children(func() {
			ui.Text(c, "正在加载更多历史提交…").FontSize(12).TextColor(theme.TextMuted)
		})
		return
	}
	if tab.hasMore && !tab.canLoadHistory() {
		ui.Row(c.Key("commit-footer-budget")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Surface.Alpha(0.6)).Children(func() {
			ui.Textf(c, "已加载 %d 条提交 · 还有更多历史", len(tab.commits)).FontSize(12).TextColor(theme.TextMuted)
			if ui.Button(c.Key("history-continue"), "继续加载").Shrink(0).Clicked() {
				tab.historyBudget += historyMemoryStep
				tab.notice = ""
				a.loadMore(tab)
			}
		})
		return
	}
	if tab.err != "" && len(tab.commits) > 0 {
		ui.Row(c.Key("commit-footer-err")).FillWidth().Padding(6, 12).Gap(8).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Background(theme.Danger.Alpha(0.1)).Children(func() {
			ui.Text(c, tab.err).FontSize(12).TextColor(theme.Danger)
			if ui.Button(c.Key("history-retry"), "重试").Shrink(0).Clicked() {
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
		return "标签 " + b.name
	case refBranch:
		if len(b.remotes) > 0 {
			return b.name + " · " + strings.Join(b.remotes, ",")
		}
		return b.name
	default:
		return b.name
	}
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
		hasRefs := item.hash == tab.head
		for _, ref := range tab.refs[item.hash] {
			if !tab.query.hideRemote || ref.kind != refRemote {
				hasRefs = true
				break
			}
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
			paintCommit(p, full, item, tab.worktreeConnectsFromTop(i), nil, bg, hasRefs)
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
				ui.Text(c, item.subject).SingleLine().Shrink(0)
				for _, label := range worktreeBadges(tab.worktreeFiles) {
					ui.Text(c, label).SingleLine().Shrink(0).TextColor(color).Background(color.Alpha(0.12)).Padding(2, 6).Radius(4)
				}
				ui.Box(c).Grow(1).MinWidth(0)
				return
			}
			if item.hash == tab.head && tab.head != zeroHash {
				ui.Text(c, "HEAD").FontWeight(600).TextColor(ui.RGBA(255, 255, 255, 1)).Background(color).Padding(2, 6).Radius(4).Shrink(0).
					Tooltip("当前检出位置 (HEAD)")
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
					tip := "本地分支 " + b.name + "（右键更多操作）"
					if len(b.remotes) > 0 {
						tip = "本地分支 " + b.name + " · 同步远端 " + strings.Join(b.remotes, ", ") + "（右键更多操作）"
					}
					pill := ui.Row(c.Key(fmt.Sprintf("ref-branch-%s-%s", hashHex(item.hash), b.name))).
						Gap(4).AlignItems(ui.Center).Shrink(0).
						Background(color.Alpha(0.14)).Border(1, color.Alpha(0.45)).Padding(1, 6).Radius(4).
						Cursor(ui.CursorPointer).Tooltip(tip)
					pill.ContextMenu(func(m *ui.Menu) {
						a.branchBadgeMenu(c, m, tab, commitCopy, b.name)
					})
					pill.Children(func() {
						ui.Text(c, b.name).FontWeight(600).SingleLine().TextColor(color)
						if len(b.remotes) > 0 {
							ui.Text(c, "· "+strings.Join(b.remotes, ",")).FontSize(11).SingleLine().TextColor(muted)
						}
					})
				case refRemote:
					pill := ui.Row(c.Key(fmt.Sprintf("ref-remote-%s-%s", hashHex(item.hash), b.name))).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Surface).Border(1, t.Border).Padding(1, 6).Radius(4).
						Cursor(ui.CursorPointer).Tooltip("远程分支 " + b.name + "（双击检出跟踪分支，右键更多操作）")
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
					pill.Children(func() {
						ui.Text(c, b.name).SingleLine().TextColor(muted)
					})
				case refTag:
					pill := ui.Row(c.Key(fmt.Sprintf("ref-tag-%s-%s", hashHex(item.hash), b.name))).
						AlignItems(ui.Center).Shrink(0).
						Background(t.Warning.Alpha(0.14)).Border(1, t.Warning.Alpha(0.45)).Padding(1, 6).Radius(4).
						Cursor(ui.CursorPointer).Tooltip("标签 " + b.name + "（右键更多操作）")
					pill.ContextMenu(func(m *ui.Menu) {
						a.tagBadgeMenu(c, m, tab, commitCopy, b.name)
					})
					pill.Children(func() {
						ui.Text(c, "标签 "+b.name).SingleLine().TextColor(t.Warning)
					})
				}
			}
			text := item.subject
			if text == "" {
				text = "（无说明）"
			}
			e := ui.Text(c, text).SingleLine().Grow(1).MinWidth(0)
			if len(item.parents) > 1 {
				e.TextColor(muted)
			}
		case "date":
			text := item.whenText()
			if worktree {
				text = "现在"
			}
			ui.Text(c, text).SingleLine().TextColor(muted)
		case "author":
			ui.Text(c, item.author).SingleLine().TextColor(muted)
		case "hash":
			if worktree {
				ui.Text(c, "工作区").SingleLine().TextColor(muted)
				return
			}
			full := hashHex(item.hash)
			btn := ui.ButtonBase(c.Key("copy-hash-" + full)).Label("复制哈希").Tooltip(full).Cursor(ui.CursorPointer)
			btn.Children(func() {
				ui.Text(c, shortHash(item.hash)).Font("monospace").SingleLine().TextColor(muted)
			})
			if btn.Clicked() {
				c.Services().WriteClipboard(full)
				c.Toast("已复制哈希")
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
			{ID: "graph", Title: "图谱", Width: graphWidth(tab.graph.lanes()), MinWidth: 40},
			{ID: "subject", Title: "描述 / 引用", MinWidth: 160},
			{ID: "date", Title: "日期", Width: 128, MinWidth: 100},
			{ID: "author", Title: "作者", Width: 96, MinWidth: 60},
			{ID: "hash", Title: "哈希", Width: 84, MinWidth: 70},
		}
	}
	return []ui.TableColumn{
		{ID: "graph", Title: "图谱", Width: graphWidth(tab.graph.lanes()), MinWidth: 40},
		{ID: "subject", Title: "描述 / 引用", MinWidth: 200},
		{ID: "date", Title: "日期", Width: 172, MinWidth: 128},
		{ID: "author", Title: "作者", Width: 156, MinWidth: 96},
		{ID: "hash", Title: "哈希", Width: 120, MinWidth: 96},
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

func paintCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool, palette []ui.Color, background ui.Color, hasRefs bool) {
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
	radius := float32(4.5)
	if hasRefs {
		radius = 5.5
	}
	paintGraphNode(p, laneCenter(r, c.nodeCol), mid, radius, graphColor(c.nodeColor, palette), background, len(c.parents) > 1)
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
	a := newApp()
	mygo.App.WhenReady(func() {
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
		for _, path := range os.Args[1:] {
			a.openPath(path)
		}
		a.startAutoRefresh()
		a.win.OnFocus(a.focusRepo)
		a.win.OnClosed(func() {
			a.closed = true
			if a.scanCancel != nil {
				a.scanCancel()
			}
			if a.recentWrites != nil {
				close(a.recentWrites)
				a.recentWrites = nil
			}
			if a.watchCancel != nil {
				a.watchCancel()
			}
			for _, tab := range a.tabs {
				tab.closed = true
				tab.cancel()
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func commandModifier() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl
}
