package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/2fa0/xylring/internal/crashlog"

	"github.com/egoist/mygo/ui"
)

func (a *app) post(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
		return
	}
	if a.win == nil {
		fn()
		return
	}
	a.win.Update(fn)
}

func (a *app) setBanner(msg, path string) {
	a.banner = msg
	a.bannerPath = path
}

// bannerWithoutLocation 去掉消息尾部的位置：调用方已经把仓库路径单列一行，
// 再在文案里说一遍就是同一事实占了两行。
func bannerWithoutLocation(msg, path string) string {
	for _, loc := range []string{repoLocation(path), path} {
		if loc == "" {
			continue
		}
		for _, sep := range []string{"：", ": "} {
			if trimmed := strings.TrimSuffix(msg, sep+loc); trimmed != msg {
				return trimmed
			}
		}
	}
	return msg
}

// gitLogBannerButton 打开命令日志，选中最近一条失败。它只在这次会话真的有过
// Git 调用失败时出现：磁盘错误、会话保存失败这类横幅背后没有可看的命令。
func (a *app) gitLogBannerButton(c *ui.Context) {
	dir := ""
	if a.bannerPath != "" {
		dir = a.bannerPath
	}
	if _, ok := gitLog.lastFailure(dir); !ok {
		return
	}
	if ui.Button(c.Key("banner-git-log"), tr("查看 Git 日志")).Clicked() {
		a.openGitLog(dir, true)
	}
}

func (a *app) notifySuccess(tab *repoTab, msg string) {
	if tab != nil {
		tab.notice = msg
	}
	a.pendingToast = msg
}

func (a *app) bannerOnRecent() bool {
	if a.banner == "" || a.bannerPath == "" {
		return false
	}
	return slices.Contains(a.recent, a.bannerPath)
}

func (a *app) activeTab() *repoTab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

func (a *app) remember(path string) {
	out := []string{path}
	for _, p := range a.recent {
		if p != path && len(out) < 12 {
			out = append(out, p)
		}
	}
	a.setRecent(out)
}

func (a *app) forget(path string) {
	out := make([]string, 0, len(a.recent))
	for _, p := range a.recent {
		if p != path {
			out = append(out, p)
		}
	}
	if len(out) == len(a.recent) {
		return
	}
	if a.bannerPath == path {
		a.setBanner("", "")
	}
	a.setRecent(out)
}

func (a *app) setRecent(out []string) {
	a.recent = out
	if !a.persist {
		return
	}
	if a.recentWrites == nil {
		a.recentWrites = make(chan []string, 1)
		writes := a.recentWrites
		go func() {
			for paths := range writes {
				if err := saveRecent(paths); err != nil {
					a.post(func() {
						if !a.closed {
							a.setBanner("保存最近仓库失败："+err.Error(), "")
						}
					})
				}
			}
		}()
	}
	select {
	case <-a.recentWrites:
	default:
	}
	a.recentWrites <- out
}

func (a *app) pickRepo() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	go func() {
		paths, err := openRepoDialog(win)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.setBanner(trimMsg(err.Error()), "")
				return
			}
			if len(paths) == 0 {
				return
			}
			a.openPath(paths[0])
		})
	}()
}

func (a *app) pickWSLRepo() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	go func() {
		defaultPath := defaultWSLRepoRoot()
		paths, err := openWSLRepoDialog(win, defaultPath)
		a.post(func() {
			a.picking = false
			if err != nil {
				a.setBanner(trimMsg(err.Error()), "")
				return
			}
			if len(paths) == 0 {
				return
			}
			a.openPath(paths[0])
		})
	}()
}

func (a *app) openRepo(path string) {
	if isDockerRepoPath(path) {
		go func() {
			// 容器仓库的恢复要多花几条 docker 命令：容器检查、端点核对、Git 探测。
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			root, err := repoRoot(ctx, path)
			a.post(func() {
				if err != nil {
					log.Printf("openRepo failed repo=%q err=%q", repoLocation(path), err)
					a.setBanner(trimMsg(err.Error()), path)
					return
				}
				a.openRoot(root)
			})
		}()
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		root, err := repoRoot(ctx, path)
		if _, statErr := os.Stat(path); statErr == nil && !isGitWorkTree(path) {
			err = errRepoUnavailable
		}
		a.post(func() {
			if err != nil {
				log.Printf("openRepo failed path=%q err=%q", path, err)
				a.setBanner(trimMsg(err.Error()), path)
				return
			}
			a.openRoot(root)
			// 导入目录会扫描它下面的仓库；最近仓库只留了一个路径，打开后补扫一次，
			// 仓库里嵌套的仓库照旧列在左侧。
			a.scanRepos(root, false)
		})
	}()
}

