package main

import "strings"

func (a *app) searchHistory(t *repoTab, query string) {
	base := t.scope
	if t.searchText != "" {
		base = t.searchScope
	}
	t.releaseHeavy()
	t.searchScope = base
	t.searchText = query
	t.scope, t.scopeChoice = "搜索结果", "搜索结果"
	if query == "" {
		t.scope, t.scopeChoice = base, base
	}
	t.wantHash, t.wantDirty = zeroHash, false
	t.resumeCount, t.resumeRow = t.options.First, 0
	t.wantFirst = true
	t.comparison = nil
	a.reload(t)
}

func (a *app) selectSearchResult(t *repoTab, index int) {
	if index < 0 || index >= len(t.commits) {
		return
	}
	t.searchIndex = index
	t.selected = t.commitRow(index)
	t.list.ScrollIntoView(t.selected)
	t.detailHidden = !t.options.OpenDetails
	t.commitApplied = t.selected
	if !t.detailHidden {
		a.onCommitSelected(t)
	} else {
		t.selection.Clear()
		t.selection.Add(t.commits[index].hash)
		t.wantHash = t.commits[index].hash
		t.selected = -1
		t.commitApplied = -1
	}
}

func (a *app) searchNext(t *repoTab) {
	q := strings.TrimSpace(t.query)
	if q != t.searchText {
		a.searchHistory(t, q)
		return
	}
	if q == "" {
		a.setFlash("先输入要查找的内容")
		return
	}
	next := t.searchIndex + 1
	if t.selected >= 0 {
		next = t.rowCommit(t.selected) + 1
	}
	if next >= len(t.commits) && t.hasMore {
		t.wantNext = true
		a.loadMore(t)
		return
	}
	if len(t.commits) == 0 {
		a.setFlash("没有匹配的提交")
		return
	}
	a.selectSearchResult(t, next%len(t.commits))
}
