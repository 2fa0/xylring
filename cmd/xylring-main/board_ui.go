package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/i18n"
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

func boardPriorityColor(c *ui.Context, p boardPriority) ui.Color {
	t := c.Theme()
	switch p {
	case boardPriorityUrgentImportant:
		return t.Danger
	case boardPriorityImportant:
		return t.Accent
	case boardPriorityUrgent:
		return t.Warning
	default:
		return t.TextMuted
	}
}

func boardPriorityPill(c *ui.Context, key string, p boardPriority, fontSize float32) ui.Element {
	col := boardPriorityColor(c, p)
	pill := ui.Row(c.Key(key)).Padding(2, 7).Gap(5).Radius(4).
		Background(col.Alpha(0.12)).AlignItems(ui.Center).Shrink(0)
	pill.Children(func() {
		ui.Box(c).Size(6, 6).Radius(3).Background(col).Shrink(0)
		lbl := ui.Text(c, tr(boardPriorityLabel(p))).FontSize(fontSize).TextColor(col).SingleLine()
		if p == boardPriorityUrgentImportant {
			lbl.FontWeight(600)
		} else {
			lbl.FontWeight(500)
		}
	})
	return pill
}

func sameBoardDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func boardCardDueAppearance(c *ui.Context, rawDue string, parsed time.Time, state boardDueState) (text, tip string, fg, bg ui.Color) {
	t := c.Theme()
	now := time.Now()
	if sameBoardDay(parsed, now) {
		text = tr("今天")
	} else if parsed.Year() == now.Year() {
		text = parsed.Format("01-02")
	} else {
		text = parsed.Format("2006-01-02")
	}
	tip = rawDue
	switch state {
	case boardDueOverdue:
		fg = t.Danger
		bg = t.Danger.Alpha(0.12)
		tip = rawDue + " · " + tr("已逾期")
	case boardDueToday:
		fg = t.Warning
		bg = t.Warning.Alpha(0.14)
		tip = rawDue + " · " + tr("今天截止")
	default:
		fg = t.TextMuted
		bg = t.TextMuted.Alpha(0.10)
	}
	return text, tip, fg, bg
}

func boardPriorityPicker(c *ui.Context, key string, current boardPriority, open *bool, fillWidth bool, onChange func(boardPriority)) {
	t := c.Theme()
	label := tr("请选择")
	if current != boardPriorityNone {
		label = tr(boardPriorityLabel(current))
	}
	btn := ui.ButtonBase(c.Key(key)).
		Label(label).
		Height(30).Padding(0, 10).Gap(6).Radius(t.Radius).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Transition(colorMotion())
	if fillWidth {
		btn.FillWidth()
	} else {
		btn.MinWidth(180)
	}
	switch {
	case *open:
		btn.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
	case btn.Pressed():
		btn.Background(t.SurfacePressed).Border(1, t.Border)
	case btn.Hovered():
		btn.Background(t.SurfaceHover).Border(1, t.Border)
	default:
		btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
	}
	btn.Children(func() {
		if current != boardPriorityNone {
			col := boardPriorityColor(c, current)
			ui.Box(c).Size(7, 7).Radius(3.5).Background(col).Shrink(0)
			lbl := ui.Text(c, tr(boardPriorityLabel(current))).FontSize(13.5).TextColor(col).FontWeight(500).SingleLine()
			if current == boardPriorityUrgentImportant {
				lbl.FontWeight(600)
			}
		} else {
			ui.Icon(c, iconFlag).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, tr("请选择")).FontSize(13.5).TextColor(t.TextMuted).SingleLine()
		}
		ui.Box(c).Grow(1).MinWidth(4)
		ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
	})
	if btn.Clicked() {
		*open = !*open
	}
	ui.PopoverBase(c, btn, open, func(panel ui.Element) {
		panel.MinWidth(max(btn.Bounds().W, 180)).Margin(4, 0, 0, 0).Padding(4).Gap(2).
			Radius(t.Radius+2).Background(t.Background).Border(1, t.Border).
			Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		for _, opt := range boardPriorities {
			p := opt
			selected := current == p
			col := boardPriorityColor(c, p)
			row := ui.ButtonBase(c.Key(key+"-opt-"+string(p))).
				Label(tr(boardPriorityLabel(p))).
				FillWidth().Padding(6, 8).Gap(8).Radius(t.Radius).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			switch {
			case row.Pressed():
				row.Background(t.SurfacePressed)
			case row.Hovered() || selected:
				row.Background(t.SurfaceHover)
			}
			row.Children(func() {
				pill := boardPriorityPill(c, key+"-opt-pill-"+string(p), p, 13)
				if selected {
					pill.Background(col.Alpha(0.18))
				}
				ui.Box(c).Grow(1)
				if selected {
					ui.Icon(c, iconCheck).Size(13, 13).TextColor(t.Accent).Shrink(0)
				}
			})
			if row.Clicked() {
				*open = false
				if current != p {
					onChange(p)
				}
			}
		}
		if current != boardPriorityNone {
			ui.Box(c).FillWidth().Height(1).Margin(2, 0).Background(t.Border.Alpha(0.6))
			clearBtn := ui.ButtonBase(c.Key(key+"-clear")).
				Label(tr("清除")).
				FillWidth().Padding(6, 8).Gap(6).Radius(t.Radius).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			if clearBtn.Hovered() {
				clearBtn.Background(t.SurfaceHover)
			}
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("清除")).FontSize(13).TextColor(t.TextMuted).SingleLine()
			})
			if clearBtn.Clicked() {
				*open = false
				onChange(boardPriorityNone)
			}
		}
	})
}

const (
	boardCalModeDate = iota
	boardCalModeMonth
	boardCalModeYear
)

func boardDueDatePicker(c *ui.Context, key, current string, doneOrArchived bool, open *bool, pickerDate *time.Time, fillWidth bool, onChange func(string)) {
	t := c.Theme()
	now := time.Now()
	parsed, dueState, hasDue := boardTaskDueInfo(current, doneOrArchived, now)
	label := tr("请选择日期")
	fg := t.TextMuted
	iconFg := t.TextMuted
	if hasDue {
		label = current
		switch dueState {
		case boardDueOverdue:
			fg = t.Danger
			iconFg = t.Danger
		case boardDueToday:
			fg = t.Warning
			iconFg = t.Warning
		default:
			fg = t.Text
		}
	}

	wrap := ui.Row(c.Key(key + "-wrap")).AlignItems(ui.Center)
	if fillWidth {
		wrap.FillWidth()
	}
	wrap.Children(func() {
		btn := ui.ButtonBase(c.Key(key)).
			Label(label).
			Height(30).Padding(0, 10).Gap(6).Radius(t.Radius).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).
			Transition(colorMotion())
		if fillWidth {
			btn.FillWidth()
		} else {
			btn.MinWidth(180)
		}
		switch {
		case *open:
			btn.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14))
		case btn.Pressed():
			btn.Background(t.SurfacePressed).Border(1, t.Border)
		case btn.Hovered():
			btn.Background(t.SurfaceHover).Border(1, t.Border)
		case dueState == boardDueOverdue:
			btn.Background(t.Danger.Alpha(0.06)).Border(1, t.Danger.Alpha(0.36))
		case dueState == boardDueToday:
			btn.Background(t.Warning.Alpha(0.07)).Border(1, t.Warning.Alpha(0.38))
		default:
			btn.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.7))
		}
		btn.Children(func() {
			ui.Icon(c, iconCalendar).Size(13, 13).TextColor(iconFg).Shrink(0)
			lbl := ui.Text(c, label).FontSize(13.5).TextColor(fg).SingleLine()
			if hasDue {
				lbl.FontFeatures("tnum")
				if dueState == boardDueOverdue || dueState == boardDueToday {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
			}
			if hasDue && dueState == boardDueToday {
				boardDueStatusChip(c, tr("今天"), t.Warning)
			} else if hasDue && dueState == boardDueOverdue {
				boardDueStatusChip(c, tr("已逾期"), t.Danger)
			}
			ui.Box(c).Grow(1).MinWidth(4)
			if hasDue {
				// Reserve room for the overlaid inline clear button so text never collides.
				ui.Box(c).Size(18, 18).Shrink(0)
			} else {
				ui.Icon(c, iconChevronDown).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
			}
		})
		if btn.Clicked() {
			*open = !*open
			if *open {
				if hasDue {
					*pickerDate = parsed
				} else {
					*pickerDate = now
				}
			}
		}
		if hasDue {
			clearBtn := ui.ButtonBase(c.Key(key+"-clear-inline")).
				Label(tr("清除截止日期")).Tooltip(tr("清除截止日期")).
				Absolute().Right(6).Top(5).Size(20, 20).Radius(10).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			clearFg := t.TextMuted
			switch {
			case clearBtn.Pressed():
				clearBtn.Background(t.SurfacePressed)
				clearFg = t.Text
			case clearBtn.Hovered():
				clearBtn.Background(t.SurfaceHover)
				clearFg = t.Text
			}
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(11, 11).TextColor(clearFg)
			})
			if clearBtn.Clicked() {
				*open = false
				onChange("")
			}
		}
		ui.PopoverBase(c, btn, open, func(panel ui.Element) {
			boardDueCalendarPopover(c, panel, key, current, parsed, hasDue, now, open, pickerDate, onChange)
		})
	})
}

func boardDueStatusChip(c *ui.Context, text string, col ui.Color) {
	ui.Row(c).Padding(1, 5).Radius(4).Background(col.Alpha(0.14)).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(600).TextColor(col).SingleLine()
	})
}

func boardDueCalendarPopover(c *ui.Context, panel ui.Element, key, current string, parsed time.Time, hasDue bool, now time.Time, open *bool, pickerDate *time.Time, onChange func(string)) {
	t := c.Theme()
	panel.Width(276).Margin(4, 0).Padding(0).Gap(0).
		Radius(10).Background(t.Background).Border(1, t.Border).Clip().
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.20))

	if pickerDate.IsZero() {
		*pickerDate = now
	}
	calMode := ui.Local(panel, "cal-mode", func() int { return boardCalModeDate })

	if *calMode == boardCalModeDate {
		if panel.Shortcut(0, ui.KeyPageUp) {
			*pickerDate = time.Date(pickerDate.Year(), pickerDate.Month()-1, 1, 0, 0, 0, 0, time.Local)
		}
		if panel.Shortcut(0, ui.KeyPageDown) {
			*pickerDate = time.Date(pickerDate.Year(), pickerDate.Month()+1, 1, 0, 0, 0, 0, time.Local)
		}
	}

	pickDate := func(target time.Time) {
		*pickerDate = target
		*open = false
		next := target.Format("2006-01-02")
		if next != current {
			onChange(next)
		}
	}

	// Quick preset bar
	ui.Row(c).FillWidth().Padding(10, 12, 8, 12).Gap(6).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.55)).Children(func() {
		quickChip := func(id, text string, target time.Time) {
			active := hasDue && current == target.Format("2006-01-02")
			b := ui.ButtonBase(c.Key(key+"-"+id)).
				Label(tr(text)).Height(24).Padding(0, 9).Radius(12).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			fg := t.Text
			switch {
			case active:
				b.Background(t.Accent.Alpha(0.14)).Border(1, t.Accent.Alpha(0.45))
				fg = t.Accent
			case b.Pressed():
				b.Background(t.SurfacePressed).Border(1, t.Border)
			case b.Hovered():
				b.Background(t.SurfaceHover).Border(1, t.Border)
			default:
				b.Background(t.Surface.Alpha(0.8)).Border(1, t.Border.Alpha(0.55))
			}
			b.Children(func() {
				lbl := ui.Text(c, tr(text)).FontSize(12).TextColor(fg).SingleLine()
				if active {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
			})
			if b.Clicked() {
				pickDate(target)
			}
		}
		quickChip("today", "今天", now)
		quickChip("tomorrow", "明天", now.AddDate(0, 0, 1))
		quickChip("nextweek", "下周", now.AddDate(0, 0, 7))
		ui.Box(c).Grow(1)
		if hasDue {
			clr := ui.ButtonBase(c.Key(key+"-clear")).
				Label(tr("清除")).Height(24).Padding(0, 7).Radius(6).Center().
				Cursor(ui.CursorPointer).Transition(colorMotion())
			clrFg := t.TextMuted
			switch {
			case clr.Pressed():
				clr.Background(t.Danger.Alpha(0.14))
				clrFg = t.Danger
			case clr.Hovered():
				clr.Background(t.Danger.Alpha(0.08))
				clrFg = t.Danger
			}
			clr.Children(func() {
				ui.Text(c, tr("清除")).FontSize(12).FontWeight(500).TextColor(clrFg).SingleLine()
			})
			if clr.Clicked() {
				*open = false
				onChange("")
			}
		}
	})

	locale, firstWeekday := boardCalendarLocale(c)
	view := time.Date(pickerDate.Year(), pickerDate.Month(), 1, 0, 0, 0, 0, time.Local)

	boardCalendarHeader(c, key, locale, view, calMode, pickerDate)

	ui.Column(c).FillWidth().Padding(2, 12, 12, 12).Gap(2).Children(func() {
		switch *calMode {
		case boardCalModeMonth:
			boardCalendarMonthGrid(c, key, locale, view, parsed, hasDue, calMode, pickerDate)
		case boardCalModeYear:
			boardCalendarYearGrid(c, key, view, parsed, hasDue, calMode, pickerDate)
		default:
			boardCalendarDayGrid(c, key, locale, firstWeekday, view, parsed, hasDue, now, pickDate)
		}
	})
}

func boardCalendarLocale(c *ui.Context) (locale string, firstWeekday time.Weekday) {
	lang := c.Locale()
	if lang == "" {
		if englishUI.Load() {
			lang = "en"
		} else {
			lang = "zh-Hans"
		}
	}
	region := c.Region()
	if region.HourCycle != 12 && region.HourCycle != 24 {
		region = i18n.RegionFor(lang)
	}
	return region.FormatLocale(lang), region.FirstWeekday
}

func boardCalendarNavButton(c *ui.Context, key, label string, icon *ui.SVG, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(label).Tooltip(label).
		Size(26, 26).Radius(13).Center().Shrink(0).
		Cursor(ui.CursorPointer).Transition(colorMotion())
	fg := t.TextMuted
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
		fg = t.Text
	case b.Hovered():
		b.Background(t.SurfaceHover)
		fg = t.Text
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(14, 14).TextColor(fg)
	})
	if b.Clicked() {
		onClick()
	}
}

