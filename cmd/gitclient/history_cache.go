package main

import (
	"sort"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Keep inactive repository data within one shared budget. Positions survive
// eviction so switching back reloads the same query, selection and file.
func (a *app) trimInactiveHistory() {
	var tabs []*repoTab
	for _, t := range a.tabs {
		if t != a.activeTab() {
			tabs = append(tabs, t)
		}
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].usedAt.After(tabs[j].usedAt) })
	var kept int64
	for _, t := range tabs {
		kept += t.cachedBytes()
		if kept > historyMemoryStep && t.loaded {
			before := t.cachedBytes()
			t.evictHistory()
			kept -= before - t.cachedBytes()
		}
	}
}

func (t *repoTab) cachedBytes() int64 {
	n := t.historyBytes
	seen := map[*editor.DiffState]bool{}
	diffs := []*editor.DiffState{t.diff, savedDiff(t.historyBack), savedDiff(t.fileBack), savedDiff(t.restoreDiff)}
	for _, tab := range t.fileTabs {
		diffs = append(diffs, tab.diff)
	}
	for _, d := range diffs {
		if d == nil || seen[d] {
			continue
		}
		seen[d] = true
		if d.Old != nil {
			n += int64(d.Old.Len())
		}
		if d.New != nil {
			n += int64(d.New.Len())
		}
	}
	seenSources := map[*fileDiffSource]bool{}
	sources := []*fileDiffSource{t.diffSource}
	for _, saved := range []*historyLocation{t.historyBack, t.fileBack, t.restoreDiff} {
		if saved != nil {
			sources = append(sources, saved.source)
		}
	}
	for _, tab := range t.fileTabs {
		sources = append(sources, tab.source)
	}
	for _, source := range sources {
		if source != nil && !seenSources[source] {
			seenSources[source] = true
			n += int64(len(source.old) + len(source.new))
		}
	}
	return n
}

func savedDiff(l *historyLocation) *editor.DiffState {
	if l == nil {
		return nil
	}
	return l.diff
}

func (t *repoTab) evictHistory() {
	l := t.location()
	if t.restore != nil {
		l = t.restore
	}
	t.cancel()
	for _, saved := range []*historyLocation{l, t.historyBack, t.fileBack} {
		if saved != nil {
			saved.diff, saved.simpleDiff, saved.source, saved.details = nil, nil, nil, nil
		}
	}
	t.restore = l
	t.restoreDiff, t.restoreFile = nil, ""
	t.commits, t.authors, t.commitIndex = nil, nil, nil
	t.refs, t.historyTips, t.refWatch = nil, nil, nil
	t.metaKey = ""
	t.graph = graph{}
	t.historyBytes = 0
	t.list, t.fileList = ui.ListState{}, ui.ListState{}
	t.selected, t.fileSel = -1, -1
	t.resetFileTree()
	t.resetWorktreeTree()
	t.selection.Clear()
	t.inspecting = zeroHash
	t.worktree = false
	t.comparison = nil
	t.diff, t.simpleDiff, t.diffSource, t.details = nil, nil, nil, nil
	t.compareFiles, t.worktreeFiles = nil, nil
	t.worktreeCount, t.worktreeKey = 0, ""
	t.loading, t.loaded, t.compareLoading, t.diffLoading, t.detailsLoading = false, false, false, false, false
}
