// Package main 的 git 层只调用系统 git。
// 解析走自定义分隔符，避免受用户语言和 core.quotepath 影响。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	pageFirst    = 300
	pageMore     = 100
	maxDiffBytes = 4 << 20
	commitFormat = "%x1e%H%x1f%P%x1f%an%x1f%at%x1f%s"
)

var zeroHash [20]byte

var gitSlots = make(chan struct{}, 4)

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
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", fmt.Errorf("找不到 git，请先安装：%w", err)
	}
	return bin, nil
}

func gitEnv() []string {
	return mergeGitEnv(nil)
}

func mergeGitEnv(extra []string) []string {
	drop := map[string]bool{
		"LC_ALL": true, "LANG": true, "LC_MESSAGES": true,
		"GIT_TERMINAL_PROMPT": true, "GIT_PAGER": true, "PAGER": true,
	}
	for _, e := range extra {
		if i := strings.IndexByte(e, '='); i > 0 {
			drop[e[:i]] = true
		}
	}
	out := make([]string, 0, 32+len(extra))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 && drop[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat")
	return append(out, extra...)
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
	return runGitLimit(ctx, dir, 16<<20, args...)
}

type limitedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *limitedBuffer) Len() int       { return b.buf.Len() }
func (b *limitedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *limitedBuffer) String() string { return b.buf.String() }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.limit-b.Len() {
		p = p[:b.limit-b.Len()]
		b.overflow = true
	}
	b.buf.Write(p)
	if b.overflow && b.cancel != nil {
		b.cancel()
		return 0, errors.New("Git 输出超过读取上限")
	}
	return n, nil
}

func runGitLimit(ctx context.Context, dir string, limit int, args ...string) ([]byte, error) {
	return runGitExtra(ctx, dir, limit, nil, args...)
}

func runGitExtra(ctx context.Context, dir string, limit int, extraEnv []string, args ...string) ([]byte, error) {
	if err := acquireGit(ctx); err != nil {
		return nil, err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, bin, gitArgs(args...)...)
	hideConsoleWindow(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = mergeGitEnv(extraEnv)
	stdout := limitedBuffer{limit: limit, cancel: cancel}
	stderr := limitedBuffer{limit: 64 << 10}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if stdout.overflow {
		return nil, fmt.Errorf("Git 输出超过 %d 字节，请缩小读取范围", limit)
	}
	if ctx.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("git 超时或已取消：%w", ctx.Err())
	}
	// git diff --no-index returns 1 for a successful comparison with changes.
	if err != nil && len(args) > 1 && args[0] == "diff" && args[1] == "--no-index" {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && stderr.Len() == 0 {
			return stdout.Bytes(), nil
		}
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
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("找不到这个仓库")
		}
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("不是 Git 仓库")
	}
	out, err := runGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(err.Error(), "找不到 git") {
			return "", err
		}
		return "", errors.New("不是 Git 仓库")
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", errors.New("不是 Git 仓库")
	}
	return root, nil
}

func skipRepoDir(name string) bool {
	switch name {
	case ".git", "node_modules":
		return true
	default:
		return false
	}
}

func isGitWorkTree(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}

// findGitRepos 找出 dir 及其子目录里的 Git 工作区。
// 目录本身不是仓库、下面也没有时，再按当前路径向上找一次。
func findGitRepos(ctx context.Context, dir string) ([]string, error) {
	roots, _, err := scanGitRepos(ctx, dir, nil)
	return roots, err
}

// streamCommits 按日期顺序读取全部分支、远程和标签，外加当前 HEAD。
// 已画出的图谱靠 skip 续页，不重排前面的泳道。
func streamCommits(ctx context.Context, dir string, skip, n int, fn func(commit) error) error {
	tips, err := historyTips(ctx, dir)
	if err != nil {
		return err
	}
	return streamHistory(ctx, dir, historyQuery{}, tips, skip, n, fn)
}

func streamHistory(ctx context.Context, dir string, query historyQuery, tips []string, skip, n int, fn func(commit) error) error {
	if len(tips) == 0 {
		return nil
	}
	if query.filePath != "" {
		return streamFileHistory(ctx, dir, query, skip, n, fn)
	}
	args := []string{
		"log", "--date-order", "--no-color",
		"--format=" + commitFormat,
		"--skip=" + strconv.Itoa(skip),
	}
	if n > 0 {
		args = append(args, "--max-count="+strconv.Itoa(n))
	}
	args = append(args, query.searchArgs()...)
	args = append(args, "--stdin", "--")
	if query.term != "" && query.kind == "文件路径" {
		args = append(args, searchPathspec(query.term))
	}
	return streamLog(ctx, dir, args, strings.Join(tips, "\n")+"\n", splitByte(0x1e), func(sc *bufio.Scanner, slot *gitLease) error {
		for sc.Scan() {
			rec := strings.TrimRight(sc.Text(), "\r\n")
			if rec == "" {
				continue
			}
			c, err := parseCommitRecord(rec)
			if err != nil {
				return err
			}
			if err := slot.yield(ctx, func() error { return fn(c) }); err != nil {
				return err
			}
		}
		return sc.Err()
	})
}