func boardCalendarTitleButton(c *ui.Context, key, text, tip string, onClick func()) {
	t := c.Theme()
	b := ui.ButtonBase(c.Key(key)).Label(text).Tooltip(tip).
		Padding(3, 6).Radius(6).Center().
		Cursor(ui.CursorPointer).Transition(colorMotion())
	fg := t.Text
	switch {
	case b.Pressed():
		b.Background(t.SurfacePressed)
		fg = t.Accent
	case b.Hovered():
		b.Background(t.SurfaceHover)
		fg = t.Accent
	}
	b.Children(func() {
		ui.Text(c, text).FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(fg).SingleLine()
	})
	if b.Clicked() {
		onClick()
	}
}

func boardCalendarHeader(c *ui.Context, key, locale string, view time.Time, calMode *int, pickerDate *time.Time) {
	t := c.Theme()
	prevTip, nextTip := tr("上个月"), tr("下个月")
	step := func(delta int) {
		switch *calMode {
		case boardCalModeMonth:
			*pickerDate = view.AddDate(delta, 0, 0)
		case boardCalModeYear:
			*pickerDate = view.AddDate(delta*10, 0, 0)
		default:
			*pickerDate = view.AddDate(0, delta, 0)
		}
	}
	switch *calMode {
	case boardCalModeMonth:
		prevTip, nextTip = tr("上一年"), tr("下一年")
	case boardCalModeYear:
		prevTip, nextTip = tr("上一年代"), tr("下一年代")
	}

	ui.Row(c).FillWidth().Padding(10, 12, 4, 12).Gap(4).AlignItems(ui.Center).Children(func() {
		boardCalendarNavButton(c, key+"-prev", prevTip, iconChevronLeft, func() { step(-1) })

		ui.Row(c).Grow(1).MinWidth(0).Gap(2).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			yearLabel := strconv.Itoa(view.Year())
			if !englishUI.Load() {
				yearLabel = fmt.Sprintf("%d年", view.Year())
			}
			monthWidth := i18n.Abbreviated
			if englishUI.Load() {
				monthWidth = i18n.Wide
			}
			monthNames := i18n.Months(locale, monthWidth, true)
			monthLabel := monthNames[view.Month()-1]

			switch *calMode {
			case boardCalModeDate:
				if englishUI.Load() {
					boardCalendarTitleButton(c, key+"-mode-month", monthLabel, tr("选择月份"), func() { *calMode = boardCalModeMonth })
					boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
				} else {
					boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
					boardCalendarTitleButton(c, key+"-mode-month", monthLabel, tr("选择月份"), func() { *calMode = boardCalModeMonth })
				}
			case boardCalModeMonth:
				boardCalendarTitleButton(c, key+"-mode-year", yearLabel, tr("选择年份"), func() { *calMode = boardCalModeYear })
			case boardCalModeYear:
				decadeStart := (view.Year() / 10) * 10
				ui.Text(c, fmt.Sprintf("%d - %d", decadeStart, decadeStart+9)).
					FontSize(13.5).FontWeight(600).FontFeatures("tnum").TextColor(t.Text).Padding(3, 6).SingleLine()
			}
		})

		boardCalendarNavButton(c, key+"-next", nextTip, iconChevronRight, func() { step(1) })
	})
}

func boardCalendarDayGrid(c *ui.Context, key, locale string, firstWeekday time.Weekday, view, parsed time.Time, hasDue bool, now time.Time, pickDate func(time.Time)) {
	t := c.Theme()
	weekdays := i18n.Weekdays(locale, i18n.Short, true)
	ui.Row(c).FillWidth().Children(func() {
		for i := range 7 {
			name := weekdays[(int(firstWeekday)+i)%7]
			ui.Text(c, name).Width(36).Height(24).TextAlign(ui.Center).
				FontSize(11.5).FontWeight(500).TextColor(t.TextMuted.Alpha(0.78)).
				LineHeight(24.0 / 11.5).SingleLine()
		}
	})

	startOffset := (int(view.Weekday()) - int(firstWeekday) + 7) % 7
	gridStart := view.AddDate(0, 0, -startOffset)
	for w := range 6 {
		ui.Row(c.Key(fmt.Sprintf("%s-w-%d", key, w))).FillWidth().Children(func() {
			for d := range 7 {
				day := gridStart.AddDate(0, 0, w*7+d)
				inMonth := day.Month() == view.Month() && day.Year() == view.Year()
				isToday := sameBoardDay(day, now)
				isSelected := hasDue && sameBoardDay(day, parsed)

				dayLabel := i18n.FormatDate(locale, day, i18n.DateOptions{Skeleton: "yMMMMd"})
				ui.Box(c).Size(36, 32).Center().Children(func() {
					cell := ui.ButtonBase(c.Key(fmt.Sprintf("%s-d-%d-%d", key, w, d))).
						Label(dayLabel).
						Size(30, 30).Radius(15).Center().
						Cursor(ui.CursorPointer).Transition(colorMotion())

					fg := t.Text
					weight := 400
					switch {
					case isSelected:
						fg = t.AccentText
						weight = 600
						if cell.Pressed() && t.AccentPressed != ui.Transparent {
							cell.Background(t.AccentPressed)
						} else if cell.Hovered() && t.AccentHover != ui.Transparent {
							cell.Background(t.AccentHover)
						} else {
							cell.Background(t.Accent)
						}
					case isToday:
						fg = t.Accent
						weight = 600
						switch {
						case cell.Pressed():
							cell.Background(t.Accent.Alpha(0.22))
						case cell.Hovered():
							cell.Background(t.Accent.Alpha(0.16))
						default:
							cell.Background(t.Accent.Alpha(0.10))
						}
					case inMonth:
						switch {
						case cell.Pressed():
							cell.Background(t.SurfacePressed)
						case cell.Hovered():
							cell.Background(t.SurfaceHover)
						}
					default:
						fg = t.TextMuted.Alpha(0.38)
						switch {
						case cell.Pressed():
							cell.Background(t.SurfacePressed.Alpha(0.6))
							fg = t.TextMuted
						case cell.Hovered():
							cell.Background(t.SurfaceHover.Alpha(0.6))
							fg = t.TextMuted
						}
					}

					cell.Children(func() {
						ui.Text(c, strconv.Itoa(day.Day())).
							FontSize(12.5).FontWeight(weight).FontFeatures("tnum").
							TextColor(fg).SingleLine()
						if isToday && !isSelected {
							ui.Box(c).Absolute().Bottom(3).Size(3, 3).Radius(1.5).Background(t.Accent)
						}
					})
					if cell.Clicked() {
						pickDate(day)
					}
				})
			}
		})
	}
}

func boardCalendarSubGrid(c *ui.Context, key string, count int, cellAt func(i int) (label string, inScope, selected bool, onPick func())) {
	t := c.Theme()
	for r := range (count + 2) / 3 {
		ui.Row(c.Key(fmt.Sprintf("%s-r-%d", key, r))).FillWidth().Children(func() {
			for col := range 3 {
				i := r*3 + col
				if i >= count {
					break
				}
				label, inScope, selected, onPick := cellAt(i)
				ui.Box(c).Size(84, 42).Center().Children(func() {
					b := ui.ButtonBase(c.Key(fmt.Sprintf("%s-c-%d", key, i))).
						Label(label).Size(76, 32).Radius(16).Center().
						Cursor(ui.CursorPointer).Transition(colorMotion())
					fg := t.Text
					weight := 400
					if !inScope {
						fg = t.TextMuted.Alpha(0.45)
					}
					switch {
					case selected:
						fg = t.AccentText
						weight = 600
						if b.Hovered() && t.AccentHover != ui.Transparent {
							b.Background(t.AccentHover)
						} else {
							b.Background(t.Accent)
						}
					case b.Pressed():
						b.Background(t.SurfacePressed)
					case b.Hovered():
						b.Background(t.SurfaceHover)
						if !inScope {
							fg = t.TextMuted
						}
					}
					b.Children(func() {
						ui.Text(c, label).FontSize(13).FontWeight(weight).FontFeatures("tnum").TextColor(fg).SingleLine()
					})
					if b.Clicked() {
						onPick()
					}
				})
			}
		})
	}
}

func boardCalendarMonthGrid(c *ui.Context, key, locale string, view, parsed time.Time, hasDue bool, calMode *int, pickerDate *time.Time) {
	months := i18n.Months(locale, i18n.Abbreviated, true)
	boardCalendarSubGrid(c, key+"-months", 12, func(i int) (string, bool, bool, func()) {
		m := time.Month(i + 1)
		selected := (hasDue && parsed.Year() == view.Year() && parsed.Month() == m) ||
			(!hasDue && view.Month() == m)
		return months[i], true, selected, func() {
			*pickerDate = time.Date(view.Year(), m, 1, 0, 0, 0, 0, time.Local)
			*calMode = boardCalModeDate
		}
	})
}

func boardCalendarYearGrid(c *ui.Context, key string, view, parsed time.Time, hasDue bool, calMode *int, pickerDate *time.Time) {
	start := (view.Year()/10)*10 - 1
	boardCalendarSubGrid(c, key+"-years", 12, func(i int) (string, bool, bool, func()) {
		y := start + i
		inDecade := i > 0 && i < 11
		selected := (hasDue && parsed.Year() == y) || (!hasDue && view.Year() == y)
		return strconv.Itoa(y), inDecade, selected, func() {
			*pickerDate = time.Date(y, view.Month(), 1, 0, 0, 0, 0, time.Local)
			*calMode = boardCalModeDate
		}
	})
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
}

var boardSpring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: 0.12}}

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
	cols, total := a.boardFilteredByStatus()
	hasFilter := a.board.repoFilter != "" || strings.TrimSpace(a.board.search) != ""
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
			a.boardStatusFocusBar(c, cols)
			ui.Box(c.Key("board-body")).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
				a.boardContextWall(c, cols, hasFilter)
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

	ui.Row(c.Key("board-focus-bar")).FillWidth().Padding(14, 20, 4, 20).Gap(12).
		AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Row(c.Key("board-focus-track")).Padding(3).Gap(2).Radius(16).
			Background(trackBg).Border(1, t.Border.Alpha(0.5)).AlignItems(ui.Center).Shrink(0).Children(func() {
			focusPill := func(id, label string, st boardStatus, count int) {
				active := a.board.statusFilter == st
				btn := ui.ButtonBase(c.Key("board-focus-"+id)).
					Label(tr(label)).
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

			focusPill("all", "全部未完成", "", activeTotal)
			focusPill("review", "待审阅", boardReview, reviewCount)
			focusPill("doing", "进行中", boardDoing, doingCount)
			focusPill("todo", "待办", boardTodo, todoCount)
		})

		ui.Box(c).Grow(1).MinWidth(8)

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
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardAddTaskWithOptions(boardAddTaskOptions{
			Title:  title,
			Status: status,
			Repos:  repos,
		})
		return bf, err
	}, func(err error) {
		a.board.quickSaving = false
		if err != nil {
			a.board.quickTitle = title
			a.setBanner(err.Error(), "")
		}
	})
}

func boardWallAttentionRank(s boardStatus) int {
	switch s {
	case boardReview:
		return 0
	case boardDoing:
		return 1
	case boardTodo:
		return 2
	default:
		return 3
	}
}

type boardSortMode string

const (
	boardSortDefault  boardSortMode = ""
	boardSortRecent   boardSortMode = "recent"
	boardSortPriority boardSortMode = "priority"
	boardSortDue      boardSortMode = "due"
)

var boardSortModes = []boardSortMode{
	boardSortDefault,
	boardSortRecent,
	boardSortPriority,
	boardSortDue,
}

func parseBoardSortMode(raw string) boardSortMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(boardSortRecent):
		return boardSortRecent
	case string(boardSortPriority):
		return boardSortPriority
	case string(boardSortDue):
		return boardSortDue
	default:
		return boardSortDefault
	}
}

func boardSortLabel(mode boardSortMode) string {
	switch mode {
	case boardSortRecent:
		return "最近活跃"
	case boardSortPriority:
		return "重要紧急"
	case boardSortDue:
		return "截止日期"
	default:
		return "默认排序"
	}
}

func boardTaskActivityTime(t boardTask) time.Time {
	latest := t.UpdatedAt
	if t.CreatedAt.After(latest) {
		latest = t.CreatedAt
	}
	for _, n := range t.Notes {
		if ts := n.displayTime(); ts.After(latest) {
			latest = ts
		}
	}
	for _, cm := range t.Commits {
		if cm.AddedAt.After(latest) {
			latest = cm.AddedAt
		}
	}
	return latest
}

func boardPrioritySortRank(p boardPriority) int {
	switch p {
	case boardPriorityUrgentImportant:
		return 0
	case boardPriorityImportant:
		return 1
	case boardPriorityUrgent:
		return 2
	case boardPriorityLow:
		return 3
	default:
		return 4
	}
}