func (a *app) openPath(path string) {
	if isDockerRepoPath(path) {
		a.openRepo(path)
		return
	}
	crashlog.Breadcrumb("打开路径 " + repoLocation(path))
	a.scanRepos(path, true)
}

func (a *app) refresh(t *repoTab) {
	a.reload(t)
	a.rescanFound()
}

func (a *app) rescanFound() {
	from := a.foundFrom
	if from == "" {
		if tab := a.activeTab(); tab != nil {
			from = tab.path
		}
	}
	if from != "" {
		a.scanRepos(from, false)
	}
}

func (a *app) scanRepos(path string, open bool) {
	if a.closed || isDockerRepoPath(path) {
		// 容器仓库没有可递归扫描的本机目录，也不允许把容器键当主机路径用。
		return
	}
	// Repeated refreshes share the in-flight scan; a new import supersedes it.
	if !open && a.scanPath == path && a.scanCancel != nil {
		return
	}
	if a.scanCancel != nil {
		a.scanCancel()
	}
	a.foundGen++
	gen := a.foundGen
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.scanCancel, a.scanPath = cancel, path
	previous := a.scanCache
	go func() {
		defer cancel()
		from := workspaceDir(path)
		statInfo, statErr := os.Stat(path)
		isDir := statErr == nil && statInfo.IsDir()
		isRepoDir := isGitWorkTree(from)
		roots, cache, err := scanGitRepos(ctx, path, previous)
		a.post(func() {
			if a.closed || a.foundGen != gen {
				return
			}
			a.scanCancel, a.scanPath = nil, ""
			if err != nil {
				log.Printf("scanRepos failed path=%q open=%v err=%q", path, open, err)
				if open {
					if err.Error() == "没有找到 Git 仓库" && isDir {
						a.setBanner("", "")
						a.openFolderImportInit(from)
						return
					}
					a.setBanner(trimMsg(err.Error()), path)
				}
				return
			}
			a.setBanner("", "")
			a.scanCache = cache
			if open && len(roots) > 1 && !isRepoDir {
				a.openFolderImportRepos(from, roots)
				return
			}
			a.setFound(from, roots)
			if open && len(roots) > 0 {
				a.openRoot(roots[0])
			}
		})
	}()
}

type folderImportKind int

const (
	folderImportInit folderImportKind = iota
	folderImportRepos
)

type folderImportDialog struct {
	open     bool
	kind     folderImportKind
	dir      string
	repos    []string
	selected []bool
	busy     bool
	err      string
	gen      int
}

func (a *app) openFolderImportInit(dir string) {
	a.folderImport = folderImportDialog{
		open: true,
		kind: folderImportInit,
		dir:  dir,
		gen:  a.folderImport.gen + 1,
	}
}

func (a *app) openFolderImportRepos(dir string, repos []string) {
	selected := make([]bool, len(repos))
	for i := range selected {
		selected[i] = true
	}
	a.folderImport = folderImportDialog{
		open:     true,
		kind:     folderImportRepos,
		dir:      dir,
		repos:    slices.Clone(repos),
		selected: selected,
		gen:      a.folderImport.gen + 1,
	}
}

func (a *app) confirmFolderImport() {
	d := &a.folderImport
	if !d.open || d.kind != folderImportRepos {
		return
	}
	var selected []string
	for i, repo := range d.repos {
		if i < len(d.selected) && d.selected[i] {
			selected = append(selected, repo)
		}
	}
	if len(selected) == 0 {
		return
	}
	dir := d.dir
	d.open = false
	a.setFound(dir, selected)
	a.openRoot(selected[0])
}

func (a *app) runFolderImportInit() {
	d := &a.folderImport
	if !d.open || d.kind != folderImportInit || d.busy || d.dir == "" {
		return
	}
	d.busy = true
	d.err = ""
	dir := d.dir
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := runGitExtra(ctx, dir, 1<<20, []string{"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true"}, "init")
		a.post(func() {
			if a.closed || a.folderImport.gen != gen {
				return
			}
			a.folderImport.busy = false
			if err != nil {
				a.folderImport.err = trimMsg(err.Error())
				return
			}
			a.folderImport.open = false
			a.openRoot(dir)
		})
	}()
}

