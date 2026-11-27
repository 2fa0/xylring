package app

// Docker 支持只做一件事：把已有的 Git 命令送到本机容器里执行。
// 仓库身份因此不能再用本机路径——同一个 /workspace/api 在不同容器里是两个仓库。
// 本文件只放身份、Docker CLI 查找、容器检查和命令构造；文件读取在 docker_files.go。
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	dockerRepoKeyPrefix = "xylring-docker:v1:"
	dockerRepoKeyVer    = "1"
	// dockerExecTimeout 只限制「查上下文 / 列容器 / 查容器」这类管理命令，
	// Git 命令自己的超时由调用方 context 决定。
	dockerCLITimeout = 15 * time.Second
	// dockerRetryFailedAfter 与 Git 一样：装好 Docker 后不该需要重启应用。
	dockerRetryFailedAfter = 15 * time.Second
)

// repoTarget 是「这条 Git 命令在哪里跑」。本机路径和 Docker 仓库共用同一套接口，
// 但身份、路径规则和文件访问方式完全不同。
type repoTarget struct {
	kind      repoTargetKind
	localDir  string
	context   string
	endpoint  string
	container string // 完整容器 ID；容器名只用于展示
	path      string // 容器内的仓库根路径
	name      string // 容器显示名，不参与身份
}

type repoTargetKind uint8

const (
	targetLocal repoTargetKind = iota
	targetDocker
)

func (t repoTarget) isDocker() bool { return t.kind == targetDocker }

// dockerRepoRef 是容器键的载荷。字段名很短，但解码后必须逐项校验：
// 损坏的键不能被当成一个本机路径继续往下走。
type dockerRepoRef struct {
	Ver       string `json:"v"`
	Context   string `json:"c"`
	Endpoint  string `json:"e"`
	Container string `json:"i"`
	Path      string `json:"p"`
}

var dockerContainerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// dockerRepoKey 用描述数据拼出仓库键。显示名故意不参与：容器改名不该变成另一个仓库。
func dockerRepoKey(t repoTarget) string {
	ref := dockerRepoRef{
		Ver:       dockerRepoKeyVer,
		Context:   t.context,
		Endpoint:  t.endpoint,
		Container: t.container,
		Path:      t.path,
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return ""
	}
	return dockerRepoKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// parseDockerRepoKey 只接受完整、可校验的容器键。任何一项不成立都返回 false，
// 让调用方按本机路径处理，而不是猜一个容器。
func parseDockerRepoKey(s string) (repoTarget, bool) {
	if !strings.HasPrefix(s, dockerRepoKeyPrefix) {
		return repoTarget{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(dockerRepoKeyPrefix):])
	if err != nil {
		return repoTarget{}, false
	}
	var ref dockerRepoRef
	if json.Unmarshal(raw, &ref) != nil {
		return repoTarget{}, false
	}
	if ref.Ver != dockerRepoKeyVer {
		return repoTarget{}, false
	}
	if ref.Context == "" || ref.Endpoint == "" || ref.Container == "" || ref.Path == "" {
		return repoTarget{}, false
	}
	if strings.ContainsRune(ref.Context+ref.Endpoint+ref.Container+ref.Path, 0) {
		return repoTarget{}, false
	}
	if !strings.HasPrefix(ref.Endpoint, "unix://") {
		return repoTarget{}, false
	}
	if !dockerContainerIDPattern.MatchString(ref.Container) {
		return repoTarget{}, false
	}
	if !isCleanContainerAbsPath(ref.Path) {
		return repoTarget{}, false
	}
	t := repoTarget{
		kind:      targetDocker,
		context:   ref.Context,
		endpoint:  ref.Endpoint,
		container: ref.Container,
		path:      ref.Path,
	}
	t.name = dockerNameOf(ref.Container)
	return t, true
}

func isDockerRepoPath(s string) bool {
	_, ok := parseDockerRepoKey(s)
	return ok
}

// repoTargetOf 把界面里流转的路径字符串还原成执行目标。
func repoTargetOf(s string) repoTarget {
	if t, ok := parseDockerRepoKey(s); ok {
		return t
	}
	return repoTarget{kind: targetLocal, localDir: s}
}

func isCleanContainerAbsPath(p string) bool {
	if p == "" || !strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return false
	}
	return path.Clean(p) == p
}