func boardDueSortKey(raw string) (time.Time, bool) {
	norm, ok := parseBoardDueDate(raw)
	if !ok || norm == "" {
		return time.Time{}, false
	}
	parsed, err := time.ParseInLocation("2006-01-02", norm, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func cmpBoardTimeDesc(a, b time.Time) int {
	switch {
	case a.After(b):
		return -1
	case b.After(a):
		return 1
	default:
		return 0
	}
}

func sortBoardWallTasks(tasks []boardTask) {
	sortBoardWallTasksByMode(tasks, boardSortDefault)
}

func sortBoardWallTasksByMode(tasks []boardTask, mode boardSortMode) {
	slices.SortStableFunc(tasks, func(a, b boardTask) int {
		if a.Pinned != b.Pinned {
			if a.Pinned {
				return -1
			}
			return 1
		}
		switch mode {
		case boardSortRecent:
			if c := cmpBoardTimeDesc(boardTaskActivityTime(a), boardTaskActivityTime(b)); c != 0 {
				return c
			}
		case boardSortPriority:
			pa, pb := boardPrioritySortRank(a.Priority), boardPrioritySortRank(b.Priority)
			if pa != pb {
				if pa < pb {
					return -1
				}
				return 1
			}
			if ra, rb := boardWallAttentionRank(a.Status), boardWallAttentionRank(b.Status); ra != rb {
				if ra < rb {
					return -1
				}
				return 1
			}
			return cmpBoardTimeDesc(boardTaskActivityTime(a), boardTaskActivityTime(b))
		case boardSortDue:
			da, okA := boardDueSortKey(a.DueDate)
			db, okB := boardDueSortKey(b.DueDate)
			if okA != okB {
				if okA {
					return -1
				}
				return 1
			}
			if okA && !da.Equal(db) {
				if da.Before(db) {
					return -1
				}
				return 1
			}
			if ra, rb := boardWallAttentionRank(a.Status), boardWallAttentionRank(b.Status); ra != rb {
				if ra < rb {
					return -1
				}
				return 1
			}
			return cmpBoardTimeDesc(boardTaskActivityTime(a), boardTaskActivityTime(b))
		}
		ra, rb := boardWallAttentionRank(a.Status), boardWallAttentionRank(b.Status)
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		return 0
	})
}

func boardMasonryColumnCount(availW float32) int {
	switch {
	case availW >= 1220:
		return 4
	case availW >= 880:
		return 3
	case availW >= 560:
		return 2
	default:
		return 1
	}
}

func estimateBoardCardHeight(task boardTask) int {
	h := 96
	if len([]rune(task.Title)) > 24 {
		h += 22
	}
	_, _, hasDue := boardTaskDueInfo(task.DueDate, task.Status == boardDone || task.Archived, time.Now())
	if task.Priority != boardPriorityNone || hasDue {
		h += 28
	}
	if items := parseBoardChecklist(task.Description); len(items) > 0 {
		shown := min(len(items), 5)
		h += 18 + shown*24 + 8
		if len(items) > 5 {
			h += 18
		}
	} else if strings.TrimSpace(task.Description) != "" {
		h += 48
	}
	if n := len(task.Commits); n > 0 {
		h += min(n, 2)*30 + 10
	}
	if len(task.Notes) > 0 {
		h += 58
	}
	return h
}

func distributeBoardMasonry(tasks []boardTask, numCols int) [][]boardTask {
	if numCols < 1 {
		numCols = 1
	}
	columns := make([][]boardTask, numCols)
	heights := make([]int, numCols)
	for _, task := range tasks {
		best := 0
		for i := 1; i < numCols; i++ {
			if heights[i] < heights[best] {
				best = i
			}
		}
		columns[best] = append(columns[best], task)
		heights[best] += estimateBoardCardHeight(task) + 14
	}
	return columns
}

func (a *app) boardContextWall(c *ui.Context, cols [][]boardTask, hasFilter bool) {
	t := c.Theme()
	var unfinished []boardTask
	if a.board.statusFilter != "" {
		unfinished = slices.Clone(cols[boardStatusIndex(a.board.statusFilter)])
	} else {
		for _, st := range []boardStatus{boardReview, boardDoing, boardTodo} {
			unfinished = append(unfinished, cols[boardStatusIndex(st)]...)
		}
	}
	sortBoardWallTasksByMode(unfinished, a.board.sortMode)
	doneTasks := cols[boardStatusIndex(boardDone)]

	var pinned, regular []boardTask
	for _, task := range unfinished {
		if task.Pinned && task.Status != boardDone {
			pinned = append(pinned, task)
		} else {
			regular = append(regular, task)
		}
	}

	availW, _ := c.Size()
	if a.showRepoSidebar() {
		availW -= a.repoSplit + 1
	}
	numCols := boardMasonryColumnCount(availW)
	masonryCols := distributeBoardMasonry(regular, numCols)

	ui.Scroll(c.Key("board-wall-scroll")).Fill().MinWidth(0).MinHeight(0).Children(func() {
		ui.Column(c.Key("board-wall-content")).FillWidth().Padding(12, 20, 28, 20).Gap(20).Children(func() {
			if len(pinned) > 0 {
				ui.Column(c.Key("board-hero-section")).FillWidth().Gap(12).Children(func() {
					if availW >= 740 {
						if len(pinned) == 1 {
							ui.Row(c.Key("board-hero-row-0")).FillWidth().Children(func() {
								ui.Box(c).Grow(1).MinWidth(0).MaxWidth(640).Children(func() {
									a.boardCard(c, pinned[0], true)
								})
							})
						} else {
							for i := 0; i < len(pinned); i += 2 {
								rowIdx := i / 2
								ui.Row(c.Key(fmt.Sprintf("board-hero-row-%d", rowIdx))).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
									ui.Box(c).Grow(1).MinWidth(0).Children(func() {
										a.boardCard(c, pinned[i], true)
									})
									if i+1 < len(pinned) {
										ui.Box(c).Grow(1).MinWidth(0).Children(func() {
											a.boardCard(c, pinned[i+1], true)
										})
									} else {
										ui.Box(c).Grow(1).MinWidth(0)
									}
								})
							}
						}
					} else {
						for _, task := range pinned {
							a.boardCard(c, task, true)
						}
					}
				})
			}

			if len(regular) > 0 {
				ui.Row(c.Key("board-masonry")).FillWidth().Gap(16).AlignItems(ui.Start).Children(func() {
					for colIdx := range numCols {
						colTasks := masonryCols[colIdx]
						ui.Column(c.Key(fmt.Sprintf("board-masonry-col-%d", colIdx))).
							Grow(1).MinWidth(0).Gap(16).Children(func() {
							for _, task := range colTasks {
								a.boardCard(c, task, false)
							}
						})
					}
				})
			} else if len(pinned) == 0 && !hasFilter {
				emptyMsg := tr("暂无任务")
				switch {
				case a.board.statusFilter != "":
					emptyMsg = tr("当前状态没有任务")
				case len(doneTasks) > 0:
					emptyMsg = tr("所有任务已完成")
				}
				ui.Row(c.Key("board-wall-empty")).FillWidth().Padding(36, 16).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
					ui.Text(c, emptyMsg).FontSize(13.5).TextColor(t.TextMuted)
				})
			}

			a.boardDoneSection(c, doneTasks)
		})
	})
}

func (a *app) boardDoneSection(c *ui.Context, doneTasks []boardTask) {
	t := c.Theme()
	doneSec := ui.Column(c.Key("board-done-section")).FillWidth().Gap(8).
		Padding(14, 0, 0, 0).
		BorderWidth(1, 0, 0, 0).BorderColor(t.Border.Alpha(0.55))
	doneSec.Children(func() {
		doneHeader := ui.Row(c.Key("board-done-header")).FillWidth().Gap(8).Padding(4, 6).Radius(8).
			AlignItems(ui.Center).Transition(colorMotion())
		if dragged, over := ui.DragOver[boardTaskDrag](doneHeader); over && dragged.status != boardDone {
			doneHeader.Background(t.Success.Alpha(0.12)).Border(1, t.Success.Alpha(0.55))
		} else {
			doneHeader.Border(1, ui.Transparent)
		}
		if dropped, ok := ui.Drop[boardTaskDrag](doneHeader); ok && dropped.status != boardDone {
			next := boardDone
			a.applyBoardTaskPatch(dropped.taskID, boardTaskPatch{Status: &next})
		}
		doneHeader.Children(func() {
			chev := iconChevronRight
			if a.board.doneExpanded {
				chev = iconChevronDown
			}
			toggleBtn := ui.ButtonBase(c.Key("board-done-toggle")).
				Label(tr("已完成")).
				Height(28).Padding(0, 8).Gap(6).Radius(6).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).
				Transition(colorMotion())
			switch {
			case toggleBtn.Pressed():
				toggleBtn.Background(t.SurfacePressed)
			case toggleBtn.Hovered():
				toggleBtn.Background(t.SurfaceHover)
			}
			toggleBtn.Children(func() {
				ui.Icon(c, chev).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("已完成")).FontSize(13).FontWeight(600).TextColor(t.TextMuted).SingleLine()
				ui.Text(c, fmt.Sprintf("%d", len(doneTasks))).FontSize(12).FontFeatures("tnum").
					TextColor(t.TextMuted.Alpha(0.8)).SingleLine()
			})
			if toggleBtn.Clicked() {
				a.board.doneExpanded = !a.board.doneExpanded
			}

			ui.Box(c).Grow(1)

			if a.board.doneExpanded && len(doneTasks) > 0 {
				archBtn := ui.ButtonBase(c.Key("board-done-archive")).
					Label(tr("归档已完成任务")).
					Height(26).Padding(0, 10).Gap(5).Radius(6).
					AlignItems(ui.Center).Cursor(ui.CursorPointer).
					Transition(colorMotion())
				switch {
				case archBtn.Pressed():
					archBtn.Background(t.SurfacePressed)
				case archBtn.Hovered():
					archBtn.Background(t.SurfaceHover)
				}
				archBtn.Children(func() {
					ui.Icon(c, iconArchive).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, tr("归档已完成任务")).FontSize(12).TextColor(t.TextMuted).SingleLine()
				})
				if archBtn.Clicked() {
					var count int
					a.changeBoard(func() (boardFile, error) {
						bf, n, err := boardArchiveDone()
						count = n
						return bf, err
					}, func(err error) {
						if err != nil {
							a.setBanner(err.Error(), "")
						} else if count > 0 {
							a.pendingToast = fmt.Sprintf(tr("已归档 %d 个任务"), count)
						}
					})
				}
			}
		})

		if !a.board.doneExpanded {
			return
		}
		if len(doneTasks) == 0 {
			ui.Text(c, tr("暂无已完成任务")).FontSize(12.5).TextColor(t.TextMuted.Alpha(0.8)).Padding(4, 10)
			return
		}
		ui.Column(c.Key("board-done-list")).FillWidth().Gap(4).Children(func() {
			for _, task := range doneTasks {
				a.boardDoneRow(c, task)
			}
		})
	})
}

func (a *app) boardDoneRow(c *ui.Context, task boardTask) {
	t := c.Theme()
	selected := a.board.selectedID == task.ID
	row := ui.ButtonBase(c.Key("board-done-row-"+task.ID)).
		Label(task.ID+" "+task.Title).
		FillWidth().Padding(7, 10).Gap(8).Radius(7).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).
		Transition(colorMotion())

	switch {
	case selected:
		row.Background(t.Accent.Alpha(0.10)).Border(1, t.Accent.Alpha(0.55))
	case row.Pressed():
		row.Background(t.SurfacePressed).Border(1, t.Border.Alpha(0.5))
	case row.Hovered():
		row.Background(t.SurfaceHover).Border(1, t.Border.Alpha(0.45))
	default:
		row.Background(t.Surface.Alpha(0.45)).Border(1, t.Border.Alpha(0.3))
	}

	restoreClicked := false
	row.Children(func() {
		ui.Icon(c, iconCheckCircle2).Size(14, 14).TextColor(t.Success.Alpha(0.75)).Shrink(0)
		ui.Text(c, task.ID).Font("monospace").FontSize(11.5).TextColor(t.TextMuted.Alpha(0.85)).SingleLine().Shrink(0)
		ui.Text(c, task.Title).FontSize(13).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
		if task.Archived {
			ui.Text(c, tr("已归档")).FontSize(11.5).TextColor(t.TextMuted.Alpha(0.75)).SingleLine().Shrink(0)
		}
		restoreBtn := ui.ButtonBase(c.Key("board-done-restore-"+task.ID)).
			Label(tr("移回待办")).Tooltip(tr("移回待办")).
			Size(22, 22).Radius(5).Center().Shrink(0).
			Cursor(ui.CursorPointer).Transition(colorMotion())
		restoreFg := t.TextMuted.Alpha(0.75)
		switch {
		case restoreBtn.Pressed():
			restoreBtn.Background(t.SurfacePressed)
			restoreFg = t.Text
		case restoreBtn.Hovered():
			restoreBtn.Background(t.SurfaceHover)
			restoreFg = t.Accent
		}
		restoreBtn.Children(func() {
			ui.Icon(c, iconRotateCcw).Size(13, 13).TextColor(restoreFg)
		})
		if restoreBtn.Clicked() {
			restoreClicked = true
		}
	})

	if restoreClicked {
		next := boardTodo
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &next})
		return
	}
	if row.Clicked() {
		a.selectBoardTask(task.ID)
	}
}

func drawBoardDropLine(p *ui.Painter, r ui.Rect, after bool, accent ui.Color) {
	y := r.Y + 2
	if after {
		y = r.Y + r.H - 2
	}
	p.Fill(ui.Rect{X: r.X, Y: y - 4, W: 8, H: 8}, accent, 4)
	p.Fill(ui.Rect{X: r.X + 4, Y: y - 1, W: r.W - 4, H: 2}, accent, 1)
}

