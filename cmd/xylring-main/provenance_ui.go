package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type ringUI struct {
	ruleStatuses []ruleFileStatus
	rulesBusy    bool
	preview      *rulePreview
	status       string
	stamp        ringStamp
	checked      time.Time
	busy, open   bool
	err          string
}

func (a *app) refreshRing(t *repoTab) {
	if t.ring.busy || time.Since(t.ring.checked) < 10*time.Second {
		return
	}
	t.ring.busy = true
	path := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status := ringHookStatus(ctx, path)
		stamp, err := readStamp(ctx, path)
		var statuses []ruleFileStatus
		if !isDockerRepoPath(path) {
			statuses = inspectRuleFiles(path)
		}
		// Expired stamps are ignored by both readers; do not remove a file that
		// another process may have replaced between reading and cleanup.
		a.post(func() {
			if t.closed {
				return
			}
			t.ring.busy = false
			t.ring.checked = time.Now()
			t.ring.status = status
			t.ring.stamp = stamp
			t.ring.ruleStatuses = statuses
			if err != nil {
				t.ring.err = "读取年轮状态失败：" + err.Error()
			}
		})
	}()
}

func (a *app) refreshRingRules(t *repoTab) {
	if t.ring.rulesBusy {
		return
	}
	// 容器仓库不读取本机规则文件：路径不是主机路径，也不开放规则写入。
	if isDockerRepoPath(t.path) {
		return
	}
	t.ring.rulesBusy = true
	path := t.path
	go func() {
		statuses := inspectRuleFiles(path)
		a.post(func() {
			if t.closed {
				return
			}
			t.ring.rulesBusy = false
			t.ring.ruleStatuses = statuses
		})
	}()
}

// ringActionErrorPrefix 让失败的那一步自己说出来：开关 Hook、清除印章共用一处错误显示。
func ringActionErrorPrefix(action string) string {
	switch action {
	case "install":
		return "开启年轮 Hook 失败："
	case "uninstall":
		return "停用年轮 Hook 失败："
	case "clear":
		return "清除印章失败："
	}
	return "更新年轮设置失败："
}

func (a *app) ringAction(t *repoTab, action string) {
	if t.ring.busy {
		return
	}
	if a.dockerActionBlocked(t) {
		return
	}
	if isDockerRepoPath(t.path) {
		t.ring.err = tr("容器仓库暂不支持修改年轮设置")
		return
	}
	t.ring.busy = true
	path := t.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		switch action {
		case "install":
			err = installRingHook(ctx, path)
		case "uninstall":
			err = uninstallRingHook(ctx, path)
		case "clear":
			err = clearStamp(ctx, path)
		}
		a.post(func() {
			if t.closed {
				return
			}
			t.ring.busy = false
			t.ring.checked = time.Time{}
			t.ring.err = ""
			if err != nil {
				t.ring.err = ringActionErrorPrefix(action) + err.Error()
			}
			a.refreshRing(t)
		})
	}()
}

// woodColor 是年轮标记的木色：亮色模式深一点，暗色模式浅一点，两种背景上都读得出来。
func woodColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#cba47c")
	}
	return ui.Hex("#986b46")
}

func ringStatusShort(status string) (short, full string) {
	if status == "" {
		return "未开启", ""
	}
	if strings.HasPrefix(status, "需手动集成") {
		return "需手动集成", status
	}
	return status, ""
}

func ringStatusColor(status string, th *ui.Theme) ui.Color {
	switch {
	case strings.HasPrefix(status, "已开启"):
		return th.Success
	case status == "版本过旧" || strings.HasPrefix(status, "需手动集成"):
		return th.Warning
	default:
		return th.TextMuted
	}
}

