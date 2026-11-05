package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

var worktreeHash = [20]byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
}

func (t *repoTab) hasWorktreeRow() bool {
	return t.worktreeCount > 0 && t.query.term == "" && t.query.filePath == ""
}

func (t *repoTab) worktreeOffset() int {
	if t.hasWorktreeRow() {
		return 1
	}
	return 0
}

func (t *repoTab) rowIndex(h [20]byte) int {
	if h == worktreeHash {
		if t.hasWorktreeRow() {
			return 0
		}
		return -1
	}
	i := t.indexOf(h)
	if i < 0 {
		return -1
	}
	return i + t.worktreeOffset()
}

func (t *repoTab) worktreeHead() *commit {
	if t.head == zeroHash {
		return nil
	}
	i := t.indexOf(t.head)
	if i < 0 {
		return nil
	}
	return &t.commits[i]
}

func (t *repoTab) worktreeLane() (col, color int, connected bool) {
	if h := t.worktreeHead(); h != nil {
		return h.nodeCol, h.nodeColor, true
	}
	return 0, 0, false
}

func (t *repoTab) worktreeConnectsFromTop(row int) bool {
	return t.hasWorktreeRow() && t.rowIndex(t.head) == row
}

func (t *repoTab) worktreeThrough(row int) bool {
	headRow := t.rowIndex(t.head)
	return t.hasWorktreeRow() && headRow > 1 && row > 0 && row < headRow
}

func (t *repoTab) syncWorktreeCommit() {
	col, color, connected := t.worktreeLane()
	t.worktreeRow = commit{
		hash:      worktreeHash,
		subject:   "未提交改动",
		nodeCol:   col,
		nodeColor: color,
	}
	if connected {
		t.worktreeRow.segs = []seg{{a: col, b: col, bot: true, color: color}}
	}
}

func worktreeBadges(files []fileChange) []string {
	var staged, unstaged, untracked, conflict int
	for _, f := range files {
		switch f.extra {
		case "未跟踪":
			untracked++
		case "冲突":
			conflict++
		case "已暂存", "重命名":
			staged++
		case "已暂存并继续改":
			staged++
			unstaged++
		default:
			unstaged++
		}
	}
	var out []string
	if conflict > 0 {
		out = append(out, fmt.Sprintf("冲突 %d", conflict))
	}
	if staged > 0 {
		out = append(out, fmt.Sprintf("已暂存 %d", staged))
	}
	if unstaged > 0 {
		out = append(out, fmt.Sprintf("未暂存 %d", unstaged))
	}
	if untracked > 0 {
		out = append(out, fmt.Sprintf("未跟踪 %d", untracked))
	}
	return out
}

func (a *app) applyWorktree(t *repoTab, files []fileChange) {
	hadRow := t.hasWorktreeRow()
	key := worktreeKey(files)
	count := len(files)
	same := t.worktreeKey == key
	t.worktreeKey = key
	t.worktreeCount = count
	t.worktreeFiles = files
	if t.worktreeSel >= count {
		t.worktreeSel = -1
	}
	a.summaryMu.Lock()
	if a.repoSummaries != nil {
		a.repoSummaries[t.path] = repoSummary{branch: t.branch, dirty: count > 0, checked: time.Now()}
	}
	a.summaryMu.Unlock()
	hasRow := t.hasWorktreeRow()
	a.shiftSelectionForWorktreeRow(t, hadRow, hasRow)
	if !t.worktree {
		return
	}
	if count == 0 {
		a.closeWorktree(t)
		t.notice = "工作区已没有未提交的改动"
		return
	}
	if hasRow && t.selected != 0 {
		t.selected = 0
		t.selection.Clear()
		t.selection.Add(worktreeHash)
	}
	if same && !t.compareLoading && t.compareErr == "" {
		return
	}
	if t.restoreFile == "" && t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
		t.restoreFile = t.compareFiles[t.fileSel].path
	}
	a.fillWorktree(t, files)
}

func (a *app) shiftSelectionForWorktreeRow(t *repoTab, had, has bool) {
	switch {
	case !had && has && t.selected >= 0:
		t.selected++
	case had && !has && t.selected == 0:
		t.selected = -1
		t.selection.Remove(worktreeHash)
	case had && !has && t.selected > 0:
		t.selected--
	}
}

