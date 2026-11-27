package app

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Git updates loose refs by renaming lock files: watching their directories
// avoids peeling every tag on every tick. A periodic full check also covers
// tools that edit ref files in place.
//
// 容器仓库没有本机目录可走，改成一条 `git for-each-ref` 的摘要；
// 引用本身变了才触发完整检查，不会每两秒读一遍容器里的 .git。
type refWatch struct {
	files  map[string]os.FileInfo
	docker *dockerRefSignature
}

type dockerRefSignature struct {
	key     string
	refs    string
	gitMeta dockerFileMeta
}

func newRefWatch(ctx context.Context, path string) *refWatch {
	if isDockerRepoPath(path) {
		return newDockerRefWatch(ctx, path)
	}
	gitDir := filepath.Join(path, ".git")
	if b, err := os.ReadFile(gitDir); err == nil {
		gitDir = resolveRepoGitPath(path, strings.TrimPrefix(string(b), "gitdir:"))
	}
	common := gitDir
	if b, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common = resolveRepoGitPath(gitDir, string(b))
	}
	w := &refWatch{files: map[string]os.FileInfo{}}
	for _, p := range []string{filepath.Join(path, ".git"), filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "commondir"), filepath.Join(common, "packed-refs"), filepath.Join(common, "shallow"), filepath.Join(common, "info", "grafts"), filepath.Join(common, "reftable"), filepath.Join(common, "reftable", "tables.list")} {
		w.files[p], _ = os.Stat(p)
		if w.files[p] != nil {
			os.SameFile(w.files[p], w.files[p]) // Capture the file identity before it can be replaced on Windows.
		}
	}
	dirs := []string{filepath.Join(common, "refs")}
	if filepath.Clean(common) != filepath.Clean(gitDir) {
		dirs = append(dirs, filepath.Join(gitDir, "refs"))
	}
	for _, dir := range dirs {
		w.files[dir], _ = os.Stat(dir)
		if w.files[dir] != nil {
			os.SameFile(w.files[dir], w.files[dir])
		}
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
				os.SameFile(info, info)
				w.files[p] = info
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil
		}
	}
	return w
}

func newDockerRefWatch(ctx context.Context, key string) *refWatch {
	sig := &dockerRefSignature{key: key}
	if out, err := runGit(ctx, key, "for-each-ref", "--format=%(objectname) %(refname)"); err == nil {
		sig.refs = string(out)
	}
	if target, ok := parseDockerRepoKey(key); ok {
		sig.gitMeta, _ = dockerStatOne(ctx, key, path.Join(target.path, ".git"))
	}
	return &refWatch{docker: sig}
}

func (w *refWatch) changed(ctx context.Context) bool {
	if w == nil {
		return true
	}
	if w.docker != nil {
		if w.docker.key == "" {
			return true
		}
		out, err := runGit(ctx, w.docker.key, "for-each-ref", "--format=%(objectname) %(refname)")
		if err != nil {
			return true
		}
		return string(out) != w.docker.refs
	}
	for path, old := range w.files {
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
func (w *refWatch) replaced(path string, next *refWatch) bool {
	if w == nil || next == nil {
		return false
	}
	if w.docker != nil || next.docker != nil {
		if w.docker == nil || next.docker == nil {
			return false
		}
		return w.docker.key == next.docker.key && w.docker.gitMeta != next.docker.gitMeta
	}
	p := filepath.Join(path, ".git")
	old, now := w.files[p], next.files[p]
	if old == nil || now == nil {
		return false
	}
	return !os.SameFile(old, now) || !old.IsDir() && !sameFileVersion(old, now)
}