// normalizeContainerRepoPath 处理用户输入的容器内路径：只接受 Linux 绝对路径。
func normalizeContainerRepoPath(raw string) (string, error) {
	p := raw
	if strings.TrimSpace(p) == "" {
		return "", errors.New("请填写容器内的仓库路径")
	}
	if strings.ContainsRune(p, 0) {
		return "", errors.New("路径包含无效字符")
	}
	if !strings.HasPrefix(p, "/") {
		return "", errors.New("请填写容器内的绝对路径，例如 /workspace/api")
	}
	clean := path.Clean(p)
	if clean == "/" {
		return "", errors.New("请填写仓库所在的子目录，而不是容器根目录")
	}
	return clean, nil
}

// ---- 容器显示名 ----

// 容器名只影响显示。键里没有它，所以改名不影响身份；为了重启后最近仓库仍能显示
// 正确的名字，把最近见到的 ID→名字单独存一份。
var dockerNames struct {
	sync.Mutex
	byID map[string]string
}

func dockerNameOf(id string) string {
	dockerNames.Lock()
	defer dockerNames.Unlock()
	if dockerNames.byID == nil {
		return dockerShortID(id)
	}
	if name := dockerNames.byID[id]; name != "" {
		return name
	}
	return dockerShortID(id)
}

func dockerShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func rememberDockerContainer(id, name string) {
	if id == "" || name == "" {
		return
	}
	dockerNames.Lock()
	if dockerNames.byID == nil {
		dockerNames.byID = map[string]string{}
	}
	old := dockerNames.byID[id]
	dockerNames.byID[id] = name
	dockerNames.Unlock()
	if old != name {
		scheduleDockerNamesSave()
	}
}

func init() {
	if b, err := os.ReadFile(dockerNamesPath()); err == nil {
		var m map[string]string
		if json.Unmarshal(b, &m) == nil {
			dockerNames.Lock()
			dockerNames.byID = m
			dockerNames.Unlock()
		}
	}
}

func dockerNamesPath() string {
	p, err := configFile("docker-containers.json")
	if err != nil {
		return ""
	}
	return p
}

var dockerNamesSave struct {
	sync.Mutex
	running bool
	dirty   bool
}

func scheduleDockerNamesSave() {
	dockerNamesSave.Lock()
	if dockerNamesSave.running {
		dockerNamesSave.dirty = true
		dockerNamesSave.Unlock()
		return
	}
	dockerNamesSave.running = true
	dockerNamesSave.Unlock()
	go func() {
		for {
			dockerNames.Lock()
			m := make(map[string]string, len(dockerNames.byID))
			for k, v := range dockerNames.byID {
				m[k] = v
			}
			dockerNames.Unlock()
			if p := dockerNamesPath(); p != "" {
				_ = writeJSON(p, m)
			}
			dockerNamesSave.Lock()
			if !dockerNamesSave.dirty {
				dockerNamesSave.running = false
				dockerNamesSave.Unlock()
				return
			}
			dockerNamesSave.dirty = false
			dockerNamesSave.Unlock()
		}
	}()
}

// ---- Docker CLI ----

// dockerUnavailableError 表示 Docker CLI 本身找不到或跑不起来，问题不在容器或仓库。
type dockerUnavailableError struct {
	message string
	err     error
}

func (e *dockerUnavailableError) Error() string { return e.message }
func (e *dockerUnavailableError) Unwrap() error { return e.err }

func isDockerUnavailable(err error) bool {
	var e *dockerUnavailableError
	return errors.As(err, &e)
}

var dockerResolve struct {
	sync.Mutex
	checked  bool
	path     string
	failure  *dockerUnavailableError
	failedAt time.Time
}

// dockerCommonPaths 覆盖从 Finder 启动时看不到的常见安装位置。
// PATH 和这里都不需要用户改 shell 配置。
func dockerCommonPaths() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	add("/usr/local/bin/docker")
	add("/opt/homebrew/bin/docker")
	add("/usr/bin/docker")
	add("/opt/local/bin/docker")
	add("/Applications/Docker.app/Contents/Resources/bin/docker")
	if home != "" {
		add(home + "/.docker/bin/docker")
		add(home + "/.rd/bin/docker")
		add(home + "/.orbstack/bin/docker")
		add(home + "/.colima/bin/docker")
		add(home + "/.local/bin/docker")
		add(home + "/.nix-profile/bin/docker")
	}
	return out
}

func dockerVersionOK(ctx context.Context, bin string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, bin, "--version")
	hideConsoleWindow(cmd)
	cmd.Env = dockerClientEnv(bin)
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// dockerCandidatePaths 是可替换的查找顺序入口：测试要能在没有 Docker 的机器上
// 验证失败分支，也要能指定一个假的 docker 可执行文件。
var dockerCandidatePaths = func() []string {
	var candidates []string
	if p, err := exec.LookPath("docker"); err == nil {
		candidates = append(candidates, p)
	}
	return append(candidates, dockerCommonPaths()...)
}

