package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// requireDockerExec 让依赖「容器命令真的能跑」的测试只在 macOS/Linux 上运行；
// Windows 上容器仓库入口本身关闭，CI 仍会跑身份与命令构造的纯逻辑测试。
func requireDockerExec(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Docker exec tests are not run on Windows")
	}
}

const fakeDockerContainerID = "abababababababababababababababababababababababababababababababab"

// fakeDockerScript 让「容器」变成测试里的一个真实本机仓库：管理命令回答固定结果，
// exec 则落到 FAKE_REPO 上执行。这样命令构造、输出解析、读写链路都能端到端验证，
// 又不需要测试机连 Docker daemon。
// fakeDockerScript 在测试机是 macOS 时还需要一个 GNU 风格的 stat；
// 真实容器是 Linux（GNU coreutils / busybox），这里只为让同一套脚本跑在本机。
const fakeStatShim = `#!/bin/bash
if [ "$1" != "-c" ]; then exec /usr/bin/stat "$@"; fi
shift 2
[ "$1" = "--" ] && shift
for p in "$@"; do
  info=$(/usr/bin/stat -f '%z %m %d %i %p' "$p" 2>/dev/null) || exit 1
  echo "$info"
done
`

const fakeDockerScript = `#!/bin/bash
log="${FAKE_DOCKER_LOG:-/dev/null}"
{
  printf 'ARGV'
  for a in "$@"; do printf '\001%s' "$a"; done
  printf '\n'
} >> "$log"
if [ "$1" = "--version" ]; then echo "Docker version 27.0.0"; exit 0; fi
if [ "$1" = "context" ]; then
  case "$2" in
    show) echo "${FAKE_CONTEXT_NAME:-testctx}" ;;
    inspect) printf '[{"Name":"%s","Endpoints":{"docker":{"Host":"%s"}}}]\n' "$3" "${FAKE_ENDPOINT:-unix:///tmp/fake-docker.sock}" ;;
  esac
  exit 0
fi
if [ "$1" = "--context" ] || [ "$1" = "--host" ]; then
  shift 2
  case "$1" in
    ps)
      printf '%s\t%s\t%s\n' "$FAKE_CONTAINER_ID" "demo-app" "fake/image:latest"
      exit 0 ;;
    inspect)
      printf '{"Id":"%s","Name":"/demo-app","Platform":"linux","Config":{"Image":"fake/image:latest"},"State":{"Status":"running","Running":true,"Paused":false}}\n' "$FAKE_CONTAINER_ID"
      exit 0 ;;
    exec) shift ;;
  esac
fi
if [ "$1" = "exec" ]; then shift; fi
while [ $# -gt 0 ]; do
  case "$1" in
    -i|-t) shift ;;
    --workdir) WD=$2; shift 2 ;;
    --env) export "$2"; shift 2 ;;
    *) break ;;
  esac
done
shift
if [ "$1" = "env" ]; then
  shift
  while [ "$1" = "-u" ]; do unset "$2"; shift 2; done
  while [ $# -gt 0 ]; do
    case "$1" in *=*) export "$1"; shift ;; *) break ;; esac
  done
fi
cd "${WD:-$FAKE_REPO}" || exit 1
if [ "$(uname)" = "Darwin" ] && [ "$1" = "sh" ] && [ "$2" = "-c" ]; then
  script=${3//\/proc\/\$\$\/fd\/3/\/dev\/fd\/3}
  shift 3
  exec sh -c "$script" "$@"
fi
exec "$@"
`

type fakeDockerEnv struct {
	key     string
	logPath string
}

func setupFakeDocker(t *testing.T, repoPath string) fakeDockerEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// 让容器脚本里的 `stat -c` 在 macOS 上也能工作。
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(fakeStatShim), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Fatal(err)
		}
		shim := "#!" + python + "\n" + `import os,sys,fcntl
p=sys.argv[-1]
if p == '/dev/fd/3':
 print(fcntl.fcntl(3, 50, bytes(1024)).split(bytes([0]))[0].decode())
else:
 os.execv('/usr/bin/readlink', ['readlink']+sys.argv[1:])
`
		if err := os.WriteFile(filepath.Join(dir, "readlink"), []byte(shim), 0755); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(dir, "argv.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_REPO", repoPath)
	t.Setenv("FAKE_CONTAINER_ID", fakeDockerContainerID)
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return filepath.Join(dir, "config"), nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	resetDockerResolution()
	t.Cleanup(resetDockerResolution)
	target := repoTarget{
		kind:      targetDocker,
		context:   "testctx",
		endpoint:  "unix:///tmp/fake-docker.sock",
		container: fakeDockerContainerID,
		path:      repoPath,
	}
	return fakeDockerEnv{key: dockerRepoKey(target), logPath: logPath}
}

