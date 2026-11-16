package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type boardState struct {
	loaded        bool
	file          boardFile
	loadErr       string
	lastMod       time.Time
	lastSize      int64
	polling       bool
	repoFilter    string
	search        string
	searchInput   ui.Handle
	showArchived  bool
	selectedID    string
	editTaskID    string
	titleDraft    string
	titleInput    ui.Handle
	descDraft     string
	descInput     ui.Handle
	noteDraft     string
	noteInput     ui.Handle
	showNewModal  bool
	newTitle      string
	newDesc       string
	newStatus     boardStatus
	newRepos      []string
	newErr        string
	showLinkModal bool
	linkRepo      string
	linkRef       string
	linkErr       string
	linkBusy      bool
	showSnippet   bool
	commitValid   map[string]bool
	checkingLinks map[string]bool
}

func (a *app) ensureBoardLoaded() {
	if a.board.loaded {
		return
	}
	a.reloadBoardSync()
}

func (a *app) reloadBoardSync() {
	bf, err := loadBoardFile()
	if err != nil {
		a.board.loadErr = err.Error()
		a.board.loaded = true
		return
	}
	a.board.loadErr = ""
	if p, pErr := boardConfigPath(); pErr == nil {
		if info, sErr := os.Stat(p); sErr == nil {
			a.board.lastMod = info.ModTime()
			a.board.lastSize = info.Size()
		}
	}
	a.applyBoardFile(bf, nil)
	a.board.loaded = true
}

func (a *app) applyBoardFile(bf boardFile, c *ui.Context) {
	a.board.file = bf
	a.board.loadErr = ""
	if a.board.selectedID == "" {
		return
	}
	idx := findBoardTaskIndex(bf.Tasks, a.board.selectedID)
	if idx < 0 {
		a.board.selectedID = ""
		a.board.editTaskID = ""
		return
	}
	t := bf.Tasks[idx]
	if a.board.editTaskID != t.ID {
		a.syncBoardDrafts(t)
		return
	}
	if c == nil || !a.board.titleInput.Focused(c) {
		a.board.titleDraft = t.Title
	}
	if c == nil || !a.board.descInput.Focused(c) {
		a.board.descDraft = t.Description
	}
}

func (a *app) syncBoardDrafts(t boardTask) {
	a.board.editTaskID = t.ID
	a.board.titleDraft = t.Title
	a.board.descDraft = t.Description
	a.board.noteDraft = ""
}

func (a *app) selectBoardTask(id string) {
	if a.board.selectedID != "" && a.board.selectedID != id {
		a.commitBoardDrafts()
	}
	a.board.selectedID = id
	if idx := findBoardTaskIndex(a.board.file.Tasks, id); idx >= 0 {
		a.syncBoardDrafts(a.board.file.Tasks[idx])
		a.verifyBoardTaskCommits(a.board.file.Tasks[idx])
	}
}

func (a *app) closeBoardDrawer() {
	a.commitBoardDrafts()
	a.board.selectedID = ""
	a.board.editTaskID = ""
}

func (a *app) commitBoardDrafts() {
	if a.board.editTaskID == "" {
		return
	}
	idx := findBoardTaskIndex(a.board.file.Tasks, a.board.editTaskID)
	if idx < 0 {
		return
	}
	cur := a.board.file.Tasks[idx]
	var patch boardTaskPatch
	changed := false
	trimmedTitle := strings.TrimSpace(a.board.titleDraft)
	if trimmedTitle != "" && trimmedTitle != cur.Title {
		patch.Title = &trimmedTitle
		changed = true
	}
	if a.board.descDraft != cur.Description {
		desc := a.board.descDraft
		patch.Description = &desc
		changed = true
	}
	if !changed {
		return
	}
	bf, updated, err := boardUpdateTask(cur.ID, patch)
	if err != nil {
		a.setBanner(err.Error(), "")
		return
	}
	a.applyBoardFile(bf, nil)
	a.syncBoardDrafts(updated)
}

func (a *app) openBoard() {
	a.ensureBoardLoaded()
	a.showHome = false
	a.showBoard = true
	a.pollBoardFile()
}

func (a *app) pollBoardFile() {
	if a.closed || a.board.polling {
		return
	}
	lastMod := a.board.lastMod
	lastSize := a.board.lastSize
	loaded := a.board.loaded
	a.board.polling = true
	go func() {
		defer a.post(func() {
			a.board.polling = false
		})
		path, err := boardConfigPath()
		if err != nil {
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && loaded && len(a.board.file.Tasks) > 0 {
				a.post(func() {
					a.board.lastMod = time.Time{}
					a.board.lastSize = 0
					a.applyBoardFile(boardFile{Version: 1, NextSeq: 1}, nil)
				})
			}
			return
		}
		if loaded && info.ModTime().Equal(lastMod) && info.Size() == lastSize {
			return
		}
		bf, rErr := readBoardFileAt(path)
		if rErr != nil {
			return
		}
		mod := info.ModTime()
		size := info.Size()
		a.post(func() {
			a.board.lastMod = mod
			a.board.lastSize = size
			a.board.loaded = true
			a.applyBoardFile(bf, nil)
			if idx := findBoardTaskIndex(bf.Tasks, a.board.selectedID); idx >= 0 {
				a.verifyBoardTaskCommits(bf.Tasks[idx])
			}
		})
	}()
}

func boardCommitCacheKey(repo, hash string) string {
	return normalizeBoardRepo(repo) + "\x00" + strings.ToLower(strings.TrimSpace(hash))
}

