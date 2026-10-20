// Xylring：验证 MyGo 原生界面能否画出提交图谱、打开 diff，
// 并在多仓库标签之间释放大块数据。这不是完整客户端。
//
//	go test
//	go tool mygo dev
package main

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
)

const (
	laneGap     = 14
	lanePad     = 10
	graphStroke = 1.75
	// 表格单元格会 Clip，并把内容垫进 Space(1.5)/Space(2.5)。
	// 图谱要画进这块内边距，线才能接到下一行的格线上。
	graphCellPadY = 1.5
	graphCellPadX = 2.5
)

// 默认泳道色。明度接近，避开纯红纯绿，浅色和深色背景都能分开。
const graphColorDefault = "#4C82B0,#3AA88E,#D08A58,#8B74AE,#D47888,#7EA35C,#5E97B8,#4AA3AE"

// 0.1 的默认色。没改过的设置迁到上面这套，改过的保留。
const graphColorLegacy = "#1f78b4,#33a02c,#e31a1c,#ff7f00,#6a3d9a,#b15928,#17becf,#bc80bd"

var laneColors []ui.Color

func init() {
	laneColors, _ = parsePalette(graphColorDefault)
}

type renderStats struct {
	Frames         uint64
	FrameAt        time.Time
	Builds         uint64
	Commits, TabID int
	Diff           bool
}

type app struct {
	rendered      *renderStats
	op            *operation
	showOperation bool
	writeBusy     map[string]bool
	recentTag     string
	credentials   []*credentialRequest
	persist       bool
	win           *mygo.Window
	dispatch      func(func())

	tabs                                                  []*repoTab
	active                                                int
	nextID                                                int
	recent                                                []string
	prefs                                                 settings
	showSettings                                          bool
	settingsRepo                                          string
	settingsScope                                         string
	settingsView                                          viewSettings
	settingsFirst, settingsMore, settingsGit, settingsErr string

	sidebarWidth, sidebarShown float32
	split                      float32
	filePane                   float32

	picking     bool
	showRecent  bool
	banner      string
	showFilter  bool
	filterRepo  string
	filterErr   string
	filterRows  []branchChoice
	filterList  ui.ListState
	showCompare bool
	compareRepo string
	compareFrom string
	compareTo   string
	compareErr  string

	flash      string
	flashUntil time.Time
	heap       uint64
	heapAt     time.Time
}

func newApp() *app {
	prefs, err := loadSettings()
	gitMu.Lock()
	gitOverride = prefs.GitPath
	gitMu.Unlock()
	a := &app{
		sidebarWidth: 220,
		split:        40,
		filePane:     300,
		recent:       loadRecent(),
		prefs:        prefs,
	}
	if err != nil {
		a.banner = err.Error()
	}
	a.persist = true
	return a
}

func (a *app) view(c *ui.Context) {
	if hook, _ := onFrame.Load().(func(*app, time.Time)); hook != nil {
		hook(a, c.Now())
	}
	if a.rendered != nil {
		defer func() {
			a.rendered.Builds++
			if now := c.Now(); now != a.rendered.FrameAt {
				a.rendered.FrameAt = now
				a.rendered.Frames++
			}
			if t := a.activeTab(); t != nil {
				a.rendered.Commits = len(t.commits)
				a.rendered.TabID = t.id
				a.rendered.Diff = t.diff != nil
			} else {
				a.rendered.Commits = 0
				a.rendered.TabID = 0
				a.rendered.Diff = false
			}
		}()
	}
	a.tabShortcuts(c)
	if os.Getenv("GITCLIENT_DEBUG") != "" {
		a.sampleMem(c)
	}
	a.expireFlash(c)
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		if a.activeTab() == nil {
			a.emptyState(c)
		} else {
			tab := a.activeTab()
			if tab.focusDiff {
				if !a.showOperation && !a.showSettings && !a.showCompare && !a.showRecent && len(a.credentials) == 0 && c.Shortcut(0, ui.KeyEscape) {
					tab.focusDiff = false
				}
				a.comparisonBar(c, tab)
				if tab.viewer != nil {
					a.viewerPane(c, tab)
				} else {
					a.readingPane(c, tab)
				}
			} else {
				ui.Row(c).Grow(1).MinWidth(0).MinHeight(0).NoWrap().AlignItems(ui.Stretch).Children(func() {
					a.repoRail(c)
					ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignSelf(ui.Stretch).Children(func() {
						a.toolbar(c)
						a.comparisonBar(c, tab)
						width, _ := c.Size()
						if a.sidebarWidth == 0 {
							a.sidebarWidth = 240
						}
						a.sidebarShown = shownSidebar(a.sidebarWidth, width)
						split := ui.Split(c.Key("main-sidebar"), &a.sidebarShown, func() { a.leftPane(c) }, func() {
							a.rightPane(c, tab)
						}).Grow(1).MinWidth(0).MinHeight(0)
						if split.Changed() {
							a.sidebarWidth = a.sidebarShown
						}
					})
				})
			}
		}
		a.statusBar(c)
	})
	a.recentModal(c)
	a.branchFilterModal(c)
	a.comparisonModal(c)
	a.settingsModal(c)
	a.operationModal(c)
	if a.op != nil && a.op.Busy && !a.showOperation && !a.op.NotedDismiss {
		a.op.NotedDismiss = true
		a.setFlash(operationTitle(a.op.Kind) + "仍在执行，没有取消")
	}
	if a.showOperation && a.op != nil {
		a.op.NotedDismiss = false
	}
	a.credentialModal(c)
}

