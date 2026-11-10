package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type fetchOpts struct {
	prune     bool
	pruneTags bool
}

type fetchDialog struct {
	repo      string
	remotes   []string
	prune     bool
	pruneTags bool
	preparing bool
	busy      bool
	err       string
	gen       int
	cancel    context.CancelFunc
}

func listRemotes(ctx context.Context, dir string) ([]string, error) {
	out, err := runGit(ctx, dir, "remote")
	if err != nil {
		return nil, err
	}
	var remotes []string
	for _, name := range strings.Fields(string(out)) {
		if name != "" {
			remotes = append(remotes, name)
		}
	}
	return remotes, nil
}

func fetchAll(ctx context.Context, dir string, opts fetchOpts) error {
	remotes, err := listRemotes(ctx, dir)
	if err != nil {
		return err
	}
	if len(remotes) == 0 {
		return errors.New("没有配置远程")
	}
	args := []string{"fetch", "--all"}
	if opts.prune || opts.pruneTags {
		args = append(args, "--prune")
	} else {
		args = append(args, "--no-prune")
	}
	if opts.pruneTags {
		args = append(args, "--prune-tags")
	} else {
		args = append(args, "--no-prune-tags")
	}
	_, err = runGit(ctx, dir, args...)
	return err
}

func (a *app) quickFetch(tab *repoTab) {
	if tab == nil || tab.unavailable {
		return
	}
	tab.stopAutoFetch()
	if a.write.busy || a.fetch.busy || tab.commitBusy || tab.syncBusy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if a.fetch.cancel != nil {
		a.fetch.cancel()
	}
	repo := tab.path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	gen := a.fetch.gen + 1
	a.fetch = fetchDialog{
		repo:   repo,
		busy:   true,
		gen:    gen,
		cancel: cancel,
	}
	a.showFetch = false
	tab.lastFetch = time.Now()
	tab.notice = "正在获取远程更新…"
	go func() {
		defer cancel()
		err := fetchAll(ctx, repo, fetchOpts{})
		a.post(func() {
			if a.fetch.gen != gen {
				return
			}
			a.fetch.busy = false
			a.fetch.cancel = nil
			t := a.tabByPath(repo)
			if err != nil {
				if t != nil {
					t.notice = ""
				}
				if !errors.Is(ctx.Err(), context.Canceled) {
					a.setBanner(err.Error(), repo)
				}
				return
			}
			if t == nil {
				return
			}
			a.reload(t)
			a.notifySuccess(t, "已获取远程更新")
		})
	}()
}

func (a *app) openFetch(tab *repoTab) {
	if tab == nil || tab.unavailable {
		return
	}
	tab.stopAutoFetch()
	if a.write.busy || a.fetch.busy || tab.commitBusy || tab.syncBusy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if a.fetch.cancel != nil {
		a.fetch.cancel()
	}
	a.fetch = fetchDialog{repo: tab.path, gen: a.fetch.gen + 1}
	a.showFetch, a.fetch.err = true, ""
	a.fetch.preparing = true
	gen, path := a.fetch.gen, tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		remotes, err := listRemotes(ctx, path)
		a.post(func() {
			if a.closed || !a.showFetch || a.fetch.gen != gen {
				return
			}
			a.fetch.preparing = false
			if err != nil {
				a.fetch.err = err.Error()
				return
			}
			a.fetch.remotes = remotes
			if len(remotes) == 0 {
				a.fetch.err = "没有配置远程"
			}
		})
	}()
}

