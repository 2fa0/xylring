package app

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
	explicit  bool
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
	} else if opts.explicit {
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
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || tab.unavailable {
		return
	}
	if tab.remoteBusy() {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	tab.stopAutoFetch()
	tab.stopManualFetch()
	repo := tab.path
	ctx, cancel := context.WithTimeout(context.Background(), manualFetchTimeout)
	tab.manualFetchGen++
	gen := tab.manualFetchGen
	tab.manualFetchBusy = true
	tab.manualFetchCancel = cancel
	a.showFetch = false
	tab.lastFetch = time.Now()
	tab.notice = "正在获取远程更新…"
	go func() {
		defer cancel()
		err := fetchAll(ctx, repo, fetchOpts{})
		canceled := errors.Is(ctx.Err(), context.Canceled)
		a.post(func() {
			if tab.closed || tab.manualFetchGen != gen {
				return
			}
			tab.manualFetchBusy = false
			tab.manualFetchCancel = nil
			if err != nil {
				if tab.notice == "正在获取远程更新…" {
					tab.notice = ""
				}
				if !canceled {
					a.setBanner(err.Error(), repo)
				}
				return
			}
			tab.fetchFailCount = 0
			tab.nextAutoFetch = time.Time{}
			tab.refsChecked = time.Time{}
			a.notifySuccess(tab, "已获取远程更新")
			if a.activeTab() == tab {
				a.checkActiveRepo()
			}
		})
	}()
}

func (a *app) openFetch(tab *repoTab) {
	if a.dockerActionBlocked(tab) {
		return
	}
	if tab == nil || tab.unavailable {
		return
	}
	if tab.remoteBusy() || a.fetch.busy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	tab.stopAutoFetch()
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
		prune := false
		if err == nil {
			b, e := configCommand(ctx, path, "--type=bool", "--get", "fetch.prune")
			if e != nil && !configExit(e, 1) {
				err = e
			}
			prune = strings.TrimSpace(string(b)) == "true"
		}
		a.post(func() {
			if a.closed || !a.showFetch || a.fetch.gen != gen {
				return
			}
			a.fetch.preparing = false
			if err != nil {
				a.fetch.err = "读取远程失败：" + err.Error()
				return
			}
			a.fetch.remotes = remotes
			a.fetch.prune = prune
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
	appModal(c.Key("fetch-modal"), &a.showFetch, tr("获取远程"), 500, d.busy, func() {
		switch {
		case d.preparing:
			busyRow(c, "fetch-preparing", tr("正在读取远程…"), "")
		case len(d.remotes) > 0:
			ui.Text(c, tr("远程：")+strings.Join(d.remotes, "、")).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		ui.Checkbox(c.Key("fetch-prune"), &d.prune, tr("清理远端已删除的分支")).Disabled(disabled)
		ui.Checkbox(c.Key("fetch-prune-tags"), &d.pruneTags, tr("同时清理远端已删除的标签")).Disabled(disabled)
		if d.err != "" {
			ui.Text(c, trMessage(d.err)).TextColor(c.Theme().Danger)
		}
		if d.busy {
			busyRow(c, "fetch-busy", tr("正在获取远程更新…"), "")
		}
	}, func() {
		if ui.Button(c.Key("fetch-cancel"), tr("取消")).Clicked() {
			if d.busy {
				if d.cancel != nil {
					d.cancel()
				}
			} else {
				a.showFetch = false
			}
		}
		if ui.PrimaryButton(c.Key("fetch-apply"), tr("获取")).Disabled(disabled || len(d.remotes) == 0).Clicked() {
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
	t := a.tabByPath(d.repo)
	if t != nil && t.remoteBusy() {
		d.err = "请等待正在执行的操作"
		return
	}
	d.err = ""
	repo := d.repo
	opts := fetchOpts{prune: d.prune, pruneTags: d.pruneTags, explicit: true}
	ctx, cancel := context.WithTimeout(context.Background(), manualFetchTimeout)
	d.cancel = cancel
	d.busy = true
	manualGen := 0
	if t != nil {
		t.stopAutoFetch()
		t.stopManualFetch()
		t.lastFetch = time.Now()
		t.manualFetchGen++
		manualGen = t.manualFetchGen
		t.manualFetchBusy = true
		t.manualFetchCancel = cancel
	}
	gen := d.gen
	go func() {
		defer cancel()
		err := fetchAll(ctx, repo, opts)
		canceled := errors.Is(ctx.Err(), context.Canceled)
		a.post(func() {
			if t != nil && !t.closed && t.manualFetchGen == manualGen {
				t.manualFetchBusy = false
				t.manualFetchCancel = nil
			}
			if a.fetch.gen != gen {
				return
			}
			a.fetch.busy = false
			a.fetch.cancel = nil
			if err != nil {
				a.showFetch = false
				if canceled {
					return
				}
				a.setBanner(err.Error(), repo)
				return
			}
			a.showFetch = false
			cur := a.tabByPath(repo)
			if cur == nil {
				return
			}
			cur.fetchFailCount = 0
			cur.nextAutoFetch = time.Time{}
			cur.refsChecked = time.Time{}
			if !a.write.busy && !cur.commitBusy && !cur.pullBusy {
				a.reload(cur)
				a.notifySuccess(cur, "已获取远程更新")
			} else {
				a.notifySuccess(cur, "已获取远程更新")
				if a.activeTab() == cur {
					a.checkActiveRepo()
				}
			}
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
	for _, t := range a.tabs {
		t.stopManualFetch()
	}
}

const (
	autoFetchInterval   = 2 * time.Minute
	autoFetchTimeout    = 20 * time.Second
	manualFetchTimeout  = 2 * time.Minute
	autoFetchMaxBackoff = 15 * time.Minute
)

func autoFetchBackoff(base time.Duration, failCount int) time.Duration {
	if failCount <= 0 || base <= 0 {
		return base
	}
	capDelay := max(base, autoFetchMaxBackoff)
	delay := base
	for i := 0; i < failCount && delay < capDelay; i++ {
		if delay > capDelay/2 {
			return capDelay
		}
		delay *= 2
	}
	return delay
}

func (t *repoTab) stopAutoFetch() {
	t.fetchGen++
	if t.fetchCancel != nil {
		t.fetchCancel()
		t.fetchCancel = nil
	}
	t.fetching = false
}

func (t *repoTab) stopManualFetch() {
	t.manualFetchGen++
	if t.manualFetchCancel != nil {
		t.manualFetchCancel()
		t.manualFetchCancel = nil
	}
	t.manualFetchBusy = false
}

func (a *app) cancelManualFetch(tab *repoTab) {
	if tab == nil || !tab.manualFetchBusy {
		return
	}
	tab.stopManualFetch()
	if tab.notice == "正在获取远程更新…" {
		tab.notice = ""
	}
	if a.fetch.busy && a.fetch.repo == tab.path {
		a.fetch.gen++
		if a.fetch.cancel != nil {
			a.fetch.cancel()
			a.fetch.cancel = nil
		}
		a.fetch.busy = false
		a.showFetch = false
	}
	a.pendingToast = "已取消获取远程"
}

func (a *app) maybeAutoFetch() {
	t := a.activeTab()
	if a.closed || t == nil || t.closed || t.unavailable || t.loading || t.fetching || !t.loaded {
		return
	}
	// 容器仓库不自动获取远程更新：第一版没有开放任何远程同步动作。
	if isDockerRepoPath(t.path) {
		return
	}
	if t.remoteBusy() || (a.showFetch && a.fetch.repo == t.path) || a.settings.open {
		return
	}
	interval := a.autoFetchDelay(t.path)
	if interval == 0 {
		return
	}
	now := time.Now()
	if !t.nextAutoFetch.IsZero() && now.Before(t.nextAutoFetch) {
		return
	}
	if t.fetchFailCount == 0 && !t.lastFetch.IsZero() && time.Since(t.lastFetch) < interval {
		return
	}
	t.fetchGen++
	gen, tabGen, path := t.fetchGen, t.generation, t.path
	ctx, cancel := context.WithTimeout(context.Background(), autoFetchTimeout)
	t.fetchCancel = cancel
	t.fetching = true
	t.lastFetch = now
	go func() {
		defer cancel()
		err := fetchAll(ctx, path, fetchOpts{})
		canceled := errors.Is(ctx.Err(), context.Canceled)
		a.post(func() {
			if t.fetchGen != gen {
				return
			}
			t.fetching = false
			t.fetchCancel = nil
			if t.closed || t.generation != tabGen {
				return
			}
			if err != nil {
				if !canceled {
					t.fetchFailCount++
					t.nextAutoFetch = time.Now().Add(autoFetchBackoff(interval, t.fetchFailCount))
				}
				return
			}
			t.fetchFailCount = 0
			t.nextAutoFetch = time.Time{}
			t.refsChecked = time.Time{}
			if a.activeTab() == t && !t.remoteBusy() {
				a.checkActiveRepo()
			}
		})
	}()
}
