package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

type patchHunk struct{ label, patch string }

func splitPatch(patch string) ([]patchHunk, error) {
	var hunks []patchHunk
	headerEnd, start, offset := -1, -1, 0
	for line := range strings.SplitAfterSeq(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			if start >= 0 {
				hunks[len(hunks)-1].patch = patch[:headerEnd] + patch[start:offset]
			} else {
				headerEnd = offset
			}
			start = offset
			hunks = append(hunks, patchHunk{label: strings.TrimSpace(line)})
		} else if start < 0 {
			if strings.HasPrefix(line, "GIT binary patch") || strings.HasPrefix(line, "rename from ") || strings.HasPrefix(line, "new file mode ") || strings.HasPrefix(line, "deleted file mode ") || strings.HasPrefix(line, "old mode ") {
				return nil, errors.New("新增、删除、二进制和模式变更请使用文件级暂存")
			}
		}
		offset += len(line)
	}
	if start >= 0 {
		hunks[len(hunks)-1].patch = patch[:headerEnd] + patch[start:]
	}
	return hunks, nil
}
func readIndexPatch(ctx context.Context, dir, path string, cached bool) (string, error) {
	args := []string{"--literal-pathspecs", "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--no-renames", "--binary"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--", path)
	b, e := runGitLimit(ctx, dir, 4<<20, args...)
	return string(b), e
}
func applyIndexHunk(ctx context.Context, dir, path, original string, cached bool, hunk int) error {
	current, e := readIndexPatch(ctx, dir, path, cached)
	if e != nil {
		return e
	}
	if current != original {
		return errors.New("文件或暂存区已变化，请关闭后重新读取")
	}
	hunks, e := splitPatch(original)
	if e != nil {
		return e
	}
	if hunk < 0 || hunk >= len(hunks) {
		return errors.New("请选择改动块")
	}
	f, e := os.CreateTemp("", "xylring-patch-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.WriteString(hunks[hunk].patch); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	args := []string{"apply", "--cached", "--whitespace=nowarn"}
	if cached {
		args = append(args, "--reverse")
	}
	check := append(append([]string{}, args...), "--check", f.Name())
	if _, e = runGit(ctx, dir, check...); e != nil {
		return fmt.Errorf("改动块不能应用：%w", e)
	}
	_, e = runGit(ctx, dir, append(args, f.Name())...)
	return e
}