func (a *app) verifyBoardTaskCommits(task boardTask) {
	if !a.gitReady() || len(task.Commits) == 0 {
		return
	}
	if a.board.commitValid == nil {
		a.board.commitValid = map[string]bool{}
	}
	if a.board.checkingLinks == nil {
		a.board.checkingLinks = map[string]bool{}
	}
	for _, link := range task.Commits {
		key := boardCommitCacheKey(link.Repo, link.Hash)
		if _, known := a.board.commitValid[key]; known || a.board.checkingLinks[key] {
			continue
		}
		a.board.checkingLinks[key] = true
		repo, hash := link.Repo, link.Hash
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := resolveCommit(ctx, repo, hash)
			ok := err == nil
			a.post(func() {
				delete(a.board.checkingLinks, key)
				if a.board.commitValid == nil {
					a.board.commitValid = map[string]bool{}
				}
				a.board.commitValid[key] = ok
			})
		}()
	}
}

func (a *app) openBoardCommit(link boardCommitLink) {
	h, ok := parseBoardHash20(link.Hash)
	if !ok {
		a.setBanner(fmt.Sprintf("无法解析哈希 %q", link.Hash), link.Repo)
		return
	}
	repoPath := link.Repo
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		root, err := repoRoot(ctx, repoPath)
		if err != nil {
			a.post(func() {
				a.setBanner(trimMsg(err.Error()), repoPath)
			})
			return
		}
		_, commitErr := resolveCommit(ctx, root, link.Hash)
		key := boardCommitCacheKey(root, link.Hash)
		a.post(func() {
			if a.board.commitValid == nil {
				a.board.commitValid = map[string]bool{}
			}
			a.board.commitValid[key] = commitErr == nil
			if commitErr != nil {
				a.pendingToast = "该提交已不存在，可能已被变基或修改"
				return
			}
			a.commitBoardDrafts()
			a.showBoard = false
			a.openRoot(root)
			if tab := a.activeTab(); tab != nil {
				if tab.query.term != "" || tab.query.filePath != "" || tab.query.ref != "" || tab.query.hideRemote {
					a.locateCommitInGraph(tab, h)
				} else {
					a.jumpToHash(tab, h)
				}
			}
		})
	}()
}

func parseBoardHash20(raw string) ([20]byte, bool) {
	var h [20]byte
	raw = strings.TrimSpace(raw)
	if len(raw) != 40 {
		return h, false
	}
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != 20 {
		return h, false
	}
	copy(h[:], b)
	return h, true
}

func (a *app) boardActiveCount() int {
	n := 0
	for _, t := range a.board.file.Tasks {
		if !t.Archived && t.Status != boardDone {
			n++
		}
	}
	return n
}

func (a *app) boardKnownRepos() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		norm := normalizeBoardRepo(p)
		if norm == "" || seen[norm] {
			return
		}
		seen[norm] = true
		out = append(out, norm)
	}
	for _, p := range a.repoItems() {
		add(p)
	}
	for _, p := range a.recent {
		add(p)
	}
	for _, t := range a.board.file.Tasks {
		for _, r := range boardTaskAllRepos(t) {
			add(r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(repoTitle(out[i])) < strings.ToLower(repoTitle(out[j]))
	})
	return out
}

func boardStatusColor(c *ui.Context, s boardStatus) ui.Color {
	t := c.Theme()
	switch s {
	case boardTodo:
		return t.TextMuted
	case boardDoing:
		return t.Accent
	case boardReview:
		return t.Warning
	case boardDone:
		return t.Success
	default:
		return t.TextMuted
	}
}

func boardRepoColor(c *ui.Context, repo string) ui.Color {
	t := c.Theme()
	palette := []ui.Color{
		t.Accent,
		t.Success,
		t.Warning,
		t.Danger,
		ui.RGB(147, 51, 234),
		ui.RGB(13, 148, 136),
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(repoTitle(repo))))
	return palette[h.Sum32()%uint32(len(palette))]
}

func boardTaskProvenanceSummary(t boardTask) string {
	for i := len(t.Commits) - 1; i >= 0; i-- {
		if t.Commits[i].Agent != "" {
			return t.Commits[i].Agent
		}
	}
	for i := len(t.Notes) - 1; i >= 0; i-- {
		if t.Notes[i].Author != "" && !strings.EqualFold(t.Notes[i].Author, "human") {
			return t.Notes[i].Author
		}
	}
	return ""
}

func (a *app) openNewBoardTaskModal(status boardStatus) {
	if status == "" {
		status = boardTodo
	}
	a.board.newTitle = ""
	a.board.newDesc = ""
	a.board.newStatus = status
	a.board.newRepos = nil
	if a.board.repoFilter != "" {
		a.board.newRepos = []string{a.board.repoFilter}
	} else if tab := a.activeTab(); tab != nil {
		a.board.newRepos = []string{tab.path}
	}
	a.board.newErr = ""
	a.board.showNewModal = true
}

func (a *app) openLinkCommitModal() {
	a.board.linkErr = ""
	a.board.linkRef = ""
	if a.board.linkRepo == "" {
		if tab := a.activeTab(); tab != nil {
			a.board.linkRepo = tab.path
		} else if idx := findBoardTaskIndex(a.board.file.Tasks, a.board.selectedID); idx >= 0 {
			repos := boardTaskAllRepos(a.board.file.Tasks[idx])
			if len(repos) > 0 {
				a.board.linkRepo = repos[0]
			}
		}
		if a.board.linkRepo == "" {
			if repos := a.boardKnownRepos(); len(repos) > 0 {
				a.board.linkRepo = repos[0]
			}
		}
	}
	a.board.showLinkModal = true
}

