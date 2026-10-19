package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// Operation data is captured when the dialog opens; switching tabs cannot change its target.
type operation struct {
	Expected                                                                                     string
	ParentHashes                                                                                 []string
	Kind, Repo, Target, Name, Message, Mode, Mainline                                            string
	Checkout, Overwrite, NoFF, Squash, NoCommit, RecordOrigin                                    bool
	Upstream, Prune, PruneTags, Annotated, PushNow, RemoteDelete, IncludeUntracked, RestoreIndex bool
	Remotes                                                                                      []string
	Selected                                                                                     map[string]bool
	Preparing, Busy, ConfirmHard                                                                 bool
	Error, Output                                                                                string
	RawError                                                                                     string
	ShowOutput, CancelRequested, NotedDismiss                                                    bool
	Recovery                                                                                     []string
	Cancel                                                                                       context.CancelFunc
}

func (o *operation) remoteNames() []string {
	var out []string
	for _, r := range o.Remotes {
		if o.Selected[r] {
			out = append(out, r)
		}
	}
	return out
}
func validName(name string) error {
	if msg := validBranch(name); msg != "" {
		return errors.New(msg)
	}
	if strings.ContainsAny(name, "\r\n\t") || name == "@" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return errors.New("引用名称不合法")
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return errors.New("引用名称不合法")
		}
	}
	return nil
}
func safeArg(s string) error {
	if s == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, "\x00\r\n") {
		return errors.New("目标不能为空或以 - 开头")
	}
	return nil
}

