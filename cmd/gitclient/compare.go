package main

import (
	"context"
	"errors"
	"fmt"
	"os"
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
				for i, f := range files {
					if f.path == t.restoreFile {
						t.fileSel = i
						break
					}
				}
				t.restoreFile = ""
			}
			t.fileList.ScrollIntoView(t.fileSel)
			t.resetFileTree()
			a.loadFileDiff(t, t.fileSel)
		})
	}()
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
	s := &fileDiffSource{}
	var err error
	s.old, s.note, err = readBlob(ctx, dir, fromRev, oldPath)
	if err != nil {
		return nil, err
	}
	var note string
	if worktree {
		s.new, note, err = readWorktreeFile(dir, newPath)
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
	if saved := t.restoreDiff; saved != nil {
		t.restoreDiff = nil
		if saved.from == t.diffFrom && saved.to == t.diffTo && saved.file == f.path && (saved.diff != nil || saved.imageDiff != nil || saved.note != "") {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		var diff *editor.DiffState
		var simple *simpleDiff
		var imgDiff *imageDiff
		var note string
		source, err := readFileDiffSource(ctx, path, fromRev, toRev, f, worktree)
		if err == nil {
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

func fileViewToggleWidget(c *ui.Context, key, label string, view *int) {
	theme := c.Theme()
	seg := ui.SegmentedBase(c.Key(key), view, 2)
	seg.Track.Shrink(0).AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Label(tr(label))
	seg.Track.Children(func() {
		icons := [2]*ui.SVG{iconList, iconListTree}
		names := [2]string{"列表", "树"}
		for i, ic := range icons {
			s := seg.Segment(i).Label(tr(names[i])).Tooltip(tr(names[i])).Padding(theme.Space(1), theme.Space(1.5)).Radius(max(theme.Radius-theme.Space(0.5), 0)).Center().Transition(colorMotion())
			if i == *view {
				bg := theme.Background
				if theme.Dark {
					bg = theme.SurfacePressed
				}
				s.Background(bg)
			}
			s.Children(func() { ui.Icon(c, ic) })
		}
	})
}

func (a *app) fileViewToggle(c *ui.Context) {
	fileViewToggleWidget(c, "file-view", "文件视图", &a.fileView)
}

func (a *app) inspectFilesPane(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).BorderWidth(0, 1, 0, 0).BorderColor(theme.Border).Children(func() {
		a.fileListHeader(c, t)
		if !t.worktree {
			a.commitSummaryCard(c, t)
		}
		a.fileFilterBar(c, t)
		if t.compareLoading && len(t.compareFiles) == 0 {
			busyBlock(c, "files-loading", tr("正在读取改动…"), "").Grow(1)
			return
		}
		if t.compareErr != "" && len(t.compareFiles) == 0 {
			ui.Column(c).Grow(1).Center().Padding(12).Children(func() {
				ui.Text(c, trMessage(t.compareErr)).TextColor(theme.Danger)
			})
			return
		}
		ui.Themed(c.Key("file-selection-theme"), listTheme(c)).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
			if a.fileView == 1 {
				a.fileTreeList(c, t)
			} else {
				a.fileFlatList(c, t)
			}
			if len(t.compareFiles) == 0 && !t.compareLoading {
				emptyHint(c, "files-empty", emptyHintProps{
					Icon:  iconGitCompare,
					Title: filesEmptyHint(t),
				}).Grow(1)
			}
		})
		if t.worktree {
			a.worktreeCommitCard(c, t)
		}
	})
}

// noMatchingFiles says the filter hid every file, and clears it on request.
func noMatchingFiles(c *ui.Context, filter *string) {
	emptyHint(c, "files-no-match", emptyHintProps{
		Icon:  iconSearch,
		Title: tr("无匹配文件"),
		Action: func() {
			if ui.Button(c.Key("files-clear-filter"), tr("清除筛选")).Clicked() {
				*filter = ""
			}
		},
	})
}

// filesEmptyHint is the one sentence an empty file list shows: a clean
// worktree, a commit that changed nothing, or two versions that match.
func filesEmptyHint(t *repoTab) string {
	switch {
	case t.worktree:
		return tr("工作区没有未提交的改动")
	case t.comparison != nil:
		return tr("这两个版本没有文件差异")
	default:
		return tr("这条提交没有改动任何文件")
	}
}

func (a *app) sidebarActiveInspect(c *ui.Context, t *repoTab) {
	a.inspectFilesPane(c, t)
}

func (a *app) fileDiffPane(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	if t.compareLoading && len(t.compareFiles) == 0 {
		busyBlock(c, "diff-loading", tr("正在读取改动…"), "").Grow(1)
		return
	}
	if t.compareErr != "" && len(t.compareFiles) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			ui.Text(c, trMessage(t.compareErr)).TextColor(theme.Danger)
		})
		return
	}
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		a.fileDiffHeader(c, t)
		a.fileDiffContent(c, t.diff, t.simpleDiff, t.imageDiff, t.diffNote, t.diffLoading, t.imageViewMode, t.imageFitActual)
	})
}

