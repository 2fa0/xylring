package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
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

func (a *app) setBanner(msg, path string) {
	a.banner = msg
	a.bannerPath = path
}

func (a *app) notifySuccess(tab *repoTab, msg string) {
	if tab != nil {
		tab.notice = msg
	}
	a.pendingToast = msg
}

func (a *app) bannerOnRecent() bool {
	if a.banner == "" || a.bannerPath == "" {
		return false
	}
	return slices.Contains(a.recent, a.bannerPath)
}

func (a *app) activeTab() *repoTab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *app) remember(path string) {
	out := []string{path}
	for _, p := range a.recent {
		if p != path && len(out) < 12 {
			out = append(out, p)
		}
	}
	a.setRecent(out)
}

func (a *app) forget(path string) {
	out := make([]string, 0, len(a.recent))
	for _, p := range a.recent {
		if p != path {
			out = append(out, p)
		}
	}
	if len(out) == len(a.recent) {
		return
	}
	if a.bannerPath == path {
		a.setBanner("", "")
	}
	a.setRecent(out)
}

func (a *app) setRecent(out []string) {
	a.recent = out
	if !a.persist {
		return
	}
	if a.recentWrites == nil {
		a.recentWrites = make(chan []string, 1)
		writes := a.recentWrites
		go func() {
			for paths := range writes {
				if err := saveRecent(paths); err != nil {
					a.post(func() {
						if !a.closed {
							a.setBanner("保存最近仓库失败："+err.Error(), "")
						}
					})
				}
			}
		}()
	}
	select {
	case <-a.recentWrites:
	default:
	}
	a.recentWrites <- out
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
				a.setBanner(trimMsg(err.Error()), "")
				return
			}
			if len(paths) == 0 {
				return
			}
			a.openPath(paths[0])
		})
	}()
}

func (a *app) openRepo(path string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		root, err := repoRoot(ctx, path)
		a.post(func() {
			if err != nil {
				a.setBanner(trimMsg(err.Error()), path)
				return
			}
			a.openRoot(root)
			// 导入目录会扫描它下面的仓库；最近仓库只留了一个路径，打开后补扫一次，
			// 仓库里嵌套的仓库照旧列在左侧。
			a.scanRepos(root, false)
		})
	}()
}

func (a *app) openPath(path string) {
	a.showHome = false
	a.scanRepos(path, true)
}

func (a *app) refresh(t *repoTab) {
	a.reload(t)
	a.rescanFound()
}

func (a *app) rescanFound() {
	from := a.foundFrom
	if from == "" {
		if tab := a.activeTab(); tab != nil {
			from = tab.path
		}
	}
	if from != "" {
		a.scanRepos(from, false)
	}
}

func (a *app) scanRepos(path string, open bool) {
	if a.closed {
		return
	}
	// Repeated refreshes share the in-flight scan; a new import supersedes it.
	if !open && a.scanPath == path && a.scanCancel != nil {
		return
	}
	if a.scanCancel != nil {
		a.scanCancel()
	}
	a.foundGen++
	gen := a.foundGen
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.scanCancel, a.scanPath = cancel, path
	previous := a.scanCache
	go func() {
		defer cancel()
		from := workspaceDir(path)
		roots, cache, err := scanGitRepos(ctx, path, previous)
		a.post(func() {
			if a.closed || a.foundGen != gen {
				return
			}
			a.scanCancel, a.scanPath = nil, ""
			if err != nil {
				if open {
					a.found, a.foundFrom = nil, ""
					a.setBanner(trimMsg(err.Error()), path)
				}
				return
			}
			a.setBanner("", "")
			a.scanCache = cache
			a.setFound(from, roots)
			if open && len(roots) > 0 {
				a.openRoot(roots[0])
			}
		})
	}()
}

func (a *app) setFound(from string, roots []string) {
	a.foundFrom = from
	if len(roots) > 1 {
		a.found = roots
		a.refreshRepoSummaries()
		return
	}
	a.found = nil
}

func workspaceDir(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		return filepath.Dir(abs)
	}
	return abs
}

func (a *app) openRoot(root string) {
	if a.closed {
		return
	}
	a.showHome = false
	for i, t := range a.tabs {
		if t.path == root {
			a.activate(i)
			a.checkActiveRepo()
			a.remember(root)
			return
		}
	}
	a.nextID++
	tab := newTab(a.nextID, root)
	a.tabs = append(a.tabs, tab)
	a.activate(len(a.tabs) - 1)
	a.setBanner("", "")
	a.remember(root)
	if !tab.loading {
		a.reload(tab)
	}
}

func (a *app) activate(i int) {
	if a.active != i {
		if old := a.activeTab(); old != nil {
			old.stopHistory()
			old.generation++
			old.loading = false
		}
	}
	a.active = i
	if t := a.activeTab(); t != nil {
		t.usedAt = time.Now()
	}
	a.trimInactiveHistory()
	if t := a.activeTab(); t != nil && !t.loaded && !t.loading {
		a.reload(t)
	}
}

