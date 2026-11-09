package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type historyQuery struct {
	term, kind string
	ref        string // 空值为全部引用，HEAD 为当前分支，其余为完整 ref。
	hideRemote bool
	filePath   string
	fileAt     [20]byte
	extraTip   [20]byte // 从任意快照的文件历史回到图谱时，保留该提交的可达历史。
}

type historyLocation struct {
	query       historyQuery
	hash        [20]byte
	top         [20]byte
	file        string
	source      *fileDiffSource
	diff        *editor.DiffState
	simpleDiff  *simpleDiff
	imageDiff   *imageDiff
	note        string
	from, to    [20]byte
	comparison  *commitComparison
	details     *commitDetails
	detailsOpen bool
	worktree    bool
	loaded      int
}

func (t *repoTab) location() *historyLocation {
	l := &historyLocation{query: t.query, hash: t.inspecting}
	l.loaded = len(t.commits)
	l.diff, l.note, l.from, l.to = t.diff, t.diffNote, t.diffFrom, t.diffTo
	l.simpleDiff, l.imageDiff, l.source = t.simpleDiff, t.imageDiff, t.diffSource
	l.comparison = t.comparison
	l.details, l.detailsOpen = t.details, t.detailsOpen
	l.worktree = t.worktree
	if first, _ := t.list.Visible(); first >= 0 {
		if item := t.rowData(first); item != nil && item.hash != worktreeHash {
			l.top = item.hash
		}
	}
	if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		l.file = t.compareFiles[t.fileSel].path
	}
	return l
}

func (q historyQuery) searchArgs() []string {
	if q.term == "" {
		return nil
	}
	switch q.kind {
	case "作者":
		return []string{"--regexp-ignore-case", "--fixed-strings", "--author=" + q.term}
	case "文件路径":
		return []string{"--full-history"}
	case "代码改动":
		return []string{"--regexp-ignore-case", "-S" + q.term}
	default:
		return []string{"--regexp-ignore-case", "--fixed-strings", "--grep=" + q.term}
	}
}

