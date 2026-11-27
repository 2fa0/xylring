package main

import (
	"context"
	"errors"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func dockerSuggestedPaths(info dockerContainerInfo) []string {
	var out []string
	add := func(raw string) {
		p, err := normalizeContainerRepoPath(raw)
		if err != nil {
			return
		}
		for _, old := range out {
			if old == p {
				return
			}
		}
		out = append(out, p)
	}
	add(info.WorkingDir)
	for _, mount := range info.Mounts {
		add(mount.Destination)
	}
	return out
}

func (a *app) selectDockerContainer(index int) {
	d := &a.docker
	d.selected, d.carryDraft, d.err = index, false, ""
	d.suggestions = nil
	d.path = ""
	d.pathEdited = false
	d.probeGen++
	gen, probeGen, dc, container := d.gen, d.probeGen, d.context, d.containers[index]
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if verifyDockerEndpoint(ctx, repoTarget{kind: targetDocker, context: dc.Name, endpoint: dc.Endpoint}) != nil {
			return
		}
		info, err := dockerInspectContainer(ctx, dc, container.ID)
		if err != nil {
			return
		}
		paths := dockerSuggestedPaths(info)
		preferred := ""
		if len(paths) > 0 {
			preferred = paths[0]
		}

		initial := preferred
		a.post(func() {
			if a.closed || !a.docker.open || a.docker.gen != gen || a.docker.probeGen != probeGen {
				return
			}
			a.docker.suggestions = paths
			if !a.docker.pathEdited {
				a.docker.path = initial
			}
		})
		for _, p := range paths {
			target, err := dockerBuildTarget(dc, info, p)
			if err != nil {
				continue
			}
			root, err := dockerRepoRoot(ctx, target)
			if err == nil {
				preferred = root.path
				break
			}
		}
		a.post(func() {
			if a.closed || !a.docker.open || a.docker.gen != gen || a.docker.probeGen != probeGen {
				return
			}
			a.docker.suggestions = paths
			if !a.docker.pathEdited && a.docker.path == initial {
				a.docker.path = preferred
			}
		})
	}()
}

func dockerRepoDisplayLabel(key string, keys []string) string {
	target, ok := parseDockerRepoKey(key)
	if !ok {
		return repoTitle(key)
	}

	multiple, sameBase := false, false
	for _, other := range keys {
		t, ok := parseDockerRepoKey(other)
		if ok && t.context == target.context && t.endpoint == target.endpoint && t.container == target.container && t.path != target.path {
			multiple = true
			sameBase = sameBase || path.Base(t.path) == path.Base(target.path)
		}
	}
	if multiple {
		name := path.Base(target.path)
		if sameBase {
			name = target.path
		}
		return repoTitle(key) + " / " + name
	}
	return repoTitle(key)
}

func (a *app) dockerTabLabel(tab *repoTab) string {
	if !isDockerRepoPath(tab.path) {
		return tab.title
	}
	keys := make([]string, len(a.tabs))
	for i, t := range a.tabs {
		keys[i] = t.path
	}
	return dockerRepoDisplayLabel(tab.path, keys)
}

// 只匹配原连接内的同名容器；不会因为另一个容器挂载同一目录就接管。
func dockerReplacement(ctx context.Context, old repoTarget) (string, error) {
	if err := verifyDockerEndpoint(ctx, old); err != nil {
		return "", err
	}
	dc := dockerContext{Name: old.context, Endpoint: old.endpoint}
	if info, err := dockerInspectContainer(ctx, dc, old.container); err == nil && info.ID == old.container && info.Running {
		return "", nil
	}
	containers, err := dockerListContainers(ctx, dc)
	if err != nil {
		return "", err
	}
	for _, container := range containers {
		if container.ID == old.container || container.Name != old.name {
			continue
		}
		next := old
		next.container, next.name = container.ID, container.Name
		root, err := dockerRepoRoot(ctx, next)
		if err == nil && root.path == old.path {
			return dockerRepoKey(root), nil
		}
	}
	return "", nil
}

