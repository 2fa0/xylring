package main

import (
	"context"
	"encoding/gob"
	"os"
	"time"

	"github.com/egoist/mygo/ui/editor"
)

// Disk backing preserves worktree snapshots even after the working file changes.
type fileTabSnapshot struct {
	Old, New   []byte
	Full, Note string
}

func (tab *fileDiffTab) close() {
	tab.closed = true
	tab.loadGen++
	if tab.cacheCancel != nil {
		tab.cacheCancel()
		tab.cacheCancel = nil
	}
	if tab.backing != "" {
		_ = os.Remove(tab.backing)
		tab.backing = ""
	}
}

func writeFileTabSnapshot(snapshot fileTabSnapshot) (string, error) {
	f, err := os.CreateTemp("", "xylring-snapshot-*")
	if err != nil {
		return "", err
	}
	err = gob.NewEncoder(f).Encode(snapshot)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func (a *app) fileTabVisible(t *repoTab, tab *fileDiffTab) bool {
	if a.activeTab() != t {
		return false
	}
	if t.contentTab == 0 {
		return tab.diff != nil && tab.diff == t.diff
	}
	return t.contentTab <= len(t.fileTabs) && t.fileTabs[t.contentTab-1] == tab
}

func (a *app) spillFileTabs(t *repoTab) {
	type job struct {
		tab       *fileDiffTab
		source    *fileDiffSource
		buffer    *editor.Buffer
		fullImage *imageInfo
		note      string
		ctx       context.Context
	}
	var jobs []job
	for _, tab := range t.fileTabs {
		if a.fileTabVisible(t, tab) || tab.evicted || tab.cachePending || tab.closed || tab.source == nil && tab.fullContent.Buffer == nil && tab.fullImage == nil {
			continue
		}
		if tab.backing != "" {
			tab.releaseContent()
			continue
		}
		tab.cachePending = true
		ctx, cancel := context.WithCancel(context.Background())
		tab.cacheCancel = cancel
		jobs = append(jobs, job{tab, tab.source, tab.fullContent.Buffer, tab.fullImage, tab.note, ctx})
	}
	if len(jobs) == 0 {
		return
	}
	go func() {
		type result struct {
			tab  *fileDiffTab
			path string
			err  error
		}
		results := make([]result, 0, len(jobs))
		for _, j := range jobs {
			if j.ctx.Err() != nil {
				results = append(results, result{j.tab, "", j.ctx.Err()})
				continue
			}
			snapshot := fileTabSnapshot{Note: j.note}
			if j.source != nil {
				snapshot.Old, snapshot.New, snapshot.Note = j.source.old, j.source.new, j.source.note
			}
			if j.fullImage != nil {
				snapshot.Old = j.fullImage.data
			}
			if j.buffer != nil {
				snapshot.Full = j.buffer.String()
			}
			path, err := writeFileTabSnapshot(snapshot)
			if path != "" {
				context.AfterFunc(j.ctx, func() { _ = os.Remove(path) })
			}
			results = append(results, result{j.tab, path, err})
		}
		a.post(func() {
			for _, r := range results {
				tab := r.tab
				tab.cachePending = false
				if t.closed || tab.closed {
					if r.path != "" {
						_ = os.Remove(r.path)
					}
					continue
				}
				if r.err != nil {
					t.notice = "暂存文件快照失败：" + r.err.Error()
					continue
				}
				tab.backing = r.path
				if !a.fileTabVisible(t, tab) {
					tab.releaseContent()
				}
			}
		})
	}()
}

func (tab *fileDiffTab) releaseContent() {
	tab.loadGen++
	tab.source, tab.diff, tab.simple, tab.imageDiff = nil, nil, nil, nil
	tab.fullContent = editor.State{}
	tab.fullImage = nil
	tab.evicted = true
}

func (a *app) restoreFileTab(t *repoTab, tab *fileDiffTab) {
	tab.evicted = false
	tab.loadGen++
	gen, path, file, full, ignore, rev := tab.loadGen, tab.backing, tab.file, tab.fullFile, tab.ignoreWhitespace, tab.fullRev
	tab.note = "正在读取文件…"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var snapshot fileTabSnapshot
		f, err := os.Open(path)
		if err == nil {
			err = gob.NewDecoder(f).Decode(&snapshot)
			_ = f.Close()
		}
		var source *fileDiffSource
		var diff *editor.DiffState
		var simple *simpleDiff
		var imgDiff *imageDiff
		var imgInfo *imageInfo
		var state editor.State
		note := snapshot.Note
		if err == nil {
			if full {
				if isImageFile(file.path) && len(snapshot.Old) > 0 {
					imgInfo, err = decodeImageInfo(snapshot.Old, file.path)
				} else {
					buffer := editor.NewBuffer(snapshot.Full)
					buffer.SetLanguage(editor.LanguageFor(file.path))
					state = editor.State{Buffer: buffer, ReadOnly: true, Label: file.path + " (" + rev + ")"}
				}
			} else {
				source = &fileDiffSource{old: snapshot.Old, new: snapshot.New, note: snapshot.Note}
				diff, simple, imgDiff, note, err = source.prepare(ctx, file.path, ignore && file.status != "A" && file.status != "D")
			}
		}
		if err != nil {
			note = "恢复文件快照失败：" + err.Error()
		}
		a.post(func() {
			if t.closed || tab.closed || tab.loadGen != gen {
				return
			}
			tab.source, tab.diff, tab.simple, tab.imageDiff, tab.fullContent, tab.fullImage, tab.note = source, diff, simple, imgDiff, state, imgInfo, note
			a.trimInactiveHistory()
		})
	}()
}
