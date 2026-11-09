package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type ringUI struct {
	ruleTarget          string
	ruleRemove          bool
	preview             *rulePreview
	status              string
	stamp               ringStamp
	checked             time.Time
	busy, open, confirm bool
	err                 string
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
			if err != nil {
				t.ring.err = err.Error()
			}
		})
	}()
}
func (a *app) ringAction(t *repoTab, action string) {
	if t.ring.busy {
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
				t.ring.err = err.Error()
			}
			a.refreshRing(t)
		})
	}()
}
func (a *app) ringStatusControl(c *ui.Context, t *repoTab, key string) {
	label := "年轮 · " + t.ring.status
	if t.ring.status == "" {
		label = "年轮 · 未开启"
	}
	if ui.Button(c.Key(key), label).Tooltip("管理本仓库终端提交的年轮记录").Clicked() {
		t.ring.open = true
		a.refreshRing(t)
	}
	if s := t.ring.stamp; s.p.agent != "" && s.expires > time.Now().Unix() {
		ui.Text(c, fmt.Sprintf("印章：%s · 剩余 %d 分钟", s.p.agent, max(1, int(time.Until(time.Unix(s.expires, 0)).Minutes())))).FontSize(11).TextColor(c.Theme().TextMuted)
	}
}
func provenanceInputs(c *ui.Context, key string, p *provenance, disabled bool) bool {
	changed := false
	ui.Row(c).Gap(6).FillWidth().Children(func() {
		if ui.TextInput(c.Key(key+"-agent"), &p.agent).Placeholder("Agent（留空为纯人工）").Label("AI Agent").Grow(1).MinWidth(0).Disabled(disabled).Changed() {
			changed = true
		}
		if ui.TextInput(c.Key(key+"-model"), &p.model).Placeholder("模型（确知时填写）").Label("AI 模型").Grow(1).MinWidth(0).Disabled(disabled).Changed() {
			changed = true
		}
	})
	return changed
}
func (a *app) commitRingControl(c *ui.Context, t *repoTab) {
	label := "◎ 记录 AI 协同…"
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
		for i, p := range a.recentProvenance {
			p := p
			if ui.Button(c.Key(fmt.Sprintf("recent-prov-%d", i)), p.description()).Disabled(t.commitBusy).Clicked() {
				t.commitProv = p
				t.commitProvEdited = true
			}
		}
		if ui.Button(c.Key("commit-ring-clear"), "清空年轮").Disabled(t.commitBusy).Clicked() {
			t.commitProv = provenance{}
			t.commitProvEdited = true
		}
		a.ringStatusControl(c, t, "commit-ring-settings")
		if s := t.ring.stamp; s.p.agent != "" && s.expires > time.Now().Unix() {
			if ui.Button(c.Key("apply-stamp"), "应用建议印章："+s.p.description()).Disabled(t.commitBusy).Clicked() {
				t.commitProv = s.p
				t.commitProv.attribution = ""
				t.commitProvEdited = true
			}
		}
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
func (a *app) ringModal(c *ui.Context) {
	t := a.activeTab()
	if t == nil || !t.ring.open {
		return
	}
	ui.Modal(c.Key("ring-settings-modal"), &t.ring.open, func() {
		ui.Column(c).Gap(8).Width(460).Children(func() {
			ui.Text(c, "本仓库年轮记录").Bold()
			ui.Text(c, t.ring.status).FontSize(12)
			if reason := provenanceReadStatus(); reason != "" {
				ui.Text(c, reason).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, "开启会修改 .git/hooks。Agent 和模型 Trailer 随提交推送后公开。推断保持关闭。").FontSize(12)
			if strings.HasPrefix(t.ring.status, "需手动集成") {
				ui.Text(c, "请将年轮脚本保存到现有 Hook 可调用的位置，再在 prepare-commit-msg 中执行该脚本并传入原参数。脚本仅依赖 Git 和 POSIX sh。").FontSize(12)
				if ui.Button(c.Key("ring-copy-script"), "复制集成脚本").Clicked() {
					c.Services().WriteClipboard(ringHookScript())
				}
			} else {
				if strings.HasPrefix(t.ring.status, "已开启") {
					if ui.Button(c.Key("ring-uninstall"), "关闭并还原原 Hook").Disabled(t.ring.busy).Clicked() {
						a.ringAction(t, "uninstall")
					}
				} else {
					ui.Checkbox(c.Key("ring-confirm"), &t.ring.confirm, "我了解以上改动并同意在本仓库开启")
					ui.Checkbox(c.Key("ring-skip-notice"), &a.ringSkipNotice, "以后开启其他仓库时不再提示")
					if ui.Button(c.Key("ring-install"), "开启终端年轮记录").Disabled(t.ring.busy || (!t.ring.confirm && !a.ringSkipNotice)).Clicked() {
						a.ringAction(t, "install")
					}
				}
			}
			if t.ring.stamp.p.agent != "" {
				ui.Text(c, "生效印章："+t.ring.stamp.p.description())
				if ui.Button(c.Key("ring-clear-stamp"), "清除印章").Disabled(t.ring.busy).Clicked() {
					a.ringAction(t, "clear")
				}
			}
			if ui.Button(c.Key("ring-copy-rules"), "复制 Agent 规则片段").Clicked() {
				c.Services().WriteClipboard(provenanceRuleSnippet())
				c.Toast("已复制，可粘贴到项目规则文件")
			}
			ui.Text(c, "可选写入项目规则：文件通常受版本控制，会影响所有协作者。").FontSize(12)
			ui.TextInput(c.Key("rule-target"), &t.ring.ruleTarget).Placeholder("AGENTS.md / CLAUDE.md / .cursor/rules/xylring-provenance.mdc")
			ui.Checkbox(c.Key("rule-remove"), &t.ring.ruleRemove, "移除已有年轮规则")
			if ui.Button(c.Key("rule-preview"), "预览规则 diff").Disabled(t.ring.busy).Clicked() {
				a.previewRingRules(t)
			}
			if preview := t.ring.preview; preview != nil {
				ui.Scroll(c).Height(180).Children(func() { ui.Text(c, preview.diff).Font("monospace").FontSize(11) })
				if ui.Button(c.Key("rule-save"), "确认写入以上 diff").Disabled(t.ring.busy).Clicked() {
					a.saveRingRules(t)
				}
			}
			if t.ring.err != "" {
				ui.Text(c, t.ring.err).TextColor(c.Theme().Danger)
			}
			if ui.Button(c.Key("ring-close"), "关闭").Clicked() {
				t.ring.open = false
			}
		})
	})
}

func (a *app) previewRingRules(t *repoTab) {
	t.ring.busy = true
	dir, target, remove := t.path, t.ring.ruleTarget, t.ring.ruleRemove
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
				t.ring.err = err.Error()
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
				t.ring.err = err.Error()
			} else {
				t.ring.preview = nil
				a.refreshWorktree(t)
			}
		})
	}()
}
