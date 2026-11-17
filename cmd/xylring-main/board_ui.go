package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type boardState struct {
	loaded          bool
	file            boardFile
	loadErr         string
	lastMod         time.Time
	lastSize        int64
	polling         bool
	repoFilter      string
	search          string
	searchInput     ui.Handle
	showArchived    bool
	selectedID      string
	editTaskID      string
	titleDraft      string
	titleInput      ui.Handle
	descDraft       string
	descInput       ui.Handle
	noteDraft       string
	noteInput       ui.Handle
	noteDrafts      map[string]string
	drawerStatusIdx int
	deleteTaskID    string
	writeGen        int
	showNewModal    bool
	newTitle        string
	newDesc         string
	newStatus       boardStatus
	newStatusIdx    int
	newRepos        []string
	newErr          string
	showLinkModal   bool
	linkRepo        string
	linkRef         string
	linkErr         string
	linkBusy        bool
	showSnippet     bool
	commitValid     map[string]bool
	checkingLinks   map[string]bool
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
	a.board.writeGen++
	prev := a.board.file
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
	prevTask, hasPrev := boardTaskByID(prev.Tasks, t.ID)
	// A snapshot can arrive from another process while the drawer is open. Keep
	// anything the user typed but has not saved yet; refresh the rest.
	titleDirty := a.board.titleDraft != "" && (!hasPrev || a.board.titleDraft != prevTask.Title)
	if !titleDirty && (c == nil || !a.board.titleInput.Focused(c)) {
		a.board.titleDraft = t.Title
	}
	descDirty := !hasPrev || a.board.descDraft != prevTask.Description
	if !descDirty && (c == nil || !a.board.descInput.Focused(c)) {
		a.board.descDraft = t.Description
	}
	a.board.drawerStatusIdx = boardStatusIndex(t.Status)
}

func boardTaskByID(tasks []boardTask, id string) (boardTask, bool) {
	if idx := findBoardTaskIndex(tasks, id); idx >= 0 {
		return tasks[idx], true
	}
	return boardTask{}, false
}

func boardStatusIndex(s boardStatus) int {
	if i := slices.Index(boardStatuses, s); i >= 0 {
		return i
	}
	return 0
}

func (a *app) syncBoardDrafts(t boardTask) {
	a.stashNoteDraft()
	a.board.editTaskID = t.ID
	a.board.titleDraft = t.Title
	a.board.descDraft = t.Description
	a.board.drawerStatusIdx = boardStatusIndex(t.Status)
	a.board.noteDraft = ""
	if draft, ok := a.board.noteDrafts[t.ID]; ok {
		a.board.noteDraft = draft
	}
}

// stashNoteDraft keeps an unsent note with its task, so closing the drawer or
// committing another field does not throw the text away.
func (a *app) stashNoteDraft() {
	id := a.board.editTaskID
	if id == "" {
		return
	}
	if strings.TrimSpace(a.board.noteDraft) == "" {
		delete(a.board.noteDrafts, id)
		return
	}
	if a.board.noteDrafts == nil {
		a.board.noteDrafts = map[string]string{}
	}
	a.board.noteDrafts[id] = a.board.noteDraft
}

func (a *app) forgetBoardTaskDraft(id string) {
	delete(a.board.noteDrafts, id)
	if a.board.editTaskID == id {
		a.board.noteDraft = ""
	}
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
	a.stashNoteDraft()
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
	switch {
	case trimmedTitle == "":
		// An emptied title cannot be saved; say so instead of dropping it silently.
		a.setBanner(tr("任务标题不能为空"), "")
		a.board.titleDraft = cur.Title
	case trimmedTitle != cur.Title:
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

// leaveBoard saves the open drawer before the board page goes away, so text
// typed there is not left only in memory.
func (a *app) leaveBoard() {
	if a.showBoard {
		a.commitBoardDrafts()
	}
	a.showBoard = false
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
	gen := a.board.writeGen
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
			// A local change landed while this read was in flight; applying the
			// older snapshot would show it reverting.
			if gen != a.board.writeGen {
				return
			}
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
			a.leaveBoard()
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
	a.board.newStatusIdx = boardStatusIndex(status)
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
		Padding(9, 12).
		Gap(9).
		Radius(8).
		Justify(ui.Start).
		AlignItems(ui.Center).
		Cursor(ui.CursorPointer).
		Margin(0, 0, 6, 0).
		Transition(colorMotion())
	switch {
	case active:
		row.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.4))
	case row.Pressed():
		row.Background(t.SurfacePressed).Border(1, t.Border.Alpha(0.7))
	case row.Hovered():
		row.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
	default:
		row.Background(t.Surface.Alpha(0.7)).Border(1, t.Border.Alpha(0.55))
	}
	row.Children(func() {
		fg := t.TextMuted
		if active {
			fg = t.Accent
		}
		ui.Icon(c, iconKanban).Size(16, 16).TextColor(fg).Shrink(0)
		lbl := ui.Text(c, tr("看板")).FontSize(13.5).Grow(1).MinWidth(0).SingleLine()
		if active {
			lbl.FontWeight(600).TextColor(t.Accent)
		} else {
			lbl.FontWeight(500).TextColor(t.Text)
		}
		if count := a.boardActiveCount(); count > 0 {
			badgeBg := t.SurfaceHover
			badgeFg := t.TextMuted
			if active {
				badgeBg = t.Accent.Alpha(0.18)
				badgeFg = t.Accent
			}
			ui.Row(c).Padding(1, 7).Radius(10).Background(badgeBg).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).FontWeight(600).TextColor(badgeFg).SingleLine()
			})
		}
	})
	if row.Clicked() {
		a.openBoard()
	}
}

