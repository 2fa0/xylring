package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// commit 只保留图谱和列表要画的字段。
type commit struct {
	hash      [20]byte
	parents   [][20]byte
	author    string
	when      int64
	subject   string
	nodeCol   int
	nodeColor int
	segs      []seg
	file      *fileChange // 文件历史中，这条提交当时的路径（含重命名前路径）。
}

func (c commit) whenText() string {
	if c.when == 0 {
		return ""
	}
	return time.Unix(c.when, 0).Local().Format("2006-01-02 15:04")
}

// repoTab 是一个已导入的仓库。当前只保留读历史和画图谱需要的状态。
type repoTab struct {
	pendingSession *savedRepo
	operation      string
	id             int
	path           string
	title          string
	generation     int
	loading        bool
	loaded         bool
	err            string
	holdAuto       bool
	closed         bool
	checking       bool
	metaKey        string
	refWatch       refWatch
	refsChecked    time.Time
	usedAt         time.Time
	notice         string
	lastFetch      time.Time
	fetching       bool
	fetchGen       int
	fetchCancel    context.CancelFunc

	historyCancel  context.CancelFunc
	historyStarted time.Time
	pager          *historyPager
	pagerCancel    context.CancelFunc
	commitIndex    map[[20]byte]int
	historyBytes   int64
	historyBudget  int64

	branch      string
	head        [20]byte
	refs        map[[20]byte][]refInfo
	ahead       int
	behind      int
	upstream    string
	hasUpstream bool
	syncBusy    bool
	commitMsg    string
	commitAmend  bool
	amendGen     int
	commitBusy   bool
	commitErr    string
	commitPinned bool
	commits     []commit
	graph       graph
	hasMore     bool
	authors     map[string]string

	list      ui.ListState
	selection ui.Selection[any]
	selected  int

	inspecting    [20]byte
	inspectParent int
	detailSplit   float32
	diffMaximized bool
	diffFrom      [20]byte
	diffTo        [20]byte

	locate     string
	locateHash [20]byte
	locateErr  string
	locateGen  int

	query             historyQuery
	historyTips       []string
	branchFilter      string
	searchText        string
	searchKind        string
	searchInput       ui.Handle
	searchSuggestOpen bool
	historyBack       *historyLocation
	fileBack          *historyLocation
	restore           *historyLocation
	restoreFile       string
	restoreDiff       *historyLocation

	worktree      bool
	worktreeCount int
	worktreeKey   string
	worktreeCache *worktreeStatusCache
	worktreeFiles []fileChange
	worktreeRow   commit

	comparison           *commitComparison
	compareFiles         []fileChange
	compareLoading       bool
	compareErr           string
	compareGen           int
	compareCancel        context.CancelFunc
	fileList             ui.ListState
	fileListScroll       ui.ScrollState
	fileListScrollY      float32
	fileSel              int
	fileFilter           string
	fileFilterInput      ui.Handle
	fileOutline          ui.OutlineState[string]
	fileTreeScroll       ui.ScrollState
	fileTreeScrollY      float32
	fileHover            ui.Handle
	fileHoverID          string
	fileHoverY           float32
	fileOutlineSel       int
	fileTreeKey          string
	fileTreeFilter       string
	fileTreeInput        *fileChange
	fileTreeCount        int
	fileTreeData         fileTree
	fileSplit            float32
	diffSource           *fileDiffSource
	diff                 *editor.DiffState
	simpleDiff           *simpleDiff
	imageDiff            *imageDiff
	imageViewMode        int
	imageFitActual       bool
	diffNote             string
	diffLoading          bool
	openFileTabAfterLoad bool
	fileTabs             []*fileDiffTab
	fileTabsScroll       ui.ScrollState
	fileTabsScrollTarget float32
	contentTab           int // 0 is history; the remaining tabs are file snapshots.
	nextFileTabID        int
	revealFileTab        bool
	shownFileTab         int

	details        *commitDetails
	detailsOpen    bool
	detailsLoading bool
	detailsErr     string
	detailsGen     int
	detailsCancel  context.CancelFunc
}

