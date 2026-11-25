package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type worktreeRemoveCheck struct {
	checking      bool
	dirty         bool
	ignored       bool
	unmerged      bool
	submodule     bool
	compareTarget string
	locked        string
	err           string
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

func resolveWorktreeGitArg(parentDir, wtPath string) (string, error) {
	parentDistro, _, parentWSL := parseWSLUNCPath(parentDir)
	wtDistro, wtLinux, wtWSL := parseWSLUNCPath(wtPath)
	if parentWSL {
		if !wtWSL || !strings.EqualFold(parentDistro, wtDistro) {
			return "", errors.New(tr("工作树与父仓库不在同一个 WSL 发行版中"))
		}
		return wtLinux, nil
	}
	if wtWSL {
		return "", errors.New(tr("工作树与父仓库不在同一个文件系统环境中"))
	}
	return wtPath, nil
}

func evalPathPrefix(p string) string {
	clean := canonicalRepoDir(p)
	if clean == "" || isDockerRepoPath(clean) {
		return clean
	}
	if _, _, isWSL := parseWSLUNCPath(clean); isWSL {
		return clean
	}
	cur := clean
	var tail []string
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func sameWorktreePath(a, b string) bool {
	ca, cb := canonicalRepoDir(a), canonicalRepoDir(b)
	if ca == cb {
		return true
	}
	return evalPathPrefix(ca) == evalPathPrefix(cb)
}

func findLinkedWorktree(ctx context.Context, parentDir, wtPath string) (repoWorktree, bool, error) {
	linked, err := listWorktrees(ctx, parentDir)
	if err != nil {
		return repoWorktree{}, false, err
	}
	for _, wt := range linked {
		if sameWorktreePath(wt.path, wtPath) {
			return wt, true, nil
		}
	}
	return repoWorktree{}, false, nil
}

func removeWorktree(ctx context.Context, parentDir, wtPath string, force bool) error {
	argPath, err := resolveWorktreeGitArg(parentDir, wtPath)
	if err != nil {
		return err
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, argPath)
	_, err = runGit(ctx, parentDir, args...)
	return err
}

func pruneWorktreeRecord(ctx context.Context, parentDir, wtPath string) error {
	ctx, release := acquireWorktreeLanes(ctx, parentDir, wtPath)
	defer release()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if isDockerRepoPath(parentDir) {
		return errors.New(tr("无法安全清理该工作树记录"))
	}
	if _, err := resolveWorktreeGitArg(parentDir, wtPath); err != nil {
		return err
	}
	current, found, err := findLinkedWorktree(ctx, parentDir, wtPath)
	if err != nil {
		return err
	}
	if !found {
		return errors.New(tr("未在父仓库中找到该工作树记录"))
	}
	if current.prunable == "" {
		return errors.New(tr("工作树目录已恢复存在，请改用移除工作树"))
	}
	if current.locked != "" {
		return errors.New(tr("该工作树已锁定，请先解锁后再移除。"))
	}
	common, err := runGit(ctx, parentDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	commonPath := strings.TrimSuffix(string(common), "\n")
	distro, _, wsl := parseWSLUNCPath(parentDir)
	if wsl {
		commonPath = formatWSLUNCPath(distro, commonPath)
	}
	if !filepath.IsAbs(commonPath) {
		return errors.New(tr("无法安全清理该工作树记录"))
	}
	commonRoot, err := os.OpenRoot(commonPath)
	if err != nil {
		return err
	}
	defer commonRoot.Close()
	meta, err := commonRoot.Lstat("worktrees")
	if err != nil {
		return err
	}
	if !meta.IsDir() {
		return errors.New(tr("无法安全清理该工作树记录"))
	}
	root, err := commonRoot.OpenRoot("worktrees")
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var match string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		gitdir, readErr := root.ReadFile(filepath.Join(entry.Name(), "gitdir"))
		if readErr != nil {
			return readErr
		}
		p := strings.TrimSuffix(string(gitdir), "\n")
		if wsl && strings.HasPrefix(p, "/") {
			p = formatWSLUNCPath(distro, p)
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root.Name(), entry.Name(), p)
		}
		if sameWorktreePath(filepath.Dir(p), wtPath) && filepath.Base(p) == ".git" {
			if match != "" {
				return errors.New(tr("无法安全清理该工作树记录"))
			}
			match = entry.Name()
		}
	}
	if match == "" {
		return errors.New(tr("无法安全清理该工作树记录"))
	}
	if _, err := root.Lstat(filepath.Join(match, "locked")); err == nil || !os.IsNotExist(err) {
		return errors.New(tr("无法安全清理该工作树记录"))
	}
	if _, err := os.Lstat(wtPath); err == nil || !os.IsNotExist(err) {
		return errors.New(tr("工作树目录已恢复存在，请改用移除工作树"))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.RemoveAll(match); err != nil {
		return err
	}
	_, stillThere, err := findLinkedWorktree(ctx, parentDir, wtPath)
	if err != nil {
		return err
	}
	if stillThere {
		return errors.New(tr("工作树记录未能移除"))
	}
	return nil
}

func inspectWorktreeRemoval(ctx context.Context, parentDir string, wt repoWorktree) (repoWorktree, worktreeRemoveCheck) {
	current, found, err := findLinkedWorktree(ctx, parentDir, wt.path)
	if err != nil {
		return wt, worktreeRemoveCheck{err: trimMsg(err.Error())}
	}
	if !found {
		return wt, worktreeRemoveCheck{err: tr("未在父仓库中找到该工作树记录")}
	}
	wt = current
	check := worktreeRemoveCheck{locked: wt.locked}
	if wt.prunable != "" {
		return wt, check
	}
	out, err := runGit(ctx, wt.path, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignored=matching")
	if err != nil {
		check.err = trimMsg(err.Error())
		return wt, check
	}
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
	subOut, subErr := runGit(ctx, wt.path, "submodule", "status", "--recursive")
	if subErr != nil {
		check.err = trimMsg(subErr.Error())
		return wt, check
	}
	for _, line := range bytes.Split(subOut, []byte{'\n'}) {
		if len(line) > 0 && line[0] != '-' {
			check.submodule = true
			break
		}
	}
	if wt.head != zeroHash {
		targetName := tr("父工作树当前 HEAD")
		if branch, berr := runGit(ctx, parentDir, "symbolic-ref", "--quiet", "--short", "HEAD"); berr == nil {
			if b := strings.TrimSpace(string(branch)); b != "" {
				targetName = b
			}
		}
		check.compareTarget = targetName
		if _, err := runGit(ctx, parentDir, "merge-base", "--is-ancestor", hashHex(wt.head), "HEAD"); err != nil {
			if err.Error() == "exit status 1" {
				check.unmerged = true
			} else {
				check.err = trimMsg(err.Error())
			}
		}
	}
	return wt, check
}

func worktreeRemovalCheckChanged(prevWt, curWt repoWorktree, prevCheck, curCheck worktreeRemoveCheck) bool {
	return prevWt.prunable != curWt.prunable ||
		prevWt.locked != curWt.locked ||
		prevWt.head != curWt.head ||
		prevCheck.dirty != curCheck.dirty ||
		prevCheck.ignored != curCheck.ignored ||
		prevCheck.unmerged != curCheck.unmerged ||
		prevCheck.submodule != curCheck.submodule ||
		curCheck.err != ""
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

func (a *app) runPruneWorktreeRecord(parentRepo, wtPath string) {
	if parentRepo == "" || wtPath == "" {
		return
	}
	w := a.startGitWrite("正在清理失效工作树")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := pruneWorktreeRecord(ctx, parentRepo, wtPath)
		a.post(func() {
			a.finishGitWrite(w)
			if a.closed {
				return
			}
			if err != nil {
				a.setBanner(tr("清理工作树失败：")+trimMsg(err.Error()), parentRepo)
				if tab := a.tabByPath(parentRepo); tab != nil {
					a.reload(tab)
				} else {
					a.refreshRepoSummary(parentRepo)
				}
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
		freshWt, check := inspectWorktreeRemoval(ctx, parentRepo, wt)
		a.post(func() {
			if a.closed || !a.showWorktreeRemove || a.wtRemove.gen != gen {
				return
			}
			a.wtRemove.wt = freshWt
			a.wtRemove.check = check
		})
	}()
}

func (a *app) runRemoveWorktree() {
	d := a.wtRemove
	if d.busy || d.parentRepo == "" || d.wt.path == "" {
		return
	}
	if t := a.tabByPath(d.wt.path); t != nil && (t.commitBusy || t.syncBusy) {
		a.wtRemove.err = tr("该工作树正在执行 Git 操作，请稍后再试。")
		return
	}
	a.wtRemove.busy = true
	a.wtRemove.err = ""
	gen := d.gen
	writeLabel := "正在移除工作树"
	if d.wt.prunable != "" {
		writeLabel = "正在清理失效工作树"
	}
	w := a.startGitWrite(writeLabel)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if d.wt.prunable != "" {
			err := pruneWorktreeRecord(ctx, d.parentRepo, d.wt.path)
			var current repoWorktree
			var check worktreeRemoveCheck
			var restored bool
			if err != nil {
				if wt, found, ferr := findLinkedWorktree(ctx, d.parentRepo, d.wt.path); ferr == nil && found && wt.prunable == "" {
					current, check = inspectWorktreeRemoval(ctx, d.parentRepo, wt)
					restored = true
				}
			}
			a.post(func() {
				a.finishGitWrite(w)
				if a.closed || a.wtRemove.gen != gen {
					return
				}
				a.wtRemove.busy = false
				if err != nil {
					a.wtRemove.err = trimMsg(err.Error())
					if restored {
						a.wtRemove.wt = current
						a.wtRemove.check = check
						a.wtRemove.force = false
					}
					return
				}
				a.showWorktreeRemove = false
				a.finishWorktreeCleanup(d.parentRepo, d.wt.path, tr("已清理失效工作树记录"))
			})
			return
		}

		ctx, release := acquireWorktreeLanes(ctx, d.parentRepo, d.wt.path)
		defer release()
		freshWt, freshCheck := inspectWorktreeRemoval(ctx, d.parentRepo, d.wt)
		if worktreeRemovalCheckChanged(d.wt, freshWt, d.check, freshCheck) {
			a.post(func() {
				a.finishGitWrite(w)
				if a.closed || a.wtRemove.gen != gen {
					return
				}
				a.wtRemove.busy = false
				a.wtRemove.wt = freshWt
				a.wtRemove.check = freshCheck
				a.wtRemove.force = false
				a.wtRemove.err = tr("工作树状态已变化，请重新确认后再移除。")
			})
			return
		}

		err := removeWorktree(ctx, d.parentRepo, freshWt.path, d.force)
		a.post(func() {
			a.finishGitWrite(w)
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
	hasRisk := d.check.dirty || d.check.ignored || d.check.unmerged || d.check.submodule
	canConfirm := !d.busy && !d.check.checking && wt.locked == "" && (wt.prunable != "" || (d.check.err == "" && (!hasRisk || d.force)))

	appModal(c.Key("worktree-remove-modal"), &a.showWorktreeRemove, title, 440, d.busy, func() {
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
		} else if d.check.err != "" {
			ui.Text(c, tr("安全检查失败，无法确认工作树是否安全移除。")).FontSize(12).TextColor(theme.Danger)
		} else if hasRisk {
			if d.check.dirty {
				ui.Text(c, tr("存在未提交或未跟踪的改动，强制移除将丢失这些改动。")).FontSize(12).TextColor(theme.Danger)
			}
			if d.check.ignored {
				ui.Text(c, tr("存在被忽略的文件，移除工作树会一并删除它们。")).FontSize(12).TextColor(theme.Warning)
			}
			if d.check.submodule {
				ui.Text(c, tr("包含已初始化的子模块，强制移除将删除子模块目录。")).FontSize(12).TextColor(theme.Warning)
			}
			if d.check.unmerged {
				target := d.check.compareTarget
				if target == "" {
					target = tr("父工作树当前 HEAD")
				}
				ui.Text(c, fmt.Sprintf(tr("该工作树的提交尚未合并到 %s。"), target)).FontSize(12).TextColor(theme.Warning)
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
