package main

import (
	"context"
	"errors"
	"fmt"
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
		if !isHash {
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

func (a *app) openCompareModal(t *repoTab) {
	a.showCompare, a.compareErr = true, ""
	if from, to, ok := selectedPair(t); ok {
		a.compareFrom, a.compareTo = t.refLabel(from), t.refLabel(to)
		return
	}
	a.compareFrom = "HEAD"
	a.compareTo = ""
	if item := t.rowData(t.selected); item != nil {
		a.compareTo = hashHex(item.hash)
	}
}

func (a *app) comparisonModal(c *ui.Context) {
	wasOpen := a.showCompare
	ui.Modal(c.Key("compare-modal"), &a.showCompare, func() {
		ui.Column(c).Width(420).Gap(8).Children(func() {
			var input ui.Element
			ui.Field(c, "从", func() {
				ui.TextInput(c.Key("compare-from"), &a.compareFrom).Placeholder("分支、标签或哈希")
			})
			ui.Field(c, "到", func() {
				input = ui.TextInput(c.Key("compare-to"), &a.compareTo).Placeholder("main..feature")
			})
			ui.Checkbox(c.Key("compare-three"), &a.compareThree, "从分叉点起")
			if a.compareErr != "" {
				ui.Text(c, a.compareErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("compare-cancel"), "取消").Clicked() {
					a.showCompare = false
				}
				t := a.activeTab()
				busy := t == nil || t.compareLoading
				if ui.PrimaryButton(c.Key("compare-apply"), "开始比较").Disabled(busy).Clicked() || (input.Submitted() && !busy) {
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
		})
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
			if t.closed || t.compareGen != navGen || !a.showCompare {
				return
			}
			t.compareLoading = false
			if err != nil {
				a.compareErr = err.Error()
				return
			}
			a.showCompare = false
			a.showComparison(t, left, right, from, to, threeDot)
		})
	}()
}

func (a *app) showComparison(t *repoTab, from, to [20]byte, fromLabel, toLabel string, threeDot bool) {
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
		t.selected = toIdx
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
	if t.detailSplit == 0 {
		t.detailSplit = 52
	}
	hash := item.hash
	file := item.file
	parents := append([][20]byte(nil), item.parents...)
	var from [20]byte
	if len(parents) > 0 {
		from = parents[0]
	}
	a.startDiff(t, from, hash, func(ctx context.Context, path string) ([]fileChange, error) {
		if file != nil {
			return []fileChange{*file}, nil
		}
		return commitFiles(ctx, path, hash, parents)
	})
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
	t.diff, t.simpleDiff = nil, nil
	t.diffNote = ""
	t.diffLoading = false
	t.fileSel = -1
	t.fileList = ui.ListState{}
	if t.fileSplit == 0 {
		t.fileSplit = 28
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
	t.diff, t.simpleDiff = nil, nil
	t.diffNote = ""
	t.diffLoading = false
	t.fileSel = -1
	t.diffFrom, t.diffTo = zeroHash, zeroHash
}

func (a *app) loadFileDiff(t *repoTab, i int) {
	if i < 0 || i >= len(t.compareFiles) {
		t.diff, t.simpleDiff, t.diffNote, t.diffLoading = nil, nil, "", false
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
		if saved.from == t.diffFrom && saved.to == t.diffTo && saved.file == f.path && (saved.diff != nil || saved.note != "") {
			t.diff, t.simpleDiff, t.diffNote, t.diffLoading = saved.diff, saved.simpleDiff, saved.note, false
			return
		}
	}
	t.diffLoading, t.diff, t.simpleDiff, t.diffNote = true, nil, nil, ""
	fromRev, toRev := "", ""
	if t.diffFrom != zeroHash {
		fromRev = hashHex(t.diffFrom)
	}
	if t.diffTo != zeroHash {
		toRev = hashHex(t.diffTo)
	}
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
	path, worktree := t.path, t.worktree
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.compareCancel = cancel
	go func() {
		defer cancel()
		oldb, oldNote, err := readBlob(ctx, path, fromRev, oldPath)
		var newb []byte
		var newNote string
		if err == nil && worktree {
			newb, newNote, err = readWorktreeFile(path, newPath)
		} else if err == nil {
			newb, newNote, err = readBlob(ctx, path, toRev, newPath)
		}
		var diff *editor.DiffState
		var simple *simpleDiff
		if err == nil && oldNote == "" && newNote == "" && ctx.Err() == nil {
			diff, simple = prepareDiff(oldb, newb, f.path)
		}
		if err == nil {
			err = ctx.Err()
		}
		a.post(func() {
			if t.closed || t.compareGen != gen {
				return
			}
			t.diffLoading = false
			if err != nil {
				t.diffNote = err.Error()
				return
			}
			note := oldNote
			if newNote != "" {
				note = newNote
			}
			if note != "" {
				t.diffNote = note
				return
			}
			t.diff, t.simpleDiff = diff, simple
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
		if t.selection.Len() == 0 {
			return
		}
		a.closeComparison(t)
	}
	if t.selection.Len() == 1 && t.worktree {
		a.closeWorktree(t)
	}
	if t.worktree {
		return
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

func (a *app) comparisonHeader(c *ui.Context, t *repoTab) {
	theme := c.Theme()
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
	ui.Row(c).Padding(0, 12, 6, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, from).Font("monospace").SingleLine().Shrink(0)
		ui.Text(c, sep).TextColor(theme.TextMuted).Shrink(0)
		ui.Text(c, to).Font("monospace").SingleLine().Grow(1).MinWidth(0)
		if ui.Checkbox(c.Key("compare-three-live"), &t.comparison.threeDot, "从分叉点起").Changed() {
			a.loadComparisonDiff(t)
		}
		if ui.Button(c.Key("compare-close"), "返回").Clicked() {
			a.endComparison(t)
		}
	})
}

func (a *app) inspectPane(c *ui.Context, t *repoTab) {
	item := t.rowData(t.indexOf(t.inspecting))
	var detailsBtn ui.Element
	ui.Row(c).Padding(0, 12, 6, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		if item == nil {
			ui.Text(c, "提交").Bold().Shrink(0)
			return
		}
		text := item.subject
		if text == "" {
			text = "（无说明）"
		}
		ui.Text(c, text).Bold().SingleLine().Grow(1).MinWidth(0)
		if ui.Button(c.Key("copy-commit-hash"), "复制哈希").Clicked() {
			c.Services().WriteClipboard(hashHex(item.hash))
			c.Toast("已复制哈希")
		}
		label := "完整信息"
		if t.detailsOpen {
			label = "收起信息"
		}
		detailsBtn = ui.Button(c.Key("commit-details"), label)
		if detailsBtn.Clicked() {
			a.toggleDetails(t)
		}
	})
	if item != nil {
		a.detailsPopover(c, t, detailsBtn)
	}
	a.fileDiffPane(c, t)
}

func (a *app) fileDiffPane(c *ui.Context, t *repoTab) {
	theme := c.Theme()
	if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		f := t.compareFiles[t.fileSel]
		ui.Row(c).Padding(0, 12, 6, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Text(c, displayPath(f.path)).SingleLine().Grow(1).MinWidth(0).Tooltip(f.path)
			if ui.Button(c.Key("copy-file-path"), "复制路径").Clicked() {
				c.Services().WriteClipboard(f.path)
				c.Toast("已复制路径")
			}
			if t.query.filePath == "" && ui.Button(c.Key("file-history"), "文件历史").Clicked() {
				a.openFileHistory(t)
			}
		})
	}
	if t.compareLoading && len(t.compareFiles) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "正在读取改动…").TextColor(theme.TextMuted)
		})
		return
	}
	if t.compareErr != "" && len(t.compareFiles) == 0 {
		ui.Column(c).Grow(1).Center().Padding(24).Children(func() {
			ui.Text(c, t.compareErr).TextColor(theme.Danger)
		})
		return
	}
	ui.SplitPercent(c.Key(fmt.Sprintf("compare-split-%d", t.id)), &t.fileSplit, func() {
		t.fileList.Selected = &t.fileSel
		t.fileList.Key = func(i int) any {
			if i >= 0 && i < len(t.compareFiles) {
				return t.compareFiles[i].path
			}
			return i
		}
		list := ui.List(c.Key(fmt.Sprintf("compare-files-%d", t.id)), &t.fileList, len(t.compareFiles), func(i int) {
			ui.Text(c, t.compareFiles[i].label()).SingleLine().Font("monospace").Tooltip(t.compareFiles[i].fullPath())
		}).Grow(1).MinHeight(0)
		if list.Changed() {
			a.loadFileDiff(t, t.fileSel)
		}
		if len(t.compareFiles) == 0 && !t.compareLoading {
			ui.Text(c, "没有文件变化").Padding(12).TextColor(theme.TextMuted)
		}
	}, func() {
		switch {
		case t.diffLoading:
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "正在读取文件…").TextColor(theme.TextMuted)
			})
		case t.diffNote != "":
			ui.Column(c).Grow(1).Padding(16).Children(func() {
				ui.Text(c, t.diffNote).TextColor(theme.TextMuted)
			})
		case t.simpleDiff != nil:
			ui.Row(c).Grow(1).MinHeight(0).Children(func() {
				editor.View(c.Key("large-diff-old"), &t.simpleDiff.old).Grow(1).Basis(0).FillHeight().MinWidth(0)
				editor.View(c.Key("large-diff-new"), &t.simpleDiff.new).Grow(1).Basis(0).FillHeight().MinWidth(0)
			})
		case t.diff != nil:
			editor.DiffView(c.Key(fmt.Sprintf("compare-diff-%d", t.id)), t.diff).Grow(1).MinHeight(0)
		}
	}).Grow(1).MinHeight(0)
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