func (a *app) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs[i].closed = true
	a.tabs[i].cancel()
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active > i || a.active >= len(a.tabs) {
		a.active--
	}
	if a.active < 0 {
		a.active = 0
	}
	if len(a.tabs) == 0 {
		a.recentSearch = ""
	}
	if t := a.activeTab(); !a.closed && t != nil {
		if !t.loaded && !t.loading {
			a.reload(t)
		} else {
			a.checkActiveRepo()
		}
	}
}

func (a *app) goHome() {
	if a.scanCancel != nil {
		a.scanCancel()
		a.scanCancel, a.scanPath = nil, ""
	}
	a.setBanner("", "")
	a.recentSearch = ""
	a.showCompare = false
	a.showWrite = false
	a.stopFetch()
	a.showHome = true
}

func (a *app) reload(t *repoTab) {
	if a.closed || t.closed {
		return
	}
	t.stopHistory()
	t.generation++
	t.locateGen++
	gen := t.generation
	t.loading = true
	t.historyStarted = time.Now()
	t.err, t.locateErr, t.notice = "", "", ""
	t.locateHash = zeroHash
	path, query := t.path, t.query
	n := max(pageFirst, len(t.commits))
	budget := max(int64(historyMemoryStep), t.historyBudget)
	location := t.location()
	streamCtx, streamCancel := context.WithCancel(context.Background())
	t.pagerCancel = streamCancel
	ctx, cancel := context.WithTimeout(streamCtx, 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		watch := newRefWatch(ctx, path)
		meta, err := loadMeta(ctx, path)
		var tips []string
		var dirty []fileChange
		if err == nil {
			tips, err = queryTips(ctx, path, meta, query)
		}
		if err == nil {
			dirty, _ = worktreeStatus(ctx, path)
		}
		var p *historyPager
		var prepared preparedHistory
		more := false
		if err == nil {
			p = newHistoryPager(streamCtx, path, query, tips, 0)
			var commits []commit
			commits, more, err = p.next(ctx, n, budget)
			// New commits can push the old anchor just beyond the loaded range. Probe
			// one extra batch, never exhaust the repository looking for a vanished hash.
			var readBytes int64
			for i := range commits {
				readBytes += commitBytes(&commits[i])
			}
			if err == nil && more && readBytes < budget && !containsLocation(commits, location) {
				var extra []commit
				extra, more, err = p.next(ctx, pageFirst, budget-readBytes)
				commits = append(commits, extra...)
			}
			if err == nil {
				prepared, err = prepareHistory(ctx, commits, graph{}, nil, query.term == "" && query.filePath == "")
			}
		}
		key := meta.key()
		a.post(func() {
			if t.generation != gen || t.closed {
				if p != nil {
					p.close()
				}
				return
			}
			t.loading, t.loaded = false, true
			if err != nil {
				if p != nil {
					p.close()
				}
				t.err = err.Error()
				return
			}
			t.historyTips, t.metaKey, t.pager = tips, key, p
			t.refWatch, t.refsChecked = watch, time.Now()
			if a.activeTab() != t || !more {
				p.close()
				t.pager = nil
			}
			t.hasMore = more
			a.installHistory(t, meta, prepared)
			a.applyWorktree(t, dirty)
		})
	}()
}

func containsLocation(commits []commit, location *historyLocation) bool {
	foundHash, foundTop := location.hash == zeroHash, location.top == zeroHash
	for _, c := range commits {
		foundHash = foundHash || c.hash == location.hash
		foundTop = foundTop || c.hash == location.top
	}
	return foundHash && foundTop
}

func (a *app) loadMore(t *repoTab) {
	if t.loading || !t.hasMore || t.closed {
		return
	}
	if !t.canLoadHistory() {
		t.notice = "历史已达到本轮内存预算，可点击继续加载"
		return
	}
	t.loading = true
	t.historyStarted = time.Now()
	t.err = ""
	gen, query := t.generation, t.query
	if t.pager == nil {
		streamCtx, cancel := context.WithCancel(context.Background())
		t.pagerCancel = cancel
		t.pager = newHistoryPager(streamCtx, t.path, query, t.historyTips, len(t.commits))
	}
	p := t.pager
	base, authors, g, baseBytes := t.commits, t.authors, t.graph.clone(), t.historyBytes
	meta := repoMeta{branch: t.branch, head: t.head, refs: t.refs, ahead: t.ahead, behind: t.behind, upstream: t.upstream, hasUpstream: t.hasUpstream}
	n := min(4096, max(pageMore, len(base)/2))
	remaining := t.historyBudget - t.historyBytes
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		commits, more, err := p.next(ctx, n, remaining)
		var prepared preparedHistory
		if err == nil {
			prepared, err = prepareHistory(ctx, commits, g, authors, query.term == "" && query.filePath == "")
			if err == nil {
				all := make([]commit, 0, len(base)+len(commits))
				all = append(all, base...)
				prepared.commits = append(all, prepared.commits...)
				for h, i := range prepared.index {
					prepared.index[h] = i + len(base)
				}
				for i, c := range base {
					prepared.index[c.hash] = i
				}
				prepared.bytes += baseBytes
			}
		}
		a.post(func() {
			if t.generation != gen || t.closed {
				p.close()
				return
			}
			t.loading = false
			if err != nil {
				p.close()
				t.pager = nil
				t.err, t.locateHash = err.Error(), zeroHash
				return
			}
			if !more || a.activeTab() != t {
				p.close()
				t.pager = nil
			}
			t.hasMore = more
			a.installHistory(t, meta, prepared)
		})
	}()
}

