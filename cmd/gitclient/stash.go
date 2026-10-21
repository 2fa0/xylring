package main

import (
	"context"
	"github.com/egoist/mygo/ui"
	"strings"
)

func loadStashes(ctx context.Context, dir string) ([]commit, error) {
	out, err := runGit(ctx, dir, "stash", "list", "--format="+commitFormat+"%x1f%gd")
	if err != nil {
		return nil, err
	}
	var result []commit
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimRight(rec, "\r\n")
		if rec == "" {
			continue
		}
		i := strings.LastIndex(rec, "\x1f")
		if i < 0 {
			continue
		}
		c, err := parseCommitRecord(rec[:i])
		if err != nil {
			return nil, err
		}
		c.stash = rec[i+1:]
		result = append(result, c)
	}
	return result, nil
}
func stashFiles(ctx context.Context, dir, hash string) ([]fileChange, error) {
	files, err := compareFiles(ctx, dir, hash+"^1", hash)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].layer = "工作区"
		files[i].oldRev = hash + "^1"
		files[i].newRev = hash
	}
	staged, err := compareFiles(ctx, dir, hash+"^1", hash+"^2")
	if err != nil {
		return nil, err
	}
	for i := range staged {
		staged[i].layer = "暂存区"
		staged[i].oldRev = hash + "^1"
		staged[i].newRev = hash + "^2"
	}
	files = append(files, staged...)
	parents, err := runGit(ctx, dir, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return nil, err
	}
	if len(strings.Fields(string(parents))) >= 4 {
		raw, err := runGit(ctx, dir, "ls-tree", "-r", "--name-only", "-z", hash+"^3", "--")
		if err != nil {
			return nil, err
		}
		for _, path := range strings.Split(string(raw), "\x00") {
			if path != "" {
				files = append(files, fileChange{status: "?", path: path, layer: "未跟踪", newRev: hash + "^3"})
			}
		}
	}
	return files, nil
}
func (a *app) stashSidebar(c *ui.Context, t *repoTab) {
	if ui.Button(c.Key("stash-new"), "新建 Stash…").Clicked() {
		a.beginOperation(t, "新建 Stash", "")
	}
	ui.List(c.Key("stash-list"), &t.stashList, len(t.stashes), func(i int) {
		s := t.stashes[i]
		e := ui.Button(c.Key(s.stash), s.stash+" "+s.subject).Tooltip(s.subject)
		if e.Clicked() {
			t.stashView = &s
			t.detailHidden = false
			t.selected = -1
			t.commitApplied = -1
			a.onCommitSelected(t)
		}
		e.ContextMenu(func(m *ui.Menu) { a.stashMenu(m, t, s) })
	}).Grow(1).MinHeight(0)
}
func (a *app) stashMenu(m *ui.Menu, t *repoTab, s commit) {
	for _, kind := range []string{"Apply Stash", "Pop Stash", "Drop Stash"} {
		if m.Item(kind + "…").Chosen() {
			a.beginOperation(t, kind, s.stash)
		}
	}
}

func (t *repoTab) layoutStashes() {
	if t.stashRows() == 0 {
		return
	}
	if t.dirty > 0 && t.head != zeroHash {
		t.graph.cols = append(t.graph.cols, t.head)
		t.graph.colors = append(t.graph.colors, 0)
	}
	for i := range t.stashes {
		node := t.stashes[i]
		node.parents = node.parents[:min(1, len(node.parents))]
		t.graph.add(&node)
		t.stashes[i].nodeCol = node.nodeCol
		t.stashes[i].nodeColor = node.nodeColor
		t.stashes[i].segs = node.segs
	}
}