func operationCommands(o *operation) ([][]string, error) {
	target := o.Target
	needsTarget := map[string]bool{"切换分支": true, "检出远程分支": true, "新建分支": true, "删除本地分支": true, "删除远程分支": true, "合并": true, "检出提交": true, "Cherry-pick": true, "Revert": true, "Reset": true, "添加标签": true, "删除标签": true, "推送标签": true, "Apply Stash": true, "Pop Stash": true, "Drop Stash": true}
	if needsTarget[o.Kind] {
		if err := safeArg(target); err != nil {
			return nil, err
		}
	}
	remotes := o.remoteNames()
	for _, r := range remotes {
		if err := safeArg(r); err != nil {
			return nil, err
		}
	}
	var cmds [][]string
	switch o.Kind {
	case "切换分支":
		cmds = append(cmds, []string{"checkout", target})
	case "检出远程分支":
		if err := validName(o.Name); err != nil {
			return nil, err
		}
		if o.Mode == "检出已有分支并拉取" {
			remote, branch, ok := strings.Cut(target, "/")
			if !ok {
				return nil, errors.New("远程分支应为 remote/branch")
			}
			cmds = append(cmds, []string{"checkout", o.Name}, []string{"pull", "--no-rebase", "--no-edit", remote, branch})
		} else {
			cmds = append(cmds, []string{"checkout", "--track", "-b", o.Name, target})
		}
	case "新建分支":
		if err := validName(o.Name); err != nil {
			return nil, err
		}
		args := []string{"branch"}
		if o.Overwrite {
			args = append(args, "-f")
		}
		cmds = append(cmds, append(args, o.Name, target))
		if o.Checkout {
			cmds = append(cmds, []string{"checkout", o.Name})
		}
	case "删除本地分支":
		flag := "-d"
		if o.Overwrite {
			flag = "-D"
		}
		cmds = append(cmds, []string{"branch", flag, target})
	case "删除远程分支":
		remote, branch, ok := strings.Cut(target, "/")
		if !ok {
			return nil, errors.New("远程分支应为 remote/branch")
		}
		if err := safeArg(remote); err != nil {
			return nil, err
		}
		cmds = append(cmds, []string{"push", remote, "--delete", branch})
	case "合并":
		args := []string{"merge", "--no-edit"}
		if o.Squash {
			args = append(args, "--squash")
		} else if o.NoFF {
			args = append(args, "--no-ff")
		}
		if o.NoCommit {
			args = append(args, "--no-commit")
		}
		cmds = append(cmds, append(args, target))
	case "检出提交":
		cmds = append(cmds, []string{"checkout", "--detach", target})
	case "Cherry-pick", "Revert":
		if len(o.ParentHashes) > 1 && o.Mainline == "" {
			return nil, errors.New("这是合并提交，请指定父提交序号")
		}
		args := []string{strings.ToLower(o.Kind)}
		if o.Kind == "Revert" {
			args = append(args, "--no-edit")
		}
		if o.Kind == "Cherry-pick" {
			if o.RecordOrigin {
				args = append(args, "-x")
			}
			if o.NoCommit {
				args = append(args, "--no-commit")
			}
		}
		if o.Mainline != "" {
			n, err := strconv.Atoi(o.Mainline)
			if err != nil || n < 1 {
				return nil, errors.New("父提交序号须从 1 开始")
			}
			args = append(args, "-m", strconv.Itoa(n))
		}
		cmds = append(cmds, append(args, target))
	case "Reset":
		if o.Mode != "soft" && o.Mode != "mixed" && o.Mode != "hard" {
			return nil, errors.New("请选择 reset 模式")
		}
		cmds = append(cmds, []string{"reset", "--" + o.Mode, target})
	case "推送":
		if len(remotes) == 0 {
			return nil, errors.New("请选择至少一个远程")
		}
		if err := safeArg(o.Name); err != nil {
			return nil, err
		}
		for _, r := range remotes {
			args := []string{"push"}
			if o.Upstream {
				args = append(args, "--set-upstream")
			}
			switch o.Mode {
			case "force":
				args = append(args, "--force")
			case "force-with-lease":
				args = append(args, "--force-with-lease")
			}
			cmds = append(cmds, append(args, r, o.Name))
		}
	case "拉取":
		if len(remotes) != 1 {
			return nil, errors.New("拉取时请选择一个远程")
		}
		args := []string{"pull", "--no-rebase", "--no-edit"}
		if o.Squash {
			args = append(args, "--squash")
		} else if o.NoFF {
			args = append(args, "--no-ff")
		}
		if err := safeArg(o.Name); err != nil {
			return nil, err
		}
		cmds = append(cmds, append(args, remotes[0], o.Name))
	case "Fetch":
		args := []string{"fetch", "--all"}
		if o.Prune || o.PruneTags {
			args = append(args, "--prune")
		}
		if o.PruneTags {
			args = append(args, "--prune-tags")
		}
		cmds = append(cmds, args)
	case "添加标签":
		if err := validName(o.Name); err != nil {
			return nil, err
		}
		args := []string{"tag"}
		if o.Annotated {
			if strings.TrimSpace(o.Message) == "" {
				return nil, errors.New("请输入附注标签消息")
			}
			args = append(args, "-a", "-m", o.Message)
		}
		cmds = append(cmds, append(args, o.Name, target))
		if o.PushNow {
			if len(remotes) == 0 {
				return nil, errors.New("请选择推送远程")
			}
			for _, r := range remotes {
				cmds = append(cmds, []string{"push", r, "refs/tags/" + o.Name})
			}
		}
	case "删除标签":
		cmds = append(cmds, []string{"tag", "-d", target})
		if o.RemoteDelete {
			if len(remotes) == 0 {
				return nil, errors.New("请选择要删除标签的远程")
			}
			for _, r := range remotes {
				cmds = append(cmds, []string{"push", r, ":refs/tags/" + target})
			}
		}
	case "推送标签":
		if len(remotes) == 0 {
			return nil, errors.New("请选择至少一个远程")
		}
		for _, r := range remotes {
			cmds = append(cmds, []string{"push", r, "refs/tags/" + target})
		}
	case "新建 Stash":
		args := []string{"stash", "push"}
		if o.IncludeUntracked {
			args = append(args, "--include-untracked")
		}
		if o.Message != "" {
			args = append(args, "-m", o.Message)
		}
		cmds = append(cmds, args)
	case "Apply Stash", "Pop Stash":
		verb := "apply"
		if o.Kind == "Pop Stash" {
			verb = "pop"
		}
		args := []string{"stash", verb}
		if o.RestoreIndex {
			args = append(args, "--index")
		}
		cmds = append(cmds, append(args, target))
	case "Drop Stash":
		cmds = append(cmds, []string{"stash", "drop", target})
	case "中止合并":
		cmds = append(cmds, []string{"merge", "--abort"})
	case "中止 Cherry-pick":
		cmds = append(cmds, []string{"cherry-pick", "--abort"})
	case "中止 Revert":
		cmds = append(cmds, []string{"revert", "--abort"})
	case "外部合并工具":
		cmds = append(cmds, []string{"mergetool", "--no-prompt"})
	default:
		return nil, fmt.Errorf("不支持的操作：%s", o.Kind)
	}
	return cmds, nil
}