func railLabel(title string) string {
	base := title
	if i := strings.LastIndex(title, "/"); i >= 0 && i < len(title)-1 {
		base = title[i+1:]
	}
	runes := []rune(base)
	if len(runes) > 2 {
		runes = runes[:2]
	}
	if len(runes) == 0 {
		return "仓"
	}
	return string(runes)
}

func (a *app) repoRail(c *ui.Context) {
	if len(a.tabs) < 2 {
		return
	}
	t := c.Theme()
	activateAt, closeAt := -1, -1
	moveID, moveTo := -1, -1
	ui.Column(c).Width(44).Fill().Background(t.Surface).Padding(4, 4).Gap(4).Children(func() {
		ui.Scroll(c.Key("repo-rail")).Grow(1).MinHeight(0).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				for i, tab := range a.tabs {
					tip := tab.title
					if tab.path != "" {
						tip += "\n" + tab.path
					}
					button := ui.Button(c.Key(fmt.Sprintf("repo-%d", tab.id)), railLabel(tab.title)).Tooltip(tip).Width(36).Selected(i == a.active).Drag(tab.id)
					if id, ok := ui.Drop[int](button); ok {
						moveID, moveTo = id, i
					}
					if button.Clicked() {
						activateAt = i
					}
					button.ContextMenu(func(m *ui.Menu) {
						if m.Item("关闭").Chosen() {
							closeAt = i
						}
					})
				}
			})
		})
		if ui.Button(c.Key("rail-add"), "+").Tooltip("打开仓库").Width(36).Clicked() {
			a.pickRepo()
		}
	})
	if moveID >= 0 {
		a.moveTab(moveID, moveTo)
	}
	if closeAt >= 0 {
		a.closeTab(closeAt)
	} else if activateAt >= 0 {
		a.activate(activateAt)
	}
}

func (a *app) comparisonBar(c *ui.Context, tab *repoTab) {
	if tab == nil || tab.comparison == nil {
		return
	}
	ui.Row(c).Padding(4, 8).Gap(8).AlignItems(ui.Center).Background(c.Theme().Surface).Children(func() {
		subject := "比较：从 " + shortHash(tab.comparison.from) + " → 到 " + shortHash(tab.comparison.to)
		ui.Text(c, subject).SingleLine().Grow(1).MinWidth(0).Tooltip(subject)
		if ui.Button(c.Key("close-comparison"), "返回提交").Clicked() {
			tab.comparison = nil
			tab.diffOpen = false
			a.onCommitSelected(tab)
		}
	})
}

func (a *app) leftPane(c *ui.Context) {
	tab := a.activeTab()
	if tab == nil {
		return
	}
	ui.Column(c).Fill().Children(func() {
		if tab.sidebarSection == "文件" || tab.sidebarSection == "Stash" {
			ui.Column(c).Fill().Padding(6).Gap(6).Children(func() {
				if ui.Button(c.Key("side-back"), "返回分支").Clicked() {
					tab.sidebarSection = ""
				}
				if tab.sidebarSection == "文件" {
					a.workspaceTree(c, tab)
				} else {
					a.stashSidebar(c, tab)
				}
			})
			return
		}
		ui.Row(c).Padding(6, 6, 0, 6).Gap(6).Children(func() {
			if ui.Button(c.Key("pane-branches"), "分支").Selected(tab.leftPane != "改动").Clicked() {
				tab.leftPane = "分支"
			}
			if ui.Button(c.Key("pane-changes"), "改动").Selected(tab.leftPane == "改动").Clicked() {
				tab.leftPane = "改动"
			}
		})
		if tab.leftPane == "改动" {
			a.changesPane(c, tab)
			return
		}
		a.branchPane(c, tab)
	})
}

func (a *app) branchPane(c *ui.Context, tab *repoTab) {
	ui.Column(c).Fill().Padding(6).Gap(6).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			if ui.Button(c.Key("open-files"), "文件").Clicked() {
				tab.sidebarSection = "文件"
			}
			if ui.Button(c.Key("open-stash"), "Stash").Clicked() {
				tab.sidebarSection = "Stash"
			}
		})
		if tab.refChoices == nil {
			for _, list := range tab.refs {
				tab.refChoices = append(tab.refChoices, list...)
			}
			sortRefs(tab.refChoices)
		}
		var branches []refInfo
		for _, ref := range tab.refChoices {
			if ref.kind != refTag {
				branches = append(branches, ref)
			}
		}
		if len(branches) == 0 {
			ui.Text(c, "没有分支").TextColor(c.Theme().TextMuted)
			return
		}
		ui.List(c.Key("branch-list"), &tab.refList, len(branches), func(i int) {
			ref := branches[i]
			current := ref.kind == refBranch && ref.name == tab.branch
			label := "分支 " + ref.name
			if ref.kind == refRemote {
				label = "远程 " + ref.name
			}
			if current {
				label = "当前 " + ref.name
			}
			e := ui.Button(c.Key("branch-"+label), label).Tooltip(label).Selected(current)
			if e.Clicked() {
				prefix := "refs/heads/"
				if ref.kind == refRemote {
					prefix = "refs/remotes/"
				}
				a.openRef(tab, prefix+ref.name)
			}
			e.ContextMenu(func(m *ui.Menu) { a.refMenu(c, m, tab, ref) })
		}).Grow(1).MinHeight(0)
	})
}

