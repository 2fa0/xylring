package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type syncStatus struct {
	hasUpstream bool
	upstream    string
	ahead       int
	behind      int
}

func queryTrackingStatus(ctx context.Context, dir string) (syncStatus, error) {
	var s syncStatus
	if dir == "" {
		return s, nil
	}
	out, err := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return s, nil
	}
	s.upstream = strings.TrimSpace(string(out))
	if s.upstream == "" {
		return s, nil
	}
	s.hasUpstream = true
	counts, err := runGit(ctx, dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		return s, err
	}
	fields := strings.Fields(string(counts))
	if len(fields) >= 2 {
		s.ahead, _ = strconv.Atoi(fields[0])
		s.behind, _ = strconv.Atoi(fields[1])
	}
	return s, nil
}

func syncBadge(ahead, behind int) string {
	if ahead > 0 && behind > 0 {
		return fmt.Sprintf("↑%d ↓%d", ahead, behind)
	}
	if ahead > 0 {
		return fmt.Sprintf("↑%d", ahead)
	}
	if behind > 0 {
		return fmt.Sprintf("↓%d", behind)
	}
	return ""
}

func gitPull(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "pull")
	return string(out), err
}

func gitPush(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, dir, "push")
	return string(out), err
}

func (a *app) quickPull(tab *repoTab) {
	if tab == nil || tab.unavailable {
		return
	}
	if a.write.busy || a.fetch.busy || tab.syncBusy || tab.commitBusy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	tab.syncBusy = true
	tab.notice = "正在拉取更新 (git pull)…"
	repoPath := tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		out, err := gitPull(ctx, repoPath)
		a.post(func() {
			tab.syncBusy = false
			tab.notice = ""
			if err != nil {
				errMsg := strings.TrimSpace(string(out))
				if errMsg == "" {
					errMsg = err.Error()
				}
				a.setBanner("拉取失败: "+errMsg, "")
				a.pendingToast = "拉取失败"
			} else {
				a.pendingToast = "拉取成功"
				a.refresh(tab)
			}
		})
	}()
}

func (a *app) quickPush(tab *repoTab) {
	if tab == nil || tab.unavailable {
		return
	}
	if a.write.busy || a.fetch.busy || tab.syncBusy || tab.commitBusy {
		a.setBanner("请等待正在执行的操作", "")
		return
	}
	if tab.behind > 0 {
		a.setBanner(fmt.Sprintf("远程有新提交 (落后 %d 个提交)，请先拉取", tab.behind), "")
		a.pendingToast = "请先拉取远程更新"
		return
	}
	if !tab.hasUpstream {
		a.openOperation("推送分支", "", "")
		return
	}
	tab.syncBusy = true
	tab.notice = "正在推送到远程 (git push)…"
	repoPath := tab.path
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		out, err := gitPush(ctx, repoPath)
		a.post(func() {
			tab.syncBusy = false
			tab.notice = ""
			if err != nil {
				errMsg := strings.TrimSpace(string(out))
				if errMsg == "" {
					errMsg = err.Error()
				}
				a.setBanner("推送失败: "+errMsg, "")
				a.pendingToast = "推送失败"
			} else {
				a.pendingToast = "推送成功"
				a.refresh(tab)
			}
		})
	}()
}