func (a *app) ringStatusControl(c *ui.Context, t *repoTab, key string) {
	th := c.Theme()
	wood := woodColor(th)
	short, full := ringStatusShort(t.ring.status)
	dot := ringStatusColor(t.ring.status, th)
	if !strings.HasPrefix(t.ring.status, "已开启") && t.ring.status != "版本过旧" && !strings.HasPrefix(t.ring.status, "需手动集成") {
		dot = th.TextMuted.Alpha(0.5)
	}
	tip := tr("管理本仓库终端提交的年轮记录")
	if full != "" {
		tip += "\n" + trMessage(full)
	}
	if s := t.ring.stamp; s.p.agent != "" && s.expires > time.Now().Unix() {
		mins := max(1, int(time.Until(time.Unix(s.expires, 0)).Minutes()))
		tip += "\n" + fmt.Sprintf(tr("生效印章：%s · 剩余 %d 分钟"), s.p.description(), mins)
	}
	row := ui.ButtonBase(c.Key(key)).
		Tooltip(tip).
		FillWidth().MinWidth(0).Shrink(0).Padding(6, 10).Gap(8).
		Radius(10).Border(1, th.Border).Background(th.Surface).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Transition(colorMotion())
	switch {
	case row.Pressed():
		row.Background(th.SurfacePressed)
	case row.Hovered():
		row.Background(th.SurfaceHover)
	}
	row.Children(func() {
		ui.Icon(c, iconTreeRings).Size(16, 16).TextColor(wood).Shrink(0)
		ui.Text(c, tr("年轮")).FontSize(12.5).FontWeight(600).TextColor(th.Text).SingleLine().Shrink(0)
		ui.Box(c).Size(6, 6).Radius(3).Background(dot).Shrink(0)
		ui.Text(c, trMessage(short)).FontSize(11.5).TextColor(th.TextMuted).SingleLine().Grow(1).MinWidth(0)
		ui.Icon(c, iconRingSettings).Size(13, 13).TextColor(th.TextMuted).Shrink(0)
	})
	if row.Clicked() {
		t.ring.open = true
		t.ring.err = ""
		a.refreshRing(t)
		a.refreshRingRules(t)
	}
}
func provenanceInputs(c *ui.Context, key string, p *provenance, disabled bool) bool {
	changed := false
	ui.Row(c).Gap(6).FillWidth().Children(func() {
		if ui.TextInput(c.Key(key+"-agent"), &p.agent).Placeholder(tr("Agent（严格模式人工填 human）")).Label("AI Agent").Grow(1).MinWidth(0).Disabled(disabled).Changed() {
			changed = true
		}
		if ui.TextInput(c.Key(key+"-model"), &p.model).Placeholder(tr("AI 提交必填，人工留空")).Label(tr("AI 模型（AI 提交必填）")).Grow(1).MinWidth(0).Disabled(disabled).Changed() {
			changed = true
		}
	})
	return changed
}
func (a *app) commitRingControl(c *ui.Context, t *repoTab) {
	label := tr("◎ 记录 AI 协同…")
	if t.commitProv.agent != "" {
		label = t.commitProv.description()
	}
	if ui.Button(c.Key("commit-ring-toggle"), label).Disabled(t.commitBusy).Clicked() {
		t.commitRingOpen = !t.commitRingOpen
	}
	if t.commitRingOpen {
		if provenanceInputs(c, "commit-ring", &t.commitProv, t.commitBusy) {
			t.commitProvEdited = true
		}
		hasStamp := t.ring.stamp.p.agent != "" && t.ring.stamp.expires > time.Now().Unix()
		ui.Row(c).FillWidth().Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
			for i, p := range a.recentProvenance {
				p := p
				if ui.Button(c.Key(fmt.Sprintf("recent-prov-%d", i)), p.description()).Disabled(t.commitBusy).Clicked() {
					t.commitProv = p
					t.commitProvEdited = true
				}
			}
			if hasStamp {
				s := t.ring.stamp
				if ui.Button(c.Key("apply-stamp"), tr("应用建议印章：")+s.p.description()).Disabled(t.commitBusy).Clicked() {
					t.commitProv = s.p
					t.commitProv.attribution = ""
					t.commitProvEdited = true
				}
			}
			if ui.Button(c.Key("commit-ring-clear"), tr("清空年轮")).Disabled(t.commitBusy).Clicked() {
				t.commitProv = provenance{}
				t.commitProvEdited = true
			}
		})
		a.ringStatusControl(c, t, "commit-ring-settings")
	}
}
func (a *app) rememberProvenance(p provenance) {
	if p.agent == "" {
		return
	}
	p.attribution = ""
	p.badge = ""
	next := []provenance{p}
	for _, old := range a.recentProvenance {
		if old.agent != p.agent || old.model != p.model {
			next = append(next, old)
		}
		if len(next) == 8 {
			break
		}
	}
	a.recentProvenance = next
}

