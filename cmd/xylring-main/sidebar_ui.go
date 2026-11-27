package main

import (
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

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
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, iconTreeRings).Size(28, 28).TextColor(woodColor(t)).Shrink(0)
					ui.Text(c, "Xylring").FontSize(26).Bold().LetterSpacing(-0.4)
				})
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c.Key("import-empty"), "").Label(tr("导入仓库")).Children(func() {
						ui.Icon(c, iconFolderPlus).Size(16, 16)
						ui.Text(c, tr("导入仓库"))
					}).Clicked() {
						a.pickRepo()
					}
					if dockerReposSupported() {
						if ui.Button(c.Key("import-docker-empty"), tr("打开容器仓库…")).Clicked() {
							a.openDockerRepoDialog()
						}
					}
					if runtime.GOOS == "windows" {
						if ui.Button(c.Key("import-wsl-empty"), tr("导入 WSL 仓库")).Clicked() {
							a.pickWSLRepo()
						}
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
	title := dockerRepoDisplayLabel(path, a.recent)
	row := ui.ButtonBase(c.Key(fmt.Sprintf("recent-%d", i))).Label(title).FillWidth().Padding(8, 10).Gap(10).Radius(t.Radius).Justify(ui.Start).AlignItems(ui.Center).Cursor(ui.CursorPointer).Transition(colorMotion())
	hovered := row.Hovered()
	switch {
	case row.Pressed():
		row.Background(t.SurfacePressed)
	case hovered:
		row.Background(t.SurfaceHover)
	}
	remove := false
	row.ContextMenu(func(m *ui.Menu) {
		a.repoColorMenu(m, path)
		a.dockerTerminalMenu(m, path)
		if !isDockerRepoPath(path) {
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
			if m.Item(a.openEditorMenuLabel()).Chosen() {
				if err := a.openInEditor(path); err != nil {
					a.setBanner("打开失败: "+err.Error(), path)
				}
			}
		}
		copyLabel := tr("复制路径")
		if isDockerRepoPath(path) {
			copyLabel = tr("复制容器内路径")
		}
		if m.Item(copyLabel).Chosen() {
			c.Services().WriteClipboard(repoCopyPath(path))
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
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				if tag, ok := a.repoColor(path); ok {
					ui.Box(c).Size(8, 8).Radius(4).Background(tag.color).Shrink(0)
				}
				ui.Text(c, title).SingleLine().MinWidth(0)
			})
			shown := homePath(path)
			sub := ui.Text(c, shown).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0)
			// 路径看得全就不弹黑框；只有副行被裁掉时才把整条路径放上去。
			if hovered || row.Focused() {
				w, _ := c.MeasureText(0, ui.Span{Text: shown, Size: 12})
				if tip := cutTip(repoLocationTooltip(path), w, sub.Bounds().W); tip != "" {
					sub.Tooltip(tip)
				}
			}
			if a.banner != "" && a.bannerPath == path {
				ui.Text(c, trMessage(bannerWithoutLocation(a.banner, path))).FontSize(12).TextColor(t.Danger)
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

// cutTip 返回 tip 只在内容放不下的时候：看得全的内容，悬停不该在它上面盖一个黑框。
func cutTip(tip string, width, room float32) string {
	if room <= 0 || width <= room+0.5 {
		return ""
	}
	return tip
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
	full := strings.ToLower(repoLocation(path))
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
	t := c.Theme()
	sidebarBg := ui.Hex("#F7F6F3")
	if t.Dark {
		sidebarBg = ui.Hex("#141519")
	}
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().MinHeight(0).Background(sidebarBg).Padding(6, 8, 8, 8).Children(func() {
		a.sidebarBoardRow(c)
		ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.55)).Margin(2, 2, 8, 2).Shrink(0)
		ui.Themed(c, listTheme(c)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			a.sidebarRepoSection(c)
			if !a.showBoard {
				if tab := a.activeTab(); tab != nil && !tab.unavailable {
					a.ringStatusControl(c, tab, "sidebar-ring-settings")
				}
			}
		})
	})
}

