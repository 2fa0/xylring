package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

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
	a.dockerWriteGuardBanner(c, tab)
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
	if len(tab.commits) > 0 {
		a.dockerReconnectButton(c, tab)
	}
	if tab.loading && len(tab.commits) == 0 && tab.err == "" {
		busyBlock(c, "history-loading", tr("正在读取提交图谱…"), "").Grow(1)
		return
	}
	if tab.err != "" && len(tab.commits) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Gap(12).Children(func() {
			ui.Text(c, trMessage(tab.err)).TextColor(t.Danger).TextAlign(ui.Center)
			if isDockerRepoPath(tab.path) {
				a.dockerReconnectButton(c, tab)
				if ui.PrimaryButton(c.Key("docker-retry-pane"), tr("重试")).Disabled(tab.loading).Clicked() {
					a.reload(tab)
				}
			}
			// 仓库读不出来是因为 Git 本身不可用时，这里直接给重试入口。
			if !a.gitReady() && !isDockerRepoPath(tab.path) {
				if ui.PrimaryButton(c.Key("git-retry-pane"), tr("重新检测")).Disabled(a.git.checking).Clicked() {
					a.checkGit(true)
				}
			}
			// Git 正常却读取失败时也要能就地重试，否则用户只能关掉标签再打开。
			if a.gitReady() && !isDockerRepoPath(tab.path) {
				if ui.PrimaryButton(c.Key("history-retry-pane"), tr("重试")).Disabled(tab.loading).Clicked() {
					a.reload(tab)
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
		columns = slices.DeleteFunc(columns, func(col ui.TableColumn) bool {
			return col.ID == "graph"
		})
	}
	subjectColW := a.subjectColumnWidth(c, columns)
	var pointerY float32
	var pointerOver bool
	rowHovered := func(r ui.Rect) bool {
		return pointerOver && pointerY >= r.Y && pointerY < r.Y+r.H
	}
	graph := func() {
		if a.win != nil {
			a.loadVisibleHistoryStats(tab)
		}
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
						a.commitCell(c, tab, row, columns[col].ID, subjectColW, rowHovered)
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

const commitScrollTopThreshold = 8

func (a *app) commitScrollTopButton(c *ui.Context, tab *repoTab) {
	if tab.unavailable || tab.contentTab != 0 || tab.rows() == 0 || (tab.diffMaximized && a.hasInspectBottom(tab)) {
		return
	}
	first, _ := tab.list.Visible()
	if first < commitScrollTopThreshold {
		return
	}
	t := c.Theme()
	b := ui.ButtonBase(c.Key(fmt.Sprintf("commit-scroll-top-%d", tab.id))).
		Label(tr("回到顶部")).Tooltip(tr("回到顶部")).
		Size(26, 26).Radius(13).Center().Shrink(0).
		Cursor(ui.CursorPointer).Transition(fadeEnter())
	fg := t.TextMuted
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed).Border(1, t.Border.Alpha(0.8))
		fg = t.Text
	case b.Hovered():
		b.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.4))
		fg = t.Accent
	default:
		b.Background(t.Surface).Border(1, t.Border.Alpha(0.65))
	}
	b.Children(func() {
		ui.Icon(c, iconScrollTop).Size(14, 14).TextColor(fg)
	})
	b.OnClick(func() {
		tab.list.ScrollTo(0, ui.Start)
		tab.list.Focus(c)
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

// 徽章要的是真强调色：listTheme 把 Accent 换成了 SurfacePressed（行的按下色），
// 拿那个画图标，亮色模式下和行底色几乎一样，等于没画。
var (
	lightAccent = ui.LightTheme().Accent
	darkAccent  = ui.DarkTheme().Accent
)

// rowAccent 给列表行用真强调色，不受 listTheme 的覆盖影响。
func rowAccent(t *ui.Theme) ui.Color {
	if t.Dark {
		return darkAccent
	}
	return lightAccent
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

func (a *app) subjectColumnWidth(c *ui.Context, columns []ui.TableColumn) float32 {
	winW, _ := c.Size()
	mainW := winW
	if a.showRepoSidebar() {
		split := a.repoSplit
		if split <= 0 {
			split = defaultSidebarWidth
		}
		mainW -= split
	}
	// Table has Padding(0, 12) -> 24px horizontal padding.
	avail := mainW - 24
	var subjectMin float32
	for _, col := range columns {
		if col.ID == "subject" {
			subjectMin = col.MinWidth
			continue
		}
		w := col.Width
		if w <= 0 {
			w = col.MinWidth
		}
		avail -= w
	}
	if avail < subjectMin {
		return subjectMin
	}
	return avail
}

func worktreeBadgeColor(t *ui.Theme, label string, fallback ui.Color) ui.Color {
	switch {
	case strings.HasPrefix(label, tr("冲突")):
		return t.Danger
	case strings.HasPrefix(label, tr("已暂存")):
		return t.Success
	case strings.HasPrefix(label, tr("未暂存")):
		if t.Dark {
			return t.Warning
		}
		return ui.Hex("#D97706")
	case strings.HasPrefix(label, tr("未跟踪")):
		return t.TextMuted
	default:
		return fallback
	}
}

func (a *app) commitCell(c *ui.Context, tab *repoTab, i int, column string, subjectColW float32, rowHovered func(ui.Rect) bool) {
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
			if !a.refsInline && item.nodeCol > 0 && commitHasRefs(tab, item) {
				paintGuideLeft(p, full, laneCenter(full, item.nodeCol), commitRadius(item)+3, nil, item.nodeColor)
			}
			paintCommit(p, full, item, tab.worktreeConnectsFromTop(i), nil, bg, tab.provenance.Get(item.prov).Attribution == "inferred")
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
	gap := float32(6)
	switch column {
	case "refs":
		padEnd = 0
		gap = 4
	case "hash":
		padEnd = 16
	}
	cellRow := ui.Row(c).Fill().MinWidth(0).MinHeight(28).Padding(2, padEnd, 2, 8).Gap(gap).AlignItems(ui.Center)
	if !worktree && (tab.query.term != "" || tab.query.filePath != "") {
		commitHash := item.hash
		if cellRow.RowDoubleClicked() {
			a.locateCommitInGraph(tab, commitHash)
		}
	}
	cellRow.Children(func() {
		color := graphColor(item.nodeColor, nil)
		switch column {
		case "refs":
			if worktree {
				return
			}
			if !a.renderCommitRefBadges(c, tab, item, color, muted, false, 0) {
				return
			}
			ui.Box(c).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Draw(func(p *ui.Painter, r ui.Rect) {
				if r.W < 6 {
					return
				}
				var path ui.Path
				y := r.Y + r.H/2
				path.MoveTo(r.X, y)
				path.LineTo(r.X+r.W-1, y)
				path.Dash(0, 1.5, 3)
				p.StrokePath(&path, 1, color.Alpha(0.35))
			})
		case "subject":
			if worktree {
				ui.Text(c, tr("未提交改动")).FontWeight(600).SingleLine().Shrink(0)
				for _, label := range worktreeBadges(tab.worktreeFiles) {
					col := worktreeBadgeColor(t, label, color)
					chip(c, "worktree-badge-"+label, label, chipStyle{
						Text:       col,
						Background: col.Alpha(0.12),
						Border:     col.Alpha(0.32),
						FontSize:   11.5,
					})
				}
				ui.Box(c).Grow(1).MinWidth(0)
				return
			}
			if a.refsInline {
				a.renderCommitRefBadges(c, tab, item, color, muted, true, subjectColW)
			}
			if tab.outgoing[item.hash] {
				iconBadge(c, "sync-out-"+hashHex(item.hash), iconUpload, rowAccent(t), tr("待推送"))
			} else if tab.incoming[item.hash] {
				iconBadge(c, "sync-in-"+hashHex(item.hash), iconDownload, t.TextMuted, tr("远端独有"))
			}
			if prov := tab.provenance.Get(item.prov); prov.Agent != "" {
				label := prov.Badge
				if cW, _ := c.Size(); cW < 900 {
					label = "◎ " + prov.Agent
					if item.prov == 1 {
						label = prov.Badge
					}
				}
				tagCol := rowAccent(t)
				borderAlpha := float32(0.26)
				if strings.EqualFold(prov.Agent, "human") {
					tagCol = woodColor(t)
					borderAlpha = 0.28
				}
				bgCol := tagCol.Alpha(0.10)
				borderCol := tagCol.Alpha(borderAlpha)
				if selected {
					bgCol = tagCol.Alpha(0.16)
					borderCol = tagCol.Alpha(0.40)
				}
				pill := ui.Row(c.Key("prov-pill-"+hashHex(item.hash))).
					Padding(1, 6).Radius(9).AlignItems(ui.Center).Shrink(0).MaxWidth(220).Clip().
					Background(bgCol).Border(1, borderCol).
					Tooltip(prov.Description()).ContextMenu(func(m *ui.Menu) {
					if m.Item(tr("按此 Agent 筛选")).Chosen() {
						tab.searchKind = "Agent"
						tab.searchText = prov.Agent
						a.searchHistory(tab)
					}
					if prov.Model != "" && m.Item(tr("按此模型筛选")).Chosen() {
						tab.searchKind = "模型"
						tab.searchText = normalizeModel(prov.Model)
						a.searchHistory(tab)
					}
				})
				pill.Children(func() {
					ui.Text(c, label).SingleLine().FontSize(11).FontWeight(500).TextColor(tagCol)
				})
			}
			text := item.subject
			if text == "" {
				text = tr("（无说明）")
			}
			minSubjectW := float32(120)
			if subjectColW > 0 && subjectColW < 280 {
				minSubjectW = 80
			}
			e := ui.Text(c, text).SingleLine().Grow(1).MinWidth(minSubjectW)
			if len(item.parents) > 1 {
				e.TextColor(muted)
			}
		case "date":
			text := item.whenText()
			if worktree {
				text = tr("现在")
			}
			ui.Text(c, text).FontFeatures("tnum").SingleLine().TextColor(muted)
		case "author":
			ui.Text(c, item.author).SingleLine().TextColor(muted)
		case "hash":
			if worktree {
				ui.Text(c, tr("工作区")).SingleLine().TextColor(muted)
				return
			}
			full := hashHex(item.hash)
			btn := ui.ButtonBase(c.Key("copy-hash-"+full)).Label(tr("复制哈希")).Tooltip(full).
				Padding(1, 5).Radius(5).Gap(4).AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			if btn.Clicked() {
				c.Services().WriteClipboard(full)
				c.Toast(tr("已复制哈希"))
				btn.Jump("copied", 1)
			}
			copied := btn.AnimateTo("copied", 0, ui.Animation{Duration: 1500 * time.Millisecond}) > 0
			hashFg := muted
			switch {
			case copied:
				btn.Background(t.Success.Alpha(0.12))
				hashFg = t.Success
			case btn.Pressed():
				btn.Background(t.SurfacePressed)
			case btn.Hovered():
				btn.Background(t.SurfaceHover)
			}
			btn.Children(func() {
				if copied {
					ui.Icon(c, iconCheck).Size(11, 11).TextColor(t.Success).Shrink(0)
				}
				ui.Text(c, shortHash(item.hash)).Font("monospace").FontFeatures("tnum").SingleLine().TextColor(hashFg)
			})
		}
	})
}

func inlineRefLayoutBudget(subjectWidth float32, isHead bool, badges []commitRefBadge) (maxVisible int, maxBadgeW float32) {
	// Reserve enough room in the subject column so the commit subject remains visible.
	minSubjectW := float32(120)
	if subjectWidth < 280 {
		minSubjectW = max(80, subjectWidth*0.42)
	}
	// Account for cell padding (16) and gap between badges and subject (6).
	maxTotalW := subjectWidth - minSubjectW - 22
	if maxTotalW < 0 {
		maxTotalW = 0
	}
	if maxTotalW > 280 {
		maxTotalW = 280
	}

	maxBadgeW = 160
	maxCount := 3
	if maxTotalW < 220 {
		maxBadgeW = 120
		maxCount = 2
	}
	if maxTotalW < 150 {
		maxBadgeW = 92
		maxCount = 1
	}
	if maxBadgeW > maxTotalW-34 && maxTotalW > 56 {
		maxBadgeW = max(56, maxTotalW-34)
	}

	usedW := float32(0)
	if isHead {
		usedW = 48
	}
	for i, b := range badges {
		if i >= maxCount {
			break
		}
		est := float32(len([]rune(b.label())))*7 + 18
		if est > maxBadgeW {
			est = maxBadgeW
		}
		reserveMore := float32(0)
		if i+1 < len(badges) {
			reserveMore = 32
		}
		if usedW+est+reserveMore > maxTotalW {
			break
		}
		usedW += est + 4
		maxVisible = i + 1
	}
	return maxVisible, maxBadgeW
}

func (a *app) renderCommitRefBadges(c *ui.Context, tab *repoTab, item *commit, color, muted ui.Color, inline bool, subjectColW float32) bool {
	t := c.Theme()
	isHead := item.hash == tab.head && tab.head != zeroHash
	badges := groupCommitRefs(tab.refs[item.hash], tab.query.hideRemote)
	if !isHead && len(badges) == 0 {
		return false
	}
	if isHead {
		chip(c, "chip-head", "HEAD", chipStyle{Text: ui.RGBA(255, 255, 255, 1), Background: color, Bold: true}).
			Tooltip(tr("当前检出位置 (HEAD)"))
	}
	maxVisible := 1
	maxBadgeW := float32(0)
	if inline {
		maxVisible, maxBadgeW = inlineRefLayoutBudget(subjectColW, isHead, badges)
	} else {
		// 左侧引用列空间紧凑：默认展示主引用，其余折叠成 +N 徽章，悬停查看全部，右键可操作折叠引用。
		if !isHead && len(badges) == 2 && len(badges[0].name)+len(badges[1].name) <= 18 {
			maxVisible = 2
		}
	}
	hasMore := len(badges) > maxVisible
	for idx, badge := range badges {
		if idx == maxVisible {
			var rest []string
			collapsed := slices.Clone(badges[maxVisible:])
			for _, b := range collapsed {
				rest = append(rest, b.label())
			}
			commitCopy := *item
			more := chip(c, fmt.Sprintf("ref-more-%s", hashHex(item.hash)), fmt.Sprintf("+%d", len(rest)),
				chipStyle{Text: muted, Background: t.Surface, Border: t.Border}).
				Cursor(ui.CursorPointer).
				Tooltip(strings.Join(rest, "\n") + "\n" + tr("（右键操作更多引用）"))
			more.ContextMenu(func(m *ui.Menu) {
				for _, rb := range collapsed {
					b := rb
					m.Submenu(b.label(), func(sub *ui.Menu) {
						a.refBadgeMenu(c, sub, tab, commitCopy, b)
					})
				}
			})
			break
		}
		b := badge
		commitCopy := *item
		var pill ui.Element
		switch b.kind {
		case refBranch:
			tip := tr("本地分支 ") + b.name + tr("（右键更多操作）")
			if len(b.remotes) > 0 {
				tip = tr("本地分支 ") + b.name + tr(" · 同步远端 ") + strings.Join(b.remotes, ", ") + tr("（右键更多操作）")
			}
			pill = chip(c, fmt.Sprintf("ref-branch-%s-%s", hashHex(item.hash), b.name), b.name,
				chipStyle{Text: color, Background: color.Alpha(0.14), Border: color.Alpha(0.45), Bold: true, MaxWidth: maxBadgeW}).
				Cursor(ui.CursorPointer).Tooltip(tip)
			pill.ContextMenu(func(m *ui.Menu) {
				a.refBadgeMenu(c, m, tab, commitCopy, b)
			})
			if len(b.remotes) > 0 && (!hasMore && (!inline || subjectColW >= 360)) {
				pill.Children(func() {
					ui.Text(c, "· "+strings.Join(b.remotes, ",")).FontSize(11).SingleLine().MinWidth(0).Shrink(1).TextColor(muted)
				})
			}
		case refRemote:
			pill = chip(c, fmt.Sprintf("ref-remote-%s-%s", hashHex(item.hash), b.name), b.name,
				chipStyle{Text: muted, Background: t.Surface, Border: t.Border, MaxWidth: maxBadgeW}).
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
				a.refBadgeMenu(c, m, tab, commitCopy, b)
			})
		case refTag:
			pill = chip(c, fmt.Sprintf("ref-tag-%s-%s", hashHex(item.hash), b.name), tr("标签 ")+b.name,
				chipStyle{Text: t.Warning, Background: t.Warning.Alpha(0.14), Border: t.Warning.Alpha(0.45), MaxWidth: maxBadgeW}).
				Cursor(ui.CursorPointer).Tooltip(tr("标签 ") + b.name + tr("（右键更多操作）"))
			pill.ContextMenu(func(m *ui.Menu) {
				a.refBadgeMenu(c, m, tab, commitCopy, b)
			})
		}
		if !inline && pill.Valid() {
			pill.Shrink(1).MinWidth(0).Clip()
		}
	}
	return true
}

func (a *app) refBadgeMenu(c *ui.Context, m *ui.Menu, tab *repoTab, commitCopy commit, b commitRefBadge) {
	switch b.kind {
	case refBranch:
		a.branchBadgeMenu(c, m, tab, commitCopy, b.name)
	case refRemote:
		a.remoteBadgeMenu(c, m, tab, commitCopy, b.name)
	case refTag:
		a.tagBadgeMenu(c, m, tab, commitCopy, b.name)
	}
}