func (a *app) changesPane(c *ui.Context, tab *repoTab) {
	if tab.comparison == nil && tab.stashView == nil && tab.selected < 0 {
		a.ensureWorktreeFiles(tab)
	}
	ui.Column(c).Fill().Children(func() {
		ui.Text(c, "选择文件后在右侧显示 diff").Padding(6, 8, 0, 8).TextColor(c.Theme().TextMuted).SingleLine()
		a.filePaneView(c, tab)
	})
}

func (a *app) rightPane(c *ui.Context, tab *repoTab) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignSelf(ui.Stretch).Children(func() {
		if tab.viewer != nil {
			a.viewerPane(c, tab)
			return
		}
		if tab.leftPane == "改动" && tab.diffOpen && !tab.detailHidden && tab.fileSel >= 0 && tab.fileSel < len(tab.files) {
			a.readingPane(c, tab)
			return
		}
		a.commitPane(c)
	})
}

func (a *app) readingPane(c *ui.Context, tab *repoTab) {
	if tab.fileSel >= 0 && tab.fileSel < len(tab.files) && tab.fileSel != tab.fileApplied {
		tab.fileApplied = tab.fileSel
		tab.diffOpen = true
		tab.fileMode = "diff"
		a.onFileSelected(tab)
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).AlignSelf(ui.Stretch).Children(func() {
		a.detailHeader(c, tab)
		if tab.detailErr != "" {
			ui.Text(c, tab.detailErr).Padding(8, 12).TextColor(c.Theme().Danger)
		}
		a.diffPane(c, tab)
	})
}

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "Xylring").FontSize(28).Bold()
		ui.Text(c, "打开仓库，浏览历史，看清每一次改动。").
			TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c.Key("open-empty"), "打开仓库").Clicked() {
				a.pickRepo()
			}
			if ui.Button(c.Key("recent-empty"), "最近的仓库").Clicked() {
				a.showRecent = true
			}
		})
	})
}

func (a *app) toolbar(c *ui.Context) {
	tab := a.activeTab()
	if tab == nil {
		return
	}
	t := c.Theme()
	width, _ := c.Size()
	wide := width >= 1050
	if c.Shortcut(commandModifier(), ui.KeyR) {
		a.reload(tab)
	}
	ui.Toolbar(c, func() {
		ui.Text(c, tab.branch).Bold().SingleLine().MaxWidth(150).MinWidth(36).Tooltip("当前分支 " + tab.branch)
		a.historyScope(c, tab)
		if tab.scope == "多选分支" && ui.Button(c.Key("edit-filter"), "选择分支…").Clicked() {
			a.beginBranchFilter(tab)
		}
		if wide {
			for _, action := range syncActions() {
				if ui.Button(c.Key("toolbar-"+action.kind), action.label).Tooltip(action.tip).Clicked() {
					a.beginOperation(tab, action.kind, "")
				}
			}
		}
		ui.Button(c.Key("repo-operations"), "仓库操作 ▾").Menu(func(m *ui.Menu) {
			if !wide {
				for _, action := range syncActions() {
					if m.Item(action.label).Chosen() {
						a.beginOperation(tab, action.kind, "")
					}
				}
				m.Separator()
			}
			for _, kind := range append([]string{"新建 Stash", "外部合并工具"}, tab.pending...) {
				if m.Item(kind + "…").Chosen() {
					a.beginOperation(tab, kind, "")
				}
			}
			if m.Item("在终端打开仓库").Chosen() {
				a.openTerminal(tab.path)
			}
			m.Separator()
			if m.Item("打开仓库").Chosen() {
				a.pickRepo()
			}
			if m.Item("最近的仓库").Chosen() {
				a.showRecent = true
			}
			if m.Item("设置").Chosen() {
				a.beginSettings()
			}
		})
		if ui.Button(c.Key("refresh"), "刷新").Tooltip("重新读取仓库").Clicked() {
			a.reload(tab)
		}
	}).Label("仓库与历史操作").Padding(2, 8).Gap(6)

	ui.Toolbar(c, func() {
		if ui.Button(c.Key("toggle-ref"), "定位提交…").Tooltip("按分支、标签或 SHA 跳转，不搜索提交说明").Clicked() {
			tab.showRef = !tab.showRef
		}
		find := ui.TextInput(c.Key(fmt.Sprintf("find-%d", tab.id)), &tab.query).Placeholder("搜索历史：消息、作者、日期、哈希或引用").Grow(1).MinWidth(72)
		if c.Shortcut(commandModifier(), ui.KeyF) {
			find.Focus()
		}
		label, tip := "搜索", "查找匹配的提交"
		if tab.searchText != "" && strings.TrimSpace(tab.query) == tab.searchText {
			label, tip = "下一个匹配", "跳到下一个匹配"
		}
		if ui.Button(c.Key("find-next"), label).Tooltip(tip).Disabled(tab.loading).Clicked() || c.Shortcut(commandModifier(), ui.KeyG) {
			a.findNext(tab)
		}
		if find.Submitted() {
			a.findNext(tab)
		}
		if ui.Button(c.Key("compare"), "比较…").Tooltip("比较两个提交之间的改动").Clicked() {
			a.showCompare, a.compareRepo = true, tab.path
			a.compareFrom, a.compareTo, a.compareErr = "HEAD", tab.refInput, ""
			if commit := tab.selectedCommit(); commit != nil {
				a.compareTo = hashHex(commit.hash)
			}
		}
	}).Label("搜索与比较").Padding(0, 8, 2, 8).Gap(6)
	if tab.showRef {
		ui.Row(c).Padding(0, 8, 4, 8).Gap(8).Shrink(0).Children(func() {
			input := ui.TextInput(c.Key(fmt.Sprintf("ref-%d", tab.id)), &tab.refInput).Placeholder("分支、标签或 SHA，例如 HEAD~3").Grow(1).AutoFocus()
			if ui.Button(c.Key("open-ref"), "定位提交").Disabled(tab.refBusy).Clicked() || input.Submitted() {
				a.openRef(tab, tab.refInput)
			}
			if ui.Button(c.Key("hide-ref"), "收起").Clicked() {
				tab.showRef = false
			}
		})
	}
	if tab.searchText != "" {
		msg := "正在搜索历史…"
		if !tab.loading {
			if len(tab.commits) == 0 {
				msg = "没有匹配的提交，试试其他关键词"
			} else if tab.hasMore {
				msg = fmt.Sprintf("第 %d 个匹配 · 还可继续查找", tab.searchIndex+1)
			} else {
				msg = fmt.Sprintf("第 %d / %d 个匹配", tab.searchIndex+1, len(tab.commits))
			}
		}
		ui.Row(c).Padding(0, 8, 4, 8).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, msg).TextColor(t.TextMuted).Grow(1).MinWidth(0).SingleLine()
			if ui.Button(c.Key("clear-search"), "返回原历史").Clicked() {
				tab.query = ""
				a.searchHistory(tab, "")
			}
		})
	}
	if tab.refErr != "" || tab.scope == "指定 ref" {
		ui.Row(c).Padding(0, 8, 4, 8).Gap(8).Children(func() {
			if tab.scope == "指定 ref" && len(tab.historyRefs) == 1 {
				ui.Text(c, "历史起点 "+shortRef(tab.historyRefs[0])).TextColor(t.TextMuted)
			}
			if tab.refErr != "" {
				ui.Text(c, tab.refErr).TextColor(t.Danger).SingleLine().Grow(1).MinWidth(0).Tooltip(tab.refErr)
			}
		})
	}
}