type boardCardDrag struct {
	taskID string
	status boardStatus
}

type boardCardMatch struct{ id string }

type boardSegIndicatorMatch struct{ key string }

func boardPageMotion() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: motionPane,
		Ease:     ui.EaseOut,
		Enter:    &ui.Motion{Opacity: 0, Y: 6},
		Exit:     &ui.Motion{Opacity: 0},
	}
}

func boardDrawerMotion() ui.ElementTransition {
	return ui.ElementTransition{
		Duration: motionPane,
		Ease:     ui.EaseOut,
		Enter:    &ui.Motion{X: 28, Opacity: 0},
		Exit:     &ui.Motion{X: 28, Opacity: 0},
		Colors:   true,
	}
}

func boardCardMotion(id string) ui.ElementTransition {
	return ui.ElementTransition{
		Duration: motionPane,
		Ease:     ui.EaseOut,
		Position: true,
		Colors:   true,
		Match:    boardCardMatch{id: id},
		Enter:    &ui.Motion{Opacity: 0, Scale: 0.96},
		Exit:     &ui.Motion{Opacity: 0, Scale: 0.96},
	}
}

func boardStatusDot(c *ui.Context, s boardStatus, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Radius(size / 2).Background(boardStatusColor(c, s)).Shrink(0)
}

func boardRepoDot(c *ui.Context, repo string, size float32) ui.Element {
	return ui.Box(c).Size(size, size).Radius(size / 2).Background(boardRepoColor(c, repo)).Shrink(0)
}

func boardAgentTag(c *ui.Context, key, agent string) ui.Element {
	t := c.Theme()
	tag := ui.Row(c.Key(key)).Padding(2, 6).Gap(4).Radius(4).AlignItems(ui.Center).Shrink(0).
		Background(t.Accent.Alpha(0.10))
	tag.Children(func() {
		ui.Icon(c, iconBot).Size(12, 12).TextColor(t.Accent).Shrink(0)
		ui.Text(c, agent).FontSize(12).TextColor(t.Accent).SingleLine()
	})
	return tag
}

func boardStatusSegmented(c *ui.Context, key string, selected *int) ui.SegmentedParts {
	t := c.Theme()
	seg := ui.SegmentedBase(c.Key(key), selected, len(boardStatuses))
	seg.Track.FillWidth().AlignItems(ui.Stretch).Padding(2).Gap(2).Radius(t.Radius).
		Background(t.Surface).Border(1, t.Border.Alpha(0.7)).Label(tr("状态"))
	seg.Track.Children(func() {
		for i, st := range boardStatuses {
			active := i == *selected
			s := seg.Segment(i).Label(tr(boardStatusLabel(st))).
				Grow(1).MinWidth(0).Center().Padding(5, 4).Gap(5).
				Radius(max(t.Radius-2, 0)).Cursor(ui.CursorPointer).Transition(colorMotion())
			if !active && s.Hovered() {
				s.Background(t.SurfaceHover)
			}
			s.Children(func() {
				stCol := boardStatusColor(c, st)
				if active {
					pillBg := stCol.Alpha(0.16)
					pillBorder := stCol.Alpha(0.65)
					if st == boardTodo {
						pillBg = t.Background
						if t.Dark {
							pillBg = t.SurfacePressed
						}
						pillBorder = t.TextMuted.Alpha(0.55)
					}
					ui.Box(c.Key(key+"-active-pill")).Absolute().Fill().
						Radius(max(t.Radius-2, 0)).Background(pillBg).
						Border(1.5, pillBorder).
						Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.06)).
						IgnoreInput(true).
						ZIndex(0).
						Transition(ui.ElementTransition{
							Duration: motionPane,
							Ease:     ui.EaseOut,
							Match:    boardSegIndicatorMatch{key: key},
						})
				}
				ui.Row(c).AlignItems(ui.Center).Gap(5).ZIndex(1).Children(func() {
					boardStatusDot(c, st, 7)
					lbl := ui.Text(c, tr(boardStatusLabel(st))).FontSize(12.5).SingleLine()
					if active {
						lbl.FontWeight(600)
						if st == boardTodo {
							lbl.TextColor(t.Text)
						} else {
							lbl.TextColor(stCol)
						}
					} else {
						lbl.TextColor(t.TextMuted)
					}
				})
			})
		}
	})
	return seg
}

func (a *app) boardFilteredByStatus() ([][]boardTask, int) {
	cols := make([][]boardTask, len(boardStatuses))
	total := 0
	for _, task := range a.board.file.Tasks {
		if !boardMatchesFilter(task, a.board.repoFilter, a.board.search, a.board.showArchived) {
			continue
		}
		cols[boardStatusIndex(task.Status)] = append(cols[boardStatusIndex(task.Status)], task)
		total++
	}
	return cols, total
}

