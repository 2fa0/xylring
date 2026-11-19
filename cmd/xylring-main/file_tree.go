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
	// section 非 sideNone 时这一行是分组标题（已暂存 / 未暂存），不是目录。
	section fileSide
}

type rawFileNode struct {
	key   string
	name  string
	file  int
	kids  []*rawFileNode
	index map[string]int

	section fileSide
}

func slashPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return strings.Trim(p, "/")
}

// sectionKey 是分组标题在树里的键。路径里不会有 NUL，所以不会撞上真实文件。
func sectionKey(side fileSide) string {
	return "\x00" + side.key()
}

// treeKey 是文件在树里的键。未提交改动两侧都有改动时按侧分开，同名文件在两组里各是一行；
// 不分组时树里没有分组节点，键就是路径。
func (f fileChange) treeKey(grouped bool) string {
	if f.side == sideNone || !grouped {
		return slashPath(f.path)
	}
	return sectionKey(f.side) + "/" + slashPath(f.path)
}

func (n *rawFileNode) child(key, name string, file int) *rawFileNode {
	k := key
	if file < 0 {
		k += "/"
	}
	if i, ok := n.index[k]; ok {
		return n.kids[i]
	}
	ch := &rawFileNode{key: key, name: name, file: file, index: map[string]int{}}
	n.index[k] = len(n.kids)
	n.kids = append(n.kids, ch)
	return ch
}

func (n *rawFileNode) compact() {
	for _, ch := range n.kids {
		ch.compact()
	}
	// 分组标题不能被压进它下面的目录里。
	if n.file >= 0 || n.section != sideNone {
		return
	}
	for len(n.kids) == 1 && n.kids[0].file < 0 {
		ch := n.kids[0]
		n.key = n.key + "/" + ch.key
		n.name = n.name + "/" + ch.name
		n.kids = ch.kids
		n.index = ch.index
	}
}

func (n *rawFileNode) emit(parentKey string, tree *fileTree) string {
	key := n.key
	if parentKey != "" {
		key = parentKey + "/" + n.key
	}
	// A diff can contain both a deleted file and a new directory at this path.
	k := key
	if n.file < 0 {
		k += "/"
	}
	tree.nodes[k] = fileTreeNode{name: n.name, dir: n.file < 0, file: n.file, section: n.section}
	if n.file >= 0 {
		return k
	}
	kids := make([]string, 0, len(n.kids))
	for _, ch := range n.kids {
		kids = append(kids, ch.emit(key, tree))
	}
	tree.kids[k] = kids
	return k
}

// addFileNode 把一个文件挂到树里。nested 为 true 时按路径拆成目录层，
// 否则整条路径就是一个节点（列表视图的树只有分组标题这一层）。
func addFileNode(root *rawFileNode, files []fileChange, idx int, nested bool) {
	if idx < 0 || idx >= len(files) {
		return
	}
	path := slashPath(files[idx].path)
	if !nested {
		root.child(path, path, idx)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		return
	}
	n := root
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." {
			continue
		}
		n = n.child(part, part, -1)
	}
	name := parts[len(parts)-1]
	if name == "" || name == "." {
		return
	}
	n.child(name, name, idx)
}

func buildFilteredFileTree(files []fileChange, indices []int, nested bool) fileTree {
	root := &rawFileNode{file: -1, index: map[string]int{}}
	// 只剩一侧时不建分组层，树里的文件直接挂在根上。分组看仓库状态、不看筛选：
	// 否则筛到只剩一侧的文件时，树会马上改结构。
	grouped := groupFileSides(files)
	if !grouped {
		for _, idx := range indices {
			addFileNode(root, files, idx, nested)
		}
	} else {
		for _, side := range []fileSide{sideStaged, sideUnstaged} {
			var section *rawFileNode
			for _, idx := range indices {
				if idx < 0 || idx >= len(files) || files[idx].side != side {
					continue
				}
				if section == nil {
					section = root.child(sectionKey(side), "", -1)
					section.section = side
				}
				addFileNode(section, files, idx, nested)
			}
		}
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
	return buildFilteredFileTree(files, indices, true)
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

// File lists are replaced as a unit; compute their structural key only on replacement.
func fileSetKey(files []fileChange) string {
	var b strings.Builder
	for _, f := range files {
		b.WriteString(f.status)
		b.WriteByte(0)
		b.WriteString(f.path)
		b.WriteByte(0)
		b.WriteString(f.oldPath)
		b.WriteByte(0)
		b.WriteString(f.side.key())
		b.WriteByte(0)
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

func (t *repoTab) resetFileTree() {
	t.fileOutline = ui.OutlineState[string]{}
	t.fileOutlineSel = -1
	t.fileTreeScroll, t.fileTreeScrollY = ui.ScrollState{}, 0
	t.fileHoverID, t.fileHoverY = "", 0
	t.fileTreeKey, t.fileTreeFilter = "", ""
	t.fileTreeInput, t.fileTreeCount = nil, 0
	t.fileTreeData = fileTree{}
}

func (t *repoTab) prepareFileTree() fileTree {
	filter := strings.ToLower(strings.TrimSpace(t.fileFilter))
	var input *fileChange
	if len(t.compareFiles) > 0 {
		input = &t.compareFiles[0]
	}
	if t.fileTreeInput != input || t.fileTreeCount != len(t.compareFiles) || t.fileTreeFilter != filter {
		key := fileSetKey(t.compareFiles) + "\x00" + filter
		t.fileTreeInput, t.fileTreeCount, t.fileTreeFilter = input, len(t.compareFiles), filter
		if t.fileTreeKey == key {
			return t.fileTreeData
		}
		t.fileTreeKey = key
		indices := t.filteredFileIndices()
		t.fileTreeData = buildFilteredFileTree(t.compareFiles, indices, true)
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
	want := t.compareFiles[t.fileSel].treeKey(t.groupedFileSides())
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