func (a *app) commitSummaryCard(c *ui.Context, t *repoTab) {
	if t.inspecting == zeroHash || t.comparison != nil || t.worktree {
		return
	}
	idx := t.indexOf(t.inspecting)
	if idx < 0 {
		return
	}
	item := &t.commits[idx]
	theme := c.Theme()
	ui.Column(c.Key(fmt.Sprintf("commit-summary-%d", t.id))).Padding(2, 8, 6, 8).Gap(4).Shrink(0).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Text(c, item.author).FontSize(12).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, "·").FontSize(12).TextColor(theme.TextMuted)
			ui.Text(c, item.whenText()).FontSize(12).TextColor(theme.TextMuted).SingleLine()
			ui.Text(c, "·").FontSize(12).TextColor(theme.TextMuted)
			ui.Text(c, shortHash(item.hash)).Font("monospace").FontSize(12).TextColor(theme.TextMuted).SingleLine()
			copyBtn := ui.ButtonBase(c.Key("copy-commit-hash")).Label(tr("复制哈希")).Tooltip(tr("复制哈希")).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Transition(colorMotion())
			switch {
			case copyBtn.Pressed():
				copyBtn.Background(theme.SurfacePressed)
			case copyBtn.Hovered():
				copyBtn.Background(theme.SurfaceHover)
			}
			copyBtn.Children(func() {
				ui.Icon(c, iconCopy).Size(12, 12).TextColor(theme.TextMuted)
			})
			if copyBtn.Clicked() {
				c.Services().WriteClipboard(hashHex(item.hash))
				c.Toast(tr("已复制哈希"))
			}
			label := "完整信息"
			if t.detailsOpen {
				label = "收起信息"
			}
			btn := ui.ButtonBase(c.Key("commit-details")).Label(tr(label)).Padding(1, 6).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Transition(colorMotion())
			if t.detailsOpen {
				btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
			} else {
				btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
			}
			btn.Children(func() {
				ui.Text(c, tr(label)).FontSize(11).SingleLine()
			})
			if btn.Clicked() {
				a.toggleDetails(t)
			}
			if len(item.parents) > 1 {
				for pi := range item.parents {
					pIdx := pi
					label := fmt.Sprintf(tr("父提交 %d"), pi+1)
					btn := ui.ButtonBase(c.Key(fmt.Sprintf("parent-%d", pi))).Padding(1, 6).Radius(theme.Radius).Cursor(ui.CursorPointer).
						Tooltip(tr("对比父提交 ") + shortHash(item.parents[pi])).Transition(colorMotion())
					if t.inspectParent == pi {
						btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
					} else {
						btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
					}
					btn.Children(func() {
						ui.Text(c, tr(label)).FontSize(11).SingleLine()
					})
					if btn.Clicked() && t.inspectParent != pIdx {
						a.startCommitDiff(t, item, pIdx)
					}
				}
			}
		})
		a.commitDetailsInline(c, t)
		if !t.detailsShown && t.details != nil && t.details.hash == hashHex(item.hash) {
			if body := t.details.body(); body != "" {
				ui.Scroll(c.Key(fmt.Sprintf("commit-body-%d", t.id))).MaxHeight(68).Padding(4, 6).Radius(theme.Radius).Background(theme.Surface).Children(func() {
					ui.Text(c, body).FontSize(12).TextColor(theme.TextMuted)
				})
			}
		}
	})
}

