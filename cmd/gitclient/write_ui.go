package main

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	writeBranch       = "branch"
	writeTag          = "tag"
	writeMessage      = "message"
	writeRenameBranch = "rename-branch"
	writeDeleteBranch = "delete-branch"
	writeDeleteTag    = "delete-tag"
)

type commitWrite struct {
	kind        string
	repo        string
	hash        [20]byte
	subject     string
	oldName     string
	name        string
	message     string
	checkout    bool
	overwrite   bool
	annotated   bool
	track       bool
	trackRemote string
	remotes     []string
	preparing   bool
	busy        bool
	err         string
	gen         int
}

func (a *app) commitRowMenu(c *ui.Context, m *ui.Menu, tab *repoTab, row int) {
	item := tab.rowData(row)
	if item == nil || item.hash == worktreeHash {
		return
	}
	commit := *item
	refs := tab.refs[commit.hash]
	if tab.query.term != "" || tab.query.filePath != "" {
		if m.Item("在图谱中定位").Chosen() {
			a.locateCommitInGraph(tab, commit.hash)
		}
		m.Separator()
	}
	for _, ref := range refs {
		if ref.kind == refBranch {
			branchName := ref.name
			isCurrent := tab != nil && tab.branch == branchName
			if m.Item("检出分支 " + branchName).Disabled(isCurrent).Chosen() {
				a.runCheckoutBranch(tab, branchName)
			}
		}
	}
	if tab.comparison != nil {
		if m.Item("检视此提交").Chosen() {
			a.selectCommit(tab, row)
		}
	}
	if m.Item("检出此提交").Chosen() {
		a.runCheckoutCommit(tab, commit.hash)
	}
	if tab.inspecting != zeroHash && tab.inspecting != commit.hash {
		if m.Item("与当前选中提交比较").Chosen() {
			a.compareCommitPair(tab, tab.inspecting, commit.hash)
		}
	}
	if tab.head != zeroHash && tab.head != commit.hash {
		if m.Item("与 HEAD 比较").Chosen() {
			a.compareCommitPair(tab, commit.hash, tab.head)
		}
	}
	if m.Item("以此提交进行比较…").Chosen() {
		a.openCompareModalWith(tab, hashHex(commit.hash), "")
	}
	m.Separator()
	if m.Item("添加标签…").Chosen() {
		a.openCommitWrite(tab, writeTag, commit)
	}
	if m.Item("新建分支…").Chosen() {
		a.openCommitWrite(tab, writeBranch, commit)
	}
	for _, ref := range refs {
		switch ref.kind {
		case refBranch:
			branchName := ref.name
			if m.Item("重命名分支 " + branchName + "…").Chosen() {
				a.openRefWrite(tab, writeRenameBranch, commit, branchName)
			}
			if m.Item("删除分支 " + branchName + "…").Chosen() {
				a.openRefWrite(tab, writeDeleteBranch, commit, branchName)
			}
		case refTag:
			tagName := ref.name
			if m.Item("删除标签 " + tagName + "…").Chosen() {
				a.openRefWrite(tab, writeDeleteTag, commit, tagName)
			}
		}
	}
	m.Separator()
	if m.Item("修改说明…").Chosen() {
		a.openCommitWrite(tab, writeMessage, commit)
	}
	m.Separator()
	if m.Item("复制哈希").Chosen() {
		c.Services().WriteClipboard(hashHex(commit.hash))
		c.Toast("已复制哈希")
	}
	if m.Item("复制提交信息").Chosen() {
		a.copyCommitInfo(c, tab, commit.hash)
	}
}

func (a *app) branchBadgeMenu(c *ui.Context, m *ui.Menu, tab *repoTab, item commit, branchName string) {
	isCurrent := tab != nil && tab.branch == branchName
	if m.Item("检出分支 " + branchName).Disabled(isCurrent).Chosen() {
		a.runCheckoutBranch(tab, branchName)
	}
	if m.Item("重命名分支…").Chosen() {
		a.openRefWrite(tab, writeRenameBranch, item, branchName)
	}
	if m.Item("删除分支…").Chosen() {
		a.openRefWrite(tab, writeDeleteBranch, item, branchName)
	}
	m.Separator()
	curBranch := tab.branch
	if curBranch == "" || strings.HasPrefix(curBranch, "分离头指针") || curBranch == "尚无提交" {
		curBranch = "HEAD"
	}
	if branchName != curBranch {
		if m.Item("与当前分支 (" + curBranch + ") 比较").Chosen() {
			a.compareRefs(tab, curBranch, branchName, true)
		}
	} else {
		if m.Item("与 HEAD 比较").Chosen() {
			a.compareRefs(tab, "HEAD", branchName, true)
		}
	}
	if m.Item("以此分支进行比较…").Chosen() {
		a.openCompareModalWith(tab, branchName, "")
	}
	m.Separator()
	if m.Item("基于此提交新建分支…").Chosen() {
		a.openCommitWrite(tab, writeBranch, item)
	}
	if m.Item("复制分支名").Chosen() {
		c.Services().WriteClipboard(branchName)
		c.Toast("已复制分支名")
	}
}

