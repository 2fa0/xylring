package main

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *app) applyWorktree(t *repoTab, files []fileChange) {
	key := worktreeKey(files)
	count := len(files)
	same := t.worktreeKey == key
	t.worktreeKey = key
	t.worktreeCount = count
	if !t.worktree {
		return
	}
	if count == 0 {
		a.closeWorktree(t)
		t.notice = "工作区已没有未提交的改动"
		return
	}
	if same && !t.compareLoading && t.compareErr == "" {
		return
	}
	a.fillWorktree(t, files)
}

func (a *app) showWorktree(t *repoTab) {
	if t.query.term != "" || t.query.filePath != "" {
		return
	}
	t.worktree = true
	t.comparison = nil
	t.inspecting = zeroHash
	t.selected = -1
	t.selection.Clear()
	a.clearDetails(t)
	if t.detailSplit == 0 {
		t.detailSplit = 52
	}
	if len(t.worktreeFiles) > 0 {
		a.fillWorktree(t, t.worktreeFiles)
	} else {
		t.compareLoading = true
	}
	a.loadWorktreeStatus(t, true)
}

func (a *app) fillWorktree(t *repoTab, files []fileChange) {
	t.worktreeFiles = files
	t.worktreeCount = len(files)
	t.worktreeKey = worktreeKey(files)
	a.startDiff(t, t.head, zeroHash, func(context.Context, string) ([]fileChange, error) {
		return files, nil
	})
}

func (a *app) loadWorktreeStatus(t *repoTab, open bool) {
	gen, path := t.generation, t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		files, err := worktreeStatus(ctx, path)
		a.post(func() {
			if t.closed || t.generation != gen {
				return
			}
			if err != nil {
				if open {
					t.compareErr = err.Error()
					t.compareLoading = false
				}
				return
			}
			if open {
				t.worktree = true
			}
			a.applyWorktree(t, files)
		})
	}()
}

func (a *app) closeWorktree(t *repoTab) {
	if !t.worktree {
		return
	}
	t.worktree = false
	if t.comparison != nil || t.inspecting != zeroHash {
		return
	}
	a.clearDiff(t)
}

func (a *app) worktreeBar(c *ui.Context, t *repoTab) {
	if t.query.term != "" || t.query.filePath != "" {
		return
	}
	if t.worktreeCount == 0 && !t.worktree {
		return
	}
	label := "工作区"
	switch t.worktreeCount {
	case 0:
		label = "工作区已干净"
	case 1:
		label = "工作区 · 1 个文件"
	default:
		label = fmt.Sprintf("工作区 · %d 个文件", t.worktreeCount)
	}
	ui.Row(c).Padding(0, 12, 8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		if t.worktree {
			ui.Text(c, label).Bold().SingleLine().Grow(1).MinWidth(0)
			if ui.Button(c.Key("worktree-close"), "返回").Clicked() {
				a.closeWorktree(t)
			}
			return
		}
		if ui.Button(c.Key("worktree-open"), label).Clicked() {
			a.showWorktree(t)
		}
	})
}
