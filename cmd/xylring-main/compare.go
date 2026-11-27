package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type commitComparison struct {
	from, to           [20]byte
	fromLabel, toLabel string
	threeDot           bool
}

func parseCompareRange(from, to string) (string, string, bool, error) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if i := strings.Index(to, "..."); i >= 0 {
		left, right := strings.TrimSpace(to[:i]), strings.TrimSpace(to[i+3:])
		if left == "" || right == "" || strings.HasPrefix(right, ".") {
			return "", "", false, errors.New("请输入两个 ref，例如 main...feature")
		}
		return left, right, true, nil
	}
	if i := strings.Index(to, ".."); i >= 0 {
		left, right := strings.TrimSpace(to[:i]), strings.TrimSpace(to[i+2:])
		if left == "" || right == "" {
			return "", "", false, errors.New("请输入两个 ref，例如 main..feature")
		}
		return left, right, false, nil
	}
	if i := strings.Index(from, "..."); i >= 0 && to == "" {
		left, right := strings.TrimSpace(from[:i]), strings.TrimSpace(from[i+3:])
		if left == "" || right == "" || strings.HasPrefix(right, ".") {
			return "", "", false, errors.New("请输入两个 ref，例如 main...feature")
		}
		return left, right, true, nil
	}
	if i := strings.Index(from, ".."); i >= 0 && to == "" {
		left, right := strings.TrimSpace(from[:i]), strings.TrimSpace(from[i+2:])
		if left == "" || right == "" {
			return "", "", false, errors.New("请输入两个 ref，例如 main..feature")
		}
		return left, right, false, nil
	}
	if from == "" || to == "" {
		return "", "", false, errors.New("请输入起点和终点，分支、标签或哈希都可以")
	}
	return from, to, false, nil
}

func (t *repoTab) refLabel(h [20]byte) string {
	if h == t.head && t.head != zeroHash {
		if t.branch != "" && !strings.HasPrefix(t.branch, "分离头指针") && t.branch != "尚无提交" {
			return t.branch
		}
		return "HEAD"
	}
	if refs := t.refs[h]; len(refs) > 0 {
		return refs[0].name
	}
	return shortHash(h)
}

func selectedPair(t *repoTab) (from, to [20]byte, ok bool) {
	if t.selection.Len() != 2 {
		return
	}
	var hashes [][20]byte
	for key := range t.selection.All() {
		h, isHash := key.([20]byte)
		if !isHash || h == worktreeHash {
			return
		}
		hashes = append(hashes, h)
	}
	if len(hashes) != 2 {
		return
	}
	if item := t.rowData(t.selected); item != nil {
		if item.hash == hashes[0] {
			return hashes[1], hashes[0], true
		}
		if item.hash == hashes[1] {
			return hashes[0], hashes[1], true
		}
	}
	return hashes[0], hashes[1], true
}

func selectedOne(t *repoTab) ([20]byte, bool) {
	if t.selection.Len() != 1 {
		return zeroHash, false
	}
	for key := range t.selection.All() {
		h, ok := key.([20]byte)
		return h, ok
	}
	return zeroHash, false
}

func (t *repoTab) refSuggestions() []string {
	if t == nil {
		return []string{"HEAD"}
	}
	var locals, remotes, tags []string
	seen := make(map[string]bool)
	for _, refs := range t.refs {
		for _, ref := range refs {
			if seen[ref.name] {
				continue
			}
			seen[ref.name] = true
			switch ref.kind {
			case refBranch:
				locals = append(locals, ref.name)
			case refRemote:
				remotes = append(remotes, ref.name)
			case refTag:
				tags = append(tags, ref.name)
			}
		}
	}
	sort.Strings(locals)
	sort.Strings(remotes)
	sort.Strings(tags)

	var list []string
	if t.branch != "" && !strings.HasPrefix(t.branch, "分离头指针") && t.branch != "尚无提交" {
		list = append(list, t.branch)
	}
	list = append(list, "HEAD")
	for _, b := range locals {
		if b != t.branch {
			list = append(list, b)
		}
	}
	list = append(list, remotes...)
	list = append(list, tags...)

	commitLimit := 100
	if len(t.commits) < commitLimit {
		commitLimit = len(t.commits)
	}
	for i := 0; i < commitLimit; i++ {
		h := t.commits[i].hash
		if h == zeroHash || h == worktreeHash {
			continue
		}
		sh := shortHash(h)
		if !seen[sh] {
			seen[sh] = true
			list = append(list, sh)
		}
	}

	return list
}

func (a *app) openCompareModalWith(t *repoTab, from, to string) {
	if t == nil {
		a.showCompare, a.compareErr = true, ""
		a.compareFrom, a.compareTo = "HEAD", ""
		return
	}
	t.contentTab = 0
	t.revealFileTab = true
	a.showCompare, a.compareErr = true, ""
	if from == "" {
		from = "HEAD"
		if t.branch != "" && !strings.HasPrefix(t.branch, "分离头指针") && t.branch != "尚无提交" {
			from = t.branch
		}
	}
	a.compareFrom = from
	a.compareTo = to
}

func (a *app) openCompareModal(t *repoTab) {
	if t == nil {
		a.showCompare, a.compareErr = true, ""
		a.compareFrom, a.compareTo = "HEAD", ""
		return
	}
	t.contentTab = 0
	t.revealFileTab = true
	a.showCompare, a.compareErr = true, ""
	if from, to, ok := selectedPair(t); ok {
		a.compareFrom, a.compareTo = t.refLabel(from), t.refLabel(to)
		return
	}
	base := "HEAD"
	if t.branch != "" && !strings.HasPrefix(t.branch, "分离头指针") && t.branch != "尚无提交" {
		base = t.branch
	}
	a.compareFrom = base
	a.compareTo = ""
	if item := t.rowData(t.selected); item != nil && item.hash != worktreeHash {
		a.compareTo = hashHex(item.hash)
	}
}

func (a *app) comparisonModal(c *ui.Context) {
	wasOpen := a.showCompare
	t := a.activeTab()
	suggestions := t.refSuggestions()
	theme := c.Theme()

	var fromInput, toInput ui.Element
	appModal(c.Key("compare-modal"), &a.showCompare, tr("比较"), 480, false, func() {
		ui.Row(c).Gap(8).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, tr("从")).FontSize(12).TextColor(theme.TextMuted)
				fromInput = ui.Autocomplete(c.Key("compare-from"), &a.compareFrom, suggestions).Placeholder("HEAD").FillWidth()
			})
			if ui.Button(c.Key("compare-swap"), "⇄").Label(tr("交换")).Shrink(0).Clicked() {
				a.compareFrom, a.compareTo = a.compareTo, a.compareFrom
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, tr("到")).FontSize(12).TextColor(theme.TextMuted)
				toInput = ui.Autocomplete(c.Key("compare-to"), &a.compareTo, suggestions).Placeholder(tr("分支、标签或哈希")).FillWidth()
			})
		})

		ui.Checkbox(c.Key("compare-three"), &a.compareThree, tr("从分叉点起"))

		if a.compareErr != "" {
			ui.Text(c, trMessage(a.compareErr)).FontSize(12).TextColor(theme.Danger)
		}
	}, func() {
		if ui.Button(c.Key("compare-cancel"), tr("取消")).Clicked() {
			a.showCompare = false
		}
		busy := t == nil || t.compareLoading
		submitted := (fromInput.Submitted() || toInput.Submitted()) && !busy
		if ui.PrimaryButton(c.Key("compare-apply"), tr("开始比较")).Disabled(busy).Clicked() || submitted {
			from, to, three, err := parseCompareRange(a.compareFrom, a.compareTo)
			if err != nil {
				a.compareErr = err.Error()
				return
			}
			if t == nil {
				a.compareErr = "没有打开的仓库"
				return
			}
			a.compareRefs(t, from, to, three || a.compareThree)
		}
	})
	if wasOpen && !a.showCompare {
		if t := a.activeTab(); t != nil && t.compareLoading && t.comparison == nil {
			if t.compareCancel != nil {
				t.compareCancel()
				t.compareCancel = nil
			}
			t.compareLoading = false
		}
	}
}

