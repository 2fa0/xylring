package main

import (
	"bufio"
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

func streamFileHistory(ctx context.Context, dir string, q historyQuery, skip, n int, fn func(commit) error) error {
	if q.fileAt == zeroHash {
		return nil
	}
	// --skip 会先跳过重命名提交，导致 --follow 无法继续追踪旧路径。
	// 重开流时从起点恢复旧路径；连续翻页由 historyPager 复用同一个流。
	args := []string{"log", "--follow", "-M", "--date-order", "--no-color", "--name-status", "-z", "--diff-merges=first-parent",
		"--format=%x00%H%x00%P%x00%an%x00%at%x00%s"}
	if n > 0 {
		args = append(args, "--max-count="+strconv.Itoa(skip+n))
	}
	args = append(args, hashHex(q.fileAt), "--", ":(literal)"+q.filePath)
	return streamLog(ctx, dir, args, "", splitByte(0), func(sc *bufio.Scanner, slot *gitLease) error {
		path, index := q.filePath, 0
		for sc.Scan() {
			if sc.Text() == "" {
				continue
			}
			fields := []string{sc.Text()}
			for range 4 {
				if !sc.Scan() {
					return errors.New("无法解析文件历史")
				}
				fields = append(fields, sc.Text())
			}
			c, err := parseCommitRecord(strings.Join(fields, "\x1f"))
			if err != nil {
				return err
			}
			var raw strings.Builder
			for sc.Scan() {
				token := sc.Text()
				if token == "" {
					break
				}
				if raw.Len() == 0 {
					token = strings.TrimLeft(token, "\r\n")
				}
				raw.WriteString(token)
				raw.WriteByte(0)
			}
			files := parseNameStatusZ([]byte(raw.String()))
			change := fileChange{status: "M", path: path}
			if len(files) > 0 {
				change = files[0]
			}
			c.file = &change
			path = change.path
			if change.oldPath != "" {
				path = change.oldPath
			}
			if index >= skip {
				if err := slot.yield(ctx, func() error { return fn(c) }); err != nil {
					return err
				}
			}
			index++
		}
		return sc.Err()
	})
}

func (a *app) openFileHistoryAt(t *repoTab, i int) {
	if i < 0 || i >= len(t.compareFiles) {
		return
	}
	t.fileSel = i
	a.openFileHistory(t)
}

func (a *app) openFileHistory(t *repoTab) {
	if t.fileSel < 0 || t.fileSel >= len(t.compareFiles) || t.query.filePath != "" {
		return
	}
	at := t.diffTo
	if at == zeroHash {
		if !t.worktree || t.head == zeroHash {
			return
		}
		at = t.head
	}
	f := t.compareFiles[t.fileSel]
	t.fileBack = t.location()
	q := historyQuery{filePath: f.path, fileAt: at}
	a.changeHistory(t, q)
}

func (a *app) leaveFileHistory(t *repoTab) {
	if t.fileBack == nil {
		return
	}
	back := t.fileBack
	t.fileBack = nil
	a.changeHistory(t, back.query)
	t.restore = back
}

func (a *app) locateCommitInGraph(t *repoTab, h [20]byte) {
	if h == zeroHash {
		return
	}
	q := historyQuery{extraTip: h}
	switch {
	case t.fileBack != nil:
		q.ref = t.fileBack.query.ref
		q.hideRemote = t.fileBack.query.hideRemote
	case t.historyBack != nil:
		q.ref = t.historyBack.query.ref
		q.hideRemote = t.historyBack.query.hideRemote
	default:
		q.ref = t.query.ref
		q.hideRemote = t.query.hideRemote
	}
	t.fileBack, t.historyBack = nil, nil
	t.searchText = ""
	a.changeHistory(t, q)
	t.restore = &historyLocation{query: q, hash: h, top: h}
}

func (a *app) locateFileCommit(t *repoTab) {
	a.locateCommitInGraph(t, t.inspecting)
}

func (a *app) fileHistoryToolbar(c *ui.Context, t *repoTab) {
	ui.Row(c).Padding(4, 12, 8, 12).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, displayPath(t.query.filePath)).Bold().SingleLine().Grow(1).MinWidth(0).Tooltip(t.query.filePath)
		if ui.Button(c.Key("file-history-locate"), "在图谱中定位").Disabled(t.inspecting == zeroHash).Clicked() {
			a.locateFileCommit(t)
		}
		if iconButton(c, "file-history-back", "返回", iconBack).Clicked() {
			a.leaveFileHistory(t)
		}
	})
}
