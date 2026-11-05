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
	branch, err := runGit(ctx, dir, "symbolic-ref", "-q", "--short", "HEAD")
	if err == nil {
		s.branch = strings.TrimSpace(string(branch))
	} else if short, serr := runGit(ctx, dir, "rev-parse", "--short", "HEAD"); serr == nil {
		s.branch = "分离头指针 " + strings.TrimSpace(string(short))
	} else {
		s.branch = "尚无提交"
	}
	out, err := runGit(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err == nil {
		files := parsePorcelainZ(out)
		s.dirty = len(files) > 0
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
		b = "(尚无提交)"
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
	for _, tab := range a.tabs {
		if tab.path == path && tab.loaded {
			return tab.branch, tab.worktreeCount > 0, tab.ahead, tab.behind
		}
	}
	a.summaryMu.Lock()
	defer a.summaryMu.Unlock()
	if s, ok := a.repoSummaries[path]; ok {
		return s.branch, s.dirty, s.ahead, s.behind
	}
	return "", false, 0, 0
}

func (a *app) isRepoSummaryFresh(path string) bool {
	a.summaryMu.Lock()
	defer a.summaryMu.Unlock()
	if a.repoSummaries == nil {
		return false
	}
	s, ok := a.repoSummaries[path]
	return ok && !s.checked.IsZero()
}

func (a *app) ensureRepoSummary(path string) {
	if a.closed || path == "" {
		return
	}
	a.summaryMu.Lock()
	running := a.summaryRunning[path]
	s, ok := a.repoSummaries[path]
	a.summaryMu.Unlock()
	if running {
		return
	}
	if ok && time.Since(s.checked) < 3*time.Second {
		return
	}
	a.refreshRepoSummary(path)
}

func (a *app) refreshRepoSummaries() {
	if a.closed {
		return
	}
	for _, p := range a.repoItems() {
		a.refreshRepoSummary(p)
	}
}

func (a *app) refreshRepoSummary(path string) {
	if a.closed || path == "" {
		return
	}
	a.summaryMu.Lock()
	if a.summaryRunning[path] {
		a.summaryMu.Unlock()
		return
	}
	if a.summaryRunning == nil {
		a.summaryRunning = make(map[string]bool)
	}
	a.summaryRunning[path] = true
	a.summaryMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s := queryRepoSummary(ctx, path)
		a.post(func() {
			if a.closed {
				return
			}
			a.summaryMu.Lock()
			if a.repoSummaries == nil {
				a.repoSummaries = make(map[string]repoSummary)
			}
			a.repoSummaries[path] = s
			delete(a.summaryRunning, path)
			a.summaryMu.Unlock()
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}
