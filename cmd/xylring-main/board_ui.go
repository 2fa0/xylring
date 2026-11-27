package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type boardFilterKey struct {
	repo, search string
	archived     bool
	english      bool
}

type boardState struct {
	jobTail            chan struct{}
	jobs               int
	loading            bool
	draftWrites        map[string]boardDraftWrite
	filterKey          boardFilterKey
	filtered           [][]boardTask
	filteredTotal      int
	loaded             bool
	file               boardFile
	loadErr            string
	lastMod            time.Time
	lastSize           int64
	polling            bool
	repoFilter         string
	statusFilter       boardStatus
	sortMode           boardSortMode
	doneExpanded       bool
	search             string
	searchInput        ui.Handle
	quickTitle         string
	quickInput         ui.Handle
	quickSaving        bool
	showArchived       bool
	selectedID         string
	editTaskID         string
	titleDraft         string
	titleInput         ui.Handle
	descDraft          string
	descInput          ui.Handle
	noteDraft          string
	noteInput          ui.Handle
	noteDrafts         map[string]string
	editingNoteID      string
	editingNoteText    string
	editNoteInput      ui.Handle
	drawerStatusIdx    int
	drawerPriorityOpen bool
	drawerDueOpen      bool
	drawerDuePicker    time.Time
	deleteTaskID       string
	deleteNoteTaskID   string
	deleteNoteID       string
	writeGen           int
	showNewModal       bool
	newSaving          bool
	newTitle           string
	newDesc            string
	newStatus          boardStatus
	newStatusIdx       int
	newPriority        boardPriority
	newPriorityOpen    bool
	newDueDate         string
	newDueOpen         bool
	newDuePicker       time.Time
	newRepos           []string
	newErr             string
	showLinkModal      bool
	linkRepo           string
	linkRef            string
	linkInput          ui.Handle
	linkErr            string
	linkBusy           bool
	showSnippet        bool
	showImportModal    bool
	importPlan         boardImportPlan
	importModeIdx      int
	importErr          string
	importBusy         bool
	showBackupsModal   bool
	backups            []boardBackupInfo
	backupsErr         string
	showStorageModal   bool
	storageErr         string
	showSwitchDirModal bool
	switchTargetDir    string
	switchTargetCount  int
	switchDirErr       string
	storageInfo        boardStorageInfo
	storageLoaded      bool
	storageBusy        bool
	commitValid        map[string]bool
	checkingLinks      map[string]bool
	recentUpdated      map[string]time.Time
	dataGen            int
	wall               boardWallLayout
	wallScroll         ui.ScrollState
	wallHandles        map[string]*ui.Handle
	wallHeights        map[string]boardWallHeight
	wallPending        []boardWallPending
	refreshAt          time.Time
	refreshPending     bool
}

func (a *app) boardModalOpen() bool {
	b := &a.board
	return b.showNewModal || b.showLinkModal || b.showSnippet ||
		b.deleteTaskID != "" || b.deleteNoteID != "" ||
		b.showImportModal || b.showBackupsModal || b.showStorageModal || b.showSwitchDirModal
}

func (a *app) ensureBoardLoaded() {
	if a.board.loaded || a.board.loading {
		return
	}
	a.board.loading = true
	var bf boardFile
	var info boardStorageInfo
	var mod time.Time
	var size int64
	var err error
	a.runBoardJob(func() {
		info, _ = boardLocationInfo()
		bf, err = loadBoardFile()
		if p, pathErr := boardConfigPath(); pathErr == nil {
			if stat, statErr := os.Stat(p); statErr == nil {
				mod, size = stat.ModTime(), stat.Size()
			}
		}
	}, func() {
		a.board.loading, a.board.loaded = false, true
		if info.ActiveDir != "" {
			a.board.storageInfo, a.board.storageLoaded = info, true
		}
		if err != nil {
			a.board.loadErr = err.Error()
			return
		}
		a.board.lastMod, a.board.lastSize = mod, size
		a.applyBoardFile(bf, nil)
	})
}

func (a *app) refreshBoardStorageInfo() {
	if info, err := boardLocationInfo(); err == nil {
		a.board.storageInfo = info
		a.board.storageLoaded = true
	}
}

