package main

import (
	"path/filepath"
	"sort"
	"unsafe"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Keep inactive repository data within one shared budget. Positions survive
// eviction so switching back reloads the same query, selection and file.
func (a *app) trimInactiveHistory() {
	if active := a.activeTab(); active != nil && active.cachedBytes()-active.historyBytes > historyMemoryStep {
		a.spillFileTabs(active)
	}
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
		if kept > historyMemoryStep {
			before := t.cachedBytes()
			if t.loaded {
				t.evictHistory()
			}
			a.spillFileTabs(t)
			kept -= before - t.cachedBytes()
		}
	}
}

func (t *repoTab) cachedBytes() int64 {
	n := t.historyBytes + t.metadataBytes()
	seen := map[*editor.DiffState]bool{}
	seenBuffers := map[*editor.Buffer]bool{}
	addBuffer := func(b *editor.Buffer) {
		if b != nil && !seenBuffers[b] {
			seenBuffers[b] = true
			n += int64(b.Len()) + int64(unsafe.Sizeof(*b))
			// Reserve per-line space for diff/syntax indexes owned by MyGo.
			n += int64(b.LineCount()) * 32
		}
	}
	for _, tab := range t.fileTabs {
		addBuffer(tab.fullContent.Buffer)
	}
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
			addBuffer(d.Old)
		}
		if d.New != nil {
			addBuffer(d.New)
		}
	}
	seenData := map[*byte]bool{}
	addData := func(data []byte) {
		if len(data) > 0 && !seenData[&data[0]] {
			seenData[&data[0]] = true
			n += int64(len(data))
		}
	}
	seenImages := map[*imageInfo]bool{}
	addImage := func(info *imageInfo) {
		if info != nil && !seenImages[info] {
			seenImages[info] = true
			addData(info.data)
			// Reserve RGBA pixels as well as the compressed source bytes.
			if info.img != nil {
				n += int64(info.width) * int64(info.height) * 4
			}
		}
	}
	addImageDiff := func(diff *imageDiff) {
		if diff != nil {
			addImage(diff.old)
			addImage(diff.new)
		}
	}
	addImageDiff(t.imageDiff)
	for _, saved := range []*historyLocation{t.historyBack, t.fileBack, t.restoreDiff, t.restore} {
		if saved != nil {
			addImageDiff(saved.imageDiff)
		}
	}
	for _, tab := range t.fileTabs {
		addImage(tab.fullImage)
		addImageDiff(tab.imageDiff)
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
			addData(source.old)
			addData(source.new)
		}
	}
	return n
}

// This is a cache estimate, not a process heap limit. Shared objects are counted once.
func (t *repoTab) metadataBytes() int64 {
	n := t.provenance.bytes()
	seenFiles := map[*fileChange]bool{}
	for _, files := range [][]fileChange{t.compareFiles, t.worktreeFiles} {
		if len(files) == 0 || seenFiles[&files[0]] {
			continue
		}
		seenFiles[&files[0]] = true
		n += int64(cap(files)) * int64(unsafe.Sizeof(fileChange{}))
		for _, f := range files {
			n += int64(len(f.path) + len(f.oldPath) + len(f.worktreeVersion))
		}
	}
	tree := t.fileTreeData
	n += int64(cap(tree.roots)) * 16
	for key, node := range tree.nodes {
		n += int64(len(key)+len(node.name)) + int64(unsafe.Sizeof(node)) + 48
	}
	for key, kids := range tree.kids {
		n += int64(len(key) + cap(kids)*16 + 64)
	}
	for _, refs := range t.refs {
		n += 64 + int64(cap(refs))*int64(unsafe.Sizeof(refInfo{}))
		for _, ref := range refs {
			n += int64(len(ref.name))
		}
	}
	seenDetails := map[*commitDetails]bool{}
	details := []*commitDetails{t.details}
	for _, saved := range []*historyLocation{t.historyBack, t.fileBack, t.restoreDiff, t.restore} {
		if saved != nil {
			details = append(details, saved.details)
		}
	}
	for _, d := range details {
		if d != nil && !seenDetails[d] {
			seenDetails[d] = true
			n += int64(unsafe.Sizeof(*d)) + int64(len(d.formatted)+len(d.message))
			if d.view.Buffer != nil {
				n += int64(d.view.Buffer.Len())
			}
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
			saved.diff, saved.simpleDiff, saved.imageDiff, saved.source, saved.details = nil, nil, nil, nil, nil
		}
	}
	t.restore = l
	t.restoreDiff, t.restoreFile = nil, ""
	t.commits, t.authors, t.commitIndex = nil, nil, nil
	t.refs, t.historyTips = nil, nil
	// Keep only the repository identity so a rebuild while evicted resets old queries.
	if t.refWatch != nil {
		marker := filepath.Join(t.path, ".git")
		t.refWatch = refWatch{marker: t.refWatch[marker]}
	}
	t.metaKey = ""
	t.graph = graph{}
	t.historyBytes = 0
	t.list, t.fileList = ui.ListState{}, ui.ListState{}
	t.selected, t.fileSel = -1, -1
	t.resetFileTree()
	t.selection.Clear()
	t.inspecting = zeroHash
	t.worktree = false
	t.comparison = nil
	t.diff, t.simpleDiff, t.imageDiff, t.diffSource, t.details = nil, nil, nil, nil, nil
	t.compareFiles, t.worktreeFiles = nil, nil
	t.worktreeCount, t.worktreeKey = 0, ""
	t.worktreeCache = nil
	t.loading, t.loaded, t.compareLoading, t.diffLoading, t.detailsLoading = false, false, false, false, false
}