func (a *app) compareRefs(t *repoTab, from, to string, threeDot bool) {
	if t.compareCancel != nil {
		t.compareCancel()
	}
	t.compareGen++
	navGen, path := t.compareGen, t.path
	wasModal := a.showCompare
	a.compareErr, t.compareLoading = "", true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		left, err := resolveCommit(ctx, path, from)
		var right [20]byte
		if err == nil {
			right, err = resolveCommit(ctx, path, to)
		}
		a.post(func() {
			if t.closed || t.compareGen != navGen {
				return
			}
			if wasModal && !a.showCompare {
				return
			}
			t.compareLoading = false
			if err != nil {
				if wasModal {
					a.compareErr = err.Error()
				} else {
					t.compareErr = err.Error()
				}
				return
			}
			a.showCompare = false
			a.showComparison(t, left, right, from, to, threeDot)
		})
	}()
}

func (a *app) showComparison(t *repoTab, from, to [20]byte, fromLabel, toLabel string, threeDot bool) {
	if a.sidebarHidden {
		a.sidebarHidden = false
	}
	t.worktree = false
	a.clearDetails(t)
	t.inspecting = zeroHash
	if t.detailSplit == 0 {
		t.detailSplit = 52
	}
	t.comparison = &commitComparison{from: from, to: to, fromLabel: fromLabel, toLabel: toLabel, threeDot: threeDot}
	t.selected = -1
	t.selection.Clear()
	fromIdx, toIdx := t.indexOf(from), t.indexOf(to)
	if fromIdx >= 0 && toIdx >= 0 {
		t.selection.Add(from)
		t.selection.Add(to)
		t.selected = t.rowIndex(to)
	}
	a.loadComparisonDiff(t)
}

func (a *app) loadComparisonDiff(t *repoTab) {
	cmp := t.comparison
	if cmp == nil {
		return
	}
	if !cmp.threeDot {
		a.startDiff(t, cmp.from, cmp.to, func(ctx context.Context, path string) ([]fileChange, error) {
			return compareFiles(ctx, path, hashHex(cmp.from), hashHex(cmp.to))
		})
		return
	}
	if t.compareCancel != nil {
		t.compareCancel()
	}
	t.compareGen++
	gen, path := t.compareGen, t.path
	from, to := cmp.from, cmp.to
	t.compareLoading, t.compareErr = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		base, err := mergeBase(ctx, path, from, to)
		a.post(func() {
			if t.closed || t.compareGen != gen || t.comparison != cmp {
				return
			}
			if err != nil {
				t.compareLoading = false
				t.compareErr = err.Error()
				return
			}
			a.startDiff(t, base, to, func(ctx context.Context, p string) ([]fileChange, error) {
				return compareFiles(ctx, p, hashHex(base), hashHex(to))
			})
		})
	}()
}

func (a *app) showInspection(t *repoTab, item *commit) {
	if item == nil {
		return
	}
	if t.inspecting == item.hash && t.comparison == nil && !t.worktree {
		return
	}
	t.worktree = false
	a.clearDetails(t)
	t.comparison = nil
	t.inspecting = item.hash
	t.inspectParent = 0
	if t.detailSplit == 0 {
		t.detailSplit = 52
	}
	a.ensureDetails(t)
	a.startCommitDiff(t, item, 0)
}

func (a *app) startCommitDiff(t *repoTab, item *commit, parentIdx int) {
	if item == nil {
		return
	}
	hash := item.hash
	file := item.file
	parents := append([][20]byte(nil), item.parents...)
	var from [20]byte
	if parentIdx >= 0 && parentIdx < len(parents) {
		from = parents[parentIdx]
	} else if len(parents) > 0 {
		from = parents[0]
	}
	a.startDiff(t, from, hash, func(ctx context.Context, path string) ([]fileChange, error) {
		if file != nil {
			return []fileChange{*file}, nil
		}
		if parentIdx > 0 && parentIdx < len(parents) {
			return compareFiles(ctx, path, hashHex(parents[parentIdx]), hashHex(hash))
		}
		return commitFiles(ctx, path, hash, parents)
	})
}

func (a *app) compareCommitPair(t *repoTab, aHash, bHash [20]byte) {
	if t == nil || aHash == zeroHash || bHash == zeroHash || aHash == bHash {
		return
	}
	from, to := aHash, bHash
	if iA, iB := t.indexOf(aHash), t.indexOf(bHash); iA >= 0 && iB >= 0 && iA < iB {
		// iA < iB means aHash is newer than bHash; compare older (bHash) -> newer (aHash).
		from, to = bHash, aHash
	}
	a.showComparison(t, from, to, t.refLabel(from), t.refLabel(to), false)
}

func (a *app) closeBottomPane(t *repoTab) {
	if t == nil {
		return
	}
	t.diffMaximized = false
	if t.comparison != nil {
		a.endComparison(t)
		return
	}
	t.selected = -1
	t.selection.Clear()
	if t.worktree {
		a.closeWorktree(t)
	}
	if t.inspecting != zeroHash {
		a.closeInspection(t)
	}
}