func probeDocker() (string, *dockerUnavailableError) {
	candidates := dockerCandidatePaths()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	seen := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		abs, err := dockerAbsPath(candidate)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		if err := dockerVersionOK(ctx, abs); err != nil {
			continue
		}
		return abs, nil
	}
	return "", &dockerUnavailableError{message: "未找到 Docker。请安装 Docker Desktop、OrbStack 或 colima，并确认 docker 命令可用"}
}

func dockerPath() (string, error) {
	if runtime.GOOS == "windows" {
		return "", &dockerUnavailableError{message: "Windows 暂不支持容器仓库"}
	}
	dockerResolve.Lock()
	defer dockerResolve.Unlock()
	if dockerResolve.checked {
		if dockerResolve.failure == nil {
			return dockerResolve.path, nil
		}
		if time.Since(dockerResolve.failedAt) < dockerRetryFailedAfter {
			return "", dockerResolve.failure
		}
	}
	bin, failure := probeDocker()
	dockerResolve.checked = true
	if failure != nil {
		dockerResolve.path, dockerResolve.failure, dockerResolve.failedAt = "", failure, time.Now()
		return "", failure
	}
	dockerResolve.path, dockerResolve.failure = bin, nil
	return bin, nil
}

func resetDockerResolution() {
	dockerResolve.Lock()
	dockerResolve.checked = false
	dockerResolve.failure = nil
	dockerResolve.Unlock()
}

// dockerClientEnv 只给 Docker 客户端用：去掉会让 --context 失效或指向远程的变量。
// 容器里的环境是另一回事，见 dockerContainerEnv。
func dockerClientEnv(bin string) []string {
	drop := map[string]bool{
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true,
		"DOCKER_TLS_VERIFY": true, "DOCKER_CERT_PATH": true,
	}
	out := make([]string, 0, 32)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 && drop[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	return gitBinaryEnv(bin, out)
}

// dockerHostManagedEnv 让「当前上下文」的查询尊重用户环境里的 DOCKER_CONTEXT，
// 但同样不接受 DOCKER_HOST 指到别处：那属于远程连接，第一版明确不打开。
func dockerHostManagedEnv(bin string) []string {
	out := make([]string, 0, 32)
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 && e[:i] == "DOCKER_HOST" {
			continue
		}
		out = append(out, e)
	}
	return gitBinaryEnv(bin, out)
}

func runDockerCLI(ctx context.Context, envFor func(string) []string, args ...string) ([]byte, error) {
	bin, err := dockerPath()
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, dockerCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(callCtx, bin, args...)
	hideConsoleWindow(cmd)
	if envFor != nil {
		cmd.Env = envFor(bin)
	} else {
		cmd.Env = dockerClientEnv(bin)
	}
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 4<<20, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if callCtx.Err() != nil {
		return stdout.Bytes(), fmt.Errorf("Docker 命令超时：%w", callCtx.Err())
	}
	if err == nil {
		return stdout.Bytes(), nil
	}
	return stdout.Bytes(), dockerCLIError(bin, err, stderr.String())
}

// dockerCLIError 把 Docker 客户端能给的几种失败翻成用户能处理的原因。
func dockerCLIError(bin string, err error, stderr string) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && samePath(pathErr.Path, bin) {
		resetDockerResolution()
		return &dockerUnavailableError{message: "Docker 无法运行：" + bin + " 已不存在", err: err}
	}
	msg := strings.TrimSpace(stderr)
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "cannot connect to the docker daemon"),
		strings.Contains(lower, "failed to connect"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "is the docker daemon running"):
		return &dockerUnavailableError{message: "Docker 未运行或无法连接，请先启动 Docker", err: err}
	case strings.Contains(lower, "context") && strings.Contains(lower, "does not exist"):
		return errors.New("Docker 连接已被删除，请重新选择容器")
	case strings.Contains(lower, "permission denied") && strings.Contains(lower, "docker.sock"):
		return errors.New("没有访问 Docker 的权限，请检查 Docker Desktop / 用户组设置")
	}
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(trimMsg(firstLine(msg)))
}

// dockerContext 是固定下来的本机连接。第一版只接受 Unix socket：
// SSH / TCP 属于远程 Docker，不在这里支持。
type dockerContext struct {
	Name     string
	Endpoint string
}

func currentDockerContext(ctx context.Context) (dockerContext, error) {
	out, err := runDockerCLI(ctx, dockerHostManagedEnv, "context", "show")
	if err != nil {
		return dockerContext{}, err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return dockerContext{}, errors.New("无法确定当前 Docker 连接")
	}
	return inspectDockerContext(ctx, name)
}

