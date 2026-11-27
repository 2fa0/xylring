// Xylring：导入一个 Git 仓库，画出它的提交图谱。
//
//	go test
//	go tool mygo dev
package main

import (
	"context"
	"fmt"
	"log"
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
	chromeMinHeight = 44
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
	externalEditor  string
	editorPathDraft string
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
	repoColors        map[string]string
	repoColorFilter   string
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
	refsInline           bool
	diffIgnoreWhitespace bool
	diffWrap             bool

	showWrite bool
	write     commitWrite

	showFetch    bool
	fetch        fetchDialog
	folderImport folderImportDialog

	sessionLoaded      bool
	sessionReady       bool
	sessionDone        chan struct{}
	sessionWrites      chan savedSession
	lastSession        *savedSession
	sessionResults     chan error
	preservedRepos     []savedRepo
	preservedActive    string
	preservedFound     []string
	preservedFoundFrom string
	showOperation      bool
	op                 repoOperation
	opCancel           context.CancelFunc
	showDiscard        bool
	discard            discardDialog
	showWorktreeRemove bool
	wtRemove           worktreeDialog
	showPush           bool
	push               pushDialog
	showBoard          bool
	board              boardState
	quitGuard          closeGuardState
	docker             dockerDialogState
	dockerWrites       dockerWriteState
	writes             map[int]string // 正在跑的 Git 写操作，见 startGitWrite
	writeSeq           int
}

type discardDialog struct {
	repo string
	open bool
	file fileChange
}