type repoAction struct {
	kind, label, tip string
}

func syncActions() []repoAction {
	return []repoAction{
		{"Fetch", "获取…", "fetch：更新远程引用，不合并到当前分支"},
		{"拉取", "拉取…", "把远程提交合并到当前分支"},
		{"推送", "推送…", "把当前分支送到远程"},
	}
}

func (a *app) historyScope(c *ui.Context, tab *repoTab) {
	options := []string{"全部分支", "当前分支", "多选分支"}
	if tab.scope == "指定 ref" || tab.scope == "文件历史" || tab.scope == "搜索结果" {
		options = append(options, tab.scope)
	}
	if ui.Select(c.Key("history-scope"), &tab.scopeChoice, options).Width(112).Disabled(tab.loading).Changed() {
		switch tab.scopeChoice {
		case "全部分支":
			a.setHistory(tab, "全部分支", nil, zeroHash)
		case "当前分支":
			a.setHistory(tab, "当前分支", []string{"HEAD"}, zeroHash)
		case "多选分支":
			a.beginBranchFilter(tab)
			tab.scopeChoice = tab.scope
		}
	}
}

func shortRef(s string) string {
	if len(s) > 12 {
		return s[:12] + "…"
	}
	return s
}

func (a *app) commitPane(c *ui.Context) {
	tab := a.activeTab()
	t := c.Theme()
	if tab == nil {
		return
	}
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		ui.Column(c).Fill().Center().Children(func() {
			loadingMessage(c, tab.historyStarted, "正在读取仓库历史…")
		})
		return
	}
	if tab.holdAuto {
		tab.holdAuto = false
	} else if tab.hasMore && !tab.loading && tab.err == "" && len(tab.commits) > 0 && tab.list.AtEnd() {
		a.loadMore(tab)
	}
	tab.list.Selected = &tab.selected
	tab.list.Selection = &tab.selection
	tab.list.Key = func(i int) any {
		if commit := tab.rowData(i); commit != nil {
			return commit.hash
		}
		return "dirty"
	}
	tab.list.Label = func(i int) string {
		if commit := tab.rowData(i); commit != nil {
			return commit.subject
		}
		return "未提交更改"
	}

	columns := []ui.TableColumn{}
	if !tab.options.HideGraph {
		columns = append(columns, ui.TableColumn{ID: "graph", Title: "图谱", Width: graphWidth(tab.graph.lanes()), MinWidth: 40})
	}
	if !tab.options.HideSubject {
		columns = append(columns, ui.TableColumn{ID: "subject", Title: "描述 / 引用", MinWidth: 200})
	}
	if !tab.options.HideDate {
		columns = append(columns, ui.TableColumn{ID: "date", Title: "日期", Width: 150, MinWidth: 100})
	}
	if !tab.options.HideAuthor {
		columns = append(columns, ui.TableColumn{ID: "author", Title: "作者", Width: 120, MinWidth: 60})
	}
	if !tab.options.HideHash {
		columns = append(columns, ui.TableColumn{ID: "hash", Title: "哈希", Width: 85, MinWidth: 70})
	}
	tab.list.Columns = tab.tableLayout
	if tab.infoExpanded && tab.comparison == nil {
		a.commitSummary(c, tab)
	}
	table := ui.Table(c.Key(fmt.Sprintf("commits-%d", tab.id)), &tab.list, columns, tab.rows(), func(row, col int) {
		a.commitCell(c, tab, row, columns[col].ID)
	}).Grow(1).MinHeight(0)
	if tab.detailHidden && table.Clicked() {
		tab.detailHidden = false
		a.onCommitSelected(tab)
	}
	tab.tableLayout = tab.list.Columns
	if tab.rows() == 0 && !tab.loading {
		msg := "这个仓库还没有提交"
		if tab.searchText != "" {
			msg = "没有匹配的提交，请修改搜索词或返回原历史"
		}
		if tab.err != "" {
			msg = tab.err
		}
		ui.Text(c, msg).Padding(12).TextColor(t.TextMuted)
	}

	if tab.selected != tab.commitApplied {
		tab.detailHidden = false
		tab.stashView = nil
		tab.commitApplied = tab.selected
		tab.comparison = nil
		var from [20]byte
		to := tab.selectedCommit()
		if to != nil && tab.selection.Len() == 2 {
			for key := range tab.selection.All() {
				if hash, ok := key.([20]byte); ok && hash != to.hash {
					from = hash
				}
			}
		}
		if from != zeroHash {
			a.showComparison(tab, from, to.hash)
		} else {
			a.onCommitSelected(tab)
		}
	}
}

