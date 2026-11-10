package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (a *app) openOperation(action, target, path string) {
	if a.write.busy || a.fetch.busy || a.op.busy {
		return
	}
	d := repoOperation{action: action, target: target, path: path, stash: -1, gen: a.op.gen + 1}
	if action == "Stash" || action == "远程" {
		d.hub = action
	}
	if t := a.activeTab(); t != nil {
		if t.syncBusy || t.commitBusy || t.unavailable && action != "克隆仓库" && action != "初始化仓库" {
			return
		}
		t.stopAutoFetch()
		d.repo = t.path
		d.name = t.branch
	}
	if action == "克隆仓库" || action == "初始化仓库" {
		d.repo = ""
	}
	if d.repo == "" && action != "克隆仓库" && action != "初始化仓库" {
		return
	}
	d.busy = true
	a.op = d
	a.showOperation = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var err error
		if d.repo != "" {
			b, _ := runGit(ctx, d.repo, "symbolic-ref", "-q", "HEAD")
			d.expectedBranch = strings.TrimSpace(string(b))
			b, _ = runGit(ctx, d.repo, "rev-parse", "--verify", "HEAD")
			d.expectedHead = strings.TrimSpace(string(b))
			d.state, err = operationState(ctx, d.repo)
			if err == nil {
				d.remotes, err = listRemotes(ctx, d.repo)
			}
			if action == "推送分支" {
				for _, key := range []string{"branch." + d.name + ".pushRemote", "remote.pushDefault", "branch." + d.name + ".remote"} {
					b, e := runGit(ctx, d.repo, "config", "--get", key)
					if e == nil {
						candidate := strings.TrimSpace(string(b))
						for _, r := range d.remotes {
							if r == candidate {
								d.remote = r
							}
						}
						if d.remote != "" {
							break
						}
					}
				}
			}
			if d.remote == "" && len(d.remotes) == 1 {
				d.remote = d.remotes[0]
			}
			d.remoteURLs = map[string]string{}
			for _, r := range d.remotes {
				b, e := runGit(ctx, d.repo, "remote", "get-url", "--", r)
				if e == nil {
					d.remoteURLs[r] = strings.TrimSpace(string(b))
				}
			}
			if action == "修改远程地址" {
				d.value = d.remoteURLs[d.remote]
			}
			if err == nil {
				d.stashes, err = listStashes(ctx, d.repo)
			}
			if len(d.stashes) > 0 {
				d.stashChoice = d.stashes[0].label
				d.stash = 0
			}
			if operationNeedsTarget(action) && err == nil {
				d.target, err = resolveOperationTarget(ctx, d.repo, target)
			}
			switch action {
			case "审阅并暂存", "审阅并取消暂存":
				d.patch, err = readIndexPatch(ctx, d.repo, path, action == "审阅并取消暂存")
				if err == nil {
					d.hunks, err = splitPatch(d.patch)
				}
				d.hunk = 0
				if len(d.hunks) > 0 {
					d.hunkChoice = d.hunks[0].label
					d.output = d.hunks[0].patch
				} else {
					d.output = d.patch
				}
			case "设置身份":
				b, _ := runGit(ctx, d.repo, "config", "--get", "user.name")
				d.name = strings.TrimSpace(string(b))
				b, _ = runGit(ctx, d.repo, "config", "--get", "user.email")
				d.value = strings.TrimSpace(string(b))
				b, _ = runGit(ctx, d.repo, "config", "--show-origin", "--get-regexp", "^user\\.(name|email)$")
				d.output = string(b)
			case "新增远程":
				d.name = ""
			case "远程":
				d.value = d.remoteURLs[d.remote]
			}
		}
		a.post(func() {
			if a.op.gen != d.gen {
				return
			}
			d.busy = false
			d.ready = err == nil
			if err != nil {
				d.err = err.Error()
			}
			a.op = d
		})
	}()
}
func operationNeedsTarget(action string) bool {
	switch action {
	case "合并", "变基", "拣选", "撤销提交", "软重置", "混合重置", "硬重置", "恢复历史文件", "Blame", "行历史":
		return true
	}
	return false
}
func operationReadOnly(action string) bool {
	return action == "Blame" || action == "行历史" || action == "查看 Stash"
}
func operationRequiresConfirm(action string) bool {
	switch action {
	case "硬重置", "混合重置", "软重置", "恢复历史文件", "中止操作", "删除 Stash", "弹出 Stash", "应用 Stash", "删除远程":
		return true
	}
	return false
}
func (a *app) pickOperationDirectory() {
	if a.picking || a.win == nil {
		return
	}
	a.picking = true
	win := a.win
	gen := a.op.gen
	go func() {
		paths, err := openRepoDialog(win)
		a.post(func() {
			a.picking = false
			if a.op.gen != gen || !a.showOperation {
				return
			}
			if err != nil {
				a.op.err = trimMsg(err.Error())
				return
			}
			if len(paths) == 0 {
				return
			}
			chosen := paths[0]
			if a.op.action == "克隆仓库" {
				chosen = deriveClonePath(chosen, a.op.value)
			}
			a.op.path = chosen
		})
	}()
}
func operationHint(action string) string {
	switch action {
	case "硬重置":
		return "将移动当前分支并覆盖已暂存和未暂存的受跟踪文件；可能删除阻挡写入的未跟踪文件。请先保存需要的改动。"
	case "混合重置":
		return "移动当前分支并重置暂存区，工作区文件保留。"
	case "软重置":
		return "移动当前分支，保留暂存区和工作区。"
	case "恢复历史文件":
		return "仅覆盖这个文件的工作区内容，保留暂存区；目标版本不存在该文件时会删除它。"
	case "删除 Stash":
		return "删除所选 Stash；请先确认其中的改动已经保存。"
	case "中止操作":
		return "中止当前 Git 操作。冲突解决期间的修改可能被撤销。"
	case "继续操作":
		return "先在编辑器解决冲突，再从工作区菜单暂存已解决文件，最后继续操作。"
	case "合并", "变基", "拣选", "撤销提交":
		return "执行于当前分支。发生冲突后可在工作区继续或中止操作。"
	case "Stash":
		return "保存当前改动，或查看、应用、弹出、删除已保存的 Stash。"
	case "远程":
		return "管理远程地址，或把当前分支、标签推到指定远程。"
	case "推送分支":
		return "将当前 HEAD 推送到所选远程的目标分支，并建立跟踪关系。"
	case "设置身份":
		return "默认只写入当前仓库；选择全局会影响其他仓库。"
	}
	return ""
}
func (a *app) operationModal(c *ui.Context) {
	d := &a.op
	title := d.action
	if d.hub != "" {
		title = d.hub
	}
	hub := d.hub == "Stash" || d.hub == "远程" || d.action == "Stash" || d.action == "远程"
	needConfirm := operationRequiresConfirm(d.action)
	appModal(c.Key("repo-operation"), &a.showOperation, title, 660, d.busy, func() {
		ui.Text(c, d.repo).FontSize(11).SingleLine().Tooltip(d.repo)
		if hint := operationHint(title); hint != "" {
			ui.Text(c, hint).FontSize(12)
		}
		if d.state != "" {
			ui.Text(c, "当前操作："+d.state).TextColor(c.Theme().Warning)
		}
		if d.target != "" {
			ui.Text(c, "提交："+d.target).FontSize(12).SingleLine()
		}
		if d.path != "" && d.action != "克隆仓库" && d.action != "初始化仓库" {
			ui.Text(c, "文件："+d.path).FontSize(12)
		}
		switch {
		case d.hub == "Stash" || d.action == "Stash":
			a.stashHub(c, d)
		case d.hub == "远程" || d.action == "远程":
			a.remoteHub(c, d)
		case d.action == "审阅并暂存" || d.action == "审阅并取消暂存":
			labels := []string{}
			for _, h := range d.hunks {
				labels = append(labels, h.label)
			}
			if len(labels) > 0 {
				ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					if ui.Select(c.Key("op-hunk"), &d.hunkChoice, labels).Grow(1).Disabled(d.busy).Changed() {
						for i, h := range d.hunks {
							if h.label == d.hunkChoice {
								d.hunk = i
								break
							}
						}
						d.output = d.hunks[d.hunk].patch
					}
					if len(labels) > 1 {
						if ui.Button(c.Key("op-hunk-prev"), "上一块").Disabled(d.busy || d.hunk <= 0).Clicked() {
							d.hunk--
							d.hunkChoice = d.hunks[d.hunk].label
							d.output = d.hunks[d.hunk].patch
						}
						if ui.Button(c.Key("op-hunk-next"), "下一块").Disabled(d.busy || d.hunk >= len(d.hunks)-1).Clicked() {
							d.hunk++
							d.hunkChoice = d.hunks[d.hunk].label
							d.output = d.hunks[d.hunk].patch
						}
					}
				})
			} else {
				ui.Text(c, "没有可操作的文本改动块")
			}
		case d.action == "保存 Stash":
			ui.TextInput(c.Key("op-value"), &d.value).Placeholder("保存说明").Disabled(d.busy)
			ui.Checkbox(c.Key("op-untracked"), &d.includeUntracked, "包含未跟踪文件").Disabled(d.busy)
		case d.action == "应用 Stash" || d.action == "弹出 Stash" || d.action == "删除 Stash" || d.action == "查看 Stash":
			labels := []string{}
			for _, s := range d.stashes {
				labels = append(labels, s.label)
			}
			if len(labels) == 0 {
				ui.Text(c, "没有 Stash")
			} else {
				if ui.Select(c.Key("op-stash"), &d.stashChoice, labels).Disabled(d.busy).Changed() {
					for i, s := range d.stashes {
						if s.label == d.stashChoice {
							d.stash = i
							break
						}
					}
				}
			}
		case d.action == "新增远程" || d.action == "修改远程地址" || d.action == "删除远程" || d.action == "推送分支" || d.action == "推送标签":
			if d.action != "新增远程" {
				ui.Text(c, "远程")
				if ui.Select(c.Key("op-remote"), &d.remote, d.remotes).Disabled(d.busy).Changed() && d.action == "修改远程地址" {
					d.value = d.remoteURLs[d.remote]
				}
				if address := d.remoteURLs[d.remote]; address != "" {
					ui.Text(c, address).FontSize(11).SingleLine().Tooltip(address)
				}
			}
			if d.action == "新增远程" || strings.HasPrefix(d.action, "推送") {
				ui.TextInput(c.Key("op-name"), &d.name).Placeholder("名称 / 目标分支或标签").Disabled(d.busy)
			}
			if d.action == "新增远程" || d.action == "修改远程地址" {
				ui.TextInput(c.Key("op-value"), &d.value).Placeholder("远程地址").Disabled(d.busy)
			}
		case d.action == "设置身份":
			ui.TextInput(c.Key("op-name"), &d.name).Placeholder("姓名").Disabled(d.busy)
			ui.TextInput(c.Key("op-value"), &d.value).Placeholder("邮箱").Disabled(d.busy)
			ui.Checkbox(c.Key("op-global"), &d.global, "写入全局配置").Disabled(d.busy)
		case d.action == "行历史":
			ui.TextInput(c.Key("op-value"), &d.value).Placeholder("起始行,结束行，例如 10,20 或 10-20").Disabled(d.busy)
		case d.action == "克隆仓库" || d.action == "初始化仓库":
			if d.action == "克隆仓库" {
				ui.TextInput(c.Key("op-url"), &d.value).Placeholder("仓库 URL").Disabled(d.busy)
			}
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("op-path"), &d.path).Placeholder("本地目录（绝对路径）").Grow(1).Disabled(d.busy)
				if a.win != nil {
					if ui.Button(c.Key("op-pick-dir"), "选择目录…").Disabled(d.busy || a.picking).Clicked() {
						a.pickOperationDirectory()
					}
				}
			})
		}
		if d.output != "" {
			height := float32(260)
			if d.action == "设置身份" {
				height = 120
			}
			ui.TextArea(c.Key("op-output"), &d.output).Font("monospace").FontSize(12).ReadOnly(true).Height(height).FillWidth()
		}
		if d.err != "" {
			ui.Text(c, d.err).TextColor(c.Theme().Danger)
		}
		if hub {
			ui.Checkbox(c.Key("op-confirm"), &d.confirmed, "确认删除、弹出或覆盖类操作").Disabled(d.busy)
		} else if needConfirm {
			ui.Checkbox(c.Key("op-confirm"), &d.confirmed, "确认对上述仓库和目标执行操作").Disabled(d.busy)
		}
	}, func() {
		if d.busy && a.opCancel != nil {
			if ui.Button(c.Key("op-cancel"), "取消执行").Clicked() {
				a.opCancel()
			}
		}
		if ui.Button(c.Key("op-close"), "关闭").Disabled(d.busy).Clicked() {
			a.showOperation = false
		}
		if !hub {
			label := "执行"
			if d.busy {
				label = "正在处理…"
			}
			if ui.PrimaryButton(c.Key("op-run"), label).Disabled(d.busy || !d.ready || (needConfirm && !d.confirmed)).Clicked() {
				a.runOperation()
			}
		}
	})
}
func (a *app) runOperation() {
	if a.op.busy || !a.op.ready || a.write.busy || a.fetch.busy {
		return
	}
	d := a.op
	if t := a.tabByPath(d.repo); t != nil && (t.commitBusy || t.syncBusy) {
		a.op.err = "请等待当前仓库操作完成"
		return
	}
	d.busy = true
	d.err = ""
	a.op = d
	a.write.busy = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	a.opCancel = cancel
	go func() {
		defer cancel()
		var out string
		var err error
		state, e := operationState(ctx, d.repo)
		if d.repo != "" && !operationReadOnly(d.action) {
			b, _ := runGit(ctx, d.repo, "symbolic-ref", "-q", "HEAD")
			branch := strings.TrimSpace(string(b))
			b, _ = runGit(ctx, d.repo, "rev-parse", "--verify", "HEAD")
			head := strings.TrimSpace(string(b))
			if branch != d.expectedBranch || head != d.expectedHead {
				e = fmt.Errorf("HEAD 或当前分支已变化，请关闭后重新确认")
			}
		}
		if d.repo != "" && e != nil {
			err = e
		} else if state != "" && d.action != "继续操作" && d.action != "中止操作" && d.action != "外部合并工具" && !operationReadOnly(d.action) {
			err = fmt.Errorf("请先完成或中止 %s", state)
		} else {
			out, err = executeOperation(ctx, d)
		}
		a.post(func() {
			a.write.busy = false
			a.opCancel = nil
			if a.op.gen != d.gen {
				return
			}
			a.op.busy = false
			a.op.confirmed = false
			a.op.output = out
			if err != nil {
				a.op.err = err.Error()
			} else {
				a.pendingToast = d.action + "完成"
				if d.action == "审阅并暂存" || d.action == "审阅并取消暂存" {
					a.showOperation = false
				}
			}
			if t := a.tabByPath(d.repo); t != nil && !operationReadOnly(d.action) {
				a.refresh(t)
			}
			if err == nil && (d.action == "克隆仓库" || d.action == "初始化仓库") {
				a.openPath(d.path)
				a.showOperation = false
			}
			if d.hub != "" {
				a.op.hub = d.hub
				a.op.action = d.hub
				if err == nil && d.action != "查看 Stash" {
					a.openOperation(d.hub, "", "")
				}
			}
		})
	}()
}