func (a *app) startDiff(t *repoTab, from, to [20]byte, list func(context.Context, string) ([]fileChange, error)) {
	if t.compareCancel != nil {
		t.compareCancel()
	}
	t.compareGen++
	gen := t.compareGen
	t.diffFrom, t.diffTo = from, to
	// 已读和行级笔记只属于这一个对比：换了来源就一起清空，不写成两个 if。
	if reviewCtx := diffReviewContext(t, from, to); reviewCtx != t.reviewCtx {
		t.reviewCtx = reviewCtx
		t.reviewed = nil
		t.diffNotes = nil
		t.noteEdit = nil
	}
	t.compareFiles = nil
	t.compareErr = ""
	t.compareLoading = true
	t.diff, t.simpleDiff, t.imageDiff, t.diffSource = nil, nil, nil, nil
	t.diffNote = ""
	t.diffLoading = false
	t.openFileTabAfterLoad = false
	t.fileSel = -1
	t.fileList = ui.ListState{}
	t.fileListScroll, t.fileListScrollY = ui.ScrollState{}, 0
	t.fileHoverID, t.fileHoverY = "", 0
	t.resetFileTree()
	if t.fileSplit <= 100 {
		t.fileSplit = a.defaultFileSplit()
	}
	path := t.path
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		files, err := list(ctx, path)
		a.post(func() {
			if t.closed || t.compareGen != gen {
				return
			}
			t.compareLoading = false
			if err != nil {
				t.compareErr = err.Error()
				return
			}
			t.compareFiles = files
			if len(files) == 0 {
				t.restoreDiff, t.restoreFile = nil, ""
				t.diffNote = "没有文件变化"
				return
			}
			t.fileSel = 0
			if t.restoreFile != "" {
				if i := restoreFileIndex(files, t.restoreFile); i >= 0 {
					t.fileSel = i
				}
				t.restoreFile = ""
			}
			if row := t.fileRowIndex(t.fileSel); row >= 0 {
				t.fileList.ScrollIntoView(row)
			}
			t.resetFileTree()
			a.loadFileDiff(t, t.fileSel)
		})
	}()
}

// restoreFileIndex 找回刷新前选中的那一行。行换了组（暂存或取消暂存之后）
// 就停在同一个文件上，别跳回列表顶端。
func restoreFileIndex(files []fileChange, want string) int {
	if want == "" {
		return -1
	}
	for i, f := range files {
		if f.rowKey() == want {
			return i
		}
	}
	path := want
	if _, rest, ok := strings.Cut(want, "\x00"); ok {
		path = rest
	}
	for i, f := range files {
		if f.path == path {
			return i
		}
	}
	return -1
}

func (a *app) closeComparison(t *repoTab) {
	t.comparison = nil
	if t.inspecting == zeroHash && !t.worktree {
		a.clearDiff(t)
	}
}

func (a *app) endComparison(t *repoTab) {
	t.selected = -1
	t.selection.Clear()
	a.closeComparison(t)
	a.closeInspection(t)
}

func (a *app) closeInspection(t *repoTab) {
	a.clearDetails(t)
	t.inspecting = zeroHash
	if t.comparison != nil {
		return
	}
	a.clearDiff(t)
}

func (a *app) clearDiff(t *repoTab) {
	if t.compareCancel != nil {
		t.compareCancel()
		t.compareCancel = nil
	}
	t.compareGen++
	t.compareFiles = nil
	t.compareLoading = false
	t.compareErr = ""
	t.diff, t.simpleDiff, t.imageDiff, t.diffSource = nil, nil, nil, nil
	t.diffNote = ""
	t.diffLoading = false
	t.fileSel = -1
	t.fileFilter = ""
	t.diffFrom, t.diffTo = zeroHash, zeroHash
}

// Keep the original bytes even when the displayed patch omits whitespace changes.
type fileDiffSource struct {
	old, new []byte
	note     string
}

func readFileDiffSource(ctx context.Context, dir, fromRev, toRev string, f fileChange, worktree bool) (*fileDiffSource, error) {
	oldPath, newPath := f.path, f.path
	if f.oldPath != "" {
		oldPath = f.oldPath
	}
	if f.status == "A" {
		oldPath = ""
	}
	if f.status == "D" {
		newPath = ""
	}
	// 冲突文件的索引里没有 stage 0，两侧基准都拿不到内容，仍按 HEAD→工作区看整份。
	if worktree && f.side != sideNone && f.extra != "冲突" {
		return readWorktreeSideSource(ctx, dir, fromRev, f.side, oldPath, newPath)
	}
	s := &fileDiffSource{}
	var err error
	s.old, s.note, err = readBlob(ctx, dir, fromRev, oldPath)
	if err != nil {
		return nil, err
	}
	var note string
	if worktree {
		s.new, note, err = readWorktreeFile(ctx, dir, newPath)
	} else {
		s.new, note, err = readBlob(ctx, dir, toRev, newPath)
	}
	if err != nil {
		return nil, err
	}
	if note != "" {
		s.note = note
	}
	return s, ctx.Err()
}

// readWorktreeSideSource 按行所属的一侧选基准：
// 已暂存是 HEAD→索引，未暂存是索引→工作区。拆开之后，暂存过的改动不会
// 再和暂存后新改的内容混在同一份 diff 里。
func readWorktreeSideSource(ctx context.Context, dir, fromRev string, side fileSide, oldPath, newPath string) (*fileDiffSource, error) {
	s := &fileDiffSource{}
	var err error
	if side == sideStaged {
		s.old, s.note, err = readBlob(ctx, dir, fromRev, oldPath)
		if err != nil {
			return nil, err
		}
		var note string
		s.new, note, err = readIndexBlob(ctx, dir, newPath)
		if note != "" {
			s.note = note
		}
		return s, err
	}
	s.old, s.note, err = readIndexBlob(ctx, dir, oldPath)
	if err != nil {
		return nil, err
	}
	var note string
	s.new, note, err = readWorktreeFile(ctx, dir, newPath)
	if note != "" {
		s.note = note
	}
	return s, err
}

func (s *fileDiffSource) prepare(ctx context.Context, path string, ignoreWhitespace bool) (*editor.DiffState, *simpleDiff, *imageDiff, string, error) {
	if s.note != "" {
		return nil, nil, nil, s.note, nil
	}
	if isImageFile(path) {
		var oldInfo, newInfo *imageInfo
		var oldErr, newErr error
		if len(s.old) > 0 {
			oldInfo, oldErr = decodeImageInfo(s.old, path)
		}
		if len(s.new) > 0 {
			newInfo, newErr = decodeImageInfo(s.new, path)
		}
		if oldErr != nil && len(s.old) > 0 && newErr != nil && len(s.new) > 0 {
			return nil, nil, nil, fmt.Sprintf("图片解码失败：%v", newErr), nil
		}
		if oldErr != nil && len(s.old) > 0 {
			return nil, nil, nil, fmt.Sprintf("历史图片解码失败：%v", oldErr), nil
		}
		if newErr != nil && len(s.new) > 0 {
			return nil, nil, nil, fmt.Sprintf("新图片解码失败：%v", newErr), nil
		}
		if oldInfo == nil && newInfo == nil {
			return nil, nil, nil, "图片文件为空", nil
		}
		return nil, nil, &imageDiff{old: oldInfo, new: newInfo}, "", nil
	}
	if !ignoreWhitespace {
		diff, simple := prepareDiff(s.old, s.new, path)
		return diff, simple, nil, "", ctx.Err()
	}
	dir, err := os.MkdirTemp("", "xylring-diff-")
	if err != nil {
		return nil, nil, nil, "", err
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string][]byte{"old": s.old, "new": s.new} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, nil, nil, "", err
		}
	}
	out, err := runGitLimit(ctx, dir, 16<<20, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--src-prefix=a/", "--dst-prefix=b/", "-w", "-p", "--", "old", "new")
	if err != nil {
		return nil, nil, nil, "", err
	}
	patches, err := editor.ParsePatch(string(out))
	if err != nil {
		return nil, nil, nil, "", err
	}
	if len(patches) > 0 && patches[0].Binary {
		return nil, nil, nil, "二进制文件，已忽略差异", nil
	}
	if len(patches) == 0 || len(patches[0].Hunks) == 0 {
		return nil, nil, nil, "无改动（空白字符改动已忽略）", nil
	}
	d := &editor.DiffState{}
	d.SetPatch(patches[0])
	return d, nil, nil, "", nil
}

