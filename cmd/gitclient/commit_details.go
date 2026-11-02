package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

type commitDetails struct {
	hash, parents                 string
	author, authorEmail, authored string
	committer, commitEmail, at    string
	message                       string
	formatted                     string
	view                          editor.State
}

func readCommitDetails(ctx context.Context, dir string, hash [20]byte) (*commitDetails, error) {
	out, err := runGitLimit(ctx, dir, 1<<20, "show", "--no-patch", "--format=%H%x00%P%x00%an%x00%ae%x00%aI%x00%cn%x00%ce%x00%cI%x00%B", hashHex(hash), "--")
	if err != nil {
		return nil, err
	}
	f := strings.SplitN(string(out), "\x00", 9)
	if len(f) != 9 {
		return nil, errors.New("无法解析完整提交信息")
	}
	d := &commitDetails{hash: f[0], parents: f[1], author: f[2], authorEmail: f[3], authored: f[4], committer: f[5], commitEmail: f[6], at: f[7], message: strings.TrimRight(f[8], "\n")}
	d.formatted = d.text()
	d.view = editor.State{Buffer: editor.NewBuffer(d.formatted), ReadOnly: true, Label: "完整提交信息"}
	return d, nil
}

func (d *commitDetails) text() string {
	if d.formatted != "" {
		return d.formatted
	}
	parents := d.parents
	if parents == "" {
		parents = "无（初始提交）"
	}
	return fmt.Sprintf("提交：%s\n父提交：%s\n作者：%s <%s>\n创作时间：%s\n提交者：%s <%s>\n提交时间：%s\n\n%s", d.hash, parents, d.author, d.authorEmail, d.authored, d.committer, d.commitEmail, d.at, d.message)
}

func (a *app) toggleDetails(t *repoTab) {
	t.detailsOpen = !t.detailsOpen
	if !t.detailsOpen || t.details != nil || t.detailsLoading || t.inspecting == zeroHash {
		return
	}
	if t.detailsCancel != nil {
		t.detailsCancel()
	}
	t.detailsGen++
	gen, hash, path := t.detailsGen, t.inspecting, t.path
	t.detailsLoading, t.detailsErr = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.detailsCancel = cancel
	go func() {
		defer cancel()
		details, err := readCommitDetails(ctx, path, hash)
		a.post(func() {
			if t.closed || t.detailsGen != gen || t.inspecting != hash {
				return
			}
			t.detailsLoading = false
			if err != nil {
				t.detailsErr = err.Error()
				return
			}
			t.details = details
		})
	}()
}

func (a *app) clearDetails(t *repoTab) {
	t.detailsGen++
	if t.detailsCancel != nil {
		t.detailsCancel()
		t.detailsCancel = nil
	}
	t.details, t.detailsErr = nil, ""
	t.detailsOpen, t.detailsLoading = false, false
}

func (a *app) detailsPopover(c *ui.Context, t *repoTab, anchor ui.Element) {
	ui.Popover(c, anchor, &t.detailsOpen, func() {
		ui.Column(c).Width(440).Height(260).Gap(8).Children(func() {
			switch {
			case t.detailsLoading:
				ui.Text(c, "正在读取…").TextColor(c.Theme().TextMuted)
			case t.detailsErr != "":
				ui.Text(c, t.detailsErr).TextColor(c.Theme().Danger)
				if ui.Button(c.Key("details-retry"), "重试").Clicked() {
					t.detailsOpen = false
					a.toggleDetails(t)
				}
			case t.details != nil:
				if ui.Button(c.Key("copy-commit-info"), "复制完整信息").Clicked() {
					c.Services().WriteClipboard(t.details.text())
					c.Toast("已复制提交信息")
				}
				editor.View(c.Key(fmt.Sprintf("commit-info-%d", t.id)), &t.details.view).Grow(1).MinHeight(0)
			}
		})
	})
}