func formatBoardRelativeTime(ts time.Time) string {
	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return tr("刚刚")
	case d < time.Hour:
		return fmt.Sprintf(tr("%d 分钟前"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(tr("%d 小时前"), int(d.Hours()))
	default:
		return ts.Local().Format("01-02 15:04")
	}
}

func (a *app) boardRepoActiveBranch(repo string) string {
	norm := normalizeBoardRepo(repo)
	if norm == "" {
		return ""
	}
	for _, tab := range a.tabs {
		if normalizeBoardRepo(tab.path) == norm {
			return tab.branch
		}
	}
	return ""
}

func (a *app) boardCard(c *ui.Context, task boardTask, hero bool) {
	t := c.Theme()
	selected := a.board.selectedID == task.ID
	isFocus := task.Pinned && task.Status != boardDone
	stCol := boardStatusColor(c, task.Status)

	recentPulse := false
	if ts, ok := a.board.recentUpdated[task.ID]; ok && time.Since(ts) < 12*time.Second {
		recentPulse = true
	}

	radius := float32(14)
	if hero {
		radius = 16
	}

	primaryText := t.Text
	mutedText := t.TextMuted
	accentOnCard := t.Accent
	chipBg := t.Surface.Alpha(0.85)
	chipHoverBg := t.SurfaceHover
	chipBorder := t.Border.Alpha(0.6)
	dividerCol := t.Border.Alpha(0.55)
	actionHoverBg := t.SurfaceHover
	actionPillBg := t.Surface.Alpha(0.88)

	if isFocus {
		accentOnCard = ui.Hex("#60A5FA")
		if t.Dark {
			primaryText = ui.Hex("#F5F6FA")
			mutedText = ui.Hex("#A5AAB8")
		} else {
			primaryText = ui.Hex("#F4F5F7")
			mutedText = ui.Hex("#A1A6B3")
		}
		chipBg = ui.RGBA(255, 255, 255, 0.08)
		chipHoverBg = ui.RGBA(255, 255, 255, 0.14)
		chipBorder = ui.RGBA(255, 255, 255, 0.14)
		dividerCol = ui.RGBA(255, 255, 255, 0.14)
		actionHoverBg = ui.RGBA(255, 255, 255, 0.14)
		actionPillBg = ui.RGBA(255, 255, 255, 0.09)
	}

	card := ui.ButtonBase(c.Key("board-card-"+task.ID)).
		Label(task.ID+" "+task.Title).
		FillWidth().
		Radius(radius).
		Cursor(ui.CursorPointer).
		Drag(boardTaskDrag{taskID: task.ID, status: task.Status}).
		Transition(boardCardMotion(task.ID))
	if hero {
		card.Padding(18, 20)
	} else {
		card.Padding(15, 16)
	}

	switch {
	case isFocus:
		if t.Dark {
			card.LinearGradient(ui.LinearGradient{From: ui.Hex("#252A3A"), To: ui.Hex("#1A1D28"), Angle: 155, Oklab: true})
		} else {
			card.LinearGradient(ui.LinearGradient{From: ui.Hex("#222633"), To: ui.Hex("#171922"), Angle: 155, Oklab: true})
		}
		card.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.14))
	case t.Dark:
		card.LinearGradient(ui.LinearGradient{From: ui.Hex("#1D2028"), To: ui.Hex("#171920"), Angle: 180})
		card.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
	default:
		card.LinearGradient(ui.LinearGradient{From: ui.Hex("#FFFFFF"), To: ui.Hex("#FCFBF9"), Angle: 180})
		card.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.95))
	}

	card.Draw(func(p *ui.Painter, r ui.Rect) {
		if r.W <= 0 || r.H <= 0 {
			return
		}
		switch {
		case isFocus:
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.15, Y: 0.0},
				Radius: min(max(r.W*0.65, 160), 280),
				Oklab:  true,
				Stops: []ui.GradientStop{
					{Color: t.Warning.Alpha(0.14), At: 0},
					{Color: ui.Transparent, At: 1},
				},
			}, radius)
		case task.Status == boardReview || task.Status == boardDoing:
			glowAlpha := float32(0.065)
			if t.Dark {
				glowAlpha = 0.10
			}
			p.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.15, Y: 0.0},
				Radius: 180,
				Oklab:  true,
				Stops: []ui.GradientStop{
					{Color: stCol.Alpha(glowAlpha), At: 0},
					{Color: ui.Transparent, At: 1},
				},
			}, radius)
		}
	})

	hovered := card.Hovered()
	dragging := card.Dragging()
	liftTarget := float32(0)
	if hovered && !dragging {
		liftTarget = -2
	}
	if liftY := card.AnimateTo("hover-lift", liftTarget, boardSpring); liftY != 0 {
		card.Translate(0, liftY)
	}
	if dragging {
		card.Opacity(0.42).Cursor(ui.CursorGrabbing)
	}

	switch {
	case selected:
		card.Border(2, t.Accent).Shadow(0, 0, 0, 3, t.Accent.Alpha(0.20))
	case isFocus:
		if hovered {
			card.Border(1, t.Warning.Alpha(0.85)).
				Shadow(0, 4, 10, 0, ui.RGBA(15, 23, 42, 0.22)).
				Shadow(0, 16, 34, -4, ui.RGBA(15, 23, 42, 0.30))
		} else {
			card.Border(1, ui.RGBA(255, 255, 255, 0.14)).
				BorderLinearGradient(ui.LinearGradient{
					From:  t.Warning.Alpha(0.55),
					To:    ui.RGBA(255, 255, 255, 0.08),
					Angle: 165,
					Oklab: true,
				}).
				Shadow(0, 2, 6, 0, ui.RGBA(15, 23, 42, 0.18)).
				Shadow(0, 10, 24, -4, ui.RGBA(15, 23, 42, 0.22))
		}
	case hovered:
		if t.Dark {
			card.Border(1, t.Accent.Alpha(0.50)).
				Shadow(0, 4, 10, 0, ui.RGBA(0, 0, 0, 0.30)).
				Shadow(0, 14, 30, -4, ui.RGBA(0, 0, 0, 0.40))
		} else {
			card.Border(1, t.Accent.Alpha(0.38)).
				Shadow(0, 2, 4, 0, ui.RGBA(15, 23, 42, 0.06)).
				Shadow(0, 12, 28, -4, ui.RGBA(15, 23, 42, 0.11))
		}
	default:
		if t.Dark {
			card.Border(1, ui.RGBA(255, 255, 255, 0.08)).
				BorderLinearGradient(ui.LinearGradient{
					From:  ui.RGBA(255, 255, 255, 0.13),
					To:    ui.RGBA(255, 255, 255, 0.04),
					Angle: 180,
				}).
				Shadow(0, 2, 6, 0, ui.RGBA(0, 0, 0, 0.24)).
				Shadow(0, 8, 22, -4, ui.RGBA(0, 0, 0, 0.28))
		} else {
			card.Border(1, ui.RGBA(15, 23, 42, 0.085)).
				Shadow(0, 1, 2, 0, ui.RGBA(15, 23, 42, 0.05)).
				Shadow(0, 6, 18, -3, ui.RGBA(15, 23, 42, 0.06))
		}
	}
	if recentPulse && !selected {
		card.Shadow(0, 0, 12, 2, t.Accent.Alpha(0.24))
	}

	var moveStatus boardStatus
	hasMove := false
	doPinToggle := false
	doCopyCtx := false
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
		pinLabel := tr("置顶聚焦")
		if task.Pinned {
			pinLabel = tr("取消置顶")
		}
		if m.Item(pinLabel).Chosen() {
			doPinToggle = true
		}
		if m.Item(tr("复制 Agent 上下文")).Chosen() {
			doCopyCtx = true
		}
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

	copyClicked := false
	pinClicked := false
	checkClicked := false
	toggledLineIdx := -1
	var clickedCommit *boardCommitLink

	repos := boardTaskAllRepos(task)
	checklist := parseBoardChecklist(task.Description)

	card.Children(func() {
		ui.Column(c).FillWidth().Gap(9).Children(func() {
			// Card Header
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					if a.board.repoFilter == "" && len(repos) > 0 {
						boardRepoDot(c, repos[0], 7)
						repoText := repoTitle(repos[0])
						if len(repos) > 1 {
							repoText += fmt.Sprintf(" +%d", len(repos)-1)
						}
						ui.Text(c, repoText).FontSize(11.5).FontWeight(500).TextColor(mutedText).SingleLine().MinWidth(0)
					}
					ui.Text(c, task.ID).Font("monospace").FontSize(11.5).TextColor(mutedText.Alpha(0.85)).SingleLine().Shrink(0)
					if a.board.statusFilter == "" {
						pillBg := stCol.Alpha(0.13)
						pillFg := stCol
						if isFocus && task.Status == boardDoing {
							pillBg = accentOnCard.Alpha(0.22)
							pillFg = accentOnCard
						} else if task.Status == boardTodo {
							pillBg = mutedText.Alpha(0.14)
							pillFg = mutedText
						} else if isFocus {
							pillBg = stCol.Alpha(0.22)
						}
						ui.Row(c).Padding(1, 6).Gap(4).Radius(9).Background(pillBg).AlignItems(ui.Center).Shrink(0).Children(func() {
							ui.Box(c).Size(5, 5).Radius(2.5).Background(pillFg).Shrink(0)
							ui.Text(c, tr(boardStatusLabel(task.Status))).FontSize(11).FontWeight(500).TextColor(pillFg).SingleLine()
						})
					}
					if hero && len(repos) > 0 {
						if branch := a.boardRepoActiveBranch(repos[0]); branch != "" {
							ui.Row(c).Padding(1, 6).Gap(4).Radius(9).Background(chipBg).AlignItems(ui.Center).Shrink(0).Children(func() {
								ui.Icon(c, iconGitBranch).Size(10.5, 10.5).TextColor(mutedText).Shrink(0)
								ui.Text(c, branch).FontSize(11).TextColor(mutedText).SingleLine()
							})
						}
					}
				})

				// Quiet-at-rest floating action capsule: Copy Agent Context, Pin/Unpin, Mark Done
				actionRow := ui.Row(c.Key("board-card-actions-"+task.ID)).Gap(2).Padding(2).Radius(8).
					AlignItems(ui.Center).Shrink(0).Transition(colorMotion())
				var actionActive bool
				actionRow.Children(func() {
					copyBtn := ui.ButtonBase(c.Key("board-card-copy-ctx-"+task.ID)).
						Label(tr("复制 Agent 上下文")).Tooltip(tr("复制 Agent 上下文")).
						Size(22, 22).Radius(6).Center().Shrink(0).
						Cursor(ui.CursorPointer).Transition(colorMotion())
					if copyBtn.Clicked() {
						copyClicked = true
						copyBtn.Jump("copied", 1)
					}
					copied := copyBtn.AnimateTo("copied", 0, ui.Animation{Duration: 2 * time.Second}) > 0
					copyIcon := iconCopy
					copyFg := mutedText.Alpha(0.75)
					if copied {
						copyIcon = iconCheck
						copyFg = t.Success
						copyBtn.Background(t.Success.Alpha(0.16))
					} else if copyBtn.Hovered() || copyBtn.Pressed() {
						copyBtn.Background(actionHoverBg)
						copyFg = primaryText
					} else if hovered || selected {
						copyFg = mutedText
					}
					if copied || hovered || selected || copyBtn.Hovered() {
						copyBtn.Opacity(1)
					} else {
						copyBtn.Opacity(0.22)
					}
					copyBtn.Children(func() {
						ui.Icon(c, copyIcon).Size(12.5, 12.5).TextColor(copyFg)
					})

					pinTip := tr("置顶聚焦")
					pinIcon := iconStar
					pinFg := mutedText.Alpha(0.75)
					if task.Pinned {
						pinTip = tr("取消置顶")
						pinIcon = iconStarFilled
						pinFg = t.Warning
					}
					pinBtn := ui.ButtonBase(c.Key("board-card-pin-"+task.ID)).
						Label(pinTip).Tooltip(pinTip).
						Size(22, 22).Radius(6).Center().Shrink(0).
						Cursor(ui.CursorPointer).Transition(colorMotion())
					if pinBtn.Hovered() || pinBtn.Pressed() {
						pinBtn.Background(actionHoverBg)
						if !task.Pinned {
							pinFg = t.Warning
						}
					} else if !task.Pinned && (hovered || selected) {
						pinFg = mutedText
					}
					if task.Pinned || hovered || selected || pinBtn.Hovered() {
						pinBtn.Opacity(1)
					} else {
						pinBtn.Opacity(0.22)
					}
					pinBtn.Children(func() {
						ui.Icon(c, pinIcon).Size(13, 13).TextColor(pinFg)
					})
					if pinBtn.Clicked() {
						pinClicked = true
					}

					chkTip := tr("标记为已完成")
					chkBtn := ui.ButtonBase(c.Key("board-card-check-"+task.ID)).
						Label(chkTip).Tooltip(chkTip).
						Size(22, 22).Radius(11).Center().Shrink(0).
						Cursor(ui.CursorPointer).Transition(colorMotion())
					chkIcon := iconCircle
					chkColor := mutedText.Alpha(0.7)
					if chkBtn.Hovered() || chkBtn.Pressed() {
						chkIcon = iconCheckCircle2
						chkColor = t.Success
						chkBtn.Background(t.Success.Alpha(0.16))
					} else if hovered || selected {
						chkColor = mutedText
					}
					if hovered || selected || chkBtn.Hovered() {
						chkBtn.Opacity(1)
					} else {
						chkBtn.Opacity(0.22)
					}
					chkBtn.Children(func() {
						ui.Icon(c, chkIcon).Size(14, 14).TextColor(chkColor)
					})
					if chkBtn.Clicked() {
						checkClicked = true
					}

					actionActive = hovered || selected || copyBtn.Hovered() || pinBtn.Hovered() || chkBtn.Hovered()
				})
				if actionActive {
					actionRow.Background(actionPillBg)
				}
			})

			// Title
			titleSize := float32(14.5)
			titleWeight := 600
			titleLH := float32(1.38)
			if hero {
				titleSize = 16
				titleWeight = 700
				titleLH = 1.35
			}
			ui.Text(c, task.Title).FontSize(titleSize).FontWeight(titleWeight).LineHeight(titleLH).
				MaxLines(2).FillWidth().MinWidth(0).TextColor(primaryText)

			// Priority & Due chips
			dueParsed, dueState, hasDue := boardTaskDueInfo(task.DueDate, task.Status == boardDone || task.Archived, time.Now())
			if task.Priority != boardPriorityNone || hasDue || task.Archived {
				ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
					if task.Priority != boardPriorityNone {
						boardPriorityPill(c, "board-card-prio-"+task.ID, task.Priority, 11.5)
					}
					if hasDue {
						dueText, dueTip, dueFg, dueBg := boardCardDueAppearance(c, task.DueDate, dueParsed, dueState)
						dueChip := ui.Row(c.Key("board-card-due-"+task.ID)).Padding(2, 7).Gap(4).Radius(4).
							Background(dueBg).AlignItems(ui.Center).Shrink(0).Tooltip(dueTip)
						dueChip.Children(func() {
							ui.Icon(c, iconCalendar).Size(11, 11).TextColor(dueFg).Shrink(0)
							lbl := ui.Text(c, dueText).FontSize(11.5).TextColor(dueFg).FontFeatures("tnum").SingleLine()
							if dueState == boardDueOverdue || dueState == boardDueToday {
								lbl.FontWeight(600)
							} else {
								lbl.FontWeight(500)
							}
						})
					}
					if task.Archived {
						ui.Text(c, tr("已归档")).FontSize(11.5).TextColor(mutedText).SingleLine()
					}
				})
			}

			// Live Checklist with Progress Bar or Description Excerpt
			if len(checklist) > 0 {
				doneCount := 0
				for _, item := range checklist {
					if item.Checked {
						doneCount++
					}
				}
				pct := float32(doneCount) * 100 / float32(len(checklist))
				barCol := accentOnCard
				if doneCount == len(checklist) {
					barCol = t.Success
				}
				trackCol := t.Border.Alpha(0.5)
				if isFocus {
					trackCol = ui.RGBA(255, 255, 255, 0.14)
				}

				ui.Column(c.Key("board-card-checklist-"+task.ID)).FillWidth().Gap(3).Children(func() {
					ui.Row(c.Key("board-card-chk-prog-"+task.ID)).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 4, 2, 4).Children(func() {
						ui.Box(c).Grow(1).MinWidth(0).Height(3).Radius(1.5).Background(trackCol).Clip().Children(func() {
							ui.Box(c.Key("board-card-chk-fill-"+task.ID)).Height(3).WidthPercent(pct).Radius(1.5).
								Background(barCol).Transition(ui.ElementTransition{Animation: &boardSpring, Width: true, Colors: true})
						})
						ui.Text(c, fmt.Sprintf("%d/%d", doneCount, len(checklist))).FontSize(11).FontFeatures("tnum").
							TextColor(mutedText.Alpha(0.85)).SingleLine().Shrink(0)
					})
					limit := min(len(checklist), 5)
					for i := range limit {
						item := checklist[i]
						rowBtn := ui.ButtonBase(c.Key(fmt.Sprintf("board-card-chk-%s-%d", task.ID, item.LineIndex))).
							Label(item.Text).
							FillWidth().Padding(3, 5).Gap(6).Radius(5).
							AlignItems(ui.Center).Justify(ui.Start).
							Cursor(ui.CursorPointer).Transition(colorMotion())
						if rowBtn.Hovered() || rowBtn.Pressed() {
							rowBtn.Background(actionHoverBg)
						}
						rowBtn.Children(func() {
							if item.Checked {
								ui.Icon(c, iconCheckCircle2).Size(13.5, 13.5).TextColor(t.Success).Shrink(0)
								ui.Text(c, item.Text).FontSize(12.5).TextColor(mutedText.Alpha(0.68)).
									Strikethrough().SingleLine().Grow(1).MinWidth(0)
							} else {
								ui.Icon(c, iconCircle).Size(13.5, 13.5).TextColor(mutedText.Alpha(0.75)).Shrink(0)
								ui.Text(c, item.Text).FontSize(12.5).TextColor(primaryText.Alpha(0.92)).
									SingleLine().Grow(1).MinWidth(0)
							}
						})
						if rowBtn.Clicked() {
							toggledLineIdx = item.LineIndex
						}
					}
					if len(checklist) > 5 {
						ui.Text(c, fmt.Sprintf("+%d", len(checklist)-5)).FontSize(11.5).
							TextColor(mutedText.Alpha(0.8)).Padding(1, 5).SingleLine()
					}
				})
			} else if desc := strings.TrimSpace(task.Description); desc != "" {
				ui.Text(c, desc).FontSize(12.5).LineHeight(1.45).MaxLines(3).
					FillWidth().MinWidth(0).TextColor(mutedText)
			}

			// Embedded Recent Commits (latest 1–2)
			if len(task.Commits) > 0 {
				ui.Column(c.Key("board-card-commits-"+task.ID)).FillWidth().Gap(4).Children(func() {
					start := max(0, len(task.Commits)-2)
					for i := len(task.Commits) - 1; i >= start; i-- {
						link := task.Commits[i]
						short := link.Hash
						if len(short) > 7 {
							short = short[:7]
						}
						label := short
						if link.Subject != "" {
							label = short + " " + link.Subject
						}
						cBtn := ui.ButtonBase(c.Key(fmt.Sprintf("board-card-commit-%s-%s", task.ID, link.Hash))).
							Label(label).
							Tooltip(link.Repo + " @ " + link.Hash).
							FillWidth().Padding(5, 8).Gap(5).Radius(8).
							AlignItems(ui.Center).Justify(ui.Start).
							Cursor(ui.CursorPointer).Transition(colorMotion())
						if cBtn.Hovered() || cBtn.Pressed() {
							cBtn.Background(chipHoverBg).Border(1, t.Accent.Alpha(0.5))
						} else {
							cBtn.Background(chipBg).Border(1, chipBorder)
						}
						cBtn.Children(func() {
							ui.Icon(c, iconGitCommit).Size(12, 12).TextColor(mutedText).Shrink(0)
							ui.Text(c, short).Font("monospace").FontSize(11).TextColor(mutedText).SingleLine().Shrink(0)
							if link.Subject != "" {
								ui.Text(c, link.Subject).FontSize(11.5).TextColor(primaryText.Alpha(0.9)).SingleLine().Grow(1).MinWidth(0)
							} else {
								ui.Box(c).Grow(1)
							}
							if link.Agent != "" {
								ui.Row(c).Padding(1, 5).Radius(4).Background(accentOnCard.Alpha(0.14)).Shrink(0).Children(func() {
									ui.Text(c, link.Agent).FontSize(10.5).FontWeight(500).TextColor(accentOnCard).SingleLine()
								})
							}
						})
						if cBtn.Clicked() {
							copyLink := link
							clickedCommit = &copyLink
						}
					}
				})
			}

			// Latest Breakpoint Note
			if len(task.Notes) > 0 {
				latest := task.Notes[len(task.Notes)-1]
				isHuman := strings.EqualFold(latest.Author, "human") || latest.Author == ""
				who := latest.Author
				if isHuman {
					who = tr("人工")
				}
				ui.Column(c.Key("board-card-note-"+task.ID)).FillWidth().Gap(3).
					Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(dividerCol).Children(func() {
					ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
						if !isHuman {
							ui.Icon(c, iconBot).Size(11.5, 11.5).TextColor(accentOnCard).Shrink(0)
							ui.Text(c, who).FontSize(11.5).FontWeight(600).TextColor(accentOnCard).SingleLine()
						} else {
							ui.Text(c, who).FontSize(11.5).FontWeight(500).TextColor(mutedText).SingleLine()
						}
						if ts := latest.displayTime(); !ts.IsZero() {
							ui.Text(c, "· "+formatBoardRelativeTime(ts)).FontSize(11).
								Tooltip(ts.Local().Format("2006-01-02 15:04:05")).
								TextColor(mutedText.Alpha(0.8)).SingleLine().Shrink(0)
						}
						if recentPulse {
							ui.Box(c).Size(6, 6).Radius(3).Background(accentOnCard).Shrink(0)
						}
					})
					ui.Text(c, latest.Text).FontSize(12).LineHeight(1.4).MaxLines(2).
						FillWidth().MinWidth(0).TextColor(mutedText)
				})
			}
		})
	})

	if copyClicked || doCopyCtx {
		c.Services().WriteClipboard(formatBoardAgentContext(task))
		c.Toast(tr("已复制 Agent 上下文"))
		return
	}
	if pinClicked || doPinToggle {
		next := !task.Pinned
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Pinned: &next})
		return
	}
	if checkClicked {
		next := boardDone
		if task.Status == boardDone {
			next = boardTodo
		}
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &next})
		return
	}
	if toggledLineIdx >= 0 {
		nextDesc := toggleBoardChecklistLine(task.Description, toggledLineIdx)
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Description: &nextDesc})
		return
	}
	if clickedCommit != nil {
		a.openBoardCommit(*clickedCommit)
		return
	}
	if hasMove {
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &moveStatus})
		return
	}
	if doArchive || doUnarchive {
		v := doArchive
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Archived: &v})
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
func (a *app) applyBoardTaskPatch(id string, patch boardTaskPatch, toast ...string) {
	a.commitBoardDrafts()
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardUpdateTask(id, patch)
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
		} else if len(toast) > 0 {
			a.pendingToast = toast[0]
		}
	})
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

	stCol := boardStatusColor(c, task.Status)
	bandCol := stCol
	if task.Pinned && task.Status != boardDone {
		bandCol = t.Warning
	}
	h, _, _, _, _, _, _ := a.chromeMetrics(c)
	drawer := ui.Column(c.Key("board-detail-drawer")).
		Absolute().Top(h + 12).Bottom(12).Right(12).WidthPercent(50).ZIndex(20).
		Radius(16).Background(t.Background).Border(1, t.Border.Alpha(0.8)).Clip().
		Transition(boardDrawerMotion())
	if t.Dark {
		drawer.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.10)).
			Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.32)).
			Shadow(0, 18, 42, -6, ui.RGBA(0, 0, 0, 0.50))
	} else {
		drawer.InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.90)).
			Shadow(0, 4, 12, 0, ui.RGBA(15, 23, 42, 0.08)).
			Shadow(0, 18, 42, -6, ui.RGBA(15, 23, 42, 0.16))
	}
	drawer.Children(func() {
		// Top ambient drawer header
		ui.Row(c.Key("board-drawer-header")).FillWidth().Padding(11, 18).Gap(6).AlignItems(ui.Center).
			LinearGradient(ui.LinearGradient{From: bandCol.Alpha(0.09), To: ui.Transparent, Angle: 180, Oklab: true}).
			BorderWidth(0, 0, 1, 0).BorderColor(t.Border.Alpha(0.65)).Shrink(0).Children(func() {
			ui.Row(c.Key("board-drawer-id")).Padding(3, 8).Radius(5).Background(t.Surface).AlignItems(ui.Center).Shrink(0).Children(func() {
				ui.Text(c, task.ID).Font("monospace").FontSize(14).FontWeight(600).TextColor(t.TextMuted).SingleLine()
			})
			copyIconButton(c, "board-drawer-copy-id", tr("复制任务编号"), task.ID)
			if task.Archived {
				ui.Row(c.Key("board-drawer-archived-badge")).Padding(3, 8).Radius(5).Background(t.Surface).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Text(c, tr("已归档")).FontSize(12).TextColor(t.TextMuted).SingleLine()
				})
			}
			ui.Box(c).Grow(1)
			ctxBtn := ui.ButtonBase(c.Key("board-drawer-copy-ctx")).
				Label(tr("复制 Agent 上下文")).Tooltip(tr("复制 Agent 上下文")).
				Height(24).Padding(0, 8).Gap(4).Radius(6).
				AlignItems(ui.Center).Cursor(ui.CursorPointer).Shrink(0).
				Transition(colorMotion())
			switch {
			case ctxBtn.Pressed():
				ctxBtn.Background(t.SurfacePressed)
			case ctxBtn.Hovered():
				ctxBtn.Background(t.SurfaceHover)
			}
			ctxBtn.Children(func() {
				ui.Icon(c, iconCopy).Size(12.5, 12.5).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("复制上下文")).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
			if ctxBtn.Clicked() {
				c.Services().WriteClipboard(formatBoardAgentContext(task))
				c.Toast(tr("已复制 Agent 上下文"))
			}

			pinTip := tr("置顶聚焦")
			pinIcon := iconStar
			pinFg := t.TextMuted
			if task.Pinned {
				pinTip = tr("取消置顶")
				pinIcon = iconStarFilled
				pinFg = t.Warning
			}
			pinBtn := ui.ButtonBase(c.Key("board-drawer-pin")).Label(pinTip).Tooltip(pinTip)
			if styleActionIcon(c, pinBtn, pinIcon, 24, 14, pinFg, task.Pinned).Clicked() {
				next := !task.Pinned
				a.applyBoardTaskPatch(task.ID, boardTaskPatch{Pinned: &next})
			}

			moreBtn := paneIconButton(c, "board-drawer-more", tr("任务操作"), iconMore)
			moreBtn.Menu(func(m *ui.Menu) {
				if task.Archived {
					if m.Item(tr("取消归档")).Chosen() {
						next := false
						a.applyBoardTaskPatch(task.ID, boardTaskPatch{Archived: &next}, tr("已取消归档"))
					}
				} else {
					if m.Item(tr("归档任务")).Chosen() {
						next := true
						a.applyBoardTaskPatch(task.ID, boardTaskPatch{Archived: &next}, tr("已归档"))
					}
				}
				m.Separator()
				if m.Item(tr("删除任务")).Chosen() {
					a.board.deleteTaskID = task.ID
				}
			})
			if closeIconButton(c, "board-drawer-close", tr("关闭详情")).Clicked() {
				a.closeBoardDrawer()
				return
			}
		})

		ui.Scroll(c.Key("board-drawer-scroll")).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c.Key("board-drawer-content-" + task.ID)).FillWidth().Padding(16, 20, 28, 20).Gap(14).
				Transition(fadeEnter()).Children(func() {
				// Borderless document-style title input
				titleEl := ui.TextInputBase(c.Key("board-drawer-title"), &a.board.titleDraft).
					Label(tr("标题")).
					Placeholder(tr("输入任务标题")).
					Bind(&a.board.titleInput).
					FontSize(20).FontWeight(700).
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
				ui.Column(c.Key("board-drawer-meta")).FillWidth().Gap(8).Children(func() {
					// Status segmented control
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("状态")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						ui.Box(c).Grow(1).MinWidth(0).Children(func() {
							seg := boardStatusSegmented(c, "board-drawer-status-seg", &a.board.drawerStatusIdx, 14)
							if seg.Track.Changed() {
								target := boardStatuses[a.board.drawerStatusIdx]
								if target != task.Status {
									a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &target})
								}
							}
						})
					})

					// Priority row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("重要紧急")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						boardPriorityPicker(c, "board-drawer-priority", task.Priority, &a.board.drawerPriorityOpen, false, func(next boardPriority) {
							a.applyBoardTaskPatch(task.ID, boardTaskPatch{Priority: &next})
						})
					})

					// Due date row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tr("截止日期")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0)
						boardDueDatePicker(c, "board-drawer-due", task.DueDate, task.Status == boardDone || task.Archived, &a.board.drawerDueOpen, &a.board.drawerDuePicker, false, func(next string) {
							a.applyBoardTaskPatch(task.ID, boardTaskPatch{DueDate: &next})
						})
					})

					// Repos row
					ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, tr("关联项目")).Width(72).FontSize(14.5).TextColor(t.TextMuted).Shrink(0).Padding(4, 0, 0, 0)
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
											lbl := ui.Text(c, repoTitle(repoPath)).FontSize(14).SingleLine()
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
								a.applyBoardTaskPatch(task.ID, boardTaskPatch{Repos: &next})
							}

							addRepoBtn := ui.Button(c.Key("board-drawer-add-repo"), tr("+ 项目")).Padding(3, 9).FontSize(14)
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
										a.applyBoardTaskPatch(task.ID, boardTaskPatch{AddRepos: []string{repoPath}})
									}
								}
							})
						})
					})
				})

				ui.Box(c).FillWidth().Height(1).Background(t.Border.Alpha(0.6))

				// Description & interactive checklist
				ui.Column(c.Key("board-drawer-desc-sec")).FillWidth().Gap(8).Children(func() {
					ui.Text(c, tr("任务描述")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
					if drawerChecklist := parseBoardChecklist(task.Description); len(drawerChecklist) > 0 {
						doneCount := 0
						for _, item := range drawerChecklist {
							if item.Checked {
								doneCount++
							}
						}
						pct := float32(doneCount) * 100 / float32(len(drawerChecklist))
						barCol := t.Accent
						if doneCount == len(drawerChecklist) {
							barCol = t.Success
						}
						ui.Column(c.Key("board-drawer-checklist")).FillWidth().Padding(8, 10).Gap(4).
							Radius(10).Background(t.Surface.Alpha(0.5)).Border(1, t.Border.Alpha(0.6)).Children(func() {
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 4, 2, 4).Children(func() {
								ui.Box(c).Grow(1).MinWidth(0).Height(3).Radius(1.5).Background(t.Border.Alpha(0.5)).Clip().Children(func() {
									ui.Box(c.Key("board-drawer-chk-fill")).Height(3).WidthPercent(pct).Radius(1.5).
										Background(barCol).Transition(ui.ElementTransition{Animation: &boardSpring, Width: true, Colors: true})
								})
								ui.Text(c, fmt.Sprintf("%d/%d", doneCount, len(drawerChecklist))).FontSize(11.5).FontFeatures("tnum").
									TextColor(t.TextMuted).SingleLine().Shrink(0)
							})
							for _, item := range drawerChecklist {
								rowBtn := ui.ButtonBase(c.Key(fmt.Sprintf("board-drawer-chk-%s-%d", task.ID, item.LineIndex))).
									Label(item.Text).
									FillWidth().Padding(4, 6).Gap(7).Radius(6).
									AlignItems(ui.Center).Justify(ui.Start).
									Cursor(ui.CursorPointer).Transition(colorMotion())
								if rowBtn.Hovered() || rowBtn.Pressed() {
									rowBtn.Background(t.SurfaceHover)
								}
								rowBtn.Children(func() {
									if item.Checked {
										ui.Icon(c, iconCheckCircle2).Size(14, 14).TextColor(t.Success).Shrink(0)
										ui.Text(c, item.Text).FontSize(13.5).TextColor(t.TextMuted).Strikethrough().Grow(1).MinWidth(0)
									} else {
										ui.Icon(c, iconCircle).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
										ui.Text(c, item.Text).FontSize(13.5).TextColor(t.Text).Grow(1).MinWidth(0)
									}
								})
								if rowBtn.Clicked() {
									baseDesc := a.board.descDraft
									if baseDesc == "" {
										baseDesc = task.Description
									}
									nextDesc := toggleBoardChecklistLine(baseDesc, item.LineIndex)
									a.board.descDraft = nextDesc
									a.applyBoardTaskPatch(task.ID, boardTaskPatch{Description: &nextDesc})
								}
							}
						})
					}
					descEl := ui.TextAreaBase(c.Key("board-drawer-desc"), &a.board.descDraft).
						Label(tr("任务描述")).
						Placeholder(tr("补充任务背景、目标或验收标准…")).
						Bind(&a.board.descInput).
						Height(96).FillWidth().Padding(9, 11).Radius(8).
						FontSize(15).LineHeight(1.4).FocusRing(false).Transition(colorMotion())
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
						ui.Text(c, tr("关联提交")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Commits) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Commits))).FontSize(14).TextColor(t.TextMuted)
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
						ui.Text(c, tr("备注")).FontSize(14.5).FontWeight(600).TextColor(t.TextMuted)
						if len(task.Notes) > 0 {
							ui.Text(c, fmt.Sprintf("%d", len(task.Notes))).FontSize(14).TextColor(t.TextMuted)
						}
					})
					if len(task.Notes) > 0 {
						ui.Column(c.Key("board-drawer-notes-list")).FillWidth().Gap(0).Children(func() {
							for idx, n := range task.Notes {
								a.boardNoteCard(c, task, idx, n)
							}
						})
					}

					submitNote := func() {
						if strings.TrimSpace(a.board.noteDraft) == "" {
							return
						}
						text := a.board.noteDraft
						a.forgetBoardTaskDraft(task.ID)
						a.changeBoard(func() (boardFile, error) {
							bf, _, err := boardAddNote(task.ID, text, "human", "")
							return bf, err
						}, func(err error) {
							if err == nil {
								return
							}
							a.setBanner(err.Error(), "")
							if a.board.selectedID == task.ID && a.board.noteDraft == "" {
								a.board.noteDraft = text
							} else if a.board.noteDrafts[task.ID] == "" {
								if a.board.noteDrafts == nil {
									a.board.noteDrafts = map[string]string{}
								}
								a.board.noteDrafts[task.ID] = text
							}
						})
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
							Height(60).FillWidth().FontSize(15).LineHeight(1.4).FocusRing(false)
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
							shortcut := "Ctrl+↩"
							if runtime.GOOS == "darwin" {
								shortcut = "⌘↩"
							}
							kbdHint(c, "board-drawer-note-key", shortcut)
							canSend := strings.TrimSpace(a.board.noteDraft) != ""
							if ui.PrimaryButton(c.Key("board-drawer-note-add"), tr("添加备注")).
								Padding(4, 12).FontSize(14.5).Disabled(!canSend).Clicked() {
								submitNote()
							}
						})
					})
				})
			})
		})
	})
}