func (a *app) sidebarBoardRow(c *ui.Context) {
	t := c.Theme()
	a.ensureBoardLoaded()
	active := a.showBoard
	row := ui.ButtonBase(c.Key("sidebar-board-nav")).
		Label(tr("看板")).
		FillWidth().
		Padding(7, 10).
		Gap(8).
		Radius(t.Radius).
		Justify(ui.Start).
		AlignItems(ui.Center).
		Cursor(ui.CursorPointer).
		Margin(0, 0, 6, 0).
		Transition(colorMotion())
	switch {
	case active:
		row.Background(t.Accent.Alpha(0.14))
	case row.Pressed():
		row.Background(t.SurfacePressed)
	case row.Hovered():
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		fg := t.TextMuted
		if active {
			fg = t.Accent
		}
		ui.Icon(c, iconKanban).Size(15, 15).TextColor(fg).Shrink(0)
		lbl := ui.Text(c, tr("看板")).FontSize(13).Grow(1).MinWidth(0).SingleLine()
		if active {
			lbl.FontWeight(600).TextColor(t.Accent)
		} else {
			lbl.TextColor(t.Text)
		}
		if count := a.boardActiveCount(); count > 0 {
			ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11).TextColor(fg).Shrink(0)
		}
	})
	if row.Clicked() {
		a.openBoard()
	}
}

func (a *app) boardScreen(c *ui.Context) {
	a.ensureBoardLoaded()
	body := func() {
		ui.Column(c.Key("board-workspace")).Grow(1).MinWidth(0).MinHeight(0).Transition(workspaceMainMotion()).Children(func() {
			a.boardToolbar(c)
			if a.board.loadErr != "" {
				ui.Row(c.Key("board-error-banner")).FillWidth().Padding(6, 16).Background(c.Theme().Danger.Alpha(0.12)).Children(func() {
					ui.Text(c, trMessage(a.board.loadErr)).FontSize(12).TextColor(c.Theme().Danger)
				})
			}
			ui.Row(c.Key("board-body")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.boardColumns(c)
				if a.board.selectedID != "" {
					a.boardDetailDrawer(c)
				}
			})
		})
	}
	if !a.showRepoSidebar() {
		body()
		return
	}
	if a.repoSplit <= 0 {
		a.repoSplit = defaultSidebarWidth
	}
	ui.Split(c.Key("board-repo-split"), &a.repoSplit, func() {
		a.repoList(c)
	}, body).Grow(1).MinHeight(0)
}

func (a *app) boardToolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c.Key("board-toolbar")).FillWidth().Padding(8, 16).Gap(8).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Shrink(0).Children(func() {
		filterLabel := tr("全部项目")
		if a.board.repoFilter != "" {
			filterLabel = repoTitle(a.board.repoFilter)
		}
		filterBtn := ui.Button(c.Key("board-repo-filter"), filterLabel).Padding(4, 10).FontSize(12)
		filterBtn.Menu(func(m *ui.Menu) {
			if m.Item(tr("全部项目")).Checked(a.board.repoFilter == "").Chosen() {
				a.board.repoFilter = ""
			}
			repos := a.boardKnownRepos()
			if len(repos) > 0 {
				m.Separator()
			}
			for _, rp := range repos {
				repoPath := rp
				if m.Item(repoTitle(repoPath)).Checked(a.board.repoFilter == repoPath).Chosen() {
					a.board.repoFilter = repoPath
				}
			}
		})

		ui.SearchField(c.Key("board-search"), &a.board.search).
			Placeholder(tr("搜索任务编号、标题、提交或备注")).
			Label(tr("搜索看板任务")).
			Bind(&a.board.searchInput).
			Width(240).
			FontSize(12)

		archLabel := tr("显示已归档")
		archBtn := ui.ButtonBase(c.Key("board-toggle-archived")).
			Label(archLabel).
			Padding(4, 10).
			Radius(t.Radius).
			Cursor(ui.CursorPointer).
			Transition(colorMotion())
		if a.board.showArchived {
			archBtn.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.4))
		} else {
			archBtn.Border(1, t.Border)
		}
		archBtn.Children(func() {
			fg := t.TextMuted
			if a.board.showArchived {
				fg = t.Accent
			}
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconArchive).Size(13, 13).TextColor(fg)
				ui.Text(c, archLabel).FontSize(12).TextColor(fg).SingleLine()
			})
		})
		if archBtn.Clicked() {
			a.board.showArchived = !a.board.showArchived
		}

		ui.Box(c).Grow(1)

		if ui.Button(c.Key("board-agent-snippet"), tr("Agent 规则")).Padding(4, 10).FontSize(12).Clicked() {
			a.board.showSnippet = true
		}
		if ui.PrimaryButton(c.Key("board-new-task"), tr("+ 新建任务")).Padding(4, 12).FontSize(12).Clicked() {
			a.openNewBoardTaskModal(boardTodo)
		}
	})
}

func (a *app) boardColumns(c *ui.Context) {
	t := c.Theme()
	colBg := t.Surface
	ui.ScrollHorizontal(c.Key("board-columns")).Grow(1).MinWidth(0).MinHeight(0).
		Padding(12, 16, 16, 16).Gap(12).Children(func() {
		for _, status := range boardStatuses {
			st := status
			var tasks []boardTask
			for _, task := range a.board.file.Tasks {
				if task.Status == st && boardMatchesFilter(task, a.board.repoFilter, a.board.search, a.board.showArchived) {
					tasks = append(tasks, task)
				}
			}
			ui.Column(c.Key("board-col-"+string(st))).
				MinWidth(235).Grow(1).FillHeight().MinHeight(0).
				Radius(10).Background(colBg).Border(1, t.Border.Alpha(0.65)).
				Padding(10, 8, 8, 8).Gap(8).Children(func() {
				ui.Row(c.Key("board-col-head-"+string(st))).FillWidth().Padding(0, 4).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
					chipSoft(c, "board-col-badge-"+string(st), tr(boardStatusLabel(st)), boardStatusColor(c, st), true)
					ui.Text(c, fmt.Sprintf("%d", len(tasks))).FontSize(12).TextColor(t.TextMuted).SingleLine()
					ui.Box(c).Grow(1)
					if paneIconButton(c, "board-col-add-"+string(st), tr("在此列新建任务"), iconPlus).Clicked() {
						a.openNewBoardTaskModal(st)
					}
					if st == boardDone {
						moreBtn := paneIconButton(c, "board-done-more", tr("已完成列操作"), iconMore)
						moreBtn.Menu(func(m *ui.Menu) {
							if m.Item(tr("归档已完成任务")).Disabled(len(tasks) == 0).Chosen() {
								bf, count, err := boardArchiveDone()
								if err != nil {
									a.setBanner(err.Error(), "")
								} else {
									a.applyBoardFile(bf, c)
									if count > 0 {
										c.Toast(fmt.Sprintf(tr("已归档 %d 个任务"), count))
									}
								}
							}
						})
					}
				})
				ui.Scroll(c.Key("board-col-scroll-"+string(st))).Grow(1).MinHeight(0).FillWidth().Children(func() {
					if len(tasks) == 0 {
						ui.Box(c).FillWidth().Padding(24, 8).Center().Children(func() {
							ui.Text(c, tr("暂无任务")).FontSize(12).TextColor(t.TextMuted)
						})
						return
					}
					ui.Column(c).FillWidth().Gap(8).Padding(2, 2, 4, 2).Children(func() {
						for _, task := range tasks {
							a.boardCard(c, task)
						}
					})
				})
			})
		}
	})
}

