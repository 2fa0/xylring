package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

type fileTree struct {
	roots []string
	nodes map[string]fileTreeNode
	kids  map[string][]string
}

type fileTreeNode struct {
	name string
	dir  bool
	file int
}

type rawFileNode struct {
	name  string
	file  int
	kids  []*rawFileNode
	index map[string]int
}

func slashPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.Trim(p, "/")
}

func (n *rawFileNode) child(name string, file int) *rawFileNode {
	key := name
	if file < 0 {
		key += "/"
	}
	if i, ok := n.index[key]; ok {
		return n.kids[i]
	}
	ch := &rawFileNode{name: name, file: file, index: map[string]int{}}
	n.index[key] = len(n.kids)
	n.kids = append(n.kids, ch)
	return ch
}

func (n *rawFileNode) compact() {
	for _, ch := range n.kids {
		ch.compact()
	}
	if n.file >= 0 {
		return
	}
	for len(n.kids) == 1 && n.kids[0].file < 0 {
		ch := n.kids[0]
		n.name = n.name + "/" + ch.name
		n.kids = ch.kids
		n.index = ch.index
	}
}

func (n *rawFileNode) emit(parent string, tree *fileTree) string {
	path := n.name
	if parent != "" {
		path = parent + "/" + n.name
	}
	// A diff can contain both a deleted file and a new directory at this path.
	key := path
	if n.file < 0 {
		key += "/"
	}
	tree.nodes[key] = fileTreeNode{name: n.name, dir: n.file < 0, file: n.file}
	if n.file >= 0 {
		return key
	}
	kids := make([]string, 0, len(n.kids))
	for _, ch := range n.kids {
		kids = append(kids, ch.emit(path, tree))
	}
	tree.kids[key] = kids
	return key
}

func buildFilteredFileTree(files []fileChange, indices []int) fileTree {
	root := &rawFileNode{file: -1, index: map[string]int{}}
	for _, idx := range indices {
		if idx < 0 || idx >= len(files) {
			continue
		}
		f := files[idx]
		parts := strings.Split(slashPath(f.path), "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		n := root
		for _, part := range parts[:len(parts)-1] {
			if part == "" || part == "." {
				continue
			}
			n = n.child(part, -1)
		}
		name := parts[len(parts)-1]
		if name == "" || name == "." {
			continue
		}
		n.child(name, idx)
	}
	for _, ch := range root.kids {
		ch.compact()
	}
	tree := fileTree{nodes: map[string]fileTreeNode{}, kids: map[string][]string{}}
	for _, ch := range root.kids {
		tree.roots = append(tree.roots, ch.emit("", &tree))
	}
	return tree
}

func buildFileTree(files []fileChange) fileTree {
	indices := make([]int, len(files))
	for i := range files {
		indices[i] = i
	}
	return buildFilteredFileTree(files, indices)
}

func (t fileTree) children(key string) []string {
	return t.kids[key]
}

func (t fileTree) rowOf(key string, open *ui.Selection[string]) int {
	row := 0
	var walk func([]string) int
	walk = func(items []string) int {
		for _, item := range items {
			if item == key {
				return row
			}
			row++
			if kids := t.kids[item]; kids != nil && open.Has(item) {
				if found := walk(kids); found >= 0 {
					return found
				}
			}
		}
		return -1
	}
	return walk(t.roots)
}

func fileSetKey(files []fileChange) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.status)
		b.WriteByte(0)
		b.WriteString(f.path)
		b.WriteByte(0)
		b.WriteString(f.oldPath)
		b.WriteByte('\n')
	}
	return b.String()
}

func (t *repoTab) filteredFileIndices() []int {
	filter := strings.ToLower(strings.TrimSpace(t.fileFilter))
	if filter == "" {
		res := make([]int, len(t.compareFiles))
		for i := range t.compareFiles {
			res[i] = i
		}
		return res
	}
	var res []int
	for i, f := range t.compareFiles {
		if strings.Contains(strings.ToLower(f.path), filter) || (f.oldPath != "" && strings.Contains(strings.ToLower(f.oldPath), filter)) {
			res = append(res, i)
		}
	}
	return res
}

func (t *repoTab) filteredWorktreeIndices() []int {
	filter := strings.ToLower(strings.TrimSpace(t.worktreeFilter))
	if filter == "" {
		res := make([]int, len(t.worktreeFiles))
		for i := range t.worktreeFiles {
			res[i] = i
		}
		return res
	}
	var res []int
	for i, f := range t.worktreeFiles {
		if strings.Contains(strings.ToLower(f.path), filter) || (f.oldPath != "" && strings.Contains(strings.ToLower(f.oldPath), filter)) {
			res = append(res, i)
		}
	}
	return res
}