func (a *app) boardNoteCard(c *ui.Context, task boardTask, idx int, n boardNote) {
	t := c.Theme()
	editing := a.board.editingNoteID == n.ID
	isHuman := strings.EqualFold(n.Author, "human") || n.Author == ""
	who := n.Author
	if isHuman {
		who = tr("人工")
	}

	saveEdit := func() {
		if strings.TrimSpace(a.board.editingNoteText) == "" {
			return
		}
		text := a.board.editingNoteText
		a.changeBoard(func() (boardFile, error) {
			bf, _, err := boardUpdateNote(task.ID, n.ID, text)
			return bf, err
		}, func(err error) {
			if err != nil {
				a.setBanner(err.Error(), "")
			} else if a.board.editingNoteID == n.ID && a.board.editingNoteText == text {
				a.board.editingNoteID, a.board.editingNoteText = "", ""
			}
		})
	}
	cancelEdit := func() {
		a.board.editingNoteID = ""
		a.board.editingNoteText = ""
	}

	slot := ui.Column(c.Key("board-note-slot-"+n.ID)).FillWidth().Padding(4, 0)
	var dropOver *boardNoteDrag
	if !editing && len(task.Notes) > 1 {
		if dragged, over := ui.DragOver[boardNoteDrag](slot); over && dragged.taskID == task.ID && dragged.noteID != n.ID {
			dropOver = &dragged
		}
	}

	slot.Children(func() {
		card := ui.Column(c.Key("board-note-"+n.ID)).FillWidth().Padding(9, 11).Gap(5).
			Radius(8).Transition(boardNoteMotion(n.ID))

		switch {
		case editing:
			card.Background(t.Background).Border(1, t.Accent).Shadow(0, 0, 0, 2, t.Accent.Alpha(0.14)).
				OnShortcut(commandModifier(), ui.KeyEnter, saveEdit).
				OnShortcut(0, ui.KeyEscape, cancelEdit)
		default:
			if len(task.Notes) > 1 {
				card.Drag(boardNoteDrag{taskID: task.ID, noteID: n.ID, index: idx})
				if card.Dragging() {
					card.Opacity(0.4).Cursor(ui.CursorGrabbing)
				} else {
					card.Cursor(ui.CursorGrab)
				}
			}
			switch {
			case card.Hovered() && dropOver == nil:
				card.Background(t.Surface.Alpha(0.75)).Border(1, t.Border.Alpha(0.85))
			default:
				card.Background(t.Surface.Alpha(0.6)).Border(1, t.Border.Alpha(0.45))
			}
		}

		card.Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				if !editing && len(task.Notes) > 1 {
					ui.Icon(c, iconGripVertical).Size(13, 13).TextColor(t.TextMuted.Alpha(0.55)).Shrink(0)
				}
				if !isHuman {
					ui.Icon(c, iconBot).Size(13, 13).TextColor(t.Accent).Shrink(0)
					ui.Text(c, who).FontSize(14).FontWeight(600).TextColor(t.Accent).SingleLine()
				} else {
					ui.Text(c, who).FontSize(14).FontWeight(600).TextColor(t.Text).SingleLine()
				}
				if n.Model != "" {
					ui.Text(c, n.Model).FontSize(13.5).TextColor(t.TextMuted).SingleLine().MinWidth(0)
				}
				ui.Box(c).Grow(1)
				timeEl := ui.Text(c, n.displayTime().Local().Format("01-02 15:04")).FontSize(13.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
				if !n.UpdatedAt.IsZero() && n.UpdatedAt.After(n.CreatedAt) {
					timeEl.Tooltip(fmt.Sprintf(tr("创建于 %s · 编辑于 %s"), n.CreatedAt.Local().Format("01-02 15:04"), n.UpdatedAt.Local().Format("01-02 15:04")))
				}
				if !editing {
					editBtn := ui.ButtonBase(c.Key("board-note-edit-" + n.ID)).Label(tr("编辑备注")).Tooltip(tr("编辑备注"))
					if styleActionIcon(c, editBtn, iconPencil, 22, 13, t.TextMuted, false).Clicked() {
						a.board.editingNoteID = n.ID
						a.board.editingNoteText = n.Text
						a.board.editNoteInput.Focus(c)
					}
					delBtn := ui.ButtonBase(c.Key("board-note-del-" + n.ID)).Label(tr("删除备注")).Tooltip(tr("删除备注"))
					delFg := t.TextMuted
					if delBtn.Hovered() || delBtn.Pressed() {
						delFg = t.Danger
					}
					if styleActionIcon(c, delBtn, iconTrash2, 22, 13, delFg, false).Clicked() {
						a.board.deleteNoteTaskID = task.ID
						a.board.deleteNoteID = n.ID
					}
				}
			})
			if editing {
				ui.TextAreaBase(c.Key("board-note-edit-input-"+n.ID), &a.board.editingNoteText).
					Label(tr("编辑备注")).
					Bind(&a.board.editNoteInput).
					Height(64).FillWidth().FontSize(15).LineHeight(1.4).FocusRing(false).AutoFocus()
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					shortcut := "Ctrl+↩"
					if runtime.GOOS == "darwin" {
						shortcut = "⌘↩"
					}
					kbdHint(c, "board-note-edit-key-"+n.ID, shortcut)
					if ui.Button(c.Key("board-note-edit-cancel-"+n.ID), tr("取消")).Padding(3, 10).FontSize(13.5).Clicked() {
						cancelEdit()
					}
					canSave := strings.TrimSpace(a.board.editingNoteText) != ""
					if ui.PrimaryButton(c.Key("board-note-edit-save-"+n.ID), tr("保存")).
						Padding(3, 10).FontSize(13.5).Disabled(!canSave).Clicked() {
						saveEdit()
					}
				})
			} else {
				ui.Text(c, n.Text).FontSize(15).LineHeight(1.4).TextColor(t.Text)
			}
		})
	})

	if dropOver != nil {
		insertAfter := dropOver.index < idx
		slot.DrawOver(func(p *ui.Painter, r ui.Rect) {
			drawBoardDropLine(p, r, insertAfter, t.Accent)
		})
	}

	if !editing && len(task.Notes) > 1 {
		if dropped, ok := ui.Drop[boardNoteDrag](slot); ok && dropped.taskID == task.ID && dropped.noteID != n.ID {
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardMoveNote(task.ID, dropped.noteID, idx)
				return bf, err
			}, nil)
		}
	}
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
					ui.Text(c, repoTitle(link.Repo)).FontSize(14).FontWeight(600).TextColor(t.Text).SingleLine().Shrink(0)
					ui.Text(c, short).Font("monospace").FontSize(13.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
					ui.Box(c).Grow(1)
					if link.Agent != "" {
						boardAgentTag(c, "board-commit-agent-"+link.Hash, link.Agent)
					}
					if stale {
						chipSoft(c, "board-commit-stale-"+link.Hash, tr("提交已失效"), t.Danger, false)
					}
				})
				if link.Subject != "" {
					sub := ui.Text(c, link.Subject).FontSize(14.5).SingleLine().MinWidth(0)
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
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardUnlinkCommit(task.ID, link.Repo, link.Hash)
				return bf, err
			}, nil)
		}
	})
}