func folderImportRepoLabel(rootDir, repoPath string) string {
	if rel, err := filepath.Rel(rootDir, repoPath); err == nil && rel != "" && rel != "." {
		return rel
	}
	if base := filepath.Base(repoPath); base != "" && base != "." {
		return base
	}
	return repoPath
}

func (a *app) folderImportModal(c *ui.Context) {
	d := &a.folderImport
	if !d.open {
		return
	}
	wasOpen := d.open
	theme := c.Theme()
	switch d.kind {
	case folderImportInit:
		appModal(c.Key("folder-import-init-modal"), &d.open, tr("当前目录不是 Git 仓库"), 460, d.busy, func() {
			ui.Text(c, d.dir).FontSize(12).Font("monospace").TextColor(theme.TextMuted)
			if d.err != "" {
				ui.Text(c, trMessage(d.err)).FontSize(12).TextColor(theme.Danger)
			}
			if d.busy {
				busyRow(c, "folder-import-init-busy", tr("正在初始化 Git 仓库…"), "")
			}
		}, func() {
			if ui.Button(c.Key("folder-import-init-cancel"), tr("取消")).Disabled(d.busy).Clicked() {
				d.open = false
			}
			if ui.PrimaryButton(c.Key("folder-import-init-confirm"), tr("初始化 Git 仓库")).Disabled(d.busy).Clicked() {
				a.runFolderImportInit()
			}
		})
		if wasOpen && !d.open && d.busy {
			d.open = true
		}
	case folderImportRepos:
		selectedCount := 0
		for _, ok := range d.selected {
			if ok {
				selectedCount++
			}
		}
		allSelected := len(d.repos) > 0 && selectedCount == len(d.repos)
		dirName := filepath.Base(d.dir)
		if dirName == "" || dirName == "." || dirName == string(filepath.Separator) {
			dirName = d.dir
		}
		appModal(c.Key("folder-import-repos-modal"), &d.open, tr("导入 Git 仓库"), 520, false, func() {
			ui.Row(c.Key("folder-import-header")).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, fmt.Sprintf(tr("在「%s」下发现 %d 个 Git 仓库"), dirName, len(d.repos))).
					FontSize(13).Grow(1).MinWidth(0)
				ui.Checkbox(c.Key("folder-import-all"), &allSelected, tr("全选")).OnClick(func() {
					for i := range d.selected {
						d.selected[i] = allSelected
					}
				})
			})
			ui.Column(c.Key("folder-import-list")).FillWidth().Gap(6).Children(func() {
				for i, repo := range d.repos {
					if i >= len(d.selected) {
						break
					}
					label := folderImportRepoLabel(d.dir, repo)
					ui.Checkbox(c.Key(fmt.Sprintf("folder-import-repo-%d", i)), &d.selected[i], label)
				}
			})
		}, func() {
			if ui.Button(c.Key("folder-import-repos-cancel"), tr("取消")).Clicked() {
				d.open = false
			}
			confirmLabel := fmt.Sprintf(tr("导入 (%d)"), selectedCount)
			if ui.PrimaryButton(c.Key("folder-import-repos-confirm"), confirmLabel).Disabled(selectedCount == 0).Clicked() {
				a.confirmFolderImport()
			}
		})
	}
}

func (a *app) setFound(from string, roots []string) {
	a.foundFrom = from
	// A scan replaces its own subtree, not repositories imported elsewhere.
	found := slices.Clone(roots)
	for _, path := range a.found {
		if path != from && !repoContains(from, path) && !slices.Contains(found, path) {
			found = append(found, path)
		}
	}
	if len(found) > 1 || len(a.found) > 0 {
		a.found = found
		a.refreshRepoSummaries()
		return
	}
	a.found = nil
}

func workspaceDir(path string) string {
	if isDockerRepoPath(path) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	info, err := os.Stat(abs)
	if err == nil && !info.IsDir() {
		return filepath.Dir(abs)
	}
	return abs
}

func (a *app) openRoot(root string) {
	crashlog.Breadcrumb("打开仓库 " + repoLocation(root))
	if a.closed {
		return
	}
	a.leaveBoard()
	a.showHome = false
	for i, t := range a.tabs {
		if t.path == root {
			a.activate(i)
			a.checkActiveRepo()
			a.remember(root)
			return
		}
	}
	a.nextID++
	tab := newTab(a.nextID, root)
	a.tabs = append(a.tabs, tab)
	a.activate(len(a.tabs) - 1)
	a.setBanner("", "")
	a.remember(root)
	if !tab.loading {
		a.reload(tab)
	}
}