func searchPathspec(term string) string {
	term = strings.TrimPrefix(term, "./")
	term = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`).Replace(term)
	return ":(icase)*" + term + "*"
}

// 分页固定在本轮加载的提交上，外部新提交不会让 skip 页重复或漏项。
func queryTips(ctx context.Context, dir string, meta repoMeta, q historyQuery) ([]string, error) {
	if q.filePath != "" {
		return []string{hashHex(q.fileAt)}, nil
	}
	if q.ref == "HEAD" {
		if meta.head == zeroHash {
			return nil, nil
		}
		return []string{hashHex(meta.head)}, nil
	}
	if q.ref != "" {
		h, err := resolveCommit(ctx, dir, q.ref)
		if err != nil {
			return nil, err
		}
		return []string{hashHex(h)}, nil
	}
	set := map[[20]byte]bool{}
	if q.extraTip != zeroHash {
		set[q.extraTip] = true
	}
	if meta.head != zeroHash {
		set[meta.head] = true
	}
	for h, refs := range meta.refs {
		for _, ref := range refs {
			if !q.hideRemote || ref.kind != refRemote {
				set[h] = true
			}
		}
	}
	var tips []string
	for h := range set {
		tips = append(tips, hashHex(h))
	}
	sort.Strings(tips)
	return tips, nil
}

func (a *app) searchHistory(t *repoTab) {
	t.searchSuggestOpen = false
	term := strings.TrimSpace(t.searchText)
	if term == "" {
		a.leaveSearch(t)
		return
	}
	if t.historyBack == nil {
		t.historyBack = t.location()
	}
	q := t.query
	q.term, q.kind = term, t.searchKind
	a.changeHistory(t, q)
}

func (a *app) leaveSearch(t *repoTab) {
	if t.historyBack == nil {
		return
	}
	back := t.historyBack
	t.historyBack = nil
	t.searchText = ""
	t.locate = ""
	t.searchKind = "说明"
	t.searchSuggestOpen = false
	a.changeHistory(t, back.query)
	t.restore = back
}

func (a *app) changeHistory(t *repoTab, q historyQuery) {
	t.query = q
	t.list = ui.ListState{}
	t.selected = -1
	t.selection.Clear()
	t.worktree = false
	a.closeInspection(t)
	a.closeComparison(t)
	t.restore = nil
	t.restoreFile, t.restoreDiff = "", nil
	t.commits = nil
	t.commitIndex, t.authors = nil, nil
	t.graph = graph{}
	t.historyBytes, t.historyBudget = 0, 0
	a.reload(t)
}

func (a *app) continueRestore(t *repoTab) {
	l := t.restore
	if l == nil {
		return
	}
	if !containsLocation(t.commits, l) && t.hasMore && (l.loaded == 0 || len(t.commits) < l.loaded+pageFirst) {
		a.loadMore(t)
		return
	}
	t.restore = nil
	if !containsLocation(t.commits, l) {
		t.notice = "原阅读位置已不在已加载范围内"
	}
	if i := t.rowIndex(l.hash); i >= 0 {
		t.restoreFile = l.file
		t.restoreDiff = l
		a.selectCommit(t, i)
		t.details, t.detailsOpen = l.details, l.detailsOpen
		if l.detailsOpen && l.details == nil {
			t.detailsOpen = false
			a.toggleDetails(t)
		}
	} else if l.comparison != nil {
		t.restoreFile, t.restoreDiff = l.file, l
		cmp := l.comparison
		a.showComparison(t, cmp.from, cmp.to, cmp.fromLabel, cmp.toLabel, cmp.threeDot)
	}
	if i := t.rowIndex(l.top); i >= 0 {
		t.list.ScrollTo(i, ui.Start)
	}
	if l.worktree {
		a.showWorktree(t)
	}
}

type omniSuggestItem struct {
	kind     string
	icon     *ui.SVG
	title    string
	subtitle string
	action   func()
}

func isKnownRef(t *repoTab, text string) bool {
	if text == "HEAD" || text == "@" {
		return true
	}
	if text == t.branch {
		return true
	}
	for _, refs := range t.refs {
		for _, r := range refs {
			if r.name == text || "refs/heads/"+r.name == text || "refs/remotes/"+r.name == text || "refs/tags/"+r.name == text {
				return true
			}
		}
	}
	return false
}

func isHexCommitHash(s string) bool {
	if len(s) < 6 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func cutSearchPrefix(s string, prefixes ...string) (string, bool) {
	lower := strings.ToLower(s)
	for _, p := range prefixes {
		if strings.HasPrefix(lower, strings.ToLower(p)) {
			return strings.TrimSpace(s[len(p):]), true
		}
	}
	return s, false
}

func (a *app) omniSubmit(t *repoTab) {
	text := strings.TrimSpace(t.searchText)
	if text == "" {
		if t.query.term != "" {
			a.leaveSearch(t)
		} else {
			a.locateRef(t, "HEAD")
		}
		return
	}
	t.searchSuggestOpen = false

	if term, ok := cutSearchPrefix(text, "code:", "diff:", "content:", "代码:", "内容:", "代码改动:"); ok {
		t.searchKind = "代码改动"
		t.searchText = term
		a.searchHistory(t)
		return
	}
	if term, ok := cutSearchPrefix(text, "author:", "作者:"); ok {
		t.searchKind = "作者"
		t.searchText = term
		a.searchHistory(t)
		return
	}
	if term, ok := cutSearchPrefix(text, "path:", "file:", "文件:", "路径:"); ok {
		t.searchKind = "文件路径"
		t.searchText = term
		a.searchHistory(t)
		return
	}

	if t.query.term != "" && t.searchKind != "说明" {
		a.searchHistory(t)
		return
	}

	if isKnownRef(t, text) {
		t.locate = text
		a.locateRef(t, text)
		return
	}

	if isHexCommitHash(text) {
		t.locate = text
		a.locateRef(t, text)
		return
	}

	t.searchKind = "说明"
	a.searchHistory(t)
}

func (a *app) omniSuggestions(t *repoTab, term string) []omniSuggestItem {
	var items []omniSuggestItem
	clean := strings.TrimSpace(term)
	lower := strings.ToLower(clean)

	if term, ok := cutSearchPrefix(clean, "code:", "diff:", "content:", "代码:", "内容:", "代码改动:"); ok {
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconFileText,
			title:    fmt.Sprintf("在代码改动中搜索 “%s”", term),
			subtitle: "按代码改动 (Pickaxe -S) 检索提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "代码改动"
				t.searchText = term
				a.searchHistory(t)
			},
		})
		return items
	}
	if term, ok := cutSearchPrefix(clean, "author:", "作者:"); ok {
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconSearch,
			title:    fmt.Sprintf("按作者筛选 “%s”", term),
			subtitle: "按作者筛选提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "作者"
				t.searchText = term
				a.searchHistory(t)
			},
		})
		return items
	}
	if term, ok := cutSearchPrefix(clean, "path:", "file:", "文件:", "路径:"); ok {
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconFolder,
			title:    fmt.Sprintf("在修改文件路径中搜索 “%s”", term),
			subtitle: "按文件路径检索提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "文件路径"
				t.searchText = term
				a.searchHistory(t)
			},
		})
		return items
	}

	matchedBranches := 0
	matchedTags := 0
	for _, refs := range t.refs {
		for _, r := range refs {
			rLower := strings.ToLower(r.name)
			if clean == "" || strings.Contains(rLower, lower) {
				refName := r.name
				switch r.kind {
				case refBranch:
					if matchedBranches < 5 {
						matchedBranches++
						items = append(items, omniSuggestItem{
							kind:     "分支",
							icon:     iconGitBranch,
							title:    refName,
							subtitle: "本地分支 · 跳转定位",
							action: func() {
								t.searchSuggestOpen = false
								t.searchText = refName
								t.locate = refName
								a.locateRef(t, refName)
							},
						})
					}
				case refRemote:
					if !t.query.hideRemote && matchedBranches < 5 {
						matchedBranches++
						items = append(items, omniSuggestItem{
							kind:     "远程分支",
							icon:     iconGitBranch,
							title:    refName,
							subtitle: "远程分支 · 跳转定位",
							action: func() {
								t.searchSuggestOpen = false
								t.searchText = refName
								t.locate = refName
								a.locateRef(t, refName)
							},
						})
					}
				case refTag:
					if matchedTags < 3 {
						matchedTags++
						items = append(items, omniSuggestItem{
							kind:     "标签",
							icon:     iconLocate,
							title:    refName,
							subtitle: "标签 · 跳转定位",
							action: func() {
								t.searchSuggestOpen = false
								t.searchText = refName
								t.locate = refName
								a.locateRef(t, refName)
							},
						})
					}
				}
			}
		}
	}

	if clean != "" && isHexCommitHash(clean) {
		fullHex := strings.ToLower(clean)
		matchedCommits := 0
		for i := range t.commits {
			c := &t.commits[i]
			hHex := hashHex(c.hash)
			if strings.HasPrefix(hHex, fullHex) {
				commitHash := c.hash
				subj := c.subject
				matchedCommits++
				items = append(items, omniSuggestItem{
					kind:     "提交",
					icon:     iconLocate,
					title:    shortHash(commitHash) + " " + subj,
					subtitle: fmt.Sprintf("%s · %s", c.author, c.whenText()),
					action: func() {
						t.searchSuggestOpen = false
						t.searchText = shortHash(commitHash)
						t.locate = hHex
						a.jumpToHash(t, commitHash)
					},
				})
				if matchedCommits >= 3 {
					break
				}
			}
		}
	}

	if clean != "" && len(t.authors) > 0 {
		matchedAuthors := 0
		for author := range t.authors {
			if strings.Contains(strings.ToLower(author), lower) {
				matchedAuthors++
				aut := author
				items = append(items, omniSuggestItem{
					kind:     "作者",
					icon:     iconSearch,
					title:    aut,
					subtitle: "按作者筛选提交历史",
					action: func() {
						t.searchSuggestOpen = false
						t.searchKind = "作者"
						t.searchText = aut
						a.searchHistory(t)
					},
				})
				if matchedAuthors >= 3 {
					break
				}
			}
		}
	}

	if clean != "" {
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconSearch,
			title:    fmt.Sprintf("在提交说明中搜索 “%s”", clean),
			subtitle: "按提交说明检索提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "说明"
				t.searchText = clean
				a.searchHistory(t)
			},
		})
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconFileText,
			title:    fmt.Sprintf("在代码改动中搜索 “%s”", clean),
			subtitle: "按代码改动 (Pickaxe -S) 检索提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "代码改动"
				t.searchText = clean
				a.searchHistory(t)
			},
		})
		items = append(items, omniSuggestItem{
			kind:     "搜索",
			icon:     iconFolder,
			title:    fmt.Sprintf("在修改文件路径中搜索 “%s”", clean),
			subtitle: "按文件路径检索提交历史",
			action: func() {
				t.searchSuggestOpen = false
				t.searchKind = "文件路径"
				t.searchText = clean
				a.searchHistory(t)
			},
		})
	}

	return items
}

func (a *app) omniSuggestionsPopover(c *ui.Context, t *repoTab, anchor ui.Element) {
	ui.Popover(c, anchor, &t.searchSuggestOpen, func() {
		th := c.Theme()
		items := a.omniSuggestions(t, t.searchText)
		if len(items) == 0 {
			ui.Row(c).Padding(8, 12).Children(func() {
				ui.Text(c, "未找到匹配项，直接回车可搜索提交").FontSize(12).TextColor(th.TextMuted)
			})
			return
		}
		ui.Column(c).MaxHeight(320).Children(func() {
			for idx, item := range items {
				itemCopy := item
				row := ui.ButtonBase(c.Key(fmt.Sprintf("suggest-item-%d-%d", t.id, idx))).
					FillWidth().
					Padding(6, 10).
					Radius(th.Radius).
					Cursor(ui.CursorPointer).
					Transition(colorMotion())
				if row.Hovered() {
					row.Background(th.SurfaceHover)
				}
				if row.Clicked() {
					itemCopy.action()
				}
				row.Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
						if itemCopy.icon != nil {
							ui.Icon(c, itemCopy.icon).Size(14, 14).TextColor(th.TextMuted).Shrink(0)
						}
						ui.Column(c).Grow(1).Children(func() {
							ui.Text(c, itemCopy.title).FontSize(13).FontWeight(500).SingleLine().TextColor(th.Text)
							if itemCopy.subtitle != "" {
								ui.Text(c, itemCopy.subtitle).FontSize(11).SingleLine().TextColor(th.TextMuted)
							}
						})
						ui.Text(c, itemCopy.kind).FontSize(10).
							Background(th.Border.Alpha(0.4)).
							Padding(1, 6).
							Radius(3).
							TextColor(th.TextMuted).
							Shrink(0)
					})
				})
			}
		})
	})
}

func (a *app) historyToolbar(c *ui.Context, t *repoTab) {
	if t.query.filePath != "" {
		a.fileHistoryToolbar(c, t)
		return
	}
	if t.locate != "" && t.searchText == "" {
		t.searchText = t.locate
	}

	scopeControls := func() {
		scope := 0
		if !t.query.hideRemote {
			scope = 1
		}
		if ui.Segmented(c.Key("branch-visibility"), &scope, "本地分支", "全部分支").Label("分支显示范围").Shrink(0).Changed() {
			q := t.query
			q.ref, q.hideRemote = "", scope == 0
			a.changeScope(t, q)
		}
		a.scopeMenu(c, t)
	}

	omniSearchBox := func() {
		searchBox := ui.Row(c.Key(fmt.Sprintf("search-container-%d", t.id))).
			Grow(1).
			MinWidth(160).
			AlignItems(ui.Center).
			Gap(4)

		searchBox.Children(func() {
			input := ui.TextInput(c.Key(fmt.Sprintf("search-%d", t.id)), &t.searchText).
				Placeholder("搜索提交、代码、分支、作者或路径… (⌘P)").
				Bind(&t.searchInput).
				Grow(1).
				MinWidth(80).
				Error(t.locateErr)

			if input.Changed() {
				t.locate = t.searchText
				t.locateErr = ""
				t.locateGen++
				t.searchSuggestOpen = true
			}

			busy := t.loading && strings.TrimSpace(t.searchText) == t.query.term && t.searchKind == t.query.kind
			searchBtn := iconButton(c, "search-history", "搜索", iconSearch).Shrink(0).Disabled(busy).Tooltip("搜索或跳转 (Enter)")
			if searchBtn.Clicked() || (input.Submitted() && !busy) {
				a.omniSubmit(t)
			}

			if t.query.term != "" {
				if ui.Button(c.Key("search-locate"), "定位到此提交").Shrink(0).Disabled(t.inspecting == zeroHash).Clicked() {
					a.locateCommitInGraph(t, t.inspecting)
				}
				backBtn := ui.ButtonBase(c.Key("search-clear")).
					Label("返回").
					Tooltip("返回完整提交历史 (Esc)").
					Padding(2, 8).
					Radius(c.Theme().Radius).
					Background(c.Theme().SurfaceHover).
					Shrink(0).
					Cursor(ui.CursorPointer)
				backBtn.Children(func() {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconBack).Size(13, 13)
						ui.Text(c, "返回").FontSize(12)
					})
				})
				if backBtn.Clicked() {
					a.leaveSearch(t)
				}
			}
		})

		if t.searchSuggestOpen {
			a.omniSuggestionsPopover(c, t, searchBox)
		}
	}

	actionButtons := func() {
		fetchBtn := iconButton(c, "fetch", "获取", iconDownload).Shrink(0).Disabled(t.loading || a.fetch.busy)
		fetchBtn.ContextMenu(func(m *ui.Menu) {
			if m.Item("立即获取").Chosen() {
				a.quickFetch(t)
			}
			if m.Item("获取选项…").Chosen() {
				a.openFetch(t)
			}
		})
		if fetchBtn.Clicked() {
			a.quickFetch(t)
		}
		if iconButton(c, "compare", "比较", iconGitCompare).Shrink(0).Disabled(t.loading).Clicked() {
			a.openCompareModal(t)
		}
	}

	winW, _ := c.Size()
	mainW := winW
	if a.showRepoSidebar() {
		split := a.repoSplit
		if split <= 0 {
			split = 320
		}
		mainW -= split
	}
	singleRow := mainW >= 660
	if singleRow {
		ui.Row(c).Padding(4, 12, 8, 12).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			scopeControls()
			omniSearchBox()
			actionButtons()
		})
	} else {
		ui.Row(c).Padding(4, 12, 4, 12).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			scopeControls()
			actionButtons()
		})
		ui.Row(c).Padding(0, 12, 8, 12).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			omniSearchBox()
		})
	}
	if t.locateErr != "" {
		ui.Row(c).Padding(0, 12, 6, 12).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, iconAlertCircle).Size(14, 14).TextColor(c.Theme().Danger)
			ui.Text(c, t.locateErr).FontSize(12).TextColor(c.Theme().Danger).Grow(1).MinWidth(0).SingleLine()
		})
	}
}

func (a *app) scopeMenu(c *ui.Context, t *repoTab) {
	label := "筛选分支"
	if t.query.ref == "HEAD" {
		label = "当前分支"
	} else if t.query.ref != "" {
		label = strings.TrimPrefix(strings.TrimPrefix(t.query.ref, "refs/heads/"), "refs/remotes/")
	}
	th := c.Theme()
	sel := ui.SelectBase(c.Key("history-scope"), &t.query.ref)
	bg := th.Surface
	if sel.Trigger.Pressed() {
		bg = th.SurfacePressed
	} else if sel.Trigger.Hovered() {
		bg = th.SurfaceHover
	}
	sel.Trigger.Padding(th.Space(1.5), th.Space(3)).Gap(th.Space(1.5)).Radius(th.Radius).
		Background(bg).Border(1, th.Border).Justify(ui.SpaceBetween).MinWidth(88).MaxWidth(148).Shrink(0).Label("筛选分支")
	sel.Trigger.Children(func() {
		ui.Text(c, label).SingleLine().MinWidth(0)
		ui.Box(c).Size(th.Space(2.5), th.Space(2.5)).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) {
			var path ui.Path
			path.MoveTo(r.X+r.W*0.1, r.Y+r.H*0.3).LineTo(r.X+r.W*0.5, r.Y+r.H*0.7).LineTo(r.X+r.W*0.9, r.Y+r.H*0.3)
			p.StrokePath(&path, 1.5, th.TextMuted)
		})
	})
	maxH := th.Space(70)
	sel.Popup(func(panel ui.Element) {
		panel.Margin(th.Space(1), 0, 0, 0).Padding(th.Space(1.5)).Radius(th.Radius+2).Background(th.Background).Border(1, th.Border).MinWidth(220)
		panel.Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.SearchField(c.Key(fmt.Sprintf("branch-filter-%d", t.id)), &t.branchFilter).Placeholder("搜索分支…").Margin(0, 0, th.Space(1), 0)
		filter := strings.ToLower(strings.TrimSpace(t.branchFilter))
		ui.Scroll(c).MaxHeight(maxH).Children(func() {
			add := func(name, ref string) {
				item := sel.Item(ref).Padding(th.Space(1.5), th.Space(2.5)).Radius(th.Radius).Shrink(0)
				switch {
				case item.Highlighted():
					item.Background(th.Accent).TextColor(th.AccentText)
				case t.query.ref == ref:
					item.Background(th.Surface)
				}
				item.Children(func() { ui.Text(c, name).SingleLine() })
			}
			if filter == "" {
				add("范围内全部分支", "")
				add("当前分支", "HEAD")
				ui.Box(c).Height(1).Margin(th.Space(1), th.Space(2)).Background(th.Border).Shrink(0)
			}
			var branches []string
			for _, refs := range t.refs {
				for _, ref := range refs {
					if ref.kind == refBranch {
						branches = append(branches, "refs/heads/"+ref.name)
					} else if ref.kind == refRemote && !t.query.hideRemote {
						branches = append(branches, "refs/remotes/"+ref.name)
					}
				}
			}
			sort.Strings(branches)
			matched := 0
			for _, ref := range branches {
				rawName := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/")
				if filter != "" && !strings.Contains(strings.ToLower(rawName), filter) {
					continue
				}
				name := rawName
				if strings.HasPrefix(ref, "refs/remotes/") {
					name = "远程 · " + name
				} else {
					name = "本地 · " + name
				}
				add(name, ref)
				matched++
			}
			if filter != "" && matched == 0 {
				ui.Text(c, "无匹配分支").FontSize(12).TextColor(th.TextMuted).Padding(th.Space(1.5), th.Space(2.5))
			}
		})
	})
	if sel.Trigger.Changed() {
		a.changeScope(t, t.query)
	}
}

func (a *app) changeScope(t *repoTab, q historyQuery) {
	if t.historyBack != nil {
		t.historyBack = &historyLocation{query: historyQuery{ref: q.ref, hideRemote: q.hideRemote}}
	}
	a.changeHistory(t, q)
}