func (a *app) boardCard(c *ui.Context, task boardTask) {
	t := c.Theme()
	selected := a.board.selectedID == task.ID
	card := ui.ButtonBase(c.Key("board-card-"+task.ID)).
		Label(task.ID + " " + task.Title).
		FillWidth().
		Padding(10, 12).
		Radius(8).
		Background(t.Background).
		Cursor(ui.CursorPointer).
		Transition(colorMotion())

	switch {
	case selected:
		card.Border(1.5, t.Accent)
	case card.Hovered():
		card.Border(1, t.Accent.Alpha(0.55))
	default:
		card.Border(1, t.Border)
	}

	var moveStatus boardStatus
	hasMove := false
	doArchive := false
	doUnarchive := false
	doDelete := false

	card.ContextMenu(func(m *ui.Menu) {
		for _, st := range boardStatuses {
			target := st
			if m.Item(tr("移至")+" "+tr(boardStatusLabel(target))).Checked(task.Status == target).Disabled(task.Status == target).Chosen() {
				moveStatus = target
				hasMove = true
			}
		}
		m.Separator()
		if m.Item(tr("复制任务编号")).Chosen() {
			c.Services().WriteClipboard(task.ID)
			c.Toast(tr("已复制任务编号"))
		}
		if task.Archived {
			if m.Item(tr("取消归档")).Chosen() {
				doUnarchive = true
			}
		} else if task.Status == boardDone {
			if m.Item(tr("归档任务")).Chosen() {
				doArchive = true
			}
		}
		if m.Item(tr("删除任务")).Chosen() {
			doDelete = true
		}
	})

	checkClicked := false
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
				titleEl := ui.Text(c, task.Title).FontSize(13).MaxLines(2).Grow(1).MinWidth(0)
				if task.Status == boardDone || task.Archived {
					titleEl.TextColor(t.TextMuted)
				} else {
					titleEl.TextColor(t.Text)
				}
				ui.Text(c, task.ID).FontSize(11).TextColor(t.TextMuted).Shrink(0).Padding(2, 0, 0, 0)
				tip := tr("标记为已完成")
				if task.Status == boardDone {
					tip = tr("移回待办")
				}
				chk := paneToggleIconButton(c, "board-card-check-"+task.ID, tip, tip, iconCheck, task.Status == boardDone)
				if chk.Clicked() {
					checkClicked = true
				}
			})

			repos := boardTaskAllRepos(task)
			prov := boardTaskProvenanceSummary(task)
			if len(repos) > 0 || len(task.Commits) > 0 || len(task.Notes) > 0 || prov != "" || task.Archived {
				ui.Row(c).FillWidth().Wrap().Gap(4).AlignItems(ui.Center).Children(func() {
					for _, rp := range repos {
						chipSoft(c, "board-card-repo-"+task.ID+"-"+rp, repoTitle(rp), boardRepoColor(c, rp), false)
					}
					if len(task.Commits) > 0 {
						chipSoft(c, "board-card-commits-"+task.ID, fmt.Sprintf(tr("%d 提交"), len(task.Commits)), t.TextMuted, false)
					}
					if prov != "" {
						chipSoft(c, "board-card-agent-"+task.ID, prov, t.Accent, false)
					}
					if len(task.Notes) > 0 {
						chipSoft(c, "board-card-notes-"+task.ID, fmt.Sprintf(tr("%d 备注"), len(task.Notes)), t.TextMuted, false)
					}
					if task.Archived {
						chipSoft(c, "board-card-arch-"+task.ID, tr("已归档"), t.TextMuted, false)
					}
				})
			}
		})
	})

	if checkClicked {
		next := boardDone
		if task.Status == boardDone {
			next = boardTodo
		}
		bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{Status: &next})
		if err != nil {
			a.setBanner(err.Error(), "")
		} else {
			a.applyBoardFile(bf, c)
			if a.board.selectedID == updated.ID {
				a.syncBoardDrafts(updated)
			}
		}
		return
	}
	if hasMove {
		bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{Status: &moveStatus})
		if err != nil {
			a.setBanner(err.Error(), "")
		} else {
			a.applyBoardFile(bf, c)
			if a.board.selectedID == updated.ID {
				a.syncBoardDrafts(updated)
			}
		}
		return
	}
	if doArchive || doUnarchive {
		v := doArchive
		bf, _, err := boardUpdateTask(task.ID, boardTaskPatch{Archived: &v})
		if err != nil {
			a.setBanner(err.Error(), "")
		} else {
			a.applyBoardFile(bf, c)
		}
		return
	}
	if doDelete {
		bf, err := boardDeleteTask(task.ID)
		if err != nil {
			a.setBanner(err.Error(), "")
		} else {
			a.applyBoardFile(bf, c)
		}
		return
	}
	if card.Clicked() {
		a.selectBoardTask(task.ID)
	}
}