func (a *app) fileListHeader(c *ui.Context, t *repoTab) {
	ui.Row(c).FillWidth().Padding(4, 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		a.fileListTitle(c, t)
		if t.worktree && len(t.compareFiles) > 0 {
			paneIconButton(c, "inspect-stage-all", tr("全部暂存"), iconPlus).Tooltip(tr("全部暂存")).OnClick(func() {
				a.stageAllWorktree(t)
			})
			paneIconButton(c, "inspect-unstage-all", tr("全部取消暂存"), iconMinus).Tooltip(tr("全部取消暂存")).OnClick(func() {
				a.unstageAllWorktree(t)
			})
		}
		if t.comparison != nil {
			if iconButton(c, "compare-close", tr("返回"), iconBack).Shrink(0).Clicked() {
				a.endComparison(t)
			}
		}
		a.fileViewToggle(c)
		if a.fileView == 1 {
			if paneIconButton(c, "tree-expand-all", tr("全部展开"), iconExpandAll).Clicked() {
				t.expandAllTree()
			}
			if paneIconButton(c, "tree-collapse-all", tr("全部收起"), iconCollapseAll).Clicked() {
				t.collapseAllTree()
			}
		}
	})
}

func (a *app) fileFilterBar(c *ui.Context, t *repoTab) {
	if len(t.compareFiles) == 0 && !t.compareLoading {
		return
	}
	theme := c.Theme()
	ui.Row(c.Key(fmt.Sprintf("file-filter-bar-%d", t.id))).FillWidth().Padding(2, 8, 4, 8).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.TextInput(c.Key(fmt.Sprintf("filter-files-input-%d", t.id)), &t.fileFilter).
			Label(tr("过滤文件")).
			Placeholder(tr("过滤文件")).
			Bind(&t.fileFilterInput).
			Grow(1).MinWidth(60)
		if t.fileFilter != "" {
			indices := t.filteredFileIndices()
			ui.Text(c, fmt.Sprintf("%d/%d", len(indices), len(t.compareFiles))).
				FontSize(11).TextColor(theme.TextMuted).Shrink(0)
			clearBtn := ui.ButtonBase(c.Key(fmt.Sprintf("clear-file-filter-%d", t.id))).Label(tr("清除过滤")).Tooltip(tr("清除过滤")).Padding(2).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Transition(colorMotion())
			clearBtn.Children(func() {
				ui.Icon(c, iconClose).Size(12, 12).TextColor(theme.TextMuted)
			})
			if clearBtn.Clicked() {
				t.fileFilter = ""
			}
		}
	})
}

func (a *app) fileDiffHeader(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	maxLabel := tr("放大阅读")
	maxIcon := iconMaximize
	if t.diffMaximized {
		maxLabel = tr("还原布局")
		maxIcon = iconMinimize
	}
	ui.Row(c).FillWidth().Padding(4, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		if t.comparison != nil {
			if ui.Checkbox(c.Key("compare-three-live"), &t.comparison.threeDot, tr("从分叉点起")).Changed() {
				a.loadComparisonDiff(t)
			}
		}
		if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			f := t.compareFiles[t.fileSel]
			dir := filepath.Dir(f.path)
			base := filepath.Base(f.path)
			if f.oldPath != "" && f.oldPath != f.path {
				base = displayPath(f.oldPath) + " → " + displayPath(f.path)
			}
			ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
				fileStatusMark(c, f)
				ui.Text(c, base).Bold().FontSize(12).SingleLine().Shrink(0)
				if dir != "" && dir != "." {
					ui.Text(c, dir).FontSize(11).TextColor(theme.TextMuted).SingleLine().Shrink(1).MinWidth(0)
				}
			})
		} else if t.worktree {
			ui.Text(c, tr("选择文件")).Bold().FontSize(12).TextColor(theme.TextMuted).SingleLine().Shrink(0)
		}
		ui.Box(c).Grow(1)
		if t.imageDiff != nil {
			a.imageDiffControls(c, t.id, t.imageDiff, &t.imageViewMode, &t.imageFitActual)
		} else {
			a.diffLayoutToggle(c)
			a.diffIgnoreWhitespaceToggle(c, t)
			a.diffWrapToggle(c)
			if t.diff != nil || t.simpleDiff != nil {
				findTip := "在 Diff 中查找 (⌘F)"
				if runtime.GOOS != "darwin" {
					findTip = "在 Diff 中查找 (Ctrl+F)"
				}
				if paneIconButton(c, "find-in-diff", tr("在 Diff 中查找"), iconSearch).Tooltip(tr(findTip)).Clicked() {
					a.openDiffFind(t)
				}
			}
		}
		fullWidthTip := tr("全宽展示 (展开至整窗宽度)")
		if a.bottomFullWidth {
			fullWidthTip = tr("侧栏旁展示 (还原底栏宽度)")
		}
		if paneToggleIconButton(c, "toggle-bottom-width", tr("切换底栏全宽"), fullWidthTip, iconPanelBottom, a.bottomFullWidth).Clicked() {
			a.toggleBottomFullWidth()
		}
		if paneIconButton(c, "toggle-diff-max", maxLabel, maxIcon).Clicked() {
			t.diffMaximized = !t.diffMaximized
		}
		if closeIconButton(c, "close-bottom-pane", tr("关闭详情")).Shrink(0).Clicked() {
			a.closeBottomPane(t)
		}
	})
}

