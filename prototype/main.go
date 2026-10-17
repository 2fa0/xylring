// Git 客户端原型：验证 MyGo 原生界面能否画出提交图谱、打开 diff，
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
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	laneGap = 14
	lanePad = 10
)

var laneColors = []ui.Color{
	ui.RGBA(31, 120, 180, 1),
	ui.RGBA(51, 160, 44, 1),
	ui.RGBA(227, 26, 28, 1),
	ui.RGBA(255, 127, 0, 1),
	ui.RGBA(106, 61, 154, 1),
	ui.RGBA(177, 89, 40, 1),
	ui.RGBA(23, 190, 207, 1),
	ui.RGBA(188, 128, 189, 1),
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

	sidebarWidth float32
	split        float32
	filePane     float32

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
		split:        48,
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
	a.sampleMem(c)
	a.expireFlash(c)
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		a.tabBar(c)
		if a.activeTab() == nil {
			a.emptyState(c)
		} else {
			a.toolbar(c)
			if a.sidebarWidth == 0 {
				a.sidebarWidth = 220
			}
			ui.Split(c.Key("main-sidebar"), &a.sidebarWidth, func() { a.sidebar(c) }, func() {
				ui.SplitVerticalPercent(c, &a.split, func() {
					a.commitPane(c)
				}, func() {
					a.detail(c)
				}).Grow(1).MinHeight(0)
			}).Grow(1).MinHeight(0)
		}
		a.statusBar(c)
	})
	a.recentModal(c)
	a.branchFilterModal(c)
	a.comparisonModal(c)
	a.settingsModal(c)
	a.operationModal(c)
	a.credentialModal(c)
}

