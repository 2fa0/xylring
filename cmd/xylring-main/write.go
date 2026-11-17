package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func validateRefName(ctx context.Context, dir, name, kind string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		if kind == "tag" {
			return errors.New("请输入标签名")
		}
		return errors.New("请输入分支名")
	}
	var args []string
	if kind == "tag" {
		args = []string{"check-ref-format", "refs/tags/" + name}
	} else {
		args = []string{"check-ref-format", "--branch", name}
	}
	if _, err := runGit(ctx, dir, args...); err != nil {
		return errors.New("名称不合法")
	}
	return nil
}

func remoteLocalName(remote string) string {
	_, name, ok := strings.Cut(remote, "/")
	if !ok || name == "" || name == "HEAD" {
		return ""
	}
	return name
}

func trackingRemotes(refs []refInfo) []string {
	var out []string
	for _, ref := range refs {
		if ref.kind != refRemote {
			continue
		}
		if remoteLocalName(ref.name) == "" {
			continue
		}
		out = append(out, ref.name)
	}
	sort.Strings(out)
	return out
}

func localBranchNames(refs map[[20]byte][]refInfo) map[string]struct{} {
	out := map[string]struct{}{}
	for _, list := range refs {
		for _, ref := range list {
			if ref.kind == refBranch {
				out[ref.name] = struct{}{}
			}
		}
	}
	return out
}

func preferredTrackingRemote(remotes []string, locals map[string]struct{}) (string, int) {
	first := ""
	n := 0
	for _, remote := range remotes {
		if _, exists := locals[remoteLocalName(remote)]; exists {
			continue
		}
		n++
		if first == "" {
			first = remote
		}
	}
	return first, n
}

func createBranch(ctx context.Context, dir, name string, hash [20]byte, checkout, overwrite bool, trackRemote string) error {
	if err := validateRefName(ctx, dir, name, "branch"); err != nil {
		return err
	}
	trackRemote = strings.TrimSpace(trackRemote)
	if trackRemote != "" {
		start := "refs/remotes/" + trackRemote
		if checkout {
			flag := "-b"
			if overwrite {
				flag = "-B"
			}
			_, err := runGit(ctx, dir, "checkout", flag, name, "--track", start)
			return err
		}
		args := []string{"branch"}
		if overwrite {
			args = append(args, "-f")
		}
		args = append(args, "--track", name, start)
		_, err := runGit(ctx, dir, args...)
		return err
	}
	args := []string{"branch"}
	if overwrite {
		args = append(args, "-f")
	}
	args = append(args, name, hashHex(hash))
	if _, err := runGit(ctx, dir, args...); err != nil {
		return err
	}
	if !checkout {
		return nil
	}
	_, err := runGit(ctx, dir, "checkout", name)
	return err
}

func createTag(ctx context.Context, dir, name, message string, hash [20]byte, annotated, overwrite bool) error {
	if err := validateRefName(ctx, dir, name, "tag"); err != nil {
		return err
	}
	args := []string{"tag"}
	if overwrite {
		args = append(args, "-f")
	}
	if annotated {
		if strings.TrimSpace(message) == "" {
			return errors.New("请输入附注标签消息")
		}
		args = append(args, "-a", "-m", message)
	}
	args = append(args, "--", name, hashHex(hash))
	_, err := runGit(ctx, dir, args...)
	return err
}