func inspectDockerContext(ctx context.Context, name string) (dockerContext, error) {
	raw, err := runDockerCLI(ctx, nil, "context", "inspect", name)
	if err != nil {
		return dockerContext{}, err
	}
	var list []struct {
		Name      string
		Endpoints struct {
			Docker struct {
				Host string
			}
		}
	}
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return dockerContext{}, errors.New("无法读取 Docker 连接信息")
	}
	host := strings.TrimSpace(list[0].Endpoints.Docker.Host)
	if host == "" {
		return dockerContext{}, errors.New("这个 Docker 连接没有可用的本机端点")
	}
	if !strings.HasPrefix(host, "unix://") {
		return dockerContext{}, errors.New("只支持本机 Docker：当前连接使用 " + dockerEndpointKind(host) + "，请切换到本机连接")
	}
	return dockerContext{Name: name, Endpoint: host}, nil
}

func dockerEndpointKind(host string) string {
	switch {
	case strings.HasPrefix(host, "ssh://"):
		return "SSH"
	case strings.HasPrefix(host, "tcp://"), strings.HasPrefix(host, "http://"), strings.HasPrefix(host, "https://"):
		return "TCP"
	case strings.HasPrefix(host, "npipe://"):
		return "Windows 命名管道"
	default:
		return "远程端点"
	}
}

// verifyDockerEndpoint 在每次打开和恢复时确认连接仍是同一个本机端点。
// 变了就不能继续把命令交给它：那可能是另一台机器或另一个 daemon。
func verifyDockerEndpoint(ctx context.Context, t repoTarget) error {
	dc, err := inspectDockerContext(ctx, t.context)
	if err != nil {
		return err
	}
	if dc.Endpoint != t.endpoint {
		return errors.New("Docker 连接已变化，请重新选择容器")
	}
	return nil
}

// dockerContainer 是容器列表的一行。
type dockerContainer struct {
	ID    string
	Name  string
	Image string
}

func dockerListContainers(ctx context.Context, dc dockerContext) ([]dockerContainer, error) {
	out, err := runDockerCLI(ctx, nil, "--host", dc.Endpoint, "ps", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}")
	if err != nil {
		return nil, err
	}
	var containers []dockerContainer
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		c := dockerContainer{ID: strings.TrimSpace(fields[0]), Name: strings.TrimSpace(fields[1]), Image: strings.TrimSpace(fields[2])}
		if !dockerContainerIDPattern.MatchString(c.ID) || c.Name == "" {
			continue
		}
		rememberDockerContainer(c.ID, c.Name)
		containers = append(containers, c)
	}
	slices.SortFunc(containers, func(a, b dockerContainer) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return containers, nil
}

// dockerContainerInfo 是一次容器检查的结果。
type dockerContainerInfo struct {
	ID         string
	Name       string
	Image      string
	Platform   string
	Status     string
	Running    bool
	Paused     bool
	WorkingDir string
	Mounts     []struct {
		Source      string
		Destination string
	}
}

func dockerInspectContainer(ctx context.Context, dc dockerContext, id string) (dockerContainerInfo, error) {
	raw, err := runDockerCLI(ctx, nil, "--host", dc.Endpoint, "inspect", "--type", "container", "--format", "{{json .}}", id)
	if err != nil {
		return dockerContainerInfo{}, err
	}
	var payload struct {
		ID       string
		Name     string
		Platform string
		Config   struct {
			Image      string
			WorkingDir string
		}
		Mounts []struct {
			Source      string
			Destination string
		}
		State struct {
			Status  string
			Running bool
			Paused  bool
		}
	}
	if json.Unmarshal(raw, &payload) != nil || payload.ID == "" {
		return dockerContainerInfo{}, errors.New("无法读取容器信息")
	}
	info := dockerContainerInfo{
		ID: payload.ID, Name: strings.TrimPrefix(payload.Name, "/"),
		Image: payload.Config.Image, Platform: payload.Platform,
		WorkingDir: payload.Config.WorkingDir, Mounts: payload.Mounts,
		Status: payload.State.Status, Running: payload.State.Running, Paused: payload.State.Paused,
	}
	rememberDockerContainer(info.ID, info.Name)
	return info, nil
}