func (a *app) commitCell(c *ui.Context, tab *repoTab, i int, column string) {
	t := c.Theme()
	ci := tab.rowCommit(i)
	item := tab.rowData(i)
	if column == "graph" {
		padX, padY := c.Theme().Space(graphCellPadX), c.Theme().Space(graphCellPadY)
		ui.Box(c).Grow(1).MinHeight(28).AlignSelf(ui.Stretch).ContextMenu(func(m *ui.Menu) {
			a.commitMenu(c, m, tab, i)
		}).Draw(func(p *ui.Painter, r ui.Rect) {
			// 画进单元格内边距，上下缘与相邻行相接。单元格会裁掉超出的部分。
			full := ui.Rect{X: r.X - padX, Y: r.Y - padY, W: r.W + padX*2, H: r.H + padY*2}
			if item == nil {
				drawDirty(p, full, tab)
				return
			}
			paintCommit(p, full, item, ci == 0 && tab.dirty > 0, tab.options.GraphStyle, tab.palette)
		})
		return
	}
	row := ui.Row(c).Fill().MinHeight(28).Padding(2, 8).Gap(6).AlignItems(ui.Center)
	row.ContextMenu(func(m *ui.Menu) { a.commitMenu(c, m, tab, i) })
	row.Children(func() {
		if item == nil {
			if column == "subject" {
				ui.Textf(c, "未提交更改（%d）", tab.dirty).SingleLine()
			}
			return
		}
		commit := *item
		switch column {
		case "subject":
			if commit.hash == tab.head && tab.head != zeroHash {
				ui.Text(c, "HEAD").TextColor(t.Success).Shrink(0)
			}
			for j, ref := range tab.refs[commit.hash] {
				if j == 4 {
					ui.Text(c, "…")
					break
				}
				label := ref.name
				if ref.kind == refTag {
					label = "tag " + label
				}
				ui.Text(c, label).SingleLine().Shrink(0).TextColor(t.Success).ContextMenu(func(m *ui.Menu) { a.refMenu(c, m, tab, ref) })
			}

			text := commit.subject
			if commit.stash != "" {
				text = commit.stash + " " + text
			}
			if text == "" {
				text = "（无说明）"
			}
			e := ui.Text(c, text).SingleLine().Grow(1).MinWidth(0)
			if tab.searchText != "" || (tab.query != "" && ci >= 0 && tab.match(ci, strings.ToLower(tab.query))) {
				e.Background(t.Surface).Bold()
			}
		case "date":
			ui.Text(c, commit.whenText()).SingleLine().TextColor(t.TextMuted)
		case "author":
			ui.Text(c, commit.author).SingleLine().TextColor(t.TextMuted)
		case "hash":
			ui.Text(c, shortHash(commit.hash)).Font("monospace").SingleLine().TextColor(t.TextMuted)
		}
	})
}

func (a *app) commitMenu(c *ui.Context, m *ui.Menu, tab *repoTab, row int) {
	item := tab.rowData(row)
	if item == nil {
		if m.Item("Stash 未提交更改…").Chosen() {
			a.beginOperation(tab, "新建 Stash", "")
		}
		return
	}
	commit := *item
	if m.Item("复制哈希").Chosen() {
		c.WriteClipboard(hashHex(commit.hash))
		a.setFlash("已复制 " + shortHash(commit.hash))
	}
	if m.Item("复制主题").Chosen() {
		c.WriteClipboard(commit.subject)
	}
	if m.Item("在此提交新建分支…").Chosen() {
		a.beginOperation(tab, "新建分支", hashHex(commit.hash))
	}
	for _, kind := range []string{"检出提交", "Cherry-pick", "Revert", "Reset", "添加标签", "合并"} {
		if m.Item(kind + "…").Chosen() {
			a.beginOperation(tab, kind, hashHex(commit.hash))
		}
	}
	if commit.stash != "" {
		a.stashMenu(m, tab, commit)
	}
}