func (a *app) boardModals(c *ui.Context) {
	a.newBoardTaskModal(c)
	a.linkBoardCommitModal(c)
	a.boardSnippetModal(c)
	a.boardDeleteTaskModal(c)
	a.boardDeleteNoteModal(c)
	a.boardImportModal(c)
	a.boardBackupRestoreModal(c)
	a.boardStorageModal(c)
	a.boardSwitchDirModal(c)
}

func (a *app) newBoardTaskModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-new-task-modal"), &a.board.showNewModal, tr("新建看板任务"), 480, a.board.newSaving, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Text(c, tr("标题")).FontSize(13).TextColor(t.TextMuted)
			titleInput := ui.TextInput(c.Key("board-new-title"), &a.board.newTitle).
				Placeholder(tr("一句话描述需要完成的任务")).
				FontSize(13.5).
				FillWidth().AutoFocus()

			ui.Text(c, tr("状态")).FontSize(13).TextColor(t.TextMuted)
			if boardStatusSegmented(c, "board-new-status-seg", &a.board.newStatusIdx, 12.5).Track.Changed() {
				a.board.newStatus = boardStatuses[a.board.newStatusIdx]
			}

			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Text(c, tr("重要紧急")).FontSize(13).TextColor(t.TextMuted)
					boardPriorityPicker(c, "board-new-priority", a.board.newPriority, &a.board.newPriorityOpen, true, func(p boardPriority) {
						a.board.newPriority = p
					})
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Text(c, tr("截止日期")).FontSize(13).TextColor(t.TextMuted)
					boardDueDatePicker(c, "board-new-due", a.board.newDueDate, false, &a.board.newDueOpen, &a.board.newDuePicker, true, func(d string) {
						a.board.newDueDate = d
					})
				})
			})

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
		if ui.PrimaryButton(c.Key("board-new-submit"), tr("创建")).Disabled(a.board.newSaving || strings.TrimSpace(a.board.newTitle) == "").Clicked() {
			a.submitNewBoardTask()
		}
	})
}

func (a *app) submitNewBoardTask() {
	if a.board.newSaving {
		return
	}
	opts := boardAddTaskOptions{
		Title: a.board.newTitle, Description: a.board.newDesc, Status: a.board.newStatus,
		Priority: a.board.newPriority, DueDate: a.board.newDueDate, Repos: slices.Clone(a.board.newRepos),
	}
	a.board.newSaving = true
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardAddTaskWithOptions(opts)
		return bf, err
	}, func(err error) {
		a.board.newSaving = false
		if err != nil {
			a.board.newErr = err.Error()
			return
		}
		a.board.showNewModal, a.board.newPriorityOpen, a.board.newDueOpen = false, false, false
	})
}

