package main

// 界面侧的 Git 状态：启动时探一次，缺 Git 时给出原因、安装指引和重试入口。
import (
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// gitState 是解析结果在界面上的镜像。只在界面线程读写。
type gitState struct {
	checked   bool
	checking  bool
	checkedAt time.Time
	path      string
	version   string
	err       string
}

// gitReady reports whether Git may be used. Before the first probe it stays
// permissive, so a slow probe never blocks a working install.
func (a *app) gitReady() bool {
	return a.git.err == ""
}

// checkGit probes off the interface thread and reports the answer back.
func (a *app) checkGit(force bool) {
	if a.git.checking {
		return
	}
	a.git.checking = true
	a.gitCheckGen++
	gen := a.gitCheckGen
	go func() {
		if force {
			resetGitResolution()
		}
		probe, err := resolveGit()
		state := gitState{checked: true, checkedAt: time.Now()}
		if err != nil {
			state.err = err.Error()
		} else {
			state.path, state.version = probe.path, probe.version
		}
		a.post(func() {
			if a.closed || a.gitCheckGen != gen {
				return
			}
			wasUnusable := a.git.err != ""
			changedBinary := a.git.path != "" && (a.git.path != state.path || a.git.version != state.version)
			a.git = state
			if state.err == "" && changedBinary {
				for _, tab := range a.tabs {
					a.reload(tab)
				}
			} else if state.err == "" && wasUnusable {
				a.resumeAfterGit()
			}
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

// recheckGit probes again while Git is unusable, so installing it or fixing a
// path takes effect without restarting the application.
func (a *app) recheckGit() {
	if a.git.checking {
		return
	}
	if a.git.checked && a.git.err == "" {
		return
	}
	if a.git.checked && time.Since(a.git.checkedAt) < gitRetryFailedAfter {
		return
	}
	a.checkGit(false)
}

// resumeAfterGit picks the work back up once Git becomes usable.
func (a *app) resumeAfterGit() {
	// 只清掉 Git 自己造成的那条提示，别的错误还得留着。
	if strings.Contains(a.banner, "Git") {
		a.setBanner("", "")
	}
	// 缺 Git 期间失败的每个仓库都重新读；只补活动标签会留下别的标签的旧错误。
	retried := false
	for _, t := range a.tabs {
		if t.err == "" {
			continue
		}
		t.err = ""
		t.loaded = false
		a.reload(t)
		retried = true
	}
	if retried {
		return
	}
	if a.foundFrom != "" {
		a.rescanFound()
	}
}

// noteGitUnavailable records a Git failure that a repository operation
// discovered, so the interface states the cause once and the retry loop stops
// until the next probe.
func (a *app) noteGitUnavailable(err error) {
	if err == nil || !a.gitReady() {
		return
	}
	var unavailable *gitUnavailableError
	if !errors.As(err, &unavailable) {
		return
	}
	a.gitCheckGen++
	a.git = gitState{checked: true, checkedAt: time.Now(), err: unavailable.Error()}
}

// applyGitExecutablePath stores the path chosen in settings and re-resolves.
func (a *app) applyGitExecutablePath(path string) {
	if a.git.checking {
		return
	}
	path = setGitExecutablePath(path)
	a.gitExecutable = path
	a.gitPathDraft = path
	a.checkGit(true)
}

func gitVersionDetail(a *app) string {
	if a.git.checking {
		return tr("正在检测 Git…")
	}
	if !a.git.checked {
		return tr("尚未检测")
	}
	if a.git.path == "" {
		return ""
	}
	if a.git.version == "" {
		return a.git.path
	}
	return a.git.version + " · " + a.git.path
}

// gitNoticeCard states the problem in the workspace instead of leaving the user
// with a repository that silently refuses to load.
func (a *app) gitNoticeCard(c *ui.Context) {
	if a.gitReady() {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("git-notice")).FillWidth().Padding(12, 14).Gap(10).Radius(t.Radius).
		Background(t.Warning.Alpha(0.1)).Border(1, t.Warning.Alpha(0.28)).Transition(fadeEnter()).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconAlertCircle).Size(16, 16).TextColor(t.Warning).Shrink(0)
			ui.Text(c, tr("Git 未就绪")).FontSize(13).FontWeight(600).TextColor(t.Warning).Grow(1).MinWidth(0)
		})
		ui.Text(c, trMessage(a.git.err)).FontSize(12).TextColor(t.Text)
		ui.Text(c, tr("安装完成后点「重新检测」即可，不需要重启应用。")).FontSize(12).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.PrimaryButton(c.Key("git-notice-retry"), tr("重新检测")).Disabled(a.git.checking).Clicked() {
				a.checkGit(true)
			}
			if ui.Button(c.Key("git-notice-settings"), tr("指定 Git 路径…")).Clicked() {
				a.openSettings()
			}
		})
	})
}

// gitBinarySettings names the Git the app runs and lets the user point at one
// it could not find on its own.
func (a *app) gitBinarySettings(c *ui.Context) {
	t := c.Theme()
	settingsSection(c, "settings-git-binary", "Git 程序",
		func() {
			settingsRow(c, "settings-git-status", func() {
				settingsName(c, "Git 状态")
				if !a.gitReady() {
					ui.Text(c, trMessage(a.git.err)).FontSize(12).TextColor(t.Danger)
					return
				}
				detail := gitVersionDetail(a)
				if detail == "" {
					detail = tr("可用")
				}
				settingsDetail(c, detail)
			}, func() {
				if ui.Button(c.Key("settings-git-recheck"), tr("重新检测")).Disabled(a.git.checking).Clicked() {
					a.checkGit(true)
				}
			})
		},
		func() {
			ui.Column(c.Key("settings-git-path")).FillWidth().Children(func() {
				settingsRow(c, "settings-git-path-row", func() {
					settingsName(c, "Git 可执行文件路径")
					settingsDetail(c, "留空则自动使用 PATH 和常见安装位置中的 Git")
				}, func() {
					if ui.Button(c.Key("settings-git-path-apply"), tr("应用")).
						Disabled(a.git.checking || strings.TrimSpace(a.gitPathDraft) == a.gitExecutable).Clicked() {
						a.applyGitExecutablePath(a.gitPathDraft)
					}
				})
				ui.Row(c.Key("settings-git-path-editor")).FillWidth().Padding(0, 16, 11, 16).Children(func() {
					input := ui.TextInput(c.Key("value"), &a.gitPathDraft).
						Label(tr("Git 可执行文件路径")).
						Placeholder(tr("留空则自动查找")).FillWidth().Disabled(a.git.checking)
					if input.Submitted() && !a.git.checking {
						a.applyGitExecutablePath(a.gitPathDraft)
					}
				})
			})
		},
	)
}
