package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type historyQuery struct {
	term, kind string
	ref        string // 空值为全部引用，HEAD 为当前分支，其余为完整 ref。
	hideRemote bool
	filePath   string
	fileAt     [20]byte
	extraTip   [20]byte // 从任意快照的文件历史回到图谱时，保留该提交的可达历史。
}

type historyLocation struct {
	query       historyQuery
	hash        [20]byte
	top         [20]byte
	file        string
	diff        *editor.DiffState
	simpleDiff  *simpleDiff
	note        string
	from, to    [20]byte
	comparison  *commitComparison
	details     *commitDetails
	detailsOpen bool
	worktree    bool
	loaded      int
}

func (t *repoTab) location() *historyLocation {
	l := &historyLocation{query: t.query, hash: t.inspecting}
	l.loaded = len(t.commits)
	l.diff, l.note, l.from, l.to = t.diff, t.diffNote, t.diffFrom, t.diffTo
	l.simpleDiff = t.simpleDiff
	l.comparison = t.comparison
	l.details, l.detailsOpen = t.details, t.detailsOpen
	l.worktree = t.worktree
	if first, _ := t.list.Visible(); first >= 0 && first < len(t.commits) {
		l.top = t.commits[first].hash
	}
	if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		l.file = t.compareFiles[t.fileSel].path
	}
	return l
}

func (q historyQuery) searchArgs() []string {
	if q.term == "" {
		return nil
	}
	switch q.kind {
	case "作者":
		return []string{"--regexp-ignore-case", "--fixed-strings", "--author=" + q.term}
	case "文件路径":
		return []string{"--full-history"}
	default:
		return []string{"--regexp-ignore-case", "--fixed-strings", "--grep=" + q.term}
	}
}