func (a *app) activate(i int) {
	if a.active != i {
		if old := a.activeTab(); old != nil {
			old.stopHistory()
			old.generation++
			old.loading = false
		}
	}
	a.active = i
	if t := a.activeTab(); t != nil {
		t.usedAt = time.Now()
	}
	a.trimInactiveHistory()
	if t := a.activeTab(); t != nil && !t.loaded && !t.loading {
		a.reload(t)
	}
}

func (a *app) closeTab(i int) {
	if i < 0 || i >= len(a.tabs) {
		return
	}
	a.tabs[i].closed = true
	a.tabs[i].cancel()
	a.closeAllFileTabs(a.tabs[i])
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active > i || a.active >= len(a.tabs) {
		a.active--
	}
	if a.active < 0 {
		a.active = 0
	}
	if len(a.tabs) == 0 {
		a.recentSearch = ""
	}
	if t := a.activeTab(); !a.closed && t != nil {
		if !t.loaded && !t.loading {
			a.reload(t)
		} else {
			a.checkActiveRepo()
		}
	}
}

// closeRepoTree 关掉一个仓库和它下面嵌套的仓库。侧栏的父子只是按路径算出来的缩进，
// 列表里它们仍是平级条目，所以清理父级要自己把子树捞干净，否则子行会原地退回顶层。
func (a *app) closeRepoTree(path string) {
	var closing []int
	for i, t := range a.tabs {
		if t.path == path || repoContains(path, t.path) {
			closing = append(closing, i)
		}
	}
	for i := len(closing) - 1; i >= 0; i-- {
		a.closeTab(closing[i])
	}
	a.found = slices.DeleteFunc(a.found, func(p string) bool {
		return p == path || repoContains(path, p)
	})
}

func (a *app) goHome() {
	if a.scanCancel != nil {
		a.scanCancel()
		a.scanCancel, a.scanPath = nil, ""
	}
	a.setBanner("", "")
	a.recentSearch = ""
	a.showCompare = false
	a.showWrite = false
	a.stopFetch()
	a.leaveBoard()
	a.showHome = true
}

func (a *app) reload(t *repoTab) {
	if a.closed || t.closed {
		return
	}
	t.stopHistory()
	t.generation++
	t.locateGen++
	gen, worktreeGen := t.generation, t.worktreeRefreshGen
	t.worktreeCache = nil
	t.nextRepoCheck, t.repoCheckDelay = time.Time{}, 0
	t.loading = true
	t.historyStarted = time.Now()
	t.err, t.locateErr, t.notice = "", "", ""
	t.locateHash = zeroHash
	path, query := t.path, t.query
	previousWatch := t.refWatch
	query.provs = &t.provenance
	query.deferStats = true
	n := max(pageFirst, len(t.commits))
	budget := max(int64(historyMemoryStep), t.historyBudget)
	location := t.location()
	streamCtx, streamCancel := context.WithCancel(context.Background())
	t.pagerCancel = streamCancel
	ctx, cancel := context.WithTimeout(streamCtx, 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		watch := newRefWatch(ctx, path)
		meta, err := loadMeta(ctx, path)
		if err == nil && previousWatch.replaced(path, watch) {
			a.post(func() {
				if t.generation == gen && !t.closed {
					a.resetRepoHistory(t)
					a.reload(t)
				}
			})
			return
		}
		if err == nil {
			meta.ringStatus = ringHookStatus(ctx, path)
			meta.stamp, _ = readStamp(ctx, path)
		}
		var tips []string
		var dirty []fileChange
		if err == nil {
			tips, err = queryTips(ctx, path, meta, query)
		}
		var p *historyPager
		var prepared preparedHistory
		more := false
		if err == nil {
			p = newHistoryPager(streamCtx, path, query, tips, 0)
			var commits []commit
			commits, more, err = p.next(ctx, n, budget)
			// New commits can push the old anchor just beyond the loaded range. Probe
			// one extra batch, never exhaust the repository looking for a vanished hash.
			var readBytes int64
			for i := range commits {
				readBytes += commitBytes(&commits[i])
			}
			if err == nil && more && readBytes < budget && !containsLocation(commits, location) {
				var extra []commit
				extra, more, err = p.next(ctx, pageFirst, budget-readBytes)
				commits = append(commits, extra...)
			}
			if err == nil {
				prepared, err = prepareHistory(ctx, commits, graph{}, nil, true, query.filtered())
			}
		}
		if err == nil && len(prepared.commits) == 0 {
			dirty, _ = worktreeStatus(ctx, path)
		}
		key := meta.key()
		a.post(func() {
			if t.generation != gen || t.closed {
				if p != nil {
					p.close()
				}
				return
			}
			t.loading, t.loaded = false, true
			if err != nil {
				log.Printf("reload failed repo=%q err=%q", path, err)
				a.probeDockerReplacement(t)
				if p != nil {
					p.close()
				}
				a.noteGitUnavailable(err)
				if errors.Is(err, errRepoUnavailable) {
					a.markRepoUnavailable(t)
				} else {
					t.err = "读取提交历史失败：" + err.Error()
				}
				return
			}
			t.unavailable = false
			t.historyTips, t.metaKey, t.pager = tips, key, p
			t.refWatch, t.refsChecked = watch, time.Now()
			if a.activeTab() != t || !more {
				p.close()
				t.pager = nil
			}
			t.hasMore = more
			a.installHistory(t, meta, prepared)
			if len(prepared.commits) == 0 {
				a.applyWorktree(t, dirty)
			}
			if t.pendingSession != nil && a.activeTab() == t {
				r := t.pendingSession
				t.pendingSession = nil
				t.restoreFile = r.File
				if h, e := parseHash(r.Commit); e == nil {
					a.jumpToHash(t, h)
				}
			}
		})
		if err != nil || len(prepared.commits) == 0 {
			return
		}
		worktreeCtx, cancelWorktree := context.WithTimeout(ctx, 10*time.Second)
		defer cancelWorktree()
		// 用新变量接结果：这里的 err 还被上面那个 post 回调读着，后台线程不能再写它。
		files, cache, worktreeErr := pollWorktreeStatus(worktreeCtx, path, nil)
		if worktreeErr == nil {
			a.post(func() {
				if !t.closed && t.generation == gen && t.worktreeRefreshGen == worktreeGen {
					t.worktreeCache = cache
					a.applyWorktree(t, files)
				}
			})
		}
	}()
}

