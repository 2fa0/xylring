package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
)

// commit 只保留列表和图谱要用的字段。正文、文件列表和 diff 在打开时再取。
type commit struct {
	hash        [20]byte
	parents     [][20]byte
	author      string
	when        int64
	subject     string
	historyPath string
	stash       string
	nodeCol     int
	nodeColor   int
	segs        []seg
}

func (c commit) whenText() string {
	if c.when == 0 {
		return ""
	}
	return time.Unix(c.when, 0).Local().Format("2006-01-02 15:04")
}

type searchOrigin struct {
	reading       *editor.DiffReadingPosition
	hash          [20]byte
	dirty, hidden bool
	count, row    int
	file, detail  string
}
type repoTab struct {
	searchOrigin                               *searchOrigin
	pending                                    []string
	historyStarted, detailStarted, diffStarted time.Time
	id                                         int
	path                                       string
	title                                      string

	generation  int
	loading     bool
	loaded      bool
	released    bool
	err         string
	holdAuto    bool
	resumeCount int
	resumeRow   int

	historyCancel context.CancelFunc
	refsCancel    context.CancelFunc
	detailCancel  context.CancelFunc
	diffCancel    context.CancelFunc
	navCancel     context.CancelFunc
	navGen        int

	scope                                     string
	scopeChoice                               string
	historyRefs                               []string
	refInput                                  string
	refErr                                    string
	refBusy                                   bool
	historyPath                               string
	searchText                                string
	searchIndex                               int
	searchScope                               string
	wantFirst, wantNext                       bool
	sidebarSection                            string
	leftPane                                  string
	diffOpen                                  bool
	dirCache                                  map[string][]browserEntry
	dirBusy                                   map[string]bool
	browseCtx                                 context.Context
	browseCancel                              context.CancelFunc
	treeOpen                                  map[string]bool
	browserErr                                string
	browserList, refList, changeTreeList      ui.ListState
	viewer                                    *fileViewer
	fileMode                                  string
	fileTree                                  bool
	closedFolders                             map[string]bool
	options                                   viewSettings
	palette                                   []ui.Color
	detailHidden                              bool
	focusDiff, infoExpanded, showRef          bool
	detailSplit, filePaneWidth, filePaneShown float32
	resumeFile, resumeDetail                  string
	resumeReading                             *editor.DiffReadingPosition

	stashes    []commit
	stashView  *commit
	stashList  ui.ListState
	branch     string
	head       [20]byte
	dirty      int
	refChoices []refInfo
	refs       map[[20]byte][]refInfo
	commits    []commit
	graph      graph
	hasMore    bool
	authors    map[string]string

	list          ui.ListState
	selection     ui.Selection[any]
	selected      int
	commitApplied int
	wantHash      [20]byte
	wantDirty     bool
	comparison    *commitComparison

	query string

	detailGen     int
	detailLoading bool
	detailErr     string
	shownKey      string
	files         []fileChange
	info          *commitInfo
	fileList      ui.ListState
	fileSel       int
	fileApplied   int

	diffGen     int
	diffLoading bool
	detailNote  string
	diff        *editor.DiffState
	layout      editor.DiffLayout
}

func newTab(id int, path string) *repoTab {
	return &repoTab{
		id:            id,
		detailSplit:   35,
		filePaneWidth: 210,
		path:          path,
		title:         repoTitle(path),
		selected:      -1,
		commitApplied: -1,
		fileSel:       -1,
		fileApplied:   -1,
		resumeRow:     -1,
		options:       defaultViewSettings(),
		scope:         "全部分支",
		scopeChoice:   "全部分支",
		authors:       map[string]string{},
		refs:          map[[20]byte][]refInfo{},
	}
}

func repoTitle(path string) string {
	base := filepath.Base(path)
	parent := filepath.Base(filepath.Dir(path))
	if parent != "" && parent != "." && parent != string(filepath.Separator) {
		return parent + "/" + base
	}
	return base
}

func (t *repoTab) intern(s string) string {
	if t.authors == nil {
		t.authors = map[string]string{}
	}
	if v, ok := t.authors[s]; ok {
		return v
	}
	t.authors[s] = s
	return s
}

func (t *repoTab) stashRows() int {
	if t.historyPath != "" || t.searchText != "" || len(t.historyRefs) > 0 {
		return 0
	}
	return len(t.stashes)
}

func (t *repoTab) rows() int {
	n := len(t.commits) + t.stashRows()
	if t.dirty > 0 {
		n++
	}
	return n
}

func (t *repoTab) rowCommit(row int) int {
	if row < 0 {
		return -1
	}
	if t.dirty > 0 {
		if row == 0 {
			return -1
		}
		return row - 1 - t.stashRows()
	}
	return row - t.stashRows()
}

func (t *repoTab) commitRow(i int) int {
	if t.dirty > 0 {
		return i + 1 + t.stashRows()
	}
	return i + t.stashRows()
}

func (t *repoTab) captureSelection() {
	if len(t.commits) > 0 {
		t.resumeCount = len(t.commits)
		t.resumeRow, _ = t.list.Visible()
	}
	if t.stashView != nil {
		t.wantHash = t.stashView.hash
		t.wantDirty = false
		return
	}
	if t.selected < 0 {
		return
	}
	if t.dirty > 0 && t.selected == 0 {
		t.wantDirty = true
		t.wantHash = zeroHash
		return
	}
	t.wantDirty = false
	if c := t.selectedCommit(); c != nil && c.stash != "" {
		t.wantHash = c.hash
		return
	}
	ci := t.rowCommit(t.selected)
	if ci >= 0 && ci < len(t.commits) {
		t.wantHash = t.commits[ci].hash
	}
}

