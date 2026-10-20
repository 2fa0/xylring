package main

import (
	"context"
	"time"

	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
)

func (a *app) post(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
		return
	}
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *app) activeTab() *repoTab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *app) tabByPath(path string) *repoTab {
	for _, t := range a.tabs {
		if t.path == path {
			return t
		}
	}
	return nil
}

func (a *app) remember(path string) {
	out := []string{path}
	for _, p := range a.recent {
		if p != path && len(out) < 12 {
			out = append(out, p)
		}
	}
	a.recent = out
	if a.persist {
		if err := saveRecent(out); err != nil {
			a.banner = "保存最近仓库失败：" + err.Error()
		}
	}
}

func (a *app) pickRepo() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	go func() {
		paths, err := openRepoDialog(win)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.banner = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 {
				return
			}
			a.openPath(paths[0])
		})
	}()
}

func (a *app) openPath(path string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		root, err := repoRoot(ctx, path)
		a.post(func() {
			if err != nil {
				a.banner = trimMsg(err.Error())
				return
			}
			a.openRoot(root)
		})
	}()
}

func (a *app) openRoot(root string) {
	for i, t := range a.tabs {
		if t.path == root {
			a.activate(i)
			a.remember(root)
			return
		}
	}
	if cur := a.activeTab(); cur != nil {
		cur.releaseHeavy()
	}
	a.nextID++
	tab := newTab(a.nextID, root)
	a.tabs = append(a.tabs, tab)
	a.active = len(a.tabs) - 1
	a.banner = ""
	a.remember(root)
	a.reload(tab)
	a.saveSession()
}

func (a *app) activate(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	if i == a.active && a.tabs[i].loaded {
		return
	}
	if cur := a.activeTab(); cur != nil && a.active != i {
		cur.releaseHeavy()
	}
	a.active = i
	tab := a.tabs[i]
	if !tab.loaded && !tab.loading {
		a.reload(tab)
	}
	a.saveSession()
}

func (a *app) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs[i].releaseHeavy()
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if len(a.tabs) == 0 {
		a.active = 0
		a.saveSession()
		return
	}
	if a.active > i {
		a.active--
	}
	if a.active >= len(a.tabs) {
		a.active = len(a.tabs) - 1
	}
	tab := a.tabs[a.active]
	if !tab.loaded && !tab.loading {
		a.reload(tab)
	}
	a.saveSession()
}

