package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func worktreeStatus(ctx context.Context, dir string) ([]fileChange, error) {
	out, err := runGit(ctx, dir, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	return enrichWorktreeStatus(ctx, dir, parsePorcelainZ(out))
}

func enrichWorktreeStatus(ctx context.Context, dir string, files []fileChange) ([]fileChange, error) {
	if len(files) == 0 {
		return files, nil
	}
	staged, unstaged := worktreeNumStats(ctx, dir)
	scratch := make([]byte, 64<<10)
	for i := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f := &files[i]
		f.stagedStat = staged[f.path]
		countLines := f.extra == "未跟踪"
		stamp, note, lines, err := fingerprintWorktreeFile(dir, f.path, scratch, countLines)
		f.worktreeVersion = stamp
		if countLines && err == nil {
			switch {
			case isImageFile(f.path) || strings.HasPrefix(note, "二进制文件"):
				f.unstagedStat = lineStat{binary: true, has: true}
			case note == "":
				f.unstagedStat = lineStat{added: lines, has: true}
			}
			continue
		}
		f.unstagedStat = unstaged[f.path]
	}
	return files, nil
}

func fingerprintWorktreeFile(dir, path string, scratch []byte, countLines bool) (stamp, note string, lines int, err error) {
	f, limit, note, err := openWorktreeFile(dir, path)
	h := sha256.New()
	if f != nil {
		defer f.Close()
		r := io.LimitedReader{R: f, N: limit + 1}
		var binary bool
		var readErr error
		last := byte('\n')
		isImage := isImageFile(path)
		for r.N > 0 && readErr == nil {
			var n int
			n, readErr = r.Read(scratch)
			chunk := scratch[:n]
			h.Write(chunk)
			binary = binary || (!isImage && bytes.IndexByte(chunk, 0) >= 0)
			if n > 0 && countLines && !binary && !isImage {
				lines += bytes.Count(chunk, []byte{'\n'})
				last = chunk[n-1]
			}
		}
		switch {
		case r.N == 0:
			note = "文件在读取时变大，未载入"
		case readErr != io.EOF:
			if !os.IsNotExist(readErr) {
				err = readErr
			}
		case binary:
			note = fmt.Sprintf("二进制文件（%d 字节）", limit+1-r.N)
		}
		if r.N == 0 || readErr != io.EOF || binary {
			h.Reset()
			lines = 0
		} else if last != '\n' {
			lines++
		}
	}
	h.Write([]byte{0})
	h.Write([]byte(note))
	if err != nil {
		h.Write([]byte{0})
		h.Write([]byte(err.Error()))
	}
	var digest [sha256.Size]byte
	return hex.EncodeToString(h.Sum(digest[:0])), note, lines, err
}

// worktreeNumStats 分别读索引侧（HEAD→index）和工作区侧（index→工作区）的增删行数。
// 一个文件两侧都有改动时，两行各显示自己那一份，行上的 +N/-N 才能和点开的 diff 对上。
func worktreeNumStats(ctx context.Context, dir string) (staged, unstaged map[string]lineStat) {
	staged = numStatByPath(ctx, dir, true)
	unstaged = numStatByPath(ctx, dir, false)
	return staged, unstaged
}

func numStatByPath(ctx context.Context, dir string, cached bool) map[string]lineStat {
	base := []string{"--no-optional-locks", "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-M", "-z"}
	// 新仓库还没有 HEAD：索引侧的基准退回空树，这时 --cached 不带 HEAD。
	specs := [][]string{append(append([]string{}, base...), "--")}
	if cached {
		specs = [][]string{
			append(append([]string{}, base...), "--cached", "HEAD", "--"),
			append(append([]string{}, base...), "--cached", "--"),
		}
	}
	for _, args := range specs {
		out, err := runGit(ctx, dir, args...)
		if err != nil {
			continue
		}
		stats := map[string]lineStat{}
		for _, s := range parseNumStatZ(out) {
			stats[s.path] = lineStat{added: s.added, deleted: s.deleted, binary: s.binary, has: true}
		}
		return stats
	}
	return nil
}

// expandWorktreeRows 把工作区状态拆成文件列表的行：已暂存在上，未暂存在下，
// 两侧都有改动的文件出现两次。冲突和未跟踪归在未暂存一组。
func expandWorktreeRows(files []fileChange) []fileChange {
	rows := make([]fileChange, 0, len(files)*2)
	var staged, unstaged []fileChange
	for _, f := range files {
		switch {
		case f.extra == "冲突":
			unstaged = append(unstaged, worktreeRow(f, sideUnstaged, f.status, f.extra))
		case f.hasStaged() && f.hasUnstaged():
			staged = append(staged, worktreeRow(f, sideStaged, sideStatus(f, f.indexStatus), ""))
			unstaged = append(unstaged, worktreeRow(f, sideUnstaged, sideStatus(f, f.workStatus), ""))
		case f.hasStaged():
			staged = append(staged, worktreeRow(f, sideStaged, sideStatus(f, f.indexStatus), ""))
		default:
			status := sideStatus(f, f.workStatus)
			if f.extra == "未跟踪" {
				status = "A"
			}
			unstaged = append(unstaged, worktreeRow(f, sideUnstaged, status, f.extra))
		}
	}
	rows = append(rows, staged...)
	return append(rows, unstaged...)
}

