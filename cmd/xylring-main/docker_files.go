package main

// 容器工作区没有本机路径可读，所以文件访问必须单独实现：
// 一次批量 stat 覆盖所有脏文件，读取只在本机内存里留副本。
// 这一层只被容器分支调用，不把整个项目改成远程文件系统。
import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
)

const (
	// dockerStatBatchSize 控制单次 docker exec 的 argv 长度。批次数随文件数缓慢增长，
	// 但不会变成每个文件一次调用。
	dockerStatBatchSize = 200
	// dockerHashBatchSize 让 `git hash-object` 一次算完一批内容指纹。
	dockerHashBatchSize = 200
)

// dockerFileMeta 是一条容器内路径的元数据。行序与请求顺序一一对应，
// 因此不需要把任意文件名字节塞进输出协议。
type dockerFileMeta struct {
	kind  byte // F 普通文件，D 目录，L 符号链接，M 其他，E 无法读取，0 未知
	size  int64
	mtime int64
	dev   string
	ino   string
	mode  string
}

func (m dockerFileMeta) present() bool { return m.kind != 0 && m.kind != 'E' }
func (m dockerFileMeta) regular() bool { return m.kind == 'F' }
func (m dockerFileMeta) version() string {
	return fmt.Sprintf("%c:%d:%d:%s:%s:%s", m.kind, m.size, m.mtime, m.dev, m.ino, m.mode)
}

// dockerStatScript 每条路径输出一行，顺序与参数一致。路径通过 argv 传入，
// 所以换行、空格、百分号都只是普通字节；不使用 shell 插值。
const dockerStatScript = `if ! command -v stat >/dev/null 2>&1; then
  printf 'xylring: missing stat\n' >&2
  exit 3
fi
for p in "$@"; do
  if [ -L "$p" ]; then printf 'L\n'
  elif [ -f "$p" ]; then
    if s=$(stat -c '%s %Y %d %i %f' -- "$p" 2>/dev/null); then printf 'F %s\n' "$s"; else printf 'E\n'; fi
  elif [ -d "$p" ]; then printf 'D\n'
  elif [ -e "$p" ]; then printf 'M\n'
  else printf 'E\n'; fi
done`

// safeRepoRelPath 拒绝会让读取逃出仓库的相对路径。绝对路径只允许由 Git 返回
// 的 git-path 使用，用户文件名一律走这里。
func safeRepoRelPath(rel string) (string, error) {
	if rel == "" {
		return "", errors.New("路径为空")
	}
	if strings.ContainsRune(rel, 0) {
		return "", errors.New("路径包含无效字符")
	}
	if strings.HasPrefix(rel, "/") {
		return "", errors.New("路径超出仓库")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", errors.New("路径超出仓库")
		}
	}
	clean := path.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, "../") {
		return "", errors.New("路径超出仓库")
	}
	return clean, nil
}

// containerPath 把 Git 返回的路径落到容器内。绝对路径保持原样，
// 相对路径相对仓库根——命令的 --workdir 始终是仓库根。
func containerPath(repoPath, p string) string {
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	return path.Join(repoPath, p)
}

// dockerStatPaths 批量查询容器内路径。返回顺序与输入一致。
func dockerStatPaths(ctx context.Context, key string, paths []string) ([]dockerFileMeta, error) {
	t, err := dockerTargetForCommand(key)
	if err != nil {
		return nil, err
	}
	out := make([]dockerFileMeta, 0, len(paths))
	for start := 0; start < len(paths); start += dockerStatBatchSize {
		end := min(len(paths), start+dockerStatBatchSize)
		chunk := paths[start:end]
		for _, p := range chunk {
			if strings.ContainsRune(p, 0) {
				return nil, errors.New("路径包含无效字符")
			}
		}
		argv := append([]string{"sh", "-c", dockerStatScript, "sh"}, chunk...)
		raw, err := dockerRun(ctx, t, t.path, nil, 4<<20, "", argv...)
		if err != nil {
			return nil, err
		}
		metas, err := parseDockerStatOutput(string(raw), len(chunk))
		if err != nil {
			return nil, err
		}
		out = append(out, metas...)
	}
	return out, nil
}

func parseDockerStatOutput(raw string, want int) ([]dockerFileMeta, error) {
	lines := strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		lines = nil
	}
	if len(lines) != want {
		return nil, errors.New("容器返回的文件信息不完整")
	}
	out := make([]dockerFileMeta, 0, want)
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "F "):
			fields := strings.Fields(line[2:])
			if len(fields) < 5 {
				return nil, errors.New("容器返回的文件信息不完整")
			}
			size, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil {
				return nil, errors.New("容器返回的文件大小无效")
			}
			mtime, _ := strconv.ParseInt(fields[1], 10, 64)
			out = append(out, dockerFileMeta{kind: 'F', size: size, mtime: mtime, dev: fields[2], ino: fields[3], mode: fields[4]})
		case len(line) == 1:
			out = append(out, dockerFileMeta{kind: line[0]})
		default:
			return nil, errors.New("容器返回的文件信息无法解析")
		}
	}
	return out, nil
}

