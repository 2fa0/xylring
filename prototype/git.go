// Package main 的 git 层只调用系统 git，不使用 go-git。
// 解析走自定义分隔符和 -z，避免受用户语言和 core.quotepath 影响。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	pageFirst    = 300
	pageMore     = 100
	maxDiffBytes = 4 << 20
	commitFormat = "%x1e%H%x1f%P%x1f%an%x1f%at%x1f%s"
)

var zeroHash [20]byte

var (
	gitMu       sync.RWMutex
	gitOverride string
	gitSlots    = make(chan struct{}, 4)
)

func acquireGit(ctx context.Context) error {
	select {
	case gitSlots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gitSlots
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func gitPath() (string, error) {
	gitMu.RLock()
	name := gitOverride
	gitMu.RUnlock()
	if name == "" {
		name = "git"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("找不到 git，请先安装或在设置中指定路径：%w", err)
	}
	return bin, nil
}

func gitEnv() []string {
	out := make([]string, 0, 32)
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "LC_ALL="),
			strings.HasPrefix(e, "LANG="),
			strings.HasPrefix(e, "LC_MESSAGES="),
			strings.HasPrefix(e, "GIT_TERMINAL_PROMPT="),
			strings.HasPrefix(e, "GIT_PAGER="),
			strings.HasPrefix(e, "PAGER="):
			continue
		}
		out = append(out, e)
	}
	return append(out, "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat")
}

func gitArgs(args ...string) []string {
	full := []string{
		"--no-pager",
		"-c", "core.quotepath=false",
		"-c", "log.showSignature=false",
		"-c", "i18n.logOutputEncoding=UTF-8",
		"-c", "core.pager=cat",
	}
	return append(full, args...)
}

// runGit 在 dir 里执行 git。失败时错误文本是 git 的 stderr。
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if err := acquireGit(ctx); err != nil {
		return nil, err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, gitArgs(args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("git 超时或已取消：%w", ctx.Err())
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), errors.New(trimMsg(msg))
	}
	return stdout.Bytes(), nil
}

func trimMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}

func repoRoot(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("不是 Git 仓库：%s", err.Error())
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("不是 Git 仓库")
	}
	return root, nil
}

// streamCommits 边读边解析 git log，不把整段输出收成一个字符串。
func streamCommits(ctx context.Context, dir string, skip, n int, fn func(commit) error) error {
	return streamHistory(ctx, dir, skip, n, nil, fn)
}

func streamHistory(ctx context.Context, dir string, skip, n int, refs []string, fn func(commit) error) error {
	return streamLog(ctx, dir, skip, n, logOptions{Refs: refs}, fn)
}

type logOptions struct {
	Refs        []string
	Path, Query string
	Names       map[[20]byte][]refInfo
}