func (a *app) detailHeader(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	ui.Column(c).Padding(4, 8).Gap(4).Shrink(0).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			subject := "未提交更改 · 工作区相对 HEAD"
			if tab.comparison != nil {
				subject = "比较：从 " + shortHash(tab.comparison.from) + " → 到 " + shortHash(tab.comparison.to)
			} else if item := tab.selectedCommit(); item != nil {
				subject = item.subject
			}
			ui.Text(c, subject).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(subject)
			label := "放大 diff"
			if tab.focusDiff {
				label = "返回"
			}
			if ui.Button(c.Key("focus-diff"), label).Clicked() {
				tab.focusDiff = !tab.focusDiff
			}
			if ui.Button(c.Key("close-details"), "关闭详情").Clicked() {
				a.closeDetails(tab)
			}
		})
		if item := tab.selectedCommit(); item != nil && tab.comparison == nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				meta := item.author + " · " + item.whenText()
				if tab.branch != "" {
					meta = tab.branch + " · " + meta
				}
				ui.Text(c, meta).SingleLine().Grow(1).MinWidth(0).TextColor(t.TextMuted).Tooltip(meta)
				if ui.Button(c.Key("copy-hash"), shortHash(item.hash)).Tooltip("复制完整哈希").Clicked() {
					c.WriteClipboard(hashHex(item.hash))
					a.setFlash("已复制完整哈希")
				}
				label := "展开摘要"
				if tab.infoExpanded {
					label = "收起摘要"
				}
				if ui.Button(c.Key("expand-info"), label).Clicked() {
					tab.infoExpanded = !tab.infoExpanded
				}
			})
			if tab.infoExpanded {
				a.commitSummary(c, tab)
			}
		}
	})
}

func (a *app) commitSummary(c *ui.Context, tab *repoTab) {
	item := tab.selectedCommit()
	if item == nil {
		return
	}
	ui.Scroll(c.Key("commit-info")).MaxHeight(140).Shrink(0).Children(func() {
		ui.Column(c).Gap(4).Padding(4, 8).Children(func() {
			ui.Text(c, hashHex(item.hash)).Selectable()
			if tab.info != nil {
				ui.Text(c, tab.info.message).Selectable()
				ui.Textf(c, "作者：%s <%s>  %s", tab.info.author, tab.info.authorEmail, tab.info.authorDate).Selectable()
				ui.Textf(c, "提交者：%s <%s>  %s", tab.info.committer, tab.info.committerEmail, tab.info.committerDate).Selectable()
			}
			for _, parent := range item.parents {
				if ui.Button(c.Key("parent-"+hashHex(parent)), "父提交 "+hashHex(parent)).Clicked() {
					a.jumpHash(tab, parent)
				}
			}
		})
	})
}

func (a *app) filePaneView(c *ui.Context, tab *repoTab) {
	ui.Column(c).Fill().Children(func() {
		ui.Checkbox(c.Key("changed-tree"), &tab.fileTree, "树状目录")
		if tab.fileTree {
			rows := changedRows(tab.files, tab.closedFolders)
			ui.List(c.Key("changed-tree-list"), &tab.changeTreeList, len(rows), func(i int) {
				r := rows[i]
				label := r.label
				if r.dir {
					label = "▾ " + label
					if tab.closedFolders[r.path] {
						label = "▸ " + r.label
					}
				} else {
					label = tab.files[r.index].status + "  " + label
					if tab.files[r.index].oldPath != "" {
						label += " ← " + tab.files[r.index].oldPath
					}
				}
				e := ui.Button(c.Key(r.path), label).Margin(0, 0, 0, float32(r.depth*12)).Tooltip(r.path)
				if !r.dir {
					e.ContextMenu(func(m *ui.Menu) { a.fileMenu(c, m, tab, r.index) })
				}
				if e.Clicked() {
					if r.dir {
						if tab.closedFolders == nil {
							tab.closedFolders = map[string]bool{}
						}
						tab.closedFolders[r.path] = !tab.closedFolders[r.path]
					} else {
						tab.fileSel = r.index
						tab.fileMode = "diff"
						tab.fileApplied = tab.fileSel
						tab.diffOpen = true
						a.onFileSelected(tab)
					}
				}
			}).Grow(1).MinHeight(0)
		} else {
			tab.fileList.Selected = &tab.fileSel
			tab.fileList.Key = func(i int) any { return tab.files[i].layer + ":" + tab.files[i].path }
			tab.fileList.Label = func(i int) string { return tab.files[i].label() }
			ui.List(c.Key(fmt.Sprintf("files-%d", tab.id)), &tab.fileList, len(tab.files), func(i int) {
				ui.Text(c, tab.files[i].label()).Padding(4, 8).SingleLine().ContextMenu(func(m *ui.Menu) { a.fileMenu(c, m, tab, i) })
			}).Grow(1).MinHeight(0)
		}
		if len(tab.files) == 0 && !tab.detailLoading {
			ui.Text(c, "没有文件改动").Padding(12)
		}
		if !tab.detailHidden && tab.files != nil && tab.fileSel != tab.fileApplied {
			if tab.fileSel >= 0 && tab.fileSel < len(tab.files) {
				tab.diffOpen = true
				tab.fileMode = "diff"
			}
			tab.fileApplied = tab.fileSel
			a.onFileSelected(tab)
		}
	})
}