func (a *app) sidebarRepoSection(c *ui.Context) {
	active := ""
	if !a.showBoard {
		if tab := a.activeTab(); tab != nil {
			active = tab.path
		}
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		items := a.repoItems()
		usedColors := a.usedRepoColors(items)
		if a.repoColorFilter != "" && !slices.ContainsFunc(usedColors, func(tag repoColorTag) bool { return tag.id == a.repoColorFilter }) {
			a.repoColorFilter = ""
		}
		if len(usedColors) > 0 {
			a.sidebarColorFilterBar(c, usedColors)
		}
		visibleItems := a.visibleRepoItems()
		rows := a.sidebarRows(visibleItems)
		a.repoSel = -1
		for i, row := range rows {
			if row.path == active {
				a.repoSel = i
				break
			}
		}
		a.repoListState.Selected = &a.repoSel
		a.repoListState.Key = func(i int) any { return rows[i].path }
		if a.repoColorFilter == "" {
			a.repoListState.Reorder = func(dragged []int, to int) {
				a.reorderRepos(items, repoIndexes(rows, dragged), repoIndexAt(rows, to, len(items)))
			}
		} else {
			a.repoListState.Reorder = nil
		}
		closing := ""
		requestClose := func(p string) {
			closing = p
		}
		// 工作树子行按其状态区分菜单：失效工作树只提供清理记录与复制路径，正常工
		// 作树支持目录操作与安全移除；颜色标签只属于顶层仓库行。
		contextMenu := func(p, parentRepo string, wt *repoWorktree, allowColor bool) func(m *ui.Menu) {
			return func(m *ui.Menu) {
				if wt != nil && wt.prunable != "" {
					if m.Item(tr("清理失效记录")).Chosen() {
						a.runPruneWorktreeRecord(parentRepo, p)
					}
					m.Separator()
					if m.Item(tr("复制路径")).Chosen() {
						c.Services().WriteClipboard(repoCopyPath(p))
						c.Toast(tr("已复制路径"))
					}
					return
				}
				if allowColor {
					a.repoColorMenu(m, p)
				}
				a.dockerTerminalMenu(m, p)
				if !isDockerRepoPath(p) {
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
					if m.Item(a.openEditorMenuLabel()).Chosen() {
						if err := a.openInEditor(p); err != nil {
							a.setBanner("打开失败: "+err.Error(), p)
						}
					}
				}
				copyLabel := tr("复制路径")
				if isDockerRepoPath(p) {
					copyLabel = tr("复制容器内路径")
				}
				if m.Item(copyLabel).Chosen() {
					c.Services().WriteClipboard(repoCopyPath(p))
					c.Toast(tr("已复制路径"))
				}
				if wt != nil {
					m.Separator()
					if m.Item(tr("移除工作树…")).Chosen() {
						a.openWorktreeRemoveModal(parentRepo, *wt)
					}
					if a.tabIndex(p) >= 0 {
						if m.Item(tr("关闭标签页")).Chosen() {
							requestClose(p)
						}
					}
					return
				}
				m.Separator()
				if m.Item(tr("关闭仓库")).Chosen() {
					requestClose(p)
				}
			}
		}
		list := ui.List(c.Key("repositories"), &a.repoListState, len(rows), func(i int) {
			record := rows[i]
			p := record.path
			tabIdx := a.tabIndex(p)
			level := repoNestLevel(visibleItems, visibleItems[record.repo])
			if record.worktree != nil {
				level++
			}
			padL := float32(10) + float32(level)*c.Theme().Space(4)
			if record.worktree != nil {
				parentRepo := record.parentRepo
				a.sidebarWorktreeRow(c, p, *record.worktree, padL, p == active, contextMenu(p, parentRepo, record.worktree, false))
				return
			}
			isActive := p == active
			label := repoListLabel(a.foundFrom, visibleItems, p)
			branch, dirty, ahead, behind := a.repoStatus(p)
			if tabIdx < 0 || !a.tabs[tabIdx].loaded {
				if !a.isRepoSummaryFresh(p) {
					a.ensureRepoSummary(p)
				}
			}
			branchName := sidebarBranchName(branch)
			branchStatus := sidebarBranchStatus(dirty, ahead, behind)
			tip := tr("当前分支：") + displayBranch(branch)
			if dirty {
				tip += tr("（工作区有未提交或未跟踪的代码）")
			}
			row := ui.Row(c).FillWidth().MinHeight(36).Padding(8, 10, 8, padL).Gap(6).AlignItems(ui.Center)
			row.ContextMenu(contextMenu(p, p, nil, level == 0))
			row.Children(func() {
				th := c.Theme()
				if isActive {
					ui.Box(c).Absolute().Left(2).Top(6).Bottom(6).Width(3).Radius(1.5).Background(rowAccent(th)).IgnoreInput(true)
				}
				if level == 0 {
					if tag, ok := a.repoColor(p); ok {
						ui.Box(c).Size(8, 8).Radius(4).Background(tag.color).Shrink(0)
					}
				}
				if isDockerRepoPath(p) {
					// 类型标记只出现一次：名字已经说明是哪个容器。
					ui.Icon(c, iconContainer).Size(12, 12).TextColor(th.TextMuted).Shrink(0).Tooltip(tr("容器仓库"))
				}
				nameLbl := ui.Text(c, label).FontSize(13).SingleLine().MinWidth(0).Grow(1).Tooltip(repoLocationTooltip(p))
				if isActive {
					nameLbl.FontWeight(600)
				}
				if branchName != "" || branchStatus != "" {
					// 分支名可以被截断，状态标记不参与收缩：侧栏再窄也留得下状态。
					ui.Row(c).Gap(3).AlignItems(ui.Center).Shrink(0).MaxWidth(130).Tooltip(tip).Children(func() {
						ui.Icon(c, iconGitBranch).Size(12, 12).TextColor(th.TextMuted)
						if branchName != "" {
							ui.Text(c, branchName).FontSize(11).TextColor(th.TextMuted).SingleLine().MinWidth(0)
						}
						if branchStatus != "" {
							statusCol := th.TextMuted
							switch {
							case dirty:
								if th.Dark {
									statusCol = th.Warning
								} else {
									statusCol = ui.Hex("#B45309")
								}
							case ahead > 0 || behind > 0:
								statusCol = rowAccent(th)
							}
							statusLbl := ui.Text(c, branchStatus).FontSize(11).FontFeatures("tnum").TextColor(statusCol).SingleLine().Shrink(0)
							if dirty || ahead > 0 || behind > 0 {
								statusLbl.FontWeight(500)
							}
						}
					})
				}
				closeBtn := closeIconButton(c, "close-repo:"+p, tr("关闭 ")+label)
				if row.Hovered() || isActive || closeBtn.Hovered() || closeBtn.Focused() {
					closeBtn.Opacity(1)
				} else {
					closeBtn.Opacity(0.18)
				}
				if closeBtn.Clicked() {
					requestClose(p)
				}
			})
		}).Grow(1).MinHeight(0).Gap(2)
		if closing != "" {
			a.closeRepoTree(closing)
		} else if list.Changed() && a.repoSel >= 0 && a.repoSel < len(rows) {
			if wt := rows[a.repoSel].worktree; wt != nil {
				if wt.prunable != "" {
					a.openWorktreeRemoveModal(rows[a.repoSel].parentRepo, *wt)
				} else {
					a.openRepo(wt.path)
				}
			} else {
				a.openRoot(visibleItems[rows[a.repoSel].repo])
			}
		}
	})
}

