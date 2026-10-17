package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type branchChoice struct {
	ref      string
	label    string
	selected bool
}

type commitComparison struct {
	from, to [20]byte
}

func (a *app) setHistory(t *repoTab, scope string, refs []string, target [20]byte) {
	t.releaseHeavy()
	t.scope, t.scopeChoice = scope, scope
	t.historyRefs = append([]string(nil), refs...)
	t.resumeCount, t.resumeRow = t.options.First, 0
	t.wantHash, t.wantDirty = target, false
	t.comparison = nil
	t.historyPath, t.searchText = "", ""
	t.refErr = ""
	a.reload(t)
}

func (a *app) showComparison(t *repoTab, from, to [20]byte) {
	t.stashView = nil
	t.comparison = &commitComparison{from: from, to: to}
	t.selected, t.commitApplied = -1, -1
	t.wantHash, t.wantDirty = zeroHash, false
	t.selection.Clear()
	a.onCommitSelected(t)
}

func (a *app) compareRefs(t *repoTab, from, to string) {
	if t.navCancel != nil {
		t.navCancel()
	}
	t.navGen++
	navGen, repoGen, path := t.navGen, t.generation, t.path
	a.compareErr, t.refBusy = "", true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.navCancel = cancel
	go func() {
		defer cancel()
		left, err := resolveCommit(ctx, path, from)
		var right [20]byte
		if err == nil {
			right, err = resolveCommit(ctx, path, to)
		}
		a.post(func() {
			if t.generation != repoGen || t.navGen != navGen || !a.showCompare || a.compareRepo != path {
				return
			}
			t.refBusy = false
			if err != nil {
				a.compareErr = err.Error()
				return
			}
			a.showCompare = false
			a.showComparison(t, left, right)
		})
	}()
}

func (a *app) comparisonModal(c *ui.Context) {
	wasOpen := a.showCompare
	ui.Modal(c.Key("compare-modal"), &a.showCompare, func() {
		ui.Column(c).Width(460).Gap(8).Children(func() {
			ui.Text(c, "比较提交或分支").Bold()
			ui.Text(c, "显示从起点到终点的文件变化，不检出分支。").TextColor(c.Theme().TextMuted)
			ui.Text(c, "起点")
			ui.TextInput(c.Key("compare-from"), &a.compareFrom).Placeholder("起点 ref / SHA").Width(420)
			ui.Text(c, "终点")
			input := ui.TextInput(c.Key("compare-to"), &a.compareTo).Placeholder("终点 ref / SHA，或起点..终点").Width(420)
			if a.compareErr != "" {
				ui.Text(c, a.compareErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("compare-cancel"), "取消").Clicked() {
					a.showCompare = false
				}
				t := a.tabByPath(a.compareRepo)
				busy := t == nil || t.refBusy
				if ui.PrimaryButton(c.Key("compare-apply"), "开始比较").Disabled(busy).Clicked() || (input.Submitted() && !busy) {
					from, to := a.compareFrom, a.compareTo
					if strings.Contains(to, "..") {
						parts := strings.Split(to, "..")
						if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], ".") {
							a.compareErr = "请输入两个 ref，例如 main..feature；不支持三点比较"
							return
						}
						from, to = parts[0], parts[1]
					}
					a.compareRefs(t, from, to)
				}
			})
		})
	})
	if wasOpen && !a.showCompare {
		if t := a.tabByPath(a.compareRepo); t != nil {
			if t.navCancel != nil {
				t.navCancel()
			}
			t.navGen++
			t.refBusy = false
		}
	}
}

func (a *app) openRef(t *repoTab, ref string) {
	if t.navCancel != nil {
		t.navCancel()
	}
	t.navGen++
	navGen, repoGen, path := t.navGen, t.generation, t.path
	t.refErr, t.refBusy = "", true
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.navCancel = cancel
	go func() {
		defer cancel()
		hash, err := resolveCommit(ctx, path, ref)
		a.post(func() {
			if t.generation != repoGen || t.navGen != navGen {
				return
			}
			t.refBusy = false
			if err != nil {
				t.refErr = err.Error()
				return
			}
			for i := range t.commits {
				if t.commits[i].hash == hash {
					t.selected = t.commitRow(i)
					t.list.ScrollIntoView(t.selected)
					return
				}
			}
			a.setHistory(t, "指定 ref", []string{hashHex(hash)}, hash)
		})
	}()
}

func (a *app) beginBranchFilter(t *repoTab) {
	a.filterRepo, a.filterErr = t.path, ""
	a.filterRows = nil
	a.filterList = ui.ListState{}
	for _, refs := range t.refs {
		for _, ref := range refs {
			prefix := "refs/heads/"
			if ref.kind == refTag {
				continue
			}
			if ref.kind == refRemote {
				prefix = "refs/remotes/"
			}
			full := prefix + ref.name
			selected := false
			for _, chosen := range t.historyRefs {
				selected = selected || chosen == full
			}
			a.filterRows = append(a.filterRows, branchChoice{ref: full, label: ref.name, selected: selected})
		}
	}
	sort.Slice(a.filterRows, func(i, j int) bool { return a.filterRows[i].ref < a.filterRows[j].ref })
	a.showFilter = true
}

func (a *app) branchFilterModal(c *ui.Context) {
	ui.Modal(c.Key("branch-filter"), &a.showFilter, func() {
		ui.Column(c).Width(420).Gap(8).Children(func() {
			ui.Text(c, "选择要查看的分支").Bold()
			ui.Text(c, "只筛选历史，不检出分支。").TextColor(c.Theme().TextMuted)
			ui.List(c.Key("branch-choices"), &a.filterList, len(a.filterRows), func(i int) {
				row := &a.filterRows[i]
				label := row.label
				if strings.HasPrefix(row.ref, "refs/remotes/") {
					label += "（远程）"
				} else {
					label += "（本地）"
				}
				ui.Checkbox(c.Key(row.ref), &row.selected, label).Padding(4, 0)
			}).Height(260)
			if a.filterErr != "" {
				ui.Text(c, a.filterErr).TextColor(c.Theme().Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("filter-cancel"), "取消").Clicked() {
					a.showFilter = false
				}
				if ui.PrimaryButton(c.Key("filter-apply"), "应用筛选").Clicked() {
					var refs []string
					for _, row := range a.filterRows {
						if row.selected {
							refs = append(refs, row.ref)
						}
					}
					if len(refs) == 0 {
						a.filterErr = "至少选择一个分支"
					} else if t := a.tabByPath(a.filterRepo); t != nil && t == a.activeTab() {
						a.showFilter = false
						a.setHistory(t, "多选分支", refs, zeroHash)
					}
				}
			})
		})
	})
}
