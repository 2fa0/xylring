package main

// 打开容器仓库的入口、路径推荐与弹窗。
// 不提供 Docker 管理功能：不启动、不重建、不删除容器，也不配置凭据。
import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type dockerDialogState struct {
	suggestions []string
	pathEdited  bool
	probeGen    int
	open        bool
	busy        bool
	gen         int
	err         string
	context     dockerContext
	containers  []dockerContainer
	selected    int
	path        string
	carryDraft  bool
}

func dockerReposSupported() bool {
	return runtime.GOOS != "windows"
}

func (a *app) openDockerRepoDialog() {
	if !dockerReposSupported() {
		a.setBanner(tr("Windows 暂不支持容器仓库"), "")
		return
	}
	if a.docker.open {
		return
	}
	a.docker = dockerDialogState{open: true, busy: true, selected: -1, gen: a.docker.gen + 1}
	gen := a.docker.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		dc, err := currentDockerContext(ctx)
		var containers []dockerContainer
		if err == nil {
			containers, err = dockerListContainers(ctx, dc)
		}
		a.post(func() {
			if a.closed || !a.docker.open || a.docker.gen != gen {
				return
			}
			a.docker.busy = false
			if err != nil {
				a.docker.err = "读取容器列表失败：" + err.Error()
				return
			}
			a.docker.context = dc
			a.docker.containers = containers
			a.docker.selected = -1
			if len(containers) > 0 {
				a.selectDockerContainer(0)
			}
		})
	}()
}

func (a *app) submitDockerRepo() {
	d := &a.docker
	if d.busy || d.selected < 0 || d.selected >= len(d.containers) {
		return
	}
	path, err := normalizeContainerRepoPath(d.path)
	if err != nil {
		d.err = err.Error()
		return
	}
	candidate := a.dockerDraftCandidate(d.containers[d.selected])
	carry := d.carryDraft && candidate != nil
	d.busy = true
	d.err = ""
	gen := d.gen
	container := d.containers[d.selected]
	dc := d.context
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		if err := verifyDockerEndpoint(ctx, repoTarget{kind: targetDocker, context: dc.Name, endpoint: dc.Endpoint}); err != nil {
			a.post(func() {
				if a.closed || a.docker.gen != gen {
					return
				}
				a.docker.busy = false
				a.docker.err = err.Error()
			})
			return
		}
		info, err := dockerInspectContainer(ctx, dc, container.ID)
		if err == nil {
			var target repoTarget
			target, err = dockerBuildTarget(dc, info, path)
			if err == nil {
				var root repoTarget
				root, err = dockerRepoRoot(ctx, target)
				if err == nil {
					key := dockerRepoKey(root)
					a.post(func() {
						if a.closed || a.docker.gen != gen {
							return
						}
						a.docker.busy = false
						a.docker.open = false
						before := len(a.tabs)
						a.openRoot(key)
						// 同名新容器不会自动接手旧仓库；草稿只在用户勾选后带过来，
						// 而且只给新建的标签，不覆盖已经打开的仓库。
						if carry && candidate != nil && len(a.tabs) > before {
							if tab := a.tabByPath(key); tab != nil {
								tab.commitMsg = candidate.Message
							}
						}
						a.remember(key)
					})
					return
				}
			}
		}
		a.post(func() {
			if a.closed || a.docker.gen != gen {
				return
			}
			a.docker.busy = false
			a.docker.err = "打开容器仓库失败：" + trimMsg(err.Error())
		})
	}()
}