// sideStatus 取一侧的状态字母。测试和旧调用点没有 porcelain 的字节时退回原来的状态。
func sideStatus(f fileChange, b byte) string {
	if b == 0 {
		return f.status
	}
	return statusLetter(string(b))
}

func worktreeRow(f fileChange, side fileSide, status, extra string) fileChange {
	f.side = side
	f.status, f.extra = status, extra
	// 一行只属于一侧：另一侧的状态清掉，hasStaged/hasUnstaged 才是这一行的意思。
	if side == sideStaged {
		f.workStatus = ' '
	} else {
		f.indexStatus = ' '
	}
	stat := f.stagedStat
	if side == sideUnstaged {
		stat = f.unstagedStat
	}
	if stat.has {
		f.added, f.deleted, f.binary, f.hasStat = stat.added, stat.deleted, stat.binary, true
	}
	return f
}

// parsePorcelainZ 解析 git status --porcelain=v1 -z。
func parsePorcelainZ(b []byte) []fileChange {
	parts := bytes.Split(b, []byte{0})
	var out []fileChange
	for i := 0; i < len(parts); {
		if len(parts[i]) == 0 {
			i++
			continue
		}
		rec := string(parts[i])
		i++
		if len(rec) < 2 {
			continue
		}
		x, y := rec[0], rec[1]
		if x == '!' && y == '!' {
			continue
		}
		path := ""
		if len(rec) >= 3 {
			path = rec[3:]
			if rec[2] != ' ' {
				path = rec[2:]
			}
		}
		ch := fileChange{path: path, indexStatus: x, workStatus: y}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			if i < len(parts) {
				// Porcelain -z emits the destination first, then the source.
				ch.oldPath = string(parts[i])
				i++
			}
		}
		ch.status, ch.extra = porcelainState(x, y)
		if ch.path == "" {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func porcelainState(x, y byte) (string, string) {
	if x == '?' || y == '?' {
		return "A", "未跟踪"
	}
	if x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D' {
		return "U", "冲突"
	}
	if y == 'D' || x == 'D' && y == ' ' {
		return "D", "已删除"
	}
	if x == 'R' || y == 'R' {
		return "R", "重命名"
	}
	if x == 'A' && y == ' ' {
		return "A", "已暂存"
	}
	if x == 'A' {
		return "A", "已暂存并继续改"
	}
	if x != ' ' && y != ' ' {
		return "M", "已暂存并继续改"
	}
	if x != ' ' {
		return statusLetter(string(x)), "已暂存"
	}
	if y != ' ' {
		return statusLetter(string(y)), "未暂存"
	}
	return "M", "已修改"
}

func openWorktreeFile(dir, rel string) (*os.File, int64, string, error) {
	if rel == "" {
		return nil, 0, "", nil
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	base, err := filepath.Abs(dir)
	if err != nil {
		return nil, 0, "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return nil, 0, "", err
	}
	if relPath, err := filepath.Rel(base, abs); err != nil || strings.HasPrefix(relPath, "..") {
		return nil, 0, "", errors.New("路径超出仓库")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, "", nil
		}
		return nil, 0, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, "符号链接，未载入", nil
	}
	if !info.Mode().IsRegular() {
		return nil, 0, "不是普通文件", nil
	}
	limit := int64(maxDiffBytes)
	if isImageFile(rel) {
		limit = 32 << 20
	}
	if info.Size() > limit {
		return nil, 0, fmt.Sprintf("文件过大（%d 字节），未载入", info.Size()), nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, 0, "", err
	}
	return f, limit, "", nil
}

func readWorktreeFile(dir, rel string) ([]byte, string, error) {
	f, limit, note, err := openWorktreeFile(dir, rel)
	if f == nil {
		return nil, note, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, "文件在读取时变大，未载入", nil
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if isImageFile(rel) {
		return data, "", nil
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Sprintf("二进制文件（%d 字节）", len(data)), nil
	}
	return data, "", nil
}

func (f fileChange) hasStaged() bool {
	if f.indexStatus != 0 {
		return f.indexStatus != ' ' && f.indexStatus != '?' && f.indexStatus != '!' && f.extra != "冲突"
	}
	return f.extra == "已暂存" || f.extra == "重命名" || f.extra == "已暂存并继续改"
}

func (f fileChange) hasUnstaged() bool {
	if f.workStatus != 0 {
		return f.workStatus != ' ' && f.workStatus != '?' && f.workStatus != '!' && f.extra != "冲突"
	}
	return f.extra != "已暂存" && f.extra != "重命名"
}