// pushDialog 记着确认推送时那颗按钮指向的仓库和分支：弹窗开着的时候用户可能换了标签页。
type pushDialog struct {
	path     string
	branch   string
	upstream string
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
		a.refsInline = p.RefsInline
		a.diffIgnoreWhitespace = p.IgnoreWhitespace
		a.diffWrap = p.Wrap
		if validAutoFetch(p.AutoFetch) {
			a.autoFetch = p.AutoFetch
		}
		a.gitExecutable = strings.TrimSpace(p.GitExecutable)
		a.externalEditor = strings.TrimSpace(p.ExternalEditor)
		if a.fileView != 1 {
			a.fileView = 0
		}
		if a.diffLayout != 1 {
			a.diffLayout = 0
		}
	}
	a.repoAutoFetch = session.RepoAutoFetch
	a.repoColors = session.RepoColors
	a.gitExecutable = setGitExecutablePath(a.gitExecutable)
	a.gitPathDraft = a.gitExecutable
	for path, value := range a.repoAutoFetch {
		if !validAutoFetch(value) {
			delete(a.repoAutoFetch, path)
		}
	}
	for path, id := range a.repoColors {
		if !validRepoColor(id) {
			delete(a.repoColors, path)
		}
	}
	if session.BottomFullWidth != nil {
		a.bottomFullWidth = *session.BottomFullWidth
	}
	a.initDockerWrites()
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
		if a.showBoard {
			ui.Column(c.Key("page-board")).Grow(1).MinWidth(0).MinHeight(0).Transition(boardPageMotion()).Children(func() {
				a.boardScreen(c)
			})
		} else if a.showHome || (a.activeTab() == nil && len(a.found) <= 1) {
			ui.Column(c.Key("page-home")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
				a.emptyState(c)
			})
		} else {
			ui.Column(c.Key("page-graph")).Grow(1).MinWidth(0).MinHeight(0).Transition(fadeEnter()).Children(func() {
				a.graphScreen(c)
			})
		}
		if a.showBoard && a.board.selectedID != "" {
			a.boardDetailDrawer(c)
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
	a.pushModal(c)
	a.discardModal(c)
	a.worktreeRemoveModal(c)
	a.ringModal(c)
	a.operationModal(c)
	a.settingsModal(c)
	a.gitLogModal(c)
	a.boardModals(c)
	a.folderImportModal(c)
	a.dockerRepoModal(c)
	a.dockerRecoveryModal(c)
	a.closeGuardModal(c)
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
		if (!empty || a.showBoard) && c.Shortcut(commandModifier(), ui.KeyB) {
			a.toggleSidebar()
		}
		if a.showBoard {
			if !a.boardModalOpen() {
				if c.Shortcut(commandModifier(), ui.KeyF) || c.Shortcut(commandModifier(), ui.KeyP) || c.Shortcut(commandModifier(), ui.KeyK) {
					a.board.searchInput.Focus(c)
					return
				}
				if c.Shortcut(0, ui.KeyEscape) {
					if a.board.search != "" {
						a.board.search = ""
					} else if a.board.selectedID != "" {
						a.closeBoardDrawer()
					}
				}
			}
			return
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

func (a *app) chromeMetrics(c *ui.Context) (h, padL, padR float32, empty, sidebarOpen bool, rightPadL, rightW float32) {
	bar := c.TitleBar()
	h = max(bar.Height, chromeMinHeight)
	padL, padR = bar.Left, bar.Right
	if bar.Left > 0 {
		padL = bar.Left + 8
	} else if padL < chromePad {
		padL = chromePad
	}
	if padR < chromePad {
		padR = chromePad
	}
	tab := a.activeTab()
	empty = !a.showBoard && (a.showHome || (tab == nil && len(a.found) <= 1))
	if !empty && a.showRepoSidebar() && a.repoSplit <= 0 {
		a.repoSplit = defaultSidebarWidth
	}
	sidebarOpen = !empty && a.showRepoSidebar() && a.repoSplit > 0 && (a.showBoard || tab == nil || !tab.diffMaximized)
	rightPadL = padL
	if sidebarOpen {
		rightPadL = max(padL, a.repoSplit+10)
	} else if !empty {
		rightPadL = padL + 94
	} else {
		rightPadL = padL + 64
	}
	winW, _ := c.Size()
	rightW = winW - rightPadL - padR
	return h, padL, padR, empty, sidebarOpen, rightPadL, rightW
}

func (a *app) unifiedTopToolbar(c *ui.Context, tab *repoTab) bool {
	if tab == nil || tab.unavailable || tab.contentTab != 0 || tab.query.filePath != "" {
		return false
	}
	_, _, _, empty, _, _, rightW := a.chromeMetrics(c)
	return !empty && rightW >= 720
}

func (a *app) chromeBranchButton(c *ui.Context, tab *repoTab, sidebarOpen, unified bool) {
	th := c.Theme()
	label := a.dockerTabLabel(tab)
	branchText := displayBranch(tab.branch)
	if tab.branch != "" {
		if sidebarOpen {
			label = branchText
		} else {
			label += " · " + branchText
		}
		if sync := syncBadge(tab.ahead, tab.behind); sync != "" {
			label += " " + sync
		}
	}
	tip := repoLocationTooltip(tab.path)
	if tab.branch != "" {
		tip += "\n" + branchText
	}
	btn := ui.ButtonBase(c.Key("topbar-branch")).Height(28).Padding(0, 10).Gap(5).
		Radius(14).AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Tooltip(tip).Label(label).MinWidth(0).Shrink(1).Transition(colorMotion())
	if unified {
		btn.MaxWidth(210)
	} else {
		btn.MaxWidthPercent(55)
	}
	defaultBg := ui.RGBA(15, 23, 42, 0.035)
	if th.Dark {
		defaultBg = ui.RGBA(255, 255, 255, 0.04)
	}
	switch {
	case btn.Pressed():
		btn.Background(th.SurfacePressed).Border(1, th.Border.Alpha(0.75))
	case btn.Hovered():
		btn.Background(th.SurfaceHover).Border(1, th.Border.Alpha(0.7))
	default:
		btn.Background(defaultBg).Border(1, th.Border.Alpha(0.5))
	}
	btn.Children(func() {
		ui.Icon(c, iconGitBranch).Size(13, 13).TextColor(th.TextMuted).Shrink(0)
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(th.Text).SingleLine().MinWidth(0)
		if !tab.unavailable {
			ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(th.TextMuted).Shrink(0)
		}
	})
	if tab.unavailable {
		return
	}
	disabled := tab.loading || a.write.busy || a.fetch.busy || tab.syncBusy || tab.commitBusy
	btn.Menu(func(m *ui.Menu) {
		branches := make([]string, 0, len(tab.refs))
		for name := range localBranchNames(tab.refs) {
			branches = append(branches, name)
		}
		slices.Sort(branches)
		for _, name := range branches {
			branchName := name
			if m.Item(branchName).Checked(branchName == tab.branch).Disabled(disabled || branchName == tab.branch).Chosen() {
				a.runCheckoutBranch(tab, branchName)
			}
		}
		if len(branches) > 0 {
			m.Separator()
		}
		if m.Item(tr("新建分支…")).Disabled(disabled || tab.head == zeroHash || isDockerRepoPath(tab.path)).Chosen() {
			a.openCommitWrite(tab, writeBranch, commit{hash: tab.head})
		}
		if tab.branch != "" && !strings.HasPrefix(tab.branch, "分离头指针") && tab.branch != "尚无提交" {
			if m.Item(tr("复制分支名")).Chosen() {
				c.Services().WriteClipboard(tab.branch)
				c.Toast(tr("已复制分支名"))
			}
		}
	})
}

func (a *app) chrome(c *ui.Context) {
	bar := c.TitleBar()
	h, padL, padR, empty, sidebarOpen, rightPadL, rightW := a.chromeMetrics(c)
	tab := a.activeTab()
	unified := a.unifiedTopToolbar(c, tab)
	ui.Box(c).Height(h).Shrink(0).DragWindow().Children(func() {
		if sidebarOpen {
			ui.Box(c).Absolute().Left(a.repoSplit - 1).Top(0).Bottom(0).Width(1).Background(c.Theme().Border)
		}
		btnY := (h - 26) / 2
		if runtime.GOOS == "darwin" && bar.Left > 0 && h >= 48 {
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
					ui.Box(c).Grow(1)
					importBtn := chromeIconButton(c, "sidebar-import-btn", "导入仓库", iconFolderPlus).
						Tooltip(tr("导入本地仓库 (⌘O)"))
					if dockerReposSupported() {
						importBtn.ContextMenu(func(m *ui.Menu) {
							if m.Item(tr("导入本地仓库…")).Chosen() {
								a.pickRepo()
							}
							if m.Item(tr("打开容器仓库…")).Chosen() {
								a.openDockerRepoDialog()
							}
						})
					}
					if importBtn.Clicked() {
						a.pickRepo()
					}
					if chromeIconButton(c, "open-settings", "设置", iconRingSettings).Clicked() {
						a.openSettings()
					}
				})
			}
		} else if !empty && (len(a.found) > 0 || len(a.tabs) > 0) {
			btn := chromeIconButton(c, "toggle-sidebar", "切换侧边栏", iconPanelLeft).
				Absolute().Left(padL).Top(btnY).
				Tooltip(sidebarToggleTip(a.showRepoSidebar()))
			if btn.Clicked() {
				a.toggleSidebar()
			}
		}
		if !sidebarOpen {
			x := padL
			if !empty && (len(a.found) > 0 || len(a.tabs) > 0) {
				x += 30
			}
			if a.showBoard && len(a.found) == 0 && len(a.tabs) == 0 {
				if chromeIconButton(c, "chrome-home-btn", "返回首页", iconHome).Absolute().Left(x).Top(btnY).Clicked() {
					a.goHome()
				}
				x += 30
			}
			boardTip := tr("看板")
			if a.showBoard {
				boardTip = tr("返回仓库")
			}
			boardBtn := chromeIconButton(c, "open-board", "看板", iconKanban).
				Absolute().Left(x).Top(btnY).Tooltip(boardTip)
			if boardBtn.Clicked() {
				if a.showBoard && (len(a.tabs) > 0 || len(a.found) > 0) {
					a.leaveBoard()
				} else if a.showBoard {
					a.goHome()
				} else {
					a.openBoard()
				}
			}
			x += 30
			if chromeIconButton(c, "open-settings", "设置", iconRingSettings).Absolute().Left(x).Top(btnY).Clicked() {
				a.openSettings()
			}
		}
		if empty {
			return
		}
		if a.showBoard {
			a.boardChromeToolbar(c, padR, rightPadL, rightW)
			return
		}
		if tab == nil {
			return
		}
		ui.Row(c).Fill().Padding(0, padR, 0, rightPadL).Gap(6).AlignItems(ui.Center).Children(func() {
			a.chromeBranchButton(c, tab, sidebarOpen, unified)
			if unified {
				a.historyScopeControls(c, tab)
				var searchMax float32
				if rightW > 1100 {
					searchMax = 560
				}
				a.historySearchControls(c, tab, searchMax)
				if searchMax > 0 && tab.query.term == "" {
					ui.Box(c).Grow(1).MinWidth(8)
				}
				if chromeIconButton(c, "compare", "比较", iconGitCompare).
					Disabled(tab.loading).Clicked() {
					a.openCompareModal(tab)
				}
			} else {
				ui.Box(c).Grow(1).MinWidth(8)
			}
			a.commitScrollTopButton(c, tab)
			disabled := tab.unavailable || tab.loading || a.write.busy || a.fetch.busy || tab.syncBusy || tab.commitBusy
			container := isDockerRepoPath(tab.path)
			if container {
				// 远程同步在容器仓库里暂不开放，按钮保留位置但不触发操作。
				disabled = true
			}
			pullTip := tr("拉取或获取远程更新")
			pushTip := tr("推送当前分支")
			if container {
				pullTip = tr("容器仓库暂不支持远程同步")
				pushTip = tr("容器仓库暂不支持远程同步")
			}
			// 拉取会改本地代码，藏在菜单里点两下不亏；推送最常用，一颗按钮一步到位（再一次确认）。
			chromeSyncButton(c, "topbar-pull", "拉取或获取远程更新", pullTip, iconDownload, tab.behind, disabled).
				Menu(func(m *ui.Menu) {
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
					m.Separator()
					if m.Item(tr("获取选项…")).Disabled(disabled).Chosen() {
						a.openFetch(tab)
					}
				})
			if chromeSyncButton(c, "topbar-push", "推送当前分支", pushTip, iconUpload, tab.ahead, disabled).Clicked() {
				a.requestPush(tab)
			}
		})
	})
}