func (a *app) boardDetailDrawer(c *ui.Context) {
	t := c.Theme()
	idx := findBoardTaskIndex(a.board.file.Tasks, a.board.selectedID)
	if idx < 0 {
		a.board.selectedID = ""
		return
	}
	task := a.board.file.Tasks[idx]
	if a.board.editTaskID != task.ID {
		a.syncBoardDrafts(task)
		a.verifyBoardTaskCommits(task)
	}

	ui.Column(c.Key("board-detail-drawer-"+task.ID)).
		Width(430).FillHeight().MinHeight(0).Shrink(0).
		Background(t.Background).BorderWidth(0, 0, 0, 1).BorderColor(t.Border).Children(func() {
		// Top drawer header
		ui.Row(c.Key("board-drawer-header")).FillWidth().Padding(10, 16).Gap(8).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Shrink(0).Children(func() {
			chipSoft(c, "board-drawer-id", task.ID, t.Accent, true)
			copyIconButton(c, "board-drawer-copy-id", tr("复制任务编号"), task.ID)
			ui.Box(c).Grow(1)
			archTip := tr("归档任务")
			if task.Archived {
				archTip = tr("取消归档")
			}
			if paneToggleIconButton(c, "board-drawer-archive", archTip, archTip, iconArchive, task.Archived).Clicked() {
				next := !task.Archived
				bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{Archived: &next})
				if err != nil {
					a.setBanner(err.Error(), "")
				} else {
					a.applyBoardFile(bf, c)
					a.syncBoardDrafts(updated)
				}
			}
			if paneIconButton(c, "board-drawer-delete", tr("删除任务"), iconTrash2).Clicked() {
				bf, err := boardDeleteTask(task.ID)
				if err != nil {
					a.setBanner(err.Error(), "")
				} else {
					a.board.selectedID = ""
					a.board.editTaskID = ""
					a.applyBoardFile(bf, c)
				}
				return
			}
			if closeIconButton(c, "board-drawer-close", tr("关闭详情")).Clicked() {
				a.closeBoardDrawer()
				return
			}
		})

		ui.Scroll(c.Key("board-drawer-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(16).Gap(14).Children(func() {
				// Title input
				titleEl := ui.TextInput(c.Key("board-drawer-title"), &a.board.titleDraft).
					Placeholder(tr("输入任务标题")).
					Bind(&a.board.titleInput).
					FontSize(16).Bold().FillWidth()
				if titleEl.Submitted() || (!a.board.titleInput.Focused(c) && strings.TrimSpace(a.board.titleDraft) != "" && strings.TrimSpace(a.board.titleDraft) != task.Title) {
					a.commitBoardDrafts()
				}

				// Metadata key-value rows (Yuque style)
				ui.Column(c.Key("board-drawer-meta")).FillWidth().Gap(10).Children(func() {
					// Status row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Row(c).Width(88).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
							ui.Icon(c, iconKanban).Size(14, 14).TextColor(t.TextMuted)
							ui.Text(c, tr("状态")).FontSize(12).TextColor(t.TextMuted)
						})
						ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(6).Children(func() {
							for _, st := range boardStatuses {
								target := st
								active := task.Status == target
								col := boardStatusColor(c, target)
								btn := ui.ButtonBase(c.Key("board-status-opt-"+string(target))).
									Label(tr(boardStatusLabel(target))).
									Padding(2, 8).Radius(4).Cursor(ui.CursorPointer).Transition(colorMotion())
								if active {
									btn.Background(col.Alpha(0.18)).Border(1, col.Alpha(0.6))
								} else {
									btn.Border(1, t.Border)
								}
								btn.Children(func() {
									lbl := ui.Text(c, tr(boardStatusLabel(target))).FontSize(12).SingleLine()
									if active {
										lbl.TextColor(col).FontWeight(600)
									} else {
										lbl.TextColor(t.TextMuted)
									}
								})
								if btn.Clicked() && !active {
									bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{Status: &target})
									if err != nil {
										a.setBanner(err.Error(), "")
									} else {
										a.applyBoardFile(bf, c)
										a.syncBoardDrafts(updated)
									}
								}
							}
						})
					})

					// Repos row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Row(c).Width(88).Gap(6).AlignItems(ui.Center).Shrink(0).Padding(3, 0, 0, 0).Children(func() {
							ui.Icon(c, iconTag).Size(14, 14).TextColor(t.TextMuted)
							ui.Text(c, tr("关联项目")).FontSize(12).TextColor(t.TextMuted)
						})
						ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
							repos := boardTaskAllRepos(task)
							removeRepo := ""
							for _, rp := range repos {
								repoPath := rp
								chipBtn := ui.ButtonBase(c.Key("board-drawer-repo-"+repoPath)).
									Label(repoTitle(repoPath)).
									Tooltip(repoPath).
									Padding(2, 7).Radius(4).
									Background(boardRepoColor(c, repoPath).Alpha(0.14)).
									Border(1, boardRepoColor(c, repoPath).Alpha(0.45)).
									Cursor(ui.CursorPointer)
								chipBtn.Children(func() {
									ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
										ui.Text(c, repoTitle(repoPath)).FontSize(12).TextColor(boardRepoColor(c, repoPath)).SingleLine()
										ui.Icon(c, iconClose).Size(10, 10).TextColor(boardRepoColor(c, repoPath))
									})
								})
								if chipBtn.Clicked() {
									removeRepo = repoPath
								}
							}
							if removeRepo != "" {
								next := make([]string, 0, len(task.Repos))
								for _, r := range task.Repos {
									if normalizeBoardRepo(r) != removeRepo {
										next = append(next, r)
									}
								}
								bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{Repos: &next})
								if err != nil {
									a.setBanner(err.Error(), "")
								} else {
									a.applyBoardFile(bf, c)
									a.syncBoardDrafts(updated)
								}
							}

							addRepoBtn := ui.Button(c.Key("board-drawer-add-repo"), tr("+ 项目")).Padding(2, 8).FontSize(12)
							addRepoBtn.Menu(func(m *ui.Menu) {
								known := a.boardKnownRepos()
								if len(known) == 0 {
									m.Item(tr("暂无已打开仓库")).Disabled(true)
									return
								}
								for _, rp := range known {
									repoPath := rp
									already := slices.Contains(repos, repoPath)
									if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Checked(already).Disabled(already).Chosen() {
										bf, updated, err := boardUpdateTask(task.ID, boardTaskPatch{AddRepos: []string{repoPath}})
										if err != nil {
											a.setBanner(err.Error(), "")
										} else {
											a.applyBoardFile(bf, c)
											a.syncBoardDrafts(updated)
										}
									}
								}
							})
						})
					})

					// Commits row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Row(c).Width(88).Gap(6).AlignItems(ui.Center).Shrink(0).Padding(3, 0, 0, 0).Children(func() {
							ui.Icon(c, iconGitCommit).Size(14, 14).TextColor(t.TextMuted)
							ui.Text(c, tr("关联提交")).FontSize(12).TextColor(t.TextMuted)
						})
						ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
							for _, link := range task.Commits {
								a.boardCommitChipRow(c, task, link)
							}
							if ui.Button(c.Key("board-drawer-link-commit"), tr("+ 关联提交")).Padding(2, 8).FontSize(12).Clicked() {
								a.openLinkCommitModal()
							}
						})
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border)

				// Description
				ui.Column(c.Key("board-drawer-desc-sec")).FillWidth().Gap(6).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("任务描述")).FontSize(12).Bold().TextColor(t.TextMuted).Grow(1)
						if a.board.descDraft != task.Description {
							if ui.PrimaryButton(c.Key("board-drawer-save-desc"), tr("保存描述")).Padding(2, 8).FontSize(11).Clicked() {
								a.commitBoardDrafts()
							}
						}
					})
					ui.TextArea(c.Key("board-drawer-desc"), &a.board.descDraft).
						Placeholder(tr("补充任务背景、目标或验收标准…")).
						Bind(&a.board.descInput).
						Height(96).
						FillWidth()
					if !a.board.descInput.Focused(c) && a.board.descDraft != task.Description {
						a.commitBoardDrafts()
					}
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border)

				// Notes timeline
				ui.Column(c.Key("board-drawer-notes-sec")).FillWidth().Gap(8).Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconMessage).Size(14, 14).TextColor(t.TextMuted)
						ui.Text(c, tr("追加备注")).FontSize(12).Bold().TextColor(t.TextMuted)
					})
					for _, n := range task.Notes {
						who := n.Author
						if strings.EqualFold(who, "human") || who == "" {
							who = tr("人工")
						}
						if n.Model != "" {
							who += " · " + n.Model
						}
						ui.Column(c.Key("board-note-"+n.ID)).FillWidth().Padding(8, 10).Gap(4).
							Radius(6).Background(t.Surface).Border(1, t.Border.Alpha(0.7)).Children(func() {
							ui.Text(c, n.Text).FontSize(12).TextColor(t.Text)
							ui.Text(c, who+" · "+n.CreatedAt.Local().Format("01-02 15:04")).FontSize(11).TextColor(t.TextMuted)
						})
					}
					ui.TextArea(c.Key("board-drawer-note-input"), &a.board.noteDraft).
						Placeholder(tr("记录进展或给 Agent 留备注…")).
						Bind(&a.board.noteInput).
						Height(58).
						FillWidth()
					ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
						canSend := strings.TrimSpace(a.board.noteDraft) != ""
						if ui.PrimaryButton(c.Key("board-drawer-note-add"), tr("添加备注")).
							Padding(3, 10).FontSize(12).Disabled(!canSend).Clicked() {
							bf, updated, err := boardAddNote(task.ID, a.board.noteDraft, "human", "")
							if err != nil {
								a.setBanner(err.Error(), "")
							} else {
								a.board.noteDraft = ""
								a.applyBoardFile(bf, c)
								a.syncBoardDrafts(updated)
							}
						}
					})
				})
			})
		})
	})
}

