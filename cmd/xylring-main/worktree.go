package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
		default:
			if f.hasStaged() {
				staged++
			}
			if f.hasUnstaged() {
				unstaged++
			}
		}
	}
	var out []string
	if conflict > 0 {
		out = append(out, fmt.Sprintf(tr("冲突 %d"), conflict))
	}
	if staged > 0 {
		out = append(out, fmt.Sprintf(tr("已暂存 %d"), staged))
	}
	if unstaged > 0 {
		out = append(out, fmt.Sprintf(tr("未暂存 %d"), unstaged))
	}
	if untracked > 0 {
		out = append(out, fmt.Sprintf(tr("未跟踪 %d"), untracked))
	}
	return out
}

func (a *app) applyWorktree(t *repoTab, files []fileChange) {
	hadRow := t.hasWorktreeRow()
	count := len(files)
	same := slices.Equal(t.worktreeFiles, files)
	t.worktreeCount = count
	t.worktreeFiles = files
	a.summaryMu.Lock()
	if a.repoSummaries != nil {
		a.repoSummaries[t.path] = repoSummary{
			branch:      t.branch,
			dirty:       count > 0,
			checked:     time.Now(),
			ahead:       t.ahead,
			behind:      t.behind,
			upstream:    t.upstream,
			hasUpstream: t.hasUpstream,
		}
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
		t.restoreFile = t.compareFiles[t.fileSel].rowKey()
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
	// 列表按侧拆成两行：同一文件两侧都有改动时各占一行，各自看自己那半段 diff。
	rows := expandWorktreeRows(files)
	a.startDiff(t, t.head, zeroHash, func(context.Context, string) ([]fileChange, error) {
		return rows, nil
	})
}

func (a *app) loadWorktreeStatus(t *repoTab, open bool) {
	t.worktreeRefreshGen++
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
	t.selected = -1
	t.selection.Clear()
	a.clearDiff(t)
}

func stageFile(ctx context.Context, dir, path string) error {
	_, err := runGit(ctx, dir, "--literal-pathspecs", "add", "-A", "--", path)
	return err
}

// unstageFile 把路径退出暂存区，工作区内容不动。带路径的 reset 在两种状态下都成立：
// 有 HEAD 时把索引还原成 HEAD，还没有第一次提交时按空树重置（索引里不再有这个文件），
// 文件都留在工作区。它只看仓库此刻的状态，不依赖界面缓存的 head，也就没有后台线程读
// 界面字段的竞争，终端在别处切了分支也不会挑错命令。
func unstageFile(ctx context.Context, dir string, paths ...string) error {
	_, err := runGit(ctx, dir, append([]string{"--literal-pathspecs", "reset", "--"}, paths...)...)
	return err
}

func stageAll(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "add", "-A")
	return err
}

// unstageAll 是 unstageFile 的整份版本：同样一条路径级 reset，空仓库也不会失败。
func unstageAll(ctx context.Context, dir string) error {
	_, err := runGit(ctx, dir, "reset", "--", ".")
	return err
}