func (a *app) boardScreen(c *ui.Context) {
	a.ensureBoardLoaded()
	t := c.Theme()
	cols, total := a.boardFilteredByStatus()
	hasFilter := a.board.repoFilter != "" || strings.TrimSpace(a.board.search) != ""
	canvasBg := t.Surface.Alpha(0.45)
	if t.Dark {
		canvasBg = ui.Hex("#131316")
	}
	body := func() {
		ui.Column(c.Key("board-workspace")).Grow(1).MinWidth(0).MinHeight(0).
			Background(canvasBg).
			BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.65)).
			Transition(workspaceMainMotion()).Children(func() {
			if a.board.loadErr != "" {
				ui.Row(c.Key("board-error-banner")).FillWidth().Padding(6, 16).Background(t.Danger.Alpha(0.12)).Children(func() {
					ui.Text(c, trMessage(a.board.loadErr)).FontSize(12.5).TextColor(t.Danger)
				})
			}
			if hasFilter && total == 0 {
				ui.Row(c.Key("board-filter-empty")).FillWidth().Padding(6, 16).Gap(8).AlignItems(ui.Center).
					Background(t.Surface.Alpha(0.65)).BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.6)).Children(func() {
					ui.Icon(c, iconSearch).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, tr("没有匹配的任务")).FontSize(12.5).TextColor(t.TextMuted).Grow(1).MinWidth(0)
					if ui.Button(c.Key("board-clear-filter"), tr("清除筛选")).Padding(2, 8).FontSize(12).Clicked() {
						a.board.repoFilter = ""
						a.board.search = ""
					}
				})
			}
			ui.Box(c.Key("board-body")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.boardColumns(c, cols)
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

func (a *app) boardSearchField(c *ui.Context, rightW float32) {
	t := c.Theme()
	maxW := float32(320)
	if rightW < 620 {
		maxW = 200
	}
	root := ui.Row(c.Key("board-search-box")).Grow(1).MinWidth(130).MaxWidth(maxW).Height(30).
		Padding(0, 8).Gap(6).AlignItems(ui.Center).Radius(t.Radius).Background(t.Background).
		Transition(colorMotion())
	var input ui.Element
	root.Children(func() {
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
		input = ui.TextInputBase(c.Key("board-search"), &a.board.search).
			Label(tr("搜索看板任务")).
			Placeholder(tr("搜索任务…")).
			Bind(&a.board.searchInput).
			FontSize(13).
			Grow(1).MinWidth(0).FocusRing(false)
		if a.board.search != "" {
			if closeIconButton(c, "board-search-clear", tr("清空搜索输入")).Clicked() {
				a.board.search = ""
				input.Focus()
			}
		} else if !input.Focused() && rightW >= 540 {
			shortcut := "Ctrl+F"
			if runtime.GOOS == "darwin" {
				shortcut = "⌘F"
			}
			kbdHint(c, "board-search-shortcut", shortcut)
		}
	})
	border := t.Border
	if root.Hovered() {
		border = t.TextMuted
	}
	if input.Focused() {
		border = t.Accent
		root.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.15))
	}
	root.Border(1, border)
}

func (a *app) boardChromeToolbar(c *ui.Context, padR, rightPadL, rightW float32) {
	t := c.Theme()
	ui.Row(c.Key("board-chrome-toolbar")).Fill().Padding(0, padR, 0, rightPadL).Gap(8).AlignItems(ui.Center).
		Transition(fadeEnter()).Children(func() {
		filterLabel := tr("全部项目")
		if a.board.repoFilter != "" {
			filterLabel = repoTitle(a.board.repoFilter)
		}
		filterBtn := ui.ButtonBase(c.Key("board-repo-filter")).Height(30).Padding(0, 9).Gap(6).
			Radius(t.Radius).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(filterLabel).MinWidth(0).MaxWidth(190).Shrink(1).Transition(colorMotion())
		switch {
		case filterBtn.Pressed():
			filterBtn.Background(t.SurfacePressed)
		case filterBtn.Hovered():
			filterBtn.Background(t.SurfaceHover)
		case a.board.repoFilter != "":
			filterBtn.Background(t.Accent.Alpha(0.12))
		}
		filterBtn.Children(func() {
			if a.board.repoFilter != "" {
				boardRepoDot(c, a.board.repoFilter, 7)
			} else {
				ui.Icon(c, iconFolderGit).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
			}
			lbl := ui.Text(c, filterLabel).FontSize(13).FontWeight(500).SingleLine().MinWidth(0)
			if a.board.repoFilter != "" {
				lbl.TextColor(t.Accent).FontWeight(600)
			} else {
				lbl.TextColor(t.Text)
			}
			ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
		})
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

		a.boardSearchField(c, rightW)

		ui.Box(c).Grow(1).MinWidth(4)

		archTip := tr("显示已归档")
		if a.board.showArchived {
			archTip = tr("隐藏已归档")
		}
		archBtn := ui.ButtonBase(c.Key("board-toggle-archived")).Label(tr("显示已归档")).Tooltip(archTip)
		styleActionIcon(c, archBtn, iconArchive, 30, 15, t.TextMuted, a.board.showArchived)
		if archBtn.Clicked() {
			a.board.showArchived = !a.board.showArchived
		}

		rulesBtn := ui.ButtonBase(c.Key("board-agent-snippet")).Height(30).Padding(0, 9).Gap(5).
			Radius(t.Radius).AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Label(tr("Agent 规则")).Tooltip(tr("Agent 看板协作规则")).Shrink(0).Transition(colorMotion())
		switch {
		case rulesBtn.Pressed():
			rulesBtn.Background(t.SurfacePressed)
		case rulesBtn.Hovered():
			rulesBtn.Background(t.SurfaceHover)
		}
		rulesBtn.Children(func() {
			ui.Icon(c, iconBot).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
			if rightW >= 640 {
				ui.Text(c, tr("Agent 规则")).FontSize(13).TextColor(t.TextMuted).SingleLine()
			}
		})
		if rulesBtn.Clicked() {
			a.board.showSnippet = true
		}

		newBtn := ui.PrimaryButton(c.Key("board-new-task"), "").Label(tr("新建任务")).
			Height(30).Padding(0, 12).Radius(t.Radius).Shrink(0)
		newBtn.Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconPlus).Size(14, 14).TextColor(t.AccentText).Shrink(0)
				ui.Text(c, tr("新建任务")).FontSize(13).FontWeight(500).TextColor(t.AccentText).SingleLine()
			})
		})
		if newBtn.Clicked() {
			a.openNewBoardTaskModal(boardTodo)
		}
	})
}