func (a *app) loadFileDiff(t *repoTab, i int) {
	if i < 0 || i >= len(t.compareFiles) {
		t.diff, t.simpleDiff, t.imageDiff, t.diffSource, t.diffNote, t.diffLoading = nil, nil, nil, nil, "", false
		return
	}
	if t.compareCancel != nil {
		t.compareCancel()
	}
	t.compareGen++
	gen := t.compareGen
	f := t.compareFiles[i]
	if t.noteEdit != nil && t.noteEdit.noteKey() != f.rowKey() {
		t.noteEdit = nil
	}
	if saved := t.restoreDiff; saved != nil {
		t.restoreDiff = nil
		if saved.from == t.diffFrom && saved.to == t.diffTo && saved.file == f.rowKey() && (saved.diff != nil || saved.imageDiff != nil || saved.note != "") {
			t.diff, t.simpleDiff, t.imageDiff, t.diffSource, t.diffNote, t.diffLoading = saved.diff, saved.simpleDiff, saved.imageDiff, saved.source, saved.note, false
			a.finishOpenFileTab(t)
			return
		}
	}
	t.diffLoading, t.diff, t.simpleDiff, t.imageDiff, t.diffSource, t.diffNote = true, nil, nil, nil, nil, ""
	fromRev, toRev := "", ""
	if t.diffFrom != zeroHash {
		fromRev = hashHex(t.diffFrom)
	}
	if t.diffTo != zeroHash {
		toRev = hashHex(t.diffTo)
	}
	path, worktree, ignoreWhitespace := t.path, t.worktree, a.diffIgnoreWhitespace
	// 忽略空白依赖本机临时目录里的 git diff --no-index；本机缺 Git 时容器仓库仍可看差异。
	if ignoreWhitespace && isDockerRepoPath(path) && !a.gitReady() {
		ignoreWhitespace = false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		var diff *editor.DiffState
		var simple *simpleDiff
		var imgDiff *imageDiff
		var note string
		source, err := readFileDiffSource(ctx, path, fromRev, toRev, f, worktree)
		stamp := ""
		if err == nil {
			stamp = fileContentStamp(source)
			diff, simple, imgDiff, note, err = source.prepare(ctx, f.path, ignoreWhitespace && f.status != "A" && f.status != "D")
		}
		if err == nil {
			err = ctx.Err()
		}
		a.post(func() {
			if t.closed || t.compareGen != gen {
				return
			}
			t.diffLoading = false
			t.diffSource = source
			t.applyFileStamp(f.rowKey(), stamp)
			if err != nil {
				t.diffNote = err.Error()
				t.openFileTabAfterLoad = false
				return
			}
			if note != "" {
				t.diffNote = note
				a.finishOpenFileTab(t)
				return
			}
			t.diff, t.simpleDiff, t.imageDiff = diff, simple, imgDiff
			a.finishOpenFileTab(t)
			a.trimInactiveHistory()
		})
	}()
}

func (a *app) maybeInspectOrCompare(t *repoTab) {
	if t.selection.Len() == 2 {
		a.maybeCompareSelection(t)
		return
	}
	if t.comparison != nil {
		return
	}
	h, ok := selectedOne(t)
	if !ok {
		if t.worktree {
			a.closeWorktree(t)
		}
		return
	}
	if h == worktreeHash {
		if !t.worktree {
			a.showWorktree(t)
		}
		return
	}
	if t.worktree {
		a.closeWorktree(t)
	}
	a.maybeInspectSelection(t)
}

func (a *app) maybeInspectSelection(t *repoTab) {
	h, ok := selectedOne(t)
	if !ok {
		return
	}
	if t.inspecting == h {
		return
	}
	i := t.indexOf(h)
	if i < 0 {
		return
	}
	a.showInspection(t, &t.commits[i])
}

func (a *app) maybeCompareSelection(t *repoTab) {
	from, to, ok := selectedPair(t)
	if !ok {
		return
	}
	if t.comparison != nil && t.comparison.from == from && t.comparison.to == to && !t.worktree {
		return
	}
	a.showComparison(t, from, to, t.refLabel(from), t.refLabel(to), false)
}

func copyPathTip(c *ui.Context) string {
	alt := "Alt"
	if runtime.GOOS == "darwin" {
		alt = "⌥"
	}
	if c.Modifiers()&ui.Alt != 0 {
		return tr("复制绝对路径")
	}
	return tr("复制相对路径\n") + alt + tr(" 复制绝对路径")
}