func (a *app) currentDiffLayout() editor.DiffLayout {
	if a.diffLayout == 1 {
		return editor.DiffUnified
	}
	return editor.DiffSplit
}

// prepareDiffState 给每个 diff 装上显示选项和按键，两条渲染路径共用。
func (a *app) prepareDiffState(d *editor.DiffState) {
	d.Wrap = a.diffWrap
	d.Layout = a.currentDiffLayout()
	d.Bindings = append(editor.DefaultBindings(),
		editor.Binding{Key: ui.KeyJ, Command: editor.NextHunk},
		editor.Binding{Key: ui.KeyK, Command: editor.PreviousHunk},
	)
}

func (a *app) openDiffFind(t *repoTab) {
	if t == nil {
		return
	}
	if t.contentTab > 0 && t.contentTab <= len(t.fileTabs) {
		a.openFileTabFind(t.fileTabs[t.contentTab-1])
		return
	}
	if t.diff != nil {
		t.diff.Run(editor.Find)
	} else if t.simpleDiff != nil {
		t.simpleDiff.new.Run(editor.Find)
	}
}

func (a *app) diffLayoutToggle(c *ui.Context) {
	theme := c.Theme()
	seg := ui.SegmentedBase(c.Key("diff-layout-toggle"), &a.diffLayout, 2)
	seg.Track.Height(24).Shrink(0).AlignItems(ui.Stretch).Padding(theme.Space(0.5)).Gap(theme.Space(0.5)).Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Label(tr("差异排版"))
	seg.Track.Children(func() {
		icons := [2]*ui.SVG{iconColumns2, iconRows2}
		names := [2]string{"并排", "内联"}
		tips := [2]string{"左右分栏 (Split)", "内联展示 (Unified)"}
		for i := 0; i < 2; i++ {
			s := seg.Segment(i).Label(tr(names[i])).Tooltip(tr(tips[i])).Padding(0, 6).Radius(max(theme.Radius-theme.Space(0.5), 0)).Center().Transition(colorMotion())
			if i == a.diffLayout {
				bg := theme.Background
				if theme.Dark {
					bg = theme.SurfacePressed
				}
				s.Background(bg)
			}
			icColor := theme.TextMuted
			if i == a.diffLayout {
				icColor = theme.Text
			}
			s.Children(func() {
				ui.Icon(c, icons[i]).Size(14, 14).TextColor(icColor)
			})
		}
	})
}

func (a *app) diffIgnoreWhitespaceToggle(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	btn := ui.ButtonBase(c.Key("diff-ignore-whitespace")).Height(24).Label(tr("忽略空白")).Tooltip(tr("忽略空白字符改动")).Padding(0, 8).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	if a.diffIgnoreWhitespace {
		btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
	} else {
		btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
	}
	btn.Children(func() {
		ui.Text(c, tr("忽略空白")).FontSize(11).SingleLine()
	})
	if btn.Clicked() {
		a.diffIgnoreWhitespace = !a.diffIgnoreWhitespace
		if t != nil && t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			a.loadFileDiff(t, t.fileSel)
		}
	}
}

func (a *app) diffWrapToggle(c *ui.Context) {
	theme := c.Theme()
	btn := ui.ButtonBase(c.Key("diff-wrap-toggle")).Height(24).Label(tr("折行")).Tooltip(tr("折行显示长行")).Padding(0, 8).Radius(theme.Radius).Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	if a.diffWrap {
		btn.Background(theme.Accent.Alpha(0.16)).TextColor(theme.Accent).Border(1, theme.Accent.Alpha(0.4))
	} else {
		btn.Background(theme.Surface).TextColor(theme.TextMuted).Border(1, theme.Border)
	}
	btn.Children(func() {
		ui.Text(c, tr("折行")).FontSize(11).SingleLine()
	})
	if btn.Clicked() {
		a.diffWrap = !a.diffWrap
	}
}

