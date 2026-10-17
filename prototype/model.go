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

// commit 只保留列表和图谱要用的字段。正文、文件列表和 diff 在打开时再取。
type commit struct {
	hash    [20]byte
	parents [][20]byte
	author  string
	when    int64
	subject string
	nodeCol int
	segs    []seg
}

func (c commit) whenText() string {
	if c.when == 0 {
		return ""
	}
	return time.Unix(c.when, 0).Local().Format("2006-01-02 15:04")
}

type repoTab struct {
	id    int
	path  string
	title string

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

	branch  string
	head    [20]byte
	dirty   int
	refs    map[[20]byte][]refInfo
	commits []commit
	graph   graph
	hasMore bool
	authors map[string]string

	list          ui.ListState
	selected      int
	commitApplied int
	wantHash      [20]byte
	wantDirty     bool

	query string

	detailGen     int
	detailLoading bool
	detailErr     string
	shownKey      string
	files         []fileChange
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
		path:          path,
		title:         repoTitle(path),
		selected:      -1,
		commitApplied: -1,
		fileSel:       -1,
		fileApplied:   -1,
		resumeRow:     -1,
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

func (t *repoTab) rows() int {
	n := len(t.commits)
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
		return row - 1
	}
	return row
}

func (t *repoTab) commitRow(i int) int {
	if t.dirty > 0 {
		return i + 1
	}
	return i
}

func (t *repoTab) captureSelection() {
	if len(t.commits) > 0 {
		t.resumeCount = len(t.commits)
		t.resumeRow, _ = t.list.Visible()
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
	ci := t.rowCommit(t.selected)
	if ci >= 0 && ci < len(t.commits) {
		t.wantHash = t.commits[ci].hash
	}
}

func (t *repoTab) historyCount() int {
	return max(pageFirst, t.resumeCount)
}

func (t *repoTab) cancelReads() {
	for _, cancel := range []context.CancelFunc{t.historyCancel, t.refsCancel, t.detailCancel, t.diffCancel} {
		if cancel != nil {
			cancel()
		}
	}
	t.historyCancel, t.refsCancel, t.detailCancel, t.diffCancel = nil, nil, nil, nil
}

func (t *repoTab) reselect() {
	if t.wantDirty && t.dirty > 0 {
		t.selected = 0
		return
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
	if t.dirty > 0 && t.selected == 0 {
		return "u"
	}
	ci := t.rowCommit(t.selected)
	if ci < 0 || ci >= len(t.commits) {
		return ""
	}
	return hashHex(t.commits[ci].hash)
}

func (t *repoTab) selectedCommit() *commit {
	ci := t.rowCommit(t.selected)
	if ci < 0 || ci >= len(t.commits) {
		return nil
	}
	return &t.commits[ci]
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
	t.captureSelection()
	t.cancelReads()
	t.generation++
	t.loading = false
	t.loaded = false
	t.released = true
	t.err = ""
	t.commits = nil
	t.graph = graph{}
	t.refs = nil
	t.authors = nil
	t.hasMore = false
	t.files = nil
	t.diff = nil
	t.detailLoading = false
	t.detailErr = ""
	t.detailNote = ""
	t.diffLoading = false
	t.shownKey = ""
	t.selected = -1
	t.commitApplied = -1
	t.fileSel = -1
	t.fileApplied = -1
	t.list = ui.ListState{}
	t.fileList = ui.ListState{}
}

func (t *repoTab) match(i int, q string) bool {
	c := t.commits[i]
	if strings.Contains(strings.ToLower(c.subject), q) || strings.Contains(strings.ToLower(c.author), q) {
		return true
	}
	return strings.Contains(hashHex(c.hash), q)
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

func recentFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "GitClientPrototype")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "recent.json"), nil
}

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

func saveRecent(paths []string) {
	path, err := recentFile()
	if err != nil {
		return
	}
	b, err := json.MarshalIndent(paths, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}
