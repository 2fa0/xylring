package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type worktreeRemoveCheck struct {
	checking bool
	dirty    bool
	ignored  bool
	unmerged bool
	locked   string
	err      string
}

type worktreeDialog struct {
	parentRepo string
	wt         repoWorktree
	check      worktreeRemoveCheck
	force      bool
	busy       bool
	err        string
	gen        int
}

func isTempWorktreePath(path string) bool {
	if path == "" || isDockerRepoPath(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if strings.HasPrefix(clean, "/tmp/") || strings.HasPrefix(clean, "/private/tmp/") {
		return true
	}
	if tmp := filepath.ToSlash(filepath.Clean(os.TempDir())); tmp != "" && tmp != "/" {
		if !strings.HasSuffix(tmp, "/") {
			tmp += "/"
		}
		if strings.HasPrefix(clean, tmp) {
			return true
		}
	}
	return false
}

func pruneWorktrees(ctx context.Context, parentDir string) error {
	_, err := runGit(ctx, parentDir, "worktree", "prune")
	return err
}

func removeWorktree(ctx context.Context, parentDir, wtPath string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	_, err := runGit(ctx, parentDir, args...)
	return err
}

func inspectWorktreeRemoval(ctx context.Context, parentDir string, wt repoWorktree) worktreeRemoveCheck {
	check := worktreeRemoveCheck{locked: wt.locked}
	if wt.prunable != "" {
		return check
	}
	out, err := runGit(ctx, wt.path, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--ignored=matching")
	if err != nil {
		check.err = trimMsg(err.Error())
	} else {
		for _, entry := range strings.Split(string(out), "\x00") {
			if entry == "" {
				continue
			}
			if strings.HasPrefix(entry, "!! ") {
				check.ignored = true
			} else {
				check.dirty = true
			}
		}
	}
	if wt.head != zeroHash {
		if _, err := runGit(ctx, parentDir, "merge-base", "--is-ancestor", hashHex(wt.head), "HEAD"); err != nil {
			check.unmerged = true
		}
	}
	return check
}

func (a *app) finishWorktreeCleanup(parentRepo, removedPath, toastMsg string) {
	if removedPath != "" {
		if idx := a.tabIndex(removedPath); idx >= 0 {
			a.closeTab(idx)
		}
		if slices.Contains(a.recent, removedPath) {
			a.setRecent(slices.DeleteFunc(slices.Clone(a.recent), func(p string) bool { return p == removedPath }))
		}
		a.found = slices.DeleteFunc(a.found, func(p string) bool { return p == removedPath })
		if a.bannerPath == removedPath {
			a.setBanner("", "")
		}
	}
	a.summaryMu.Lock()
	delete(a.repoSummaries, parentRepo)
	if removedPath != "" {
		delete(a.repoSummaries, removedPath)
	}
	a.summaryMu.Unlock()
	if tab := a.tabByPath(parentRepo); tab != nil {
		a.reload(tab)
	} else {
		a.refreshRepoSummary(parentRepo)
	}
	if toastMsg != "" {
		a.pendingToast = toastMsg
	}
}

func (a *app) runPruneWorktrees(parentRepo, wtPath string) {
	if parentRepo == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := pruneWorktrees(ctx, parentRepo)
		a.post(func() {
			if a.closed {
				return
			}
			if err != nil {
				a.setBanner(tr("清理工作树失败：")+trimMsg(err.Error()), parentRepo)
				return
			}
			a.finishWorktreeCleanup(parentRepo, wtPath, tr("已清理失效工作树记录"))
		})
	}()
}

func (a *app) openWorktreeRemoveModal(parentRepo string, wt repoWorktree) {
	a.wtRemove.gen++
	gen := a.wtRemove.gen
	a.wtRemove = worktreeDialog{
		parentRepo: parentRepo,
		wt:         wt,
		check:      worktreeRemoveCheck{checking: wt.prunable == "", locked: wt.locked},
		gen:        gen,
	}
	a.showWorktreeRemove = true
	if wt.prunable != "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		check := inspectWorktreeRemoval(ctx, parentRepo, wt)
		a.post(func() {
			if a.closed || !a.showWorktreeRemove || a.wtRemove.gen != gen {
				return
			}
			a.wtRemove.check = check
		})
	}()
}