func (a *app) beginOperation(t *repoTab, kind, target string) {
	if a.op != nil && a.op.Busy {
		a.showOperation = true
		a.banner = "请等待或取消正在执行的操作"
		return
	}
	o := &operation{Kind: kind, Repo: t.path, Target: target, Preparing: true, NoFF: true, RecordOrigin: true, Mode: "普通", Selected: map[string]bool{}}
	if kind == "Reset" {
		o.Mode = "mixed"
	}
	if kind == "检出远程分支" {
		_, o.Name, _ = strings.Cut(target, "/")
		o.Mode = "建立跟踪分支"
	}
	if kind == "推送" || kind == "拉取" {
		o.Name = t.branch
	}
	for _, s := range t.stashes {
		if s.stash == target {
			o.Expected = hashHex(s.hash)
		}
	}
	a.op = o
	a.showOperation = true
	root := t.path
	branch := t.branch
	go func() {
		out, err := runGit(context.Background(), root, "remote")
		tags, _ := runGit(context.Background(), root, "for-each-ref", "--sort=-creatordate", "--count=1", "--format=%(refname:short)", "refs/tags")
		upstreamRemote, upstreamBranch := "", ""
		if kind == "拉取" {
			remote, e := runGit(context.Background(), root, "config", "--get", "branch."+branch+".remote")
			if e == nil {
				upstreamRemote = strings.TrimSpace(string(remote))
			}
			merge, e := runGit(context.Background(), root, "config", "--get", "branch."+branch+".merge")
			if e == nil {
				upstreamBranch = strings.TrimPrefix(strings.TrimSpace(string(merge)), "refs/heads/")
			}
		}
		var parents []string
		if kind == "Cherry-pick" || kind == "Revert" {
			raw, e := runGit(context.Background(), root, "rev-list", "--parents", "-n", "1", target)
			if e == nil {
				fields := strings.Fields(string(raw))
				if len(fields) > 1 {
					parents = fields[1:]
				}
			}
		}
		a.post(func() {
			o.Preparing = false
			o.ParentHashes = parents
			if a.op != o {
				return
			}
			if err != nil {
				o.Error = err.Error()
				return
			}
			o.Remotes = strings.Fields(string(out))
			if len(o.Remotes) > 0 {
				o.Selected[o.Remotes[0]] = true
			}
			if upstreamRemote != "" && upstreamRemote != "." {
				for _, r := range o.Remotes {
					o.Selected[r] = r == upstreamRemote
				}
			}
			if upstreamBranch != "" {
				o.Name = upstreamBranch
			}
			a.recentTag = strings.TrimSpace(string(tags))
		})
	}()
}