func (t *repoTab) canLoadHistory() bool {
	if t.historyBudget == 0 {
		t.historyBudget = historyMemoryStep
	}
	return t.historyBytes < t.historyBudget
}

// Also used by small synchronous model tests; production prepares on the worker.
func (a *app) applyHistory(t *repoTab, meta repoMeta, commits []commit, _, page int) {
	prepared, _ := prepareHistory(context.Background(), commits, graph{}, nil, t.query.term == "" && t.query.filePath == "")
	if page >= 0 {
		t.hasMore = len(commits) == page
	}
	a.installHistory(t, meta, prepared)
}

func (a *app) installHistory(t *repoTab, meta repoMeta, p preparedHistory) {
	selected := zeroHash
	if item := t.rowData(t.selected); item != nil {
		selected = item.hash
	}
	t.branch, t.head, t.refs = meta.branch, meta.head, meta.refs
	t.ahead, t.behind, t.upstream, t.hasUpstream = meta.ahead, meta.behind, meta.upstream, meta.hasUpstream
	a.summaryMu.Lock()
	if a.repoSummaries != nil {
		a.repoSummaries[t.path] = repoSummary{
			branch:      t.branch,
			dirty:       t.worktreeCount > 0,
			checked:     time.Now(),
			ahead:       t.ahead,
			behind:      t.behind,
			upstream:    t.upstream,
			hasUpstream: t.hasUpstream,
		}
	}
	a.summaryMu.Unlock()
	t.graph, t.authors, t.commitIndex, t.historyBytes = p.graph, p.authors, p.index, p.bytes
	t.commits = p.commits
	t.selected = t.rowIndex(selected)
	for key := range t.selection.All() {
		h, ok := key.([20]byte)
		if !ok || t.rowIndex(h) < 0 {
			t.selection.Remove(key)
		}
	}
	if t.inspecting != zeroHash && t.indexOf(t.inspecting) < 0 {
		a.closeInspection(t)
		t.notice = "原提交已不在当前范围内"
	}
	t.holdAuto = true
	a.continueRestore(t)
	a.continueLocate(t)
	a.trimInactiveHistory()
}

func (a *app) locateRef(t *repoTab, ref string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	t.locateErr = ""
	t.locateGen++
	gen, repoGen := t.locateGen, t.generation
	path := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		hash, err := resolveCommit(ctx, path, ref)
		a.post(func() {
			if t.generation != repoGen || t.locateGen != gen {
				return
			}
			if err != nil {
				t.locateErr = err.Error()
				return
			}
			a.jumpToHash(t, hash)
		})
	}()
}

func (a *app) jumpToHash(t *repoTab, hash [20]byte) {
	if i := t.rowIndex(hash); i >= 0 {
		t.locateHash = zeroHash
		a.selectCommit(t, i)
		return
	}
	if t.loading {
		t.locateHash = hash
		return
	}
	if !t.hasMore {
		t.locateHash = zeroHash
		t.locateErr = "图谱里没有这个提交"
		if t.query.term != "" || t.query.filePath != "" || t.query.ref != "" || t.query.hideRemote {
			t.locateErr = "当前范围内没有这个提交"
		}
		return
	}
	t.locateHash = hash
	a.loadMore(t)
}

func (a *app) continueLocate(t *repoTab) {
	if t.locateHash == zeroHash {
		return
	}
	a.jumpToHash(t, t.locateHash)
}

func (a *app) selectCommit(t *repoTab, i int) {
	item := t.rowData(i)
	if item == nil {
		return
	}
	if a.sidebarHidden {
		a.sidebarHidden = false
	}
	if item.hash == worktreeHash {
		a.showWorktree(t)
		t.list.ScrollIntoView(i)
		return
	}
	t.worktree = false
	if t.comparison != nil {
		a.closeComparison(t)
	}
	t.selected = i
	t.selection.Clear()
	t.selection.Add(item.hash)
	t.list.ScrollIntoView(i)
	a.showInspection(t, item)
}