func searchPathspec(term string) string {
	term = strings.TrimPrefix(term, "./")
	term = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`).Replace(term)
	return ":(icase)*" + term + "*"
}

// 分页固定在本轮加载的提交上，外部新提交不会让 skip 页重复或漏项。
func queryTips(ctx context.Context, dir string, meta repoMeta, q historyQuery) ([]string, error) {
	if q.filePath != "" {
		return []string{hashHex(q.fileAt)}, nil
	}
	if q.ref == "HEAD" {
		if meta.head == zeroHash {
			return nil, nil
		}
		return []string{hashHex(meta.head)}, nil
	}
	if q.ref != "" {
		h, err := resolveCommit(ctx, dir, q.ref)
		if err != nil {
			return nil, err
		}
		return []string{hashHex(h)}, nil
	}
	set := map[[20]byte]bool{}
	if q.extraTip != zeroHash {
		set[q.extraTip] = true
	}
	if meta.head != zeroHash {
		set[meta.head] = true
	}
	for h, refs := range meta.refs {
		for _, ref := range refs {
			if !q.hideRemote || ref.kind != refRemote {
				set[h] = true
			}
		}
	}
	var tips []string
	for h := range set {
		tips = append(tips, hashHex(h))
	}
	sort.Strings(tips)
	return tips, nil
}

var searchKinds = []string{"说明", "作者", "文件路径"}

func (a *app) searchHistory(t *repoTab) {
	term := strings.TrimSpace(t.searchText)
	if term == "" {
		a.leaveSearch(t)
		return
	}
	if t.historyBack == nil {
		t.historyBack = t.location()
	}
	q := t.query
	q.term, q.kind = term, t.searchKind
	a.changeHistory(t, q)
}

func (a *app) leaveSearch(t *repoTab) {
	if t.historyBack == nil {
		return
	}
	back := t.historyBack
	t.historyBack = nil
	t.searchText = ""
	a.changeHistory(t, back.query)
	t.restore = back
}

func (a *app) changeHistory(t *repoTab, q historyQuery) {
	t.query = q
	t.list = ui.ListState{}
	t.selected = -1
	t.selection.Clear()
	t.worktree = false
	a.closeInspection(t)
	a.closeComparison(t)
	t.restore = nil
	t.restoreFile, t.restoreDiff = "", nil
	t.commits = nil
	t.commitIndex, t.authors = nil, nil
	t.graph = graph{}
	t.historyBytes, t.historyBudget = 0, 0
	a.reload(t)
}

func (a *app) continueRestore(t *repoTab) {
	l := t.restore
	if l == nil {
		return
	}
	if !containsLocation(t.commits, l) && t.hasMore && (l.loaded == 0 || len(t.commits) < l.loaded+pageFirst) {
		a.loadMore(t)
		return
	}
	t.restore = nil
	if !containsLocation(t.commits, l) {
		t.notice = "原阅读位置已不在已加载范围内"
	}
	if i := t.indexOf(l.hash); i >= 0 {
		t.restoreFile = l.file
		t.restoreDiff = l
		a.selectCommit(t, i)
		t.details, t.detailsOpen = l.details, l.detailsOpen
		if l.detailsOpen && l.details == nil {
			t.detailsOpen = false
			a.toggleDetails(t)
		}
	} else if l.comparison != nil {
		t.restoreFile, t.restoreDiff = l.file, l
		cmp := l.comparison
		a.showComparison(t, cmp.from, cmp.to, cmp.fromLabel, cmp.toLabel, cmp.threeDot)
	}
	if i := t.indexOf(l.top); i >= 0 {
		t.list.ScrollTo(i, ui.Start)
	}
	if l.worktree {
		a.showWorktree(t)
	}
}

func (a *app) historyToolbar(c *ui.Context, t *repoTab) {
	if t.query.filePath != "" {
		a.fileHistoryToolbar(c, t)
		return
	}
	ui.Row(c).Padding(4, 12, 8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		scope := 0
		if !t.query.hideRemote {
			scope = 1
		}
		if ui.Segmented(c.Key("branch-visibility"), &scope, "本地分支", "全部分支").Label("分支显示范围").Changed() {
			q := t.query
			q.ref, q.hideRemote = "", scope == 0
			a.changeScope(t, q)
		}
		a.scopeMenu(c, t)
		locate := ui.TextInput(c.Key(fmt.Sprintf("locate-%d", t.id)), &t.locate).Placeholder("HEAD、分支、标签或哈希").Grow(1).MinWidth(80)
		if ui.Button(c.Key("locate-ref"), "跳到").Clicked() || locate.Submitted() {
			a.locateRef(t, t.locate)
		}
		if ui.Button(c.Key("refresh"), "刷新").Disabled(t.loading).Clicked() {
			a.refresh(t)
		}
		if ui.Button(c.Key("compare"), "比较").Disabled(t.loading).Clicked() {
			a.openCompareModal(t)
		}
	})
	ui.Row(c).Padding(0, 12, 8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Select(c.Key(fmt.Sprintf("search-kind-%d", t.id)), &t.searchKind, searchKinds).MinWidth(96)
		input := ui.TextInput(c.Key(fmt.Sprintf("search-%d", t.id)), &t.searchText).Bind(&t.searchInput).Grow(1).MinWidth(80)
		busy := t.loading && strings.TrimSpace(t.searchText) == t.query.term && t.searchKind == t.query.kind
		if ui.Button(c.Key("search-history"), "搜索").Disabled(busy).Clicked() || (input.Submitted() && !busy) {
			a.searchHistory(t)
		}
		if t.query.term != "" {
			if ui.Button(c.Key("search-clear"), "返回").Clicked() {
				a.leaveSearch(t)
			}
		}
	})
}

func (a *app) scopeMenu(c *ui.Context, t *repoTab) {
	label := "筛选分支"
	if t.query.ref == "HEAD" {
		label = "当前分支"
	} else if t.query.ref != "" {
		label = strings.TrimPrefix(strings.TrimPrefix(t.query.ref, "refs/heads/"), "refs/remotes/")
	}
	ui.MenuButton(c.Key("history-scope"), label, func(m *ui.Menu) {
		for _, opt := range []struct{ label, ref string }{{"范围内全部分支", ""}, {"当前分支", "HEAD"}} {
			if m.Item(opt.label).Checked(t.query.ref == opt.ref).Chosen() {
				q := t.query
				q.ref = opt.ref
				a.changeScope(t, q)
			}
		}
		m.Separator()
		var branches []string
		for _, refs := range t.refs {
			for _, ref := range refs {
				if ref.kind == refBranch {
					branches = append(branches, "refs/heads/"+ref.name)
				} else if ref.kind == refRemote && !t.query.hideRemote {
					branches = append(branches, "refs/remotes/"+ref.name)
				}
			}
		}
		sort.Strings(branches)
		for _, ref := range branches {
			name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/")
			if strings.HasPrefix(ref, "refs/remotes/") {
				name = "远程 · " + name
			} else {
				name = "本地 · " + name
			}
			if m.Item(name).Checked(t.query.ref == ref).Chosen() {
				q := t.query
				q.ref = ref
				a.changeScope(t, q)
			}
		}
	}).MinWidth(120).MaxWidth(240)
}

func (a *app) changeScope(t *repoTab, q historyQuery) {
	if t.historyBack != nil {
		t.historyBack = &historyLocation{query: historyQuery{ref: q.ref, hideRemote: q.hideRemote}}
	}
	a.changeHistory(t, q)
}
