package main

// 容器工作区的刷新不能复用本机的 Stat 判断：本机 os.FileInfo 对容器里的文件一无所知。
// 这里用「状态 + 批量元数据 + 定期内容校验」代替，并把调用次数控制在每轮常数条。
import (
	"context"
	"time"
)

// dockerContentCheckInterval 是内容校验的最长间隔。等大小、恢复时间戳的修改
// 靠它最终被发现；更频繁的轮询只看元数据，避免每两秒读一遍所有脏文件。
const dockerContentCheckInterval = 30 * time.Second

func dockerPollWorktreeStatus(ctx context.Context, key string, previous *worktreeStatusCache) ([]fileChange, *worktreeStatusCache, error) {
	_, err := dockerTargetForCommand(key)
	if err != nil {
		return nil, previous, err
	}
	out, err := runGit(ctx, key, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, previous, err
	}
	next := &worktreeStatusCache{raw: string(out), docker: true}
	if previous != nil && previous.docker {
		next.indexPath = previous.indexPath
		next.contentChecked = previous.contentChecked
	}
	if next.indexPath == "" {
		p, err := gitFileContainerPath(ctx, key, "index")
		if err != nil {
			return nil, previous, err
		}
		next.indexPath = p
	}
	indexMeta, err := dockerStatOne(ctx, key, next.indexPath)
	if err != nil {
		return nil, previous, err
	}
	next.indexMeta = indexMeta
	files := parsePorcelainZ(out)
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.path
	}
	var metas []dockerFileMeta
	var statErr error
	contentDue := previous == nil || !previous.docker || time.Since(previous.contentChecked) >= dockerContentCheckInterval
	if previous != nil && previous.docker && next.raw == previous.raw && previous.indexMeta == indexMeta && !contentDue {
		metas, statErr = dockerStatPaths(ctx, key, paths)
		if statErr == nil && len(metas) == len(previous.metaVersions) && sameDockerVersions(metas, previous.metaVersions) {
			next.files = previous.files
			next.metaVersions = previous.metaVersions
			next.checked = previous.checked
			return previous.files, next, nil
		}
	}
	if metas == nil || statErr != nil {
		metas, err = dockerStatPaths(ctx, key, paths)
		if err != nil {
			return nil, previous, err
		}
	}
	var previousFiles map[string]fileChange
	if previous != nil && previous.docker {
		previousFiles = make(map[string]fileChange, len(previous.files))
		for _, f := range previous.files {
			previousFiles[f.path] = f
		}
	}
	files, versions, err := enrichDockerWorktreeStatus(ctx, key, files, metas, previousFiles)
	if err != nil {
		return nil, previous, err
	}
	next.files = files
	next.metaVersions = versions
	next.checked = time.Now()
	if contentDue {
		next.contentChecked = next.checked
	}
	return files, next, nil
}

func sameDockerVersions(metas []dockerFileMeta, versions []string) bool {
	if len(metas) != len(versions) {
		return false
	}
	for i := range metas {
		if metas[i].version() != versions[i] {
			return false
		}
	}
	return true
}

// enrichDockerWorktreeStatus 补齐每行的行数统计和版本指纹。内容哈希整批算一次，
// 未跟踪文件的行数也批量读取，避免按文件数启动 Docker。
func enrichDockerWorktreeStatus(ctx context.Context, key string, files []fileChange, metas []dockerFileMeta, previous map[string]fileChange) ([]fileChange, []string, error) {
	if len(files) == 0 {
		return files, nil, nil
	}
	staged, unstaged := worktreeNumStats(ctx, key)
	versions := make([]string, len(files))
	hashAt := map[int]string{}
	var hashPaths []string
	var hashRows []int
	for i := range files {
		meta := metas[i]
		versions[i] = meta.version()
		// 内容校验一次算完所有普通文件：未跟踪文件也在里面，行数只有内容变了才重读。
		if meta.regular() {
			hashPaths = append(hashPaths, files[i].path)
			hashRows = append(hashRows, i)
		}
	}
	if len(hashPaths) > 0 {
		if hashes, err := dockerHashFiles(ctx, key, hashPaths); err == nil && len(hashes) == len(hashPaths) {
			for j, i := range hashRows {
				hashAt[i] = hashes[j]
			}
		}
	}
	var statPaths []string
	var statSizes []int64
	var statRows []int
	for i := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		f := &files[i]
		f.stagedStat = staged[f.path]
		if h, ok := hashAt[i]; ok {
			f.worktreeVersion = "h:" + h
		} else {
			f.worktreeVersion = versions[i]
		}
		countLines := f.extra == "未跟踪"
		if !countLines {
			if !metas[i].regular() {
				if note := noteForDockerMeta(metas[i]); note != "" {
					f.worktreeVersion += ":" + note
				}
			}
			f.unstagedStat = unstaged[f.path]
			continue
		}
		// 内容没变的未跟踪文件可以沿用上一轮的行数，不必再读一遍。
		if prev, ok := previous[f.path]; ok && hashAt[i] != "" && prev.worktreeVersion == f.worktreeVersion {
			if prev.unstagedStat.has {
				f.unstagedStat = prev.unstagedStat
				continue
			}
		}
		switch {
		case !metas[i].regular() || metas[i].size > int64(maxDiffBytes):
			if isImageFile(f.path) {
				f.unstagedStat = lineStat{binary: true, has: true}
			}
		case isImageFile(f.path):
			f.unstagedStat = lineStat{binary: true, has: true}
		default:
			statPaths = append(statPaths, f.path)
			statSizes = append(statSizes, metas[i].size)
			statRows = append(statRows, i)
		}
	}
	if len(statPaths) > 0 {
		stats, err := dockerUntrackedStats(ctx, key, statPaths, statSizes)
		if err != nil {
			return nil, nil, err
		}
		for j, i := range statRows {
			files[i].unstagedStat = stats[j]
		}
	}

	return files, versions, nil
}
