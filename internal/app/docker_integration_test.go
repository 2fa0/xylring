package app

// 真实 Docker 验收：默认跳过，只有显式设置 XYLRING_TEST_DOCKER=1 并准备好镜像时运行。
// 只创建、停止和删除本测试自己命名的容器与 volume，不碰用户现有资源，也不自动拉镜像。
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func dockerIntegrationImage(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Docker integration tests are not run on Windows")
	}
	if os.Getenv("XYLRING_TEST_DOCKER") != "1" {
		t.Skip("set XYLRING_TEST_DOCKER=1 and XYLRING_TEST_DOCKER_IMAGE to run Docker integration tests")
	}
	image := strings.TrimSpace(os.Getenv("XYLRING_TEST_DOCKER_IMAGE"))
	if image == "" {
		t.Skip("set XYLRING_TEST_DOCKER_IMAGE to a prepared Linux image with git and sh")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker CLI not found: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput(); err != nil {
		t.Skipf("docker daemon unavailable: %v %s", err, out)
	}
	return image
}

func dockerTestCLI(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func dockerTestExec(t *testing.T, name, script string) string {
	t.Helper()
	return dockerTestCLI(t, "exec", name, "sh", "-c", script)
}

// testDockerCallCount 把 docker CLI 换成本地计数包装：既记录调用次数，也验证
// 应用从 Finder 启动时用绝对路径之外的方式找不到 Docker 时仍能工作。
type testDockerCallCount struct {
	path string
	real string
	log  string
}

func installDockerTestCounter(t *testing.T) *testDockerCallCount {
	t.Helper()
	real, err := exec.LookPath("docker")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	wrapper := filepath.Join(dir, "docker")
	script := "#!/bin/bash\n{ printf 'CALL'; for a in \"$@\"; do printf '\\001%s' \"$a\"; done; printf '\\n'; } >> \"$XYLRING_TEST_DOCKER_LOG\"\nexec \"$XYLRING_TEST_DOCKER_REAL\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XYLRING_TEST_DOCKER_LOG", logPath)
	t.Setenv("XYLRING_TEST_DOCKER_REAL", real)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resetDockerResolution()
	t.Cleanup(resetDockerResolution)
	return &testDockerCallCount{path: wrapper, real: real, log: logPath}
}

func (c *testDockerCallCount) count(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(c.log)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "CALL") {
			n++
		}
	}
	return n
}

func (c *testDockerCallCount) phase(t *testing.T, name string, fn func()) int {
	t.Helper()
	before := c.count(t)
	start := time.Now()
	fn()
	n := c.count(t) - before
	t.Logf("docker calls: %s = %d, %s", name, n, time.Since(start).Round(time.Millisecond))
	return n
}