func (a *app) tabBar(c *ui.Context) {
	t := c.Theme()
	activateAt, closeAt := -1, -1
	moveID, moveTo := -1, -1
	ui.Row(c).Padding(6, 8).Gap(6).AlignItems(ui.Center).Background(t.Surface).Children(func() {
		ui.ScrollHorizontal(c.Key("tab-scroll")).Grow(1).MinWidth(0).Height(34).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				for i, tab := range a.tabs {
					label := tab.title
					if i == a.active && !tab.released {
						label = "● " + label
					}
					if tab.released {
						label = "○ " + label
					}
					button := ui.Button(c.Key(fmt.Sprintf("tab-%d", tab.id)), label).Drag(tab.id)
					if id, ok := ui.Drop[int](button); ok {
						moveID, moveTo = id, i
					}
					if button.Clicked() {
						activateAt = i
					}
					if ui.Button(c.Key(fmt.Sprintf("tabx-%d", tab.id)), "×").Clicked() {
						closeAt = i
					}
				}
			})
		})
		if ui.Button(c.Key("add-tab"), "+").Tooltip("打开仓库").Clicked() {
			a.pickRepo()
		}
		if ui.Button(c.Key("settings-open"), "设置").Clicked() {
			a.beginSettings()
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

func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		ui.Text(c, "Git 客户端原型").FontSize(28).Bold()
		ui.Text(c, "打开一个仓库，查看提交图谱和 diff。后台标签会释放已加载的历史。").
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
	if c.Shortcut(ui.Cmd, ui.KeyR) {
		a.reload(tab)
	}
	ui.Row(c).Padding(6, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tab.branch).Bold().Shrink(0)
		options := []string{"全部分支", "当前分支", "多选分支"}
		if tab.scope == "指定 ref" || tab.scope == "文件历史" || tab.scope == "搜索结果" {
			options = append(options, tab.scope)
		}
		if ui.Select(c.Key("history-scope"), &tab.scopeChoice, options).Width(130).Disabled(tab.loading).Changed() {
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
		if tab.scope == "多选分支" {
			if ui.Button(c.Key("edit-filter"), "选择分支…").Clicked() {
				a.beginBranchFilter(tab)
			}
		}
		if ui.Button(c.Key("toolbar-Fetch"), "Fetch…").Clicked() {
			a.beginOperation(tab, "Fetch", "")
		}
		ui.Button(c.Key("repo-operations"), "操作 ▾").Menu(func(m *ui.Menu) {
			for _, kind := range []string{"推送", "拉取", "新建 Stash", "中止合并", "中止 Cherry-pick", "中止 Revert", "外部合并工具"} {
				if m.Item(kind + "…").Chosen() {
					a.beginOperation(tab, kind, "")
				}
			}
			if m.Item("在终端打开仓库").Chosen() {
				a.openTerminal(tab.path)
			}
		})

		if ui.Button(c.Key("refresh"), "刷新").Clicked() {
			a.reload(tab)
		}
		if ui.Button(c.Key("open-more"), "打开").Clicked() {
			a.pickRepo()
		}
		if ui.Button(c.Key("recent-open"), "最近").Clicked() {
			a.showRecent = true
		}
		ui.Textf(c, "已加载 %d", len(tab.commits)).TextColor(t.TextMuted).Shrink(0)
		if tab.hasMore {
			ui.Text(c, "可继续").TextColor(t.TextMuted).Shrink(0)
		}
	})
	ui.Row(c).Padding(0, 8, 6, 8).Gap(8).AlignItems(ui.Center).Children(func() {
		input := ui.TextInput(c.Key(fmt.Sprintf("ref-%d", tab.id)), &tab.refInput).Placeholder("分支、标签或 SHA，例如 HEAD~3").Width(260)
		if ui.Button(c.Key("open-ref"), "打开提交").Disabled(tab.refBusy).Clicked() || input.Submitted() {
			a.openRef(tab, tab.refInput)
		}
		if tab.refBusy {
			ui.Text(c, "正在定位…").TextColor(t.TextMuted)
		}
		find := ui.TextInput(c.Key(fmt.Sprintf("find-%d", tab.id)), &tab.query).Placeholder("消息、日期、作者、哈希或 ref").Grow(1).MinWidth(100)
		if ui.Button(c.Key("find-next"), "下一个").Clicked() || c.Shortcut(ui.Cmd, ui.KeyG) {
			a.findNext(tab)
		}
		if find.Submitted() {
			a.findNext(tab)
		}
		if tab.searchText != "" && ui.Button(c.Key("clear-search"), "清除搜索").Clicked() {
			tab.query = ""
			a.searchHistory(tab, "")
		}
		if ui.Button(c.Key("compare"), "比较…").Clicked() {
			a.showCompare, a.compareRepo = true, tab.path
			a.compareFrom, a.compareTo, a.compareErr = "HEAD", tab.refInput, ""
			if commit := tab.selectedCommit(); commit != nil {
				a.compareTo = hashHex(commit.hash)
			}
		}
	})
	if tab.refErr != "" || tab.scope == "指定 ref" {
		ui.Row(c).Padding(0, 8, 6, 8).Gap(8).Children(func() {
			if tab.scope == "指定 ref" && len(tab.historyRefs) == 1 {
				ui.Text(c, "历史起点 "+tab.historyRefs[0][:7]).TextColor(t.TextMuted)
			}
			if tab.refErr != "" {
				ui.Text(c, tab.refErr).TextColor(t.Danger).SingleLine().Grow(1).MinWidth(0).Tooltip(tab.refErr)
			}
		})
	}
}

