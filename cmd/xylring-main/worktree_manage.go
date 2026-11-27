package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type worktreeRemoveCheck struct {
	checking          bool
	dirty             bool
	ignored           bool
	unmerged          bool
	submodule         bool
	reflogUnreachable bool
	prunableStaged    bool
	prunableDetached  bool
	hasDraft          bool
	draftText         string
	dirtyEntries      []string
	ignoredEntries    []string
	submoduleEntries  []string
	stateDigest       string
	compareTarget     string
	locked            string
	err               string
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

func resolveWorktreeAdminDir(ctx context.Context, parentDir, wtPath string) (string, string, error) {
	if isDockerRepoPath(parentDir) {
		return "", "", errors.New(tr("无法安全清理该工作树记录"))
	}
	if _, err := resolveWorktreeGitArg(parentDir, wtPath); err != nil {
		return "", "", err
	}
	common, err := runGit(ctx, parentDir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	commonPath := strings.TrimSuffix(string(common), "\n")
	distro, _, wsl := parseWSLUNCPath(parentDir)
	if wsl {
		commonPath = formatWSLUNCPath(distro, commonPath)
	}
	if !filepath.IsAbs(commonPath) {
		return "", "", errors.New(tr("无法安全清理该工作树记录"))
	}
	commonRoot, err := os.OpenRoot(commonPath)
	if err != nil {
		return "", "", err
	}
	defer commonRoot.Close()
	meta, err := commonRoot.Lstat("worktrees")
	if err != nil {
		return "", "", err
	}
	if !meta.IsDir() {
		return "", "", errors.New(tr("无法安全清理该工作树记录"))
	}
	root, err := commonRoot.OpenRoot("worktrees")
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return "", "", err
	}
	var match string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		gitdir, readErr := root.ReadFile(filepath.Join(entry.Name(), "gitdir"))
		if readErr != nil {
			return "", "", readErr
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
				return "", "", errors.New(tr("无法安全清理该工作树记录"))
			}
			match = entry.Name()
		}
	}
	if match == "" {
		return "", "", errors.New(tr("无法安全清理该工作树记录"))
	}
	return commonPath, match, nil
}