// dockerStatOne 是单文件版本，供一次性读取使用。
func dockerStatOne(ctx context.Context, key, p string) (dockerFileMeta, error) {
	metas, err := dockerStatPaths(ctx, key, []string{p})
	if err != nil {
		return dockerFileMeta{}, err
	}
	if len(metas) != 1 {
		return dockerFileMeta{}, errors.New("容器返回的文件信息不完整")
	}
	return metas[0], nil
}

// 同一文件句柄完成边界检查和读取，不能校验路径后再按路径打开。
// /proc 中的句柄链接来自内核；缺少它时拒绝读取。
const dockerOpenFileScript = `fd="/proc/$$/fd/3"
if [ -L "$p" ]; then echo 'xylring: symlink' >&2; exit 40; fi
if [ ! -e "$p" ]; then
  if stat -- "$p" >/dev/null; then :; else exit 1; fi
fi
if [ ! -f "$p" ]; then echo 'xylring: special file' >&2; exit 41; fi
exec 3< "$p" || exit 1
if [ ! -e "$fd" ]; then echo 'xylring: missing descriptor access' >&2; exit 43; fi
if [ ! -f "$fd" ]; then echo 'xylring: unsafe file descriptor' >&2; exit 41; fi
resolved=$(readlink -- "$fd" && printf '.') || exit 1
resolved=${resolved%?}
resolved=${resolved%?}
if [ -n "$root" ]; then
  case "$resolved" in "$root"/*) ;; *) echo 'xylring: outside repository' >&2; exit 42;; esac
fi
`

func dockerReadPath(ctx context.Context, key, p string) ([]byte, string, error) {
	t, err := dockerTargetForCommand(key)
	if err != nil {
		return nil, "", err
	}
	root := ""
	if !strings.HasPrefix(p, "/") {
		p, err = safeRepoRelPath(p)
		if err != nil {
			return nil, "", err
		}
		root = t.path
	}
	limit := maxDiffBytes
	if isImageFile(p) {
		limit = 32 << 20
	}
	// 先查大小避免加载已知超限文件；实际读取仍受输出预算约束。
	meta, err := dockerStatOne(ctx, key, p)
	if err != nil {
		return nil, "", err
	}
	if meta.size > int64(limit) {
		return nil, fmt.Sprintf("文件过大（%d 字节），未载入", meta.size), nil
	}
	data, err := dockerRun(ctx, t, t.path, nil, limit+1, "", "sh", "-c", "p=$1; root=$2; limit=$3\n"+dockerOpenFileScript+`head -c "$limit" <&3`, "sh", p, root, strconv.Itoa(limit+1))
	if err != nil {
		switch {
		case errors.Is(err, errDockerOutputLimit):
			return nil, "文件在读取时变大，未载入", nil
		case strings.Contains(err.Error(), "xylring: missing descriptor access"):
			return nil, "", errors.New(tr("容器不支持文件句柄校验，无法安全读取文件"))
		case strings.Contains(err.Error(), "xylring: outside repository"):
			return nil, "", errors.New(tr("路径超出仓库"))
		case strings.Contains(err.Error(), "xylring: symlink"):
			return nil, "符号链接，未载入", nil
		case strings.Contains(err.Error(), "xylring: special file"), strings.Contains(err.Error(), "xylring: unsafe file descriptor"):
			return nil, "不是普通文件", nil
		case dockerPathMissing(err):
			return nil, "", nil
		}
		return nil, "", err
	}
	if len(data) > limit {
		return nil, "文件在读取时变大，未载入", nil
	}
	return data, "", nil
}

func dockerPathMissing(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such file") || strings.Contains(s, "cannot open") && strings.Contains(s, "not found")
}

// dockerReadFileByGitPath 读由 `git rev-parse --git-path` 得到的路径：
// 它可能落在仓库目录之外（例如已配置的 Hook），只能由 Git 解析所得，不能作为浏览入口。
func dockerReadGitPathFile(ctx context.Context, key, name string) ([]byte, bool, error) {
	p, err := gitFileContainerPath(ctx, key, name)
	if err != nil {
		return nil, false, err
	}
	data, note, err := dockerReadPath(ctx, key, p)
	if err != nil {
		return nil, false, err
	}
	if note != "" {
		return nil, true, errors.New(note)
	}
	if data == nil {
		return nil, false, nil
	}
	return data, true, nil
}

