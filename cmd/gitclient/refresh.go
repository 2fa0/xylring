package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

func (m repoMeta) key() string {
	parts := []string{m.branch, hashHex(m.head), m.operation}
	for h, refs := range m.refs {
		for _, ref := range refs {
			parts = append(parts, hashHex(h)+refPrefix(ref.kind)+ref.name)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}

func refPrefix(kind refKind) string {
	switch kind {
	case refBranch:
		return "refs/heads/"
	case refRemote:
		return "refs/remotes/"
	default:
		return "refs/tags/"
	}
}

// 只检查活动仓库的引用，不扫描工作区或后台仓库。所有模型读写仍在界面线程。
func (a *app) startAutoRefresh() {
	ctx, cancel := context.WithCancel(context.Background())
	a.watchCancel = cancel
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var tickCount int
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tickCount++
				refreshSummaries := tickCount%5 == 0
				a.onMain(func() {
					if a.win == nil || a.win.IsFocused() && !a.win.IsMinimized() {
						a.checkActiveRepo()
						a.maybeAutoFetch()
						if refreshSummaries && a.showRepoSidebar() {
							a.refreshRepoSummaries()
						}
					}
				})
			}
		}
	}()
}

func (a *app) checkActiveRepo() {
	t := a.activeTab()
	if a.closed || t == nil || t.closed || t.loading || t.checking || !t.loaded || a.showCompare || a.fetch.busy || a.write.busy || t.commitBusy || t.syncBusy {
		return
	}
	t.checking = true
	gen, path := t.generation, t.path
	watch, lastCheck := t.refWatch, t.refsChecked
	unavailable := t.unavailable
	cachedStatus := t.worktreeCache
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		full := unavailable || watch.changed() || time.Since(lastCheck) >= 30*time.Second
		var meta repoMeta
		var err error
		var key string
		replaced := false
		if full {
			next := newRefWatch(ctx, path)
			replaced = watch.replaced(path, next)
			watch = next
			meta, err = loadMeta(ctx, path)
			key = meta.key()
		}
		var dirty []fileChange
		if err == nil {
			dirty, cachedStatus, err = pollWorktreeStatus(ctx, path, cachedStatus)
		}
		dirtyKey := worktreeKey(dirty)
		a.onMain(func() {
			changed := false
			defer func() {
				if changed && a.win != nil {
					a.win.Invalidate()
				}
			}()
			t.checking = false
			if a.closed || t.closed || t.generation != gen || a.activeTab() != t {
				return
			}
			if errors.Is(err, errRepoUnavailable) {
				a.markRepoUnavailable(t)
				changed = true
				return
			}
			if err != nil {
				msg := "检查仓库更新失败：" + err.Error()
				changed, t.err = t.err != msg, msg
				return
			}
			if unavailable || replaced {
				a.resetRepoHistory(t)
				a.reload(t)
				changed = true
				return
			}
			t.worktreeCache = cachedStatus
			if strings.HasPrefix(t.err, "检查仓库更新失败：") {
				t.err = ""
				changed = true
			}
			if full {
				t.refWatch, t.refsChecked = watch, time.Now()
			}
			if full && key != t.metaKey {
				a.reload(t)
				changed = true
				return
			}
			if dirtyKey != t.worktreeKey {
				a.applyWorktree(t, dirty)
				changed = true
			}
		})
	}()
}

func (a *app) focusRepo() {
	if t := a.activeTab(); t != nil {
		t.refsChecked = time.Time{}
	}
	a.checkActiveRepo()
	a.maybeAutoFetch()
	if a.showRepoSidebar() {
		a.refreshRepoSummaries()
	}
}

// Unlike Window.Update, a no-op check must not schedule another UI frame.
func (a *app) onMain(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
	} else if a.win == nil {
		fn()
	} else {
		mygo.RunOnMain(fn)
	}
}

// Discard repository-derived state, keeping the path and the user's draft message.
func (a *app) resetRepoHistory(t *repoTab) {
	t.evictHistory()
	t.refWatch = nil
	a.closeAllFileTabs(t)
	t.restore, t.restoreDiff, t.historyBack, t.fileBack, t.pendingSession = nil, nil, nil, nil, nil
	t.query = historyQuery{hideRemote: t.query.hideRemote}
	t.searchText, t.locate, t.branchFilter = "", "", ""
	t.searchSuggestOpen, t.openFileTabAfterLoad = false, false
	t.branch, t.operation, t.upstream = "", "", ""
	t.head, t.diffFrom, t.diffTo = zeroHash, zeroHash, zeroHash
	t.ahead, t.behind, t.hasUpstream = 0, 0, false
	t.hasMore, t.commitAmend = false, false
	t.ring = ringUI{}
	t.err, t.locateErr, t.notice = "", "", ""
	t.lastFetch = time.Time{}
	t.unavailable = false
}

func (a *app) markRepoUnavailable(t *repoTab) {
	if !t.unavailable {
		a.resetRepoHistory(t)
	}
	t.loaded, t.unavailable = true, true
	t.branch = "仓库不可用"
	t.err = errRepoUnavailable.Error()
}