func inspectWorktreeAdminRecoverable(ctx context.Context, parentDir string, wt repoWorktree, checkIndex bool) (staged bool, detached bool, adminDigest string, err error) {
	commonPath, match, err := resolveWorktreeAdminDir(ctx, parentDir, wt.path)
	if err != nil {
		return false, false, "", err
	}
	wtGitDir := filepath.Join(commonPath, "worktrees", match)
	gitDirArg := wtGitDir
	if _, wtLinux, wsl := parseWSLUNCPath(wtGitDir); wsl {
		gitDirArg = wtLinux
	}
	h := sha256.New()
	_, _ = h.Write(wt.head[:])

	// 1. Check whether the worktree's index still holds staged-only changes.
	if checkIndex {
		indexPath := filepath.Join(wtGitDir, "index")
		if info, statErr := os.Lstat(indexPath); statErr == nil {
			if !info.Mode().IsRegular() {
				return false, false, "", errors.New(tr("工作树索引文件类型异常"))
			}
			indexBytes, rerr := os.ReadFile(indexPath)
			if rerr != nil {
				return false, false, "", rerr
			}
			_, _ = io.WriteString(h, "INDEX:\x00")
			_, _ = h.Write(indexBytes)
			if _, headErr := runGit(ctx, parentDir, "--git-dir="+gitDirArg, "rev-parse", "--verify", "--quiet", "HEAD"); headErr == nil {
				diffOut, diffErr := runGit(ctx, parentDir, "--git-dir="+gitDirArg, "diff", "--cached", "--name-only")
				if diffErr != nil {
					return false, false, "", diffErr
				}
				if len(bytes.TrimSpace(diffOut)) > 0 {
					staged = true
				}
			} else {
				lsOut, lsErr := runGit(ctx, parentDir, "--git-dir="+gitDirArg, "ls-files")
				if lsErr != nil {
					return false, false, "", lsErr
				}
				if len(bytes.TrimSpace(lsOut)) > 0 {
					staged = true
				}
			}
		} else if !os.IsNotExist(statErr) {
			return false, false, "", statErr
		}
	}

	// 2. Check whether detached HEAD or worktree reflog references commits not on any branch/tag/remote.
	candidates := map[string]struct{}{}
	if wt.branch == "" && wt.head != zeroHash {
		candidates[hashHex(wt.head)] = struct{}{}
	}
	reflogPath := filepath.Join(wtGitDir, "logs", "HEAD")
	if info, statErr := os.Lstat(reflogPath); statErr == nil {
		if !info.Mode().IsRegular() {
			return staged, false, "", errors.New(tr("工作树 reflog 文件类型异常"))
		}
		reflogBytes, rerr := os.ReadFile(reflogPath)
		if rerr != nil {
			return staged, false, "", rerr
		}
		_, _ = io.WriteString(h, "REFLOG:\x00")
		_, _ = h.Write(reflogBytes)
		for _, line := range strings.Split(string(reflogBytes), "\n") {
			fields := strings.Fields(line)
			for i := 0; i < 2 && i < len(fields); i++ {
				oid := fields[i]
				if len(oid) == 40 && strings.Trim(oid, "0") != "" {
					candidates[oid] = struct{}{}
				}
			}
		}
	} else if !os.IsNotExist(statErr) {
		return staged, false, "", statErr
	}
	if len(candidates) > 0 {
		oids := make([]string, 0, len(candidates))
		for oid := range candidates {
			oids = append(oids, oid)
		}
		slices.Sort(oids)
		args := make([]string, 0, len(oids)+7)
		args = append(args, "rev-list", "--ignore-missing", "--max-count=1")
		args = append(args, oids...)
		args = append(args, "--not", "--branches", "--tags", "--remotes")
		unreached, rerr := runGit(ctx, parentDir, args...)
		if rerr != nil {
			return staged, false, "", rerr
		}
		if len(bytes.TrimSpace(unreached)) > 0 {
			detached = true
		}
	}
	return staged, detached, hex.EncodeToString(h.Sum(nil)), nil
}

func inspectPrunableWorktreeRecoverable(ctx context.Context, parentDir string, wt repoWorktree) (staged bool, detached bool, err error) {
	staged, detached, _, err = inspectWorktreeAdminRecoverable(ctx, parentDir, wt, true)
	return staged, detached, err
}

func pruneWorktreeRecord(ctx context.Context, parentDir, wtPath string, force bool) error {
	ctx, release := acquireWorktreeLanes(ctx, parentDir, wtPath)
	defer release()
	return pruneWorktreeRecordLocked(ctx, parentDir, wtPath, force)
}

