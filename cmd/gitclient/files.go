package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	editor "github.com/egoist/mygo/gitclient-editor"
	"github.com/egoist/mygo/ui"
)

type browserEntry struct {
	path, name string
	dir        bool
}
type browserRow struct {
	browserEntry
	depth int
}
type fileViewer struct {
	path, rev, note string
	state           *editor.State
	loading         bool
}

func (a *app) loadDirectory(t *repoTab, rel string) {
	if t.dirCache == nil {
		t.dirCache = map[string][]browserEntry{}
		t.dirBusy = map[string]bool{}
	}
	if t.dirBusy[rel] {
		return
	}
	if _, ok := t.dirCache[rel]; ok {
		return
	}
	if t.browseCtx == nil {
		t.browseCtx, t.browseCancel = context.WithCancel(context.Background())
	}
	t.dirBusy[rel] = true
	ctx, gen, root := t.browseCtx, t.generation, t.path
	go func() {
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
		var result []browserEntry
		for _, entry := range entries {
			if entry.Name() == ".git" {
				continue
			}
			result = append(result, browserEntry{path: filepath.ToSlash(filepath.Join(rel, entry.Name())), name: entry.Name(), dir: entry.IsDir()})
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].dir != result[j].dir {
				return result[i].dir
			}
			return result[i].name < result[j].name
		})
		a.post(func() {
			if t.generation != gen || ctx.Err() != nil {
				return
			}
			delete(t.dirBusy, rel)
			if err != nil {
				t.dirCache[rel] = nil
				t.browserErr = err.Error()
				return
			}
			t.dirCache[rel] = result
		})
	}()
}

func browserRows(t *repoTab, dir string, depth int) []browserRow {
	var rows []browserRow
	for _, entry := range t.dirCache[dir] {
		rows = append(rows, browserRow{browserEntry: entry, depth: depth})
		if entry.dir && t.treeOpen[entry.path] {
			rows = append(rows, browserRows(t, entry.path, depth+1)...)
		}
	}
	return rows
}

func (a *app) workspaceTree(c *ui.Context, t *repoTab) {
	a.loadDirectory(t, "")
	if t.dirBusy[""] {
		ui.Text(c, "正在读取目录…")
	}
	rows := browserRows(t, "", 0)
	ui.List(c.Key("workspace-tree"), &t.browserList, len(rows), func(i int) {
		row := rows[i]
		label := row.name
		if row.dir {
			if t.treeOpen[row.path] {
				label = "▾ " + label
			} else {
				label = "▸ " + label
			}
		}
		e := ui.Button(c.Key(row.path), label).Tooltip(row.path).Margin(0, 0, 0, float32(row.depth*12))
		if !row.dir {
			e.ContextMenu(func(m *ui.Menu) {
				if m.Item("文件历史").Chosen() {
					a.openFileHistory(t, row.path)
				}
				if m.Item("查看当前文件").Chosen() {
					a.openFile(t, "", row.path)
				}
				if m.Item("打开当前文件").Chosen() {
					a.openCurrentFile(t, row.path)
				}
			})
		}
		if e.Clicked() {
			if row.dir {
				if t.treeOpen == nil {
					t.treeOpen = map[string]bool{}
				}
				t.treeOpen[row.path] = !t.treeOpen[row.path]
				if t.treeOpen[row.path] {
					a.loadDirectory(t, row.path)
				}
			} else {
				a.openFile(t, "", row.path)
			}
		}
	}).Grow(1).MinHeight(0)
	if t.browserErr != "" {
		ui.Text(c, t.browserErr).TextColor(c.Theme().Danger)
	}
}

func (a *app) openFileHistory(t *repoTab, path string) {
	t.releaseHeavy()
	t.viewer = nil
	t.historyPath = path
	t.searchText = ""
	t.historyRefs = []string{"HEAD"}
	t.scope, t.scopeChoice = "文件历史", "文件历史"
	t.resumeCount, t.resumeRow = t.options.First, 0
	t.wantHash, t.wantDirty = zeroHash, false
	t.comparison = nil
	t.wantFirst = true
	a.reload(t)
}

func (a *app) openCurrentFile(t *repoTab, path string) {
	full, err := withinRepo(t.path, path)
	if err == nil {
		err = mygo.Shell.OpenPath(full)
	}
	if err != nil {
		a.banner = err.Error()
	}
}

func (a *app) openFile(t *repoTab, rev, path string) {
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.diffGen++
	gen, repoGen := t.diffGen, t.generation
	v := &fileViewer{path: path, rev: rev, loading: true}
	t.viewer = v
	t.diff = nil
	t.detailHidden = false
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.diffCancel = cancel
	root := t.path
	go func() {
		defer cancel()
		var data []byte
		var note string
		var err error
		if rev == "" {
			data, note, err = readWorktree(root, path)
		} else {
			data, note, err = readBlob(ctx, root, rev, path)
		}
		a.post(func() {
			if t.generation != repoGen || t.diffGen != gen || t.viewer != v {
				return
			}
			v.loading = false
			v.note = note
			if err != nil {
				v.note = err.Error()
				return
			}
			if note != "" {
				return
			}
			buffer := editor.NewBuffer(string(data))
			buffer.SetLanguage(editor.LanguageFor(path))
			v.state = &editor.State{Buffer: buffer, ReadOnly: true}
		})
	}()
}