func (a *app) diffPane(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	width, _ := c.Size()
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Row(c).Padding(2, 8).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			label := "并排"
			if tab.layout == editor.DiffUnified {
				label = "统一"
			}
			if ui.Button(c.Key("layout"), label).Tooltip("切换并排或统一视图").Clicked() {
				if tab.layout == editor.DiffSplit {
					tab.layout = editor.DiffUnified
				} else {
					tab.layout = editor.DiffSplit
				}
				if tab.diff != nil {
					tab.diff.Layout = tab.layout
				}
			}
			if tab.fileSel >= 0 && tab.fileSel < len(tab.files) {
				path := tab.files[tab.fileSel].path
				ui.Text(c, path).SingleLine().Grow(1).MinWidth(40).Tooltip(path)
			} else {
				ui.Box(c).Grow(1).MinWidth(0)
			}
			if tab.diff != nil && width >= 1050 {
				if ui.Button(c.Key("expand-context"), "展开上下文").Clicked() {
					tab.diff.ExpandAll()
				}
				if ui.Button(c.Key("collapse-context"), "折叠上下文").Clicked() {
					tab.diff.CollapseAll()
				}
			} else if tab.diff != nil {
				ui.Button(c.Key("diff-context"), "上下文").Menu(func(m *ui.Menu) {
					if m.Item("展开上下文").Chosen() {
						tab.diff.ExpandAll()
					}
					if m.Item("折叠上下文").Chosen() {
						tab.diff.CollapseAll()
					}
				})
			}
		})
		switch {
		case tab.diffLoading:
			loadingMessage(c, tab.diffStarted, "正在读取所选文件的 diff…")
		case tab.detailNote != "":
			ui.Text(c, tab.detailNote).Padding(12).TextColor(t.TextMuted)
		case tab.diff != nil:
			editor.DiffView(c.Key(fmt.Sprintf("diff-%d", tab.id)), tab.diff).Grow(1).MinHeight(0)
		default:
			ui.Text(c, "选择左侧的文件").Padding(12).TextColor(t.TextMuted)
		}
	})
}

func (a *app) statusBar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Padding(4, 8).Gap(12).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		if os.Getenv("GITCLIENT_DEBUG") != "" {
			ui.Textf(c, "Go 堆 %.1f MB（不含窗口与 GPU）", float64(a.heap)/1e6).FontSize(12).TextColor(t.TextMuted)
		}
		if tab := a.activeTab(); tab != nil {
			status := tab.title
			if tab.branch != "" {
				status += " · " + tab.branch
			}
			if tab.dirty > 0 {
				status += fmt.Sprintf(" · %d 处未提交", tab.dirty)
			}
			ui.Text(c, status).SingleLine().MinWidth(0).TextColor(t.TextMuted).Tooltip(status)
			if tab.hasMore && !tab.loading {
				ui.Textf(c, "已显示 %d 条，滚动继续", len(tab.commits)).FontSize(12).TextColor(t.TextMuted).Shrink(0)
			}
		}
		if a.op != nil && a.op.Busy {
			ui.Text(c, operationTitle(a.op.Kind)+"正在执行…").FontSize(12)
			if ui.Button(c.Key("show-running-op"), "查看 / 取消").Clicked() {
				a.showOperation = true
			}
		}
		if n := a.releasedCount(); n > 0 && os.Getenv("GITCLIENT_DEBUG") != "" {
			ui.Textf(c, "后台标签已释放 %d", n).FontSize(12).TextColor(t.TextMuted)
		}
		if tab := a.activeTab(); tab != nil && tab.loading {
			ui.Text(c, "读取中").FontSize(12).TextColor(t.TextMuted)
		}
		if tab := a.activeTab(); tab != nil && tab.err != "" {
			ui.Text(c, tab.err).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		}
		if a.banner != "" {
			ui.Text(c, a.banner).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0).SingleLine()
		}
		if a.flash != "" {
			ui.Text(c, a.flash).FontSize(12).Grow(1).MinWidth(0).SingleLine()
		}
	})
}

func (a *app) releasedCount() int {
	n := 0
	for _, tab := range a.tabs {
		if tab.released {
			n++
		}
	}
	return n
}

func (a *app) recentModal(c *ui.Context) {
	ui.Modal(c.Key("recent-modal"), &a.showRecent, func() {
		ui.Column(c).Gap(8).Width(460).Children(func() {
			ui.Text(c, "最近的仓库").Bold()
			if a.prefs.RecentSort == "" {
				a.prefs.RecentSort = "最近使用"
			}
			ui.Select(c.Key("recent-sort"), &a.prefs.RecentSort, []string{"最近使用", "名称", "完整路径"})
			if len(a.recent) == 0 {
				ui.Text(c, "还没有。打开一个仓库后会出现在这里。").TextColor(c.Theme().TextMuted)
			}
			for i, p := range a.sortedRecent() {
				p := p
				if ui.Button(c.Key(fmt.Sprintf("recent-%d", i)), p).Clicked() {
					a.showRecent = false
					a.openPath(p)
				}
			}
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("recent-close"), "关闭").Clicked() {
					a.showRecent = false
				}
			})
		})
	})
}