func TestDockerIntegrationRepository(t *testing.T) {
	image := dockerIntegrationImage(t)
	// 测试不能写用户真实的配置目录（容器显示名缓存、进行中写记录都在那里）。
	oldConfig := userConfigDir
	userConfigDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { userConfigDir = oldConfig })
	unique := fmt.Sprintf("xylring-it-%d-%d", os.Getpid(), time.Now().UnixNano())
	name := unique
	volume := unique + "-vol"
	dockerTestCLI(t, "volume", "create", volume)
	dockerTestCLI(t, "run", "-d", "--name", name, "--label", "xylring.test=integration",
		"--user", "0", "-v", volume+":/vol", "--entrypoint", "sleep", image, "900")
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		_ = exec.Command("docker", "volume", "rm", "-f", volume).Run()
	})
	// 容器可能刚刚 start，给它一点时间让 exec 可用。
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer waitCancel()
	for {
		if err := exec.CommandContext(waitCtx, "docker", "exec", name, "true").Run(); err == nil {
			break
		}
		if waitCtx.Err() != nil {
			t.Fatal("container did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	setup := `set -e
for d in /tmp/xylring-it/repo /vol/repo; do
  mkdir -p "$d/sub"
  cd "$d"
  git init -q -b main
  git config user.name "Container Test"
  git config user.email "container@example.com"
  printf 'one\n' > a.txt
  printf 'sub\n' > sub/b.txt
  git add -A
  git commit -qm first
  printf 'two\n' >> a.txt
  git commit -qam second
done`
	dockerTestExec(t, name, setup)
	gitVersion := strings.TrimSpace(dockerTestExec(t, name, "git --version"))
	t.Logf("image=%s git=%s", image, gitVersion)

	// 从这里开始走应用自己的 Docker 路径。
	counter := installDockerTestCounter(t)
	ctx := context.Background()
	dc, err := currentDockerContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, err := dockerInspectContainer(ctx, dc, strings.TrimSpace(dockerTestCLI(t, "inspect", "--format", "{{.Id}}", name)))
	if err != nil {
		t.Fatal(err)
	}
	if info.Platform != "" && info.Platform != "linux" {
		t.Fatalf("test image is not linux: %q", info.Platform)
	}
	for _, containerPath := range []string{"/tmp/xylring-it/repo", "/vol/repo"} {
		containerPath := containerPath
		t.Run(strings.TrimPrefix(containerPath, "/"), func(t *testing.T) {
			target, err := dockerBuildTarget(dc, info, containerPath)
			if err != nil {
				t.Fatal(err)
			}
			var root string
			counter.phase(t, "first open+history", func() {
				root, err = repoRoot(ctx, dockerRepoKey(target))
				if err != nil {
					t.Fatal(err)
				}
				meta, err := loadMeta(ctx, root)
				if err != nil {
					t.Fatal(err)
				}
				tips, err := queryTips(ctx, root, meta, historyQuery{})
				if err != nil {
					t.Fatal(err)
				}
				var subjects []string
				if err := streamHistory(ctx, root, historyQuery{}, tips, 0, 50, func(c commit) error {
					subjects = append(subjects, c.subject)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(subjects, []string{"second", "first"}) {
					t.Fatalf("subjects = %v", subjects)
				}
			})
			// 比较两个提交，并读取两个版本的文件内容。
			var first, second [20]byte
			meta, err := loadMeta(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			second = meta.head
			out, err := runGit(ctx, root, "rev-parse", "HEAD~1")
			if err != nil {
				t.Fatal(err)
			}
			first, err = parseHash(string(out))
			if err != nil {
				t.Fatal(err)
			}
			files, err := compareFiles(ctx, root, hashHex(first), hashHex(second))
			if err != nil || len(files) != 1 || files[0].path != "a.txt" {
				t.Fatalf("compare = %+v, %v", files, err)
			}
			oldContent, _, err := readBlob(ctx, root, hashHex(first), "a.txt")
			if err != nil || string(oldContent) != "one\n" {
				t.Fatalf("old blob = %q, %v", oldContent, err)
			}
			// 工作区：修改、新增未跟踪、重命名、删除、图片；同名文件两侧都有改动。
			dockerTestExec(t, name, "cd "+containerPath+` &&
printf 'three\n' >> a.txt &&
git add a.txt &&
printf 'four\n' >> a.txt &&
printf 'new\n' > new.txt &&
git mv sub/b.txt sub/c.txt &&
rm sub/c.txt &&
printf '\211PNG\r\n\032\n' > pic.png`)
			var cache *worktreeStatusCache
			var dirty []fileChange
			counter.phase(t, "worktree status (mixed)", func() {
				dirty, cache, err = pollWorktreeStatus(ctx, root, nil)
				if err != nil {
					t.Fatal(err)
				}
			})
			byPath := map[string]fileChange{}
			for _, f := range dirty {
				byPath[f.path] = f
			}
			if _, ok := byPath["new.txt"]; !ok {
				t.Fatalf("untracked file missing: %+v", dirty)
			}
			if f, ok := byPath["a.txt"]; !ok || !f.hasStaged() || !f.hasUnstaged() {
				t.Fatalf("a.txt should be staged and unstaged: %+v", dirty)
			}
			if data, note, err := readWorktreeFile(ctx, root, "new.txt"); err != nil || note != "" || string(data) != "new\n" {
				t.Fatalf("untracked read = %q %q %v", data, note, err)
			}
			if data, note, err := readWorktreeFile(ctx, root, "pic.png"); err != nil || note != "" || len(data) == 0 {
				t.Fatalf("image read = %d %q %v", len(data), note, err)
			}
			// 稳定后再检查一轮：只允许常数条 Docker 调用。
			counter.phase(t, "stable check round", func() {
				if _, _, err := pollWorktreeStatus(ctx, root, cache); err != nil {
					t.Fatal(err)
				}
			})
			// 暂存、取消暂存、全部暂存后提交（归因走容器 Git）。
			if err := stageFile(ctx, root, "new.txt"); err != nil {
				t.Fatal(err)
			}
			if err := unstageFile(ctx, root, "new.txt"); err != nil {
				t.Fatal(err)
			}
			if err := stageAll(ctx, root); err != nil {
				t.Fatal(err)
			}
			msg, err := applyProvenanceTrailers(ctx, root, "container commit", provenance{Agent: "Pi", Model: "test-model"})
			if err != nil {
				t.Fatal(err)
			}
			if err := commitWorktree(ctx, root, msg, false); err != nil {
				t.Fatal(err)
			}
			body, err := runGit(ctx, root, "log", "-1", "--format=%B")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "AI-Agent: Pi") || !strings.Contains(string(body), "AI-Model: test-model") {
				t.Fatalf("provenance missing: %q", body)
			}
			left, err := worktreeStatus(ctx, root)
			if err != nil || len(left) != 0 {
				t.Fatalf("worktree not clean: %+v %v", left, err)
			}
			// 年轮只读：读已有印章与 Hook 状态，写入必须被拒绝。
			dockerTestExec(t, name, "cd "+containerPath+` &&
printf 'agent=Pi\nmodel=probe-model\nexpires=9999999999\nonce=0\n' > .git/xylring-stamp`)
			stamp, err := readStamp(ctx, root)
			if err != nil || stamp.p.Agent != "Pi" || stamp.p.Model != "probe-model" {
				t.Fatalf("stamp = %+v, %v", stamp.p, err)
			}
			if status := ringHookStatus(ctx, root); status != "未开启" {
				t.Fatalf("hook status = %q", status)
			}
			if err := writeStamp(ctx, root, ringStamp{p: provenance{Agent: "Pi", Model: "m"}, expires: time.Now().Add(time.Hour).Unix()}); err == nil || !strings.Contains(err.Error(), "暂不支持") {
				t.Fatalf("stamp write was not blocked: %v", err)
			}
			if err := installRingHook(ctx, root); err == nil || !strings.Contains(err.Error(), "暂不支持") {
				t.Fatalf("hook install was not blocked: %v", err)
			}
			// 容器里的 Hook 必须照常执行，拒绝时保留原因。
			dockerTestExec(t, name, "cd "+containerPath+` &&
printf '#!/bin/sh\ngrep -q ALLOW "$1" || { echo "blocked by test hook" >&2; exit 1; }\n' > .git/hooks/prepare-commit-msg &&
chmod +x .git/hooks/prepare-commit-msg &&
printf 'hook\n' > hook.txt && git add hook.txt`)
			if err := commitWorktree(ctx, root, "without permission", false); err == nil || !strings.Contains(err.Error(), "blocked by test hook") {
				t.Fatalf("hook rejection not reported: %v", err)
			}
			if err := commitWorktree(ctx, root, "ALLOW me", false); err != nil {
				t.Fatalf("hook-approved commit failed: %v", err)
			}
		})
	}

	// 100 个脏文件：第一轮会逐个读未跟踪文件数行数，之后靠批量元数据和内容哈希。
	var root100 string
	target, err := dockerBuildTarget(dc, info, "/tmp/xylring-it/repo")
	if err != nil {
		t.Fatal(err)
	}
	if root100, err = repoRoot(ctx, dockerRepoKey(target)); err != nil {
		t.Fatal(err)
	}
	// 100 个已跟踪文件同时变脏：状态、元数据和内容哈希都应保持常数条调用。
	var script strings.Builder
	script.WriteString("cd /tmp/xylring-it/repo && mkdir -p many && ")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&script, "printf 'line\\n' > many/f%03d.txt && ", i)
	}
	script.WriteString("git add many && git commit -qm 'ALLOW many' && ")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&script, "printf 'line2\\n' >> many/f%03d.txt && ", i)
	}
	script.WriteString("true")
	dockerTestExec(t, name, script.String())
	var manyCache *worktreeStatusCache
	counter.phase(t, "100 dirty files first round", func() {
		if _, manyCache, err = pollWorktreeStatus(ctx, root100, nil); err != nil {
			t.Fatal(err)
		}
	})
	manyCache.contentChecked = time.Now().Add(-2 * dockerContentCheckInterval)
	counter.phase(t, "100 dirty files content check round", func() {
		if _, _, err := pollWorktreeStatus(ctx, root100, manyCache); err != nil {
			t.Fatal(err)
		}
	})
	_ = counter

	t.Run("safe file reads", func(t *testing.T) {
		dockerTestExec(t, name, `mkdir -p /tmp/xylring-safe/a /tmp/xylring-outside
printf correct > '/tmp/xylring-safe/a\b.txt'
printf wrong > /tmp/xylring-safe/a/b.txt
printf secret > /tmp/xylring-outside/secret
ln -s /tmp/xylring-outside /tmp/xylring-safe/escape`)
		safeTarget := target
		safeTarget.path = "/tmp/xylring-safe"
		key := dockerRepoKey(safeTarget)
		data, note, err := readWorktreeFile(ctx, key, `a\b.txt`)
		if err != nil || note != "" || string(data) != "correct" {
			t.Fatalf("backslash filename: %q %q %v", data, note, err)
		}
		if data, _, err := readWorktreeFile(ctx, key, "escape/secret"); err == nil || len(data) != 0 {
			t.Fatalf("outside read allowed: %q %v", data, err)
		}
		if hashes, err := dockerHashFiles(ctx, key, []string{`a\b.txt`, "a/b.txt"}); err != nil || len(hashes) != 2 || hashes[0] == hashes[1] {
			t.Fatalf("safe batch hashes: %v %v", hashes, err)
		}
	})

	t.Run("repository boundaries and paths", func(t *testing.T) {
		dockerTestExec(t, name, `mkdir -p /tmp/xylring-parent/child
git init -q /tmp/xylring-parent
printf keep > /tmp/xylring-parent/child/a.txt
git init -q '/tmp/xylring-space'
git init -q '/tmp/xylring-space '
ln -s '/tmp/xylring-space ' /tmp/xylring-alias`)
		probe := target
		probe.path = "/tmp/xylring-parent/child"
		if err := stageFile(ctx, dockerRepoKey(probe), "a.txt"); err == nil {
			t.Fatal("missing child wrote to parent")
		}
		if out := dockerTestExec(t, name, `cd /tmp/xylring-parent; git ls-files`); out != "" {
			t.Fatalf("parent index changed: %q", out)
		}
		probe.path = "/tmp/xylring-alias"
		root, err := dockerRepoRoot(ctx, probe)
		if err != nil || root.path != "/tmp/xylring-space " {
			t.Fatalf("wrong root: %q %v", root.path, err)
		}
	})
	t.Run("200 untracked files", func(t *testing.T) {
		dockerTestExec(t, name, `mkdir -p /tmp/xylring-untracked; cd /tmp/xylring-untracked; git init -q
for n in $(seq 1 200); do printf 'one\ntwo' > "new$n.txt"; done`)
		probe := target
		probe.path = "/tmp/xylring-untracked"
		short, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		before := counter.count(t)
		started := time.Now()
		files, cache, err := pollWorktreeStatus(short, dockerRepoKey(probe), nil)
		calls := counter.count(t) - before
		t.Logf("200 untracked: calls=%d elapsed=%s", calls, time.Since(started))
		if err != nil || len(files) != 200 {
			t.Fatalf("refresh: files=%d err=%v", len(files), err)
		}
		for _, f := range files {
			if !f.unstagedStat.has || f.unstagedStat.added != 2 {
				t.Fatalf("wrong stats: %+v", f)
			}
		}
		if calls > 20 {
			t.Fatalf("too many Docker calls: %d", calls)
		}
		cache.contentChecked = time.Now().Add(-2 * dockerContentCheckInterval)
		before = counter.count(t)
		started = time.Now()
		if _, _, err := pollWorktreeStatus(short, dockerRepoKey(probe), cache); err != nil {
			t.Fatal(err)
		}
		t.Logf("200 untracked unchanged content check: calls=%d elapsed=%s", counter.count(t)-before, time.Since(started))
	})

	// 停止 / 启动：错误原因必须不同。
	dockerTestCLI(t, "stop", name)
	if err := checkDockerTarget(ctx, target); err == nil || !strings.Contains(err.Error(), "未运行") {
		t.Fatalf("stopped container = %v", err)
	}
	// 容器不可读时年轮状态要给出真实原因，而不是「未开启」。
	if status := ringHookStatus(ctx, root100); !strings.Contains(status, "未运行") && !strings.Contains(status, "无法读取") {
		t.Fatalf("offline hook status = %q", status)
	}
	if _, err := runGit(ctx, root100, "status"); err == nil {
		t.Fatal("git succeeded against a stopped container")
	}
	dockerTestCLI(t, "start", name)
	waitCtx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel2()
	for {
		if err := exec.CommandContext(waitCtx2, "docker", "exec", name, "true").Run(); err == nil {
			break
		}
		if waitCtx2.Err() != nil {
			t.Fatal("container did not restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := checkDockerTarget(ctx, target); err != nil {
		t.Fatalf("restarted container: %v", err)
	}

	// 删除后同名重建：旧键必须失效，新容器是另一个身份。
	oldKey := root100
	dockerTestCLI(t, "rm", "-f", name)
	dockerTestCLI(t, "run", "-d", "--name", name, "--label", "xylring.test=integration",
		"--user", "0", "-v", volume+":/vol", "--entrypoint", "sleep", image, "900")
	waitCtx3, cancel3 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel3()
	for {
		if err := exec.CommandContext(waitCtx3, "docker", "exec", name, "true").Run(); err == nil {
			break
		}
		if waitCtx3.Err() != nil {
			t.Fatal("replacement container did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := repoRoot(ctx, oldKey); err == nil {
		t.Fatal("deleted container's repository key still resolves")
	}
	newID := strings.TrimSpace(dockerTestCLI(t, "inspect", "--format", "{{.Id}}", name))
	if newID == info.ID {
		t.Fatal("recreated container kept the same ID")
	}
	newTarget, err := dockerBuildTarget(dc, dockerContainerInfo{ID: newID, Name: name, Platform: "linux", Status: "running", Running: true}, "/tmp/xylring-it/repo")
	if err != nil {
		t.Fatal(err)
	}
	// 新容器里没有旧仓库，打开会明确报错，而不是自动接手旧身份。
	if _, err := repoRoot(ctx, dockerRepoKey(newTarget)); err == nil {
		t.Fatal("new container silently inherited the old repository")
	}
	if dockerRepoKey(newTarget) == oldKey {
		t.Fatal("recreated container produced the same identity")
	}
}
