package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type worktreeStatusCache struct {
	raw       string
	files     []fileChange
	stats     []os.FileInfo
	indexPath string
	index     os.FileInfo
	checked   time.Time
}

func sameFileVersion(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Metadata checks avoid reading every dirty file on every tick. A periodic full
// check still detects edits made with restored timestamps, including index edits.
func pollWorktreeStatus(ctx context.Context, dir string, previous *worktreeStatusCache) ([]fileChange, *worktreeStatusCache, error) {
	out, err := runGit(ctx, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, previous, err
	}
	next := &worktreeStatusCache{raw: string(out)}
	if previous != nil {
		next.indexPath = previous.indexPath
	}
	if next.indexPath == "" {
		path, err := runGit(ctx, dir, "rev-parse", "--git-path", "index")
		if err != nil {
			return nil, previous, err
		}
		next.indexPath = strings.TrimSpace(string(path))
		if !filepath.IsAbs(next.indexPath) {
			next.indexPath = filepath.Join(dir, next.indexPath)
		}
	}
	next.index, _ = os.Stat(next.indexPath)
	if previous != nil && next.raw == previous.raw && time.Since(previous.checked) < 30*time.Second && sameFileVersion(previous.index, next.index) {
		unchanged := true
		for i, f := range previous.files {
			if err := ctx.Err(); err != nil {
				return nil, previous, err
			}
			info, _ := os.Lstat(filepath.Join(dir, filepath.FromSlash(f.path)))
			if !sameFileVersion(previous.stats[i], info) {
				unchanged = false
				break
			}
		}
		if unchanged {
			return previous.files, previous, nil
		}
	}
	next.files = parsePorcelainZ(out)
	// Capture metadata before reading, so an edit during the read invalidates the next poll.
	next.stats = make([]os.FileInfo, len(next.files))
	for i, f := range next.files {
		next.stats[i], _ = os.Lstat(filepath.Join(dir, filepath.FromSlash(f.path)))
	}
	next.files, err = enrichWorktreeStatus(ctx, dir, next.files)
	if err != nil {
		return nil, previous, err
	}
	next.checked = time.Now()
	return next.files, next, nil
}