func containsLocation(commits []commit, location *historyLocation) bool {
	foundHash, foundTop := location.hash == zeroHash, location.top == zeroHash
	for _, c := range commits {
		foundHash = foundHash || c.hash == location.hash
		foundTop = foundTop || c.hash == location.top
	}
	return foundHash && foundTop
}

func (a *app) loadMore(t *repoTab) {
	if t.loading || !t.hasMore || t.closed {
		return
	}
	if !t.canLoadHistory() {
		t.notice = "历史已达到本轮内存预算，可点击继续加载"
		return
	}
	t.loading = true
	t.historyStarted = time.Now()
	t.err = ""
	gen, query := t.generation, t.query
	query.provs = &t.provenance
	query.deferStats = true
	if t.pager == nil {
		streamCtx, cancel := context.WithCancel(context.Background())
		t.pagerCancel = cancel
		t.pager = newHistoryPager(streamCtx, t.path, query, t.historyTips, len(t.commits))
	}
	p := t.pager
	baseLen, authors, g := len(t.commits), t.authors, t.graph.clone()
	n := min(4096, max(pageMore, baseLen/2))
	remaining := t.historyBudget - t.historyBytes
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.historyCancel = cancel
	go func() {
		defer cancel()
		commits, more, err := p.next(ctx, n, remaining)
		var prepared preparedHistory
		if err == nil {
			prepared, err = prepareHistory(ctx, commits, g, authors, true, query.filtered())
		}
		a.post(func() {
			if t.generation != gen || t.closed {
				p.close()
				return
			}
			t.loading = false
			if err != nil {
				p.close()
				t.pager = nil
				t.err, t.locateHash = "加载更多提交失败："+err.Error(), zeroHash
				return
			}
			if !more || a.activeTab() != t {
				p.close()
				t.pager = nil
			}
			t.hasMore = more
			// Only the accepted page mutates UI-owned history and its index.
			if t.commitIndex == nil {
				t.commitIndex = make(map[[20]byte]int, baseLen+len(prepared.commits))
				for i, c := range t.commits {
					t.commitIndex[c.hash] = i
				}
			}
			for h, i := range prepared.index {
				t.commitIndex[h] = baseLen + i
			}
			prepared.commits = append(t.commits, prepared.commits...)
			prepared.index = t.commitIndex
			prepared.bytes += t.historyBytes
			a.installHistoryPage(t, prepared)
		})
	}()
}