func (a *app) boardColumns(c *ui.Context, cols [][]boardTask) {
	t := c.Theme()
	laneBg := t.Surface
	if t.Dark {
		laneBg = t.Surface.Alpha(0.55)
	}
	availW, _ := c.Size()
	if a.showRepoSidebar() {
		availW -= a.repoSplit + 1
	}
	nCols := float32(len(boardStatuses))
	colW := max(float32(220), (availW-32-(nCols-1)*12)/nCols)
	ui.ScrollHorizontal(c.Key("board-columns")).Fill().MinWidth(0).MinHeight(0).
		AlignItems(ui.Stretch).Padding(14, 16, 16, 16).Gap(12).Children(func() {
		for idx, status := range boardStatuses {
			st := status
			tasks := cols[idx]
			col := ui.Column(c.Key("board-col-"+string(st))).
				Width(colW).FillHeight().MinHeight(0).Shrink(0).
				Radius(10).Background(laneBg).Border(1, t.Border.Alpha(0.45)).
				Padding(12, 10, 10, 10).Gap(10).
				Transition(ui.ElementTransition{
					Duration: motionColor,
					Ease:     ui.EaseOut,
					Colors:   true,
				})
			if dragged, over := ui.DragOver[boardCardDrag](col); over && dragged.status != st {
				col.Background(boardStatusColor(c, st).Alpha(0.10)).
					Border(1, boardStatusColor(c, st).Alpha(0.55))
			}
			col.Children(func() {
				ui.Row(c.Key("board-col-head-"+string(st))).FillWidth().Padding(2, 4).Gap(7).AlignItems(ui.Center).Shrink(0).Children(func() {
					boardStatusDot(c, st, 9)
					ui.Text(c, tr(boardStatusLabel(st))).FontSize(14.5).FontWeight(600).TextColor(t.Text).SingleLine()
					ui.Text(c, fmt.Sprintf("%d", len(tasks))).FontSize(13).TextColor(t.TextMuted).SingleLine()
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
				ui.Scroll(c.Key("board-col-scroll-" + string(st))).Grow(1).MinHeight(0).FillWidth().Children(func() {
					if len(tasks) == 0 {
						return
					}
					ui.Column(c).FillWidth().Gap(9).Padding(2, 1, 4, 1).Children(func() {
						for _, task := range tasks {
							a.boardCard(c, task)
						}
					})
				})
			})
			if dropped, ok := ui.Drop[boardCardDrag](col); ok && dropped.status != st {
				a.applyBoardTaskPatch(dropped.taskID, c, boardTaskPatch{Status: &st})
			}
		}
	})
}

func (a *app) boardCard(c *ui.Context, task boardTask) {
	t := c.Theme()
	selected := a.board.selectedID == task.ID
	cardBg := t.Background
	cardShadow := ui.RGBA(0, 0, 0, 0.04)
	if t.Dark {
		cardBg = t.Surface
		cardShadow = ui.RGBA(0, 0, 0, 0.18)
	}
	card := ui.ButtonBase(c.Key("board-card-"+task.ID)).
		Label(task.ID+" "+task.Title).
		FillWidth().
		Padding(12, 14).
		Radius(8).
		Background(cardBg).
		Cursor(ui.CursorPointer).
		Drag(boardCardDrag{taskID: task.ID, status: task.Status}).
		Transition(boardCardMotion(task.ID))

	if card.Dragging() {
		card.Opacity(0.4)
	}
	hovered := card.Hovered()
	switch {
	case selected:
		selBg := t.Accent.Alpha(0.06)
		if t.Dark {
			selBg = t.Accent.Alpha(0.14)
		}
		card.Background(selBg).Border(2, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.18))
	case hovered:
		card.Border(1, t.Accent.Alpha(0.55)).Shadow(0, 2, 6, 0, cardShadow)
	default:
		card.Border(1, t.Border.Alpha(0.75)).Shadow(0, 1, 2, 0, cardShadow)
	}

	var moveStatus boardStatus
	hasMove := false
	doArchive := false
	doUnarchive := false
	doDelete := false

	card.ContextMenu(func(m *ui.Menu) {
		for _, st := range boardStatuses {
			target := st
			if m.Item(tr("移至") + " " + tr(boardStatusLabel(target))).Checked(task.Status == target).Disabled(task.Status == target).Chosen() {
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
	repos := boardTaskAllRepos(task)
	prov := boardTaskProvenanceSummary(task)
	card.Children(func() {
		ui.Column(c).FillWidth().Gap(7).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, task.ID).Font("monospace").FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
				if a.board.repoFilter == "" && len(repos) > 0 {
					boardRepoDot(c, repos[0], 7)
					repoText := repoTitle(repos[0])
					if len(repos) > 1 {
						repoText += fmt.Sprintf(" +%d", len(repos)-1)
					}
					ui.Text(c, repoText).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0)
				}
				ui.Box(c).Grow(1)
				tip := tr("标记为已完成")
				if task.Status == boardDone {
					tip = tr("移回待办")
				}
				chk := ui.ButtonBase(c.Key("board-card-check-"+task.ID)).
					Label(tip).Tooltip(tip).
					Size(20, 20).Radius(10).Center().Shrink(0).
					Cursor(ui.CursorPointer).Transition(colorMotion())
				chkIcon := iconCircle
				chkColor := t.TextMuted.Alpha(0.32)
				switch {
				case task.Status == boardDone:
					chkIcon = iconCheckCircle2
					chkColor = t.Success
					if chk.Hovered() {
						chk.Background(t.Success.Alpha(0.12))
					}
				case chk.Hovered():
					chkIcon = iconCheckCircle2
					chkColor = t.Accent
					chk.Background(t.Accent.Alpha(0.12))
				case hovered || selected:
					chkColor = t.TextMuted.Alpha(0.75)
				}
				chk.Children(func() {
					ui.Icon(c, chkIcon).Size(15, 15).TextColor(chkColor)
				})
				if chk.Clicked() {
					checkClicked = true
				}
			})

			titleEl := ui.Text(c, task.Title).FontSize(14.5).FontWeight(500).LineHeight(1.35).MaxLines(2).FillWidth().MinWidth(0)
			if task.Status == boardDone || task.Archived {
				titleEl.TextColor(t.TextMuted)
			} else {
				titleEl.TextColor(t.Text)
			}

			if len(task.Commits) > 0 || len(task.Notes) > 0 || prov != "" || task.Archived {
				ui.Row(c).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
					if len(task.Commits) > 0 {
						ui.Row(c).Gap(3).AlignItems(ui.Center).Tooltip(fmt.Sprintf(tr("%d 提交"), len(task.Commits))).Children(func() {
							ui.Icon(c, iconGitCommit).Size(13, 13).TextColor(t.TextMuted)
							ui.Text(c, fmt.Sprintf("%d", len(task.Commits))).FontSize(12).TextColor(t.TextMuted).SingleLine()
						})
					}
					if len(task.Notes) > 0 {
						ui.Row(c).Gap(3).AlignItems(ui.Center).Tooltip(fmt.Sprintf(tr("%d 备注"), len(task.Notes))).Children(func() {
							ui.Icon(c, iconMessage).Size(13, 13).TextColor(t.TextMuted)
							ui.Text(c, fmt.Sprintf("%d", len(task.Notes))).FontSize(12).TextColor(t.TextMuted).SingleLine()
						})
					}
					if task.Archived {
						ui.Text(c, tr("已归档")).FontSize(12).TextColor(t.TextMuted).SingleLine()
					}
					ui.Box(c).Grow(1)
					if prov != "" {
						boardAgentTag(c, "board-card-agent-"+task.ID, prov)
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
		a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Status: &next})
		return
	}
	if hasMove {
		a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Status: &moveStatus})
		return
	}
	if doArchive || doUnarchive {
		v := doArchive
		a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Archived: &v})
		return
	}
	if doDelete {
		a.board.deleteTaskID = task.ID
		return
	}
	if card.Clicked() {
		a.selectBoardTask(task.ID)
	}
}