func newTab(id int, path string) *repoTab {
	return &repoTab{
		id:             id,
		path:           path,
		title:          repoTitle(path),
		selected:       -1,
		fileSel:        -1,
		fileOutlineSel: -1,
		authors:        map[string]string{},
		refs:           map[[20]byte][]refInfo{},
		searchKind:     "说明",
	}
}

func repoTitle(path string) string {
	return filepath.Base(path)
}

func homePath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil {
		if path == home {
			return "~"
		}
		prefix := home + string(filepath.Separator)
		if strings.HasPrefix(path, prefix) {
			return "~" + path[len(home):]
		}
	}
	return path
}

func repoChoiceLabel(from, path string) string {
	if from == "" {
		return repoTitle(path)
	}
	rel, err := filepath.Rel(from, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return repoTitle(path)
	}
	if rel == "." {
		return filepath.Base(path)
	}
	return rel
}

func repoContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return false
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}

func repoNestLevel(items []string, path string) int {
	n := 0
	for _, other := range items {
		if repoContains(other, path) {
			n++
		}
	}
	return n
}

func repoListLabel(from string, items []string, path string) string {
	parent := ""
	for _, other := range items {
		if repoContains(other, path) && (parent == "" || len(other) > len(parent)) {
			parent = other
		}
	}
	if parent != "" {
		rel, err := filepath.Rel(parent, path)
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return repoChoiceLabel(from, path)
}

func (t *repoTab) intern(s string) string {
	if t.authors == nil {
		t.authors = map[string]string{}
	}
	if v, ok := t.authors[s]; ok {
		return v
	}
	s = strings.Clone(s)
	t.authors[s] = s
	return s
}

func (t *repoTab) rows() int { return len(t.commits) + t.worktreeOffset() }

func (t *repoTab) rowData(row int) *commit {
	off := t.worktreeOffset()
	if off == 1 && row == 0 {
		t.syncWorktreeCommit()
		return &t.worktreeRow
	}
	row -= off
	if row < 0 || row >= len(t.commits) {
		return nil
	}
	return &t.commits[row]
}

func (t *repoTab) indexOf(h [20]byte) int {
	if t.commitIndex != nil {
		if i, ok := t.commitIndex[h]; ok {
			return i
		}
		return -1
	}
	for i := range t.commits {
		if t.commits[i].hash == h {
			return i
		}
	}
	return -1
}

func (t *repoTab) cancel() {
	t.stopAutoFetch()
	t.stopHistory()
	t.generation++
	t.compareGen++
	t.locateGen++
	if t.compareCancel != nil {
		t.compareCancel()
		t.compareCancel = nil
	}
	t.detailsGen++
	if t.detailsCancel != nil {
		t.detailsCancel()
		t.detailsCancel = nil
	}
}

func (t *repoTab) stopHistory() {
	if t.pagerCancel != nil {
		t.pagerCancel()
		t.pagerCancel = nil
	}
	if t.pager != nil {
		t.pager.close()
		t.pager = nil
	}
	if t.historyCancel != nil {
		t.historyCancel()
		t.historyCancel = nil
	}
}

var userConfigDir = os.UserConfigDir

const (
	appConfigName    = "Xylring"
	legacyConfigName = "GitClientPrototype"
)

func configFile(name string) (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	cur := filepath.Join(dir, appConfigName)
	if err := migrateConfigDir(filepath.Join(dir, legacyConfigName), cur); err != nil {
		return "", err
	}
	if err := os.MkdirAll(cur, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(cur, name), nil
}

func migrateConfigDir(legacy, cur string) error {
	if _, err := os.Stat(cur); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(legacy); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(legacy, cur)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gitclient-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func loadRecent() []string {
	path, err := configFile("recent.json")
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var paths []string
	if json.Unmarshal(b, &paths) != nil {
		return nil
	}
	return paths
}

func saveRecent(paths []string) error {
	path, err := configFile("recent.json")
	if err != nil {
		return err
	}
	return writeJSON(path, paths)
}