func readCommitMessage(ctx context.Context, dir string, hash [20]byte) (string, error) {
	out, err := runGit(ctx, dir, "log", "-1", "--format=%B", hashHex(hash))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

type rewordInfo struct {
	author, email, date string
	tree                [20]byte
	parents             [][20]byte
	message             string
}

func readRewordInfo(ctx context.Context, dir string, hash [20]byte) (rewordInfo, error) {
	out, err := runGit(ctx, dir, "log", "-1", "--format=%an%x00%ae%x00%aI%x00%T%x00%P%x00%B", hashHex(hash))
	if err != nil {
		return rewordInfo{}, err
	}
	f := strings.SplitN(string(out), "\x00", 6)
	if len(f) != 6 {
		return rewordInfo{}, errors.New("无法读取提交说明")
	}
	tree, err := parseHash(f[3])
	if err != nil {
		return rewordInfo{}, err
	}
	var parents [][20]byte
	for _, p := range strings.Fields(f[4]) {
		h, err := parseHash(p)
		if err != nil {
			return rewordInfo{}, err
		}
		parents = append(parents, h)
	}
	return rewordInfo{
		author:  f[0],
		email:   f[1],
		date:    f[2],
		tree:    tree,
		parents: parents,
		message: strings.TrimRight(f[5], "\n"),
	}, nil
}

func rewordCommit(ctx context.Context, dir string, hash [20]byte, message string) ([20]byte, error) {
	message = strings.TrimRight(message, "\n")
	if strings.TrimSpace(message) == "" {
		return zeroHash, errors.New("说明不能为空")
	}
	info, err := readRewordInfo(ctx, dir, hash)
	if err != nil {
		return zeroHash, err
	}
	if info.message == message {
		return hash, nil
	}
	if len(info.parents) > 1 {
		return zeroHash, errors.New("不能修改合并提交的说明")
	}
	gitDir, err := rewordGitDir(ctx, dir)
	if err != nil {
		return zeroHash, err
	}
	head, err := resolveCommit(ctx, dir, "HEAD")
	if err != nil {
		return zeroHash, err
	}
	if _, err := runGit(ctx, dir, "merge-base", "--is-ancestor", hashHex(hash), hashHex(head)); err != nil {
		return zeroHash, errors.New("只能修改当前 HEAD 历史里的提交")
	}
	if head != hash {
		status, err := runGit(ctx, dir, "status", "--porcelain")
		if err != nil {
			return zeroHash, err
		}
		if len(bytes.TrimSpace(status)) > 0 {
			return zeroHash, errors.New("工作区有未提交改动，改写较早提交前请先处理它们")
		}
		merges, err := runGit(ctx, dir, "rev-list", "--merges", "--max-count=1", hashHex(hash)+".."+hashHex(head))
		if err != nil {
			return zeroHash, err
		}
		if len(bytes.TrimSpace(merges)) > 0 {
			return zeroHash, errors.New("后续历史包含合并提交，不能修改这条提交的说明")
		}
	}
	newHash, err := commitTreeMessage(ctx, dir, info, message)
	if err != nil {
		return zeroHash, err
	}
	if head == hash {
		_, err = runGit(ctx, dir, "reset", "--soft", hashHex(newHash))
		if err != nil {
			return zeroHash, err
		}
		return newHash, nil
	}
	_, err = runGit(ctx, dir, "rebase", "--no-update-refs", "--onto", hashHex(newHash), hashHex(hash))
	if err != nil {
		// A failure before rebase starts must not abort another operation.
		for _, state := range []string{"rebase-merge", "rebase-apply"} {
			original, readErr := os.ReadFile(filepath.Join(gitDir, state, "orig-head"))
			if readErr == nil && strings.TrimSpace(string(original)) == hashHex(head) {
				abortCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, _ = runGit(abortCtx, dir, "rebase", "--abort")
				cancel()
				break
			}
		}
		return zeroHash, err
	}
	return newHash, nil
}

func rewordGitDir(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDir := strings.TrimSpace(string(out))
	for _, state := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer"} {
		if _, err := os.Stat(filepath.Join(gitDir, state)); err == nil {
			return "", errors.New("仓库正在进行 rebase、合并或拣选等操作，请先完成或取消后再修改说明")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return gitDir, nil
}

func commitTreeMessage(ctx context.Context, dir string, info rewordInfo, message string) ([20]byte, error) {
	f, err := os.CreateTemp("", "xylring-msg-")
	if err != nil {
		return zeroHash, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(message + "\n"); err != nil {
		f.Close()
		return zeroHash, err
	}
	if err := f.Close(); err != nil {
		return zeroHash, err
	}
	args := []string{"commit-tree", hashHex(info.tree)}
	for _, p := range info.parents {
		args = append(args, "-p", hashHex(p))
	}
	args = append(args, "-F", path)
	env := []string{
		"GIT_AUTHOR_NAME=" + info.author,
		"GIT_AUTHOR_EMAIL=" + info.email,
		"GIT_AUTHOR_DATE=" + info.date,
	}
	out, err := runGitExtra(ctx, dir, 16<<20, env, args...)
	if err != nil {
		return zeroHash, err
	}
	return parseHash(strings.TrimSpace(string(out)))
}

func checkoutBranch(ctx context.Context, dir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请输入分支名")
	}
	_, err := runGit(ctx, dir, "checkout", name)
	return err
}

func checkoutDetached(ctx context.Context, dir string, hash [20]byte) error {
	_, err := runGit(ctx, dir, "checkout", "--detach", hashHex(hash))
	return err
}

func renameBranch(ctx context.Context, dir, oldName, newName string, overwrite bool) error {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if err := validateRefName(ctx, dir, newName, "branch"); err != nil {
		return err
	}
	flag := "-m"
	if overwrite {
		flag = "-M"
	}
	_, err := runGit(ctx, dir, "branch", flag, oldName, newName)
	return err
}

func deleteBranch(ctx context.Context, dir, name string, force bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请输入分支名")
	}
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := runGit(ctx, dir, "branch", flag, name)
	return err
}

func deleteTag(ctx context.Context, dir, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("请输入标签名")
	}
	_, err := runGit(ctx, dir, "tag", "-d", "--", name)
	return err
}
