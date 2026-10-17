package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
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
	saveRecent(out)
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
}

func (a *app) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs[i].releaseHeavy()
	a.tabs = append(a.tabs[:i], a.tabs[i+1:]...)
	if len(a.tabs) == 0 {
		a.active = 0
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
}

func (a *app) reload(t *repoTab) {
	t.captureSelection()
	t.cancelReads()
	t.generation++
	gen := t.generation
	t.loading = true
	t.loaded = false
	t.released = false
	t.err = ""
	t.commits = nil
	t.graph = graph{}
	t.refs = nil
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
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		meta, metaErr := loadMeta(ctx, path)
		var commits []commit
		var logErr error
		if metaErr == nil {
			logErr = streamHistory(ctx, path, 0, count, refs, func(c commit) error {
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
			t.graph = graph{}
			for i := range commits {
				commits[i].author = t.intern(commits[i].author)
				t.graph.add(&commits[i])
			}
			t.commits = commits
			t.hasMore = len(commits) == count
			t.loaded = true
			t.holdAuto = true
			t.reselect()
			if t.resumeRow >= 0 && t.rows() > 0 {
				t.list.ScrollTo(min(t.resumeRow, t.rows()-1), ui.Start)
			}
			if t.comparison != nil {
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
	t.err = ""
	gen := t.generation
	skip := len(t.commits)
	path := t.path
	refs := append([]string(nil), t.historyRefs...)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		var commits []commit
		err := streamHistory(ctx, path, skip, pageMore, refs, func(c commit) error {
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
				t.graph.add(&commits[i])
			}
			t.commits = append(t.commits, commits...)
			t.hasMore = len(commits) == pageMore
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.refsCancel = cancel
	go func() {
		defer cancel()
		meta, err := loadMeta(ctx, path)
		a.post(func() {
			if err != nil || t.generation != gen {
				return
			}
			t.refs = meta.refs
			t.branch = meta.branch
			t.head = meta.head
			t.dirty = meta.dirty
		})
	}()
}

func (a *app) onCommitSelected(t *repoTab) {
	key := t.selectionKey()
	if key == t.shownKey && key != "" && (t.files != nil || t.detailLoading) {
		return
	}
	if t.detailCancel != nil {
		t.detailCancel()
	}
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.detailGen++
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
	comparison := t.comparison
	var hash string
	if c := t.selectedCommit(); c != nil {
		hash = hashHex(c.hash)
	}
	t.detailLoading = true
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
			files, err = uncommittedFiles(ctx, path)
		} else {
			info, err = loadCommitInfo(ctx, path, hash)
			if err == nil {
				files, err = commitFiles(ctx, path, hash)
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
			if len(files) > 0 {
				t.fileSel = 0
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
	file := t.files[t.fileSel]
	gen := t.diffGen
	repoGen := t.generation
	detailGen := t.detailGen
	t.diffLoading = true
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
	if t.comparison != nil {
		parent, hash = hashHex(t.comparison.from), hashHex(t.comparison.to)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.diffCancel = cancel
	go func() {
		defer cancel()
		oldText, newText, note, err := loadSides(ctx, path, file, uncommitted, hash, parent)
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

func (a *app) findNext(t *repoTab) {
	q := strings.ToLower(strings.TrimSpace(t.query))
	if q == "" || len(t.commits) == 0 {
		a.setFlash("先输入要查找的内容")
		return
	}
	start := t.rowCommit(t.selected)
	n := len(t.commits)
	for k := 1; k <= n; k++ {
		i := (start + k) % n
		if i < 0 {
			i += n
		}
		if t.match(i, q) {
			t.selected = t.commitRow(i)
			t.list.ScrollIntoView(t.selected)
			a.setFlash("第 " + itoa(k) + " 个匹配，在已加载的 " + itoa(n) + " 条里")
			return
		}
	}
	a.setFlash("已加载的提交里没有匹配")
}

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

func (a *app) createBranch() {
	if a.branchBusy {
		return
	}
	name := strings.TrimSpace(a.branchName)
	if msg := validBranch(name); msg != "" {
		a.branchErr = msg
		return
	}
	a.branchBusy = true
	a.branchErr = ""
	repo := a.branchRepo
	hash := a.branchHash
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, err := runGit(ctx, repo, "branch", name, hash)
		a.post(func() {
			a.branchBusy = false
			if err != nil {
				a.branchErr = err.Error()
				return
			}
			a.branching = false
			a.branchName = ""
			a.setFlash("已创建分支 " + name)
			if t := a.tabByPath(repo); t != nil && t.loaded {
				a.refreshRefs(t)
			}
		})
	}()
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
