package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func worktreeStatus(ctx context.Context, dir string) ([]fileChange, error) {
	out, err := runGit(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	files := parsePorcelainZ(out)
	applyWorktreeNumStat(ctx, dir, files)
	for i := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Hash the bounded preview content so edits with unchanged status or timestamps are noticed.
		data, note, err := readWorktreeFile(dir, files[i].path)
		h := sha256.New()
		h.Write(data)
		h.Write([]byte{0})
		h.Write([]byte(note))
		if err != nil {
			h.Write([]byte{0})
			h.Write([]byte(err.Error()))
		}
		files[i].worktreeVersion = fmt.Sprintf("%x", h.Sum(nil))
	}
	return files, nil
}

func applyWorktreeNumStat(ctx context.Context, dir string, files []fileChange) {
	if len(files) == 0 {
		return
	}
	statOut, err := runGit(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-M", "-z", "HEAD", "--")
	if err != nil {
		statOut, err = runGit(ctx, dir, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-M", "-z", "--cached", "--")
	}
	if err == nil {
		applyNumStat(files, parseNumStatZ(statOut))
	}
	for i := range files {
		if files[i].hasStat || files[i].extra != "未跟踪" {
			continue
		}
		added, binary, ok := worktreeUntrackedStat(dir, files[i].path)
		if !ok {
			continue
		}
		files[i].added = added
		files[i].binary = binary
		files[i].hasStat = true
	}
}

func worktreeUntrackedStat(dir, rel string) (added int, binary, ok bool) {
	data, note, err := readWorktreeFile(dir, rel)
	if err != nil {
		return 0, false, false
	}
	if note != "" {
		if strings.HasPrefix(note, "二进制文件") {
			return 0, true, true
		}
		return 0, false, false
	}
	return countTextLines(data), false, true
}

func countTextLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func worktreeKey(files []fileChange) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%t\x00%t", f.status, f.extra, f.oldPath, f.path, f.worktreeVersion, f.added, f.deleted, f.binary, f.hasStat))
	}
	return strings.Join(parts, "\x00")
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
		ch := fileChange{path: path}
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

func readWorktreeFile(dir, rel string) ([]byte, string, error) {
	if rel == "" {
		return nil, "", nil
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	base, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return nil, "", err
	}
	if relPath, err := filepath.Rel(base, abs); err != nil || strings.HasPrefix(relPath, "..") {
		return nil, "", errors.New("路径超出仓库")
	}
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, "符号链接，未载入", nil
	}
	if !info.Mode().IsRegular() {
		return nil, "不是普通文件", nil
	}
	if info.Size() > maxDiffBytes {
		return nil, fmt.Sprintf("文件过大（%d 字节），未载入", info.Size()), nil
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDiffBytes+1))
	if len(data) > maxDiffBytes {
		return nil, "文件在读取时变大，未载入", nil
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Sprintf("二进制文件（%d 字节）", len(data)), nil
	}
	return data, "", nil
}