func (a *app) showWorktree(t *repoTab) {
	if t.query.term != "" || t.query.filePath != "" {
		return
	}
	if a.sidebarHidden {
		a.sidebarHidden = false
	}
	already := t.worktree && t.comparison == nil && t.inspecting == zeroHash
	t.worktree = true
	t.comparison = nil
	t.inspecting = zeroHash
	if t.hasWorktreeRow() {
		t.selected = 0
		t.selection.Clear()
		t.selection.Add(worktreeHash)
	}
	if t.detailSplit == 0 {
		t.detailSplit = 52
	}
	if already {
		return
	}
	a.clearDetails(t)
	t.compareLoading = true
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
	t.worktreeSel = -1
	if t.comparison != nil || t.inspecting != zeroHash {
		return
	}
	t.selected = -1
	t.selection.Clear()
	a.clearDiff(t)
}

func (a *app) selectWorktreeFile(t *repoTab, i int) {
	if i < 0 || i >= len(t.worktreeFiles) {
		return
	}
	t.worktreeSel = i
	targetPath := t.worktreeFiles[i].path
	if t.worktree && len(t.compareFiles) > 0 {
		for idx, f := range t.compareFiles {
			if f.path == targetPath {
				t.fileSel = idx
				a.loadFileDiff(t, idx)
				return
			}
		}
	}
	t.restoreFile = targetPath
	if !t.worktree || t.comparison != nil || t.inspecting != zeroHash {
		a.showWorktree(t)
		return
	}
	t.compareLoading = true
	a.loadWorktreeStatus(t, true)
}

func stageFile(ctx context.Context, dir, path string) error {
	_, err := runGit(ctx, dir, "add", "-A", "--", path)
	return err
}

func unstageFile(ctx context.Context, dir, path string) error {
	_, err := runGit(ctx, dir, "restore", "--staged", "--", path)
	if err != nil {
		_, err = runGit(ctx, dir, "reset", "HEAD", "--", path)
	}
	return err
}

func stageAll(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "add", "-A")
	return err
}

func unstageAll(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "restore", "--staged", ".")
	if err != nil {
		_, err = runGit(ctx, dir, "reset", "HEAD")
	}
	return err
}

func discardFile(ctx context.Context, dir string, f fileChange) error {
	if f.extra == "未跟踪" {
		fullPath := filepath.Join(dir, filepath.FromSlash(f.path))
		return os.RemoveAll(fullPath)
	}
	_, err := runGit(ctx, dir, "restore", "--worktree", "--", f.path)
	if err != nil {
		_, err = runGit(ctx, dir, "checkout", "--", f.path)
	}
	return err
}

func commitWorktree(ctx context.Context, dir, msg string, amend bool) error {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return errors.New("请输入提交说明")
	}
	args := []string{"commit", "-m", msg}
	if amend {
		args = []string{"commit", "--amend", "-m", msg}
	}
	_, err := runGit(ctx, dir, args...)
	return err
}

func (a *app) refreshWorktree(t *repoTab) {
	if t == nil {
		return
	}
	a.loadWorktreeStatus(t, t.worktree)
}