func gitFileContainerPath(ctx context.Context, key, name string) (string, error) {
	out, err := runGit(ctx, key, "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	t, err := dockerTargetForCommand(key)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSuffix(string(out), "\n")
	if raw == "" {
		return "", errors.New("Git 没有返回路径")
	}
	return containerPath(t.path, raw), nil
}

// repoPathExists 判断仓库目录下（或 Git 返回的绝对路径上）是否存在某个文件。
func repoPathExists(ctx context.Context, key, containerFilePath string) (bool, error) {
	if !isDockerRepoPath(key) {
		return false, nil
	}
	meta, err := dockerStatOne(ctx, key, containerFilePath)
	if err != nil {
		return false, err
	}
	return meta.present(), nil
}

func noteForDockerMeta(meta dockerFileMeta) string {
	switch meta.kind {
	case 'L':
		return "符号链接，未载入"
	case 'D', 'M':
		return "不是普通文件"
	}
	return ""
}

// dockerHashFiles 一次容器调用算多个文件的内容哈希。返回顺序与输入一致；
// 文件在查询时消失等局部问题会让整批退回元数据指纹，不阻塞刷新。
func dockerHashFiles(ctx context.Context, key string, paths []string) ([]string, error) {
	if _, err := dockerTargetForCommand(key); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for start := 0; start < len(paths); start += dockerHashBatchSize {
		end := min(len(paths), start+dockerHashBatchSize)
		chunk := paths[start:end]
		target, _ := parseDockerRepoKey(key)
		script := `root=$1; shift
for p in "$@"; do
` + dockerOpenFileScript + `git hash-object --stdin <&3 || exit 1
done`
		argv := append([]string{"sh", "-c", script, "sh", target.path}, chunk...)
		raw, err := dockerRun(ctx, target, target.path, nil, 8<<20, "", argv...)
		if err != nil {
			return nil, err
		}
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(lines) != len(chunk) {
			return nil, errors.New("容器返回的内容哈希不完整")
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if len(line) != 40 {
				return nil, errors.New("容器返回的内容哈希无效")
			}
			out = append(out, line)
		}
	}
	return out, nil
}

// dockerTargetForCommand 校验键并解释执行目标。所有容器文件操作都从这里进入。
func dockerTargetForCommand(key string) (repoTarget, error) {
	t, ok := parseDockerRepoKey(key)
	if !ok {
		return repoTarget{}, errors.New("不是容器仓库")
	}
	return t, nil
}

// 按文件数与总字节预算分批，避免统计未跟踪文件时逐个启动 Docker。
func dockerUntrackedStats(ctx context.Context, key string, paths []string, sizes []int64) ([]lineStat, error) {
	target, err := dockerTargetForCommand(key)
	if err != nil {
		return nil, err
	}
	var stats []lineStat
	for start := 0; start < len(paths); {
		end, total := start, int64(0)
		for end < len(paths) && end-start < dockerStatBatchSize && (end == start || total+sizes[end] <= 8<<20) {
			total += sizes[end]
			end++
		}
		script := `root=$1; limit=$2; shift 2
for tool in head base64 tr; do
  command -v "$tool" >/dev/null 2>&1 || { echo "xylring: missing $tool" >&2; exit 1; }
done
for p in "$@"; do
` + dockerOpenFileScript + `data=$(head -c "$limit" <&3 | base64 | tr -d '\n') || exit 1
printf '%s\n' "$data"
done`
		argv := append([]string{"sh", "-c", script, "sh", target.path, strconv.Itoa(maxDiffBytes + 1)}, paths[start:end]...)
		// 文件在查询后增长时也不能突破整批输出预算。
		raw, err := dockerRun(ctx, target, target.path, nil, 16<<20, "", argv...)
		if err != nil {
			return nil, err
		}
		rows := bytes.Split(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\n'})
		if len(rows) != end-start {
			return nil, errors.New("容器返回的文件信息不完整")
		}
		for _, row := range rows {
			data, err := base64.StdEncoding.DecodeString(string(row))
			if err != nil {
				return nil, err
			}
			stat := lineStat{}
			if len(data) <= maxDiffBytes {
				stat.has = true
				stat.binary = bytes.IndexByte(data, 0) >= 0
				if !stat.binary {
					stat.added = bytes.Count(data, []byte{'\n'})
					if len(data) > 0 && data[len(data)-1] != '\n' {
						stat.added++
					}
				}
			}
			stats = append(stats, stat)
		}
		start = end
	}
	return stats, nil
}