func streamLog(ctx context.Context, dir string, skip, n int, options logOptions, fn func(commit) error) error {
	// --revs-only 在 unborn HEAD 上返回空输出，同时保留 detached HEAD 的提交。
	tips, err := historyTips(ctx, dir, options.Refs)
	if err != nil {
		return err
	}
	if len(tips) == 0 {
		return nil
	}
	if err := acquireGit(ctx); err != nil {
		return err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return err
	}
	format := commitFormat
	if options.Path != "" || options.Query != "" {
		format += "%x00"
	}
	if options.Query != "" {
		format += "%B%n%ae%n%ce%x00"
	}
	args := gitArgs("log", "--date-order", "--no-color", "--format="+format)
	if options.Path == "" && options.Query == "" {
		args = append(args, "--skip="+strconv.Itoa(skip), "--max-count="+strconv.Itoa(n))
	}
	if options.Path != "" {
		args = append(args, "--follow", "--name-status", "-z")
	}
	args = append(args, tips...)
	args = append(args, "--")
	if options.Path != "" {
		args = append(args, options.Path)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	sc.Split(splitByte(0x1e))
	var parseErr error
	path := options.Path
	seen, emitted := 0, 0
	stopped := false
	for sc.Scan() {
		rec := strings.TrimRight(sc.Text(), "\r\n")
		if rec == "" {
			continue
		}
		header, rest, _ := strings.Cut(rec, "\x00")
		body := ""
		if options.Query != "" {
			body, rest, _ = strings.Cut(rest, "\x00")
		}
		c, err := parseCommitRecord(header)
		if err != nil {
			parseErr = err
			break
		}
		if options.Path != "" {
			c.historyPath = path
			for _, change := range parseNameStatusZ([]byte(strings.TrimLeft(rest, "\x00\r\n"))) {
				if change.status == "R" && change.path == path {
					path = change.oldPath
				}
			}
		}
		if options.Query != "" {
			q := strings.ToLower(options.Query)
			text := strings.ToLower(body + "\n" + c.author + "\n" + c.whenText() + "\n" + hashHex(c.hash))
			for _, ref := range options.Names[c.hash] {
				text += "\n" + strings.ToLower(ref.name)
			}
			if !strings.Contains(text, q) {
				continue
			}
		}
		if options.Path != "" || options.Query != "" {
			seen++
			if seen <= skip {
				continue
			}
		}
		if err := fn(c); err != nil {
			parseErr = err
			break
		}
		emitted++
		if (options.Path != "" || options.Query != "") && emitted >= n {
			stopped = true
			break
		}
	}
	if parseErr == nil {
		parseErr = sc.Err()
	}
	if (parseErr != nil || stopped) && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if ctx.Err() != nil {
		return fmt.Errorf("git 超时或已取消：%w", ctx.Err())
	}
	if stopped {
		return nil
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return errors.New(trimMsg(msg))
	}
	return nil
}

func historyTips(ctx context.Context, dir string, refs []string) ([]string, error) {
	var tips []string
	if len(refs) == 0 {
		tips = []string{"--branches", "--remotes", "--tags"}
		refs = []string{"HEAD"}
	}
	for _, ref := range refs {
		if ref == "HEAD" {
			head, err := runGit(ctx, dir, "rev-parse", "--revs-only", "HEAD")
			if err != nil {
				return nil, err
			}
			if h := strings.TrimSpace(string(head)); h != "" {
				tips = append(tips, h)
			}
		} else {
			// refs come from for-each-ref or resolveCommit, never raw user input.
			tips = append(tips, ref)
		}
	}
	return tips, nil
}

func resolveCommit(ctx context.Context, dir, ref string) ([20]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return zeroHash, errors.New("请输入 ref 或 SHA")
	}
	out, err := runGit(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return zeroHash, err
	}
	return parseHash(string(out))
}

type commitInfo struct {
	author, authorEmail, authorDate          string
	committer, committerEmail, committerDate string
	message                                  string
}

func loadCommitInfo(ctx context.Context, dir, hash string) (*commitInfo, error) {
	out, err := runGit(ctx, dir, "show", "-s", "--no-color", "--format=%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B", hash, "--")
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(string(out), "\x00", 7)
	if len(f) != 7 {
		return nil, errors.New("无法解析提交详情")
	}
	return &commitInfo{
		author: f[0], authorEmail: f[1], authorDate: f[2],
		committer: f[3], committerEmail: f[4], committerDate: f[5],
		message: strings.TrimRight(f[6], "\n"),
	}, nil
}

func compareFiles(ctx context.Context, dir, from, to string) ([]fileChange, error) {
	out, err := runGit(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-M", "-z", from, to, "--")
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(out), nil
}

func splitByte(sep byte) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, sep); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

func parseCommitRecord(rec string) (commit, error) {
	fields := strings.Split(rec, "\x1f")
	if len(fields) < 5 {
		return commit{}, errors.New("无法解析提交记录")
	}
	hash, err := parseHash(fields[0])
	if err != nil {
		return commit{}, err
	}
	var parents [][20]byte
	if strings.TrimSpace(fields[1]) != "" {
		for _, p := range strings.Fields(fields[1]) {
			h, err := parseHash(p)
			if err != nil {
				return commit{}, err
			}
			parents = append(parents, h)
		}
	}
	when, err := strconv.ParseInt(strings.TrimSpace(fields[3]), 10, 64)
	if err != nil {
		return commit{}, errors.New("无法解析提交时间")
	}
	return commit{
		hash:    hash,
		parents: parents,
		author:  fields[2],
		when:    when,
		subject: strings.Join(fields[4:], "\x1f"),
	}, nil
}