func repoFilePath(repo, rel string, abs bool) string {
	if !abs {
		return rel
	}
	if t, ok := parseDockerRepoKey(repo); ok {
		// 容器内的绝对路径就是用户能拿去用的路径，不映射成本机路径。
		return path.Join(t.path, rel)
	}
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func absolutePathMenuLabel(repo string) string {
	if isDockerRepoPath(repo) {
		return tr("复制容器内路径")
	}
	return tr("复制绝对路径")
}

func copyFilePath(c *ui.Context, repo, rel string, mods ui.Modifiers) {
	abs := mods&ui.Alt != 0
	c.Services().WriteClipboard(repoFilePath(repo, rel, abs))
	switch {
	case abs && isDockerRepoPath(repo):
		c.Toast(tr("已复制容器内路径"))
	case abs:
		c.Toast(tr("已复制绝对路径"))
	default:
		c.Toast(tr("已复制相对路径"))
	}
}

func (a *app) fileHoverActions(c *ui.Context, t *repoTab, i int, hovered bool) {
	if !hovered || i < 0 || i >= len(t.compareFiles) {
		return
	}
	theme := c.Theme()
	bg := theme.SurfaceHover
	if i == t.fileSel {
		bg = theme.Accent
	}
	f := t.compareFiles[i]
	ui.Row(c).Gap(2).AlignItems(ui.Center).Padding(2, 6).Background(bg).Radius(0, theme.Radius, theme.Radius, 0).Label(tr("文件操作")).Absolute().Top(0).Right(0).Bottom(0).Children(func() {
		fileReviewButton(c, t, f)
		if t.worktree {
			a.worktreeStageHoverButtons(c, t, f)
		}
		copy := fileIconButton(c, "copy-file-path-"+f.rowKey(), "复制路径", iconCopy).Tooltip(copyPathTip(c))
		if copy.Clicked() {
			copyFilePath(c, t.path, f.path, copy.ClickModifiers())
		}
		open := fileIconButton(c, "open-file-tab-"+f.rowKey(), "在新标签页打开", iconOpenTab).Disabled(t.compareLoading || t.diffLoading)
		if open.Clicked() {
			a.openFileTabAt(t, i)
		}
		if t.query.filePath == "" {
			fileIconButton(c, "file-history-"+f.rowKey(), "文件历史", iconHistory).OnClick(func() {
				a.openFileHistoryAt(t, i)
			})
		}
	})
}

func statusColor(theme *ui.Theme, st string) ui.Color {
	switch statusLetter(st) {
	case "A", "C", "R":
		return theme.Success
	case "D", "U":
		return theme.Danger
	case "M", "T":
		return theme.Warning
	default:
		return theme.TextMuted
	}
}

func statusTip(f fileChange) string {
	if f.extra != "" {
		return f.extra
	}
	return statusWord(f.status)
}

func fileStatusMark(c *ui.Context, f fileChange) {
	theme := c.Theme()
	if f.extra == "未跟踪" {
		ui.Text(c, "U").Font("monospace").FontSize(12).FontWeight(600).TextColor(theme.Success).TextAlign(ui.End).MinWidth(16).Shrink(0).Tooltip(tr(statusTip(f)))
		return
	}
	mark := statusLetter(f.status)
	if mark == "" || mark == "?" {
		return
	}
	ui.Text(c, mark).Font("monospace").FontSize(12).FontWeight(600).TextColor(statusColor(theme, f.status)).TextAlign(ui.End).MinWidth(16).Shrink(0).Tooltip(tr(statusTip(f)))
}

func takeScrollMove(s *ui.ScrollState, last *float32) bool {
	y := s.Y
	moved := y != *last
	*last = y
	return moved
}

func filePathTip(path string, scrolling bool) string {
	if scrolling {
		return ""
	}
	return path
}

func (t *repoTab) fileRowMoving(c *ui.Context, row ui.Element, id string, hovered bool) bool {
	if !hovered || id == "" {
		return false
	}
	row.Bind(&t.fileHover)
	y := t.fileHover.Bounds(c).Y
	moving := t.fileHoverID == id && t.fileHoverY != 0 && y != t.fileHoverY
	t.fileHoverID = id
	t.fileHoverY = y
	return moving
}

func fileTrailingMeta(c *ui.Context, f fileChange, hovered bool) {
	if hovered {
		return
	}
	fileLineStats(c, f)
	fileStatusMark(c, f)
}

func fileLineStats(c *ui.Context, f fileChange) {
	if !f.hasStat || f.binary {
		return
	}
	theme := c.Theme()
	muted := theme.TextMuted
	ui.Row(c).Gap(4).Shrink(0).AlignItems(ui.Center).Tooltip(fmt.Sprintf(tr("新增 %d 行，删除 %d 行"), f.added, f.deleted)).Children(func() {
		ui.Text(c, "(").FontSize(12).TextColor(muted).Shrink(0)
		ui.Text(c, fmt.Sprintf("+%d", f.added)).Font("monospace").FontSize(12).TextColor(theme.Success).Shrink(0)
		ui.Text(c, "|").FontSize(12).TextColor(muted).Shrink(0)
		ui.Text(c, fmt.Sprintf("-%d", f.deleted)).Font("monospace").FontSize(12).TextColor(theme.Danger).Shrink(0)
		ui.Text(c, ")").FontSize(12).TextColor(muted).Shrink(0)
	})
}

// fileListRow 是文件列表里的一行：分组标题，或一个文件。
// 未提交改动的暂存和未暂存各自成组，同一文件两侧都有改动时出现两次。
type fileListRow struct {
	file    int // compareFiles 下标，-1 表示分组标题
	section fileSide
}

func (r fileListRow) key(files []fileChange) string {
	if r.file < 0 || r.file >= len(files) {
		return sectionKey(r.section)
	}
	return files[r.file].rowKey()
}

// fileListRows 在文件行前面插分组标题。两侧都有改动时才分组；只剩一侧时标题一带而过、
// 又占一行，不如直接列出文件。没有分侧的列表（提交之间的对比）也不加标题。
// 分组看的是仓库状态（files 是整份列表），筛选只是不出这一侧的行，不改变分组本身。
func fileListRows(files []fileChange, indices []int) []fileListRow {
	rows := make([]fileListRow, 0, len(indices)+2)
	grouped := groupFileSides(files)
	last := sideNone
	for _, idx := range indices {
		if idx < 0 || idx >= len(files) {
			continue
		}
		if side := files[idx].side; grouped && side != sideNone && side != last {
			rows = append(rows, fileListRow{file: -1, section: side})
			last = side
		}
		rows = append(rows, fileListRow{file: idx})
	}
	return rows
}

// groupedFileSides 是这份列表是否带分组标题。
func (t *repoTab) groupedFileSides() bool {
	return groupFileSides(t.compareFiles)
}

// soloFileSide 说仓库是否只剩一侧：只剩一侧时列表不分组，这一侧的批量操作
// 要另找地方放，不能跟着分组标题一起消失。
func (t *repoTab) soloFileSide() fileSide {
	if groupFileSides(t.compareFiles) {
		return sideNone
	}
	side := sideNone
	for _, f := range t.compareFiles {
		if f.side != sideNone {
			side = f.side
		}
	}
	return side
}

// fileRowIndex 把 compareFiles 的下标换成列表行号：分组标题也占行。
func (t *repoTab) fileRowIndex(file int) int {
	for i, r := range fileListRows(t.compareFiles, t.filteredFileIndices()) {
		if r.file == file {
			return i
		}
	}
	return -1
}

// fileSectionRow 是分组标题：标题点名这一组是什么，右边是这一组的批量操作。
// 计数只留在图谱里的未提交改动徽章，这里不再重复一遍。
func (a *app) fileSectionRow(c *ui.Context, t *repoTab, side fileSide) {
	theme := c.Theme()
	ui.Row(c).FillWidth().MinHeight(26).Padding(4, 6).Gap(6).AlignItems(ui.Center).Background(theme.Background).Children(func() {
		ui.Text(c, fileSideTitle(side)).FontSize(12).Bold().TextColor(theme.TextMuted).SingleLine().Shrink(0)
		ui.Box(c).Grow(1).MinWidth(0)
		a.bulkStageButton(c, t, side)
	})
}

// bulkStageButton 是整份列表的批量操作，出现在分组标题右侧，或（只有一侧时）列表行尾。
// 它作用于整个仓库，筛选只是不出某些行，所以 tooltip 把范围说出来。
func (a *app) bulkStageButton(c *ui.Context, t *repoTab, side fileSide) {
	switch side {
	case sideStaged:
		if paneIconButton(c, fmt.Sprintf("staged-unstage-all-%d", t.id), tr("全部取消暂存（整个仓库）"), iconMinus).Clicked() {
			a.unstageAllWorktree(t)
		}
	case sideUnstaged:
		if paneIconButton(c, fmt.Sprintf("unstaged-stage-all-%d", t.id), tr("全部暂存（整个仓库）"), iconPlus).Clicked() {
			a.stageAllWorktree(t)
		}
	}
}

func (a *app) fileFlatList(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	scrolling := takeScrollMove(&t.fileListScroll, &t.fileListScrollY)
	indices := t.filteredFileIndices()
	if len(indices) == 0 && len(t.compareFiles) > 0 {
		noMatchingFiles(c, &t.fileFilter)
		return
	}
	rows := fileListRows(t.compareFiles, indices)
	currentSel := -1
	for row, r := range rows {
		if r.file == t.fileSel {
			currentSel = row
			break
		}
	}
	selectedRow := currentSel
	var action func()
	t.fileList.Selected = &selectedRow
	t.fileList.Header = func(i int) bool { return rows[i].file < 0 }
	t.fileList.Key = func(i int) any {
		if i < 0 || i >= len(rows) {
			return i
		}
		return rows[i].key(t.compareFiles)
	}
	t.fileList.Label = func(i int) string {
		if i < 0 || i >= len(rows) {
			return ""
		}
		if rows[i].file < 0 {
			return fileSideTitle(rows[i].section)
		}
		return t.compareFiles[rows[i].file].rowLabel()
	}
	list := ui.List(c.Key(fmt.Sprintf("compare-files-%d", t.id)), &t.fileList, len(rows), func(i int) {
		if i < 0 || i >= len(rows) {
			return
		}
		if rows[i].file < 0 {
			a.fileSectionRow(c, t, rows[i].section)
			return
		}
		origIdx := rows[i].file
		f := t.compareFiles[origIdx]
		muted := theme.TextMuted
		if origIdx == t.fileSel {
			muted = theme.Text.Alpha(0.75)
		}
		dir := filepath.Dir(f.path)
		if dir == "." {
			dir = tr("仓库根目录")
		}
		row := ui.Column(c).FillWidth().MinHeight(48).Padding(6, 10).Gap(4)
		hovered := row.Hovered()
		path := f.fullPath()
		row.Tooltip(filePathTip(path, scrolling || t.fileRowMoving(c, row, "list:"+f.rowKey(), hovered)))
		if row.RowDoubleClicked() {
			a.openFileTabAt(t, origIdx)
		}
		row.ContextMenu(func(m *ui.Menu) {
			action = a.fileRowMenu(c, m, t, origIdx)
		})
		row.Children(func() {
			ui.Row(c).Gap(6).MinWidth(0).AlignItems(ui.Center).Children(func() {
				if t.worktree {
					a.worktreeStageCheckbox(c, t, f, "wt-stage-list")
				}
				name := ui.Text(c, filepath.Base(f.path)).FontSize(13).SingleLine().Grow(1).MinWidth(0)
				if t.fileViewed(f.rowKey()) {
					name.TextColor(theme.TextMuted) // 已读用布局表达，不补“已读”两个字。
				}
				fileTrailingMeta(c, f, hovered)
			})
			ui.Text(c, dir).FontSize(11).TextColor(muted).SingleLine().Grow(1).MinWidth(0)
			a.fileHoverActions(c, t, origIdx, hovered)
		})
	}).Grow(1).MinHeight(0).Padding(4, 8).Gap(2).TrackScroll(&t.fileListScroll)
	if action != nil {
		action()
		return
	}
	if list.Changed() && selectedRow >= 0 && selectedRow < len(rows) && rows[selectedRow].file >= 0 {
		t.fileSel = rows[selectedRow].file
		a.loadFileDiff(t, t.fileSel)
	}
}

func (a *app) fileTreeList(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	scrolling := takeScrollMove(&t.fileTreeScroll, &t.fileTreeScrollY)
	tree := t.prepareFileTree()
	if len(tree.roots) == 0 && len(t.compareFiles) > 0 {
		noMatchingFiles(c, &t.fileFilter)
		return
	}
	t.fileOutline.Indent = theme.Space(1.5)
	t.syncFileOutlineSel(tree)
	t.fileOutline.List.Selected = &t.fileOutlineSel
	t.fileOutline.List.Label = func(i int) string {
		if i < 0 || i >= t.fileOutline.Rows() {
			return ""
		}
		n := tree.nodes[t.fileOutline.Item(i)]
		if n.section != sideNone {
			return fileSideTitle(n.section)
		}
		if n.dir || n.file < 0 || n.file >= len(t.compareFiles) {
			return n.name
		}
		return t.compareFiles[n.file].rowLabel()
	}
	var action func()
	outline := ui.Outline(c.Key(fmt.Sprintf("compare-tree-%d", t.id)), &t.fileOutline, tree.roots, tree.children, func(key string) {
		n := tree.nodes[key]
		if n.section != sideNone {
			a.fileSectionRow(c, t, n.section)
			return
		}
		file := n.file >= 0 && n.file < len(t.compareFiles)
		tip := key
		if file {
			tip = t.compareFiles[n.file].fullPath()
		}
		row := ui.Row(c).Gap(5).MinWidth(0).Grow(1).MinHeight(20).Padding(1, 0).AlignItems(ui.Center)
		hovered := row.Hovered()
		id := "tree:" + key
		if file {
			id = "tree:" + t.compareFiles[n.file].rowKey()
		}
		row.Tooltip(filePathTip(tip, scrolling || t.fileRowMoving(c, row, id, hovered)))
		if file {
			idx := n.file
			if row.RowDoubleClicked() {
				a.openFileTabAt(t, idx)
			}
			row.ContextMenu(func(m *ui.Menu) {
				action = a.fileRowMenu(c, m, t, idx)
			})
		}
		row.Children(func() {
			if t.worktree && file {
				a.worktreeStageCheckbox(c, t, t.compareFiles[n.file], "wt-stage-tree")
			}
			name := ui.Text(c, n.name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
			if file && t.fileViewed(t.compareFiles[n.file].rowKey()) {
				name.TextColor(theme.TextMuted)
			}
			if file {
				f := t.compareFiles[n.file]
				fileTrailingMeta(c, f, hovered)
				a.fileHoverActions(c, t, n.file, hovered)
			}
		})
	}).Grow(1).MinHeight(0).Padding(4, 6).Gap(1).TrackScroll(&t.fileTreeScroll)
	if action != nil {
		action()
		return
	}
	if outline.Changed() {
		if t.fileOutlineSel < 0 || t.fileOutlineSel >= t.fileOutline.Rows() {
			return
		}
		n := tree.nodes[t.fileOutline.Item(t.fileOutlineSel)]
		if n.dir || n.file < 0 || n.file >= len(t.compareFiles) || n.file == t.fileSel {
			return
		}
		t.fileSel = n.file
		a.loadFileDiff(t, t.fileSel)
	}
}

// Return history navigation so it cannot clear compareFiles while rows are being built.
func (a *app) fileRowMenu(c *ui.Context, m *ui.Menu, t *repoTab, idx int) func() {
	if idx < 0 || idx >= len(t.compareFiles) {
		return nil
	}
	var after func()
	f := t.compareFiles[idx]
	if m.Item(tr("在新标签页打开")).Chosen() {
		a.openFileTabAt(t, idx)
	}
	if t.worktree {
		a.worktreeFileMenu(c, m, t, f)
	}
	if !t.worktree {
		actions := []string{"Blame", "行历史", "恢复历史文件"}
		if isDockerRepoPath(t.path) {
			// 容器仓库只读历史，不提供恢复文件到工作区。
			actions = actions[:2]
		}
		for _, action := range actions {
			if m.Item(tr(action) + "…").Chosen() {
				a.openOperation(action, hashHex(t.diffTo), f.path)
			}
		}
	}
	if m.Item(tr("查看此版本完整文件")).Chosen() {
		rev := hashHex(t.diffTo)
		revLabel := shortHash(t.diffTo)
		if t.worktree {
			rev = "worktree"
			revLabel = "工作区"
		}
		a.openFullFileTab(t, f.path, rev, revLabel)
	}
	if t.query.filePath == "" {
		if m.Item(tr("查看文件历史")).Chosen() {
			after = func() { a.openFileHistoryAt(t, idx) }
		}
	}
	m.Separator()
	if m.Item(tr("复制相对路径")).Chosen() {
		copyFilePath(c, t.path, f.path, 0)
	}
	if m.Item(absolutePathMenuLabel(t.path)).Chosen() {
		copyFilePath(c, t.path, f.path, ui.Alt)
	}
	// 容器里的文件没有本机外部编辑器或访达位置；只保留复制容器内路径。
	if isDockerRepoPath(t.path) {
		return after
	}
	m.Separator()
	fullPath := repoFilePath(t.path, f.path, true)
	if m.Item(a.openEditorMenuLabelForContext(t.worktree)).Chosen() {
		if _, err := os.Stat(fullPath); err != nil {
			c.Toast(tr("工作区中不存在此文件"))
		} else if err := a.openInEditor(fullPath); err != nil {
			c.Toast(trMessage("打开失败: " + err.Error()))
		}
	}
	revealName := "在访达中显示"
	if runtime.GOOS != "darwin" {
		revealName = "在文件管理器中显示"
	}
	if m.Item(tr(revealName)).Chosen() {
		if _, err := os.Stat(fullPath); err != nil {
			c.Toast(tr("工作区中不存在此文件"))
		} else if err := revealInFileManager(fullPath); err != nil {
			c.Toast(trMessage("定位失败: " + err.Error()))
		}
	}
	return after
}

func (a *app) fileDiffContent(c *ui.Context, diff *editor.DiffState, simple *simpleDiff, imgDiff *imageDiff, note string, loading bool, viewMode int, fitActual bool) {
	theme := c.Theme()
	switch {
	case loading:
		busyBlock(c, "diff-file-loading", tr("正在读取文件…"), "").Grow(1)
	case imgDiff != nil:
		a.imageDiffView(c, imgDiff, viewMode, fitActual)
	case note != "":
		ui.Column(c).Grow(1).Padding(16).Children(func() {
			ui.Text(c, trMessage(note)).TextColor(theme.TextMuted)
		})
	case simple != nil:
		simple.old.Label = tr("原版本")
		simple.new.Label = tr("新版本")
		simple.old.RenderFindBar = renderFindBar
		simple.new.RenderFindBar = renderFindBar
		ui.Row(c).Grow(1).MinHeight(0).Children(func() {
			editor.View(c.Key("large-diff-old"), &simple.old).Grow(1).Basis(0).FillHeight().MinWidth(0)
			editor.View(c.Key("large-diff-new"), &simple.new).Grow(1).Basis(0).FillHeight().MinWidth(0)
		})
	case diff != nil:
		a.prepareDiffState(diff)
		diff.RenderFindBar = renderFindBar
		editor.DiffView(c.Key("file-diff"), diff).Grow(1).MinHeight(0)
	}
}

func renderFindBar(c *ui.Context, anchor ui.Element, f *editor.FindBar) {
	anchor.Left(4).Right(4).Height(0)
	// Diff separators paint above the editors; the find panel must escape that layer.
	ui.Overlay(c, func() {
		bar := ui.Column(c.Key(anchor.ID())).
			AttachTo(anchor, ui.AnchorTopRight, ui.AnchorTopRight).
			Width(360).MaxWidthPercent(100).Role(ui.RoleToolbar).Label(tr("文件内查找")).
			Cursor(ui.CursorDefault).HandleInput(func(ui.InputEvent) bool { return false })
		renderFindBarContent(c, bar, f)
	})
}

// findQueryDelay keeps the diff from revealing a match under the pointer on
// every keystroke, which scrolls the text away and repaints it twice.
const findQueryDelay = 150 * time.Millisecond

// findQueryInput is the query as typed, and the one the editor searches for.
type findQueryInput struct {
	text, applied string
	edited        time.Time
}

func renderFindBarContent(c *ui.Context, bar ui.Element, f *editor.FindBar) {
	theme := c.Theme()
	query := ui.Local(bar, f.Query(), func() findQueryInput {
		return findQueryInput{text: *f.Query(), applied: *f.Query()}
	})
	// Opening Find from a selection can seed a new query in the editor.
	if query.applied != *f.Query() {
		query.text, query.applied = *f.Query(), *f.Query()
	}
	commit := func() {
		*f.Query(), query.applied = query.text, query.text
	}
	// Split panes can be narrower than the editor's default single-row toolbar.
	bar.Padding(6).Gap(4).Background(theme.Surface).
		Border(1, theme.Border).Radius(theme.Radius)
	bar.Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			before := query.text
			input := ui.TextInput(c.Key("query"), &query.text).Label(tr("查找内容")).
				Placeholder(tr("查找")).Grow(1).MinWidth(0)
			if query.text != before {
				query.edited = c.Now()
			}
			if query.text != query.applied {
				// Searching moves the text to the match, so the editor searches
				// once the typing pauses, and at once for Enter and an emptied
				// query, which needs no pause to say what it finds.
				wait := findQueryDelay - c.Now().Sub(query.edited)
				if query.text == "" || input.Submitted() || wait <= 0 {
					commit()
				} else {
					c.After(wait)
				}
			}
			f.QueryInput(input)
		})
		ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
			toggle := func(value *bool, label, tip string) {
				b := ui.ToggleBase(c.Key(tip), value).Label(tr(tip)).Tooltip(tr(tip)).
					Padding(2, 4).Shrink(0).Radius(theme.Radius).KeepFocus()
				if *value {
					b.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent)
				}
				b.Children(func() { ui.Text(c, tr(label)).FontSize(11).SingleLine() })
			}
			toggle(f.CaseSensitive(), "Aa", "区分大小写")
			toggle(f.WholeWord(), "ab", "全字匹配")
			toggle(f.Regexp(), ".*", "正则表达式")
			toggle(f.InSelection(), "[ ]", "在选区中查找")
			ui.Box(c).Grow(1)
			button := func(label, tip string) bool {
				b := ui.ButtonBase(c.Key(tip)).Label(tr(tip)).Tooltip(tr(tip)).
					Padding(2, 4).Shrink(0).Radius(theme.Radius).KeepFocus()
				b.Children(func() { ui.Text(c, tr(label)).FontSize(11).SingleLine() })
				return b.Clicked()
			}
			if button("↑", "上一个匹配") {
				commit()
				f.Previous()
			}
			if button("↓", "下一个匹配") {
				commit()
				f.Next()
			}
			if button("✕", "关闭查找") {
				f.Close()
			}
		})
		// The row keeps its line while the typing pauses, so the panel does not
		// grow with the first result and shrink back to nothing.
		status := " "
		if query.text == query.applied {
			// Matches searches the text the bar read before it was built.
			f.Matches()
			if s := f.Status(); s != "" {
				status = s
			}
		}
		ui.Text(c, status).FontSize(11).TextColor(theme.TextMuted).Role(ui.RoleStatus)
	})
}