func (a *app) remoteBadgeMenu(c *ui.Context, m *ui.Menu, tab *repoTab, item commit, remoteName string) {
	if m.Item("检出并跟踪 " + remoteName + "…").Chosen() {
		a.openCommitWrite(tab, writeBranch, item)
		a.write.track = true
		a.write.trackRemote = remoteName
		if local := remoteLocalName(remoteName); local != "" {
			a.write.name = local
		}
		a.write.checkout = true
	}
	m.Separator()
	curBranch := tab.branch
	if curBranch == "" || strings.HasPrefix(curBranch, "分离头指针") || curBranch == "尚无提交" {
		curBranch = "HEAD"
	}
	if m.Item("与当前分支 (" + curBranch + ") 比较").Chosen() {
		a.compareRefs(tab, curBranch, remoteName, true)
	}
	if m.Item("以此分支进行比较…").Chosen() {
		a.openCompareModalWith(tab, remoteName, "")
	}
	m.Separator()
	if m.Item("复制远程分支名").Chosen() {
		c.Services().WriteClipboard(remoteName)
		c.Toast("已复制远程分支名")
	}
}

func (a *app) tagBadgeMenu(c *ui.Context, m *ui.Menu, tab *repoTab, item commit, tagName string) {
	if m.Item("检出此标签提交").Chosen() {
		a.runCheckoutCommit(tab, item.hash)
	}
	if m.Item("删除标签…").Chosen() {
		a.openRefWrite(tab, writeDeleteTag, item, tagName)
	}
	m.Separator()
	curBranch := tab.branch
	if curBranch == "" || strings.HasPrefix(curBranch, "分离头指针") || curBranch == "尚无提交" {
		curBranch = "HEAD"
	}
	if m.Item("与当前分支 (" + curBranch + ") 比较").Chosen() {
		a.compareRefs(tab, curBranch, tagName, true)
	}
	if m.Item("以此标签进行比较…").Chosen() {
		a.openCompareModalWith(tab, tagName, "")
	}
	m.Separator()
	if m.Item("复制标签名").Chosen() {
		c.Services().WriteClipboard(tagName)
		c.Toast("已复制标签名")
	}
}

func (a *app) runCheckoutBranch(tab *repoTab, name string) {
	if a.write.busy || a.fetch.busy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if tab != nil && tab.branch == name {
		a.setBanner("当前已处于分支 "+name, "")
		a.pendingToast = "当前已处于分支 " + name
		return
	}
	a.write.busy = true
	a.write.gen++
	gen, repo := a.write.gen, tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := checkoutBranch(ctx, repo, name)
		a.post(func() {
			if a.write.gen != gen {
				return
			}
			a.write.busy = false
			if err != nil {
				a.setBanner(err.Error(), repo)
				return
			}
			if t := a.tabByPath(repo); t != nil {
				a.notifySuccess(t, "已切换到分支 "+name)
				a.reload(t)
			}
		})
	}()
}

func (a *app) runCheckoutCommit(tab *repoTab, hash [20]byte) {
	if a.write.busy || a.fetch.busy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	a.write.busy = true
	a.write.gen++
	gen, repo := a.write.gen, tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := checkoutDetached(ctx, repo, hash)
		a.post(func() {
			if a.write.gen != gen {
				return
			}
			a.write.busy = false
			if err != nil {
				a.setBanner(err.Error(), repo)
				return
			}
			if t := a.tabByPath(repo); t != nil {
				a.notifySuccess(t, "已检出提交 "+shortHash(hash))
				a.reload(t)
			}
		})
	}()
}

func (a *app) openRefWrite(tab *repoTab, kind string, item commit, refName string) {
	if a.write.busy || a.fetch.busy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if i := tab.rowIndex(item.hash); i >= 0 {
		a.selectCommit(tab, i)
	}
	a.write = commitWrite{
		kind:    kind,
		repo:    tab.path,
		hash:    item.hash,
		subject: item.subject,
		oldName: refName,
		name:    refName,
		gen:     a.write.gen + 1,
	}
	a.showWrite, a.write.err = true, ""
}