func pruneWorktreeRecordLocked(ctx context.Context, parentDir, wtPath string, force bool) error {
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
	if !force {
		staged, detached, rerr := inspectPrunableWorktreeRecoverable(ctx, parentDir, current)
		if rerr != nil {
			return fmt.Errorf("%s: %w", tr("无法确认失效工作树是否包含可恢复数据"), rerr)
		}
		if staged || detached {
			return errors.New(tr("该失效工作树仍包含可恢复的暂存改动或游离提交，请在弹窗中确认强制清理。"))
		}
	}
	commonPath, match, err := resolveWorktreeAdminDir(ctx, parentDir, wtPath)
	if err != nil {
		return err
	}
	commonRoot, err := os.OpenRoot(commonPath)
	if err != nil {
		return err
	}
	defer commonRoot.Close()
	root, err := commonRoot.OpenRoot("worktrees")
	if err != nil {
		return err
	}
	defer root.Close()
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

func hashWorktreePath(ctx context.Context, h io.Writer, baseDir, relPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	full := filepath.Join(baseDir, filepath.FromSlash(relPath))
	info, err := os.Lstat(full)
	if err != nil {
		return fmt.Errorf("%s: %w", relPath, err)
	}
	fmt.Fprintf(h, "path:%s:%d:%d:%d\x00", relPath, info.Mode(), info.Size(), info.ModTime().UnixNano())
	if info.Mode()&os.ModeSymlink != 0 {
		target, lerr := os.Readlink(full)
		if lerr != nil {
			return fmt.Errorf("%s: %w", relPath, lerr)
		}
		fmt.Fprintf(h, "link:%s\x00", target)
		return nil
	}
	if info.IsDir() {
		entries, derr := os.ReadDir(full)
		if derr != nil {
			return fmt.Errorf("%s: %w", relPath, derr)
		}
		for _, e := range entries {
			if err := hashWorktreePath(ctx, h, baseDir, pathJoinSlash(relPath, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		// Never open FIFOs, sockets, or device nodes: opening a FIFO blocks indefinitely.
		fmt.Fprintf(h, "special:%s:%d\x00", relPath, info.Mode())
		return nil
	}
	f, ferr := os.Open(full)
	if ferr != nil {
		return fmt.Errorf("%s: %w", relPath, ferr)
	}
	defer f.Close()
	if _, cerr := io.Copy(h, f); cerr != nil {
		return fmt.Errorf("%s: %w", relPath, cerr)
	}
	_, _ = io.WriteString(h, "\x00")
	return nil
}

func pathJoinSlash(a, b string) string {
	a = strings.TrimSuffix(a, "/")
	if a == "" {
		return b
	}
	return a + "/" + b
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
		staged, detached, adminDigest, rerr := inspectWorktreeAdminRecoverable(ctx, parentDir, wt, true)
		if rerr != nil {
			check.err = trimMsg(rerr.Error())
		} else {
			check.prunableStaged = staged
			check.prunableDetached = detached
			check.stateDigest = adminDigest
		}
		return wt, check
	}
	out, err := runGit(ctx, wt.path, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignored=matching")
	if err != nil {
		check.err = trimMsg(err.Error())
		return wt, check
	}
	tokens := strings.Split(string(out), "\x00")
	var contentPaths []string
	for i := 0; i < len(tokens); i++ {
		entry := tokens[i]
		if entry == "" {
			continue
		}
		if len(entry) >= 3 && (entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C') && i+1 < len(tokens) {
			orig := tokens[i+1]
			i++
			entry = entry + "\x00" + orig
		}
		if strings.HasPrefix(entry, "!! ") {
			check.ignored = true
			check.ignoredEntries = append(check.ignoredEntries, entry)
			contentPaths = append(contentPaths, entry[3:])
		} else {
			check.dirty = true
			check.dirtyEntries = append(check.dirtyEntries, entry)
			if len(entry) > 3 {
				p := entry[3:]
				if idx := strings.IndexByte(p, 0); idx >= 0 {
					p = p[:idx]
				}
				contentPaths = append(contentPaths, p)
			}
		}
	}
	slices.Sort(check.dirtyEntries)
	slices.Sort(check.ignoredEntries)
	slices.Sort(contentPaths)
	contentPaths = slices.Compact(contentPaths)

	subOut, subErr := runGit(ctx, wt.path, "submodule", "status", "--recursive")
	if subErr != nil {
		check.err = trimMsg(subErr.Error())
		return wt, check
	}
	for _, line := range bytes.Split(subOut, []byte{'\n'}) {
		if len(line) > 0 && line[0] != '-' {
			check.submodule = true
			check.submoduleEntries = append(check.submoduleEntries, string(line))
		}
	}
	slices.Sort(check.submoduleEntries)

	// Hash staged diff, unstaged diff, and dirty/untracked/ignored file contents so
	// post-confirmation edits to an already-dirty file are caught before removal.
	h := sha256.New()
	_, _ = h.Write(wt.head[:])
	for _, e := range check.dirtyEntries {
		_, _ = io.WriteString(h, "D:"+e+"\x00")
	}
	for _, e := range check.ignoredEntries {
		_, _ = io.WriteString(h, "I:"+e+"\x00")
	}
	for _, e := range check.submoduleEntries {
		_, _ = io.WriteString(h, "S:"+e+"\x00")
	}
	cachedDiff, err := runGit(ctx, wt.path, "--no-optional-locks", "diff", "--cached", "--binary")
	if err != nil {
		check.err = trimMsg(err.Error())
		return wt, check
	}
	_, _ = io.WriteString(h, "CACHED:\x00")
	_, _ = h.Write(cachedDiff)
	worktreeDiff, err := runGit(ctx, wt.path, "--no-optional-locks", "diff", "--binary")
	if err != nil {
		check.err = trimMsg(err.Error())
		return wt, check
	}
	_, _ = io.WriteString(h, "WORKTREE:\x00")
	_, _ = h.Write(worktreeDiff)
	for _, relPath := range contentPaths {
		if err := hashWorktreePath(ctx, h, wt.path, relPath); err != nil {
			check.err = trimMsg(err.Error())
			return wt, check
		}
	}
	_, reflogDetached, adminDigest, rerr := inspectWorktreeAdminRecoverable(ctx, parentDir, wt, false)
	if rerr != nil {
		check.err = trimMsg(rerr.Error())
		return wt, check
	}
	check.reflogUnreachable = reflogDetached
	_, _ = io.WriteString(h, "ADMIN:"+adminDigest+"\x00")
	check.stateDigest = hex.EncodeToString(h.Sum(nil))

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
		prevCheck.reflogUnreachable != curCheck.reflogUnreachable ||
		prevCheck.prunableStaged != curCheck.prunableStaged ||
		prevCheck.prunableDetached != curCheck.prunableDetached ||
		prevCheck.hasDraft != curCheck.hasDraft ||
		prevCheck.draftText != curCheck.draftText ||
		prevCheck.stateDigest != curCheck.stateDigest ||
		!slices.Equal(prevCheck.dirtyEntries, curCheck.dirtyEntries) ||
		!slices.Equal(prevCheck.ignoredEntries, curCheck.ignoredEntries) ||
		!slices.Equal(prevCheck.submoduleEntries, curCheck.submoduleEntries) ||
		curCheck.err != ""
}

func (a *app) worktreeCommitDraft(wtPath string) string {
	if wtPath == "" {
		return ""
	}
	if t := a.tabByPath(wtPath); t != nil {
		if strings.TrimSpace(t.commitMsg) != "" {
			return t.commitMsg
		}
	}
	for _, r := range a.preservedRepos {
		if sameWorktreePath(r.Path, wtPath) && strings.TrimSpace(r.Draft) != "" {
			return r.Draft
		}
	}
	return ""
}

func (a *app) finishWorktreeCleanup(parentRepo, removedPath, toastMsg string) {
	if removedPath != "" {
		if idx := a.tabIndex(removedPath); idx >= 0 {
			a.closeTab(idx)
		}
		a.preservedRepos = slices.DeleteFunc(a.preservedRepos, func(r savedRepo) bool {
			return sameWorktreePath(r.Path, removedPath)
		})
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
	if a.worktreeCommitDraft(wtPath) != "" {
		a.openWorktreeRemoveModal(parentRepo, repoWorktree{path: wtPath, prunable: "prunable"})
		return
	}
	w := a.startGitWrite("正在清理失效工作树")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := pruneWorktreeRecord(ctx, parentRepo, wtPath, false)
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
	draft := a.worktreeCommitDraft(wt.path)
	a.wtRemove = worktreeDialog{
		parentRepo: parentRepo,
		wt:         wt,
		check:      worktreeRemoveCheck{checking: true, locked: wt.locked, hasDraft: draft != "", draftText: draft},
		gen:        gen,
	}
	a.showWorktreeRemove = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		freshWt, check := inspectWorktreeRemoval(ctx, parentRepo, wt)
		a.post(func() {
			if a.closed || !a.showWorktreeRemove || a.wtRemove.gen != gen {
				return
			}
			curDraft := a.worktreeCommitDraft(freshWt.path)
			check.hasDraft = curDraft != ""
			check.draftText = curDraft
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
	if t := a.tabByPath(d.wt.path); t != nil && (t.commitBusy || t.anySyncBusy()) {
		a.wtRemove.err = tr("该工作树正在执行 Git 操作，请稍后再试。")
		return
	}
	curDraft := a.worktreeCommitDraft(d.wt.path)
	if (curDraft != "") != d.check.hasDraft || curDraft != d.check.draftText {
		a.wtRemove.check.hasDraft = curDraft != ""
		a.wtRemove.check.draftText = curDraft
		a.wtRemove.force = false
		a.wtRemove.err = tr("工作树状态已变化，请重新确认后再移除。")
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
		ctx, release := acquireWorktreeLanes(ctx, d.parentRepo, d.wt.path)
		defer release()
		freshWt, freshCheck := inspectWorktreeRemoval(ctx, d.parentRepo, d.wt)
		freshCheck.hasDraft = d.check.hasDraft
		freshCheck.draftText = d.check.draftText
		if worktreeRemovalCheckChanged(d.wt, freshWt, d.check, freshCheck) {
			a.post(func() {
				a.finishGitWrite(w)
				if a.closed || a.wtRemove.gen != gen {
					return
				}
				latestDraft := a.worktreeCommitDraft(freshWt.path)
				freshCheck.hasDraft = latestDraft != ""
				freshCheck.draftText = latestDraft
				a.wtRemove.busy = false
				a.wtRemove.wt = freshWt
				a.wtRemove.check = freshCheck
				a.wtRemove.force = false
				a.wtRemove.err = tr("工作树状态已变化，请重新确认后再移除。")
			})
			return
		}

		if d.wt.prunable != "" {
			err := pruneWorktreeRecordLocked(ctx, d.parentRepo, freshWt.path, d.force)
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
				a.finishWorktreeCleanup(d.parentRepo, d.wt.path, tr("已清理失效工作树记录"))
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
	hasRisk := d.check.dirty || d.check.ignored || d.check.unmerged || d.check.submodule || d.check.reflogUnreachable || d.check.prunableStaged || d.check.prunableDetached || d.check.hasDraft
	canConfirm := !d.busy && !d.check.checking && wt.locked == "" && d.check.err == "" && (!hasRisk || d.force)

	appModal(c.Key("worktree-remove-modal"), &a.showWorktreeRemove, title, 440, d.busy, func() {
		ui.Text(c, worktreeRowLabel(wt)).FontSize(13).Bold().TextColor(theme.Text)
		ui.Text(c, wt.path).FontSize(12).Font("monospace").TextColor(theme.TextMuted)
		if d.check.checking {
			ui.Text(c, tr("正在检查工作树状态…")).FontSize(12).TextColor(theme.TextMuted)
		} else if wt.locked != "" {
			msg := tr("该工作树已锁定，请先解锁后再移除。")
			if wt.locked != worktreeMarkLocked {
				msg += " (" + wt.locked + ")"
			}
			ui.Text(c, msg).FontSize(12).TextColor(theme.Danger)
		} else if d.check.err != "" {
			ui.Text(c, tr("安全检查失败，无法确认工作树是否安全移除。")).FontSize(12).TextColor(theme.Danger)
		} else if wt.prunable != "" {
			if hasRisk {
				if d.check.prunableStaged {
					ui.Text(c, tr("该失效工作树的索引中仍包含仅暂存的改动，清理后将难以恢复。")).FontSize(12).TextColor(theme.Danger)
				}
				if d.check.prunableDetached {
					ui.Text(c, tr("该失效工作树包含未在任何分支上的游离提交或 reflog 记录，清理后将难以恢复。")).FontSize(12).TextColor(theme.Warning)
				}
				if d.check.hasDraft {
					ui.Text(c, tr("应用内仍有未提交的提交说明草稿，清理后将一并清除。")).FontSize(12).TextColor(theme.Warning)
				}
				ui.Checkbox(c.Key("wt-force-remove"), &d.force, tr("强制清理（放弃索引、reflog 与草稿）"))
			} else {
				ui.Text(c, tr("该工作树目录已不存在，清理仅移除 Git 中的失效记录。")).FontSize(12).TextColor(theme.TextMuted)
			}
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
			if d.check.reflogUnreachable {
				ui.Text(c, tr("该工作树的 reflog 中仍有未在任何分支上的提交，移除后将难以恢复。")).FontSize(12).TextColor(theme.Warning)
			}
			if d.check.hasDraft {
				ui.Text(c, tr("应用内仍有未提交的提交说明草稿，移除后将一并清除。")).FontSize(12).TextColor(theme.Warning)
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