// Only a worker touches these buffers and caches until ownership is handed to UI.
type simpleDiff struct{ old, new editor.State }

func prepareDiff(oldb, newb []byte, path string) (*editor.DiffState, *simpleDiff) {
	oldBuf, newBuf := editor.NewBuffer(string(oldb)), editor.NewBuffer(string(newb))
	d := &editor.DiffState{Old: oldBuf, New: newBuf}
	if oldBuf.LineCount() > 100000 || newBuf.LineCount() > 100000 {
		return d, &simpleDiff{
			old: editor.State{Buffer: oldBuf, ReadOnly: true, Label: "原版本"},
			new: editor.State{Buffer: newBuf, ReadOnly: true, Label: "新版本"},
		}
	}
	lang := editor.LanguageFor(path)
	oldBuf.SetLanguage(lang)
	newBuf.SetLanguage(lang)
	for _, h := range d.Hunks() {
		if h.OldEnd-h.OldStart > 2000 || h.NewEnd-h.NewStart > 2000 {
			d.Inline = editor.DiffInlineNone
			break
		}
	}
	return d, nil
}

// diffReviewContext 说出这批 diff 的来源，作为已读和笔记的生命周期键。
func diffReviewContext(t *repoTab, from, to [20]byte) string {
	if t.worktree {
		return "worktree"
	}
	return hashHex(from) + ":" + hashHex(to)
}