// A paused history reader must not reserve a Git slot while waiting for a page.
type gitLease struct{ held bool }

func (s *gitLease) acquire(ctx context.Context) error {
	if err := acquireGit(ctx); err != nil {
		return err
	}
	s.held = true
	return nil
}

func (s *gitLease) release() {
	if s.held {
		<-gitSlots
		s.held = false
	}
}

func (s *gitLease) yield(ctx context.Context, fn func() error) error {
	s.release()
	if err := fn(); err != nil {
		return err
	}
	return s.acquire(ctx)
}

func streamLog(ctx context.Context, dir string, args []string, input string, split bufio.SplitFunc, scan func(*bufio.Scanner, *gitLease) error) error {
	slot := &gitLease{}
	if err := slot.acquire(ctx); err != nil {
		return err
	}
	defer slot.release()
	bin, err := gitPath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, gitArgs(args...)...)
	hideConsoleWindow(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(input)
	stderr := limitedBuffer{limit: 64 << 10}
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// A descendant inheriting stdout must not keep the scanner blocked after
	// cancellation, before Wait can apply its pipe-drain deadline.
	stopRead := context.AfterFunc(ctx, func() { _ = stdout.Close() })
	defer stopRead()
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 2<<20)
	sc.Split(split)
	parseErr := scan(sc, slot)
	if parseErr == nil {
		parseErr = sc.Err()
	}
	if parseErr != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if parseErr != nil {
		return parseErr
	}
	if ctx.Err() != nil {
		return fmt.Errorf("git 超时或已取消：%w", ctx.Err())
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		if strings.Contains(msg, "does not have any commits") {
			return nil
		}
		return errors.New(trimMsg(msg))
	}
	return nil
}

func historyTips(ctx context.Context, dir string) ([]string, error) {
	meta, err := loadMeta(ctx, dir)
	if err != nil {
		return nil, err
	}
	return queryTips(ctx, dir, meta, historyQuery{})
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
	branch      string
	head        [20]byte
	refs        map[[20]byte][]refInfo
	ahead       int
	behind      int
	upstream    string
	hasUpstream bool
}

func loadMeta(ctx context.Context, dir string) (repoMeta, error) {
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
	sync, _ := queryTrackingStatus(ctx, dir)
	m.hasUpstream = sync.hasUpstream
	m.upstream = sync.upstream
	m.ahead = sync.ahead
	m.behind = sync.behind
	rawRefs, err := runGit(ctx, dir, "for-each-ref", "--format=%(objectname) %(refname) %(*objectname)")
	if err != nil {
		return m, err
	}
	m.refs = parseRefs(rawRefs)
	return m, nil
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
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].kind != list[j].kind {
				return list[i].kind < list[j].kind
			}
			return list[i].name < list[j].name
		})
		refs[h] = list
	}
	return refs
}

func resolveCommit(ctx context.Context, dir, ref string) ([20]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return zeroHash, errors.New("请输入分支、标签或哈希")
	}
	out, err := runGit(ctx, dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		if missingRevision(err) {
			return zeroHash, fmt.Errorf("找不到「%s」这个提交、分支或标签", ref)
		}
		return zeroHash, err
	}
	return parseHash(string(out))
}

func missingRevision(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Needed a single revision") ||
		strings.Contains(s, "unknown revision") ||
		strings.Contains(s, "Not a valid object") ||
		strings.Contains(s, "bad revision") ||
		strings.Contains(s, "ambiguous argument") ||
		strings.Contains(s, "invalid object name")
}

func mergeBase(ctx context.Context, dir string, a, b [20]byte) ([20]byte, error) {
	out, err := runGit(ctx, dir, "merge-base", hashHex(a), hashHex(b))
	if err != nil {
		return zeroHash, err
	}
	return parseHash(string(out))
}

type fileChange struct {
	status         string
	path           string
	oldPath        string
	extra          string
	added, deleted int
	binary         bool
	hasStat        bool

	worktreeVersion string
}

func (f fileChange) label() string {
	name := displayPath(f.path)
	if f.oldPath != "" && f.oldPath != f.path {
		name = displayPath(f.oldPath) + " → " + displayPath(f.path)
	}
	word := f.extra
	if word == "" {
		word = statusLetter(f.status)
		if word == "?" {
			word = ""
		}
	}
	if word == "" {
		return name
	}
	return word + "  " + name
}

func (f fileChange) fullPath() string {
	if f.oldPath != "" && f.oldPath != f.path {
		return f.oldPath + " → " + f.path
	}
	return f.path
}