func (a *app) currentBoardStorageInfo() boardStorageInfo {
	if !a.board.storageLoaded {
		a.refreshBoardStorageInfo()
	}
	return a.board.storageInfo
}

func (a *app) resetBoardAfterReplace() {
	a.board.selectedID = ""
	a.board.editTaskID = ""
	a.board.titleDraft = ""
	a.board.descDraft = ""
	a.board.noteDraft = ""
	a.board.noteDrafts = nil
	a.board.editingNoteID = ""
	a.board.editingNoteText = ""
	a.board.drawerPriorityOpen = false
	a.board.drawerDueOpen = false
	a.board.commitValid = nil
	a.board.checkingLinks = nil
	a.board.recentUpdated = nil
}

func (a *app) markExternalBoardUpdates(prev, next []boardTask) {
	now := time.Now()
	for id, ts := range a.board.recentUpdated {
		if now.Sub(ts) > 15*time.Second {
			delete(a.board.recentUpdated, id)
		}
	}
	prevByID := make(map[string]boardTask, len(prev))
	for _, t := range prev {
		prevByID[t.ID] = t
	}
	for _, t := range next {
		old, ok := prevByID[t.ID]
		if !ok || !t.UpdatedAt.Equal(old.UpdatedAt) || t.Status != old.Status ||
			len(t.Notes) != len(old.Notes) || len(t.Commits) != len(old.Commits) {
			if a.board.recentUpdated == nil {
				a.board.recentUpdated = map[string]time.Time{}
			}
			a.board.recentUpdated[t.ID] = now
		}
	}
}

func (a *app) reloadBoardSync() {
	a.refreshBoardStorageInfo()
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
	// Every view derived from the tasks is stale now; the wall rebuilds from
	// this generation and keeps its measured card heights per task.
	a.board.dataGen++
	a.board.wall.valid = false
	// The previous file is only needed to tell what the open drawer's drafts
	// differ from, so keep that one task and let the rest go before the new
	// file is held: the two files no longer sit side by side in memory.
	var prevTask boardTask
	var hasPrev bool
	if a.board.selectedID != "" {
		prevTask, hasPrev = boardTaskByID(a.board.file.Tasks, a.board.selectedID)
	}
	a.board.file = bf
	a.board.loadErr = ""
	a.board.filtered = nil
	if len(a.board.wallHeights) > 0 {
		live := make(map[string]struct{}, len(bf.Tasks))
		for _, task := range bf.Tasks {
			live[task.ID] = struct{}{}
		}
		for id := range a.board.wallHeights {
			if _, ok := live[id]; !ok {
				delete(a.board.wallHeights, id)
			}
		}
	}
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
	// A snapshot can arrive from another process while the drawer is open. Keep
	// anything the user typed but has not saved yet; refresh the rest.
	titleDirty := !hasPrev || a.board.titleDraft != prevTask.Title
	queued := a.board.draftWrites[t.ID]
	if queued.token != nil {
		titleDirty = queued.token.Title != nil || titleDirty
	}
	if !titleDirty && (c == nil || !a.board.titleInput.Focused(c)) {
		a.board.titleDraft = t.Title
	}
	descDirty := !hasPrev || a.board.descDraft != prevTask.Description || queued.token != nil && queued.token.Description != nil
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
	if queued, ok := a.board.draftWrites[t.ID]; ok {
		a.board.titleDraft, a.board.descDraft = queued.title, queued.description
	}
	a.board.drawerStatusIdx = boardStatusIndex(t.Status)
	a.board.noteDraft = ""
	if draft, ok := a.board.noteDrafts[t.ID]; ok {
		a.board.noteDraft = draft
	}
	a.board.editingNoteID = ""
	a.board.editingNoteText = ""
	a.board.drawerPriorityOpen = false
	a.board.drawerDueOpen = false
	a.board.deleteNoteTaskID = ""
	a.board.deleteNoteID = ""
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
	a.board.editingNoteID = ""
	a.board.editingNoteText = ""
	a.board.drawerPriorityOpen = false
	a.board.drawerDueOpen = false
	a.board.deleteNoteTaskID = ""
	a.board.deleteNoteID = ""
}

type boardDraftWrite struct {
	title, description string
	token              *boardTaskPatch
}