func (a *app) operationModal(c *ui.Context) {
	ui.Modal(c.Key("operation"), &a.showOperation, func() {
		o := a.op
		if o == nil {
			return
		}
		firstInput := true
		input := func(key string, value *string, placeholder string) ui.Element {
			e := ui.TextInput(c.Key(key), value).Placeholder(placeholder).OnSubmit(func() { a.runOperation(o) })
			if firstInput {
				e.AutoFocus()
				firstInput = false
			}
			return e
		}
		panel := ui.Column(c).Width(550).Gap(8).Children(func() {
			ui.Text(c, operationTitle(o.Kind)).Bold()
			ui.Text(c, o.Repo).FontSize(12).SingleLine().Tooltip(o.Repo)
			if o.Error != "" {
				ui.Text(c, o.Error).Selectable().TextColor(c.Theme().Danger)
			}
			if !o.Busy && len(o.Recovery) > 0 {
				ui.Row(c).Gap(8).Children(func() {
					for _, kind := range o.Recovery {
						if ui.Button(c.Key("recover-"+kind), kind+"…").Clicked() {
							a.beginOperation(&repoTab{path: o.Repo}, kind, "")
						}
					}
					if ui.Button(c.Key("recover-terminal"), "打开终端").Clicked() {
						a.openTerminal(o.Repo)
					}
				})
			}
			fieldMax := float32(360)
			if o.Error != "" {
				fieldMax = 160
			}
			ui.Scroll(c.Key("operation-fields")).MaxHeight(fieldMax).Disabled(o.Preparing || o.Busy).Children(func() {
				ui.Column(c).Gap(8).Children(func() {
					if len(o.ParentHashes) > 1 {
						for i, p := range o.ParentHashes {
							ui.Textf(c, "父提交 %d：%s", i+1, p)
						}
					}
					if o.Target != "" {
						ui.Text(c, "目标："+o.Target).Selectable()
					}
					switch o.Kind {
					case "新建分支":
						input("op-name", &o.Name, "分支名").Width(500)
						ui.Checkbox(c.Key("op-checkout"), &o.Checkout, "创建后检出")
						ui.Checkbox(c.Key("op-overwrite"), &o.Overwrite, "覆盖已有同名分支（移动分支指针）")
					case "删除本地分支":
						ui.Text(c, "删除后可通过 reflog 查找尚未回收的提交。").TextColor(c.Theme().Danger)
						ui.Checkbox(c.Key("op-force-delete"), &o.Overwrite, "强制删除未合并的分支")
					case "删除远程分支", "删除标签", "Drop Stash":
						ui.Text(c, "请确认删除目标，其他协作者可能仍在使用它。").TextColor(c.Theme().Danger)
					case "检出远程分支":
						input("op-local", &o.Name, "本地分支名")
						ui.Select(c.Key("op-checkout-mode"), &o.Mode, []string{"建立跟踪分支", "检出已有分支并拉取"})
					case "合并", "拉取":
						ui.Checkbox(c.Key("op-noff"), &o.NoFF, "no-ff（保留合并提交）")
						ui.Checkbox(c.Key("op-squash"), &o.Squash, "squash（合并改动，不创建合并提交）")
						if o.Kind == "合并" {
							ui.Checkbox(c.Key("op-no-commit"), &o.NoCommit, "no-commit（合并后不提交）")
						}
					case "Cherry-pick":
						ui.Checkbox(c.Key("op-record"), &o.RecordOrigin, "-x（记录来源提交）")
						ui.Checkbox(c.Key("op-no-commit"), &o.NoCommit, "no-commit（应用后不提交）")
						input("op-mainline", &o.Mainline, "合并提交必填：父提交序号，例如 1")
					case "Revert":
						input("op-mainline", &o.Mainline, "合并提交必填：父提交序号，例如 1")
					case "Reset":
						ui.Select(c.Key("op-reset"), &o.Mode, []string{"soft", "mixed", "hard"})
						ui.Text(c, "soft 保留暂存；mixed 保留工作区；hard 丢弃受跟踪文件的未提交改动。").TextColor(c.Theme().Danger)
					case "推送":
						input("op-push-branch", &o.Name, "本地分支名")
						ui.Checkbox(c.Key("op-upstream"), &o.Upstream, "设置 upstream")
						ui.Select(c.Key("op-push-mode"), &o.Mode, []string{"普通", "force-with-lease", "force"})
						if o.Mode != "普通" {
							ui.Text(c, "强制推送可能改写远程历史。force-with-lease 会检查远程是否被更新。").TextColor(c.Theme().Danger)
						}
					case "Fetch":
						ui.Checkbox(c.Key("op-prune"), &o.Prune, "prune（清理失效远程引用）")
						ui.Checkbox(c.Key("op-prune-tags"), &o.PruneTags, "prune tags（删除本地失效标签，同时启用 prune）")
					case "添加标签":
						input("op-tag-name", &o.Name, "标签名")
						if a.recentTag != "" {
							ui.Text(c, "最近标签："+a.recentTag)
						}
						ui.Checkbox(c.Key("op-annotated"), &o.Annotated, "附注标签")
						if o.Annotated {
							input("op-tag-message", &o.Message, "标签消息")
						}
						ui.Checkbox(c.Key("op-push-now"), &o.PushNow, "创建后立即推送")
					case "新建 Stash":
						input("op-stash-message", &o.Message, "Stash 消息（可选）")
						ui.Checkbox(c.Key("op-untracked"), &o.IncludeUntracked, "包含未跟踪文件")
					case "Apply Stash", "Pop Stash":
						ui.Checkbox(c.Key("op-index"), &o.RestoreIndex, "恢复 index（暂存状态）")
					}
					if o.Kind == "删除标签" {
						ui.Checkbox(c.Key("op-remote-delete"), &o.RemoteDelete, "同时删除所选远程的标签")
					}
					if o.Kind == "拉取" {
						input("op-pull-branch", &o.Name, "远程分支名")
					}
					if o.Kind == "推送" || o.Kind == "拉取" || o.Kind == "推送标签" || o.Kind == "添加标签" && o.PushNow || o.Kind == "删除标签" && o.RemoteDelete {
						ui.Text(c, "远程（可多选；拉取只选一个）")
						for _, r := range o.Remotes {
							r := r
							selected := o.Selected[r]
							ui.Checkbox(c.Key("remote-"+r), &selected, r).OnClick(func() { o.Selected[r] = !o.Selected[r] })
						}
						if len(o.Remotes) == 0 {
							ui.Text(c, "此仓库没有配置远程")
						}
					}
				})
			})
			if o.RawError != "" || o.Output != "" {
				ui.Checkbox(c.Key("op-show-output"), &o.ShowOutput, "查看 Git 原始输出")
				if ui.Button(c.Key("op-copy-output"), "复制输出").Clicked() {
					c.WriteClipboard(o.Output + "\n" + o.RawError)
				}
			}
			if o.ShowOutput && (o.Output != "" || o.RawError != "") {
				ui.Scroll(c.Key("op-output")).MaxHeight(130).Children(func() { ui.Text(c, o.Output+"\n"+o.RawError).Selectable() })
			}
			if o.Preparing {
				ui.Text(c, "正在读取远程和提交信息…")
			} else if o.Busy {
				if o.CancelRequested {
					ui.Text(c, "已请求取消，正在等待 Git 退出；已执行的步骤会保留。")
				} else {
					ui.Text(c, "Git 正在执行… 可取消；已执行的步骤会保留。")
				}
			} else if o.Error == "" {
				ui.Text(c, "执行前请核对仓库、目标和选项。").FontSize(12)
			}
			if o.ConfirmHard {
				ui.Text(c, "再次确认：hard reset 会永久丢弃受跟踪文件的未提交改动。").TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("op-close"), "关闭").Disabled(o.Busy).Clicked() {
					a.showOperation = false
				}
				if o.Busy {
					if ui.Button(c.Key("op-cancel"), "取消执行").Clicked() {
						o.CancelRequested = true
						o.Cancel()
					}
				} else {
					label := "确认执行"
					if o.Error != "" {
						label = "重新执行"
					}
					if o.ConfirmHard {
						label = "丢弃改动并执行 hard reset"
					}
					button := ui.PrimaryButton(c.Key("op-run"), label).Disabled(o.Preparing)
					if firstInput {
						button.AutoFocus()
					}
					if button.Clicked() {
						a.runOperation(o)
					}
				}
			})
		})
		if !o.Preparing && !o.Busy && panel.OverlayShortcut(0, ui.KeyEnter) {
			a.runOperation(o)
		}
	})
}