func (a *app) fetchModal(c *ui.Context) {
	wasOpen := a.showFetch
	d := &a.fetch
	disabled := d.preparing || d.busy
	appModal(c.Key("fetch-modal"), &a.showFetch, "获取远程", 500, d.busy, func() {
		ui.Text(c, "从所有远程获取最新引用。不会合并或改动工作区。").FontSize(12).TextColor(c.Theme().TextMuted)
		switch {
		case d.preparing:
			busyRow(c, "fetch-preparing", "正在读取远程…", "")
		case len(d.remotes) > 0:
			ui.Text(c, "远程："+strings.Join(d.remotes, "、")).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		ui.Checkbox(c.Key("fetch-prune"), &d.prune, "清理远端已删除的分支").Disabled(disabled)
		ui.Checkbox(c.Key("fetch-prune-tags"), &d.pruneTags, "同时清理远端已删除的标签").Disabled(disabled)
		if d.err != "" {
			ui.Text(c, d.err).TextColor(c.Theme().Danger)
		}
		if d.busy {
			busyRow(c, "fetch-busy", "正在获取远程更新…", "")
		}
	}, func() {
		if ui.Button(c.Key("fetch-cancel"), "取消").Clicked() {
			if d.busy {
				if d.cancel != nil {
					d.cancel()
				}
			} else {
				a.showFetch = false
			}
		}
		if ui.PrimaryButton(c.Key("fetch-apply"), "获取").Disabled(disabled || len(d.remotes) == 0).Clicked() {
			a.runFetch()
		}
	})
	if wasOpen && !a.showFetch && a.fetch.busy {
		a.showFetch = true
	}
}

func (a *app) runFetch() {
	d := &a.fetch
	if d.preparing || d.busy || len(d.remotes) == 0 {
		return
	}
	d.err = ""
	repo := d.repo
	opts := fetchOpts{prune: d.prune, pruneTags: d.pruneTags}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	d.cancel = cancel
	d.busy = true
	if t := a.tabByPath(repo); t != nil {
		t.lastFetch = time.Now()
	}
	gen := d.gen
	go func() {
		defer cancel()
		err := fetchAll(ctx, repo, opts)
		a.post(func() {
			if a.fetch.gen != gen {
				return
			}
			a.fetch.busy = false
			a.fetch.cancel = nil
			if err != nil {
				a.showFetch = false
				if errors.Is(ctx.Err(), context.Canceled) {
					return
				}
				a.setBanner(err.Error(), repo)
				return
			}
			a.showFetch = false
			t := a.tabByPath(repo)
			if t == nil {
				return
			}
			a.reload(t)
			a.notifySuccess(t, "已获取远程更新")
		})
	}()
}

func (a *app) stopFetch() {
	a.fetch.gen++
	if a.fetch.cancel != nil {
		a.fetch.cancel()
		a.fetch.cancel = nil
	}
	a.fetch.busy = false
	a.showFetch = false
}

const (
	autoFetchInterval = 2 * time.Minute
	autoFetchTimeout  = 45 * time.Second
)

func (t *repoTab) stopAutoFetch() {
	t.fetchGen++
	if t.fetchCancel != nil {
		t.fetchCancel()
		t.fetchCancel = nil
	}
	t.fetching = false
}

func (a *app) maybeAutoFetch() {
	t := a.activeTab()
	if a.closed || t == nil || t.closed || t.unavailable || t.loading || t.fetching || !t.loaded {
		return
	}
	if a.fetch.busy || a.showFetch || a.write.busy {
		return
	}
	if !t.lastFetch.IsZero() && time.Since(t.lastFetch) < autoFetchInterval {
		return
	}
	t.fetchGen++
	gen, tabGen, path := t.fetchGen, t.generation, t.path
	ctx, cancel := context.WithTimeout(context.Background(), autoFetchTimeout)
	t.fetchCancel = cancel
	t.fetching = true
	t.lastFetch = time.Now()
	go func() {
		defer cancel()
		err := fetchAll(ctx, path, fetchOpts{})
		a.post(func() {
			if t.fetchGen != gen {
				return
			}
			t.fetching = false
			t.fetchCancel = nil
			if t.closed || t.generation != tabGen || err != nil {
				return
			}
			t.refsChecked = time.Time{}
			if a.activeTab() == t && !a.fetch.busy {
				a.checkActiveRepo()
			}
		})
	}()
}