func (a *app) reload(t *repoTab) {
	t.captureReading()
	t.captureSelection()
	t.cancelReads()
	t.options = a.options(t.path)
	t.palette, _ = parsePalette(t.options.Colors)
	if t.tableLayout.Widths == nil {
		t.tableLayout = t.options.Layout
	}
	t.generation++
	gen := t.generation
	t.loading = true
	t.historyStarted = time.Now()
	t.loaded = false
	t.released = false
	t.err = ""
	t.commits = nil
	t.graph = graph{}
	t.refs = nil
	t.refChoices = nil
	t.stashes = nil
	t.stashView = nil
	t.viewer = nil
	t.dirCache = nil
	t.dirBusy = nil
	t.authors = map[string]string{}
	t.hasMore = false
	t.files = nil
	t.info = nil
	t.diff = nil
	t.detailLoading = false
	t.detailErr = ""
	t.detailNote = ""
	t.diffLoading = false
	t.shownKey = ""
	t.selected = -1
	t.commitApplied = -1
	t.selection.Clear()
	t.fileSel = -1
	t.fileApplied = -1
	path := t.path
	count := t.historyCount()
	refs := append([]string(nil), t.historyRefs...)
	options := t.options
	historyPath, searchText := t.historyPath, t.searchText
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		meta, metaErr := loadMeta(ctx, path, options.IncludeUntracked)
		var commits []commit
		var logErr error
		if metaErr == nil {
			logErr = streamLog(ctx, path, 0, count, logOptions{Refs: refs, Path: historyPath, Query: searchText, Names: meta.refs}, func(c commit) error {
				commits = append(commits, c)
				return nil
			})
		}
		a.post(func() {
			if t.generation != gen {
				return
			}
			t.loading = false
			if metaErr != nil {
				t.err = metaErr.Error()
				t.loaded = true
				return
			}
			if logErr != nil {
				t.err = logErr.Error()
				t.loaded = true
				return
			}
			t.branch = meta.branch
			t.head = meta.head
			t.dirty = meta.dirty
			t.refs = meta.refs
			t.refChoices = nil
			t.stashes = meta.stashes
			t.pending = meta.pending
			t.graph = graph{}
			t.layoutStashes()
			for i := range commits {
				commits[i].author = t.intern(commits[i].author)
				if t.searchText == "" {
					t.graph.add(&commits[i])
				}
			}
			t.commits = commits
			t.hasMore = len(commits) == count
			t.loaded = true
			t.holdAuto = true
			t.reselect()
			if t.detailHidden {
				t.commitApplied = t.selected
			}
			if !t.wantFirst && t.detailHidden && t.searchText != "" && t.selected >= 0 {
				a.selectSearchResult(t, t.rowCommit(t.selected))
			}
			if t.wantFirst {
				t.wantFirst = false
				a.selectSearchResult(t, 0)
			}
			if t.resumeRow >= 0 && t.rows() > 0 {
				t.list.ScrollTo(min(t.resumeRow, t.rows()-1), ui.Start)
			}
			if !t.detailHidden && (t.comparison != nil || t.stashView != nil || t.selected >= 0) {
				t.commitApplied = t.selected
				a.onCommitSelected(t)
			}
			a.sampleNow()
		})
	}()
}

func (a *app) loadMore(t *repoTab) {
	if t.loading || !t.hasMore {
		return
	}
	t.loading = true
	t.historyStarted = time.Now()
	t.err = ""
	gen := t.generation
	skip := len(t.commits)
	count := t.options.More
	path := t.path
	refs := append([]string(nil), t.historyRefs...)
	queryOptions := logOptions{Refs: refs, Path: t.historyPath, Query: t.searchText, Names: t.refs}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		var commits []commit
		err := streamLog(ctx, path, skip, count, queryOptions, func(c commit) error {
			commits = append(commits, c)
			return nil
		})
		a.post(func() {
			if t.generation != gen {
				return
			}
			t.loading = false
			if err != nil {
				t.err = err.Error()
				return
			}
			for i := range commits {
				commits[i].author = t.intern(commits[i].author)
				if t.searchText == "" {
					t.graph.add(&commits[i])
				}
			}
			t.commits = append(t.commits, commits...)
			t.hasMore = len(commits) == count
			if t.wantNext {
				t.wantNext = false
				if len(commits) > 0 {
					a.selectSearchResult(t, skip)
				} else if len(t.commits) > 0 {
					a.selectSearchResult(t, 0)
				}
			}
			t.holdAuto = true
			a.sampleNow()
		})
	}()
}

func (a *app) refreshRefs(t *repoTab) {
	if t.refsCancel != nil {
		t.refsCancel()
	}
	gen := t.generation
	path := t.path
	includeUntracked := t.options.IncludeUntracked
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.refsCancel = cancel
	go func() {
		defer cancel()
		meta, err := loadMeta(ctx, path, includeUntracked)
		a.post(func() {
			if err != nil || t.generation != gen {
				return
			}
			t.refs = meta.refs
			t.refChoices = nil
			t.stashes = meta.stashes
			t.pending = meta.pending
			t.branch = meta.branch
			t.head = meta.head
			t.dirty = meta.dirty
		})
	}()
}