func ringCopyButton(c *ui.Context, key, label, tooltip, content string) {
	b := ui.Button(c.Key(key), "").Shrink(0)
	if b.Clicked() {
		c.Services().WriteClipboard(content)
		b.Jump("copied", 1)
	}
	copied := b.AnimateTo("copied", 0, ui.Animation{Duration: 2 * time.Second}) > 0
	display := label
	if copied {
		display = tr("已复制")
	}
	b.Label(display)
	if tooltip != "" {
		b.Tooltip(tooltip)
	}
	b.Children(func() {
		ui.Text(c, display).SingleLine()
	})
}

func renderRuleDiff(c *ui.Context, diff string, th *ui.Theme) {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	spans := make([]ui.Span, 0, len(lines)*2)
	for i, line := range lines {
		if i > 0 {
			spans = append(spans, ui.Span{Text: "\n"})
		}
		color := th.Text
		switch {
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "):
			color = th.TextMuted
		case strings.HasPrefix(line, "+"):
			color = th.Success
		case strings.HasPrefix(line, "-"):
			color = th.Danger
		}
		spans = append(spans, ui.Span{Text: line, Color: color})
	}
	ui.RichText(c, spans...).Font("monospace").FontSize(11).LineHeight(1.45)
}

func findRuleStatus(statuses []ruleFileStatus, target string) ruleFileStatus {
	for _, s := range statuses {
		if s.target == target {
			return s
		}
	}
	return ruleFileStatus{target: target}
}