func (t *repoTab) resetFileTree() {
	t.fileOutline = ui.OutlineState[string]{}
	t.fileOutlineSel = -1
	t.fileTreeScroll, t.fileTreeScrollY = ui.ScrollState{}, 0
	t.fileHoverID, t.fileHoverY = "", 0
	t.fileTreeKey = ""
	t.fileTreeData = fileTree{}
}

func (t *repoTab) prepareFileTree() fileTree {
	filter := strings.ToLower(strings.TrimSpace(t.fileFilter))
	key := fileSetKey(t.compareFiles) + "\x00" + filter
	if t.fileTreeKey != key {
		t.fileTreeKey = key
		indices := t.filteredFileIndices()
		t.fileTreeData = buildFilteredFileTree(t.compareFiles, indices)
		t.fileOutline.Open.Clear()
		for k, n := range t.fileTreeData.nodes {
			if n.dir {
				t.fileOutline.Open.Add(k)
			}
		}
		t.fileOutlineSel = -1
	}
	return t.fileTreeData
}

func (t *repoTab) expandAllTree() {
	tree := t.prepareFileTree()
	for k, n := range tree.nodes {
		if n.dir {
			t.fileOutline.Open.Add(k)
		}
	}
}

func (t *repoTab) collapseAllTree() {
	t.fileOutline.Open.Clear()
}

func (t *repoTab) syncFileOutlineSel(tree fileTree) {
	if t.fileSel < 0 || t.fileSel >= len(t.compareFiles) {
		return
	}
	want := slashPath(t.compareFiles[t.fileSel].path)
	if t.fileOutlineSel >= 0 && t.fileOutlineSel < t.fileOutline.Rows() {
		cur := t.fileOutline.Item(t.fileOutlineSel)
		if cur == want {
			return
		}
		if n, ok := tree.nodes[cur]; ok && n.dir {
			return
		}
	}
	row := tree.rowOf(want, &t.fileOutline.Open)
	if row >= 0 {
		t.fileOutlineSel = row
		t.fileOutline.List.ScrollIntoView(row)
	}
}

func (t *repoTab) resetWorktreeTree() {
	t.worktreeOutline = ui.OutlineState[string]{}
	t.worktreeOutlineSel = -1
	t.worktreeTreeScroll, t.worktreeTreeScrollY = ui.ScrollState{}, 0
	t.worktreeHoverID, t.worktreeHoverY = "", 0
	t.worktreeTreeKey = ""
	t.worktreeTreeData = fileTree{}
}

func (t *repoTab) prepareWorktreeTree() fileTree {
	filter := strings.ToLower(strings.TrimSpace(t.worktreeFilter))
	key := fileSetKey(t.worktreeFiles) + "\x00" + filter
	if t.worktreeTreeKey != key {
		t.worktreeTreeKey = key
		indices := t.filteredWorktreeIndices()
		t.worktreeTreeData = buildFilteredFileTree(t.worktreeFiles, indices)
		t.worktreeOutline.Open.Clear()
		for k, n := range t.worktreeTreeData.nodes {
			if n.dir {
				t.worktreeOutline.Open.Add(k)
			}
		}
		t.worktreeOutlineSel = -1
	}
	return t.worktreeTreeData
}

func (t *repoTab) expandAllWorktreeTree() {
	tree := t.prepareWorktreeTree()
	for k, n := range tree.nodes {
		if n.dir {
			t.worktreeOutline.Open.Add(k)
		}
	}
}

func (t *repoTab) collapseAllWorktreeTree() {
	t.worktreeOutline.Open.Clear()
}

func (t *repoTab) syncWorktreeOutlineSel(tree fileTree) {
	if t.worktreeSel < 0 || t.worktreeSel >= len(t.worktreeFiles) {
		return
	}
	want := slashPath(t.worktreeFiles[t.worktreeSel].path)
	if t.worktreeOutlineSel >= 0 && t.worktreeOutlineSel < t.worktreeOutline.Rows() {
		cur := t.worktreeOutline.Item(t.worktreeOutlineSel)
		if cur == want {
			return
		}
		if n, ok := tree.nodes[cur]; ok && n.dir {
			return
		}
	}
	row := tree.rowOf(want, &t.worktreeOutline.Open)
	if row < 0 {
		parts := strings.Split(want, "/")
		prefix := ""
		for _, p := range parts[:len(parts)-1] {
			if prefix == "" {
				prefix = p
			} else {
				prefix += "/" + p
			}
			t.worktreeOutline.Open.Add(prefix + "/")
		}
		row = tree.rowOf(want, &t.worktreeOutline.Open)
	}
	if row >= 0 {
		t.worktreeOutlineSel = row
		t.worktreeOutline.List.ScrollIntoView(row)
	}
}