func (a *app) runHubAction(action string, requireConfirm bool) {
	if a.op.busy || !a.op.ready {
		return
	}
	if requireConfirm && !a.op.confirmed {
		a.op.err = "请先确认再执行"
		return
	}
	if a.op.hub == "" && (a.op.action == "Stash" || a.op.action == "远程") {
		a.op.hub = a.op.action
	}
	switch action {
	case "新增远程":
		a.op.name = a.op.newRemote
		a.op.value = a.op.newURL
	case "推送标签":
		if strings.TrimSpace(a.op.refName) == "" {
			a.op.err = "请填写标签名"
			return
		}
		a.op.name = a.op.refName
	}
	a.op.action = action
	a.op.err = ""
	a.runOperation()
}

func (a *app) stashHub(c *ui.Context, d *repoOperation) {
	ui.TextInput(c.Key("op-value"), &d.value).Placeholder("保存说明").Disabled(d.busy)
	ui.Checkbox(c.Key("op-untracked"), &d.includeUntracked, "包含未跟踪文件").Disabled(d.busy)
	if ui.Button(c.Key("op-stash-save"), "保存").Disabled(d.busy || !d.ready).Clicked() {
		a.runHubAction("保存 Stash", false)
	}
	labels := []string{}
	for _, s := range d.stashes {
		labels = append(labels, s.label)
	}
	if len(labels) == 0 {
		ui.Text(c, "没有 Stash")
		return
	}
	if ui.Select(c.Key("op-stash"), &d.stashChoice, labels).Disabled(d.busy).Changed() {
		for i, s := range d.stashes {
			if s.label == d.stashChoice {
				d.stash = i
				break
			}
		}
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c.Key("op-stash-show"), "查看").Disabled(d.busy || !d.ready).Clicked() {
			a.runHubAction("查看 Stash", false)
		}
		if ui.Button(c.Key("op-stash-apply"), "应用").Disabled(d.busy || !d.ready).Clicked() {
			a.runHubAction("应用 Stash", true)
		}
		if ui.Button(c.Key("op-stash-pop"), "弹出").Disabled(d.busy || !d.ready).Clicked() {
			a.runHubAction("弹出 Stash", true)
		}
		if ui.Button(c.Key("op-stash-drop"), "删除").Disabled(d.busy || !d.ready).Clicked() {
			a.runHubAction("删除 Stash", true)
		}
	})
}