func (a *app) ringModal(c *ui.Context) {
	t := a.activeTab()
	if t == nil || !t.ring.open {
		return
	}
	th := c.Theme()
	shortStatus, _ := ringStatusShort(t.ring.status)
	statusColor := ringStatusColor(t.ring.status, th)
	isEnabled := strings.HasPrefix(t.ring.status, "已开启")
	isOutdated := t.ring.status == "版本过旧"
	isManual := strings.HasPrefix(t.ring.status, "需手动集成")
	hasStamp := t.ring.stamp.p.agent != "" && t.ring.stamp.expires > time.Now().Unix()
	container := isDockerRepoPath(t.path)

	appModal(c.Key("ring-settings-modal"), &t.ring.open, tr("本仓库年轮记录"), 560, t.ring.busy, func() {
		if reason := provenanceReadStatus(); reason != "" {
			settingsNotice(c, "ring-read-status", reason, th.Warning)
		}
		if container {
			settingsNotice(c, "ring-docker-readonly", tr("容器仓库只读取年轮状态，暂不支持开启 Hook、设置印章或写入规则文件。"), th.TextMuted)
		}
		if t.ring.err != "" {
			settingsNotice(c, "ring-error", trMessage(t.ring.err), th.Danger)
		}

		// Card 1: Terminal commit Hook & active stamp
		ui.Column(c.Key("ring-hook-card")).FillWidth().Radius(10).Clip().Background(th.Surface).Border(1, th.Border).Children(func() {
			ui.Row(c.Key("ring-hook-row")).FillWidth().Padding(12, 14).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
						ui.Text(c, tr("终端提交 Hook")).FontSize(13.5).FontWeight(600).TextColor(th.Text)
						chipSoft(c, "ring-hook-badge", trMessage(shortStatus), statusColor, false)
					})
					switch {
					case isManual:
						ui.Text(c, tr("已设置 core.hooksPath，请在现有 prepare-commit-msg 中调用年轮脚本。")).FontSize(12).LineHeight(1.4).TextColor(th.TextMuted)
					case isOutdated:
						ui.Text(c, tr("Hook 版本过旧，建议更新到最新脚本。")).FontSize(12).LineHeight(1.4).TextColor(th.TextMuted)
					case !isEnabled && !a.ringSkipNotice:
						ui.Text(c, tr("开启后会写入 .git/hooks/prepare-commit-msg，Agent 与模型信息会随提交公开。")).FontSize(12).LineHeight(1.4).TextColor(th.TextMuted)
						ui.Checkbox(c.Key("ring-skip-notice"), &a.ringSkipNotice, tr("以后开启其他仓库时不再提示"))
					}
				})
				if container {
					return
				}
				ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
					switch {
					case isManual:
						ringCopyButton(c, "ring-copy-script", tr("复制脚本"), tr("将年轮脚本保存到现有 Hook 可调用的位置，再在 prepare-commit-msg 中执行该脚本并传入原参数；脚本仅依赖 Git 和 POSIX sh。"), ringHookScript())
					case isEnabled:
						if ui.Button(c.Key("ring-uninstall"), tr("停用")).Disabled(t.ring.busy).Tooltip(tr("移除年轮 Hook 并还原原 Hook")).Clicked() {
							a.ringAction(t, "uninstall")
						}
					case isOutdated:
						if ui.Button(c.Key("ring-uninstall"), tr("停用")).Disabled(t.ring.busy).Tooltip(tr("移除年轮 Hook 并还原原 Hook")).Clicked() {
							a.ringAction(t, "uninstall")
						}
						if ui.PrimaryButton(c.Key("ring-install"), tr("更新 Hook")).Disabled(t.ring.busy).Clicked() {
							a.ringAction(t, "install")
						}
					default:
						if ui.PrimaryButton(c.Key("ring-install"), tr("开启 Hook")).Disabled(t.ring.busy).Clicked() {
							a.ringAction(t, "install")
						}
					}
				})
			})
			if hasStamp {
				settingsCardDivider(c)
				mins := max(1, int(time.Until(time.Unix(t.ring.stamp.expires, 0)).Minutes()))
				ttlText := fmt.Sprintf(tr("剩余 %d 分钟"), mins)
				if t.ring.stamp.once {
					ttlText += tr(" · 单次有效")
				}
				ui.Row(c.Key("ring-stamp-row")).FillWidth().Padding(10, 14).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
							ui.Text(c, tr("生效印章")).FontSize(12.5).FontWeight(500).TextColor(th.Text)
							chipSoft(c, "ring-stamp-desc", t.ring.stamp.p.description(), woodColor(th), false)
						})
						ui.Text(c, ttlText).FontSize(11.5).TextColor(th.TextMuted)
					})
					if !container {
						if ui.Button(c.Key("ring-clear-stamp"), tr("清除印章")).Disabled(t.ring.busy).Shrink(0).Clicked() {
							a.ringAction(t, "clear")
						}
					}
				})
			}
		})

		// Card 2: Project rules
		if container {
			return
		}
		ui.Column(c.Key("ring-rules-card")).FillWidth().Radius(10).Clip().Background(th.Surface).Border(1, th.Border).Children(func() {
			ui.Row(c.Key("ring-rules-header")).FillWidth().Padding(10, 14).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tr("项目规则")).FontSize(13.5).FontWeight(600).TextColor(th.Text).Grow(1).MinWidth(0)
				ringCopyButton(c, "ring-copy-rules", tr("复制规则片段"), tr("复制到剪贴板，可粘贴到全局或其他规则文件"), provenanceRuleSnippet())
			})
			for _, target := range provenanceRuleTargets {
				st := findRuleStatus(t.ring.ruleStatuses, target)
				settingsCardDivider(c)
				row := ui.Row(c.Key("rule-row-"+target)).FillWidth().Padding(9, 14).Gap(8).AlignItems(ui.Center)
				if t.ring.preview != nil && t.ring.preview.target == target {
					row.Background(th.Accent.Alpha(0.06))
				}
				row.Children(func() {
					ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, target).Font("monospace").FontSize(12).FontWeight(500).TextColor(th.Text).SingleLine().MinWidth(0).Tooltip(target)
						switch {
						case st.hasRule && !st.outdated:
							chipSoft(c, "rule-status-"+target, tr("已写入"), th.Success, false)
						case st.hasRule && st.outdated:
							chipSoft(c, "rule-status-"+target, tr("可更新"), th.Warning, false)
						case st.exists:
							chipSoft(c, "rule-status-"+target, tr("已有文件"), th.TextMuted, false)
						}
					})
					ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
						switch {
						case !st.hasRule:
							if ui.Button(c.Key("rule-add-"+target), tr("写入…")).Disabled(t.ring.busy).Clicked() {
								a.previewRingRulesFor(t, target, false)
							}
						case st.outdated:
							if ui.Button(c.Key("rule-update-"+target), tr("更新…")).Disabled(t.ring.busy).Clicked() {
								a.previewRingRulesFor(t, target, false)
							}
							if ui.Button(c.Key("rule-remove-"+target), tr("移除…")).Disabled(t.ring.busy).Clicked() {
								a.previewRingRulesFor(t, target, true)
							}
						default:
							if ui.Button(c.Key("rule-remove-"+target), tr("移除…")).Disabled(t.ring.busy).Clicked() {
								a.previewRingRulesFor(t, target, true)
							}
						}
					})
				})
			}
			if preview := t.ring.preview; preview != nil {
				settingsCardDivider(c)
				previewTitle := fmt.Sprintf(tr("预览写入：%s"), preview.target)
				confirmLabel := tr("确认写入")
				if preview.remove {
					previewTitle = fmt.Sprintf(tr("预览移除：%s"), preview.target)
					confirmLabel = tr("确认移除")
				}
				ui.Column(c.Key("ring-rule-preview")).FillWidth().Padding(12, 14).Gap(8).Background(th.Background).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, previewTitle).FontSize(12).FontWeight(600).TextColor(th.Text).SingleLine().Grow(1).MinWidth(0)
						if ui.Button(c.Key("rule-preview-cancel"), tr("取消")).Disabled(t.ring.busy).Shrink(0).Clicked() {
							t.ring.preview = nil
						}
						if ui.PrimaryButton(c.Key("rule-save"), confirmLabel).Disabled(t.ring.busy).Shrink(0).Clicked() {
							a.saveRingRules(t)
						}
					})
					ui.Scroll(c.Key("rule-diff-scroll")).Height(160).FillWidth().Radius(8).Border(1, th.Border).Background(th.Surface).Children(func() {
						ui.Column(c).Padding(8, 10).Children(func() {
							renderRuleDiff(c, preview.diff, th)
						})
					})
				})
			}
		})
	}, func() {
		if t.ring.busy || t.ring.rulesBusy {
			busyRow(c, "ring-busy", tr("正在处理…"), "")
		}
		ui.Box(c).Grow(1)
		if ui.Button(c.Key("ring-close"), tr("关闭")).Disabled(t.ring.busy).Clicked() {
			t.ring.open = false
			t.ring.preview = nil
			t.ring.err = ""
		}
	})
}

func (a *app) previewRingRulesFor(t *repoTab, target string, remove bool) {
	if t.ring.busy {
		return
	}
	t.ring.busy = true
	dir := t.path
	go func() {
		p, err := previewRuleSnippet(dir, target, remove)
		a.post(func() {
			if t.closed {
				return
			}
			t.ring.busy = false
			t.ring.preview = nil
			t.ring.err = ""
			if err != nil {
				t.ring.err = "预览规则修改失败：" + err.Error()
			} else if p.diff == "" {
				a.refreshRingRules(t)
			} else {
				t.ring.preview = &p
			}
		})
	}()
}
func (a *app) saveRingRules(t *repoTab) {
	p := t.ring.preview
	if p == nil {
		return
	}
	t.ring.busy = true
	go func() {
		err := saveRulePreview(*p)
		a.post(func() {
			if t.closed {
				return
			}
			t.ring.busy = false
			t.ring.err = ""
			if err != nil {
				t.ring.err = "保存规则失败：" + err.Error()
			} else {
				t.ring.preview = nil
				a.refreshRingRules(t)
				a.refreshWorktree(t)
			}
		})
	}()
}