func (a *app) boardCommitChipRow(c *ui.Context, task boardTask, link boardCommitLink) {
	t := c.Theme()
	short := link.Hash
	if len(short) > 7 {
		short = short[:7]
	}
	key := boardCommitCacheKey(link.Repo, link.Hash)
	valid, checked := a.board.commitValid[key]
	stale := checked && !valid

	bg := t.Surface
	border := t.Border
	if stale {
		bg = t.Danger.Alpha(0.08)
		border = t.Danger.Alpha(0.35)
	}
	ui.Row(c.Key("board-commit-row-"+link.Repo+"-"+link.Hash)).
		FillWidth().Padding(6, 8).Gap(6).AlignItems(ui.Center).
		Radius(6).Background(bg).Border(1, border).Children(func() {
		jumpBtn := ui.ButtonBase(c.Key("board-commit-open-"+link.Repo+"-"+link.Hash)).
			Label(repoTitle(link.Repo) + " " + short).
			Tooltip(link.Repo + " @ " + link.Hash).
			Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer)
		jumpBtn.Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, repoTitle(link.Repo)).FontSize(11).Bold().TextColor(boardRepoColor(c, link.Repo)).SingleLine().Shrink(0)
					ui.Text(c, short).Font("monospace").FontSize(11).TextColor(t.TextMuted).SingleLine().Shrink(0)
					if link.Agent != "" {
						chipSoft(c, "board-commit-agent-"+link.Hash, link.Agent, t.Accent, false)
					}
					if stale {
						chipSoft(c, "board-commit-stale-"+link.Hash, tr("提交已失效"), t.Danger, false)
					}
				})
				if link.Subject != "" {
					sub := ui.Text(c, link.Subject).FontSize(12).SingleLine().MinWidth(0)
					if stale {
						sub.TextColor(t.TextMuted)
					} else {
						sub.TextColor(t.Text)
					}
				}
			})
		})
		if jumpBtn.Clicked() {
			a.openBoardCommit(link)
		}
		if paneIconButton(c, "board-commit-unlink-"+link.Repo+"-"+link.Hash, tr("移除关联"), iconClose).Clicked() {
			bf, updated, err := boardUnlinkCommit(task.ID, link.Repo, link.Hash)
			if err != nil {
				a.setBanner(err.Error(), "")
			} else {
				a.applyBoardFile(bf, c)
				a.syncBoardDrafts(updated)
			}
		}
	})
}