func (a *app) commitPane(c *ui.Context) {
	tab := a.activeTab()
	t := c.Theme()
	if tab == nil {
		return
	}
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		ui.Column(c).Fill().Center().Children(func() {
			ui.Text(c, "正在读取提交…").TextColor(t.TextMuted)
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
		columns = append(columns, ui.TableColumn{ID: "graph", Title: "图谱", Width: graphWidth(tab.graph.lanes()) + 16, MinWidth: 40})
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
	row := ui.Row(c).Fill().MinHeight(28).Padding(2, 8).Gap(6).AlignItems(ui.Center)
	row.ContextMenu(func(m *ui.Menu) { a.commitMenu(c, m, tab, i) })
	row.Children(func() {
		if column == "graph" {
			ui.Box(c).Grow(1).MinHeight(28).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
				if item == nil {
					drawDirty(p, r, tab)
					return
				}

				paintCommit(p, r, item, ci == 0 && tab.dirty > 0, tab.options.GraphStyle, tab.palette)
			})
			return
		}
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

func (a *app) detail(c *ui.Context) {
	tab := a.activeTab()
	t := c.Theme()
	if tab == nil {
		return
	}
	if tab.viewer != nil {
		a.viewerPane(c, tab)
		return
	}
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		if tab.detailHidden {
			ui.Text(c, "已定位匹配提交，单击提交打开详情").Padding(12)
			return
		}
		if tab.comparison == nil && tab.stashView == nil && (tab.selected < 0 || (tab.selectedCommit() == nil && !(tab.dirty > 0 && tab.selected == 0))) {
			ui.Text(c, "单击一条提交查看改动").Padding(12).TextColor(t.TextMuted)
			return
		}
		ui.Row(c).Padding(2, 8).Justify(ui.End).Children(func() {
			if ui.Button(c.Key("close-details"), "关闭详情").Clicked() {
				a.closeDetails(tab)
			}
		})
		a.detailHeader(c, tab)
		if tab.detailErr != "" {
			ui.Text(c, tab.detailErr).Padding(8, 12).TextColor(t.Danger)
		}
		if tab.detailLoading {
			ui.Text(c, "正在读取文件列表…").Padding(8, 12).TextColor(t.TextMuted)
		}
		ui.Split(c.Key(fmt.Sprintf("file-split-%d", tab.id)), &a.filePane, func() {
			a.filePaneView(c, tab)
		}, func() {
			a.diffPane(c, tab)
		}).Grow(1).MinHeight(0)
	})
}

func (a *app) detailHeader(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	if tab.comparison != nil {
		ui.Row(c).Padding(8, 12).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "比较 "+shortHash(tab.comparison.from)+" → "+shortHash(tab.comparison.to)).Bold().Grow(1)
			if ui.Button(c.Key("close-comparison"), "关闭比较").Clicked() {
				tab.comparison = nil
				a.onCommitSelected(tab)
			}
		})
		return
	}
	if tab.dirty > 0 && tab.selected == 0 {
		ui.Column(c).Padding(8, 12).Gap(4).Shrink(0).Children(func() {
			ui.Textf(c, "未提交更改（%d 个文件）", tab.dirty).Bold()
			if ui.Button(c.Key("dirty-stash"), "Stash 未提交更改…").Clicked() {
				a.beginOperation(tab, "新建 Stash", "")
			}
			ui.Text(c, "对比的是工作区相对 HEAD 的内容").FontSize(12).TextColor(t.TextMuted)
		})
		return
	}
	commit := tab.selectedCommit()
	if commit == nil {
		return
	}
	ui.Column(c).Padding(8, 12).Gap(4).Shrink(0).Children(func() {
		subject := commit.subject
		if subject == "" {
			subject = "（无说明）"
		}
		if tab.info == nil {
			ui.Text(c, subject).Bold()
		} else {
			ui.Scroll(c.Key("commit-info")).MaxHeight(140).Shrink(0).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, tab.info.message).Selectable()
					ui.Textf(c, "作者：%s <%s>  %s", tab.info.author, tab.info.authorEmail, tab.info.authorDate).FontSize(12).TextColor(t.TextMuted).Selectable()
					ui.Textf(c, "提交者：%s <%s>  %s", tab.info.committer, tab.info.committerEmail, tab.info.committerDate).FontSize(12).TextColor(t.TextMuted).Selectable()
				})
			})
		}
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, hashHex(commit.hash)).Font("monospace").FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c.Key("copy-hash"), "复制").Clicked() {
				c.WriteClipboard(hashHex(commit.hash))
				a.setFlash("已复制 " + shortHash(commit.hash))
			}
			ui.Text(c, commit.author).TextColor(t.TextMuted)
			ui.Text(c, commit.whenText()).TextColor(t.TextMuted)
		})
		if len(commit.parents) > 0 {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "父提交").FontSize(12).TextColor(t.TextMuted)
				for _, p := range commit.parents {
					p := p
					label := shortHash(p)
					if ui.Button(c.Key("parent-"+hashHex(p)), label).Clicked() {
						a.jumpHash(tab, p)
					}
				}
			})
		}
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
		if tab.fileSel != tab.fileApplied {
			tab.fileApplied = tab.fileSel
			tab.fileMode = "diff"
			a.onFileSelected(tab)
		}
	})
}

