package main

import (
	"context"
	"strings"
	"time"
)

type repoSummary struct {
	branch      string
	dirty       bool
	checked     time.Time
	ahead       int
	behind      int
	upstream    string
	hasUpstream bool
}

func queryRepoSummary(ctx context.Context, dir string) repoSummary {
	var s repoSummary
	s.checked = time.Now()
	if dir == "" {
		return s
	}
	if !isGitWorkTree(dir) {
		s.branch = "仓库不可用"
		return s
	}
	branch, err := runGit(ctx, dir, "symbolic-ref", "-q", "--short", "HEAD")
	if err == nil {
		s.branch = strings.TrimSpace(string(branch))
	} else if short, serr := runGit(ctx, dir, "rev-parse", "--short", "HEAD"); serr == nil {
		s.branch = "分离头指针 " + strings.TrimSpace(string(short))
	} else {
		if _, e := runGit(ctx, dir, "rev-parse", "--git-dir"); e != nil {
			s.branch = "仓库不可用"
		} else {
			s.branch = "尚无提交"
		}
	}
	out, err := runGit(ctx, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err == nil {
		s.dirty = len(out) > 0
	}
	sync, _ := queryTrackingStatus(ctx, dir)
	s.hasUpstream = sync.hasUpstream
	s.upstream = sync.upstream
	s.ahead = sync.ahead
	s.behind = sync.behind
	return s
}

func sidebarBranchDisplay(branch string, dirty bool) string {
	if branch == "" {
		if dirty {
			return "*"
		}
		return ""
	}
	b := branch
	if strings.HasPrefix(b, "分离头指针 ") {
		b = "(" + strings.TrimPrefix(b, "分离头指针 ") + ")"
	} else if b == "尚无提交" {
		b = "(" + tr("尚无提交") + ")"
	}
	if dirty {
		b += "*"
	}
	return b
}

func (a *app) repoStatus(path string) (branch string, dirty bool, ahead, behind int) {
	if tab := a.activeTab(); tab != nil && tab.path == path && tab.loaded {
		return tab.branch, tab.worktreeCount > 0, tab.ahead, tab.behind
	}
	a.summaryMu.Lock()
	s, ok := a.repoSummaries[path]
	a.summaryMu.Unlock()
	if ok {
		return s.branch, s.dirty, s.ahead, s.behind
	}
	for _, tab := range a.tabs {
		if tab.path == path && tab.loaded {
			return tab.branch, tab.worktreeCount > 0, tab.ahead, tab.behind
		}
	}

	return "", false, 0, 0
}

const repoSummaryInterval = 10 * time.Second

func (a *app) isRepoSummaryFresh(path string) bool {
	a.summaryMu.Lock()
	defer a.summaryMu.Unlock()
	s, ok := a.repoSummaries[path]
	return ok && time.Since(s.checked) < repoSummaryInterval
}

func (a *app) ensureRepoSummary(path string) {
	if !a.isRepoSummaryFresh(path) {
		a.refreshRepoSummary(path)
	}
}

// Only visible rows need periodic status. A batch uses one worker and one UI update.
func (a *app) visibleRepoPaths() []string {
	rows := a.sidebarRows(a.repoItems())
	first, last := a.repoListState.Visible()
	if first < 0 || last < first {
		first, last = 0, min(32, len(rows))-1
	}
	// 列表里还有工作树子行，行号不等于仓库下标，按行取路径。
	paths := make([]string, 0, last-first+1)
	for _, row := range rows[min(first, len(rows)):min(last+1, len(rows))] {
		paths = append(paths, row.path)
	}
	return paths
}

func (a *app) refreshRepoSummaries() { a.refreshSummaryBatch(a.visibleRepoPaths()) }

func (a *app) refreshRepoSummary(path string) {
	paths := append([]string{path}, a.visibleRepoPaths()...)
	a.refreshSummaryBatch(paths)
}

func (a *app) refreshSummaryBatch(paths []string) {
	if a.closed {
		return
	}
	a.summaryMu.Lock()
	if len(a.summaryRunning) != 0 {
		a.summaryMu.Unlock()
		return
	}
	pending := make(map[string]bool)
	for _, path := range paths {
		if path == "" {
			continue
		}
		if tab := a.activeTab(); tab != nil && tab.path == path && tab.loaded {
			continue
		}
		if s, ok := a.repoSummaries[path]; ok && time.Since(s.checked) < repoSummaryInterval {
			continue
		}
		pending[path] = true
	}
	if len(pending) == 0 {
		a.summaryMu.Unlock()
		return
	}
	a.summaryRunning = pending
	batchCtx, batchCancel := context.WithTimeout(context.Background(), 15*time.Second)
	a.summaryCancel = batchCancel
	a.summaryMu.Unlock()
	go func() {
		defer batchCancel()
		results := make(map[string]repoSummary, len(pending))
		for path := range pending {
			if batchCtx.Err() != nil {
				break
			}
			ctx, cancel := context.WithTimeout(batchCtx, 5*time.Second)
			results[path] = queryRepoSummary(ctx, path)
			cancel()
		}
		a.onMain(func() {
			a.summaryMu.Lock()
			a.summaryRunning = nil
			a.summaryCancel = nil
			if a.closed {
				a.summaryMu.Unlock()
				return
			}
			if a.repoSummaries == nil {
				a.repoSummaries = make(map[string]repoSummary)
			}
			changed := false
			for path, result := range results {
				old, ok := a.repoSummaries[path]
				old.checked = result.checked
				changed = changed || !ok || old != result
				a.repoSummaries[path] = result
			}
			a.summaryMu.Unlock()
			if changed && a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

func displayBranch(branch string) string {
	if strings.HasPrefix(branch, "分离头指针 ") {
		return tr("分离头指针 ") + strings.TrimPrefix(branch, "分离头指针 ")
	}
	if branch == "尚无提交" || branch == "仓库不可用" {
		return tr(branch)
	}
	return branch
}