func (a *app) boardModals(c *ui.Context) {
	a.newBoardTaskModal(c)
	a.linkBoardCommitModal(c)
	a.boardSnippetModal(c)
}

func (a *app) newBoardTaskModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-new-task-modal"), &a.board.showNewModal, tr("新建看板任务"), 480, false, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, tr("标题")).FontSize(12).TextColor(t.TextMuted)
			titleInput := ui.TextInput(c.Key("board-new-title"), &a.board.newTitle).
				Placeholder(tr("一句话描述需要完成的任务")).
				FillWidth().AutoFocus()

			ui.Text(c, tr("状态")).FontSize(12).TextColor(t.TextMuted)
			ui.Row(c).Wrap().Gap(6).Children(func() {
				for _, st := range boardStatuses {
					target := st
					active := a.board.newStatus == target
					btn := ui.ButtonBase(c.Key("board-new-st-"+string(target))).
						Label(tr(boardStatusLabel(target))).Padding(3, 10).Radius(4).Cursor(ui.CursorPointer)
					col := boardStatusColor(c, target)
					if active {
						btn.Background(col.Alpha(0.18)).Border(1, col.Alpha(0.6))
					} else {
						btn.Border(1, t.Border)
					}
					btn.Children(func() {
						lbl := ui.Text(c, tr(boardStatusLabel(target))).FontSize(12)
						if active {
							lbl.TextColor(col).FontWeight(600)
						} else {
							lbl.TextColor(t.TextMuted)
						}
					})
					if btn.Clicked() {
						a.board.newStatus = target
					}
				}
			})

			if known := a.boardKnownRepos(); len(known) > 0 {
				ui.Text(c, tr("关联项目（可选）")).FontSize(12).TextColor(t.TextMuted)
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for _, rp := range known {
						repoPath := rp
						selected := slices.Contains(a.board.newRepos, repoPath)
						col := boardRepoColor(c, repoPath)
						btn := ui.ButtonBase(c.Key("board-new-repo-"+repoPath)).
							Label(repoTitle(repoPath)).Padding(2, 8).Radius(4).Cursor(ui.CursorPointer)
						if selected {
							btn.Background(col.Alpha(0.18)).Border(1, col.Alpha(0.55))
						} else {
							btn.Border(1, t.Border)
						}
						btn.Children(func() {
							lbl := ui.Text(c, repoTitle(repoPath)).FontSize(12).SingleLine()
							if selected {
								lbl.TextColor(col).FontWeight(600)
							} else {
								lbl.TextColor(t.TextMuted)
							}
						})
						if btn.Clicked() {
							if selected {
								a.board.newRepos = slices.DeleteFunc(a.board.newRepos, func(s string) bool { return s == repoPath })
							} else {
								a.board.newRepos = append(a.board.newRepos, repoPath)
							}
						}
					}
				})
			}

			ui.Text(c, tr("任务描述（可选）")).FontSize(12).TextColor(t.TextMuted)
			ui.TextArea(c.Key("board-new-desc"), &a.board.newDesc).
				Placeholder(tr("补充任务背景、目标或验收标准…")).
				Height(80).FillWidth()

			if a.board.newErr != "" {
				ui.Text(c, trMessage(a.board.newErr)).FontSize(12).TextColor(t.Danger)
			}
			if titleInput.Submitted() {
				a.submitNewBoardTask()
			}
		})
	}, func() {
		if ui.Button(c.Key("board-new-cancel"), tr("取消")).Clicked() {
			a.board.showNewModal = false
		}
		if ui.PrimaryButton(c.Key("board-new-submit"), tr("创建")).Disabled(strings.TrimSpace(a.board.newTitle) == "").Clicked() {
			a.submitNewBoardTask()
		}
	})
}

func (a *app) submitNewBoardTask() {
	bf, created, err := boardAddTask(a.board.newTitle, a.board.newDesc, a.board.newStatus, a.board.newRepos)
	if err != nil {
		a.board.newErr = err.Error()
		return
	}
	a.board.showNewModal = false
	a.applyBoardFile(bf, nil)
	a.selectBoardTask(created.ID)
}