func (a *app) viewerPane(c *ui.Context, t *repoTab) {
	v := t.viewer
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(6, 8).Gap(8).Children(func() {
			label := "工作区"
			if v.rev != "" {
				label = v.rev[:min(7, len(v.rev))]
			}
			ui.Text(c, v.path+" @ "+label).Grow(1).SingleLine()
			if ui.Button(c.Key("viewer-close"), "关闭文件").Clicked() {
				if t.diffCancel != nil {
					t.diffCancel()
				}
				t.diffGen++
				t.viewer = nil
				t.fileApplied = -1
			}
		})
		if v.loading {
			ui.Text(c, "正在读取文件…").Padding(8)
		} else if v.note != "" {
			ui.Text(c, v.note).Padding(8)
		} else if v.state != nil {
			editor.View(c.Key("file-viewer"), v.state).Grow(1).MinHeight(0)
		}
	})
}

func (a *app) fileMenu(c *ui.Context, m *ui.Menu, t *repoTab, index int) {
	if index < 0 || index >= len(t.files) {
		return
	}
	file := t.files[index]
	if m.Item("查看 diff").Chosen() {
		t.fileSel = index
		t.fileMode = "diff"
		a.onFileSelected(t)
	}
	rev := ""
	if t.comparison != nil {
		rev = hashHex(t.comparison.to)
	} else if commit := t.selectedCommit(); commit != nil {
		rev = hashHex(commit.hash)
		if file.newRev != "" {
			rev = file.newRev
		}
	}
	if m.Item("查看该版本文件").Disabled(rev == "").Chosen() {
		a.openFile(t, rev, file.path)
	}
	if m.Item("与工作区文件比较").Disabled(rev == "").Chosen() {
		t.fileSel = index
		t.fileMode = "worktree"
		a.onFileSelected(t)
	}
	if m.Item("打开当前文件").Chosen() {
		a.openCurrentFile(t, file.path)
	}
	if m.Item("文件历史").Chosen() {
		a.openFileHistory(t, file.path)
	}
}

type changedTreeRow struct {
	label, path  string
	index, depth int
	dir          bool
}

func changedRows(files []fileChange, closed map[string]bool) []changedTreeRow {
	// Sorted paths group directories; one-child directory chains share a compact row.
	type node struct {
		path, name string
		index      int
		children   map[string]*node
	}
	root := &node{children: map[string]*node{}, index: -1}
	for i, file := range files {
		current := root
		path := file.path
		if file.layer != "" {
			path = file.layer + "/" + path
		}
		parts := strings.Split(path, "/")
		for j, part := range parts {
			if current.children[part] == nil {
				current.children[part] = &node{name: part, path: strings.Join(parts[:j+1], "/"), index: -1, children: map[string]*node{}}
			}
			current = current.children[part]
		}
		current.index = i
	}
	var rows []changedTreeRow
	var visit func(*node, int)
	visit = func(parent *node, depth int) {
		var keys []string
		for key := range parent.children {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := parent.children[keys[i]], parent.children[keys[j]]
			if (a.index < 0) != (b.index < 0) {
				return a.index < 0
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			n := parent.children[key]
			label := n.name
			for n.index < 0 && len(n.children) == 1 {
				var child *node
				for _, v := range n.children {
					child = v
				}
				if child.index >= 0 {
					break
				}
				n = child
				label += "/" + n.name
			}
			rows = append(rows, changedTreeRow{label: label, path: n.path, index: n.index, depth: depth, dir: n.index < 0})
			if n.index < 0 && !closed[n.path] {
				visit(n, depth+1)
			}
		}
	}
	visit(root, 0)
	return rows
}

func worktreeSides(ctx context.Context, dir, rev, path string) (string, string, string, error) {
	old, on, err := readBlob(ctx, dir, rev, path)
	if err != nil {
		return "", "", "", err
	}
	current, nn, err := readWorktree(dir, path)
	if err != nil {
		return "", "", "", err
	}
	if on != "" {
		return "", "", on, nil
	}
	return string(old), string(current), nn, nil
}

func (a *app) closeDetails(t *repoTab) {
	t.captureReading()
	t.focusDiff = false
	t.captureSelection()
	if t.detailCancel != nil {
		t.detailCancel()
	}
	if t.diffCancel != nil {
		t.diffCancel()
	}
	t.detailGen++
	t.diffGen++
	t.detailLoading = false
	t.diffLoading = false
	t.files = nil
	t.info = nil
	t.diff = nil
	t.viewer = nil
	t.detailErr = ""
	t.detailNote = ""
	t.shownKey = ""
	t.detailHidden = true
	t.diffOpen = false
	t.selection.Clear()
	if c := t.selectedCommit(); c != nil {
		t.selection.Add(c.hash)
	}
	t.commitApplied = t.selected
}