func (a *app) commitBoardDrafts() {
	id := a.board.editTaskID
	if id == "" {
		return
	}
	cur, ok := boardTaskByID(a.board.file.Tasks, id)
	if !ok {
		return
	}
	base, queued := a.board.draftWrites[id]
	if !queued {
		base.title, base.description = cur.Title, cur.Description
	}
	title, desc := strings.TrimSpace(a.board.titleDraft), a.board.descDraft
	if title == "" {
		a.setBanner(tr("任务标题不能为空"), "")
		title = base.title
		a.board.titleDraft = title
	}
	patch := &boardTaskPatch{}
	if title != base.title {
		patch.Title = &title
	}
	if desc != base.description {
		patch.Description = &desc
	}
	if patch.Title == nil && patch.Description == nil {
		return
	}
	if queued && base.token.Title != nil {
		patch.Title = &title
	}
	if queued && base.token.Description != nil {
		patch.Description = &desc
	}
	if a.board.draftWrites == nil {
		a.board.draftWrites = make(map[string]boardDraftWrite)
	}
	a.board.draftWrites[id] = boardDraftWrite{title, desc, patch}
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardUpdateTask(id, *patch)
		return bf, err
	}, func(err error) {
		if a.board.draftWrites[id].token == patch {
			delete(a.board.draftWrites, id)
			if err == nil && a.board.editTaskID == id && strings.TrimSpace(a.board.titleDraft) == title {
				a.board.titleDraft = title
			}
		}
		if err != nil {
			a.setBanner(err.Error(), "")
		}
	})
}

// leaveBoard saves the open drawer before the board page goes away, so text
// typed there is not left only in memory.
func (a *app) leaveBoard() {
	if a.showBoard {
		a.commitBoardDrafts()
	}
	// The filtered columns hold a copy of every task they show; the board page
	// builds them again when it comes back, so let them go while it is away.
	a.board.filtered = nil
	a.board.filteredTotal = 0
	a.showBoard = false
}

func (a *app) openBoard() {
	a.ensureBoardLoaded()
	a.showHome = false
	a.showBoard = true
	a.pollBoardFile()
}