func (a *app) copyCommitInfo(c *ui.Context, tab *repoTab, hash [20]byte) {
	if d := tab.details; d != nil && (d.hash == hashHex(hash) || tab.inspecting == hash) {
		c.Services().WriteClipboard(d.text())
		c.Toast("已复制提交信息")
		return
	}
	svc := c.Services()
	path := tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		details, err := readCommitDetails(ctx, path, hash)
		a.post(func() {
			if err != nil {
				a.setBanner(err.Error(), path)
				return
			}
			svc.WriteClipboard(details.text())
			if t := a.tabByPath(path); t != nil {
				a.notifySuccess(t, "已复制提交信息")
			}
		})
	}()
}

func (a *app) openCommitWrite(tab *repoTab, kind string, item commit) {
	if a.write.busy || a.fetch.busy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if i := tab.rowIndex(item.hash); i >= 0 {
		a.selectCommit(tab, i)
	}
	a.write = commitWrite{
		kind:    kind,
		repo:    tab.path,
		hash:    item.hash,
		subject: item.subject,
		gen:     a.write.gen + 1,
	}
	a.showWrite, a.write.err = true, ""
	if kind == writeBranch {
		prepareBranchWrite(&a.write, tab.refs[item.hash], tab.refs)
		return
	}
	if kind != writeMessage {
		return
	}
	a.write.preparing = true
	gen, path, hash := a.write.gen, tab.path, item.hash
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg, err := readCommitMessage(ctx, path, hash)
		a.post(func() {
			if a.closed || !a.showWrite || a.write.gen != gen {
				return
			}
			a.write.preparing = false
			if err != nil {
				a.write.err = err.Error()
				return
			}
			a.write.message = msg
		})
	}()
}

func prepareBranchWrite(d *commitWrite, commitRefs []refInfo, allRefs map[[20]byte][]refInfo) {
	d.remotes = trackingRemotes(commitRefs)
	if len(d.remotes) == 0 {
		return
	}
	pick, n := preferredTrackingRemote(d.remotes, localBranchNames(allRefs))
	if pick == "" {
		d.trackRemote = d.remotes[0]
		return
	}
	d.trackRemote = pick
	d.name = remoteLocalName(pick)
	if n == 1 {
		d.track = true
		d.checkout = true
	}
}

func writeTitle(kind string) string {
	switch kind {
	case writeTag:
		return "添加标签"
	case writeBranch:
		return "新建分支"
	case writeRenameBranch:
		return "重命名分支"
	case writeDeleteBranch:
		return "删除分支"
	case writeDeleteTag:
		return "删除标签"
	default:
		return "修改说明"
	}
}