// applyBoardTaskPatch flushes the open drawer first: its typed title or
// description must not be replaced by the snapshot this change returns.
func (a *app) applyBoardTaskPatch(id string, c *ui.Context, patch boardTaskPatch) {
	a.commitBoardDrafts()
	bf, _, err := boardUpdateTask(id, patch)
	if err != nil {
		a.setBanner(err.Error(), "")
		return
	}
	a.applyBoardFile(bf, c)
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

	drawerShadow := ui.RGBA(0, 0, 0, 0.16)
	if t.Dark {
		drawerShadow = ui.RGBA(0, 0, 0, 0.45)
	}
	ui.Column(c.Key("board-detail-drawer")).
		Absolute().Top(12).Bottom(12).Right(12).Width(450).ZIndex(20).
		Radius(12).Background(t.Background).Border(1, t.Border).
		Shadow(0, 12, 32, 0, drawerShadow).
		Transition(boardDrawerMotion()).Children(func() {
		// Top drawer header
		ui.Row(c.Key("board-drawer-header")).FillWidth().Padding(10, 16).Gap(6).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.65)).Shrink(0).Children(func() {
			ui.Row(c.Key("board-drawer-id")).Padding(3, 8).Radius(5).Background(t.Surface).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, task.ID).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(t.TextMuted).SingleLine()
			})
			copyIconButton(c, "board-drawer-copy-id", tr("复制任务编号"), task.ID)
			ui.Box(c).Grow(1)
			archTip := tr("归档任务")
			if task.Archived {
				archTip = tr("取消归档")
			}
			if paneToggleIconButton(c, "board-drawer-archive", archTip, archTip, iconArchive, task.Archived).Clicked() {
				next := !task.Archived
				a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Archived: &next})
			}
			if paneIconButton(c, "board-drawer-delete", tr("删除任务"), iconTrash2).Clicked() {
				a.board.deleteTaskID = task.ID
			}
			if closeIconButton(c, "board-drawer-close", tr("关闭详情")).Clicked() {
				a.closeBoardDrawer()
				return
			}
		})

		ui.Scroll(c.Key("board-drawer-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c.Key("board-drawer-content-" + task.ID)).FillWidth().Padding(16).Gap(16).
				Transition(fadeEnter()).Children(func() {
				// Borderless document-style title input
				titleEl := ui.TextInputBase(c.Key("board-drawer-title"), &a.board.titleDraft).
					Label(tr("标题")).
					Placeholder(tr("输入任务标题")).
					Bind(&a.board.titleInput).
					FontSize(18).FontWeight(600).
					Padding(5, 7).Radius(6).FillWidth().FocusRing(false).
					Transition(colorMotion())
				switch {
				case a.board.titleInput.Focused(c):
					titleEl.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
				case titleEl.Hovered():
					titleEl.Background(t.Surface.Alpha(0.65))
				}
				if titleEl.Submitted() || (!a.board.titleInput.Focused(c) && strings.TrimSpace(a.board.titleDraft) != "" && strings.TrimSpace(a.board.titleDraft) != task.Title) {
					a.commitBoardDrafts()
				}

				// Compact metadata rows
				ui.Column(c.Key("board-drawer-meta")).FillWidth().Gap(10).Children(func() {
					// Status segmented control
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("状态")).Width(72).FontSize(13).TextColor(t.TextMuted).Shrink(0)
						ui.Box(c).Grow(1).MinWidth(0).Children(func() {
							seg := boardStatusSegmented(c, "board-drawer-status-seg", &a.board.drawerStatusIdx)
							if seg.Track.Changed() {
								target := boardStatuses[a.board.drawerStatusIdx]
								if target != task.Status {
									a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Status: &target})
								}
							}
						})
					})

					// Repos row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, tr("关联项目")).Width(72).FontSize(13).TextColor(t.TextMuted).Shrink(0).Padding(4, 0, 0, 0)
						ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
							repos := boardTaskAllRepos(task)
							removeRepo := ""
							for _, rp := range repos {
								repoPath := rp
								explicit := slices.ContainsFunc(task.Repos, func(r string) bool {
									return normalizeBoardRepo(r) == repoPath
								})
								filtered := normalizeBoardRepo(a.board.repoFilter) == repoPath
								pillBg := t.Surface.Alpha(0.7)
								pillBorder := t.Border.Alpha(0.7)
								if filtered {
									pillBg = t.Accent.Alpha(0.12)
									pillBorder = t.Accent.Alpha(0.45)
								}
								ui.Row(c.Key("board-drawer-repo-pill-"+repoPath)).Padding(3, 7).Gap(5).
									Radius(5).Background(pillBg).Border(1, pillBorder).AlignItems(ui.Center).Children(func() {
									filterBtn := ui.ButtonBase(c.Key("board-drawer-repo-" + repoPath)).
										Label(repoTitle(repoPath)).
										Tooltip(tr("按此项目筛选") + "\n" + repoPath).
										Cursor(ui.CursorPointer)
									filterBtn.Children(func() {
										ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
											boardRepoDot(c, repoPath, 7)
											lbl := ui.Text(c, repoTitle(repoPath)).FontSize(12.5).SingleLine()
											if filtered {
												lbl.TextColor(t.Accent).FontWeight(500)
											} else {
												lbl.TextColor(t.Text)
											}
										})
									})
									if filterBtn.Clicked() {
										if filtered {
											a.board.repoFilter = ""
										} else {
											a.board.repoFilter = repoPath
										}
									}
									if explicit {
										rmBtn := ui.ButtonBase(c.Key("board-drawer-repo-rm-"+repoPath)).
											Label(tr("从任务移除")).Tooltip(tr("从任务移除")).
											Size(15, 15).Radius(3).Center().Cursor(ui.CursorPointer)
										if rmBtn.Hovered() {
											rmBtn.Background(t.SurfaceHover)
										}
										rmBtn.Children(func() {
											ui.Icon(c, iconClose).Size(10, 10).TextColor(t.TextMuted)
										})
										if rmBtn.Clicked() {
											removeRepo = repoPath
										}
									}
								})
							}
							if removeRepo != "" {
								next := make([]string, 0, len(task.Repos))
								for _, r := range task.Repos {
									if normalizeBoardRepo(r) != removeRepo {
										next = append(next, r)
									}
								}
								a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{Repos: &next})
							}

							addRepoBtn := ui.Button(c.Key("board-drawer-add-repo"), tr("+ 项目")).Padding(3, 9).FontSize(12.5)
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
										a.applyBoardTaskPatch(task.ID, c, boardTaskPatch{AddRepos: []string{repoPath}})
									}
								}
							})
						})
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Description
				ui.Column(c.Key("board-drawer-desc-sec")).FillWidth().Gap(6).Children(func() {
					ui.Text(c, tr("任务描述")).FontSize(13).FontWeight(600).TextColor(t.TextMuted)
					descEl := ui.TextAreaBase(c.Key("board-drawer-desc"), &a.board.descDraft).
						Label(tr("任务描述")).
						Placeholder(tr("补充任务背景、目标或验收标准…")).
						Bind(&a.board.descInput).
						Height(96).FillWidth().Padding(9, 11).Radius(8).
						FontSize(13.5).LineHeight(1.4).FocusRing(false).Transition(colorMotion())
					switch {
					case a.board.descInput.Focused(c):
						descEl.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
					case descEl.Hovered():
						descEl.Background(t.Surface.Alpha(0.65)).Border(1, t.Border)
					default:
						descEl.Background(t.Surface.Alpha(0.45)).Border(1, t.Border.Alpha(0.6))
					}
					if !a.board.descInput.Focused(c) && a.board.descDraft != task.Description {
						a.commitBoardDrafts()
					}
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Linked commits (full-width section)
				ui.Column(c.Key("board-drawer-commits-sec")).FillWidth().Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconGitCommit).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, tr("关联提交")).FontSize(13).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Commits) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Commits))).FontSize(12.5).TextColor(t.TextMuted)
						}
						ui.Box(c).Grow(1)
						if paneIconButton(c, "board-drawer-link-commit", tr("关联提交到任务"), iconPlus).Clicked() {
							a.openLinkCommitModal()
						}
					})
					for _, link := range task.Commits {
						a.boardCommitChipRow(c, task, link)
					}
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Notes timeline
				ui.Column(c.Key("board-drawer-notes-sec")).FillWidth().Gap(8).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, iconMessage).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
						ui.Text(c, tr("备注")).FontSize(13).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Notes) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Notes))).FontSize(12.5).TextColor(t.TextMuted)
						}
					})
					for _, n := range task.Notes {
						isHuman := strings.EqualFold(n.Author, "human") || n.Author == ""
						who := n.Author
						if isHuman {
							who = tr("人工")
						}
						ui.Column(c.Key("board-note-"+n.ID)).FillWidth().Padding(9, 11).Gap(4).
							Radius(8).Background(t.Surface.Alpha(0.6)).Children(func() {
							ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
								if !isHuman {
									ui.Icon(c, iconBot).Size(13, 13).TextColor(t.Accent).Shrink(0)
									ui.Text(c, who).FontSize(12.5).FontWeight(600).TextColor(t.Accent).SingleLine()
								} else {
									ui.Text(c, who).FontSize(12.5).FontWeight(600).TextColor(t.Text).SingleLine()
								}
								if n.Model != "" {
									ui.Text(c, n.Model).FontSize(12).TextColor(t.TextMuted).SingleLine().MinWidth(0)
								}
								ui.Box(c).Grow(1)
								ui.Text(c, n.CreatedAt.Local().Format("01-02 15:04")).FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
							})
							ui.Text(c, n.Text).FontSize(13.5).LineHeight(1.4).TextColor(t.Text)
						})
					}

					submitNote := func() {
						if strings.TrimSpace(a.board.noteDraft) == "" {
							return
						}
						bf, _, err := boardAddNote(task.ID, a.board.noteDraft, "human", "")
						if err != nil {
							a.setBanner(err.Error(), "")
							return
						}
						a.forgetBoardTaskDraft(task.ID)
						a.applyBoardFile(bf, c)
					}
					composer := ui.Column(c.Key("board-drawer-note-box")).FillWidth().Padding(9, 11).Gap(6).
						Radius(8).Transition(colorMotion()).
						OnShortcut(commandModifier(), ui.KeyEnter, submitNote)
					composerFocused := a.board.noteInput.Focused(c)
					switch {
					case composerFocused:
						composer.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
					case composer.Hovered():
						composer.Background(t.Surface.Alpha(0.65)).Border(1, t.Border)
					default:
						composer.Background(t.Surface.Alpha(0.45)).Border(1, t.Border.Alpha(0.6))
					}
					composer.Children(func() {
						ui.TextAreaBase(c.Key("board-drawer-note-input"), &a.board.noteDraft).
							Label(tr("备注")).
							Placeholder(tr("记录进展或给 Agent 留备注…")).
							Bind(&a.board.noteInput).
							Height(60).FillWidth().FontSize(13.5).LineHeight(1.4).FocusRing(false)
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
							shortcut := "Ctrl+↩"
							if runtime.GOOS == "darwin" {
								shortcut = "⌘↩"
							}
							kbdHint(c, "board-drawer-note-key", shortcut)
							canSend := strings.TrimSpace(a.board.noteDraft) != ""
							if ui.PrimaryButton(c.Key("board-drawer-note-add"), tr("添加备注")).
								Padding(4, 12).FontSize(13).Disabled(!canSend).Clicked() {
								submitNote()
							}
						})
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

	bg := t.Surface.Alpha(0.55)
	border := t.Border.Alpha(0.7)
	if stale {
		bg = t.Danger.Alpha(0.08)
		border = t.Danger.Alpha(0.35)
	}
	row := ui.Row(c.Key("board-commit-row-"+link.Repo+"-"+link.Hash)).
		FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
		Radius(8).Background(bg).Transition(colorMotion())
	if row.Hovered() && !stale {
		border = t.Accent.Alpha(0.5)
	}
	row.Border(1, border)
	row.Children(func() {
		jumpBtn := ui.ButtonBase(c.Key("board-commit-open-" + link.Repo + "-" + link.Hash)).
			Label(repoTitle(link.Repo) + " " + short).
			Tooltip(link.Repo + " @ " + link.Hash).
			Grow(1).MinWidth(0).Justify(ui.Start).Cursor(ui.CursorPointer)
		jumpBtn.Children(func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					boardRepoDot(c, link.Repo, 7)
					ui.Text(c, repoTitle(link.Repo)).FontSize(12.5).FontWeight(600).TextColor(t.Text).SingleLine().Shrink(0)
					ui.Text(c, short).Font("monospace").FontSize(12).TextColor(t.TextMuted).SingleLine().Shrink(0)
					ui.Box(c).Grow(1)
					if link.Agent != "" {
						boardAgentTag(c, "board-commit-agent-"+link.Hash, link.Agent)
					}
					if stale {
						chipSoft(c, "board-commit-stale-"+link.Hash, tr("提交已失效"), t.Danger, false)
					}
				})
				if link.Subject != "" {
					sub := ui.Text(c, link.Subject).FontSize(13).SingleLine().MinWidth(0)
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
			a.commitBoardDrafts()
			bf, _, err := boardUnlinkCommit(task.ID, link.Repo, link.Hash)
			if err != nil {
				a.setBanner(err.Error(), "")
			} else {
				a.applyBoardFile(bf, c)
			}
		}
	})
}

