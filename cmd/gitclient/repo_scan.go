package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

type scannedDir struct {
	info     os.FileInfo
	children []string
	repo     bool
}

// Cache directory listings, not files. Unchanged directories need one stat on
// refresh; creating/removing a nested repository changes its parent's mtime.
func scanGitRepos(ctx context.Context, dir string, previous map[string]scannedDir) ([]string, map[string]scannedDir, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	cache := map[string]scannedDir{}
	var roots []string
	var walk func(string) error
	walk = func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(path)
		if os.IsPermission(err) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		entry, ok := previous[path]
		if !ok || !os.SameFile(entry.info, info) || !entry.info.ModTime().Equal(info.ModTime()) {
			entries, err := os.ReadDir(path)
			if os.IsPermission(err) || os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			entry = scannedDir{info: info, repo: isGitWorkTree(path)}
			for _, d := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if d.IsDir() && !skipRepoDir(d.Name()) {
					entry.children = append(entry.children, d.Name())
				}
			}
		}
		// A very large import must not turn the scan cache into an unbounded heap.
		if len(cache) < 20000 {
			cache[path] = entry
		}
		if entry.repo {
			roots = append(roots, path)
		}
		for _, child := range entry.children {
			if err := walk(filepath.Join(path, child)); err != nil {
				return err
			}
		}
		return nil
	}
	err = walk(abs)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, errors.New("查找仓库超时或已取消")
		}
		return nil, nil, err
	}
	if len(roots) > 0 {
		sort.Strings(roots)
		return roots, cache, nil
	}
	root, err := repoRoot(ctx, abs)
	if err != nil {
		return nil, cache, errors.New("没有找到 Git 仓库")
	}
	return []string{root}, cache, nil
}