func (a *app) sampleMem(c *ui.Context) {
	now := c.Now()
	if !a.heapAt.IsZero() && now.Sub(a.heapAt) < 2*time.Second {
		return
	}
	a.heap = readHeap()
	a.heapAt = now
}

func (a *app) expireFlash(c *ui.Context) {
	if a.flash == "" {
		return
	}
	if !c.Now().Before(a.flashUntil) {
		a.flash = ""
		return
	}
	c.After(time.Until(a.flashUntil))
}

func readHeap() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
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

func drawCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool) {
	paintCommit(p, r, c, contFromTop, "圆角", nil)
}

func paintCommit(p *ui.Painter, r ui.Rect, c *commit, contFromTop bool, style string, palette []ui.Color) {
	colorAt := func(col int) ui.Color {
		if len(palette) > 0 {
			return palette[max(0, col)%len(palette)]
		}
		return laneColor(col)
	}
	mid := r.Y + r.H/2
	for _, s := range c.segs {
		y0, y1 := r.Y, r.Y+r.H
		if !s.top {
			y0 = mid
		} else {
			y0--
		}
		if !s.bot {
			y1 = mid
		} else {
			y1++
		}
		// 线色跟着它进入的泳道，这样下一行同一列是同一种颜色。
		strokeLane(p, laneCenter(r, s.a), y0, laneCenter(r, s.b), y1, colorAt(s.b), style != "折线")
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y-1, x, mid, graphStroke, colorAt(c.nodeCol))
	}
	var node ui.Path
	node.Circle(laneCenter(r, c.nodeCol), mid, 3.75)
	p.FillPath(&node, colorAt(c.nodeCol))
}

// strokeLane 在两端沿泳道竖直进出，再弯向目标列。斜切的贝塞尔会在节点旁断开。
func strokeLane(p *ui.Painter, x0, y0, x1, y1 float32, color ui.Color, rounded bool) {
	if x0 == x1 || abs32(x0-x1) < 0.5 {
		p.Line(x0, y0, x1, y1, graphStroke, color)
		return
	}
	if !rounded {
		mid := (y0 + y1) / 2
		p.Line(x0, y0, x0, mid, graphStroke, color)
		p.Line(x0, mid, x1, mid, graphStroke, color)
		p.Line(x1, mid, x1, y1, graphStroke, color)
		return
	}
	var path ui.Path
	path.MoveTo(x0, y0)
	dy := y1 - y0
	path.CubeTo(x0, y0+dy*0.65, x1, y1-dy*0.65, x1, y1)
	p.StrokePath(&path, graphStroke, color)
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func drawDirty(p *ui.Painter, r ui.Rect, tab *repoTab) {
	col := 0
	if len(tab.commits) > 0 {
		col = tab.commits[0].nodeCol
	}
	color := ui.RGBA(196, 137, 58, 1)
	x := laneCenter(r, col)
	mid := r.Y + r.H/2
	p.Line(x, mid, x, r.Y+r.H+1, graphStroke, color)
	var node ui.Path
	node.Circle(x, mid, 3.75)
	p.FillPath(&node, color)
}

// Filled only by the p0bench build for repeatable native process measurements.
var (
	onAppReady func(*app)
	// onFrame observes a view build. Store only through setFrameHook.
	onFrame atomic.Value
	// benchExit is set by a measurement run that should not look successful.
	benchExit int
)

func setFrameHook(hook func(*app, time.Time)) {
	onFrame.Store(hook)
}

func main() {
	if askpassMain() {
		return
	}
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Xylring",
			Width:     1200,
			Height:    800,
			MinWidth:  860,
			MinHeight: 560,
			StateKey: func() string {
				if onAppReady != nil {
					return ""
				}
				return "main"
			}(),
			Content: ui.View(a.view),
		})
		a.win.OnClose(func(e *mygo.CloseEvent) { a.saveSession() })
		if onAppReady != nil {
			onAppReady(a)
		} else {
			a.restoreSession()
			for _, path := range os.Args[1:] {
				a.openPath(path)
			}
		}
		a.win.OnClosed(func() {
			if a.op != nil && a.op.Cancel != nil {
				a.op.Cancel()
			}
			for _, tab := range a.tabs {
				tab.releaseHeavy()
			}
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	if benchExit != 0 {
		os.Exit(benchExit)
	}
}

func commandModifier() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Cmd
	}
	return ui.Ctrl
}

func shownSidebar(preferred, width float32) float32 {
	return fitPane(preferred, 220, width*0.22, 148, 240)
}

// fitPane keeps a saved width when the window has room, and only the
// displayed width yields on a narrow window. The saved value comes back
// when the window grows, unless the user actually drags the divider.
func fitPane(preferred, fallback, cap, floor, ceiling float32) float32 {
	if preferred <= 0 {
		preferred = fallback
	}
	if cap <= 0 {
		return preferred
	}
	if cap > ceiling {
		cap = ceiling
	}
	if cap < floor {
		cap = floor
	}
	if preferred > cap {
		return cap
	}
	return preferred
}

func loadingMessage(c *ui.Context, started time.Time, description string) {
	elapsed := c.Now().Sub(started)
	if elapsed < 150*time.Millisecond {
		c.After(150*time.Millisecond - elapsed)
		return
	}
	label := "读取中…"
	if elapsed >= time.Second {
		label = description
	} else {
		c.After(time.Second - elapsed)
	}
	ui.Text(c, label).Padding(8, 12).TextColor(c.Theme().TextMuted)
}