func (a *app) sidebarColorFilterBar(c *ui.Context, usedColors []repoColorTag) {
	t := c.Theme()
	ui.Row(c).FillWidth().Padding(0, 4, 6, 4).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		allActive := a.repoColorFilter == ""
		allBtn := ui.ButtonBase(c.Key("color-filter-all")).
			Label(tr("全部")).
			Padding(2, 7).
			Radius(10).
			Cursor(ui.CursorPointer).
			Transition(colorMotion())
		if allActive {
			allBtn.Background(t.SurfaceHover)
		} else if allBtn.Hovered() {
			allBtn.Background(t.SurfaceHover.Alpha(0.6))
		}
		allBtn.Children(func() {
			col := t.TextMuted
			if allActive {
				col = t.Text
			}
			ui.Text(c, tr("全部")).FontSize(11).TextColor(col).SingleLine()
		})
		if allBtn.Clicked() {
			a.repoColorFilter = ""
		}
		for _, tag := range usedColors {
			active := a.repoColorFilter == tag.id
			dotBtn := ui.ButtonBase(c.Key("color-filter-"+tag.id)).
				Label(tr(tag.label)).
				Tooltip(tr(tag.label)).
				Size(16, 16).
				Radius(8).
				Center().
				Cursor(ui.CursorPointer).
				Transition(colorMotion())
			if active {
				dotBtn.Border(1.5, t.Text).Shadow(0, 0, 0, 1.5, tag.color.Alpha(0.35))
			} else if dotBtn.Hovered() {
				dotBtn.Background(t.SurfaceHover)
			}
			dotBtn.Children(func() {
				ui.Box(c).Size(10, 10).Radius(5).Background(tag.color).Shrink(0)
			})
			if dotBtn.Clicked() {
				if active {
					a.repoColorFilter = ""
				} else {
					a.repoColorFilter = tag.id
				}
			}
		}
		ui.Box(c).Grow(1)
		if paneIconButton(c, "sort-repos-by-color", "按标签颜色排序", iconExpandAll).Clicked() {
			a.sortReposByColor()
		}
	})
}

