package gitx

import (
	"os"
	"slices"
	"strings"
)

// SkippedRepoDirs lists common non-repository/dependency directories pruned during repo scans.
var SkippedRepoDirs = []string{
	".git",
	"node_modules",
	"vendor",
	".venv",
	"venv",
	"__pycache__",
	".mypy_cache",
	".pytest_cache",
	".ruff_cache",
	"target",
	"dist",
	"build",
	"out",
	".next",
	".nuxt",
	".turbo",
	".cache",
	".gradle",
	"Pods",
}

func SkipRepoDir(name string) bool {
	return slices.Contains(SkippedRepoDirs, name)
}

func GitEnv() []string {
	return MergeGitEnv(nil)
}

func MergeGitEnv(extra []string) []string {
	drop := map[string]bool{
		"LC_ALL": true, "LANG": true, "LC_MESSAGES": true,
		"GIT_TERMINAL_PROMPT": true, "GIT_PAGER": true, "PAGER": true,
		// status 和 diff 只碰可选锁；关掉它才敢把 status 算成读操作。
		// 写操作的强制锁不受影响。
		"GIT_OPTIONAL_LOCKS": true,
	}
	for _, e := range extra {
		if i := strings.IndexByte(e, '='); i > 0 {
			drop[e[:i]] = true
		}
	}
	out := make([]string, 0, 32+len(extra))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 && drop[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	out = append(out, "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	return append(out, extra...)
}

func TrimMsg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}