func (a *app) dockerRepoModal(c *ui.Context) {
	d := &a.docker
	if !d.open {
		return
	}
	theme := c.Theme()
	appModal(c.Key("docker-repo-modal"), &d.open, tr("打开容器仓库"), 560, d.busy, func() {
		if !dockerReposSupported() {
			ui.Text(c, tr("Windows 暂不支持容器仓库")).FontSize(12).TextColor(theme.Danger)
			return
		}
		if d.busy && len(d.containers) == 0 && d.err == "" {
			busyRow(c, "docker-loading", tr("正在读取容器列表…"), "")
			return
		}
		if d.err != "" {
			settingsNotice(c, "docker-error", d.err, theme.Danger)
		}
		if d.context.Name != "" {
			ui.Row(c.Key("docker-context")).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconContainer).Size(13, 13).TextColor(theme.TextMuted).Shrink(0)
				ui.Text(c, dockerContextLabel(d.context)).FontSize(11.5).TextColor(theme.TextMuted).SingleLine().MinWidth(0)
			})
		}
		if len(d.containers) == 0 && !d.busy {
			emptyHint(c, "docker-no-containers", emptyHintProps{
				Icon:  iconContainer,
				Title: tr("没有运行中的容器"),
			})
		} else {
			ui.Text(c, tr("运行中的容器")).FontSize(12).TextColor(theme.TextMuted)
			ui.Column(c.Key("docker-container-list")).FillWidth().Gap(2).Children(func() {
				for i, container := range d.containers {
					selected := i == d.selected
					row := ui.ButtonBase(c.Key(fmt.Sprintf("docker-container-%d", i))).
						FillWidth().Padding(8, 10).Gap(10).Radius(theme.Radius).
						AlignItems(ui.Center).Cursor(ui.CursorPointer).Transition(colorMotion()).
						Tooltip(container.ID).Disabled(d.busy)
					switch {
					case selected:
						row.Background(theme.Accent.Alpha(0.14)).Border(1, theme.Accent.Alpha(0.5))
					case row.Hovered():
						row.Background(theme.SurfaceHover)
					}
					row.Children(func() {
						ui.Icon(c, iconContainer).Size(16, 16).TextColor(theme.TextMuted).Shrink(0)
						// 一行只放主文案和一个类型标记：镜像说明这是哪类容器。
						ui.Text(c, container.Name).FontSize(13).FontWeight(600).SingleLine().MinWidth(0).Grow(1)
						ui.Text(c, container.Image).FontSize(11.5).TextColor(theme.TextMuted).SingleLine().MaxWidthPercent(45).Shrink(0)
						if selected {
							ui.Icon(c, iconCheck).Size(13, 13).TextColor(theme.Accent).Shrink(0)
						}
					})
					if row.Clicked() {
						a.selectDockerContainer(i)
					}
				}
			})
		}
		if d.selected >= 0 && d.selected < len(d.containers) {
			if candidate := a.dockerDraftCandidate(d.containers[d.selected]); candidate != nil {
				ui.Checkbox(c.Key("docker-carry-draft"), &d.carryDraft, tr("带入旧容器的提交草稿")).Tooltip(candidate.Repo)
			} else {
				d.carryDraft = false
			}
		}
		input := ui.TextInput(c.Key("docker-repo-path"), &d.path).
			Label(tr("容器内仓库路径")).
			Placeholder("/workspace/api").
			FillWidth().Disabled(d.busy)
		ui.Row(c.Key("docker-path-suggestions")).Wrap().Gap(6).Children(func() {
			for _, suggestion := range d.suggestions {
				p := suggestion
				if p == d.path {
					continue
				}
				if ui.Button(c.Key("docker-path-"+p), p).MaxWidthPercent(100).Tooltip(p).Disabled(d.busy).Clicked() {
					d.path = p
					d.pathEdited = true
				}
			}
		})
		if input.Changed() {
			d.pathEdited = true
		}
		if input.Submitted() && !d.busy {
			a.submitDockerRepo()
		}
	}, func() {
		if ui.Button(c.Key("docker-cancel"), tr("取消")).Disabled(d.busy).Clicked() {
			d.open = false
		}
		if ui.PrimaryButton(c.Key("docker-open"), tr("打开")).Disabled(d.busy || d.selected < 0 || strings.TrimSpace(d.path) == "").Clicked() {
			a.submitDockerRepo()
		}
	})
}

func dockerContextLabel(dc dockerContext) string {
	if dc.Name == "" {
		return ""
	}
	return tr("Docker 连接：") + dc.Name
}

// dockerDraftCandidate 找同一容器名但已不在运行的旧标签，且它还留着提交草稿。
// 只提供「是否带过来」的选择，不迁移任何缓存或待执行动作。
type dockerDraftCandidate struct {
	Repo    string
	Message string
}

func (a *app) dockerDraftCandidate(container dockerContainer) *dockerDraftCandidate {
	for _, t := range a.tabs {
		if !isDockerRepoPath(t.path) || strings.TrimSpace(t.commitMsg) == "" {
			continue
		}
		target, ok := parseDockerRepoKey(t.path)
		if !ok || target.container == container.ID {
			continue
		}
		if a.docker.context.Name != "" && (target.context != a.docker.context.Name || target.endpoint != a.docker.context.Endpoint || target.path != a.docker.path) {
			continue
		}
		if repoTitle(t.path) != container.Name {
			continue
		}
		return &dockerDraftCandidate{Repo: t.path, Message: t.commitMsg}
	}
	return nil
}