func (a *app) pollBoardFile() {
	if a.closed || a.board.polling || a.board.jobs != 0 {
		return
	}
	lastMod := a.board.lastMod
	lastSize := a.board.lastSize
	loaded := a.board.loaded
	hasTasks := len(a.board.file.Tasks) > 0
	gen := a.board.writeGen
	a.board.polling = true
	go func() {
		defer a.onMain(func() {
			a.board.polling = false
		})
		path, err := boardConfigPath()
		if err != nil {
			return
		}
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && loaded && hasTasks {
				a.post(func() {
					if gen != a.board.writeGen || a.closed {
						return
					}
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
			if a.closed || gen != a.board.writeGen {
				return
			}
			a.board.lastMod = mod
			a.board.lastSize = size
			a.board.loaded = true
			if loaded {
				a.markExternalBoardUpdates(a.board.file.Tasks, bf.Tasks)
			}
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
	if isDockerRepoPath(link.Repo) {
		a.setBanner(tr("容器仓库暂不支持从看板打开"), link.Repo)
		return
	}
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

func (a *app) boardRepoActiveCount(repo string) int {
	if !a.board.loaded {
		return 0
	}
	norm := normalizeBoardRepo(repo)
	if norm == "" {
		return 0
	}
	n := 0
	for _, t := range a.board.file.Tasks {
		if t.Archived || t.Status == boardDone {
			continue
		}
		if slices.Contains(boardTaskAllRepos(t), norm) {
			n++
		}
	}
	return n
}

func (a *app) boardTasksForCommit(repo, fullHash string) []boardTask {
	if !a.board.loaded || fullHash == "" {
		return nil
	}
	normRepo := normalizeBoardRepo(repo)
	normHash := strings.ToLower(strings.TrimSpace(fullHash))
	var out []boardTask
	for _, t := range a.board.file.Tasks {
		for _, link := range t.Commits {
			if strings.ToLower(strings.TrimSpace(link.Hash)) == normHash &&
				(normRepo == "" || normalizeBoardRepo(link.Repo) == normRepo) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func (a *app) boardKnownRepos() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if isDockerRepoPath(p) {
			// 看板与容器仓库的关联暂不开放；已有本机数据保持不变。
			return
		}
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

func (a *app) openNewBoardTaskModal(status boardStatus) {
	if status == "" {
		status = boardTodo
	}
	a.board.newTitle = ""
	a.board.newDesc = ""
	a.board.newStatus = status
	a.board.newStatusIdx = boardStatusIndex(status)
	a.board.newPriority = boardPriorityNone
	a.board.newPriorityOpen = false
	a.board.newDueDate = ""
	a.board.newDueOpen = false
	a.board.newDuePicker = time.Now()
	a.board.newRepos = nil
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
		if t.Dark {
			row.Background(ui.RGBA(255, 255, 255, 0.04)).Border(1, t.Border.Alpha(0.5)).
				InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
		} else {
			row.Background(ui.RGBA(255, 255, 255, 0.78)).Border(1, t.Border.Alpha(0.55)).
				Shadow(0, 1, 2, 0, ui.RGBA(15, 23, 42, 0.05))
		}
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
				ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).FontWeight(600).FontFeatures("tnum").TextColor(badgeFg).SingleLine()
			})
		}
	})
	if row.Clicked() {
		a.openBoard()
	}
}

type boardNoteDrag struct {
	taskID string
	noteID string
	index  int
}

type boardNoteMatch struct{ id string }

func boardNoteMotion(id string) ui.ElementTransition {
	return ui.ElementTransition{
		Duration: motionColor,
		Ease:     ui.EaseOut,
		Position: true,
		Colors:   true,
		Match:    boardNoteMatch{id: id},
	}
}

type boardCardMatch struct{ id string }

type boardSegIndicatorMatch struct{ key string }

type boardFocusPillMatch struct{}

type boardTaskDrag struct {
	taskID string
	status boardStatus
	pinned bool
}

var boardSpring = uiSpring

func boardPageMotion() ui.ElementTransition {
	// Keep the page opaque to avoid a window-sized offscreen render target.
	return ui.ElementTransition{
		Duration: motionPane,
		Ease:     ui.EaseOut,
		Enter:    &ui.Motion{Opacity: 1, Y: 6},
	}
}

func boardDrawerMotion() ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &boardSpring,
		Enter:     &ui.Motion{X: 22, Opacity: 1},
		Exit:      &ui.Motion{X: 22, Opacity: 0},
		Colors:    true,
	}
}