func (t *repoTab) canLoadHistory() bool {
	if t.historyBudget == 0 {
		t.historyBudget = historyMemoryStep
	}
	return t.historyBytes < t.historyBudget
}

// Also used by small synchronous model tests; production prepares on the worker.
func (a *app) applyHistory(t *repoTab, meta repoMeta, commits []commit, _, page int) {
	prepared, _ := prepareHistory(context.Background(), commits, graph{}, nil, true, t.query.filtered())
	if page >= 0 {
		t.hasMore = len(commits) == page
	}
	a.installHistory(t, meta, prepared)
}

func (a *app) installHistory(t *repoTab, meta repoMeta, p preparedHistory) {
	t.branch, t.head, t.refs = meta.branch, meta.head, meta.refs
	t.operation = meta.operation
	if meta.ringStatus != "" {
		t.ring.status = meta.ringStatus
		t.ring.stamp = meta.stamp
		t.ring.checked = time.Now()
	}
	t.ahead, t.behind, t.upstream, t.hasUpstream = meta.ahead, meta.behind, meta.upstream, meta.hasUpstream
	// 分页重建的 meta 不带工作树（nil），只有整仓加载才覆盖列表。
	if meta.worktrees != nil {
		t.worktrees = meta.worktrees
	}
	t.incoming, t.outgoing = hashSet(meta.incoming), hashSet(meta.outgoing)
	a.summaryMu.Lock()
	if a.repoSummaries != nil {
		a.repoSummaries[t.path] = repoSummary{
			branch:      t.branch,
			dirty:       t.worktreeCount > 0,
			checked:     time.Now(),
			ahead:       t.ahead,
			behind:      t.behind,
			upstream:    t.upstream,
			hasUpstream: t.hasUpstream,
		}
	}
	a.summaryMu.Unlock()
	a.installHistoryPage(t, p)
}

func (a *app) installHistoryPage(t *repoTab, p preparedHistory) {
	selected := zeroHash
	if item := t.rowData(t.selected); item != nil {
		selected = item.hash
	}
	t.graph, t.authors, t.commitIndex, t.historyBytes = p.graph, p.authors, p.index, p.bytes
	t.commits = p.commits
	t.selected = t.rowIndex(selected)
	for key := range t.selection.All() {
		h, ok := key.([20]byte)
		if !ok || t.rowIndex(h) < 0 {
			t.selection.Remove(key)
		}
	}
	if t.inspecting != zeroHash && t.indexOf(t.inspecting) < 0 {
		a.closeInspection(t)
		t.notice = "原提交已不在当前范围内"
	}
	t.holdAuto = true
	a.continueRestore(t)
	a.continueLocate(t)
	a.trimInactiveHistory()
}

func (a *app) locateRef(t *repoTab, ref string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	t.locateErr = ""
	t.locateGen++
	gen, repoGen := t.locateGen, t.generation
	path := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		hash, err := resolveCommit(ctx, path, ref)
		a.post(func() {
			if t.generation != repoGen || t.locateGen != gen {
				return
			}
			if err != nil {
				t.locateErr = err.Error()
				return
			}
			a.jumpToHash(t, hash)
		})
	}()
}

func (a *app) jumpToHash(t *repoTab, hash [20]byte) {
	if i := t.rowIndex(hash); i >= 0 {
		t.locateHash = zeroHash
		a.selectCommit(t, i)
		return
	}
	if t.loading {
		t.locateHash = hash
		return
	}
	if !t.hasMore {
		t.locateHash = zeroHash
		t.locateErr = "图谱里没有这个提交"
		if t.query.term != "" || t.query.filePath != "" || t.query.ref != "" || t.query.hideRemote {
			t.locateErr = "当前范围内没有这个提交"
		}
		return
	}
	t.locateHash = hash
	a.loadMore(t)
}

func (a *app) continueLocate(t *repoTab) {
	if t.locateHash == zeroHash {
		return
	}
	a.jumpToHash(t, t.locateHash)
}

func (a *app) selectCommit(t *repoTab, i int) {
	item := t.rowData(i)
	if item == nil {
		return
	}
	if a.sidebarHidden {
		a.sidebarHidden = false
	}
	if item.hash == worktreeHash {
		a.showWorktree(t)
		t.list.ScrollIntoView(i)
		return
	}
	t.worktree = false
	if t.comparison != nil {
		a.closeComparison(t)
	}
	t.selected = i
	t.selection.Clear()
	t.selection.Add(item.hash)
	t.list.ScrollIntoView(i)
	a.showInspection(t, item)
}