func (a *app) linkBoardCommitModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-link-commit-modal"), &a.board.showLinkModal, tr("关联提交到任务"), 520, a.board.linkBusy, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			known := a.boardKnownRepos()
			ui.Text(c, tr("选择仓库")).FontSize(12).TextColor(t.TextMuted)
			if len(known) > 0 {
				repoBtn := ui.Button(c.Key("board-link-repo-btn"), func() string {
					if a.board.linkRepo == "" {
						return tr("选择仓库")
					}
					return repoTitle(a.board.linkRepo) + " (" + homePath(a.board.linkRepo) + ")"
				}()).FillWidth()
				repoBtn.Menu(func(m *ui.Menu) {
					for _, rp := range known {
						repoPath := rp
						if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Checked(a.board.linkRepo == repoPath).Chosen() {
							a.board.linkRepo = repoPath
						}
					}
				})
			} else {
				ui.Text(c, tr("请先打开至少一个 Git 仓库")).FontSize(12).TextColor(t.TextMuted)
			}

			ui.Text(c, tr("输入提交哈希或引用")).FontSize(12).TextColor(t.TextMuted)
			refEl := ui.TextInput(c.Key("board-link-ref"), &a.board.linkRef).
				Placeholder(tr("例如 HEAD、main 或提交哈希前缀")).
				Disabled(a.board.linkBusy).FillWidth().AutoFocus()

			// Quick pick from already-loaded commits in that tab
			for _, tab := range a.tabs {
				if normalizeBoardRepo(tab.path) == normalizeBoardRepo(a.board.linkRepo) && len(tab.commits) > 0 {
					ui.Text(c, tr("或从近期提交选择")).FontSize(12).TextColor(t.TextMuted)
					ui.Column(c).FillWidth().Gap(4).Children(func() {
						shown := 0
						for _, cm := range tab.commits {
							if cm.hash == worktreeHash || cm.hash == zeroHash {
								continue
							}
							hexStr := hashHex(cm.hash)
							short := hexStr[:7]
							row := ui.ButtonBase(c.Key("board-link-pick-"+hexStr)).
								Label(short + " " + cm.subject).
								FillWidth().Padding(6, 8).Radius(4).Justify(ui.Start).Cursor(ui.CursorPointer)
							if row.Hovered() {
								row.Background(t.SurfaceHover)
							}
							row.Children(func() {
								ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Text(c, short).Font("monospace").FontSize(11).TextColor(t.Accent).Shrink(0)
									ui.Text(c, cm.subject).FontSize(12).SingleLine().Grow(1).MinWidth(0)
								})
							})
							if row.Clicked() {
								a.board.linkRef = hexStr
							}
							shown++
							if shown >= 6 {
								break
							}
						}
					})
					break
				}
			}

			if a.board.linkErr != "" {
				ui.Text(c, trMessage(a.board.linkErr)).FontSize(12).TextColor(t.Danger)
			}
			if refEl.Submitted() && !a.board.linkBusy {
				a.submitBoardLinkCommit()
			}
		})
	}, func() {
		if ui.Button(c.Key("board-link-cancel"), tr("取消")).Disabled(a.board.linkBusy).Clicked() {
			a.board.showLinkModal = false
		}
		canSubmit := !a.board.linkBusy && a.board.linkRepo != "" && strings.TrimSpace(a.board.linkRef) != ""
		if ui.PrimaryButton(c.Key("board-link-submit"), tr("关联")).Disabled(!canSubmit).Clicked() {
			a.submitBoardLinkCommit()
		}
	})
}

func (a *app) submitBoardLinkCommit() {
	taskID := a.board.selectedID
	repo := a.board.linkRepo
	ref := strings.TrimSpace(a.board.linkRef)
	if taskID == "" || repo == "" || ref == "" {
		return
	}
	a.board.linkBusy = true
	a.board.linkErr = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		link, err := readBoardCommitSnapshot(ctx, repo, ref, "human")
		if err != nil {
			a.post(func() {
				a.board.linkBusy = false
				a.board.linkErr = err.Error()
			})
			return
		}
		bf, updated, err := boardLinkCommit(taskID, link)
		a.post(func() {
			a.board.linkBusy = false
			if err != nil {
				a.board.linkErr = err.Error()
				return
			}
			if a.board.commitValid == nil {
				a.board.commitValid = map[string]bool{}
			}
			a.board.commitValid[boardCommitCacheKey(link.Repo, link.Hash)] = true
			a.board.showLinkModal = false
			a.applyBoardFile(bf, nil)
			if a.board.selectedID == updated.ID {
				a.syncBoardDrafts(updated)
			}
		})
	}()
}

func (a *app) boardSnippetModal(c *ui.Context) {
	snippet := boardRuleSnippet()
	appModal(c.Key("board-snippet-modal"), &a.board.showSnippet, tr("Agent 看板协作规则"), 560, false, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.TextArea(c.Key("board-snippet-text"), &snippet).Font("monospace").FontSize(12).ReadOnly(true).Height(210).FillWidth()
		})
	}, func() {
		copyTextButton(c, "board-snippet-copy", "复制规则", snippet)
		if ui.PrimaryButton(c.Key("board-snippet-close"), tr("完成")).Clicked() {
			a.board.showSnippet = false
		}
	})
}

func (a *app) linkCommitToBoardTask(tab *repoTab, cm commit, taskID string) {
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	prov := tab.provenance.get(cm.prov)
	link := boardCommitLink{
		Repo:    canonicalRepoDir(tab.path),
		Hash:    hashHex(cm.hash),
		Subject: cm.subject,
		Branch:  tab.branch,
		Agent:   prov.agent,
		Model:   prov.model,
		AddedBy: "human",
		AddedAt: time.Now().UTC(),
	}
	bf, _, err := boardLinkCommit(taskID, link)
	if err != nil {
		a.setBanner(err.Error(), "")
		return
	}
	if a.board.commitValid == nil {
		a.board.commitValid = map[string]bool{}
	}
	a.board.commitValid[boardCommitCacheKey(link.Repo, link.Hash)] = true
	a.applyBoardFile(bf, nil)
	a.pendingToast = fmt.Sprintf("已关联到 %s", taskID)
}

func (a *app) createBoardTaskWithCommit(tab *repoTab, cm commit) {
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	title := strings.TrimSpace(cm.subject)
	if title == "" {
		title = hashHex(cm.hash)[:7]
	}
	bf, created, err := boardAddTask(title, "", boardDoing, []string{tab.path})
	if err != nil {
		a.setBanner(err.Error(), "")
		return
	}
	a.applyBoardFile(bf, nil)
	a.linkCommitToBoardTask(tab, cm, created.ID)
}
