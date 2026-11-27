package app

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

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
		if ts := n.DisplayTime(); ts.After(latest) {
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

func estimateBoardCardHeight(task boardTask, checklistItems int) int {
	h := 88
	if len([]rune(task.Title)) > 24 {
		h += 22
	}
	_, _, hasDue := boardTaskDueInfo(task.DueDate, task.Status == boardDone || task.Archived, time.Now())
	if task.Priority != boardPriorityNone || hasDue {
		h += 28
	}
	if checklistItems > 0 {
		shown := min(checklistItems, 5)
		h += 18 + shown*24 + 8
		if checklistItems > 5 {
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

// boardWallCard is one card of the wall, with everything only the task's own
// data decides kept: the parsed checklist, the repositories, and the height
// the card took when it was last built.
type boardWallCard struct {
	task             boardTask
	repos            []string
	items            []boardChecklistItem
	signature        uint64
	width            float32
	height           float32
	measured         bool
	measureRequested bool
	y                float32
}

// boardWallHeight remembers the height a card's content took at a width.
type boardWallHeight struct {
	signature uint64
	width     float32
	height    float32
}

type boardWallPending struct {
	id        string
	signature uint64
}

// boardWallKey is everything the built wall depends on: the board data it
// shows and the place it shows it in.
type boardWallKey struct {
	filter  boardFilterKey
	status  boardStatus
	sort    boardSortMode
	dataGen int
	width   int
}

// boardWallLayout keeps the sorted, distributed and measured wall between
// frames, so a frame builds only the cards its window shows.
type boardWallLayout struct {
	byID     map[string]*boardWallCard
	pinned   []boardWallCard
	regular  []boardWallCard
	columns  [][]int
	done     []boardTask
	heights  []float32
	key      boardWallKey
	numCols  int
	total    int
	colWidth float32
	pinnedH  float32
	masonryH float32
	valid    bool
	relative bool
}

const boardWallGap = 16

// boardHeroWidth is how wide a pinned card gets in the flow the hero section
// lays out: full width on narrow windows, a capped single card, or one of two
// side by side.
func boardHeroWidth(availW float32, pinnedCount int) float32 {
	inner := max(availW-40, 120)
	switch {
	case availW < 740:
		return inner
	case pinnedCount <= 1:
		return min(inner, 640)
	default:
		return (inner - boardWallGap) / 2
	}
}

// boardCardSignature changes when anything that sizes the card changes, so a
// measured height is reused only for the same content.
func boardCardSignature(task boardTask, checklistItems int) uint64 {
	h := fnv.New64a()
	// Done and archived tasks hide their due chip, so the status counts too.
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%d\x00%t\x00%t\x00%d\x00%d",
		task.Title, task.Status, task.Priority, task.DueDate, checklistItems, task.Pinned, task.Archived,
		len(task.Commits), len(task.Notes))
	if checklistItems == 0 {
		if desc := strings.TrimSpace(task.Description); desc != "" {
			fmt.Fprintf(h, "\x00%s", desc)
		}
	}
	if note, ok := latestBoardNote(task.Notes); ok {
		fmt.Fprintf(h, "\x00%d\x00%d", len([]rune(note.Text)), len(note.Author))
	}
	return h.Sum64()
}

func (a *app) boardWallAvailWidth(c *ui.Context) float32 {
	availW, _ := c.Size()
	if a.showRepoSidebar() {
		availW -= a.repoSplit + 1
	}
	return availW
}

// boardWallCardHandle keeps the identity assistive technology and the
// keyboard use, and reads the card's laid-out height a frame later.
func (a *app) boardWallCardHandle(id string) *ui.Handle {
	if a.board.wallHandles == nil {
		a.board.wallHandles = make(map[string]*ui.Handle)
	}
	h := a.board.wallHandles[id]
	if h == nil {
		h = &ui.Handle{}
		a.board.wallHandles[id] = h
	}
	return h
}

func (a *app) boardWallCachedHeight(id string, signature uint64, width float32) (float32, bool) {
	got, ok := a.board.wallHeights[id]
	if !ok || got.signature != signature || got.height <= 0 || max(got.width-width, width-got.width) > 1 {
		return 0, false
	}
	return got.height, true
}

func (a *app) boardWallMakeCard(task boardTask, width float32) boardWallCard {
	items := parseBoardChecklist(task.Description)
	card := boardWallCard{
		task:      task,
		repos:     boardTaskAllRepos(task),
		items:     items,
		signature: boardCardSignature(task, len(items)),
		width:     width,
	}
	if h, ok := a.boardWallCachedHeight(task.ID, card.signature, width); ok {
		card.height, card.measured = h, true
	} else {
		card.height = float32(estimateBoardCardHeight(task, len(items)))
	}
	return card
}

// boardWallLayoutFor returns the wall for the current data and window,
// rebuilding it only when one of them changed.
func (a *app) boardWallLayoutFrom(c *ui.Context) *boardWallLayout {
	availW := a.boardWallAvailWidth(c)
	key := boardWallKey{
		dataGen: a.board.dataGen,
		filter:  boardFilterKey{repo: a.board.repoFilter, search: a.board.search, archived: a.board.showArchived, english: englishUI.Load()},
		status:  a.board.statusFilter,
		sort:    a.board.sortMode,
		width:   int(availW),
	}
	l := &a.board.wall
	if l.valid && l.key == key {
		return l
	}
	// Read the columns here, not from the caller's build: a quick capture can
	// write the file while this frame is still being built, and the layout must
	// belong to the data it was keyed with.
	cols, _ := a.boardFilteredByStatus()
	*l = boardWallLayout{valid: true, key: key, byID: make(map[string]*boardWallCard)}
	l.numCols = boardMasonryColumnCount(availW)
	l.colWidth = max((availW-40-float32(l.numCols-1)*boardWallGap)/float32(l.numCols), 120)

	var unfinished []boardTask
	if a.board.statusFilter != "" {
		unfinished = append(unfinished, cols[boardStatusIndex(a.board.statusFilter)]...)
	} else {
		for _, st := range []boardStatus{boardReview, boardDoing, boardTodo} {
			unfinished = append(unfinished, cols[boardStatusIndex(st)]...)
		}
	}
	sortBoardWallTasksByMode(unfinished, a.board.sortMode)

	pinnedCount := 0
	for _, task := range unfinished {
		if task.Pinned && task.Status != boardDone {
			pinnedCount++
		}
	}
	heroW := boardHeroWidth(availW, pinnedCount)
	for _, task := range unfinished {
		isPinned := task.Pinned && task.Status != boardDone
		width := l.colWidth
		if isPinned {
			width = heroW
		}
		card := a.boardWallMakeCard(task, width)
		if isPinned {
			l.pinned = append(l.pinned, card)
		} else {
			l.regular = append(l.regular, card)
		}
	}
	l.total = len(l.regular) + len(l.pinned)
	l.done = cols[boardStatusIndex(boardDone)]
	// Relative note times go stale as time passes; only such notes need a
	// frame a minute, and none do while the board is hidden.
	now := time.Now()
	seen := func(task boardTask) {
		if note, ok := latestBoardNote(task.Notes); ok {
			if ts := note.DisplayTime(); !ts.IsZero() && now.Sub(ts) < 24*time.Hour {
				l.relative = true
			}
		}
	}
	for i := range l.pinned {
		seen(l.pinned[i].task)
	}
	for i := range l.regular {
		seen(l.regular[i].task)
	}
	for i := range l.pinned {
		l.byID[l.pinned[i].task.ID] = &l.pinned[i]
	}
	for i := range l.regular {
		l.byID[l.regular[i].task.ID] = &l.regular[i]
	}
	a.boardWallRelayout(l, availW)
	return l
}

// boardWallRelayout distributes regular cards across columns and puts every
// card at the offset its column gives it, so the window can find the cards
// near the scroll place.
func (a *app) boardWallRelayout(l *boardWallLayout, availW float32) {
	if l.numCols <= 0 {
		l.numCols = 1
	}
	if cap(l.columns) < l.numCols {
		l.columns = make([][]int, l.numCols)
	} else {
		l.columns = l.columns[:l.numCols]
		for ci := range l.columns {
			l.columns[ci] = l.columns[ci][:0]
		}
	}
	if cap(l.heights) < l.numCols {
		l.heights = make([]float32, l.numCols)
	} else {
		l.heights = l.heights[:l.numCols]
		clear(l.heights)
	}
	// Masonry keeps the sorted order and drops each card into the shortest
	// column, which keeps the top of every column level. A small tolerance keeps
	// visually equal-height rows filling left to right despite minor font or
	// subpixel differences.
	const boardWallBalanceTol = float32(4)
	for i := range l.regular {
		best := 0
		for ci := 1; ci < l.numCols; ci++ {
			if l.heights[ci]+boardWallBalanceTol < l.heights[best] {
				best = ci
			}
		}
		card := &l.regular[i]
		y := l.heights[best]
		if len(l.columns[best]) > 0 {
			y += boardWallGap
		}
		card.y = y
		l.columns[best] = append(l.columns[best], i)
		l.heights[best] = y + card.height
	}
	l.masonryH = 0
	for _, h := range l.heights {
		l.masonryH = max(l.masonryH, h)
	}
	const heroGap = float32(12)
	var pinnedH float32
	if n := len(l.pinned); n > 0 {
		if availW >= 740 && n > 1 {
			for i := 0; i < n; i += 2 {
				rowH := l.pinned[i].height
				if i+1 < n {
					rowH = max(rowH, l.pinned[i+1].height)
				}
				if i > 0 {
					pinnedH += heroGap
				}
				pinnedH += rowH
			}
		} else {
			for i := 0; i < n; i++ {
				if i > 0 {
					pinnedH += heroGap
				}
				pinnedH += l.pinned[i].height
			}
		}
	}
	l.pinnedH = pinnedH
}

// boardWallNoteBuilt remembers a card the frame built, to read its real height
// once the frame is laid out. Cards keep the estimate until then.
func (a *app) boardWallNoteBuilt(c *ui.Context, id string) {
	card, ok := a.board.wall.byID[id]
	if !ok || card.measured {
		return
	}
	alreadyPending := false
	for _, pending := range a.board.wallPending {
		if pending.id == id {
			alreadyPending = true
			break
		}
	}
	if !alreadyPending {
		a.board.wallPending = append(a.board.wallPending, boardWallPending{id: id, signature: card.signature})
	}
	if !card.measureRequested {
		card.measureRequested = true
		if !c.WindowHidden() {
			if w, h := c.Size(); w > 0 && h > 0 {
				c.Invalidate()
			}
		}
	}
}

// boardWallSyncMeasured reads the heights of the cards the last frame built and
// relayouts when one moved its column.
func (a *app) boardWallSyncMeasured(c *ui.Context, l *boardWallLayout) {
	if len(a.board.wallPending) == 0 {
		return
	}
	pending := a.board.wallPending
	a.board.wallPending = nil
	changed := false
	for _, item := range pending {
		card, ok := l.byID[item.id]
		if !ok || card.measured || card.signature != item.signature {
			continue
		}
		h := a.board.wallHandles[item.id]
		if h == nil {
			continue
		}
		b := h.Bounds(c)
		if b.W <= 0 || b.H <= 0 || max(b.W-card.width, card.width-b.W) > 2 {
			continue
		}
		if max(b.H-card.height, card.height-b.H) > 0.5 {
			changed = true
		}
		card.height, card.measured = b.H, true
		if a.board.wallHeights == nil {
			a.board.wallHeights = make(map[string]boardWallHeight)
		}
		a.board.wallHeights[item.id] = boardWallHeight{signature: item.signature, width: b.W, height: b.H}
	}
	if changed {
		a.boardWallRelayout(l, a.boardWallAvailWidth(c))
	}
}

// boardWallCardInWindow reports whether a card is near the scroll place, or
// holds the keyboard focus and must stay reachable however far it scrolled.
func (a *app) boardWallCardInWindow(c *ui.Context, card *boardWallCard, top, bottom float32) bool {
	if card.y+card.height >= top && card.y <= bottom {
		return true
	}
	if h := a.board.wallHandles[card.task.ID]; h != nil && h.FocusWithin(c) {
		return true
	}
	return false
}

func (a *app) boardWallPinned(c *ui.Context, l *boardWallLayout, availW float32) {
	ui.Column(c.Key("board-hero-section")).FillWidth().Gap(12).Children(func() {
		if availW >= 740 {
			if len(l.pinned) == 1 {
				ui.Row(c.Key("board-hero-row-0")).FillWidth().Children(func() {
					ui.Box(c).Grow(1).MinWidth(0).MaxWidth(640).Children(func() {
						a.boardCard(c, l.pinned[0], true)
					})
				})
				return
			}
			for i := 0; i < len(l.pinned); i += 2 {
				i := i
				ui.Row(c.Key(fmt.Sprintf("board-hero-row-%d", i/2))).FillWidth().Gap(boardWallGap).AlignItems(ui.Start).Children(func() {
					ui.Box(c).Grow(1).MinWidth(0).Children(func() {
						a.boardCard(c, l.pinned[i], true)
					})
					if i+1 < len(l.pinned) {
						ui.Box(c).Grow(1).MinWidth(0).Children(func() {
							a.boardCard(c, l.pinned[i+1], true)
						})
					} else {
						ui.Box(c).Grow(1).MinWidth(0)
					}
				})
			}
			return
		}
		for _, card := range l.pinned {
			a.boardCard(c, card, true)
		}
	})
}

// boardNextRefreshAt is the earliest moment the wall must repaint itself for
// reasons no input caused: an external-update highlight ending, or a relative
// time crossing into the next minute.
func (a *app) boardNextRefreshAt(now time.Time, layout *boardWallLayout) time.Time {
	var target time.Time
	add := func(t time.Time) {
		if t.IsZero() || !t.After(now) {
			return
		}
		if target.IsZero() || t.Before(target) {
			target = t
		}
	}
	for _, ts := range a.board.recentUpdated {
		if !ts.IsZero() {
			add(ts.Add(12 * time.Second))
		}
	}
	if layout.relative {
		add(now.Truncate(time.Minute).Add(time.Minute))
	}
	return target
}

// boardScheduleRefresh asks for the frame that lets an external-update
// highlight expire and a relative time tick, and only while the board shows:
// a hidden board keeps no timer running.
func (a *app) boardScheduleRefresh(layout *boardWallLayout) {
	if a.closed || !a.showBoard {
		return
	}
	now := time.Now()
	target := a.boardNextRefreshAt(now, layout)
	if target.IsZero() || !target.After(now.Add(100*time.Millisecond)) {
		return
	}
	// A pending timer that fires no later than this target covers it; an
	// earlier target, as a fresh highlight, gets its own.
	if a.board.refreshPending && !a.board.refreshAt.After(target) {
		return
	}
	a.board.refreshPending = true
	a.board.refreshAt = target
	delay := time.Until(target)
	time.AfterFunc(delay, func() {
		a.onMain(func() {
			fire := a.board.refreshPending && !a.closed && a.showBoard
			a.board.refreshPending = false
			a.board.refreshAt = time.Time{}
			if fire {
				a.post(func() {})
			}
		})
	})
}

// boardWallEmpty is the board's only words when nothing matches: what is empty
// and, while filtering, the one action that brings tasks back.
func (a *app) boardWallEmpty(c *ui.Context, msg string, clearable bool) {
	t := c.Theme()
	ui.Row(c.Key("board-wall-empty")).FillWidth().Padding(36, 16).Gap(10).
		Justify(ui.Center).AlignItems(ui.Center).Children(func() {
		if clearable {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(t.TextMuted).Shrink(0)
		}
		ui.Text(c, msg).FontSize(13.5).TextColor(t.TextMuted)
		if clearable {
			if ui.Button(c.Key("board-clear-filter"), tr("清除筛选")).Padding(2, 8).FontSize(12).Clicked() {
				a.board.repoFilter = ""
				a.board.search = ""
			}
		}
	})
}

func (a *app) boardContextWall(c *ui.Context) {
	filtering := a.board.repoFilter != "" || strings.TrimSpace(a.board.search) != ""
	layout := a.boardWallLayoutFrom(c)
	a.boardWallSyncMeasured(c, layout)
	doneTasks := layout.done
	// Search shows what it matched, including completed tasks, without
	// touching the fold the user chose for the quiet list below the wall.
	doneExpanded := a.board.doneExpanded || (filtering && len(doneTasks) > 0)

	availW := a.boardWallAvailWidth(c)
	masonryTop := float32(12) // the wall content's top padding
	if len(layout.pinned) > 0 {
		masonryTop += layout.pinnedH + 20
	}
	scrollTop := a.board.wallScroll.Y
	viewportH := float32(600)
	if b := a.board.wallScroll.Handle.Bounds(c); b.H > 0 {
		viewportH = b.H
	}
	top := scrollTop - masonryTop - viewportH*0.5
	bottom := scrollTop - masonryTop + viewportH
	a.boardScheduleRefresh(layout)

	ui.Scroll(c.Key("board-wall-scroll")).Fill().MinWidth(0).MinHeight(0).
		TrackScroll(&a.board.wallScroll).Children(func() {
		ui.Column(c.Key("board-wall-content")).FillWidth().Padding(12, 20, 28, 20).Gap(20).Children(func() {
			if len(layout.pinned) > 0 {
				a.boardWallPinned(c, layout, availW)
			}
			if layout.total > 0 {
				ui.Box(c.Key("board-masonry")).FillWidth().Height(layout.masonryH).Children(func() {
					for colIdx := range layout.columns {
						x := float32(colIdx) * (layout.colWidth + boardWallGap)
						for _, cardIdx := range layout.columns[colIdx] {
							card := &layout.regular[cardIdx]
							if !a.boardWallCardInWindow(c, card, top, bottom) {
								// Offscreen cards are not built: their column keeps
								// the place the measured height gives them.
								continue
							}
							ui.Box(c.Key("board-masonry-slot-" + card.task.ID)).Absolute().
								Left(x).Top(card.y).Width(layout.colWidth).Children(func() {
								a.boardCard(c, *card, false)
							})
						}
					}
				})
			}
			if layout.total == 0 {
				switch {
				case len(doneTasks) == 0 && filtering:
					a.boardWallEmpty(c, tr("没有匹配的任务"), true)
				case len(doneTasks) == 0 && a.board.statusFilter != "":
					a.boardWallEmpty(c, tr("当前状态没有任务"), false)
				case len(doneTasks) == 0:
					a.boardWallEmpty(c, tr("暂无任务"), false)
				case !filtering && a.board.statusFilter == "":
					a.boardWallEmpty(c, tr("所有任务已完成"), false)
				}
			}

			a.boardDoneSection(c, doneTasks, doneExpanded)
		})
	})
}

// boardDoneSection shows the completed tasks. expanded is how the list shows
// this frame, which search makes true without changing the user's preference.
func (a *app) boardDoneSection(c *ui.Context, doneTasks []boardTask, expanded bool) {
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
			if expanded {
				chev = iconChevronDown
			}
			toggleBtn := ui.ButtonBase(c.Key("board-done-toggle")).
				Label(tr("已完成")).
				Expanded(expanded).
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
				a.board.doneExpanded = !expanded
			}

			ui.Box(c).Grow(1)

			if expanded && len(doneTasks) > 0 {
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
					// Only the completed tasks on screen at the click, checked again
					// under the file lock: a hidden project or a search result the
					// user cannot see must not be archived behind their back.
					ids := make([]string, 0, len(doneTasks))
					for _, task := range doneTasks {
						if !task.Archived {
							ids = append(ids, task.ID)
						}
					}
					var count int
					a.changeBoard(func() (boardFile, error) {
						bf, n, err := boardArchiveDoneTasks(ids)
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

		if !expanded {
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
		// Moving an archived task back to the todo list also takes it out of
		// the archive: left archived it would vanish from the default board.
		next, archived := boardTodo, false
		a.applyBoardTaskPatch(task.ID, boardTaskPatch{Status: &next, Archived: &archived})
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

// boardCardBranch picks the repository whose branch a pinned card shows: the
// project the board is filtered to when the task belongs to it, so filtering a
// multi-repository task shows the branch the user is looking at.
func (a *app) boardCardBranch(repos []string) (chosen, branch string) {
	if len(repos) == 0 {
		return "", ""
	}
	chosen = repos[0]
	if filter := normalizeBoardRepo(a.board.repoFilter); filter != "" && slices.Contains(repos, filter) {
		chosen = filter
	}
	return chosen, a.boardRepoActiveBranch(chosen)
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

func (a *app) boardCard(c *ui.Context, wallCard boardWallCard, hero bool) {
	task := wallCard.task
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

	// The card is a group, not one big button: its copy, pin, done and
	// checklist actions must stay reachable to assistive technology, which
	// reads nothing inside a button. A click anywhere else still opens it.
	card := ui.ButtonBase(c.Key("board-card-" + task.ID)).
		Label(task.ID + " " + task.Title).
		Role(ui.RoleGroup).
		FillWidth().
		Radius(radius).
		Cursor(ui.CursorPointer).
		Drag(boardTaskDrag{taskID: task.ID, status: task.Status, pinned: isFocus}).
		Transition(boardCardMotion(task.ID))
	// The card keeps its identity for focus and assistive technology, and the
	// wall reads the height it took once the frame is laid out.
	card.Bind(a.boardWallCardHandle(task.ID))
	a.boardWallNoteBuilt(c, task.ID)
	if hero {
		if selected {
			card.Padding(17, 19)
		} else {
			card.Padding(18, 20)
		}
	} else {
		if selected {
			card.Padding(14, 15)
		} else {
			card.Padding(15, 16)
		}
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
	var toggledItem *boardChecklistItem
	var clickedCommit *boardCommitLink

	repos, checklist := wallCard.repos, wallCard.items

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
					if hero {
						if chosen, branch := a.boardCardBranch(repos); branch != "" {
							label := branch
							if len(repos) > 1 {
								// With several repositories the branch alone does not
								// say whose it is once the project row is hidden.
								label = repoTitle(chosen) + " · " + branch
							}
							ui.Row(c).Padding(1, 6).Gap(4).Radius(9).Background(chipBg).
								AlignItems(ui.Center).Shrink(0).
								Tooltip(repoTitle(chosen) + " · " + branch + "\n" + chosen).
								Children(func() {
									boardRepoDot(c, chosen, 6)
									ui.Icon(c, iconGitBranch).Size(10.5, 10.5).TextColor(mutedText).Shrink(0)
									ui.Text(c, label).FontSize(11).TextColor(mutedText).SingleLine()
								})
						}
					}
				})

				// Quiet-at-rest floating action capsule: Copy Agent Context, Pin/Unpin, Mark Done
				actionRow := ui.Row(c.Key("board-card-actions-" + task.ID)).Gap(2).Padding(2).Radius(8).
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

				ui.Column(c.Key("board-card-checklist-" + task.ID)).FillWidth().Gap(3).Children(func() {
					ui.Row(c.Key("board-card-chk-prog-"+task.ID)).FillWidth().Gap(8).AlignItems(ui.Center).Padding(0, 4, 2, 4).Children(func() {
						ui.Box(c).Grow(1).MinWidth(0).Height(3).Radius(1.5).Background(trackCol).Clip().Children(func() {
							ui.Box(c.Key("board-card-chk-fill-" + task.ID)).Height(3).WidthPercent(pct).Radius(1.5).
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
							Role(ui.RoleCheckBox).Checked(item.Checked).
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
							picked := item
							toggledItem = &picked
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
				ui.Column(c.Key("board-card-commits-" + task.ID)).FillWidth().Gap(4).Children(func() {
					for _, link := range boardNewestCommits(task.Commits, 2) {
						short := link.Hash
						if len(short) > 7 {
							short = short[:7]
						}
						label := short
						if link.Subject != "" {
							label = short + " " + link.Subject
						}
						// The same commit can hang off a task from two repositories,
						// so the chip's identity names both.
						linkKey := boardCommitCacheKey(link.Repo, link.Hash)
						cBtn := ui.ButtonBase(c.Key("board-card-commit-"+task.ID+"-"+linkKey)).
							Label(label).
							Tooltip(link.Repo+" @ "+link.Hash).
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
			if latest, ok := latestBoardNote(task.Notes); ok {
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
						if ts := latest.DisplayTime(); !ts.IsZero() {
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

	// In the default sort a card can be dropped on another to take its place,
	// inside the same pin group; a status drop there also moves the card. The
	// other sorts would put it back, so they offer no target.
	if dragged, over := ui.DragOver[boardTaskDrag](card); over && a.boardCardDropAllowed(dragged, task) {
		_, y, _ := card.PointerPosition()
		after := y > card.Bounds().H/2
		card.DrawOver(func(p *ui.Painter, r ui.Rect) {
			drawBoardDropLine(p, r, after, t.Accent)
		})
	}
	if dropped, ok := ui.Drop[boardTaskDrag](card); ok && a.boardCardDropAllowed(dropped, task) {
		_, y, _ := card.PointerPosition()
		a.moveBoardTaskNear(dropped.taskID, task, y > card.Bounds().H/2)
		return
	}

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
	if toggledItem != nil {
		a.toggleBoardChecklistItem(task.ID, *toggledItem)
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

// boardCardDropAllowed reports whether a card may take the place of target:
// manual order only exists in the default sort, and only inside one pin group.
func (a *app) boardCardDropAllowed(dragged boardTaskDrag, target boardTask) bool {
	if a.board.sortMode != boardSortDefault || dragged.taskID == target.ID {
		return false
	}
	return dragged.pinned == (target.Pinned && target.Status != boardDone)
}

// moveBoardTaskNear puts a dragged card before or after another one with the
// stored manual order, moving it to that card's status as well when they differ.
func (a *app) moveBoardTaskNear(id string, target boardTask, after bool) {
	status := target.Status
	a.commitBoardDrafts()
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardMoveTask(id, status, target.ID, after)
		return bf, err
	}, func(err error) {
		if err != nil {
			a.setBanner(err.Error(), "")
		}
	})
}

// toggleBoardChecklistItem flips one checklist marker where the task is
// stored, identifying the item by its line and text. Only the marker byte is
// rewritten, so a description another client just edited keeps its changes
// and an item that moved or changed state fails instead of flipping another
// line. An open drawer for the same task flips the marker in its draft too;
// the rest of the draft stays unsaved until it is committed.
func (a *app) toggleBoardChecklistItem(id string, item boardChecklistItem) {
	if a.board.editTaskID == id && a.board.descDraft != "" {
		if next, ok := toggleBoardChecklistItem(a.board.descDraft, item.LineIndex, item.Text, item.Checked); ok {
			a.board.descDraft = next
		}
		// An item only the draft has carries no line to flip on disk: the
		// drawer's next save writes it together with its marker.
		if !a.boardChecklistItemStored(id, item) {
			return
		}
	}
	a.changeBoard(func() (boardFile, error) {
		bf, _, err := boardToggleChecklistTask(id, item.LineIndex, item.Text, item.Checked)
		return bf, err
	}, func(err error) {
		if err == nil {
			return
		}
		// Nothing was written, so the draft must not keep the flip either: it
		// would be saved later over the text that made the click fail.
		if a.board.editTaskID == id {
			if back, ok := toggleBoardChecklistItem(a.board.descDraft, item.LineIndex, item.Text, !item.Checked); ok {
				a.board.descDraft = back
			}
		}
		a.setBanner(err.Error(), "")
	})
}

// boardChecklistItemStored reports whether the saved description still holds
// this item in the state the caller saw, using the same identity the write
// uses.
func (a *app) boardChecklistItemStored(id string, item boardChecklistItem) bool {
	idx := findBoardTaskIndex(a.board.file.Tasks, id)
	if idx < 0 {
		return false
	}
	_, ok := toggleBoardChecklistItem(a.board.file.Tasks[idx].Description, item.LineIndex, item.Text, item.Checked)
	return ok
}