func (a *app) runRemoveWorktree() {
	d := a.wtRemove
	if d.busy || d.parentRepo == "" || d.wt.path == "" {
		return
	}
	if d.wt.prunable != "" {
		a.showWorktreeRemove = false
		a.runPruneWorktrees(d.parentRepo, d.wt.path)
		return
	}
	a.wtRemove.busy = true
	a.wtRemove.err = ""
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := removeWorktree(ctx, d.parentRepo, d.wt.path, d.force)
		a.post(func() {
			if a.closed || a.wtRemove.gen != gen {
				return
			}
			a.wtRemove.busy = false
			if err != nil {
				a.wtRemove.err = trimMsg(err.Error())
				return
			}
			a.showWorktreeRemove = false
			a.finishWorktreeCleanup(d.parentRepo, d.wt.path, tr("已移除工作树"))
		})
	}()
}

func (a *app) worktreeRemoveModal(c *ui.Context) {
	if !a.showWorktreeRemove {
		return
	}
	theme := c.Theme()
	d := &a.wtRemove
	wt := d.wt
	title := tr("移除工作树")
	confirmLabel := tr("移除工作树")
	if wt.prunable != "" {
		title = tr("清理失效工作树")
		confirmLabel = tr("清理记录")
	}
	hasRisk := d.check.dirty || d.check.ignored || d.check.unmerged
	canConfirm := !d.busy && !d.check.checking && wt.locked == "" && (wt.prunable != "" || !hasRisk || d.force)

	appModal(c.Key("worktree-remove-modal"), &a.showWorktreeRemove, title, 440, false, func() {
		ui.Text(c, worktreeRowLabel(wt)).FontSize(13).Bold().TextColor(theme.Text)
		ui.Text(c, wt.path).FontSize(12).Font("monospace").TextColor(theme.TextMuted)
		if wt.prunable != "" {
			ui.Text(c, tr("该工作树目录已不存在，清理仅移除 Git 中的失效记录。")).FontSize(12).TextColor(theme.TextMuted)
		} else if d.check.checking {
			ui.Text(c, tr("正在检查工作树状态…")).FontSize(12).TextColor(theme.TextMuted)
		} else if wt.locked != "" {
			msg := tr("该工作树已锁定，请先解锁后再移除。")
			if wt.locked != worktreeMarkLocked {
				msg += " (" + wt.locked + ")"
			}
			ui.Text(c, msg).FontSize(12).TextColor(theme.Danger)
		} else if hasRisk {
			if d.check.dirty {
				ui.Text(c, tr("存在未提交或未跟踪的改动，强制移除将丢失这些改动。")).FontSize(12).TextColor(theme.Danger)
			}
			if d.check.ignored {
				ui.Text(c, tr("存在被忽略的文件，移除工作树会一并删除它们。")).FontSize(12).TextColor(theme.Warning)
			}
			if d.check.unmerged {
				ui.Text(c, tr("该工作树的提交尚未合并到当前主分支。")).FontSize(12).TextColor(theme.Warning)
			}
			ui.Checkbox(c.Key("wt-force-remove"), &d.force, tr("强制移除（忽略上述风险并删除目录）"))
		} else {
			ui.Text(c, tr("工作区干净且提交已合并，移除后将删除该工作树目录。")).FontSize(12).TextColor(theme.TextMuted)
		}
		if d.check.err != "" {
			ui.Text(c, d.check.err).FontSize(12).TextColor(theme.Danger)
		}
		if d.err != "" {
			ui.Text(c, d.err).FontSize(12).TextColor(theme.Danger)
		}
	}, func() {
		if ui.Button(c.Key("wt-remove-cancel"), tr("取消")).Disabled(d.busy).Clicked() {
			a.showWorktreeRemove = false
		}
		btn := ui.Button(c.Key("wt-remove-confirm"), confirmLabel).Padding(2, 12).Disabled(!canConfirm)
		if btn.Clicked() {
			a.runRemoveWorktree()
		}
	})
}