func (a *app) linkBoardCommitModal(c *ui.Context) {
	t := c.Theme()
	appModal(c.Key("board-link-commit-modal"), &a.board.showLinkModal, tr("关联提交到任务"), 540, a.board.linkBusy, func() {
		ui.Column(c).FillWidth().Gap(14).Children(func() {
			a.linkCommitRepoField(c)
			refEl := a.linkCommitRefField(c)
			a.linkCommitRecentPicker(c)

			if a.board.linkErr != "" {
				ui.Row(c.Key("board-link-err")).FillWidth().Padding(8, 10).Gap(6).AlignItems(ui.Center).
					Radius(t.Radius).Background(t.Danger.Alpha(0.08)).Border(1, t.Danger.Alpha(0.3)).Children(func() {
					ui.Icon(c, iconAlertCircle).Size(14, 14).TextColor(t.Danger).Shrink(0)
					ui.Text(c, trMessage(a.board.linkErr)).FontSize(12).TextColor(t.Danger).Grow(1).MinWidth(0)
				})
			}
			if a.board.linkBusy {
				busyRow(c, "board-link-busy", tr("正在读取提交信息…"), "")
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

func (a *app) linkCommitRepoField(c *ui.Context) {
	t := c.Theme()
	known := a.boardKnownRepos()
	ui.Column(c.Key("board-link-repo-sec")).FillWidth().Gap(6).Children(func() {
		ui.Text(c, tr("选择仓库")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
		if len(known) == 0 {
			ui.Row(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
				Radius(t.Radius).Background(t.Surface.Alpha(0.5)).Border(1, t.Border.Alpha(0.6)).Children(func() {
				ui.Icon(c, iconFolderGit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, tr("请先打开至少一个 Git 仓库")).FontSize(12.5).TextColor(t.TextMuted)
			})
			return
		}
		repoLabel := tr("选择仓库")
		if a.board.linkRepo != "" {
			repoLabel = repoTitle(a.board.linkRepo) + " (" + homePath(a.board.linkRepo) + ")"
		}
		repoBtn := ui.ButtonBase(c.Key("board-link-repo-btn")).
			Label(repoLabel).
			FillWidth().Padding(8, 10).Radius(t.Radius).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Transition(colorMotion()).Disabled(a.board.linkBusy)
		if a.board.linkRepo != "" {
			repoBtn.Tooltip(homePath(a.board.linkRepo))
		}
		bg := t.Surface
		border := t.Border
		switch {
		case repoBtn.Pressed():
			bg = t.SurfacePressed
		case repoBtn.Hovered():
			bg = t.SurfaceHover
			border = t.TextMuted.Alpha(0.55)
		}
		repoBtn.Background(bg).Border(1, border)
		repoBtn.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if a.board.linkRepo != "" {
					boardRepoDot(c, a.board.linkRepo, 8)
					ui.Text(c, repoTitle(a.board.linkRepo)).FontSize(13).FontWeight(500).TextColor(t.Text).SingleLine().Shrink(0)
					ui.Text(c, homePath(a.board.linkRepo)).FontSize(12).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
				} else {
					ui.Icon(c, iconFolderGit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, tr("选择仓库")).FontSize(13).TextColor(t.TextMuted).SingleLine().Grow(1).MinWidth(0)
				}
				ui.Icon(c, iconChevronDown).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			})
		})
		repoBtn.Menu(func(m *ui.Menu) {
			for _, rp := range known {
				repoPath := rp
				if m.Item(repoTitle(repoPath) + " (" + homePath(repoPath) + ")").Checked(a.board.linkRepo == repoPath).Chosen() {
					a.board.linkRepo = repoPath
					a.board.linkErr = ""
				}
			}
		})
	})
}

func (a *app) linkCommitRefField(c *ui.Context) ui.Element {
	t := c.Theme()
	var refEl ui.Element
	ui.Column(c.Key("board-link-ref-sec")).FillWidth().Gap(6).Children(func() {
		ui.Text(c, tr("输入提交哈希或引用")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
		box := ui.Row(c.Key("board-link-ref-box")).FillWidth().Height(34).Padding(0, 6, 0, 10).Gap(6).
			AlignItems(ui.Center).Radius(t.Radius).Background(t.Background).Transition(colorMotion())
		box.Children(func() {
			ui.Icon(c, iconGitCommit).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
			refEl = ui.TextInputBase(c.Key("board-link-ref"), &a.board.linkRef).
				Label(tr("输入提交哈希或引用")).
				Placeholder(tr("例如 HEAD、main 或提交哈希前缀")).
				Bind(&a.board.linkInput).
				FontSize(13).
				Disabled(a.board.linkBusy).
				Grow(1).MinWidth(0).FocusRing(false).AutoFocus()
			if refEl.Changed() {
				a.board.linkErr = ""
			}
			if a.board.linkRef != "" && !a.board.linkBusy {
				if closeIconButton(c, "board-link-ref-clear", tr("清空")).Clicked() {
					a.board.linkRef = ""
					a.board.linkErr = ""
					refEl.Focus()
				}
			}
		})
		border := t.Border
		switch {
		case a.board.linkErr != "":
			border = t.Danger
		case refEl.Focused():
			border = t.Accent
			box.Shadow(0, 0, 0, 2, t.Accent.Alpha(0.15))
		case box.Hovered():
			border = t.TextMuted.Alpha(0.6)
		}
		box.Border(1, border)
	})
	return refEl
}

func (a *app) linkCommitRecentPicker(c *ui.Context) {
	t := c.Theme()
	var matchedTab *repoTab
	for _, tab := range a.tabs {
		if normalizeBoardRepo(tab.path) == normalizeBoardRepo(a.board.linkRepo) {
			matchedTab = tab
			break
		}
	}
	if matchedTab == nil {
		return
	}

	var recent []commit
	for _, cm := range matchedTab.commits {
		if cm.hash == worktreeHash || cm.hash == zeroHash {
			continue
		}
		recent = append(recent, cm)
		if len(recent) >= 6 {
			break
		}
	}
	if len(recent) == 0 {
		return
	}

	linkedHashes := map[string]bool{}
	if idx := findBoardTaskIndex(a.board.file.Tasks, a.board.selectedID); idx >= 0 {
		for _, l := range a.board.file.Tasks[idx].Commits {
			if normalizeBoardRepo(l.Repo) == normalizeBoardRepo(a.board.linkRepo) {
				linkedHashes[strings.ToLower(l.Hash)] = true
			}
		}
	}
	trimmedRef := strings.ToLower(strings.TrimSpace(a.board.linkRef))

	ui.Column(c.Key("board-link-recent-sec")).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tr("或从近期提交选择")).FontSize(12.5).FontWeight(500).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			if matchedTab.branch != "" {
				ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
					ui.Icon(c, iconGitBranch).Size(12, 12).TextColor(t.TextMuted).Shrink(0)
					ui.Text(c, matchedTab.branch).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
				})
			}
		})

		ui.Column(c.Key("board-link-recent-list")).FillWidth().Padding(4).Gap(2).
			Radius(8).Background(t.Surface.Alpha(0.45)).Border(1, t.Border).Children(func() {
			for _, cm := range recent {
				hexStr := hashHex(cm.hash)
				short := hexStr[:7]
				selected := trimmedRef != "" && (strings.EqualFold(trimmedRef, hexStr) || (len(trimmedRef) >= 6 && strings.HasPrefix(hexStr, trimmedRef)))
				alreadyLinked := linkedHashes[hexStr]
				subj := cm.subject
				if subj == "" {
					subj = tr("（无说明）")
				}

				tipParts := []string{short}
				if cm.author != "" {
					tipParts = append(tipParts, cm.author)
				}
				if when := cm.whenText(); when != "" {
					tipParts = append(tipParts, when)
				}
				tip := strings.Join(tipParts, " · ") + "\n" + subj
				if alreadyLinked {
					tip += "\n" + tr("已关联到此任务")
				}

				row := ui.ButtonBase(c.Key("board-link-pick-"+hexStr)).
					Label(short+" "+subj).
					Tooltip(tip).
					FillWidth().Padding(6, 8).Radius(6).Justify(ui.Start).
					Cursor(ui.CursorPointer).Transition(colorMotion()).Disabled(a.board.linkBusy)
				switch {
				case selected:
					row.Background(t.Accent.Alpha(0.12)).Border(1, t.Accent.Alpha(0.4))
				case row.Pressed():
					row.Background(t.SurfacePressed).Border(1, ui.Transparent)
				case row.Hovered():
					row.Background(t.SurfaceHover).Border(1, ui.Transparent)
				default:
					row.Border(1, ui.Transparent)
				}

				row.Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						hashBg := t.SurfaceHover
						hashFg := t.TextMuted
						if selected {
							hashBg = t.Accent.Alpha(0.16)
							hashFg = t.Accent
						}
						ui.Row(c).Padding(1, 6).Radius(4).Background(hashBg).Shrink(0).Children(func() {
							ht := ui.Text(c, short).Font("monospace").FontSize(11.5).TextColor(hashFg).SingleLine()
							if selected {
								ht.FontWeight(600)
							}
						})
						subEl := ui.Text(c, subj).FontSize(12.5).SingleLine().Grow(1).MinWidth(0)
						switch {
						case selected:
							subEl.FontWeight(500).TextColor(t.Text)
						case alreadyLinked:
							subEl.TextColor(t.TextMuted)
						default:
							subEl.TextColor(t.Text)
						}
						switch {
						case selected:
							ui.Icon(c, iconCheck).Size(14, 14).TextColor(t.Accent).Shrink(0)
						case alreadyLinked:
							ui.Icon(c, iconLink).Size(13, 13).TextColor(t.TextMuted).Shrink(0)
						case cm.when > 0:
							ui.Text(c, time.Unix(cm.when, 0).Local().Format("01-02")).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Shrink(0)
						}
					})
				})
				if row.Clicked() {
					a.board.linkRef = hexStr
					a.board.linkErr = ""
				}
			}
		})
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
		a.post(func() {
			if a.closed {
				return
			}
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardLinkCommit(taskID, link)
				return bf, err
			}, func(err error) {
				a.board.linkBusy = false
				if err != nil {
					a.board.linkErr = err.Error()
					return
				}
				a.rememberBoardCommit(link)
				a.board.showLinkModal = false
			})
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
		if dangerButton(c, "board-delete-confirm", tr("确认删除")).Clicked() {
			open = false
			a.changeBoard(func() (boardFile, error) { return boardDeleteTask(id) }, func(err error) {
				if err != nil {
					a.setBanner(err.Error(), "")
				} else {
					a.forgetBoardTaskDraft(id)
				}
			})
		}
	})
	if !open {
		a.board.deleteTaskID = ""
	}
}

// boardDeleteNoteModal asks before removing a note from a task; notes have no
// undo, so clicking the trash icon opens a confirmation dialog first.
func (a *app) boardDeleteNoteModal(c *ui.Context) {
	open := a.board.deleteNoteTaskID != "" && a.board.deleteNoteID != ""
	if !open {
		return
	}
	taskID := a.board.deleteNoteTaskID
	noteID := a.board.deleteNoteID
	task, ok := boardTaskByID(a.board.file.Tasks, taskID)
	var note boardNote
	found := false
	if ok {
		for _, n := range task.Notes {
			if n.ID == noteID {
				note = n
				found = true
				break
			}
		}
	}
	if !found {
		a.board.deleteNoteTaskID = ""
		a.board.deleteNoteID = ""
		return
	}
	t := c.Theme()
	preview := strings.Join(strings.Fields(note.Text), " ")
	if runes := []rune(preview); len(runes) > 120 {
		preview = string(runes[:120]) + "…"
	}
	appModal(c.Key("board-delete-note-modal"), &open, tr("删除备注"), 420, false, func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			if preview != "" {
				ui.Text(c, preview).FontSize(13).FontWeight(500).TextColor(t.Text)
			}
			ui.Text(c, tr("备注删除后无法恢复。")).FontSize(12.5).TextColor(t.TextMuted)
		})
	}, func() {
		if ui.Button(c.Key("board-delete-note-cancel"), tr("取消")).Clicked() {
			open = false
		}
		if dangerButton(c, "board-delete-note-confirm", tr("确认删除")).Clicked() {
			open = false
			a.changeBoard(func() (boardFile, error) {
				bf, _, err := boardDeleteNote(taskID, noteID)
				return bf, err
			}, func(err error) {
				if err != nil {
					a.setBanner(err.Error(), "")
				} else if a.board.editingNoteID == noteID {
					a.board.editingNoteID, a.board.editingNoteText = "", ""
				}
			})
		}
	})
	if !open {
		a.board.deleteNoteTaskID = ""
		a.board.deleteNoteID = ""
	}
}

func (a *app) linkCommitToBoardTask(tab *repoTab, cm commit, taskID string) {
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	link := boardCommitFromRow(tab, cm)
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardLinkCommit(taskID, link)
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
			return
		}
		a.rememberBoardCommit(link)
		a.pendingToast = fmt.Sprintf("已关联到 %s", taskID)
	})
}

func boardCommitFromRow(tab *repoTab, cm commit) boardCommitLink {
	prov := tab.provenance.get(cm.prov)
	return boardCommitLink{
		Repo:    canonicalRepoDir(tab.path),
		Hash:    hashHex(cm.hash),
		Subject: cm.subject,
		Branch:  tab.branch,
		Agent:   prov.agent,
		Model:   prov.model,
		AddedBy: "human",
		AddedAt: time.Now().UTC(),
	}
}

func (a *app) rememberBoardCommit(link boardCommitLink) {
	if a.board.commitValid == nil {
		a.board.commitValid = map[string]bool{}
	}
	a.board.commitValid[boardCommitCacheKey(link.Repo, link.Hash)] = true
}

func (a *app) createBoardTaskWithCommit(tab *repoTab, cm commit) {
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || cm.hash == zeroHash || cm.hash == worktreeHash {
		return
	}
	title := strings.TrimSpace(cm.subject)
	if title == "" {
		title = hashHex(cm.hash)[:7]
	}
	link := boardCommitFromRow(tab, cm)
	opts := boardAddTaskOptions{Title: title, Status: boardDoing, Repos: []string{tab.path}, commit: &link}
	var created boardTask
	a.changeBoard(func() (boardFile, error) {
		bf, task, err := boardAddTaskWithOptions(opts)
		created = task
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
			return
		}
		a.rememberBoardCommit(link)
		a.pendingToast = fmt.Sprintf("已关联到 %s", created.ID)
	})
}

func boardBackupReasonLabel(reason string) string {
	switch reason {
	case "delete":
		return tr("删除前")
	case "archive":
		return tr("归档前")
	case "import":
		return tr("导入前")
	case "restore":
		return tr("恢复前")
	case "switch-merge", "switch-overwrite":
		return tr("切换目录前")
	default:
		return tr("自动快照")
	}
}