func (a *app) probeDockerReplacement(t *repoTab) {
	old, ok := parseDockerRepoKey(t.path)
	if !ok || t.dockerProbing || time.Since(t.dockerProbed) < 15*time.Second {
		return
	}
	t.dockerProbing, t.dockerProbed = true, time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		key, _ := dockerReplacement(ctx, old)
		a.post(func() {
			if a.closed || t.closed {
				return
			}
			t.dockerProbing = false
			t.dockerReplacement = key
		})
	}()
}

func (a *app) reconnectDockerRepo(t *repoTab) {
	if t == nil || t.dockerReplacement == "" || t.dockerProbing || t.commitBusy || t.anySyncBusy() || a.write.busy {
		return
	}
	key, oldPath := t.dockerReplacement, t.path
	// 未确认的写操作仍留在旧身份上，不能通过接管绕过保护。
	if reason := a.dockerWriteBlockReason(oldPath); reason != "" {
		a.openDockerRecovery(t)
		return
	}
	t.dockerProbing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		target, _ := parseDockerRepoKey(key)
		root, err := dockerRepoRoot(ctx, target)
		a.post(func() {
			t.dockerProbing = false
			if a.closed || t.closed {
				return
			}
			if err != nil {
				a.setBanner(err.Error(), oldPath)
				return
			}
			if root.path != target.path {
				a.setBanner(tr("容器内仓库路径已变化，请重新打开"), oldPath)
				return
			}
			if existing := a.tabByPath(key); existing != nil {
				a.setBanner(tr("新容器仓库已打开，请先处理两个标签页的草稿"), oldPath)
				return
			}
			a.openRoot(key)
			if next := a.tabByPath(key); next != nil {
				next.commitMsg = t.commitMsg
				next.commitProv = t.commitProv
				next.commitProvEdited = t.commitProvEdited
			}
			if i := a.tabIndex(oldPath); i >= 0 {
				a.closeTab(i)
			}
		})
	}()
}

func (a *app) dockerReconnectButton(c *ui.Context, t *repoTab) {
	if t.dockerReplacement == "" {
		return
	}
	if ui.PrimaryButton(c.Key("docker-reconnect"), tr("切换到同名新容器并保留草稿")).Disabled(t.dockerProbing || a.write.busy || t.commitBusy || t.anySyncBusy()).Clicked() {
		a.reconnectDockerRepo(t)
	}
}

func shellQuoteDocker(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func dockerTerminalArgs(t repoTarget) []string {
	return []string{"--host", t.endpoint, "exec", "-it", "--workdir", t.path, t.container, "sh"}
}

func openDockerTerminal(repo string) error {
	t, ok := parseDockerRepoKey(repo)
	if !ok {
		return errors.New("不是容器仓库")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := verifyDockerEndpoint(ctx, t); err != nil {
		return err
	}
	bin, err := dockerPath()
	if err != nil {
		return err
	}
	args := dockerTerminalArgs(t)
	if runtime.GOOS == "darwin" {
		words := []string{shellQuoteDocker(bin)}
		for _, arg := range args {
			words = append(words, shellQuoteDocker(arg))
		}
		script := "tell application \"Terminal\"\nactivate\ndo script " + strconv.Quote(strings.Join(words, " ")) + "\nend tell"
		return exec.Command("osascript", "-e", script).Run()
	}
	for _, term := range []string{"x-terminal-emulator", "gnome-terminal", "konsole", "xfce4-terminal", "alacritty", "xterm"} {
		terminal, err := exec.LookPath(term)
		if err != nil {
			continue
		}
		prefix := []string{"-e", bin}
		if term == "gnome-terminal" {
			prefix = []string{"--", bin}
		}
		return exec.Command(terminal, append(prefix, args...)...).Start()
	}
	return errors.New(tr("未找到可用的终端应用"))
}

func (a *app) dockerTerminalMenu(m *ui.Menu, repo string) {
	if !isDockerRepoPath(repo) {
		return
	}
	if m.Item(tr("进入容器终端")).Chosen() {
		a.enterDockerTerminal(repo)
	}
}

func (a *app) enterDockerTerminal(repo string) {
	go func() {
		if err := openDockerTerminal(repo); err != nil {
			a.post(func() { a.setBanner(tr("打开终端失败: ")+err.Error(), repo) })
		}
	}()
}