func statusWord(st string) string {
	switch statusLetter(st) {
	case "M":
		return "修改"
	case "A":
		return "新增"
	case "D":
		return "删除"
	case "R":
		return "重命名"
	case "C":
		return "复制"
	case "U":
		return "冲突"
	case "T":
		return "类型变更"
	default:
		return statusLetter(st)
	}
}

func displayPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.Split(p, "/")
	if len(parts) <= 3 {
		return p
	}
	return parts[0] + "/…/" + parts[len(parts)-1]
}

func compareFiles(ctx context.Context, dir, from, to string) ([]fileChange, error) {
	return loadFileChanges(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--name-status", "-M", "-z", from, to, "--")
}

// commitFiles 是一条提交相对第一父提交的文件列表。没有父提交时相对空树。
func commitFiles(ctx context.Context, dir string, hash [20]byte, parents [][20]byte) ([]fileChange, error) {
	if len(parents) == 0 {
		return loadFileChanges(ctx, dir, "diff-tree", "-r", "--root", "--no-commit-id", "--no-ext-diff", "--no-textconv", "--name-status", "-M", "-z", hashHex(hash), "--")
	}
	return compareFiles(ctx, dir, hashHex(parents[0]), hashHex(hash))
}

// loadFileChanges 先读 --name-status，再用同一组参数读 --numstat，补上增减行数。
func loadFileChanges(ctx context.Context, dir string, args ...string) ([]fileChange, error) {
	out, err := runGit(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	files := parseNameStatusZ(out)
	statArgs := append([]string(nil), args...)
	for i, a := range statArgs {
		if a != "--name-status" {
			continue
		}
		statArgs[i] = "--numstat"
		if statOut, err := runGit(ctx, dir, statArgs...); err == nil {
			applyNumStat(files, parseNumStatZ(statOut))
		}
		break
	}
	return files, nil
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

type fileStat struct {
	path, oldPath  string
	added, deleted int
	binary         bool
}

// parseNumStatZ 解析 git diff --numstat -z。
// 普通文件是 增 TAB 删 TAB 路径 NUL。
// 重命名是 增 TAB 删 TAB NUL 旧路径 NUL 新路径 NUL。
// 二进制是 - TAB - TAB 路径 NUL。
func parseNumStatZ(b []byte) []fileStat {
	var out []fileStat
	for i := 0; i < len(b); {
		if b[i] == 0 {
			i++
			continue
		}
		n := bytes.IndexByte(b[i:], 0)
		if n < 0 {
			break
		}
		prefix := b[i : i+n]
		i += n + 1
		tab1 := bytes.IndexByte(prefix, '\t')
		if tab1 < 0 {
			continue
		}
		tab2 := bytes.IndexByte(prefix[tab1+1:], '\t')
		if tab2 < 0 {
			continue
		}
		tab2 += tab1 + 1
		addedStr := string(prefix[:tab1])
		deletedStr := string(prefix[tab1+1 : tab2])
		rest := prefix[tab2+1:]
		st := fileStat{}
		switch {
		case addedStr == "-" && deletedStr == "-":
			st.binary = true
		default:
			var err1, err2 error
			st.added, err1 = strconv.Atoi(addedStr)
			st.deleted, err2 = strconv.Atoi(deletedStr)
			if err1 != nil || err2 != nil {
				continue
			}
		}
		if len(rest) == 0 {
			if i >= len(b) {
				break
			}
			n1 := bytes.IndexByte(b[i:], 0)
			if n1 < 0 {
				break
			}
			st.oldPath = string(b[i : i+n1])
			i += n1 + 1
			n2 := bytes.IndexByte(b[i:], 0)
			if n2 < 0 {
				st.path = string(b[i:])
				i = len(b)
			} else {
				st.path = string(b[i : i+n2])
				i += n2 + 1
			}
		} else {
			st.path = string(rest)
		}
		if st.path == "" {
			continue
		}
		out = append(out, st)
	}
	return out
}

func applyNumStat(files []fileChange, stats []fileStat) {
	byPath := make(map[string]int, len(files))
	byRename := make(map[string]int)
	for i, f := range files {
		byPath[f.path] = i
		if f.oldPath != "" {
			byRename[f.oldPath+"\x00"+f.path] = i
		}
	}
	for _, s := range stats {
		i, ok := -1, false
		if s.oldPath != "" {
			i, ok = byRename[s.oldPath+"\x00"+s.path]
		}
		if !ok {
			i, ok = byPath[s.path]
		}
		if !ok {
			continue
		}
		files[i].added = s.added
		files[i].deleted = s.deleted
		files[i].binary = s.binary
		files[i].hasStat = true
	}
}

func statusLetter(st string) string {
	for _, r := range st {
		if r != ' ' && r != '.' {
			return string(r)
		}
	}
	return "?"
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

func missingBlob(err error) bool {
	s := err.Error()
	return strings.Contains(s, "does not exist") ||
		strings.Contains(s, "exists on disk") ||
		strings.Contains(s, "Not a valid object") ||
		strings.Contains(s, "bad file")
}