func (t *repoTab) historyCount() int {
	return max(t.options.First, t.resumeCount)
}

func (t *repoTab) cancelReads() {
	for _, cancel := range []context.CancelFunc{t.historyCancel, t.refsCancel, t.detailCancel, t.diffCancel, t.navCancel, t.browseCancel} {
		if cancel != nil {
			cancel()
		}
	}
	t.historyCancel, t.refsCancel, t.detailCancel, t.diffCancel = nil, nil, nil, nil
	t.navCancel = nil
	t.browseCancel = nil
	t.browseCtx = nil
	t.refBusy = false
}

func (t *repoTab) reselect() {
	if t.wantDirty && t.dirty > 0 {
		t.selected = 0
		return
	}
	for i, s := range t.stashes {
		if s.hash == t.wantHash {
			if i >= t.stashRows() {
				t.stashView = &t.stashes[i]
				t.selected = -1
				return
			}
			t.selected = i
			if t.dirty > 0 {
				t.selected++
			}
			return
		}
	}
	if t.wantHash == zeroHash {
		t.selected = -1
		return
	}
	for i := range t.commits {
		if t.commits[i].hash == t.wantHash {
			t.selected = t.commitRow(i)
			return
		}
	}
	t.selected = -1
}

func (t *repoTab) selectionKey() string {
	if t.comparison != nil {
		return "compare:" + hashHex(t.comparison.from) + ":" + hashHex(t.comparison.to)
	}
	if t.dirty > 0 && t.selected == 0 {
		return "u"
	}
	if c := t.selectedCommit(); c != nil && c.stash != "" {
		return "stash:" + hashHex(c.hash)
	}
	ci := t.rowCommit(t.selected)
	if ci < 0 || ci >= len(t.commits) {
		return ""
	}
	return hashHex(t.commits[ci].hash)
}

func (t *repoTab) rowData(row int) *commit {
	i := row
	if t.dirty > 0 {
		i--
	}
	if i >= 0 && i < t.stashRows() {
		return &t.stashes[i]
	}
	ci := t.rowCommit(row)
	if ci < 0 || ci >= len(t.commits) {
		return nil
	}
	return &t.commits[ci]
}
func (t *repoTab) selectedCommit() *commit {
	if t.stashView != nil {
		return t.stashView
	}
	return t.rowData(t.selected)
}

func (t *repoTab) refText(h [20]byte) string {
	var b strings.Builder
	if h == t.head && t.head != zeroHash {
		b.WriteString("HEAD")
	}
	rs := t.refs[h]
	for i, r := range rs {
		if i == 4 {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString("…")
			break
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if r.kind == refTag {
			b.WriteString("tag ")
		}
		b.WriteString(r.name)
	}
	return b.String()
}

func (t *repoTab) releaseHeavy() {
	t.captureReading()
	t.captureSelection()
	t.cancelReads()
	t.generation++
	t.loading = false
	t.loaded = false
	t.released = true
	t.err = ""
	t.commits = nil
	t.stashes = nil
	t.stashView = nil
	t.stashList = ui.ListState{}
	t.graph = graph{}
	t.refs = nil
	t.refChoices = nil
	t.authors = nil
	t.hasMore = false
	t.files = nil
	t.info = nil
	t.diff = nil
	t.viewer = nil
	t.dirCache = nil
	t.dirBusy = nil
	t.browserList = ui.ListState{}
	t.refList = ui.ListState{}
	t.changeTreeList = ui.ListState{}
	t.detailLoading = false
	t.detailErr = ""
	t.detailNote = ""
	t.diffLoading = false
	t.shownKey = ""
	t.selected = -1
	t.commitApplied = -1
	t.fileSel = -1
	t.fileApplied = -1
	t.list = ui.ListState{Columns: t.list.Columns}
	t.fileList = ui.ListState{}
	t.selection = ui.Selection[any]{}
}

func (t *repoTab) match(i int, q string) bool {
	c := t.commits[i]
	if strings.Contains(strings.ToLower(c.subject), q) || strings.Contains(strings.ToLower(c.author), q) {
		return true
	}
	return strings.Contains(hashHex(c.hash), q) ||
		strings.Contains(c.whenText(), q) || strings.Contains(strings.ToLower(t.refText(c.hash)), q)
}

func validBranch(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "请输入分支名"
	}
	if strings.HasPrefix(name, "-") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") {
		return "分支名不合法"
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.ContainsAny(name, " ~^:?*[\\\x00") {
		return "分支名含有非法字符"
	}
	return ""
}

func recentFile() (string, error) { return configFile("recent.json") }

func loadRecent() []string {
	path, err := recentFile()
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
	path, err := recentFile()
	if err != nil {
		return err
	}
	return writeJSON(path, paths)
}

// Keep identities only; background tabs must not retain buffers.
func (t *repoTab) captureReading() {
	if t.fileSel >= 0 && t.fileSel < len(t.files) {
		f := t.files[t.fileSel]
		if t.resumeDetail != t.shownKey || t.resumeFile != f.layer+":"+f.path {
			t.resumeReading = nil
		}
		t.resumeFile = f.layer + ":" + f.path
		t.resumeDetail = t.shownKey
		if t.diff != nil {
			position := t.diff.ReadingPosition()
			t.resumeReading = &position
		}
	}
}