// checkDockerTarget 确认容器还在、是 Linux、能跑 Git。停止和暂停分别给出原因。
func checkDockerTarget(ctx context.Context, t repoTarget) error {
	if err := verifyDockerEndpoint(ctx, t); err != nil {
		return err
	}
	dc := dockerContext{Name: t.context, Endpoint: t.endpoint}
	info, err := dockerInspectContainer(ctx, dc, t.container)
	if err != nil {
		return err
	}
	if info.ID != t.container {
		return errors.New("容器已变化，请重新选择容器")
	}
	if info.Platform != "" && info.Platform != "linux" {
		return fmt.Errorf("只支持 Linux 容器（%s）", info.Platform)
	}
	if info.Paused || strings.EqualFold(info.Status, "paused") {
		return errors.New("容器已暂停，请恢复后重试")
	}
	if !info.Running && !strings.EqualFold(info.Status, "running") {
		return errors.New("容器未运行，请启动后重试")
	}
	if _, err := dockerRun(ctx, t, t.path, nil, 1<<20, "", "git", "--version"); err != nil {
		if isDockerUnavailable(err) {
			return err
		}
		return fmt.Errorf("容器内 Git 不可用：%w", err)
	}
	return nil
}

// ---- 命令构造 ----

// dockerContainerEnv 是容器内 Git 的环境。它从固定值开始，只接受调用方明确给的覆盖，
// 不把本机 PATH、HOME、Git 配置路径或凭据带进去。
func dockerContainerEnv(t repoTarget, extra []string) []string {
	env := map[string]string{
		"LC_ALL":              "C",
		"LANG":                "C",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_PAGER":           "cat",
		"PAGER":               "cat",
		"GIT_OPTIONAL_LOCKS":  "0",
	}
	order := []string{"LC_ALL", "LANG", "GIT_TERMINAL_PROMPT", "GIT_PAGER", "PAGER", "GIT_OPTIONAL_LOCKS"}
	ceilingSet := false
	for _, e := range extra {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" || strings.ContainsAny(k+v, "\x00\n") {
			continue
		}
		if _, exists := env[k]; !exists {
			order = append(order, k)
		}
		env[k] = v
		if k == "GIT_CEILING_DIRECTORIES" {
			ceilingSet = true
		}
	}
	// 打开仓库之后的命令不能回退到父仓库。打开那一次由调用方显式传空值。
	if !ceilingSet {
		if parent := path.Dir(t.path); parent != "" && parent != t.path {
			if _, exists := env["GIT_CEILING_DIRECTORIES"]; !exists {
				order = append(order, "GIT_CEILING_DIRECTORIES")
			}
			env["GIT_CEILING_DIRECTORIES"] = parent
		}
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+env[k])
	}
	return out
}

// dockerEnvClears 是容器里可能被镜像预设、让命令偏离选定仓库的变量。
// 通过 env -u 清掉，比假定镜像干净可靠。
var dockerEnvClears = []string{
	"-u", "GIT_DIR",
	"-u", "GIT_WORK_TREE",
	"-u", "GIT_INDEX_FILE",
	"-u", "GIT_OBJECT_DIRECTORY",
	"-u", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	"-u", "GIT_COMMON_DIR",
	"-u", "GIT_NAMESPACE",
	"-u", "GIT_CEILING_DIRECTORIES",
}

// dockerExecArgv 是 `docker exec` 的完整参数，不含 docker 可执行文件。
// 不使用 shell：容器命令是独立 argv，文件名里的任何字节都不会被当成语法。
func dockerExecArgv(dc dockerContext, container, workdir string, env []string, argv ...string) []string {
	args := []string{"--host", dc.Endpoint, "exec", "-i"}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	args = append(args, container)
	args = append(args, "env")
	args = append(args, dockerEnvClears...)
	// 应用自己的环境必须在清除镜像环境之后设置。
	args = append(args, env...)
	return append(args, argv...)
}

// dockerGitCommand 复用现有入口：gitArgs 已经是完整参数，这里只负责把它送进容器。
func dockerGitCommand(ctx context.Context, t repoTarget, extraEnv []string, fullArgs ...string) (*exec.Cmd, string, error) {
	if err := verifyDockerEndpoint(ctx, t); err != nil {
		return nil, "", err
	}
	bin, err := dockerPath()
	if err != nil {
		return nil, "", err
	}
	workdir := t.path
	if workdir == "" {
		workdir = "/"
	}
	env := dockerContainerEnv(t, extraEnv)
	argv := dockerExecArgv(dockerContext{Name: t.context, Endpoint: t.endpoint}, t.container, workdir, env, append([]string{"git"}, fullArgs...)...)
	cmd := exec.CommandContext(ctx, bin, argv...)
	hideConsoleWindow(cmd)
	cmd.Env = dockerClientEnv(bin)
	cmd.WaitDelay = 2 * time.Second
	return cmd, bin, nil
}