func (a *app) startBoardExport() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	defaultName := fmt.Sprintf("xylring-board-%s.json", time.Now().Format("20060102"))
	go func() {
		outPath, err := saveBoardExportDialog(win, defaultName)
		if err != nil || strings.TrimSpace(outPath) == "" {
			a.post(func() {
				a.picking = false
				if err != nil {
					a.setBanner(trimMsg(err.Error()), "")
				}
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		bundle, expErr := boardExport(ctx, boardExportOptions{IncludeArchived: true})
		if expErr == nil {
			expErr = writeBoardExportFile(outPath, bundle)
		}
		a.post(func() {
			a.picking = false
			if expErr != nil {
				a.setBanner(trimMsg(expErr.Error()), "")
				return
			}
			a.pendingToast = fmt.Sprintf("已导出 %d 个任务", len(bundle.Tasks))
		})
	}()
}

func (a *app) startBoardImport() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	known := a.boardKnownRepos()
	go func() {
		paths, err := openBoardImportDialog(win)
		if err != nil || len(paths) == 0 {
			a.post(func() {
				a.picking = false
				if err != nil {
					a.setBanner(trimMsg(err.Error()), "")
				}
			})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		plan, planErr := buildBoardImportPlan(ctx, paths[0], known)
		a.post(func() {
			a.picking = false
			if planErr != nil {
				a.setBanner(trimMsg(planErr.Error()), "")
				return
			}
			a.board.importPlan = plan
			a.board.importModeIdx = 0
			a.board.importErr = ""
			a.board.importBusy = false
			a.board.showImportModal = true
		})
	}()
}

func (a *app) executeBoardImport() {
	if a.board.importBusy {
		return
	}
	mode := boardImportMerge
	if a.board.importModeIdx == 1 {
		mode = boardImportReplace
	}
	repoMap := map[string]string{}
	for _, m := range a.board.importPlan.RepoMappings {
		if strings.TrimSpace(m.ChosenPath) != "" && normalizeBoardRepo(m.ChosenPath) != normalizeBoardRepo(m.OldPath) {
			repoMap[m.OldPath] = m.ChosenPath
		}
	}
	a.board.importBusy = true
	a.board.importErr = ""
	srcPath := a.board.importPlan.SourcePath
	var bf boardFile
	var res boardImportResult
	var err error
	a.runBoardJob(func() {
		bf, res, err = boardImport(srcPath, boardImportOptions{Mode: mode, RepoMap: repoMap})
	}, func() {
		a.board.importBusy = false
		if err != nil {
			a.board.importErr = err.Error()
			return
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.board.showImportModal = false
		a.pendingToast = fmt.Sprintf("已导入 %d 个任务", res.ImportedTasks)
	})
}

func (a *app) pickRemapRepoDir(idx int) {
	if a.picking || a.win == nil || idx < 0 || idx >= len(a.board.importPlan.RepoMappings) {
		return
	}
	a.picking = true
	win := a.win
	defDir := a.board.importPlan.RepoMappings[idx].ChosenPath
	go func() {
		paths, err := openBoardDirDialog(win, defDir)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.board.importErr = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 || idx >= len(a.board.importPlan.RepoMappings) {
				return
			}
			chosen := normalizeBoardRepo(paths[0])
			if !isBoardRepoAvailable(chosen) {
				a.board.importErr = "所选目录不是有效的 Git 仓库"
				return
			}
			a.board.importErr = ""
			a.board.importPlan.RepoMappings[idx].ChosenPath = chosen
			a.board.importPlan.RepoMappings[idx].Status = "matched"
		})
	}()
}

func (a *app) openBoardBackupsModal() {
	items, err := listBoardBackups()
	if err != nil {
		a.board.backups = nil
		a.board.backupsErr = err.Error()
	} else {
		a.board.backups = items
		a.board.backupsErr = ""
	}
	a.board.showBackupsModal = true
}

func (a *app) restoreBoardFromBackup(backupPath string) {
	if a.board.storageBusy {
		return
	}
	a.board.storageBusy = true
	var bf boardFile
	var err error
	a.runBoardJob(func() { bf, err = boardRestoreBackup(backupPath) }, func() {
		a.board.storageBusy = false
		if err != nil {
			a.board.backupsErr = err.Error()
			return
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.board.showBackupsModal = false
		a.pendingToast = fmt.Sprintf("已恢复 %d 个任务", len(bf.Tasks))
	})
}

func (a *app) openBoardStorageModal() {
	a.refreshBoardStorageInfo()
	a.board.storageErr = ""
	a.board.showStorageModal = true
}

func (a *app) openBoardFolder() {
	info := a.currentBoardStorageInfo()
	if info.ActiveDir == "" {
		return
	}
	_ = os.MkdirAll(info.ActiveDir, 0o755)
	if err := openFolderInFileManager(info.ActiveDir); err != nil {
		a.setBanner(trimMsg(err.Error()), "")
	}
}

func (a *app) pickBoardStorageDir() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	info := a.currentBoardStorageInfo()
	go func() {
		paths, err := openBoardDirDialog(win, info.ActiveDir)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.board.storageErr = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 {
				return
			}
			a.beginSwitchBoardDir(paths[0])
		})
	}()
}

func (a *app) beginSwitchBoardDir(targetDir string) {
	clean := filepath.Clean(strings.TrimSpace(targetDir))
	info := a.currentBoardStorageInfo()
	if clean == "" || clean == filepath.Clean(info.ActiveDir) {
		return
	}
	hasBoard, count, err := boardProbeDir(clean)
	if err != nil {
		a.board.storageErr = err.Error()
		return
	}
	if hasBoard {
		a.board.switchTargetDir = clean
		a.board.switchTargetCount = count
		a.board.switchDirErr = ""
		a.board.showSwitchDirModal = true
		return
	}
	a.applySwitchBoardDir(clean, boardSwitchAuto)
}

func (a *app) applySwitchBoardDir(targetDir string, strategy boardDirSwitchStrategy) {
	if a.board.storageBusy {
		return
	}
	a.board.storageBusy = true
	var bf boardFile
	var info boardStorageInfo
	var err error
	a.runBoardJob(func() {
		bf, err = boardSwitchStorageDir(targetDir, strategy)
		if err == nil {
			info, _ = boardLocationInfo()
		}
	}, func() {
		a.board.storageBusy = false
		if err != nil {
			a.board.switchDirErr = err.Error()
			a.board.storageErr = err.Error()
			return
		}
		a.board.showSwitchDirModal = false
		a.board.showStorageModal = false
		a.board.storageErr = ""
		a.board.switchDirErr = ""
		if info.ActiveDir != "" {
			a.board.storageInfo, a.board.storageLoaded = info, true
		}
		a.resetBoardAfterReplace()
		a.applyBoardFile(bf, nil)
		a.board.loaded = true
		a.pendingToast = "已切换看板数据目录"
	})
}

func (a *app) boardImportModal(c *ui.Context) {
	if !a.board.showImportModal {
		return
	}
	t := c.Theme()
	plan := &a.board.importPlan
	appModal(c.Key("board-import-modal"), &a.board.showImportModal, tr("导入看板备份"), 520, a.board.importBusy, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconFileText).Size(15, 15).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, filepath.Base(plan.SourcePath)).FontSize(13).FontWeight(500).TextColor(t.Text).
					SingleLine().Grow(1).MinWidth(0).Tooltip(plan.SourcePath)
				ui.Text(c, fmt.Sprintf(tr("%d 个任务"), plan.TotalTasks)).FontSize(12.5).TextColor(t.TextMuted).Shrink(0)
			})
			ui.Segmented(c.Key("board-import-mode"), &a.board.importModeIdx, tr("合并到现有看板"), tr("覆盖现有看板")).FillWidth()
			if a.board.importModeIdx == 0 && len(plan.ConflictIDs) > 0 {
				ui.Text(c, fmt.Sprintf(tr("%d 个任务编号冲突，导入时将自动重新编号。"), len(plan.ConflictIDs))).
					FontSize(12.5).TextColor(t.TextMuted)
			}
			if a.board.importModeIdx == 1 {
				ui.Text(c, tr("覆盖将替换当前所有任务，操作前会自动备份。")).FontSize(12.5).TextColor(t.Danger)
			}
			hasUnmatched := false
			for _, m := range plan.RepoMappings {
				if m.Status != "matched" {
					hasUnmatched = true
					break
				}
			}
			if hasUnmatched {
				ui.Divider(c)
				ui.Text(c, tr("仓库路径映射")).FontSize(12.5).FontWeight(500).TextColor(t.Text)
				for i := range plan.RepoMappings {
					idx := i
					m := plan.RepoMappings[idx]
					if m.Status == "matched" {
						continue
					}
					ui.Row(c.Key(fmt.Sprintf("remap-repo-%d", idx))).FillWidth().Padding(8, 10).Gap(8).
						AlignItems(ui.Center).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
								ui.Text(c, repoTitle(m.OldPath)).FontSize(12.5).FontWeight(500).TextColor(t.Text).SingleLine()
								if m.Status == "suggested" {
									chip(c, "suggested", tr("已自动匹配"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
								} else {
									chip(c, "missing", tr("未在本机找到"), chipStyle{Text: t.Warning, Background: t.Warning.Alpha(0.12), FontSize: 10})
								}
							})
							sub := m.OldPath
							if m.ChosenPath != "" {
								sub = homePath(m.ChosenPath)
							}
							ui.Text(c, sub).FontSize(11.5).TextColor(t.TextMuted).SingleLine().Tooltip(sub)
						})
						if ui.Button(c.Key(fmt.Sprintf("remap-pick-%d", idx)), tr("选择目录…")).Padding(3, 8).FontSize(12).Disabled(a.picking).Clicked() {
							a.pickRemapRepoDir(idx)
						}
					})
				}
			}
			if a.board.importErr != "" {
				ui.Text(c, trMessage(a.board.importErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-import-cancel"), tr("取消")).Disabled(a.board.importBusy).Clicked() {
			a.board.showImportModal = false
		}
		if a.board.importModeIdx == 1 {
			if dangerButton(c, "board-import-confirm", tr("确认覆盖")).Disabled(a.board.importBusy).Clicked() {
				a.executeBoardImport()
			}
		} else {
			if ui.PrimaryButton(c.Key("board-import-confirm"), tr("确认导入")).Disabled(a.board.importBusy).Clicked() {
				a.executeBoardImport()
			}
		}
	})
}

func (a *app) boardBackupRestoreModal(c *ui.Context) {
	if !a.board.showBackupsModal {
		return
	}
	t := c.Theme()
	appModal(c.Key("board-backups-modal"), &a.board.showBackupsModal, tr("恢复自动备份"), 500, a.board.storageBusy, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if a.board.backupsErr != "" {
				ui.Text(c, trMessage(a.board.backupsErr)).FontSize(12).TextColor(t.Danger)
			}
			if len(a.board.backups) == 0 {
				ui.Column(c).FillWidth().Padding(18).Center().Children(func() {
					ui.Text(c, tr("暂无自动备份")).FontSize(13).TextColor(t.TextMuted)
				})
				return
			}
			for i, b := range a.board.backups {
				backup := b
				ui.Row(c.Key(fmt.Sprintf("backup-item-%d", i))).FillWidth().Padding(9, 12).Gap(10).
					AlignItems(ui.Center).Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, backup.CreatedAt.Local().Format("2006-01-02 15:04:05")).FontSize(13).FontWeight(500).TextColor(t.Text)
							chip(c, "reason", boardBackupReasonLabel(backup.Reason), chipStyle{Text: t.TextMuted, Background: t.Border.Alpha(0.4), FontSize: 10})
						})
						ui.Text(c, fmt.Sprintf(tr("%d 个任务"), backup.TaskCount)).FontSize(12).TextColor(t.TextMuted)
					})
					if ui.Button(c.Key(fmt.Sprintf("backup-restore-%d", i)), tr("恢复")).Padding(4, 10).FontSize(12).Clicked() {
						a.restoreBoardFromBackup(backup.Path)
					}
				})
			}
		})
	}, func() {
		if ui.Button(c.Key("board-backups-close"), tr("完成")).Clicked() {
			a.board.showBackupsModal = false
		}
	})
}

func (a *app) boardStorageModal(c *ui.Context) {
	if !a.board.showStorageModal {
		return
	}
	t := c.Theme()
	info := a.currentBoardStorageInfo()
	appModal(c.Key("board-storage-modal"), &a.board.showStorageModal, tr("看板数据目录"), 480, false, func() {
		ui.Column(c).FillWidth().Gap(10).Children(func() {
			ui.Row(c).FillWidth().Padding(10, 12).Gap(8).AlignItems(ui.Center).
				Radius(t.Radius).Background(t.Surface).Border(1, t.Border).Children(func() {
				ui.Icon(c, iconFolder).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
				ui.Text(c, homePath(info.ActiveDir)).FontSize(12.5).TextColor(t.Text).
					SingleLine().Grow(1).MinWidth(0).Tooltip(info.ActiveDir)
				if info.IsCustom {
					chip(c, "custom", tr("自定义"), chipStyle{Text: t.Accent, Background: t.Accent.Alpha(0.12), FontSize: 10})
				}
			})
			if info.FromEnv {
				ui.Text(c, tr("当前通过环境变量 XYLRING_BOARD_DIR 指定目录。")).FontSize(12).TextColor(t.TextMuted)
			}
			if a.board.storageErr != "" {
				ui.Text(c, trMessage(a.board.storageErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-storage-open"), tr("打开目录")).Clicked() {
			a.openBoardFolder()
		}
		if info.IsCustom {
			if ui.Button(c.Key("board-storage-reset"), tr("恢复默认")).Disabled(info.FromEnv || a.picking).Clicked() {
				a.beginSwitchBoardDir(info.DefaultDir)
			}
		}
		if ui.PrimaryButton(c.Key("board-storage-change"), tr("更改目录…")).Disabled(info.FromEnv || a.picking).Clicked() {
			a.pickBoardStorageDir()
		}
	})
}

func (a *app) boardSwitchDirModal(c *ui.Context) {
	if !a.board.showSwitchDirModal {
		return
	}
	t := c.Theme()
	appModal(c.Key("board-switch-dir-modal"), &a.board.showSwitchDirModal, tr("目标目录已有看板数据"), 480, a.board.storageBusy, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, homePath(a.board.switchTargetDir)).FontSize(12.5).FontWeight(500).TextColor(t.Text).
				SingleLine().Tooltip(a.board.switchTargetDir)
			ui.Text(c, fmt.Sprintf(tr("该目录中已有 %d 个任务，请选择处理方式。"), a.board.switchTargetCount)).
				FontSize(12.5).TextColor(t.TextMuted)
			if a.board.switchDirErr != "" {
				ui.Text(c, trMessage(a.board.switchDirErr)).FontSize(12).TextColor(t.Danger)
			}
		})
	}, func() {
		if ui.Button(c.Key("board-switch-cancel"), tr("取消")).Clicked() {
			a.board.showSwitchDirModal = false
		}
		if dangerButton(c, "board-switch-overwrite", tr("覆盖目标")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchOverwriteTarget)
		}
		if ui.Button(c.Key("board-switch-merge"), tr("合并过去")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchMergeTarget)
		}
		if ui.PrimaryButton(c.Key("board-switch-use"), tr("使用目标数据")).Clicked() {
			a.applySwitchBoardDir(a.board.switchTargetDir, boardSwitchUseTarget)
		}
	})
}