func (a *app) sidebarWorktreeOwnership(items []string) (map[string]bool, map[string]string, map[string][]repoWorktree) {
	worktrees := make(map[string][]repoWorktree, len(items))
	inAnyWorktree := map[string]bool{}
	for _, p := range items {
		wts := a.repoWorktrees(p)
		worktrees[p] = wts
		for _, wt := range wts {
			inAnyWorktree[wt.path] = true
		}
	}
	topLevel := make(map[string]bool, len(items))
	ownerOf := make(map[string]string, len(items))
	claimed := make(map[string]bool, len(items))
	claim := func(p string) {
		topLevel[p] = true
		claimed[p] = true
		ownerOf[p] = p
		for _, wt := range worktrees[p] {
			if !claimed[wt.path] {
				claimed[wt.path] = true
				ownerOf[wt.path] = p
			}
		}
	}
	for _, p := range items {
		if !inAnyWorktree[p] {
			claim(p)
		}
	}
	for _, p := range items {
		if !claimed[p] {
			claim(p)
		}
	}
	return topLevel, ownerOf, worktrees
}

func (a *app) visibleRepoItems() []string {
	items := a.repoItems()
	if a.repoColorFilter == "" {
		return items
	}
	usedColors := a.usedRepoColors(items)
	if !slices.ContainsFunc(usedColors, func(tag repoColorTag) bool { return tag.id == a.repoColorFilter }) {
		return items
	}
	topLevel, ownerOf, _ := a.sidebarWorktreeOwnership(items)
	groups := buildRepoGroups(items)
	visibleRoots := make(map[string]bool, len(groups))
	for _, g := range groups {
		if topLevel[g.root] && a.repoColors[g.root] == a.repoColorFilter {
			visibleRoots[g.root] = true
		}
	}
	visible := make([]string, 0, len(items))
	for _, g := range groups {
		rootVisible := visibleRoots[g.root] || (!topLevel[g.root] && visibleRoots[ownerOf[g.root]])
		if rootVisible {
			visible = append(visible, g.root)
		}
		for _, child := range g.children {
			if topLevel[child] {
				if rootVisible {
					visible = append(visible, child)
				}
			} else if visibleRoots[ownerOf[child]] {
				visible = append(visible, child)
			}
		}
	}
	return visible
}

// sidebarRow 是侧栏列表里的一行：仓库本身（worktree 为 nil），或挂在它下面的工作树。
type sidebarRow struct {
	path       string
	parentRepo string
	worktree   *repoWorktree
	repo       int // visibleItems 里的下标，工作树行指向它所属的仓库
}

// sidebarRows 摊平仓库列表：每个仓库后面跟它的工作树子行。
// 同一个 Git 仓库家族只选定唯一的展示父级（优先主工作树，其次列表里最靠前的项），
// 避免两个已打开的兄弟工作树互相把对方标成子行而一起消失。
// 先在完整仓库列表上确定归属，再按 items 筛选，防止筛选掉父仓库后把工作树误显示为普通仓库。
func (a *app) sidebarRows(items []string) []sidebarRow {
	fullItems := items
	if all := a.repoItems(); len(all) > 0 {
		fullItems = all
	}
	topLevel, _, worktrees := a.sidebarWorktreeOwnership(fullItems)
	rows := make([]sidebarRow, 0, len(items))
	for i, p := range items {
		if !topLevel[p] {
			continue
		}
		rows = append(rows, sidebarRow{repo: i, path: p, parentRepo: p})
		for _, wt := range worktrees[p] {
			wt := wt
			rows = append(rows, sidebarRow{repo: i, path: wt.path, parentRepo: p, worktree: &wt})
		}
	}
	return rows
}