func (a *app) formatCompareRef(t *repoTab, hash [20]byte, label string) string {
	if hash == zeroHash {
		if label != "" {
			return label
		}
		return "HEAD"
	}
	short := shortHash(hash)
	refName := label
	if refName == "" || refName == short || refName == hashHex(hash) {
		if t != nil && len(t.refs[hash]) > 0 {
			refName = t.refs[hash][0].name
		} else {
			refName = ""
		}
	}
	if refName != "" && refName != short && refName != hashHex(hash) {
		return fmt.Sprintf("%s (%s)", short, refName)
	}
	return short
}

func (a *app) comparisonBanner(c *ui.Context, tab *repoTab) {
	if tab == nil || tab.comparison == nil {
		return
	}
	t := c.Theme()
	cmp := tab.comparison
	fromText := a.formatCompareRef(tab, cmp.from, cmp.fromLabel)
	toText := a.formatCompareRef(tab, cmp.to, cmp.toLabel)

	ui.Row(c.Key("comparison-banner")).FillWidth().Padding(6, 12).Gap(10).AlignItems(ui.Center).Shrink(0).Background(t.Accent.Alpha(0.08)).Border(1, t.Accent.Alpha(0.2)).Transition(fadeEnter()).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
			ui.Text(c, tr("对比模式")).Bold().FontSize(12).TextColor(t.Accent).Shrink(0)
			ui.Text(c, "·").TextColor(t.TextMuted).Shrink(0)
			ui.Text(c, fromText).Font("monospace").FontSize(12).Bold().TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
			ui.Text(c, "⇄").FontSize(13).Bold().TextColor(t.Accent).Shrink(0)
			ui.Text(c, toText).Font("monospace").FontSize(12).Bold().TextColor(t.Text).SingleLine().Shrink(1).MinWidth(0)
			if cmp.threeDot {
				ui.Text(c, tr("(三点)")).FontSize(11).TextColor(t.TextMuted).Shrink(0)
			}
		})

		ui.Box(c).Grow(1)

		ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
			if ui.Button(c.Key("compare-banner-exit"), tr("退出对比")).Label(tr("退出对比")).Shrink(0).Clicked() {
				a.endComparison(tab)
			}
			kbdHint(c, "compare-banner-esc", "Esc")
		})
	})
}

func (a *app) fileListTitle(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	switch {
	case t.worktree:
		ui.Text(c, tr("未提交改动")).Bold().SingleLine().Shrink(0)
	case t.comparison != nil:
		from, to := t.comparison.fromLabel, t.comparison.toLabel
		if from == "" {
			from = shortHash(t.comparison.from)
		}
		if to == "" {
			to = shortHash(t.comparison.to)
		}
		sep := "→"
		if t.comparison.threeDot {
			sep = "..."
		}
		ui.Text(c, from).Font("monospace").SingleLine().Shrink(0)
		ui.Text(c, sep).TextColor(theme.TextMuted).Shrink(0)
		ui.Text(c, to).Font("monospace").SingleLine().Grow(1).MinWidth(0)
	default:
		text := tr("提交")
		idx := t.indexOf(t.inspecting)
		row := t.rowIndex(t.inspecting)
		if idx >= 0 {
			text = t.commits[idx].subject
			if text == "" {
				text = tr("（无说明）")
			}
		}
		title := ui.Text(c, text).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(text)
		if row >= 0 {
			title.ContextMenu(func(m *ui.Menu) {
				a.commitRowMenu(c, m, t, row)
			})
		}
	}
}