func readFakeDockerLog(t *testing.T, path string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var calls [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if !strings.HasPrefix(line, "ARGV") {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(line, "ARGV"), "\x01")
		if len(parts) > 0 && parts[0] == "" {
			parts = parts[1:]
		}
		calls = append(calls, parts)
	}
	return calls
}

func TestDockerRepoKeyRoundTrip(t *testing.T) {
	paths := []string{
		"/workspace/api",
		"/workspace/中文 目录/100% 项目",
		"/srv/a b/c#d?e",
	}
	for _, p := range paths {
		target := repoTarget{
			kind:      targetDocker,
			context:   "desktop-linux",
			endpoint:  "unix:///var/run/docker.sock",
			container: fakeDockerContainerID,
			path:      p,
		}
		key := dockerRepoKey(target)
		got, ok := parseDockerRepoKey(key)
		if !ok {
			t.Fatalf("cannot parse key for %q", p)
		}
		if got.path != p || got.context != target.context || got.endpoint != target.endpoint || got.container != target.container {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if canonicalRepoDir(key) != key {
			t.Fatalf("docker key must stay the same identity: %q -> %q", key, canonicalRepoDir(key))
		}
	}
}

func TestDockerRepoKeySeparatesTargets(t *testing.T) {
	base := repoTarget{kind: targetDocker, context: "c", endpoint: "unix:///a.sock", container: fakeDockerContainerID, path: "/workspace/api"}
	other := base
	other.container = strings.Repeat("cd", 32)
	if dockerRepoKey(base) == dockerRepoKey(other) {
		t.Fatal("different containers must not share an identity")
	}
	other = base
	other.endpoint = "unix:///b.sock"
	if dockerRepoKey(base) == dockerRepoKey(other) {
		t.Fatal("different endpoints must not share an identity")
	}
	other = base
	other.context = "other"
	if dockerRepoKey(base) == dockerRepoKey(other) {
		t.Fatal("different docker contexts must not share an identity")
	}
	// 显示名不参与身份：容器改名不能让仓库变成另一个。
	named := base
	named.name = "renamed"
	if dockerRepoKey(base) != dockerRepoKey(named) {
		t.Fatal("container name must not affect identity")
	}
	// 同一个路径在不同容器里是两个仓库。
	local := "/workspace/api"
	if repoContains(dockerRepoKey(base), local) || repoContains(local, dockerRepoKey(base)) {
		t.Fatal("local and container paths must not nest")
	}
}

func TestDockerRepoPathNormalization(t *testing.T) {
	cases := map[string]string{
		"/workspace/api/":       "/workspace/api",
		"/workspace/./api":      "/workspace/api",
		"/workspace/lib/../api": "/workspace/api",
		"/workspace//api":       "/workspace/api",
	}
	for in, want := range cases {
		got, err := normalizeContainerRepoPath(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "workspace/api", "./api", "/"} {
		if got, err := normalizeContainerRepoPath(bad); err == nil {
			t.Fatalf("accepted %q as %q", bad, got)
		}
	}
}

func TestDockerRepoKeyRejectsBrokenData(t *testing.T) {
	target := repoTarget{kind: targetDocker, context: "c", endpoint: "unix:///a.sock", container: fakeDockerContainerID, path: "/workspace/api"}
	key := dockerRepoKey(target)
	cases := []string{
		"",
		"/workspace/api",
		dockerRepoKeyPrefix,
		dockerRepoKeyPrefix + "!!!",
		key[:len(key)-4],
	}
	for _, c := range cases {
		if _, ok := parseDockerRepoKey(c); ok {
			t.Fatalf("accepted broken key %q", c)
		}
	}
	// 校验失败时必须按本机路径处理，不能 panic 或猜一个容器。
	if isDockerRepoPath("/workspace/api") {
		t.Fatal("plain path detected as container")
	}
	if got := repoTargetOf("/tmp/x"); got.kind != targetLocal || got.localDir != "/tmp/x" {
		t.Fatalf("local target changed: %+v", got)
	}
}

func TestDockerExecArgvShape(t *testing.T) {
	target := repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/x.sock", container: fakeDockerContainerID, path: "/workspace/api"}
	env := dockerContainerEnv(target, nil)
	argv := dockerExecArgv(dockerContext{Name: target.context, Endpoint: target.endpoint}, target.container, target.path, env, "git", "--no-pager", "status", "--", "a;rm -rf /")
	if !slices.Equal(argv[:5], []string{"--host", target.endpoint, "exec", "-i", "--workdir"}) {
		t.Fatalf("unexpected prefix: %v", argv[:5])
	}
	if slices.Contains(argv, "-t") || slices.Contains(argv, "-it") {
		t.Fatal("exec must not allocate a TTY")
	}
	if argv[5] != target.path {
		t.Fatalf("workdir = %q", argv[5])
	}
	joined := strings.Join(argv, "\x00")
	if !strings.Contains(joined, "\x00"+fakeDockerContainerID+"\x00") {
		t.Fatal("commands must use the full container ID")
	}
	if !strings.Contains(joined, "\x00git\x00--no-pager\x00status\x00--\x00a;rm -rf /") {
		t.Fatalf("git args must stay separate argv: %v", argv)
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_CEILING_DIRECTORIES"} {
		if !slices.Contains(argv, name) {
			t.Fatalf("container env must clear %s", name)
		}
	}
	if !slices.Contains(env, "GIT_CEILING_DIRECTORIES=/workspace") {
		t.Fatalf("opened repository must not fall back to a parent: %v", env)
	}
	// 打开那一次显式传空值，允许 Git 找上层仓库。
	openEnv := dockerContainerEnv(target, []string{"GIT_CEILING_DIRECTORIES="})
	if !slices.Contains(openEnv, "GIT_CEILING_DIRECTORIES=") {
		t.Fatalf("explicit empty ceiling lost: %v", openEnv)
	}
}

func TestDockerContainerEnvDoesNotLeakHost(t *testing.T) {
	t.Setenv("PATH", "/host/only/path")
	t.Setenv("HOME", "/host/home")
	t.Setenv("GIT_DIR", "/host/repo/.git")
	t.Setenv("GIT_AUTHOR_NAME", "leaked")
	target := repoTarget{kind: targetDocker, context: "c", endpoint: "unix:///a.sock", container: fakeDockerContainerID, path: "/workspace/api/sub"}
	env := dockerContainerEnv(target, nil)
	for _, bad := range []string{"/host/only/path", "/host/home", "/host/repo/.git", "leaked"} {
		for _, e := range env {
			if strings.Contains(e, bad) {
				t.Fatalf("host value %q leaked into container env: %v", bad, env)
			}
		}
	}
}

func TestDockerRepoRootAndHistoryThroughCommand(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	env := setupFakeDocker(t, repo)
	ctx := context.Background()
	root, err := repoRoot(ctx, env.key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := parseDockerRepoKey(root)
	if !ok {
		t.Fatalf("root is not a container key: %q", root)
	}
	if !isCleanContainerAbsPath(parsed.path) {
		t.Fatalf("root path not normalized: %q", parsed.path)
	}
	meta, err := loadMeta(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if meta.branch != "main" || meta.head == zeroHash {
		t.Fatalf("meta = %+v", meta)
	}
	var subjects []string
	if err := streamCommits(ctx, root, 0, 10, func(c commit) error {
		subjects = append(subjects, c.subject)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(subjects, "base") {
		t.Fatalf("history missing base: %v", subjects)
	}
	// 命令日志和车道使用同一身份，而不是容器路径。
	if canonicalRepoDir(root) != root {
		t.Fatal("container identity changed between calls")
	}
	// 同一路径的符号链接别名要归一到真实根目录。
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	aliasKey := dockerRepoKey(repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/fake-docker.sock", container: fakeDockerContainerID, path: alias})
	aliasRoot, err := repoRoot(ctx, aliasKey)
	if err != nil {
		t.Fatal(err)
	}
	if aliasRoot != root {
		t.Fatalf("symlink alias produced a different identity: %q vs %q", aliasRoot, root)
	}
}

func TestDockerWorktreeReadCoversSpecialNamesAndLimits(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	// 文件名里同时有空格、百分号、中文和换行；未跟踪文件放在根目录，
	// 因为 --untracked-files=normal 会把整个未跟踪目录折叠成一行。
	name := "中文 % name\nnewline.txt"
	if err := os.WriteFile(filepath.Join(repo, name), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := setupFakeDocker(t, repo)
	root, err := repoRoot(context.Background(), env.key)
	if err != nil {
		t.Fatal(err)
	}
	data, note, err := readWorktreeFile(context.Background(), root, name)
	if err != nil || note != "" {
		t.Fatalf("read %q: %v %q", name, err, note)
	}
	if string(data) != "hello\nworld\n" {
		t.Fatalf("content = %q", data)
	}
	files, err := worktreeStatus(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		if f.path == name {
			found = true
			if f.extra != "未跟踪" {
				t.Fatalf("untracked state = %q", f.extra)
			}
		}
	}
	if !found {
		t.Fatalf("status missed the file: %+v", files)
	}
	for _, bad := range []string{"../outside", "/etc/hosts", "a/../../b", ""} {
		if _, _, err := readWorktreeFile(context.Background(), root, bad); err == nil {
			t.Fatalf("accepted unsafe path %q", bad)
		}
	}
	// 父目录符号链接不能把读取带出仓库。
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	if data, _, err := readWorktreeFile(context.Background(), root, "escape/secret.txt"); err == nil {
		t.Fatalf("read escaped the repository: %q", data)
	}
}

func TestDockerWorktreeRefreshFindsSameSizeEdit(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	path := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(path, []byte("aaaa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-m", "base")
	if err := os.WriteFile(path, []byte("bbbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := setupFakeDocker(t, repo)
	root, err := repoRoot(context.Background(), env.key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	files, cache, err := pollWorktreeStatus(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := ""
	for _, f := range files {
		if f.path == "a.txt" {
			before = f.worktreeVersion
		}
	}
	if before == "" {
		t.Fatalf("no version for a.txt: %+v", files)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("cccc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 等大小 + 恢复时间戳：元数据看起来没变，内容校验必须最终发现它。
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	cache.contentChecked = time.Now().Add(-2 * dockerContentCheckInterval)
	files, _, err = pollWorktreeStatus(ctx, root, cache)
	if err != nil {
		t.Fatal(err)
	}
	after := ""
	for _, f := range files {
		if f.path == "a.txt" {
			after = f.worktreeVersion
		}
	}
	if after == "" || after == before {
		t.Fatalf("same-size edit was not detected: %q -> %q", before, after)
	}
}

func TestDockerStageAndCommitInContainer(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	env := setupFakeDocker(t, repo)
	root, err := repoRoot(context.Background(), env.key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := stageFile(context.Background(), root, "new.txt"); err != nil {
		t.Fatal(err)
	}
	msg, err := applyProvenanceTrailers(context.Background(), root, "add file", provenance{agent: "Pi", model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if err := commitWorktree(context.Background(), root, msg, false); err != nil {
		t.Fatal(err)
	}
	body := string(git("log", "-1", "--format=%B"))
	if !strings.Contains(body, "AI-Agent: Pi") || !strings.Contains(body, "AI-Model: test-model") {
		t.Fatalf("provenance missing: %q", body)
	}
	files, err := worktreeStatus(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("worktree not clean: %+v", files)
	}
}

func TestDockerWriteGuardPersistsUnknownState(t *testing.T) {
	requireDockerRepos(t)
	dir := t.TempDir()
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	repo := setupFakeDocker(t, t.TempDir()).key

	a := &app{}
	a.initDockerWrites()
	rec, err := a.beginDockerWrite(repo, "提交")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.beginDockerWrite(repo, "暂存文件"); err == nil {
		t.Fatal("a second write must be blocked while the first is unconfirmed")
	}
	// 连接中断：结果未知，记录必须保留。
	a.finishDockerWrite(rec, &dockerUnavailableError{message: "Docker 未运行或无法连接，请先启动 Docker"})
	if a.dockerWriteBlockReason(repo) == "" {
		t.Fatal("unknown write state was cleared")
	}
	// 重启后仍未确认，不能自动变成可重试。
	b := &app{}
	b.initDockerWrites()
	if b.dockerWriteBlockReason(repo) == "" {
		t.Fatal("unknown state did not survive restart")
	}
	if _, err := b.beginDockerWrite(repo, "提交"); err == nil {
		t.Fatal("restart unlocked an unconfirmed write")
	}
	if err := b.clearDockerWrite(repo); err != nil {
		t.Fatal(err)
	}
	if b.dockerWriteBlockReason(repo) != "" {
		t.Fatal("explicit confirmation did not unlock the repository")
	}
	if rec, err := b.beginDockerWrite(repo, "提交"); err != nil || rec == nil {
		t.Fatalf("write should be allowed again: %v", err)
	}
}

func TestDockerFailureClassification(t *testing.T) {
	target := repoTarget{kind: targetDocker, context: "c", endpoint: "unix:///a.sock", container: fakeDockerContainerID, path: "/workspace/api"}
	cases := []struct {
		stderr string
		want   string
	}{
		{"Error response from daemon: No such container: abc", "不存在"},
		{"Error response from daemon: container abc is not running", "未运行"},
		{"Error response from daemon: Container abc is paused", "已暂停"},
		{"kiri: cannot start container: working directory: No such file or directory (os error 2)", "路径不存在"},
		{"kiri: cannot start container: git: executable file not found in $PATH", "安装 Git"},
		{"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?", "Docker 未运行"},
		{"cat: /workspace/api/x: Permission denied", "权限不足"},
	}
	for _, tc := range cases {
		err := dockerExecError(target, exec.Command("false").Run(), tc.stderr)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q -> %v (want %q)", tc.stderr, err, tc.want)
		}
	}
	// Git 自己的多行错误不能被 Docker 分类吞掉。
	gitErr := dockerRunError(target, "/fake/docker", exec.Command("false").Run(), []byte("hook rejected\nsee template\n"))
	if gitErr == nil || !strings.Contains(gitErr.Error(), "hook rejected\nsee template") {
		t.Fatalf("git stderr changed: %v", gitErr)
	}
	if isDockerUnavailable(gitErr) {
		t.Fatal("git failure reported as missing Docker")
	}
}

func TestDockerMissingIsNotMissingGit(t *testing.T) {
	dir := t.TempDir()
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	oldCandidates := dockerCandidatePaths
	dockerCandidatePaths = func() []string { return nil }
	t.Cleanup(func() { dockerCandidatePaths = oldCandidates })
	resetDockerResolution()
	t.Cleanup(resetDockerResolution)
	key := dockerRepoKey(repoTarget{kind: targetDocker, context: "c", endpoint: "unix:///a.sock", container: fakeDockerContainerID, path: "/workspace/api"})
	_, err := runGit(context.Background(), key, "status")
	if err == nil {
		t.Fatal("expected a Docker failure")
	}
	if !isDockerUnavailable(err) {
		t.Fatalf("not a Docker failure: %v", err)
	}
	if isGitUnavailable(err) {
		t.Fatal("missing Docker must not be reported as missing local Git")
	}
}

func TestDockerFinderStylePathResolution(t *testing.T) {
	requireDockerExec(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nif [ \"$1\" = \"--version\" ]; then echo ok; exit 0; fi\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 模拟从 Finder 启动：PATH 里没有 docker，只有常见安装位置（这里替换成临时目录）。
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	oldCandidates := dockerCandidatePaths
	dockerCandidatePaths = func() []string { return []string{script} }
	t.Cleanup(func() {
		dockerCandidatePaths = oldCandidates
		_ = oldPath
	})
	resetDockerResolution()
	t.Cleanup(resetDockerResolution)
	bin, err := dockerPath()
	if err != nil || bin != script {
		t.Fatalf("Finder-style lookup failed: %q %v", bin, err)
	}
}

func TestDockerListContainersAndNames(t *testing.T) {
	requireDockerExec(t)
	dir := t.TempDir()
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return filepath.Join(dir, "config"), nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	setupFakeDocker(t, t.TempDir())
	dc, err := currentDockerContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if dc.Name != "testctx" || dc.Endpoint != "unix:///tmp/fake-docker.sock" {
		t.Fatalf("context = %+v", dc)
	}
	containers, err := dockerListContainers(context.Background(), dc)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].Name != "demo-app" || containers[0].ID != fakeDockerContainerID {
		t.Fatalf("containers = %+v", containers)
	}
	if got := dockerNameOf(fakeDockerContainerID); got != "demo-app" {
		t.Fatalf("name = %q", got)
	}
	// 容器名只用于显示，编码键里不出现它。
	target := repoTarget{kind: targetDocker, context: dc.Name, endpoint: dc.Endpoint, container: fakeDockerContainerID, path: "/workspace/api", name: "demo-app"}
	key := dockerRepoKey(target)
	if strings.Contains(key, "demo-app") {
		t.Fatalf("name leaked into identity: %q", key)
	}
}

func TestDockerNonUnixEndpointRejected(t *testing.T) {
	requireDockerExec(t)
	dir := t.TempDir()
	script := filepath.Join(dir, "docker")
	if err := os.WriteFile(script, []byte("#!/bin/bash\nif [ \"$1\" = \"--version\" ]; then echo ok; exit 0; fi\nif [ \"$1\" = \"context\" ] && [ \"$2\" = \"show\" ]; then echo remote; exit 0; fi\necho '[{\"Name\":\"remote\",\"Endpoints\":{\"docker\":{\"Host\":\"ssh://user@host\"}}}]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetDockerResolution()
	t.Cleanup(resetDockerResolution)
	if _, err := currentDockerContext(context.Background()); err == nil || !strings.Contains(err.Error(), "只支持本机 Docker") {
		t.Fatalf("remote endpoint accepted: %v", err)
	}
}

func TestDockerOpenRejectsUnsupportedWorktreeShapes(t *testing.T) {
	requireDockerExec(t)
	repo, git := initTestRepo(t)
	git("commit", "--allow-empty", "-m", "base")
	env := setupFakeDocker(t, repo)
	ctx := context.Background()
	if _, err := repoRoot(ctx, env.key); err != nil {
		t.Fatalf("plain repository rejected: %v", err)
	}
	// linked worktree：第一版明确拒绝，而不是悄悄操作主工作树。
	linked := filepath.Join(t.TempDir(), "linked")
	git("worktree", "add", "-b", "linked", linked)
	linkedKey := dockerRepoKey(repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/fake-docker.sock", container: fakeDockerContainerID, path: linked})
	t.Setenv("FAKE_REPO", linked)
	if _, err := repoRoot(ctx, linkedKey); err == nil || !strings.Contains(err.Error(), "linked worktree") {
		t.Fatalf("linked worktree accepted: %v", err)
	}
	// 作为独立仓库打开的 submodule 同样拒绝。
	sub := filepath.Join(t.TempDir(), "sub")
	if out, err := exec.Command("git", "init", "-b", "main", sub).CombinedOutput(); err != nil {
		t.Fatalf("init submodule source: %v %s", err, out)
	}
	subGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", sub}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	subGit("config", "user.email", "sub@example.com")
	subGit("config", "user.name", "Sub")
	subGit("commit", "--allow-empty", "-m", "sub")
	git("-c", "protocol.file.allow=always", "submodule", "add", sub, "mod")
	git("commit", "-m", "add submodule")
	modPath := filepath.Join(repo, "mod")
	modKey := dockerRepoKey(repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/fake-docker.sock", container: fakeDockerContainerID, path: modPath})
	t.Setenv("FAKE_REPO", modPath)
	if _, err := repoRoot(ctx, modKey); err == nil || !strings.Contains(err.Error(), "submodule") {
		t.Fatalf("submodule accepted: %v", err)
	}
	// bare 仓库没有工作区。
	bare := filepath.Join(t.TempDir(), "bare.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v %s", err, out)
	}
	bareKey := dockerRepoKey(repoTarget{kind: targetDocker, context: "testctx", endpoint: "unix:///tmp/fake-docker.sock", container: fakeDockerContainerID, path: bare})
	t.Setenv("FAKE_REPO", bare)
	if _, err := repoRoot(ctx, bareKey); err == nil || !strings.Contains(err.Error(), "bare") {
		t.Fatalf("bare repository accepted: %v", err)
	}
}