// fileContentStamp 是两侧内容的指纹。判定失效不能看 status：M 还是 M，
// 内容可能已经变了。
func fileContentStamp(s *fileDiffSource) string {
	if s == nil || (len(s.old) == 0 && len(s.new) == 0) {
		return ""
	}
	sum := sha256.New()
	sum.Write(s.old)
	sum.Write([]byte{'\n'})
	sum.Write(s.new)
	return hex.EncodeToString(sum.Sum(nil))
}

// applyFileStamp 记下这一行内容现在的指纹，内容变了就把已读作废。
func (t *repoTab) applyFileStamp(key, stamp string) {
	if stamp == "" {
		return
	}
	if t.reviewed == nil {
		t.reviewed = map[string]fileReview{}
	}
	rec := t.reviewed[key]
	if rec.stamp == stamp {
		return
	}
	if rec.stamp != "" {
		rec.viewed = false
	}
	rec.stamp = stamp
	t.reviewed[key] = rec
}

// fileViewed 说这一行是否已标记为已读。
func (t *repoTab) fileViewed(key string) bool { return t.reviewed[key].viewed }

// toggleFileReviewed 在已读和未读之间切换一个文件。
func (t *repoTab) toggleFileReviewed(key string) {
	if t.reviewed == nil {
		t.reviewed = map[string]fileReview{}
	}
	rec := t.reviewed[key]
	rec.viewed = !rec.viewed
	t.reviewed[key] = rec
}