func fileIconButton(c *ui.Context, key, name string, icon *ui.SVG) ui.Element {
	theme := c.Theme()
	btn := ui.ButtonBase(c.Key(key)).Label(tr(name)).Tooltip(tr(name)).Padding(4).Radius(theme.Radius).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case btn.Pressed():
		btn.Background(theme.SurfacePressed)
	case btn.Hovered():
		btn.Background(theme.Text.Alpha(0.08))
	}
	btn.Children(func() {
		ui.Icon(c, icon)
	})
	return btn
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
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func copyFilePath(c *ui.Context, repo, rel string, mods ui.Modifiers) {
	abs := mods&ui.Alt != 0
	c.Services().WriteClipboard(repoFilePath(repo, rel, abs))
	if abs {
		c.Toast(tr("已复制绝对路径"))
	} else {
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
		if t.worktree {
			a.worktreeStageHoverButtons(c, t, f)
		}
		copy := fileIconButton(c, "copy-file-path-"+f.path, "复制路径", iconCopy).Tooltip(copyPathTip(c))
		if copy.Clicked() {
			copyFilePath(c, t.path, f.path, copy.ClickModifiers())
		}
		open := fileIconButton(c, "open-file-tab-"+f.path, "在新标签页打开", iconOpenTab).Disabled(t.compareLoading || t.diffLoading)
		if open.Clicked() {
			a.openFileTabAt(t, i)
		}
		if t.query.filePath == "" {
			fileIconButton(c, "file-history-"+f.path, "文件历史", iconHistory).OnClick(func() {
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

func (a *app) fileFlatList(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	scrolling := takeScrollMove(&t.fileListScroll, &t.fileListScrollY)
	indices := t.filteredFileIndices()
	if len(indices) == 0 && len(t.compareFiles) > 0 {
		noMatchingFiles(c, &t.fileFilter)
		return
	}
	currentSel := -1
	for row, origIdx := range indices {
		if origIdx == t.fileSel {
			currentSel = row
			break
		}
	}
	selectedRow := currentSel
	var action func()
	t.fileList.Selected = &selectedRow
	t.fileList.Key = func(i int) any {
		if i >= 0 && i < len(indices) && indices[i] >= 0 && indices[i] < len(t.compareFiles) {
			return t.compareFiles[indices[i]].path
		}
		return i
	}
	t.fileList.Label = func(i int) string {
		if i >= 0 && i < len(indices) && indices[i] >= 0 && indices[i] < len(t.compareFiles) {
			return t.compareFiles[indices[i]].label()
		}
		return ""
	}
	list := ui.List(c.Key(fmt.Sprintf("compare-files-%d", t.id)), &t.fileList, len(indices), func(i int) {
		if i < 0 || i >= len(indices) {
			return
		}
		origIdx := indices[i]
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
		row.Tooltip(filePathTip(path, scrolling || t.fileRowMoving(c, row, "list:"+path, hovered)))
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
				ui.Text(c, filepath.Base(f.path)).FontSize(13).SingleLine().Grow(1).MinWidth(0)
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
	if list.Changed() && selectedRow >= 0 && selectedRow < len(indices) {
		t.fileSel = indices[selectedRow]
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
		if n.dir || n.file < 0 || n.file >= len(t.compareFiles) {
			return n.name
		}
		return t.compareFiles[n.file].label()
	}
	var action func()
	outline := ui.Outline(c.Key(fmt.Sprintf("compare-tree-%d", t.id)), &t.fileOutline, tree.roots, tree.children, func(key string) {
		n := tree.nodes[key]
		tip := key
		if !n.dir && n.file >= 0 && n.file < len(t.compareFiles) {
			tip = t.compareFiles[n.file].fullPath()
		}
		row := ui.Row(c).Gap(5).MinWidth(0).Grow(1).MinHeight(20).Padding(1, 0).AlignItems(ui.Center)
		hovered := row.Hovered()
		row.Tooltip(filePathTip(tip, scrolling || t.fileRowMoving(c, row, "tree:"+tip, hovered)))
		if !n.dir && n.file >= 0 && n.file < len(t.compareFiles) {
			idx := n.file
			if row.RowDoubleClicked() {
				a.openFileTabAt(t, idx)
			}
			row.ContextMenu(func(m *ui.Menu) {
				action = a.fileRowMenu(c, m, t, idx)
			})
		}
		row.Children(func() {
			if t.worktree && !n.dir && n.file >= 0 && n.file < len(t.compareFiles) {
				a.worktreeStageCheckbox(c, t, t.compareFiles[n.file], "wt-stage-tree")
			}
			ui.Text(c, n.name).FontSize(13).SingleLine().Grow(1).MinWidth(0)
			if !n.dir && n.file >= 0 && n.file < len(t.compareFiles) {
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
		for _, action := range []string{"Blame", "行历史", "恢复历史文件"} {
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
	if m.Item(tr("复制绝对路径")).Chosen() {
		copyFilePath(c, t.path, f.path, ui.Alt)
	}
	m.Separator()
	fullPath := repoFilePath(t.path, f.path, true)
	if m.Item(tr("使用默认编辑器打开")).Chosen() {
		if _, err := os.Stat(fullPath); err != nil {
			c.Toast(tr("工作区中不存在此文件"))
		} else if err := openWithDefaultEditor(fullPath); err != nil {
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
