package main

import (
	"bytes"
	"context"
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
	return parsePorcelainZ(out), nil
}

func worktreeKey(files []fileChange) string {
	parts := make([]string, 0, len(files))
	for _, f := range files {
		parts = append(parts, f.status+"|"+f.extra+"|"+f.oldPath+"|"+f.path)
	}
	return strings.Join(parts, "\n")
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
				ch.oldPath = path
				ch.path = string(parts[i])
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