func boardCardMotion(id string) ui.ElementTransition {
	return ui.ElementTransition{
		Animation: &boardSpring,
		Position:  true,
		Colors:    true,
		Shadows:   true,
		Match:     boardCardMatch{id: id},
		Enter:     &ui.Motion{Opacity: 0, Scale: 0.96, Y: 6},
		Exit:      &ui.Motion{Opacity: 0, Scale: 0.96},
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

func boardStatusSegmented(c *ui.Context, key string, selected *int, fontSize float32) ui.SegmentedParts {
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
					lbl := ui.Text(c, tr(boardStatusLabel(st))).FontSize(fontSize).SingleLine()
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
	b := &a.board
	key := boardFilterKey{repo: b.repoFilter, search: b.search, archived: b.showArchived, english: englishUI.Load()}
	if b.filtered != nil && b.filterKey == key {
		return b.filtered, b.filteredTotal
	}
	cols := make([][]boardTask, len(boardStatuses))
	total := 0
	for _, task := range a.board.file.Tasks {
		if !boardMatchesFilter(task, a.board.repoFilter, a.board.search, a.board.showArchived) {
			continue
		}
		cols[boardStatusIndex(task.Status)] = append(cols[boardStatusIndex(task.Status)], task)
		total++
	}
	b.filterKey, b.filtered, b.filteredTotal = key, cols, total
	return cols, total
}

func (a *app) boardScreen(c *ui.Context) {
	a.ensureBoardLoaded()
	t := c.Theme()
	cols, _ := a.boardFilteredByStatus()
	canvasBg := ui.Hex("#F4F3EF")
	if t.Dark {
		canvasBg = ui.Hex("#111215")
	}
	statusTint := t.Accent
	tintAlpha := float32(0.035)
	if t.Dark {
		tintAlpha = 0.05
	}
	switch a.board.statusFilter {
	case boardReview:
		statusTint = t.Warning
		tintAlpha = 0.065
		if t.Dark {
			tintAlpha = 0.09
		}
	case boardDoing:
		statusTint = t.Accent
		tintAlpha = 0.065
		if t.Dark {
			tintAlpha = 0.09
		}
	}
	dotCol := ui.RGBA(15, 23, 42, 0.045)
	if t.Dark {
		dotCol = ui.RGBA(255, 255, 255, 0.032)
	}
	body := func() {
		ui.Column(c.Key("board-workspace")).Grow(1).MinWidth(0).MinHeight(0).
			Background(canvasBg).
			BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.65)).
			Draw(func(p *ui.Painter, r ui.Rect) {
				if r.W <= 0 || r.H <= 0 {
					return
				}
				p.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.35, Y: 0.0},
					Radius: max(r.W*0.55, 360),
					Oklab:  true,
					Stops: []ui.GradientStop{
						{Color: statusTint.Alpha(tintAlpha), At: 0},
						{Color: ui.Transparent, At: 1},
					},
				}, 0)
				const pitch = float32(24)
				for y := r.Y + 12; y < r.Y+r.H; y += pitch {
					for x := r.X + 12; x < r.X+r.W; x += pitch {
						p.Fill(ui.Rect{X: x, Y: y, W: 1.5, H: 1.5}, dotCol, 0.75)
					}
				}
			}).
			Transition(workspaceMainMotion()).Children(func() {
			a.globalAlertBanner(c, nil)
			if a.board.loadErr != "" {
				ui.Row(c.Key("board-error-banner")).FillWidth().Padding(6, 16).Gap(8).AlignItems(ui.Center).
					Background(t.Danger.Alpha(0.12)).Children(func() {
					ui.Text(c, trMessage(a.board.loadErr)).FontSize(12.5).TextColor(t.Danger).Grow(1).MinWidth(0)
					if ui.Button(c.Key("board-err-restore"), tr("恢复备份…")).Padding(2, 8).FontSize(12).Clicked() {
						a.openBoardBackupsModal()
					}
					if ui.Button(c.Key("board-err-dir"), tr("数据目录…")).Padding(2, 8).FontSize(12).Clicked() {
						a.openBoardStorageModal()
					}
				})
			}
			a.boardStatusFocusBar(c, cols)
			ui.Box(c.Key("board-body")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.boardContextWall(c)
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

		moreBtn := ui.ButtonBase(c.Key("board-more-menu")).Label(tr("更多操作")).Tooltip(tr("更多操作"))
		styleActionIcon(c, moreBtn, iconMore, 30, 15, t.TextMuted, false)
		moreBtn.Menu(func(m *ui.Menu) {
			if m.Item(tr("导出备份…")).Disabled(a.picking).Chosen() {
				a.startBoardExport()
			}
			if m.Item(tr("导入备份…")).Disabled(a.picking).Chosen() {
				a.startBoardImport()
			}
			if m.Item(tr("从自动备份恢复…")).Chosen() {
				a.openBoardBackupsModal()
			}
			m.Separator()
			if m.Item(tr("数据目录…")).Chosen() {
				a.openBoardStorageModal()
			}
			if m.Item(tr("打开数据目录")).Chosen() {
				a.openBoardFolder()
			}
		})

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

func (a *app) boardStatusFocusBar(c *ui.Context, cols [][]boardTask) {
	t := c.Theme()
	reviewCount := len(cols[boardStatusIndex(boardReview)])
	doingCount := len(cols[boardStatusIndex(boardDoing)])
	todoCount := len(cols[boardStatusIndex(boardTodo)])
	activeTotal := reviewCount + doingCount + todoCount

	availW, _ := c.Size()
	if a.showRepoSidebar() {
		availW -= a.repoSplit + 1
	}

	trackBg := ui.RGBA(15, 23, 42, 0.045)
	pillActiveBg := ui.Hex("#FFFFFF")
	if t.Dark {
		trackBg = ui.RGBA(255, 255, 255, 0.04)
		pillActiveBg = ui.Hex("#232630")
	}

	// The pills, the sort menu and the quick capture share one row while there
	// is room; below that the pills get their own row, wrapping if they must,
	// so no control is pushed past the window's edge.
	stacked := availW < 860
	focusPill := func(id, label string, st boardStatus, count int) {
		active := a.board.statusFilter == st
		btn := ui.ButtonBase(c.Key("board-focus-"+id)).
			Label(tr(label)).
			Role(ui.RoleTab).
			AccessSelected(active).
			Height(26).Padding(0, 11).Radius(13).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Shrink(0).
			Transition(colorMotion())

		var dropHover bool
		if st != "" {
			if dragged, over := ui.DragOver[boardTaskDrag](btn); over && dragged.status != st {
				dropHover = true
			}
		}

		stCol := boardStatusColor(c, st)
		fg := t.TextMuted
		countFg := t.TextMuted.Alpha(0.75)
		switch {
		case dropHover:
			btn.Background(stCol.Alpha(0.18)).Border(1.5, stCol)
			fg = stCol
			countFg = stCol
		case active:
			if st == "" {
				fg = t.Text
				countFg = t.TextMuted
			} else {
				fg = stCol
				countFg = stCol
			}
		case btn.Pressed():
			btn.Background(t.SurfacePressed.Alpha(0.6))
			fg = t.Text
		case btn.Hovered():
			btn.Background(t.SurfaceHover.Alpha(0.7))
			fg = t.Text
		}

		btn.Children(func() {
			if active && !dropHover {
				pill := ui.Box(c.Key("board-focus-active-pill")).Absolute().Fill().
					Radius(13).Background(pillActiveBg).
					IgnoreInput(true).ZIndex(0).
					Transition(ui.ElementTransition{
						Animation: &boardSpring,
						Match:     boardFocusPillMatch{},
					})
				if st != "" {
					pill.Border(1, stCol.Alpha(0.42))
				} else {
					pill.Border(1, t.Border.Alpha(0.55))
				}
				if t.Dark {
					pill.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.08))
				} else {
					pill.Shadow(0, 1, 3, 0, ui.RGBA(15, 23, 42, 0.10))
				}
			}
			ui.Row(c).AlignItems(ui.Center).Gap(6).ZIndex(1).Children(func() {
				if st != "" {
					boardStatusDot(c, st, 7)
				}
				lbl := ui.Text(c, tr(label)).FontSize(12.5).TextColor(fg).SingleLine()
				if active || dropHover {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
				ui.Text(c, fmt.Sprintf("%d", count)).FontSize(11.5).FontFeatures("tnum").
					TextColor(countFg).SingleLine()
			})
		})

		if st != "" {
			if dropped, ok := ui.Drop[boardTaskDrag](btn); ok && dropped.status != st {
				target := st
				a.applyBoardTaskPatch(dropped.taskID, boardTaskPatch{Status: &target})
			}
		}
		if btn.Clicked() {
			a.board.statusFilter = st
		}
	}

	pills := func(fill bool) {
		track := ui.Row(c.Key("board-focus-track")).Padding(3).Gap(2).Radius(16).
			Role(ui.RoleTabList).Label(tr("状态筛选")).
			Background(trackBg).Border(1, t.Border.Alpha(0.5)).AlignItems(ui.Center)
		if fill {
			track.FillWidth().Wrap()
		} else {
			track.Shrink(0)
		}
		track.Children(func() {
			focusPill("all", "全部未完成", "", activeTotal)
			focusPill("review", "待审阅", boardReview, reviewCount)
			focusPill("doing", "进行中", boardDoing, doingCount)
			focusPill("todo", "待办", boardTodo, todoCount)
		})
	}
	right := func() {
		ui.Row(c.Key("board-focus-right")).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			sortLabel := tr(boardSortLabel(a.board.sortMode))
			isCustomSort := a.board.sortMode != boardSortDefault
			sortBtn := ui.ButtonBase(c.Key("board-sort-menu")).
				Label(sortLabel).Tooltip(tr("排序方式")).
				Height(32).Padding(0, 11).Gap(5).Radius(16).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).Shrink(0).
				Transition(colorMotion())
			sortFg := t.TextMuted
			switch {
			case isCustomSort:
				sortFg = t.Accent
				sortBtn.Background(t.Accent.Alpha(0.11)).Border(1, t.Accent.Alpha(0.40))
			case sortBtn.Pressed():
				sortBtn.Background(t.SurfacePressed).Border(1, t.Border.Alpha(0.75))
				sortFg = t.Text
			case sortBtn.Hovered():
				sortBtn.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.7))
				sortFg = t.Text
			default:
				sortBg := ui.RGBA(255, 255, 255, 0.72)
				if t.Dark {
					sortBg = ui.RGBA(255, 255, 255, 0.04)
				}
				sortBtn.Background(sortBg).Border(1, t.Border.Alpha(0.6))
			}
			sortBtn.Children(func() {
				ui.Icon(c, iconArrowUpDown).Size(13, 13).TextColor(sortFg).Shrink(0)
				if availW >= 640 {
					lbl := ui.Text(c, sortLabel).FontSize(12.5).TextColor(sortFg).SingleLine()
					if isCustomSort {
						lbl.FontWeight(600)
					} else {
						lbl.FontWeight(500)
					}
				}
				ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(sortFg).Shrink(0)
			})
			sortBtn.Menu(func(m *ui.Menu) {
				for _, mode := range boardSortModes {
					target := mode
					if m.Item(tr(boardSortLabel(target))).Checked(a.board.sortMode == target).Chosen() {
						a.board.sortMode = target
					}
				}
			})

			if availW >= 580 {
				capW := float32(240)
				if availW < 880 {
					capW = 180
				}
				capBg := ui.RGBA(255, 255, 255, 0.85)
				if t.Dark {
					capBg = ui.RGBA(255, 255, 255, 0.05)
				}
				capBox := ui.Row(c.Key("board-quick-capture")).Width(capW).Height(32).
					Padding(0, 10).Gap(6).Radius(16).Background(capBg).
					AlignItems(ui.Center).Shrink(0).Transition(colorMotion())
				var quickEl ui.Element
				capBox.Children(func() {
					ui.Icon(c, iconPlus).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
					quickEl = ui.TextInputBase(c.Key("board-quick-input"), &a.board.quickTitle).
						Label(tr("快速新建任务")).
						Placeholder(tr("快速记一条任务…")).
						Bind(&a.board.quickInput).
						Disabled(a.board.quickSaving).
						FontSize(12.5).Grow(1).MinWidth(0).FocusRing(false)
					if quickEl.Focused() && strings.TrimSpace(a.board.quickTitle) != "" {
						kbdHint(c, "board-quick-enter", "↩")
					}
				})
				switch {
				case quickEl.Focused():
					capBox.Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
				case capBox.Hovered():
					capBox.Border(1, t.TextMuted.Alpha(0.5))
				default:
					capBox.Border(1, t.Border.Alpha(0.65))
				}
				if quickEl.Submitted() {
					a.submitQuickBoardTask()
				}
			}
		})
	}
	bar := ui.Column(c.Key("board-focus-bar")).FillWidth().Padding(14, 20, 4, 20).Gap(8).Shrink(0)
	bar.Children(func() {
		if stacked {
			ui.Row(c.Key("board-focus-row-pills")).FillWidth().Children(func() {
				pills(true)
			})
			ui.Row(c.Key("board-focus-row-controls")).FillWidth().Justify(ui.End).Children(func() {
				right()
			})
			return
		}
		ui.Row(c.Key("board-focus-row")).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			pills(false)
			ui.Box(c).Grow(1).MinWidth(8)
			right()
		})
	})
}

func (a *app) submitQuickBoardTask() {
	title := strings.TrimSpace(a.board.quickTitle)
	if title == "" || a.board.quickSaving {
		return
	}
	status := a.board.statusFilter
	if status == "" {
		status = boardTodo
	}
	var repos []string
	if a.board.repoFilter != "" {
		repos = []string{a.board.repoFilter}
	}
	a.board.quickSaving = true
	a.board.quickTitle = ""
	var created boardTask
	a.changeBoard(func() (boardFile, error) {
		bf, task, err := boardAddTaskWithOptions(boardAddTaskOptions{
			Title:  title,
			Status: status,
			Repos:  repos,
		})
		created = task
		return bf, err
	}, func(err error) {
		a.board.quickSaving = false
		if err != nil {
			// The draft stays so nothing typed is lost.
			a.board.quickTitle = title
			a.setBanner(err.Error(), "")
			return
		}
		if created.ID != "" {
			// The new card can be hidden by the search or the status focus; the
			// task number says it was created either way.
			a.pendingToast = fmt.Sprintf(tr("已创建任务 %s"), created.ID)
		}
	})
}