func (a *app) ensureWorktreeFiles(t *repoTab) {
	if t.shownKey == "worktree" && (t.files != nil || t.detailLoading) {
		return
	}
	if t.detailCancel != nil {
		t.detailCancel()
	}
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.detailGen++
	t.diffGen++
	t.diffOpen = false
	t.fileSel = -1
	t.fileApplied = -1
	t.shownKey = "worktree"
	t.files = nil
	t.info = nil
	t.diff = nil
	t.detailErr = ""
	t.detailNote = ""
	t.detailLoading = true
	t.detailStarted = time.Now()
	gen, repoGen := t.detailGen, t.generation
	path := t.path
	include := t.options.IncludeUntracked
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.detailCancel = cancel
	go func() {
		defer cancel()
		files, err := statusFiles(ctx, path, include)
		a.post(func() {
			if t.generation != repoGen || t.detailGen != gen {
				return
			}
			t.detailLoading = false
			if err != nil {
				t.detailErr = err.Error()
				return
			}
			t.files = files
		})
	}()
}

func (a *app) onCommitSelected(t *repoTab) {
	key := t.selectionKey()
	if key == t.shownKey && key != "" && (t.files != nil || t.detailLoading) {
		return
	}
	t.diffOpen = false
	if t.detailCancel != nil {
		t.detailCancel()
	}
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.detailGen++
	t.viewer = nil
	t.fileMode = "diff"
	t.diffLoading = false
	if key == "" {
		t.shownKey = ""
		t.files = nil
		t.info = nil
		t.diff = nil
		t.detailErr = ""
		t.detailNote = ""
		t.detailLoading = false
		return
	}
	t.shownKey = key
	t.files = nil
	t.info = nil
	t.diff = nil
	t.detailErr = ""
	t.detailNote = ""
	t.fileSel = -1
	t.fileApplied = -1
	gen := t.detailGen
	repoGen := t.generation
	path := t.path
	uncommitted := key == "u"
	includeUntracked := t.options.IncludeUntracked
	comparison := t.comparison
	var hash string
	historyPath := ""
	stash := false
	if c := t.selectedCommit(); c != nil {
		hash = hashHex(c.hash)
		stash = c.stash != ""
		historyPath = c.historyPath
	}
	t.detailLoading = true
	t.detailStarted = time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.detailCancel = cancel
	go func() {
		defer cancel()
		var files []fileChange
		var info *commitInfo
		var err error
		if comparison != nil {
			files, err = compareFiles(ctx, path, hashHex(comparison.from), hashHex(comparison.to))
		} else if uncommitted {
			files, err = statusFiles(ctx, path, includeUntracked)
		} else {
			info, err = loadCommitInfo(ctx, path, hash)
			if err == nil {
				if stash {
					files, err = stashFiles(ctx, path, hash)
				} else {
					files, err = commitFiles(ctx, path, hash)
				}
				if historyPath != "" {
					filtered := files[:0]
					for _, f := range files {
						if f.path == historyPath || f.oldPath == historyPath {
							filtered = append(filtered, f)
						}
					}
					files = filtered
				}
			}
		}
		a.post(func() {
			if t.generation != repoGen || t.detailGen != gen {
				return
			}
			t.detailLoading = false
			if err != nil {
				t.detailErr = err.Error()
				return
			}
			t.files = files
			t.info = info
			t.fileSel = -1
			t.fileApplied = -1
			t.diffOpen = false
			if t.resumeDetail == key && t.resumeFile != "" {
				for i, f := range files {
					if f.layer+":"+f.path == t.resumeFile {
						t.fileSel = i
						t.diffOpen = true
						t.leftPane = "改动"
						break
					}
				}
				if t.fileSel < 0 {
					t.detailNote = "原先阅读的文件已不在此改动中，请重新选择文件"
				}
			}
		})
	}()
}