// repoWorktrees 返回仓库的工作树：已打开且加载完成的标签页直接取它的，否则复用后台摘要。
func (a *app) repoWorktrees(path string) []repoWorktree {
	for _, tab := range a.tabs {
		if tab.path == path && tab.loaded {
			return tab.worktrees
		}
	}
	a.summaryMu.Lock()
	s, ok := a.repoSummaries[path]
	a.summaryMu.Unlock()
	if ok {
		return s.worktrees
	}
	return nil
}

func (a *app) tabIndex(path string) int {
	for i, tab := range a.tabs {
		if tab.path == path {
			return i
		}
	}
	return -1
}

// repoIndexes 把列表行号换回仓库下标：抓住工作树子行拖动等于拖动它所属的仓库。
func repoIndexes(rows []sidebarRow, indexes []int) []int {
	out := make([]int, 0, len(indexes))
	for _, i := range indexes {
		if i >= 0 && i < len(rows) {
			out = append(out, rows[i].repo)
		}
	}
	return out
}

func repoIndexAt(rows []sidebarRow, at, count int) int {
	if at >= 0 && at < len(rows) {
		return rows[at].repo
	}
	return count
}

// sidebarWorktreeRow 画一个工作树子行：比它所属的仓库低一层，主文案是分支，
// 后面是锁定 / 可清理两个图标，点开这个目录。
func (a *app) sidebarWorktreeRow(c *ui.Context, p string, wt repoWorktree, padL float32, isActive bool, menu func(m *ui.Menu)) {
	var dirty bool
	if wt.prunable == "" {
		_, dirty, _, _ = a.repoStatus(p)
		if !a.isRepoSummaryFresh(p) {
			a.ensureRepoSummary(p)
		}
	}
	tip := p
	if wt.prunable != "" {
		tip = p + "\n" + tr("目录已不存在，可清理")
	} else if isTempWorktreePath(p) {
		tip = p + "\n" + tr("临时目录")
	}
	row := ui.Row(c).FillWidth().MinHeight(28).Padding(6, 10, 6, padL).Gap(6).AlignItems(ui.Center)
	row.ContextMenu(menu)
	row.Children(func() {
		th := c.Theme()
		if isActive {
			ui.Box(c).Absolute().Left(2).Top(5).Bottom(5).Width(2.5).Radius(1.25).Background(rowAccent(th)).IgnoreInput(true)
		}
		label := ui.Text(c, worktreeRowLabel(wt)).FontSize(12).SingleLine().MinWidth(0).Grow(1).Tooltip(tip)
		if wt.prunable != "" {
			label.TextColor(th.TextMuted)
		} else if isActive {
			label.FontWeight(600)
		}
		if status := sidebarBranchStatus(dirty, 0, 0); status != "" {
			statusCol := th.Warning
			if !th.Dark {
				statusCol = ui.Hex("#B45309")
			}
			ui.Text(c, status).FontSize(12).FontWeight(600).FontFeatures("tnum").TextColor(statusCol).SingleLine().Shrink(0)
		}
		if wt.locked != "" {
			ui.Icon(c, iconLock).Size(12, 12).TextColor(th.TextMuted).Shrink(0).Tooltip(tr("已锁定"))
		}
		if wt.prunable != "" {
			ui.Icon(c, iconAlertCircle).Size(12, 12).TextColor(th.Warning).Shrink(0).Tooltip(tr("目录已不存在，可清理"))
		}
	})
}

func listTheme(c *ui.Context) *ui.Theme {
	theme := *c.Theme()
	theme.Accent = theme.SurfacePressed
	theme.AccentText = theme.Text
	return &theme
}

func (a *app) hasTabPath(path string) bool {
	for _, t := range a.tabs {
		if t.path == path {
			return true
		}
	}
	return false
}