func (a *app) commitWriteModal(c *ui.Context) {
	wasOpen := a.showWrite
	ui.Modal(c.Key("commit-write-modal"), &a.showWrite, func() {
		d := &a.write
		ui.Column(c).Width(460).Gap(8).Children(func() {
			ui.Text(c, writeTitle(d.kind)).Bold()
			summary := shortHash(d.hash)
			if d.subject != "" {
				summary += "  " + d.subject
			}
			ui.Text(c, summary).FontSize(12).SingleLine().TextColor(c.Theme().TextMuted).Tooltip(hashHex(d.hash) + "\n" + d.subject)
			disabled := d.preparing || d.busy
			switch d.kind {
			case writeBranch:
				var input ui.Element
				ui.Field(c, "分支名", func() {
					input = ui.TextInput(c.Key("write-branch-name"), &d.name).Placeholder("feature/fix").Disabled(disabled).AutoFocus()
				})
				if len(d.remotes) > 0 {
					label := "跟踪远程分支"
					if d.trackRemote != "" {
						label = "跟踪 " + d.trackRemote
					}
					ui.Checkbox(c.Key("write-track"), &d.track, label).Disabled(disabled)
					if d.track && len(d.remotes) > 1 {
						ui.Select(c.Key("write-track-remote"), &d.trackRemote, d.remotes).Disabled(disabled)
					}
				}
				ui.Checkbox(c.Key("write-checkout"), &d.checkout, "创建后检出").Disabled(disabled)
				ui.Checkbox(c.Key("write-overwrite-branch"), &d.overwrite, "覆盖已有同名分支").Disabled(disabled)
				if input.Submitted() && !disabled {
					a.runCommitWrite()
				}
			case writeRenameBranch:
				var input ui.Element
				ui.Field(c, "新分支名", func() {
					input = ui.TextInput(c.Key("write-rename-branch"), &d.name).Placeholder(d.oldName).Disabled(disabled).AutoFocus()
				})
				ui.Checkbox(c.Key("write-overwrite-rename"), &d.overwrite, "覆盖已有同名分支").Disabled(disabled)
				if input.Submitted() && !disabled {
					a.runCommitWrite()
				}
			case writeDeleteBranch:
				ui.Text(c, "确定删除本地分支 "+d.name+" 吗？").FontSize(13)
				ui.Checkbox(c.Key("write-force-delete-branch"), &d.overwrite, "强制删除（即使尚未完全合并）").Disabled(disabled)
			case writeDeleteTag:
				ui.Text(c, "确定删除本地标签 "+d.name+" 吗？").FontSize(13)
			case writeTag:
				var input ui.Element
				ui.Field(c, "标签名", func() {
					input = ui.TextInput(c.Key("write-tag-name"), &d.name).Placeholder("v1.0.0").Disabled(disabled).AutoFocus()
				})
				ui.Checkbox(c.Key("write-annotated"), &d.annotated, "附注标签").Disabled(disabled)
				if d.annotated {
					ui.Field(c, "消息", func() {
						ui.TextInput(c.Key("write-tag-message"), &d.message).Placeholder("标签说明").Disabled(disabled)
					})
				}
				ui.Checkbox(c.Key("write-overwrite-tag"), &d.overwrite, "覆盖已有同名标签").Disabled(disabled)
				if input.Submitted() && !disabled {
					a.runCommitWrite()
				}
			default:
				if d.preparing {
					ui.Text(c, "正在读取说明…").TextColor(c.Theme().TextMuted)
				} else {
					ui.Text(c, "会生成新的提交哈希；若不是 HEAD，其后的提交也会改写。").FontSize(12).TextColor(c.Theme().TextMuted)
					ui.TextArea(c.Key("write-message"), &d.message).Height(160).Disabled(disabled).AutoFocus()
				}
			}
			if d.err != "" {
				ui.Text(c, d.err).TextColor(c.Theme().Danger)
			}
			if d.busy {
				ui.Text(c, "Git 正在执行…").TextColor(c.Theme().TextMuted)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("write-cancel"), "取消").Disabled(d.busy).Clicked() {
					a.showWrite = false
				}
				label := "创建"
				switch d.kind {
				case writeMessage, writeRenameBranch:
					label = "保存"
				case writeDeleteBranch, writeDeleteTag:
					label = "删除"
				}
				if ui.PrimaryButton(c.Key("write-apply"), label).Disabled(disabled).Clicked() {
					a.runCommitWrite()
				}
			})
		})
	})
	if wasOpen && !a.showWrite && a.write.busy {
		a.showWrite = true
	}
}

func (a *app) runCommitWrite() {
	d := &a.write
	if d.preparing || d.busy {
		return
	}
	d.err = ""
	kind, repo, hash := d.kind, d.repo, d.hash
	oldName, name, message := d.oldName, d.name, d.message
	checkout, overwrite, annotated := d.checkout, d.overwrite, d.annotated
	trackRemote := ""
	if kind == writeBranch && d.track {
		for _, remote := range d.remotes {
			if remote == d.trackRemote {
				trackRemote = remote
				break
			}
		}
	}
	d.busy = true
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var err error
		var newHash [20]byte
		var notice string
		switch kind {
		case writeBranch:
			err = createBranch(ctx, repo, name, hash, checkout, overwrite, trackRemote)
			if err == nil {
				notice = "已新建分支 " + name
			}
		case writeRenameBranch:
			err = renameBranch(ctx, repo, oldName, name, overwrite)
			if err == nil {
				notice = "已重命名分支为 " + name
			}
		case writeDeleteBranch:
			err = deleteBranch(ctx, repo, name, overwrite)
			if err == nil {
				notice = "已删除分支 " + name
			}
		case writeDeleteTag:
			err = deleteTag(ctx, repo, name)
			if err == nil {
				notice = "已删除标签 " + name
			}
		case writeTag:
			err = createTag(ctx, repo, name, message, hash, annotated, overwrite)
			if err == nil {
				notice = "已添加标签 " + name
			}
		default:
			newHash, err = rewordCommit(ctx, repo, hash, message)
			if err == nil {
				notice = "已修改说明"
			}
		}
		a.post(func() {
			if a.write.gen != gen {
				return
			}
			a.write.busy = false
			if err != nil {
				a.showWrite = false
				a.setBanner(err.Error(), repo)
				return
			}
			a.showWrite = false
			t := a.tabByPath(repo)
			if t == nil {
				return
			}
			a.notifySuccess(t, notice)
			if kind == writeMessage && newHash != zeroHash {
				t.inspecting = newHash
				t.selected = -1
				t.selection.Clear()
				t.restore = &historyLocation{query: t.query, hash: newHash, top: newHash}
			}
			a.reload(t)
		})
	}()
}

func (a *app) tabByPath(path string) *repoTab {
	for _, t := range a.tabs {
		if t != nil && !t.closed && t.path == path {
			return t
		}
	}
	return nil
}