func discardFile(ctx context.Context, dir string, f fileChange) error {
	if f.extra == "未跟踪" {
		if isDockerRepoPath(dir) {
			rel, err := safeRepoRelPath(f.path)
			if err != nil {
				return err
			}
			_, err = runGit(ctx, dir, "--literal-pathspecs", "clean", "-fd", "--", rel)
			return err
		}
		fullPath := filepath.Join(dir, filepath.FromSlash(f.path))
		return os.RemoveAll(fullPath)
	}
	_, err := runGit(ctx, dir, "--literal-pathspecs", "restore", "--worktree", "--", f.path)
	if err != nil && !dockerCommandGone(err, "") {
		_, err = runGit(ctx, dir, "--literal-pathspecs", "checkout", "--", f.path)
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
	_, err := runGitExtra(ctx, dir, 16<<20, []string{"XYLRING_GUI=1"}, args...)
	return err
}

func (a *app) refreshWorktree(t *repoTab) {
	if t == nil {
		return
	}
	a.loadWorktreeStatus(t, t.worktree)
}

func (a *app) stageWorktreeFile(t *repoTab, path string) {
	if a.write.busy || a.fetch.busy || (t != nil && (t.commitBusy || t.syncBusy)) {
		return
	}
	if t == nil || path == "" {
		return
	}
	t.commitBusy = true
	rec, ok := a.beginRepoWrite(t, "暂存文件")
	if !ok {
		return
	}
	w := a.startGitWrite("正在暂存文件")
	repoPath := t.path
	go func() {
		ctx, cancel := writeContext(repoPath, 10*time.Second)
		defer cancel()
		err := stageFile(ctx, repoPath, path)
		if err != nil {
			a.post(func() {
				a.finishDockerWrite(rec, err)
				a.finishGitWrite(w)
				t.commitBusy = false
				a.setBanner("暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.finishDockerWrite(rec, nil)
			a.finishGitWrite(w)
			t.commitBusy = false
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) unstageWorktreeFile(t *repoTab, f fileChange) {
	if a.write.busy || a.fetch.busy || (t != nil && (t.commitBusy || t.syncBusy)) {
		return
	}
	if t == nil || f.path == "" {
		return
	}
	paths := []string{f.path}
	if f.status == "R" && f.oldPath != "" {
		paths = append(paths, f.oldPath)
	}
	t.commitBusy = true
	rec, ok := a.beginRepoWrite(t, "取消暂存")
	if !ok {
		return
	}
	w := a.startGitWrite("正在取消暂存")
	repoPath := t.path
	go func() {
		ctx, cancel := writeContext(repoPath, 10*time.Second)
		defer cancel()
		err := unstageFile(ctx, repoPath, paths...)
		if err != nil {
			a.post(func() {
				a.finishDockerWrite(rec, err)
				a.finishGitWrite(w)
				t.commitBusy = false
				a.setBanner("取消暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.finishDockerWrite(rec, nil)
			a.finishGitWrite(w)
			t.commitBusy = false
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) stageAllWorktree(t *repoTab) {
	if a.write.busy || a.fetch.busy || (t != nil && (t.commitBusy || t.syncBusy)) {
		return
	}
	if t == nil {
		return
	}
	t.commitBusy = true
	rec, ok := a.beginRepoWrite(t, "全部暂存")
	if !ok {
		return
	}
	w := a.startGitWrite("正在暂存文件")
	repoPath := t.path
	go func() {
		ctx, cancel := writeContext(repoPath, 15*time.Second)
		defer cancel()
		err := stageAll(ctx, repoPath)
		if err != nil {
			a.post(func() {
				a.finishDockerWrite(rec, err)
				a.finishGitWrite(w)
				t.commitBusy = false
				a.setBanner("全部暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.finishDockerWrite(rec, nil)
			a.finishGitWrite(w)
			t.commitBusy = false
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) unstageAllWorktree(t *repoTab) {
	if a.write.busy || a.fetch.busy || (t != nil && (t.commitBusy || t.syncBusy)) {
		return
	}
	if t == nil {
		return
	}
	t.commitBusy = true
	rec, ok := a.beginRepoWrite(t, "全部取消暂存")
	if !ok {
		return
	}
	w := a.startGitWrite("正在取消暂存")
	repoPath := t.path
	go func() {
		ctx, cancel := writeContext(repoPath, 15*time.Second)
		defer cancel()
		err := unstageAll(ctx, repoPath)
		if err != nil {
			a.post(func() {
				a.finishDockerWrite(rec, err)
				a.finishGitWrite(w)
				t.commitBusy = false
				a.setBanner("全部取消暂存失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.finishDockerWrite(rec, nil)
			a.finishGitWrite(w)
			t.commitBusy = false
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) promptDiscardWorktreeFile(t *repoTab, f fileChange) {
	if t == nil || t.unavailable {
		return
	}
	a.discard = discardDialog{
		open: true,
		repo: t.path,
		file: f,
	}
	a.showDiscard = true
}

func (a *app) runDiscardWorktreeFile(t *repoTab, f fileChange) {
	if t == nil || t.unavailable {
		return
	}
	if a.write.busy || a.fetch.busy || (t != nil && (t.commitBusy || t.syncBusy)) {
		return
	}
	if t == nil {
		return
	}
	rec, ok := a.beginRepoWrite(t, "丢弃改动")
	if !ok {
		return
	}
	a.showDiscard = false
	a.discard.open = false
	t.commitBusy = true
	w := a.startGitWrite("正在放弃改动")
	repoPath := t.path
	go func() {
		ctx, cancel := writeContext(repoPath, 10*time.Second)
		defer cancel()
		if err := discardFile(ctx, repoPath, f); err != nil {
			a.post(func() {
				a.finishDockerWrite(rec, err)
				a.finishGitWrite(w)
				t.commitBusy = false
				a.setBanner("丢弃失败: "+err.Error(), "")
			})
			return
		}
		a.post(func() {
			a.finishDockerWrite(rec, nil)
			a.finishGitWrite(w)
			t.commitBusy = false
			a.pendingToast = "已丢弃改动: " + filepath.Base(f.path)
			a.refreshWorktree(t)
			if t.worktree {
				a.showWorktree(t)
			}
		})
	}()
}

func (a *app) runCommitWorktree(t *repoTab) {
	if t == nil || t.unavailable {
		return
	}
	if a.write.busy || a.fetch.busy || t.commitBusy || t.syncBusy {
		return
	}
	msg := strings.TrimSpace(t.commitMsg)
	if msg == "" {
		t.commitErr = "请输入提交说明"
		return
	}
	stagedCount := 0
	for _, f := range t.worktreeFiles {
		if f.hasStaged() {
			stagedCount++
		}
	}
	if stagedCount == 0 && !t.commitAmend {
		t.commitErr = "请先暂存要提交的改动"
		return
	}
	repoPath := t.path
	amend := t.commitAmend
	if amend && isDockerRepoPath(repoPath) {
		t.commitErr = "容器仓库暂不支持修改上次提交"
		return
	}
	if reason := a.dockerWriteBlockReason(repoPath); reason != "" {
		t.commitErr = "上次操作结果未确认，请先核对容器状态"
		return
	}
	t.commitBusy = true
	rec, ok := a.beginRepoWrite(t, "提交")
	if !ok {
		return
	}
	w := a.startGitWrite("正在提交")
	t.commitErr = ""
	prov := t.commitProv
	provEdited := t.commitProvEdited
	go func() {
		ctx, cancel := writeContext(repoPath, 30*time.Second)
		defer cancel()
		var err error
		if !provEdited && prov.agent == "" && prov.model == "" {
			prov, err = messageProvenance(ctx, repoPath, msg)
		}
		var finalMsg string
		if err == nil {
			finalMsg, err = applyProvenanceTrailers(ctx, repoPath, msg, prov)
		}
		sent := false
		if err == nil {
			sent = true
			err = commitWorktree(ctx, repoPath, finalMsg, amend)
		}
		a.post(func() {
			// 写操作记录和界面状态一样只在界面线程改。
			if sent {
				a.finishDockerWrite(rec, err)
			} else {
				// 归因合成失败时提交命令根本没发出去，不能把记录留成「结果未知」。
				a.clearDockerWriteRecord(rec)
			}
			a.finishGitWrite(w)
			t.commitBusy = false
			if err != nil {
				t.commitErr = err.Error()
				a.setBanner("提交失败: "+err.Error(), "")
			} else {
				a.rememberProvenance(prov)
				t.commitProv = provenance{}
				t.commitProvEdited = false
				t.commitMsg = ""
				t.commitAmend = false
				t.commitErr = ""
				a.pendingToast = "提交成功"
				a.refresh(t)
			}
		})
	}()
}

func commitPinTip(pinned, expanded bool) string {
	if pinned {
		return tr("取消固定（随鼠标移开自动折叠）")
	}
	if expanded {
		return tr("固定展开提交面板")
	}
	return tr("展开提交面板")
}

func (a *app) worktreeCommitCard(c *ui.Context, t *repoTab) {
	if t == nil {
		return
	}
	theme := c.Theme()
	stagedCount := 0
	for _, f := range t.worktreeFiles {
		if f.hasStaged() {
			stagedCount++
		}
	}
	card := ui.Column(c.Key(fmt.Sprintf("worktree-commit-card-%d", t.id))).
		FillWidth().
		Padding(6, 10).
		Gap(6).
		Background(theme.Surface.Alpha(0.6)).
		Border(1, theme.Border).
		Radius(theme.Radius).
		Transition(paneMotion())

	isExpanded := card.Hovered() ||
		card.FocusWithin() ||
		strings.TrimSpace(t.commitMsg) != "" ||
		t.commitAmend ||
		t.commitBusy ||
		t.commitErr != "" ||
		t.commitPinned

	card.Children(func() {
		header := ui.Row(c.Key(fmt.Sprintf("commit-header-%d", t.id))).
			FillWidth().
			AlignItems(ui.Center).
			Gap(6).
			Cursor(ui.CursorPointer).
			Tooltip(commitPinTip(t.commitPinned, isExpanded))
		header.Children(func() {
			ui.Text(c, tr("提交改动")).Bold().FontSize(12).Shrink(0)
			if t.operation != "" {
				ui.Text(c, tr(operationDisplayName(t.operation))+tr(" 进行中")).FontSize(12).TextColor(theme.Warning).Shrink(0)
			}
			ui.Box(c).Grow(1)
			toggleIcon := iconExpandAll
			if isExpanded {
				toggleIcon = iconCollapseAll
			}
			ui.Icon(c, toggleIcon).Size(12, 12).TextColor(theme.TextMuted).Shrink(0)
		})
		if header.Clicked() {
			t.commitPinned = !t.commitPinned
		}

		if isExpanded {
			a.commitRingControl(c, t)
			ui.TextArea(c.Key(fmt.Sprintf("commit-msg-%d", t.id)), &t.commitMsg).
				Placeholder(tr("写提交说明（⌘Enter）")).
				Height(54).
				Disabled(t.commitBusy)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
				// 普通提交卡在容器仓库里不显示 amend 控件：修改提交说明不在第一版范围。
				if t.head != zeroHash && !isDockerRepoPath(t.path) {
					if ui.Checkbox(c.Key(fmt.Sprintf("commit-amend-%d", t.id)), &t.commitAmend, tr("修改上次提交")).
						Disabled(t.commitBusy).Changed() {
						t.amendGen++
						if t.commitAmend {
							a.loadAmendMessage(t)
						} else {
							t.commitProv = provenance{}
						}
					}
				}
				ui.Box(c).Grow(1)
				canCommit := !t.commitBusy && strings.TrimSpace(t.commitMsg) != "" && (stagedCount > 0 || t.commitAmend)
				commitBtn := ui.PrimaryButton(c.Key(fmt.Sprintf("commit-submit-%d", t.id)), tr("提交")).
					Padding(2, 10).
					FontSize(12).
					Disabled(!canCommit)
				if commitBtn.Clicked() {
					a.runCommitWorktree(t)
				}
			})
			if t.commitErr != "" {
				ui.Text(c, trMessage(t.commitErr)).FontSize(11).TextColor(theme.Danger).SingleLine().Grow(1).MinWidth(0)
			}
		}
	})
}

func (a *app) discardModal(c *ui.Context) {
	if !a.showDiscard {
		return
	}
	tab := a.tabByPath(a.discard.repo)
	if tab == nil {
		a.showDiscard = false
		return
	}
	theme := c.Theme()
	f := a.discard.file
	appModal(c.Key("discard-confirm-modal"), &a.showDiscard, tr("确定丢弃文件修改？"), 420, false, func() {
		ui.Text(c, f.path).FontSize(12).Font("monospace").TextColor(theme.TextMuted)
		if f.extra == "未跟踪" {
			ui.Text(c, tr("此文件为未跟踪文件，丢弃将永久删除该文件，无法恢复。")).FontSize(12).TextColor(theme.Danger)
		} else {
			ui.Text(c, tr("工作区的修改将被撤销并恢复，无法恢复。")).FontSize(12).TextColor(theme.TextMuted)
		}
	}, func() {
		if ui.Button(c.Key("discard-cancel"), tr("取消")).Clicked() {
			a.showDiscard = false
			a.discard.open = false
		}
		confirmBtn := ui.Button(c.Key("discard-confirm"), tr("确定丢弃")).Padding(2, 12)
		if confirmBtn.Clicked() {
			a.runDiscardWorktreeFile(tab, f)
		}
	})
}

func (a *app) loadAmendMessage(t *repoTab) {
	head, path := t.head, t.path
	t.amendGen++
	gen := t.amendGen
	initialProv := t.commitProv
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		message, err := readCommitMessage(ctx, path, head)
		var prov provenance
		if err == nil {
			prov, err = messageProvenance(ctx, path, message)
		}
		a.post(func() {
			if t.closed || t.amendGen != gen || t.head != head || !t.commitAmend || t.commitBusy {
				return
			}
			if err != nil {
				t.commitErr = "读取提交说明失败：" + err.Error()
				return
			}
			if strings.TrimSpace(t.commitMsg) == "" {
				t.commitMsg = message
			}
			if !t.commitProvEdited && t.commitProv == initialProv {
				t.commitProv = prov
			}
		})
	}()
}

func (a *app) worktreeStageCheckbox(c *ui.Context, t *repoTab, f fileChange, keyPrefix string) {
	theme := c.Theme()
	key := fmt.Sprintf("%s-%d-%s", keyPrefix, t.id, f.rowKey())
	if f.extra == "冲突" {
		ui.Box(c.Key(key)).
			Size(14, 14).Radius(3).
			Background(theme.Danger.Alpha(0.15)).Border(1, theme.Danger.Alpha(0.5)).
			Shrink(0).Center().Tooltip(tr("存在合并冲突")).Children(func() {
			ui.Text(c, "!").Font("monospace").FontSize(10).Bold().TextColor(theme.Danger)
		})
		return
	}
	// 一行只属于一侧：已暂存的行勾上，未暂存的行空着，点一下就是那一侧的操作。
	staged := f.side == sideStaged
	label, tip := "暂存文件", "未暂存 (点击暂存)"
	if staged {
		label, tip = "取消暂存", "已暂存 (点击取消暂存)"
	}
	btn := ui.ButtonBase(c.Key(key)).
		Size(14, 14).Radius(3).
		Label(tr(label)).Tooltip(trMessage(tip)).
		Cursor(ui.CursorPointer).Shrink(0).Center().Transition(colorMotion())
	if staged {
		btn.Background(theme.Accent.Alpha(0.18)).Border(1, theme.Accent.Alpha(0.6))
	} else if btn.Hovered() {
		btn.Background(theme.SurfaceHover).Border(1, theme.TextMuted)
	} else {
		btn.Border(1, theme.TextMuted.Alpha(0.55))
	}
	btn.Children(func() {
		if staged {
			ui.Icon(c, iconCheck).Size(10, 10).TextColor(theme.Accent)
		}
	})
	if btn.Clicked() {
		if staged {
			a.unstageWorktreeFile(t, f)
		} else {
			a.stageWorktreeFile(t, f.path)
		}
	}
}

func (a *app) worktreeStageHoverButtons(c *ui.Context, t *repoTab, f fileChange) {
	key := func(prefix string) string { return prefix + f.rowKey() }
	if f.side == sideStaged {
		fileIconButton(c, key("inspect-unstage-"), "取消暂存", iconMinus).Tooltip(tr("取消暂存")).OnClick(func() {
			a.unstageWorktreeFile(t, f)
		})
		return
	}
	fileIconButton(c, key("inspect-stage-"), "暂存文件", iconPlus).Tooltip(tr("暂存文件")).OnClick(func() {
		a.stageWorktreeFile(t, f.path)
	})
	fileIconButton(c, key("inspect-discard-"), "丢弃改动", iconTrash2).Tooltip(tr("丢弃改动")).OnClick(func() {
		a.promptDiscardWorktreeFile(t, f)
	})
}

func (a *app) worktreeFileMenu(c *ui.Context, m *ui.Menu, t *repoTab, f fileChange) {
	// 容器仓库仍只开放整文件操作。
	container := isDockerRepoPath(t.path)
	if f.side == sideStaged {
		// 已暂存的行只能往后退：屏幕上的 diff 就是 HEAD→索引，块序号对得上。
		if container {
			if m.Item(tr("取消暂存")).Chosen() {
				a.unstageWorktreeFile(t, f)
			}
			return
		}
		if m.Item("审阅并取消暂存…").Chosen() {
			a.openOperation("审阅并取消暂存", "", f.path)
		}
		m.Separator()
		if m.Item(tr("取消暂存")).Chosen() {
			a.unstageWorktreeFile(t, f)
		}
		return
	}
	if f.extra == "冲突" {
		if !container {
			if m.Item(tr("使用外部合并工具解决…")).Chosen() {
				a.openOperation("外部合并工具", "", f.path)
			}
		}
		if m.Item(tr("标记已解决")).Chosen() {
			a.stageWorktreeFile(t, f.path)
		}
		return
	}
	if !container {
		if m.Item("审阅并暂存…").Chosen() {
			a.openOperation("审阅并暂存", "", f.path)
		}
	}
	m.Separator()
	if m.Item(tr("暂存文件")).Chosen() {
		a.stageWorktreeFile(t, f.path)
	}
	if m.Item(tr("丢弃改动…")).Chosen() {
		a.promptDiscardWorktreeFile(t, f)
	}
}

func (a *app) operationBanner(c *ui.Context, tab *repoTab) {
	if tab == nil || tab.operation == "" {
		return
	}
	theme := c.Theme()
	ui.Row(c.Key("operation-banner")).FillWidth().Padding(8, 14).Gap(10).AlignItems(ui.Center).
		Background(theme.Warning.Alpha(0.12)).Border(1, theme.Warning.Alpha(0.28)).Radius(theme.Radius).Shrink(0).Children(func() {
		ui.Text(c, tr("正在进行")+tr(operationDisplayName(tab.operation))).FontSize(13).Grow(1).MinWidth(0)
		for _, action := range []string{"继续操作", "中止操作"} {
			act := action
			if ui.Button(c.Key("banner-"+act), act).Disabled(tab.commitBusy || a.write.busy).Clicked() {
				a.openOperation(act, "", "")
			}
		}
	})
}
