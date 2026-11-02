package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

func (m repoMeta) key() string {
	parts := []string{m.branch, hashHex(m.head)}
	for h, refs := range m.refs {
		for _, ref := range refs {
			parts = append(parts, hashHex(h)+refPrefix(ref.kind)+ref.name)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00")
}

func refPrefix(kind refKind) string {
	switch kind {
	case refBranch:
		return "refs/heads/"
	case refRemote:
		return "refs/remotes/"
	default:
		return "refs/tags/"
	}
}

// 只检查活动仓库的引用，不扫描工作区或后台仓库。所有模型读写仍在界面线程。
func (a *app) startAutoRefresh() {
	ctx, cancel := context.WithCancel(context.Background())
	a.watchCancel = cancel
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.onMain(func() {
					if a.win == nil || a.win.IsFocused() && !a.win.IsMinimized() {
						a.checkActiveRepo()
					}
				})
			}
		}
	}()
}

func (a *app) checkActiveRepo() {
	t := a.activeTab()
	if a.closed || t == nil || t.closed || t.loading || t.checking || !t.loaded || a.showCompare {
		return
	}
	t.checking = true
	gen, path := t.generation, t.path
	watch, lastCheck := t.refWatch, t.refsChecked
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		full := watch.changed() || time.Since(lastCheck) >= 30*time.Second
		var meta repoMeta
		var err error
		var key string
		if full {
			watch = newRefWatch(ctx, path)
			meta, err = loadMeta(ctx, path)
			key = meta.key()
		}
		var dirty []fileChange
		if err == nil {
			dirty, err = worktreeStatus(ctx, path)
		}
		dirtyKey := worktreeKey(dirty)
		a.onMain(func() {
			changed := false
			defer func() {
				if changed && a.win != nil {
					a.win.Invalidate()
				}
			}()
			t.checking = false
			if a.closed || t.closed || t.generation != gen || a.activeTab() != t {
				return
			}
			if err != nil {
				msg := "检查仓库更新失败：" + err.Error()
				changed, t.err = t.err != msg, msg
				return
			}
			if strings.HasPrefix(t.err, "检查仓库更新失败：") {
				t.err = ""
				changed = true
			}
			if full {
				t.refWatch, t.refsChecked = watch, time.Now()
			}
			if full && key != t.metaKey {
				a.reload(t)
				changed = true
				return
			}
			if dirtyKey != t.worktreeKey {
				a.applyWorktree(t, dirty)
				changed = true
			}
		})
	}()
}

func (a *app) focusRepo() {
	if t := a.activeTab(); t != nil {
		t.refsChecked = time.Time{}
	}
	a.checkActiveRepo()
}

// Unlike Window.Update, a no-op check must not schedule another UI frame.
func (a *app) onMain(fn func()) {
	if a.dispatch != nil {
		a.dispatch(fn)
	} else if a.win == nil {
		fn()
	} else {
		mygo.RunOnMain(fn)
	}
}