func (a *app) runOperation(o *operation) {
	if o.Preparing || o.Busy {
		return
	}
	commands, err := operationCommands(o)
	if err != nil {
		o.Error = err.Error()
		return
	}
	if o.Kind == "Reset" && o.Mode == "hard" && !o.ConfirmHard {
		o.ConfirmHard = true
		return
	}
	if a.writeBusy[o.Repo] {
		o.Error = "此仓库已有写操作正在执行，请等待或取消它"
		return
	}
	if a.writeBusy == nil {
		a.writeBusy = map[string]bool{}
	}
	a.writeBusy[o.Repo] = true
	o.Busy = true
	o.Error = ""
	o.Output = ""
	o.RawError = ""
	o.CancelRequested = false
	o.Recovery = nil
	ctx, cancel := context.WithCancel(context.Background())
	o.Cancel = cancel
	go func() {
		defer cancel()
		var err error
		if o.Expected != "" {
			resolved, e := resolveCommit(ctx, o.Repo, o.Target)
			if e != nil {
				err = e
			} else if hashHex(resolved) != o.Expected {
				err = errStashChanged
			}
		}
		for i, args := range commands {
			if err != nil {
				break
			}
			var out string
			out, err = runWriteGit(ctx, o.Repo, args, a.askCredential)
			status := "已完成"
			if err != nil {
				status = "未完成"
			}
			receipt := fmt.Sprintf("步骤 %d/%d · %s · git %s\n%s", i+1, len(commands), status, redactGitOutput(strings.Join(args, " ")), out)
			a.post(func() { o.Output = trimOutput(o.Output + "\n" + receipt) })
			if err != nil {
				break
			}
		}
		recovery := []string(nil)
		if err != nil {
			stateCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
			recovery = pendingOperations(stateCtx, o.Repo)
			done()
		}
		summary := operationFailure(err, ctx.Err(), recovery)
		a.post(func() {
			o.Busy = false
			o.Cancel = nil
			delete(a.writeBusy, o.Repo)
			if err != nil {
				o.Error = summary
				o.RawError = redactGitOutput(err.Error())
				o.Recovery = recovery
				a.banner = repoTitle(o.Repo) + " · " + operationTitle(o.Kind) + "：" + summary
				if a.op == o {
					a.showOperation = true
				}
			} else {
				a.setFlash(repoTitle(o.Repo) + " · " + operationTitle(o.Kind) + "已完成")
				if a.op == o {
					a.showOperation = false
				}
			}
			if t := a.tabByPath(o.Repo); t != nil && t == a.activeTab() {
				a.reload(t)
			} else if t != nil {
				t.releaseHeavy()
			}
		})
	}()
}
func trimOutput(s string) string {
	const limit = 64 << 10
	if len(s) > limit {
		return "（前部输出已省略）\n" + s[len(s)-limit:]
	}
	return strings.TrimSpace(s)
}

func (a *app) refMenu(c *ui.Context, m *ui.Menu, t *repoTab, r refInfo) {
	if m.Item("复制名称").Chosen() {
		c.WriteClipboard(r.name)
	}
	if r.kind == refRemote && strings.HasSuffix(r.name, "/HEAD") {
		return
	}
	if r.kind == refTag {
		if m.Item("删除标签…").Chosen() {
			a.beginOperation(t, "删除标签", r.name)
		}
		if m.Item("推送标签…").Chosen() {
			a.beginOperation(t, "推送标签", r.name)
		}
		return
	}
	kind := "切换分支"
	if r.kind == refRemote {
		kind = "检出远程分支"
	}
	if m.Item(kind + "…").Chosen() {
		a.beginOperation(t, kind, r.name)
	}
	if m.Item("合并到当前分支…").Chosen() {
		target := "refs/heads/" + r.name
		if r.kind == refRemote {
			target = "refs/remotes/" + r.name
		}
		a.beginOperation(t, "合并", target)
	}
	kind = "删除本地分支"
	if r.kind == refRemote {
		kind = "删除远程分支"
	}
	if m.Item(kind + "…").Chosen() {
		a.beginOperation(t, kind, r.name)
	}
}