func (a *app) remoteHub(c *ui.Context, d *repoOperation) {
	if len(d.remotes) == 0 {
		ui.Text(c, "还没有远程")
	} else {
		if ui.Select(c.Key("op-remote"), &d.remote, d.remotes).Disabled(d.busy).Changed() {
			d.value = d.remoteURLs[d.remote]
		}
		ui.TextInput(c.Key("op-value"), &d.value).Placeholder("远程地址").Disabled(d.busy)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c.Key("op-remote-save"), "保存地址").Disabled(d.busy || !d.ready || d.remote == "").Clicked() {
				a.runHubAction("修改远程地址", false)
			}
			if ui.Button(c.Key("op-remote-delete"), "删除远程").Disabled(d.busy || !d.ready || d.remote == "").Clicked() {
				a.runHubAction("删除远程", true)
			}
		})
	}
	ui.Text(c, "新增远程").FontSize(12)
	ui.TextInput(c.Key("op-new-remote"), &d.newRemote).Placeholder("名称").Disabled(d.busy)
	ui.TextInput(c.Key("op-new-url"), &d.newURL).Placeholder("远程地址").Disabled(d.busy)
	if ui.Button(c.Key("op-remote-add"), "添加").Disabled(d.busy || !d.ready).Clicked() {
		a.runHubAction("新增远程", false)
	}
	ui.Text(c, "推送到所选远程").FontSize(12)
	ui.TextInput(c.Key("op-name"), &d.name).Placeholder("目标分支").Disabled(d.busy)
	if ui.Button(c.Key("op-push-branch"), "推送分支").Disabled(d.busy || !d.ready || d.remote == "").Clicked() {
		a.runHubAction("推送分支", false)
	}
	ui.TextInput(c.Key("op-ref-name"), &d.refName).Placeholder("标签名").Disabled(d.busy)
	if ui.Button(c.Key("op-push-tag"), "推送标签").Disabled(d.busy || !d.ready || d.remote == "").Clicked() {
		a.runHubAction("推送标签", false)
	}
}