func parseHash(s string) ([20]byte, error) {
	var h [20]byte
	s = strings.TrimSpace(s)
	if len(s) != 40 {
		return h, fmt.Errorf("无法解析哈希 %q", s)
	}
	_, err := hex.Decode(h[:], []byte(s))
	if err != nil {
		return h, fmt.Errorf("无法解析哈希 %q", s)
	}
	return h, nil
}

func hashHex(h [20]byte) string { return hex.EncodeToString(h[:]) }

func shortHash(h [20]byte) string { return hashHex(h)[:7] }

type refKind uint8

const (
	refBranch refKind = iota
	refRemote
	refTag
)

type refInfo struct {
	name string
	kind refKind
}

type repoMeta struct {
	stashes []commit
	branch  string
	head    [20]byte
	dirty   int
	refs    map[[20]byte][]refInfo
}

func loadMeta(ctx context.Context, dir string, includeUntracked ...bool) (repoMeta, error) {
	var m repoMeta
	m.refs = map[[20]byte][]refInfo{}
	if branch, err := runGit(ctx, dir, "symbolic-ref", "-q", "--short", "HEAD"); err == nil {
		m.branch = strings.TrimSpace(string(branch))
	} else if short, serr := runGit(ctx, dir, "rev-parse", "--short", "HEAD"); serr == nil {
		m.branch = "分离头指针 " + strings.TrimSpace(string(short))
	} else {
		m.branch = "尚无提交"
	}
	if head, err := runGit(ctx, dir, "rev-parse", "HEAD"); err == nil {
		if h, herr := parseHash(string(head)); herr == nil {
			m.head = h
		}
	}
	status, err := statusFiles(ctx, dir, len(includeUntracked) > 0 && includeUntracked[0])
	if err != nil {
		return m, err
	}
	m.dirty = len(status)
	rawRefs, err := runGit(ctx, dir, "for-each-ref", "--format=%(objectname) %(refname) %(*objectname)")
	if err != nil {
		return m, err
	}
	m.refs = parseRefs(rawRefs)
	m.stashes, err = loadStashes(ctx, dir)
	return m, err
}

func parseRefs(raw []byte) map[[20]byte][]refInfo {
	refs := map[[20]byte][]refInfo{}
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := f[1]
		var kind refKind
		var label string
		switch {
		case strings.HasPrefix(name, "refs/heads/"):
			kind, label = refBranch, name[len("refs/heads/"):]
		case strings.HasPrefix(name, "refs/remotes/"):
			kind, label = refRemote, name[len("refs/remotes/"):]
		case strings.HasPrefix(name, "refs/tags/"):
			kind, label = refTag, name[len("refs/tags/"):]
		default:
			continue
		}
		hashStr := f[0]
		if len(f) >= 3 && len(f[2]) == 40 {
			hashStr = f[2]
		}
		h, err := parseHash(hashStr)
		if err != nil {
			continue
		}
		refs[h] = append(refs[h], refInfo{name: label, kind: kind})
	}
	for h, list := range refs {
		sortRefs(list)
		refs[h] = list
	}
	return refs
}

func sortRefs(list []refInfo) {
	sort.SliceStable(list, func(i, j int) bool { return refLess(list[i], list[j]) })
}

func refLess(a, b refInfo) bool {
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	return a.name < b.name
}

type fileChange struct {
	layer, oldRev, newRev string
	status                string
	path                  string
	oldPath               string
}

func commitFiles(ctx context.Context, dir, hash string) ([]fileChange, error) {
	out, err := runGit(ctx, dir,
		"diff-tree", "-m", "--first-parent", "--root", "-M", "-r", "-z",
		"--name-status", "--no-commit-id", hash,
	)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(out), nil
}

func uncommittedFiles(ctx context.Context, dir string) ([]fileChange, error) {
	return statusFiles(ctx, dir, false)
}

func statusFiles(ctx context.Context, dir string, untracked bool) ([]fileChange, error) {
	flag := "--untracked-files=no"
	if untracked {
		flag = "--untracked-files=all"
	}
	out, err := runGit(ctx, dir, "status", "--porcelain=v1", "-z", flag)
	if err != nil {
		return nil, err
	}
	return parsePorcelainZ(out), nil
}