func (a *app) boardModals(c *ui.Context) {
	a.newBoardTaskModal(c)
	a.linkBoardCommitModal(c)
	a.boardSnippetModal(c)
	a.boardDeleteTaskModal(c)
}

func (a *app) newBoardTaskModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-new-task-modal"), &a.board.showNewModal, tr("新建看板任务"), 480, false, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, tr("标题")).FontSize(13).TextColor(t.TextMuted)
			titleInput := ui.TextInput(c.Key("board-new-title"), &a.board.newTitle).
				Placeholder(tr("一句话描述需要完成的任务")).
				FontSize(13.5).
				FillWidth().AutoFocus()

			ui.Text(c, tr("状态")).FontSize(13).TextColor(t.TextMuted)
			if boardStatusSegmented(c, "board-new-status-seg", &a.board.newStatusIdx).Track.Changed() {
				a.board.newStatus = boardStatuses[a.board.newStatusIdx]
			}

			if known := a.boardKnownRepos(); len(known) > 0 {
				ui.Text(c, tr("关联项目（可选）")).FontSize(13).TextColor(t.TextMuted)
				ui.Row(c).Wrap().Gap(6).Children(func() {
					for _, rp := range known {
						repoPath := rp
						selected := slices.Contains(a.board.newRepos, repoPath)
						btn := ui.ButtonBase(c.Key("board-new-repo-"+repoPath)).
							Label(repoTitle(repoPath)).Padding(4, 9).Radius(5).Cursor(ui.CursorPointer).Transition(colorMotion())
						if selected {
							btn.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.5))
						} else {
							btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
						}
						btn.Children(func() {
							ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
								boardRepoDot(c, repoPath, 7)
								lbl := ui.Text(c, repoTitle(repoPath)).FontSize(12.5).SingleLine()
								if selected {
									lbl.TextColor(t.Accent).FontWeight(600)
								} else {
									lbl.TextColor(t.TextMuted)
								}
							})
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
								Label(short+" "+cm.subject).
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
		bf, _, err := boardLinkCommit(taskID, link)
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

// boardDeleteTaskModal asks before a task and everything linked to it disappears;
// there is no undo, so one click in the card menu or drawer must not be enough.
func (a *app) boardDeleteTaskModal(c *ui.Context) {
	open := a.board.deleteTaskID != ""
	if !open {
		return
	}
	id := a.board.deleteTaskID
	t := c.Theme()
	task, ok := boardTaskByID(a.board.file.Tasks, id)
	label := id
	if ok {
		label = task.ID + " " + task.Title
	}
	appModal(c.Key("board-delete-task-modal"), &open, tr("删除看板任务"), 420, false, func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, label).FontSize(13).FontWeight(500).TextColor(t.Text)
			ui.Text(c, tr("任务和它的关联提交、备注会一起删除，无法恢复。")).FontSize(12.5).TextColor(t.TextMuted)
		})
	}, func() {
		if ui.Button(c.Key("board-delete-cancel"), tr("取消")).Clicked() {
			open = false
		}
		if ui.Button(c.Key("board-delete-confirm"), tr("确认删除")).Padding(2, 12).Clicked() {
			bf, err := boardDeleteTask(id)
			if err != nil {
				a.setBanner(err.Error(), "")
				open = false
			} else {
				a.forgetBoardTaskDraft(id)
				if a.board.selectedID == id {
					a.board.selectedID = ""
					a.board.editTaskID = ""
				}
				a.applyBoardFile(bf, c)
				open = false
			}
		}
	})
	if !open {
		a.board.deleteTaskID = ""
	}
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
