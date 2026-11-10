package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Git updates loose refs by renaming lock files: watching their directories
// avoids peeling every tag on every tick. A periodic full check also covers
// tools that edit ref files in place.
type refWatch map[string]os.FileInfo

func newRefWatch(ctx context.Context, path string) refWatch {
	gitDir := filepath.Join(path, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		gitDir = strings.TrimSpace(strings.TrimPrefix(string(b), "gitdir:"))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(path, gitDir)
		}
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = strings.TrimSpace(string(b))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
	}
	w := refWatch{}
	for _, p := range []string{filepath.Join(path, ".git"), filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "commondir"), filepath.Join(common, "packed-refs"), filepath.Join(common, "shallow"), filepath.Join(common, "info", "grafts"), filepath.Join(common, "reftable"), filepath.Join(common, "reftable", "tables.list")} {
		w[p], _ = os.Stat(p)
	}
	dirs := []string{filepath.Join(common, "refs")}
	if filepath.Clean(common) != filepath.Clean(gitDir) {
		dirs = append(dirs, filepath.Join(gitDir, "refs"))
	}
	for _, dir := range dirs {
		w[dir], _ = os.Stat(dir)
		err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				w[p] = info
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil
		}
	}
	return w
}

func (w refWatch) changed() bool {
	if w == nil {
		return true
	}
	for path, old := range w {
		now, err := os.Stat(path)
		if os.IsNotExist(err) {
			if old != nil {
				return true
			}
			continue
		}
		if err != nil || old == nil || !os.SameFile(old, now) {
			return true
		}
		// Index locks and object writes also touch .git's directory mtime.
		if old.IsDir() && filepath.Base(path) == ".git" {
			continue
		}
		if !old.ModTime().Equal(now.ModTime()) || old.Size() != now.Size() {
			return true
		}
	}
	return false
}

// A replacement can have the same refs as the old repository (including no refs).
func (w refWatch) replaced(path string, next refWatch) bool {
	p := filepath.Join(path, ".git")
	old, now := w[p], next[p]
	if old == nil || now == nil {
		return false
	}
	return !os.SameFile(old, now) || !old.IsDir() && !sameFileVersion(old, now)
}