func (a *app) onFileSelected(t *repoTab) {
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.diffGen++
	if t.fileSel < 0 || t.fileSel >= len(t.files) {
		t.diff = nil
		t.detailNote = ""
		t.diffLoading = false
		return
	}
	t.viewer = nil
	file := t.files[t.fileSel]
	mode := t.fileMode
	gen := t.diffGen
	repoGen := t.generation
	detailGen := t.detailGen
	t.diffLoading = true
	t.diffStarted = time.Now()
	t.detailErr = ""
	t.detailNote = ""
	t.diff = nil
	path := t.path
	uncommitted := t.shownKey == "u"
	var hash string
	var parent string
	if c := t.selectedCommit(); c != nil {
		hash = hashHex(c.hash)
		if len(c.parents) > 0 {
			parent = hashHex(c.parents[0])
		}
	}
	if file.oldRev != "" {
		parent = file.oldRev
	}
	if file.newRev != "" {
		hash = file.newRev
	}
	if t.comparison != nil {
		parent, hash = hashHex(t.comparison.from), hashHex(t.comparison.to)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.diffCancel = cancel
	go func() {
		defer cancel()
		var oldText, newText, note string
		var err error
		if mode == "worktree" {
			oldText, newText, note, err = worktreeSides(ctx, path, hash, file.path)
		} else {
			oldText, newText, note, err = loadSides(ctx, path, file, uncommitted, hash, parent)
		}
		a.post(func() {
			if t.generation != repoGen || t.detailGen != detailGen || t.diffGen != gen {
				return
			}
			t.diffLoading = false
			if err != nil {
				t.detailErr = err.Error()
				return
			}
			if note != "" {
				t.detailNote = note
				return
			}
			oldBuf := editor.NewBuffer(oldText)
			newBuf := editor.NewBuffer(newText)
			lang := editor.LanguageFor(file.path)
			oldBuf.SetLanguage(lang)
			newBuf.SetLanguage(lang)
			if t.diff == nil {
				t.diff = &editor.DiffState{Layout: t.layout}
			}
			t.diff.Old = oldBuf
			t.diff.New = newBuf
			t.diff.Layout = t.layout
			t.diff.Font.Family = "monospace"
			t.diff.Font.Size = 13
			if t.resumeReading != nil && t.resumeDetail == t.shownKey && t.resumeFile == file.layer+":"+file.path {
				t.diff.RestoreReadingPosition(*t.resumeReading)
			}
		})
	}()
}

func loadSides(ctx context.Context, dir string, file fileChange, uncommitted bool, hash, parent string) (oldText, newText, note string, err error) {
	oldRev, newRev := parent, hash
	oldPath := file.path
	if file.oldPath != "" {
		oldPath = file.oldPath
	}
	worktree := false
	if uncommitted {
		worktree = file.status != "D"
		oldRev = "HEAD"
		if file.status == "A" || file.status == "?" {
			oldRev = ""
		}
		newRev = ""
	} else {
		if file.status == "A" {
			oldRev = ""
		}
		if file.status == "D" {
			newRev = ""
		}
	}
	oldb, oldNote, err := readBlob(ctx, dir, oldRev, oldPath)
	if err != nil {
		return "", "", "", err
	}
	var newb []byte
	var newNote string
	if worktree {
		newb, newNote, err = readWorktree(dir, file.path)
	} else {
		newb, newNote, err = readBlob(ctx, dir, newRev, file.path)
	}
	if err != nil {
		return "", "", "", err
	}
	if oldNote != "" {
		return "", "", oldNote, nil
	}
	if newNote != "" {
		return "", "", newNote, nil
	}
	return string(oldb), string(newb), "", nil
}

func (a *app) findNext(t *repoTab) { a.searchNext(t) }

func (a *app) jumpHash(t *repoTab, h [20]byte) {
	for i := range t.commits {
		if t.commits[i].hash == h {
			t.selected = t.commitRow(i)
			t.list.ScrollIntoView(t.selected)
			return
		}
	}
	a.openRef(t, hashHex(h))
}

func (a *app) setFlash(s string) {
	a.flash = s
	a.flashUntil = time.Now().Add(4 * time.Second)
}

func (a *app) sampleNow() {
	a.heap, a.heapAt = readHeap(), time.Now()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