func (a *app) stageWorktreeFile(t *repoTab, path string) {
	if t == nil || path == "" {
		return
	}
	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := stageFile(ctx, repoPath, path); err != nil {
			a.post(func() {
				a.setBanner("暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) unstageWorktreeFile(t *repoTab, path string) {
	if t == nil || path == "" {
		return
	}
	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := unstageFile(ctx, repoPath, path); err != nil {
			a.post(func() {
				a.setBanner("取消暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) stageAllWorktree(t *repoTab) {
	if t == nil {
		return
	}
	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := stageAll(ctx, repoPath); err != nil {
			a.post(func() {
				a.setBanner("全部暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) unstageAllWorktree(t *repoTab) {
	if t == nil {
		return
	}
	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := unstageAll(ctx, repoPath); err != nil {
			a.post(func() {
				a.setBanner("全部取消暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) promptDiscardWorktreeFile(t *repoTab, f fileChange) {
	if t == nil {
		return
	}
	a.discard = discardDialog{
		open: true,
		file: f,
	}
	a.showDiscard = true
}

func (a *app) runDiscardWorktreeFile(t *repoTab, f fileChange) {
	if t == nil {
		return
	}
	a.showDiscard = false
	a.discard.open = false
	repoPath := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := discardFile(ctx, repoPath, f); err != nil {
			a.post(func() {
				a.setBanner("丢弃失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.pendingToast = "已丢弃改动: " + filepath.Base(f.path)
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) runCommitWorktree(t *repoTab) {
	if t == nil {
		return
	}
	msg := strings.TrimSpace(t.commitMsg)
	if msg == "" {
		t.commitErr = "请输入提交说明"
		return
	}
	stagedCount := 0
	for _, f := range t.worktreeFiles {
		if f.extra == "已暂存" || f.extra == "重命名" || f.extra == "已暂存并继续改" {
			stagedCount++
		}
	}
	if stagedCount == 0 && !t.commitAmend {
		t.commitErr = "请先暂存要提交的改动"
		return
	}
	t.commitBusy = true
	t.commitErr = ""
	repoPath := t.path
	amend := t.commitAmend
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := commitWorktree(ctx, repoPath, msg, amend)
		a.post(func() {
			t.commitBusy = false
			if err != nil {
				t.commitErr = err.Error()
				a.setBanner("提交失败: "+err.Error(), "")
			} else {
				t.commitMsg = ""
				t.commitAmend = false
				t.commitErr = ""
				a.pendingToast = "提交成功"
				a.refresh(t)
			}
		})
	}()
}

func (a *app) worktreeCommitCard(c *ui.Context, t *repoTab) {
	if t == nil {
		return
	}
	theme := c.Theme()
	stagedCount := 0
	for _, f := range t.worktreeFiles {
		if f.extra == "已暂存" || f.extra == "重命名" || f.extra == "已暂存并继续改" {
			stagedCount++
		}
	}
	ui.Column(c.Key(fmt.Sprintf("worktree-commit-card-%d", t.id))).
		FillWidth().
		Padding(8, 10).
		Gap(6).
		Background(theme.Surface.Alpha(0.6)).
		Border(1, theme.Border).
		Radius(theme.Radius).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, "提交改动").Bold().FontSize(12)
				ui.Box(c).Grow(1)
				if stagedCount > 0 {
					ui.Text(c, fmt.Sprintf("%d 个文件已暂存", stagedCount)).FontSize(11).TextColor(theme.Accent)
				} else {
					ui.Text(c, "未暂存任何文件").FontSize(11).TextColor(theme.TextMuted)
				}
			})
			ui.TextArea(c.Key(fmt.Sprintf("commit-msg-%d", t.id)), &t.commitMsg).
				Placeholder("输入提交说明 (Commit message)... (⌘Enter)").
				Height(54).
				Disabled(t.commitBusy)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				if t.head != zeroHash {
					if ui.Checkbox(c.Key(fmt.Sprintf("commit-amend-%d", t.id)), &t.commitAmend, "Amend").
						Disabled(t.commitBusy).Changed() {
						if t.commitAmend && strings.TrimSpace(t.commitMsg) == "" {
							if headMsg, err := readCommitMessage(context.Background(), t.path, t.head); err == nil {
								t.commitMsg = headMsg
							}
						}
					}
				}
				ui.Box(c).Grow(1)
				canCommit := !t.commitBusy && strings.TrimSpace(t.commitMsg) != "" && (stagedCount > 0 || t.commitAmend)
				commitBtn := ui.PrimaryButton(c.Key(fmt.Sprintf("commit-submit-%d", t.id)), "提交").
					Padding(2, 10).
					FontSize(12).
					Disabled(!canCommit)
				if commitBtn.Clicked() {
					a.runCommitWorktree(t)
				}
			})
			if t.commitErr != "" {
				ui.Text(c, t.commitErr).FontSize(11).TextColor(theme.Danger).SingleLine().Grow(1).MinWidth(0)
			}
		})
}

func (a *app) discardModal(c *ui.Context) {
	if !a.showDiscard {
		return
	}
	tab := a.activeTab()
	if tab == nil {
		a.showDiscard = false
		return
	}
	theme := c.Theme()
	ui.Modal(c.Key("discard-confirm-modal"), &a.showDiscard, func() {
		f := a.discard.file
		ui.Column(c).Width(380).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconTrash2).Size(18, 18).TextColor(theme.Danger)
				ui.Text(c, "确定丢弃文件修改？").Bold().FontSize(14)
			})
			ui.Text(c, f.path).FontSize(12).Font("monospace").TextColor(theme.TextMuted)
			if f.extra == "未跟踪" {
				ui.Text(c, "此文件为未跟踪文件，丢弃将永久删除该文件，无法恢复。").FontSize(12).TextColor(theme.Danger)
			} else {
				ui.Text(c, "工作区的修改将被撤销并恢复，无法恢复。").FontSize(12).TextColor(theme.TextMuted)
			}
			ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
				if ui.Button(c.Key("discard-cancel"), "取消").Clicked() {
					a.showDiscard = false
					a.discard.open = false
				}
				confirmBtn := ui.Button(c.Key("discard-confirm"), "确定丢弃").Padding(2, 12)
				if confirmBtn.Clicked() {
					a.runDiscardWorktreeFile(tab, f)
				}
			})
		})
	})
}