// parseNameStatusZ 解析 git diff --name-status -z。
// 重命名是 R100\0旧路径\0新路径\0。
func parseNameStatusZ(b []byte) []fileChange {
	parts := bytes.Split(b, []byte{0})
	var out []fileChange
	for i := 0; i < len(parts); {
		if len(parts[i]) == 0 {
			i++
			continue
		}
		st := string(parts[i])
		i++
		if i >= len(parts) {
			break
		}
		first := string(parts[i])
		i++
		ch := fileChange{status: statusLetter(st), path: first}
		if st != "" && (st[0] == 'R' || st[0] == 'C') {
			if i < len(parts) {
				ch.oldPath = first
				ch.path = string(parts[i])
				i++
			}
		}
		out = append(out, ch)
	}
	return out
}

// parsePorcelainZ 解析 git status --porcelain=v1 -z。
// 重命名的顺序是「新路径\0旧路径\0」。
func parsePorcelainZ(b []byte) []fileChange {
	parts := bytes.Split(b, []byte{0})
	var out []fileChange
	for i := 0; i < len(parts); {
		if len(parts[i]) == 0 {
			i++
			continue
		}
		p := parts[i]
		i++
		if len(p) < 4 {
			continue
		}
		xy := string(p[:2])
		ch := fileChange{status: statusLetter(xy), path: string(p[3:])}
		if xy == "??" {
			ch.layer = "未跟踪"
		}
		if strings.ContainsAny(xy, "RC") {
			if i < len(parts) {
				ch.oldPath = string(parts[i])
				i++
			}
		}
		out = append(out, ch)
	}
	return out
}

func statusLetter(st string) string {
	switch st {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return "U"
	}
	for _, r := range st {
		if r != ' ' && r != '.' {
			return string(r)
		}
	}
	return "?"
}

func (f fileChange) label() string {
	prefix := ""
	if f.layer != "" {
		prefix = "[" + f.layer + "] "
	}
	if f.oldPath != "" && f.oldPath != f.path {
		return prefix + f.status + "  " + f.oldPath + " → " + f.path
	}
	return prefix + f.status + "  " + f.path
}

// readBlob 读取 rev:path。文件不存在时返回空内容。过大或二进制时 note 非空。
func readBlob(ctx context.Context, dir, rev, path string) ([]byte, string, error) {
	if rev == "" || path == "" {
		return nil, "", nil
	}
	spec := rev + ":" + path
	sizeOut, err := runGit(ctx, dir, "cat-file", "-s", spec)
	if err != nil {
		if missingBlob(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	size, _ := strconv.ParseInt(strings.TrimSpace(string(sizeOut)), 10, 64)
	if size > maxDiffBytes {
		return nil, fmt.Sprintf("文件过大（%d 字节），未载入", size), nil
	}
	data, err := runGit(ctx, dir, "cat-file", "blob", spec)
	if err != nil {
		if missingBlob(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Sprintf("二进制文件（%d 字节）", len(data)), nil
	}
	return data, "", nil
}

func readWorktree(dir, rel string) ([]byte, string, error) {
	full, err := withinRepo(dir, rel)
	if err != nil {
		return nil, "", err
	}
	st, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		return []byte(target), "", err
	}
	if st.Size() > maxDiffBytes {
		return nil, fmt.Sprintf("文件过大（%d 字节），未载入", st.Size()), nil
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDiffBytes+1))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if len(data) > maxDiffBytes {
		return nil, "文件读取过程中超过大小限制，未载入", nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Sprintf("二进制文件（%d 字节）", len(data)), nil
	}
	return data, "", nil
}

func withinRepo(dir, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("非法路径 %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("非法路径 %q", rel)
	}
	return filepath.Join(dir, clean), nil
}

func missingBlob(err error) bool {
	s := err.Error()
	return strings.Contains(s, "does not exist") ||
		strings.Contains(s, "exists on disk") ||
		strings.Contains(s, "Not a valid object") ||
		strings.Contains(s, "bad file")
}
