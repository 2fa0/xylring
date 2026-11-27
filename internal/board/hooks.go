package board

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
)

// Hooks injected by the host application (cmd/xylring-main).
// Sensible defaults are provided so internal/board can be tested standalone.
var (
	UserConfigDir = os.UserConfigDir

	CanonicalRepoDir = func(path string) string {
		return filepath.Clean(path)
	}

	RepoTitle = func(path string) string {
		if path == "" {
			return ""
		}
		return filepath.Base(filepath.Clean(path))
	}

	PrepareGitCommand = func(ctx context.Context, dir string, extraEnv []string, args ...string) (*exec.Cmd, string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if len(extraEnv) > 0 {
			cmd.Env = append(os.Environ(), extraEnv...)
		}
		return cmd, "git", nil
	}

	AppVersion = func() string {
		return ""
	}
)

func configFile(name string) (string, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Xylring", name), nil
}