func chromeSyncButton(c *ui.Context, key, name, tip string, icon *ui.SVG, count int, disabled bool) ui.Element {
	if count > 0 && !disabled {
		t := c.Theme()
		b := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tip).
			Height(26).Padding(0, 8).Gap(4).Radius(13).Center().Shrink(0).
			Cursor(ui.CursorPointer).Transition(colorMotion())
		bg := t.Accent.Alpha(0.12)
		switch {
		case b.Pressed():
			bg = t.Accent.Alpha(0.22)
		case b.Hovered():
			bg = t.Accent.Alpha(0.18)
		}
		b.Background(bg).Border(1, t.Accent.Alpha(0.38))
		b.Children(func() {
			ui.Icon(c, icon).Size(14, 14).TextColor(t.Accent).Shrink(0)
			ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).FontWeight(600).
				FontFeatures("tnum").TextColor(t.Accent).SingleLine().Shrink(0)
		})
		return b
	}
	return chromeIconButton(c, key, name, icon).Tooltip(tip).Disabled(disabled)
}

func (a *app) globalAlertBanner(c *ui.Context, tab *repoTab) {
	if a.banner == "" {
		return
	}
	if tab != nil && a.bannerPath != "" && a.bannerPath != tab.path && a.hasTabPath(a.bannerPath) {
		return
	}
	// 横幅也会出现在没有仓库上下文的页面（看板、主页）：那里只报错误不报位置，
	// 用户无法判断说的是哪个仓库。当前标签页就是出事仓库、或消息里已经带了位置时，
	// 同一屏不再重复。
	location := ""
	if a.bannerPath != "" && (tab == nil || tab.path != a.bannerPath) && !strings.Contains(a.banner, repoLocation(a.bannerPath)) {
		location = repoLocation(a.bannerPath)
	}
	t := c.Theme()
	ui.Row(c.Key("global-alert-banner")).FillWidth().Padding(8, 14).Gap(10).AlignItems(ui.Center).
		Background(t.Danger.Alpha(0.1)).Border(1, t.Danger.Alpha(0.28)).Radius(t.Radius).Shrink(0).Transition(fadeEnter()).Children(func() {
		ui.Icon(c, iconAlertCircle).Size(16, 16).TextColor(t.Danger).Shrink(0)
		ui.Column(c.Key("global-alert-text")).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, trMessage(a.banner)).FontSize(13).TextColor(t.Danger).MinWidth(0)
			if location != "" {
				ui.Text(c, location).FontSize(11.5).Font("monospace").TextColor(t.Danger.Alpha(0.75)).SingleLine().MinWidth(0).Tooltip(repoLocationTooltip(a.bannerPath))
			}
		})
		a.gitLogBannerButton(c)
		copyText := a.banner
		if location != "" {
			copyText = a.banner + "\n" + location
		}
		copyIconButton(c, "banner-copy-err", tr("复制错误信息"), copyText)
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

func main() {
	if handleCLIArgs(os.Args[1:]) {
		return
	}
	// 保证单实例运行：若已有正在运行的实例，将命令行参数与工作目录转发并立即退出
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	startLogging()
	// 更新插件必须在 App.Run 之前注册；启动 10 秒后它自己查一次，请求不占主线程。
	useUpdater()
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
		a.watchUpdates()
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
		configureWindowRendering(a.win)
		paths := normalizePaths(os.Args[1:], "")
		a.restoreSessionWithRepos(len(paths) == 0)
		// 先把 Git 探明白，缺 Git 时首页直接说清楚原因。
		a.checkGit(false)
		for _, path := range paths {
			a.openPath(path)
		}
		a.startAutoRefresh()
		a.win.OnFocus(a.focusRepo)
		a.win.OnClose(a.closeGuardHook)
		a.win.OnClosed(func() {
			a.commitBoardDrafts()
			a.flushSessionOnClose()
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
	a.flushSessionOnClose()
	if a.sessionDone != nil {
		<-a.sessionDone
	}
	if a.board.jobTail != nil {
		<-a.board.jobTail
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