// dockerRun 在容器里执行一条非 Git 命令（读文件、批量 stat、探测）。
// limit 是输出上限；input 用于标准输入。
func dockerRun(ctx context.Context, t repoTarget, workdir string, env []string, limit int, input string, argv ...string) ([]byte, error) {
	if err := verifyDockerEndpoint(ctx, t); err != nil {
		return nil, err
	}
	bin, err := dockerPath()
	if err != nil {
		return nil, err
	}
	if workdir == "" {
		workdir = "/"
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, bin, dockerExecArgv(dockerContext{Name: t.context, Endpoint: t.endpoint}, t.container, workdir, env, argv...)...)
	hideConsoleWindow(cmd)
	cmd.Env = dockerClientEnv(bin)
	cmd.WaitDelay = time.Second
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	stdout := limitedBuffer{limit: limit, cancel: cancel}
	stderr := limitedBuffer{limit: 64 << 10}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if stdout.overflow {
		if ctx.Err() != nil {
			return stdout.Bytes(), ctx.Err()
		}
		return stdout.Bytes(), errDockerOutputLimit
	}
	if ctx.Err() != nil {
		return stdout.Bytes(), ctx.Err()
	}
	if err != nil {
		if isDockerUnavailable(err) {
			return stdout.Bytes(), err
		}
		var pathErr *os.PathError
		if errors.As(err, &pathErr) && samePath(pathErr.Path, bin) {
			return stdout.Bytes(), &dockerUnavailableError{message: "Docker 无法运行：" + bin + " 已不存在", err: err}
		}
		return stdout.Bytes(), dockerExecError(t, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

var errDockerOutputLimit = errors.New("容器输出超过读取上限")

// dockerRunError 把一次 Docker 客户端调用的失败分成两类：
// Docker 自己没跑起来（类型化错误，界面据此区分「缺 Docker」与「仓库有问题」），
// 或者容器里的 Git 正常退出但报了错（原样保留 Git 的多行说明，例如 Hook 的拒绝理由）。
func dockerRunError(t repoTarget, bin string, err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if isDockerUnavailable(err) {
		return err
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) && samePath(pathErr.Path, bin) {
		resetDockerResolution()
		return &dockerUnavailableError{message: "Docker 无法运行：" + bin + " 已不存在", err: err}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if dockerCommandGone(err, string(stderr)) {
		return &dockerUnknownResultError{err: fmt.Errorf("%s: %w", dockerExecErrorMessage(t, err, string(stderr)), err)}
	}
	msg := strings.TrimSpace(string(stderr))
	lower := strings.ToLower(msg)
	for _, sign := range []string{
		"error response from daemon", "cannot start container", "oci runtime exec failed",
		"executable file not found in $path", "is not running", "is paused",
		"cannot connect to the docker daemon", "failed to connect", "connection refused",
		"no such container", "is the docker daemon running",
	} {
		if strings.Contains(lower, sign) {
			return dockerExecError(t, err, msg)
		}
	}
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(trimMsg(msg))
}

// dockerTargetError 是容器/连接层面的失败，而不是容器里的 Git 自己报的错。
// 两者都要给用户看，但只有后者可以原样沿用 stderr 的多行说明。
type dockerUnknownResultError struct{ err error }

func (e *dockerUnknownResultError) Error() string { return e.err.Error() }
func (e *dockerUnknownResultError) Unwrap() error { return e.err }

type dockerTargetError struct{ message string }

func (e *dockerTargetError) Error() string { return e.message }

func dockerTargetMessage(err error) bool {
	var e *dockerTargetError
	var unknown *dockerUnknownResultError
	return errors.As(err, &e) || errors.As(err, &unknown)
}

// dockerExecError 把容器命令的失败分成几类，避免把「Docker 没启动」「容器停了」
// 「仓库路径不存在」都报成同一句话。
func dockerExecError(t repoTarget, err error, stderr string) error {
	return &dockerTargetError{message: dockerExecErrorMessage(t, err, stderr)}
}

func dockerExecErrorMessage(t repoTarget, err error, stderr string) string {
	msg := strings.TrimSpace(stderr)
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "no such container"):
		return "容器不存在或已被删除，请重新选择容器"
	case strings.Contains(lower, "is not running"):
		return "容器未运行，请启动后重试"
	case strings.Contains(lower, "is paused"):
		return "容器已暂停，请恢复后重试"
	case strings.Contains(lower, "working directory"):
		return fmt.Sprintf("容器内路径不存在：%s", t.path)
	case strings.Contains(lower, "missing stat"):
		return "容器缺少 stat 命令，无法读取工作区状态"
	case strings.Contains(lower, "executable file not found"):
		// 分清到底缺的是哪个基础工具：命令前缀里有 env，读取路径还会用到 sh/cat/stat。
		switch {
		case strings.Contains(lower, "env:"):
			return "容器缺少 env 命令，无法安全执行 Git"
		case strings.Contains(lower, "sh:"):
			return "容器缺少 sh 命令"
		case strings.Contains(lower, "cat:"):
			return "容器缺少 cat 命令"
		case strings.Contains(lower, "stat:"):
			return "容器缺少 stat 命令，无法读取工作区状态"
		}
		return "容器内缺少 Git，请先在容器里安装 Git"
	case strings.Contains(lower, "permission denied"):
		return "容器内权限不足，无法访问这个仓库"
	case strings.Contains(lower, "cannot connect to the docker daemon"),
		strings.Contains(lower, "failed to connect"),
		strings.Contains(lower, "connection refused"),
		strings.Contains(lower, "is the docker daemon running"):
		return "Docker 未运行或无法连接，请先启动 Docker"
	case strings.Contains(lower, "context") && strings.Contains(lower, "does not exist"):
		return "Docker 连接已被删除，请重新选择容器"
	}
	if msg == "" {
		msg = err.Error()
	}
	return trimMsg(firstLine(msg))
}

// dockerCommandGone reports whether this failure leaves the command's fate unknown:
// the client lost the connection, so a container-side Git may still be running.
func dockerCommandGone(err error, stderr string) bool {
	if err == nil {
		return false
	}
	if isDockerUnavailable(err) {
		return true
	}
	var unknown *dockerUnknownResultError
	if errors.As(err, &unknown) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() < 0 || exitErr.ExitCode() == 125) {
		return true
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		return true
	}
	var targetErr *dockerTargetError
	if errors.As(err, &targetErr) {
		// 容器/连接层面的失败都不证明容器里的 Git 已退出；未命中中文分类时继续检查原始特征。
		for _, sign := range []string{"容器未运行", "容器已暂停", "容器不存在", "Docker 未运行", "无法连接", "无法运行"} {
			if strings.Contains(err.Error(), sign) {
				return true
			}
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	lower := strings.ToLower(stderr + " " + err.Error())
	for _, sign := range []string{
		"cannot connect to the docker daemon", "failed to connect", "connection refused",
		"is the docker daemon running", "no such container", "is not running", "is paused",
		"broken pipe", "unexpected eof", "context canceled", "i/o timeout",
	} {
		if strings.Contains(lower, sign) {
			return true
		}
	}
	return false
}

// dockerAbsPath 与 Git 的查找共用 filepath.Abs，避免不同平台的分隔符差异。
func dockerAbsPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("空路径")
	}
	if !strings.ContainsAny(p, `/\`) {
		if found, err := exec.LookPath(p); err == nil {
			return found, nil
		}
	}
	return filepath.Abs(p)
}

// dockerBuildTarget 用检查过的容器信息补全目标。容器上记下的是完整 ID，
// 命令里也只使用完整 ID。
func dockerBuildTarget(dc dockerContext, info dockerContainerInfo, containerPath string) (repoTarget, error) {
	clean, err := normalizeContainerRepoPath(containerPath)
	if err != nil {
		return repoTarget{}, err
	}
	return repoTarget{
		kind:      targetDocker,
		context:   dc.Name,
		endpoint:  dc.Endpoint,
		container: info.ID,
		path:      clean,
		name:      info.Name,
	}, nil
}

// dockerRepoRoot 打开容器内的仓库：先用容器 Git 找出真实根目录，再拒绝
// 第一版不支持的形态（bare、linked worktree、作为独立仓库打开的 submodule）。
func dockerRepoRoot(ctx context.Context, t repoTarget) (repoTarget, error) {
	if t.path == "" || !strings.HasPrefix(t.path, "/") {
		return repoTarget{}, errors.New("请填写容器内的绝对路径")
	}
	if err := verifyDockerEndpoint(ctx, t); err != nil {
		return repoTarget{}, err
	}
	dc := dockerContext{Name: t.context, Endpoint: t.endpoint}
	info, err := dockerInspectContainer(ctx, dc, t.container)
	if err != nil {
		return repoTarget{}, err
	}
	if info.ID != t.container {
		return repoTarget{}, errors.New("容器已变化，请重新选择容器")
	}
	if info.Platform != "" && info.Platform != "linux" {
		return repoTarget{}, fmt.Errorf("只支持 Linux 容器（%s）", info.Platform)
	}
	if info.Paused || strings.EqualFold(info.Status, "paused") {
		return repoTarget{}, errors.New("容器已暂停，请恢复后重试")
	}
	if !info.Running && !strings.EqualFold(info.Status, "running") {
		return repoTarget{}, errors.New("容器未运行，请启动后重试")
	}
	if _, err := dockerRun(ctx, t, t.path, nil, 1<<20, "", "git", "--version"); err != nil {
		if st := err.Error(); strings.Contains(st, "executable file not found") {
			return repoTarget{}, errors.New("容器内缺少 Git，请先在容器里安装 Git")
		}
		return repoTarget{}, err
	}
	probe := t
	probe.name = info.Name
	// bare 仓库没有工作区，先明确拒绝，否则 show-toplevel 只会报一句 Git 内部错误。
	// 打开目录允许寻找所属仓库：GIT_CEILING_DIRECTORIES 显式置空，不套用父目录限制。
	if bare, berr := dockerRun(ctx, probe, probe.path, []string{"GIT_CEILING_DIRECTORIES="}, 1<<20, "", "git", "rev-parse", "--is-bare-repository"); berr == nil && strings.TrimSpace(string(bare)) == "true" {
		return repoTarget{}, errors.New("bare 仓库暂不支持")
	}
	out, err := dockerRun(ctx, probe, probe.path, []string{"GIT_CEILING_DIRECTORIES="}, 1<<20, "", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		if notARepository(err) || strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
			return repoTarget{}, notGitRepoErr(dockerTargetLocation(t))
		}
		return repoTarget{}, err
	}
	top := strings.TrimSuffix(string(out), "\n")
	if top == "" || !isCleanContainerAbsPath(top) {
		return repoTarget{}, errors.New("Git 没有返回有效的仓库路径")
	}
	// bare 仓库没有工作区；linked worktree 和 submodule 第一版明确拒绝，而不是降级成
	// 主仓库或父仓库去操作别的内容。
	check := func(args ...string) (string, error) {
		b, err := dockerRun(ctx, repoTarget{kind: targetDocker, context: probe.context, endpoint: probe.endpoint, container: probe.container, path: top, name: probe.name}, top, []string{"GIT_CEILING_DIRECTORIES="}, 1<<20, "", append([]string{"git"}, args...)...)
		return strings.TrimSuffix(string(b), "\n"), err
	}
	if bare, err := check("rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		return repoTarget{}, errors.New("bare 仓库暂不支持")
	}
	if super, err := check("rev-parse", "--show-superproject-working-tree"); err == nil && super != "" {
		return repoTarget{}, errors.New("作为独立仓库打开 submodule 暂不支持")
	}
	gitDir, err := check("rev-parse", "--absolute-git-dir")
	if err != nil || gitDir == "" {
		return repoTarget{}, errors.New("无法确认仓库的 Git 目录")
	}
	common, err := check("rev-parse", "--git-common-dir")
	if err != nil || common == "" {
		return repoTarget{}, errors.New("无法确认仓库的 Git 目录")
	}
	if !strings.HasPrefix(common, "/") {
		common = path.Join(top, common)
	}
	// 普通仓库的 git-dir 与 common-dir 是同一份存储；linked worktree 的
	// git-dir 在 common-dir/worktrees/<name> 下。按路径比较比假定 .git 更稳。
	if path.Clean(gitDir) != path.Clean(common) {
		return repoTarget{}, errors.New("linked worktree 暂不支持")
	}
	root := repoTarget{
		kind:      targetDocker,
		context:   probe.context,
		endpoint:  probe.endpoint,
		container: probe.container,
		path:      top,
		name:      info.Name,
	}
	return root, nil
}

// dockerTargetLocation 是完整位置：容器名 + 容器内路径。
func dockerTargetLocation(t repoTarget) string {
	return dockerNameOf(t.container) + " · " + t.path
}

// repoLocation 给界面用的完整位置，绝不显示编码后的键。
func repoLocation(key string) string {
	if t, ok := parseDockerRepoKey(key); ok {
		return dockerTargetLocation(t)
	}
	return key
}

// repoLocationTooltip 在完整位置之外补上连接与完整容器 ID：
// 截断后的标题悬停时，用户需要能确认这是哪个 Docker 连接上的哪个容器。
func repoLocationTooltip(key string) string {
	if t, ok := parseDockerRepoKey(key); ok {
		return dockerTargetLocation(t) + "\n" + t.context + " · " + t.container
	}
	return key
}

// repoCopyPath 是「复制路径」在两种仓库上的含义：本机路径，或容器内路径。
func repoCopyPath(key string) string {
	if t, ok := parseDockerRepoKey(key); ok {
		return t.path
	}
	return key
}