// reviewedCount 数这批行里已读的有几个、共几个。
func (t *repoTab) reviewedCount() (viewed, total int) {
	for _, f := range t.compareFiles {
		total++
		if t.fileViewed(f.rowKey()) {
			viewed++
		}
	}
	return viewed, total
}

// currentFileRowKey 是详情面板正在看的那一行的身份。
func (t *repoTab) currentFileRowKey() string {
	if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		return t.compareFiles[t.fileSel].rowKey()
	}
	return ""
}

// currentFilePath 是详情面板正在看的文件。
func (t *repoTab) currentFilePath() string {
	if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		return t.compareFiles[t.fileSel].path
	}
	return ""
}

// notesFor 是这一行上的笔记。键是行的身份，同名文件的两侧各算各的。
func (t *repoTab) notesFor(key string) []diffNote {
	if key == "" {
		return nil
	}
	var out []diffNote
	for _, n := range t.diffNotes {
		if n.noteKey() == key {
			out = append(out, n)
		}
	}
	return out
}

// copyDiffNotes 把这次对比里的笔记写成 Markdown 复制到剪贴板。
func (a *app) copyDiffNotes(c *ui.Context, t *repoTab) {
	if len(t.diffNotes) == 0 {
		return
	}
	seen := map[string]bool{}
	keys := make([]string, 0, len(t.diffNotes))
	pathOf := map[string]string{}
	for _, n := range t.diffNotes {
		key := n.noteKey()
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
			pathOf[key] = n.noteLabel()
		}
	}
	sort.Strings(keys)
	current := t.currentFileRowKey()
	var b strings.Builder
	for _, key := range keys {
		var d *editor.DiffState
		if key == current {
			d = t.diff // 只有当前装载的文件拿得到代码上下文。
		}
		b.WriteString(renderNotesMarkdown(d, t.notesFor(key), pathOf[key]))
	}
	c.Services().WriteClipboard(b.String())
	c.Toast(tr("已复制审阅笔记"))
}

// noteContextLines 是导出时每条笔记带的上下文行数。
const noteContextLines = 3

// renderNotesMarkdown 把笔记写成 Markdown：路径、行号、前后各 3 行代码和正文。
// d 是这条路径当前装载的 diff，为 nil 时只有路径、行号和正文。
func renderNotesMarkdown(d *editor.DiffState, notes []diffNote, path string) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n", path)
	for _, n := range notes {
		// 导出的是文档，不是界面文案：这里不套 tr。
		side := "新"
		if n.Side == "old" {
			side = "旧"
		}
		fmt.Fprintf(&b, "\n### L%d (%s)\n", n.Line, side)
		if snippet := noteSnippet(d, n); snippet != "" {
			fmt.Fprintf(&b, "\n```diff\n%s```\n", snippet)
		}
		b.WriteString("\n")
		for _, line := range strings.Split(n.Text, "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}
	return b.String()
}

// noteSnippet 取笔记行前后各 3 行，改动的行照 diff 的样子打上 + 或 -。
func noteSnippet(d *editor.DiffState, n diffNote) string {
	if d == nil {
		return ""
	}
	buf := d.New
	if n.Side == "old" {
		buf = d.Old
	}
	if buf == nil || buf.Len() == 0 {
		return ""
	}
	start := max(n.Line-noteContextLines, 1)
	end := min(n.Line+noteContextLines, buf.LineCount())
	if start > end {
		return ""
	}
	var b strings.Builder
	for line := start; line <= end; line++ {
		mark := " "
		if noteLineChanged(d, n.Side, line) {
			mark = "+"
			if n.Side == "old" {
				mark = "-"
			}
		}
		b.WriteString(mark + buf.Line(line-1) + "\n")
	}
	return b.String()
}

// noteLineChanged 说这一行是否在改动块里：行号从 1 起，diff 的从 0 起。
func noteLineChanged(d *editor.DiffState, side string, line int) bool {
	i := line - 1
	for _, h := range d.Hunks() {
		if side == "old" {
			if i >= h.OldStart && i < h.OldEnd {
				return true
			}
		} else if i >= h.NewStart && i < h.NewEnd {
			return true
		}
	}
	return false
}