func (a *app) diffPane(c *ui.Context, tab *repoTab) {
	t := c.Theme()
	ui.Column(c).Fill().MinHeight(0).Children(func() {
		ui.Row(c).Padding(4, 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			label := "并排"
			if tab.layout == editor.DiffUnified {
				label = "统一"
			}
			if ui.Button(c.Key("layout"), label).Clicked() {
				if tab.layout == editor.DiffSplit {
					tab.layout = editor.DiffUnified
				} else {
					tab.layout = editor.DiffSplit
				}
				if tab.diff != nil {
					tab.diff.Layout = tab.layout
				}
			}
			if tab.diff != nil {
				if ui.Button(c.Key("expand-context"), "展开上下文").Clicked() {
					tab.diff.ExpandAll()
				}
				if ui.Button(c.Key("collapse-context"), "折叠上下文").Clicked() {
					tab.diff.CollapseAll()
				}
			}
			if tab.fileSel >= 0 && tab.fileSel < len(tab.files) {
				ui.Text(c, tab.files[tab.fileSel].path).SingleLine().Grow(1).MinWidth(0)
			}
		})
		switch {
		case tab.diffLoading:
			ui.Text(c, "正在读取 diff…").Padding(12).TextColor(t.TextMuted)
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
		ui.Textf(c, "Go 堆 %.1f MB", float64(a.heap)/1024/1024).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, "不含窗口和 GPU").FontSize(12).TextColor(t.TextMuted)
		if a.op != nil && a.op.Busy {
			ui.Text(c, a.op.Kind+"正在执行…").FontSize(12)
			if ui.Button(c.Key("show-running-op"), "查看 / 取消").Clicked() {
				a.showOperation = true
			}
		}
		if n := a.releasedCount(); n > 0 {
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
	var path ui.Path
	mid := r.Y + r.H/2
	for _, s := range c.segs {
		y0, y1 := r.Y, r.Y+r.H
		if !s.top {
			y0 = mid
		}
		if !s.bot {
			y1 = mid
		}
		color := colorAt(s.a)
		x0, x1 := laneCenter(r, s.a), laneCenter(r, s.b)
		if x0 == x1 {
			p.Line(x0, y0, x1, y1, 2, color)
			continue
		}
		path.Reset()
		path.MoveTo(x0, y0)
		if style == "折线" {
			path.LineTo(x1, y1)
		} else {
			path.CubeTo(x0, (y0+y1)/2, x1, (y0+y1)/2, x1, y1)
		}
		p.StrokePath(&path, 2, color)
	}
	if contFromTop {
		x := laneCenter(r, c.nodeCol)
		p.Line(x, r.Y, x, mid, 2, colorAt(c.nodeCol))
	}
	var node ui.Path
	node.Circle(laneCenter(r, c.nodeCol), mid, 4.5)
	p.FillPath(&node, colorAt(c.nodeCol))
}

func drawDirty(p *ui.Painter, r ui.Rect, tab *repoTab) {
	col := 0
	if len(tab.commits) > 0 {
		col = tab.commits[0].nodeCol
	}
	color := ui.RGBA(230, 160, 0, 1)
	x := laneCenter(r, col)
	mid := r.Y + r.H/2
	p.Line(x, mid, x, r.Y+r.H, 2, color)
	var node ui.Path
	node.Circle(x, mid, 4.5)
	p.FillPath(&node, color)
}

// Filled only by the p0bench build for repeatable native process measurements.
var onAppReady func(*app)

func main() {
	if askpassMain() {
		return
	}
	a := newApp()
	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Git 客户端原型",
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
}
