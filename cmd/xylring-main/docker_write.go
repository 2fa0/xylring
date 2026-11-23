package main

// 容器写操作的结果必须可信。Docker CLI 被杀、连接中断或 daemon 故障都不证明
// 容器里的 Git 已经退出，所以这里把「已发出、未确认结束」的记录持久化，
// 未知状态跨标签、跨重启都存在，直到用户核对后明确解除。
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type dockerWriteRecord struct {
	Repo    string `json:"repo"`
	Action  string `json:"action"`
	OpID    string `json:"op_id"`
	Started int64  `json:"started"`
	Reason  string `json:"reason,omitempty"`
}

type dockerWriteState struct {
	records  map[string]*dockerWriteRecord // 仓库键 -> 未确认结束的记录
	unknown  map[string]string             // 仓库键 -> 未知原因
	gen      int
	loadErr  error
	recovery dockerRecoveryDialog
}

type dockerRecoveryDialog struct {
	open     bool
	busy     bool
	repo     string
	evidence []dockerEvidenceLine
	err      string
	gen      int
}

func dockerWriteRecordsPath() (string, error) { return configFile("git-writes.json") }

func loadDockerWriteRecords() ([]dockerWriteRecord, error) {
	p, err := dockerWriteRecordsPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []dockerWriteRecord
	if err := json.Unmarshal(b, &records); err != nil {
		return nil, err
	}
	for _, rec := range records {
		if !isDockerRepoPath(rec.Repo) || rec.OpID == "" || rec.Action == "" {
			return nil, errors.New(tr("写操作记录损坏，已暂停容器写入"))
		}
	}
	return records, nil
}

// initDockerWrites 在启动时把上次没结束的记录标成「结果未知」。
// 不能把未知操作清成可以安全重试：那等于替用户猜了一个结果。
func (a *app) initDockerWrites() {
	a.dockerWrites.records = map[string]*dockerWriteRecord{}
	a.dockerWrites.unknown = map[string]string{}
	records, err := loadDockerWriteRecords()
	a.dockerWrites.loadErr = err
	if err != nil {
		return
	}
	for _, rec := range records {
		if rec.Repo == "" || !isDockerRepoPath(rec.Repo) {
			continue
		}
		rec := rec
		if rec.Reason == "" {
			rec.Reason = "应用上次退出时这条操作还没有结束，结果未确认"
		}
		a.dockerWrites.records[rec.Repo] = &rec
		a.dockerWrites.unknown[rec.Repo] = rec.Reason
	}
}

func (a *app) saveDockerWrites() error {
	p, err := dockerWriteRecordsPath()
	if err != nil {
		return err
	}
	records := make([]dockerWriteRecord, 0, len(a.dockerWrites.records))
	for _, rec := range a.dockerWrites.records {
		records = append(records, *rec)
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".git-writes-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(p))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// beginDockerWrite 在发出写命令之前先落盘。落盘失败就不执行：
// 宁可让用户再点一次，也不能让重启后的应用不知道有条命令可能还在跑。
func (a *app) beginDockerWrite(repo, action string) (*dockerWriteRecord, error) {
	if a.dockerWrites.records == nil || a.dockerWrites.loadErr != nil {
		a.initDockerWrites()
	}
	if a.dockerWrites.loadErr != nil {
		return nil, fmt.Errorf("%s: %w", tr("无法读取写操作记录，已暂停容器写入"), a.dockerWrites.loadErr)
	}
	if reason := a.dockerWrites.unknown[repo]; reason != "" {
		return nil, fmt.Errorf("上次操作结果未知（%s），请先核对容器状态", reason)
	}
	if _, exists := a.dockerWrites.records[repo]; exists {
		return nil, fmt.Errorf("这个仓库已有未确认结束的写操作，请先核对")
	}
	if a.dockerWrites.records == nil {
		a.dockerWrites.records = map[string]*dockerWriteRecord{}
	}
	if a.dockerWrites.unknown == nil {
		a.dockerWrites.unknown = map[string]string{}
	}
	rec := &dockerWriteRecord{
		Repo: repo, Action: action,
		OpID:    fmt.Sprintf("%d-%d", time.Now().UnixNano(), a.dockerWrites.gen),
		Started: time.Now().Unix(),
	}
	a.dockerWrites.gen++
	a.dockerWrites.records[repo] = rec
	if err := a.saveDockerWrites(); err != nil {
		delete(a.dockerWrites.records, repo)
		return nil, fmt.Errorf("无法保存进行中的操作记录，已取消执行：%w", err)
	}
	return rec, nil
}

// finishDockerWrite 只在结果可信时清掉记录。连接中断留下的未知状态保持原样。
func (a *app) finishDockerWrite(rec *dockerWriteRecord, err error) {
	if rec == nil {
		return
	}
	if err != nil && dockerCommandGone(err, "") {
		rec.Reason = err.Error()
		a.dockerWrites.unknown[rec.Repo] = rec.Reason
		_ = a.saveDockerWrites()
		return
	}
	delete(a.dockerWrites.records, rec.Repo)
	delete(a.dockerWrites.unknown, rec.Repo)
	_ = a.saveDockerWrites()
}

// clearDockerWriteRecord 用于「命令还没发出去」的情况：此时没有容器内操作在跑，
// 结果完全可信，不能把它当成未知状态留在恢复入口里。
func (a *app) clearDockerWriteRecord(rec *dockerWriteRecord) {
	if rec == nil {
		return
	}
	delete(a.dockerWrites.records, rec.Repo)
	delete(a.dockerWrites.unknown, rec.Repo)
	_ = a.saveDockerWrites()
}

func (a *app) dockerWriteBlockReason(repo string) string {
	if repo == "" || !isDockerRepoPath(repo) {
		return ""
	}
	if a.dockerWrites.loadErr != nil {
		return tr("无法读取写操作记录，已暂停容器写入") + ": " + a.dockerWrites.loadErr.Error()
	}
	return a.dockerWrites.unknown[repo]
}

// beginRepoWrite 统一容器写操作的前置记录。本机仓库不需要额外记录。
func (a *app) beginRepoWrite(t *repoTab, action string) (*dockerWriteRecord, bool) {
	if t == nil || !isDockerRepoPath(t.path) {
		return nil, true
	}
	rec, err := a.beginDockerWrite(t.path, action)
	if err != nil {
		t.commitBusy = false
		a.setBanner(err.Error(), t.path)
		return nil, false
	}
	return rec, true
}

// writeContext 给容器写操作一个只由退出决定的生命周期：不再有 30 秒自动杀客户端，
// 否则一次稍慢的 Hook 会被误报成失败，而容器里的 Git 可能还在跑。
func writeContext(repo string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if isDockerRepoPath(repo) {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}

// dockerWriteEvidence 核对容器状态、Git 锁和工作区，作为用户解除保护的依据。
// 它不谎称能证明 Git 进程已退出：那需要容器里的进程视图，这里如实说明。
type dockerEvidenceLine struct {
	text string
	tone string
}

func dockerWriteEvidence(ctx context.Context, repo string) []dockerEvidenceLine {
	var lines []dockerEvidenceLine
	add := func(text, tone string) { lines = append(lines, dockerEvidenceLine{text: text, tone: tone}) }
	t, ok := parseDockerRepoKey(repo)
	if !ok {
		return []dockerEvidenceLine{{text: tr("不是容器仓库"), tone: "warning"}}
	}
	dc := dockerContext{Name: t.context, Endpoint: t.endpoint}
	info, err := dockerInspectContainer(ctx, dc, t.container)
	if err != nil {
		add(tr("无法确认容器状态：")+err.Error(), "warning")
	} else {
		tone := "warning"
		if info.Running && !info.Paused {
			tone = "success"
		}
		add(fmt.Sprintf(tr("容器：%s（%s）"), info.Name, info.Status), tone)
	}
	lockPath, lockErr := gitFileContainerPath(ctx, repo, "index.lock")
	if lockErr != nil {
		add(tr("无法读取 Git 锁：")+lockErr.Error(), "warning")
	} else {
		exists, err := repoPathExists(ctx, repo, lockPath)
		switch {
		case err != nil:
			add(tr("无法读取 Git 锁：")+err.Error(), "warning")
		case exists:
			add(tr("index.lock：仍存在"), "warning")
		default:
			add(tr("未发现 Git 锁文件"), "success")
		}
	}
	if head, err := runGit(ctx, repo, "rev-parse", "--short", "HEAD"); err == nil {
		add("HEAD："+strings.TrimSpace(string(head)), "")
	} else {
		add(fmt.Sprintf(tr("HEAD：无法读取（%s）"), err.Error()), "warning")
	}
	if out, err := runGit(ctx, repo, "--no-optional-locks", "status", "--porcelain=v1", "--untracked-files=no"); err == nil {
		n := 0
		for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				n++
			}
		}
		add(fmt.Sprintf(tr("工作区：%d 个已跟踪改动"), n), "")
	}
	add(tr("进入容器终端，运行 ps -ef 检查是否还有 Git 或 Hook 命令；确认上次操作已结束后再解除保护。"), "")
	return lines
}

func (a *app) openDockerRecovery(t *repoTab) {
	if t == nil || !isDockerRepoPath(t.path) {
		return
	}
	reason := a.dockerWriteBlockReason(t.path)
	if reason == "" {
		return
	}
	a.dockerWrites.recovery = dockerRecoveryDialog{open: true, repo: t.path, gen: a.dockerWrites.recovery.gen + 1}
	a.refreshDockerRecovery()
}

func (a *app) refreshDockerRecovery() {
	d := &a.dockerWrites.recovery
	if !d.open || d.busy {
		return
	}
	d.busy = true
	d.err = ""
	gen := d.gen
	repo := d.repo
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		evidence := dockerWriteEvidence(ctx, repo)
		a.post(func() {
			if a.dockerWrites.recovery.gen != gen {
				return
			}
			a.dockerWrites.recovery.busy = false
			a.dockerWrites.recovery.evidence = evidence
		})
	}()
}

func (a *app) clearDockerWrite(repo string) error {
	// 用户已经确认容器内的操作结束：不需要保留「已确认」的历史，直接落盘。
	if a.dockerWrites.loadErr != nil {
		return a.dockerWrites.loadErr
	}
	rec, reason := a.dockerWrites.records[repo], a.dockerWrites.unknown[repo]
	delete(a.dockerWrites.records, repo)
	delete(a.dockerWrites.unknown, repo)
	if err := a.saveDockerWrites(); err != nil {
		if rec != nil {
			a.dockerWrites.records[repo] = rec
		}
		if reason != "" {
			a.dockerWrites.unknown[repo] = reason
		}
		return err
	}
	return nil
}

func (a *app) dockerRecoveryModal(c *ui.Context) {
	d := &a.dockerWrites.recovery
	if !d.open {
		return
	}
	theme := c.Theme()
	appModal(c.Key("docker-write-recovery-modal"), &d.open, tr("核对未确认结束的操作"), 560, d.busy, func() {
		ui.Text(c, tr("上次操作的结果尚未确认，请检查容器后恢复写入。")).FontSize(12.5).LineHeight(1.45).TextColor(theme.Text)
		if d.err != "" {
			settingsNotice(c, "docker-recovery-err", d.err, theme.Danger)
		}
		if d.busy {
			busyRow(c, "docker-recovery-busy", tr("正在读取容器状态…"), "")
			return
		}
		if len(d.evidence) > 0 {
			ui.Column(c.Key("docker-recovery-evidence")).FillWidth().Padding(10, 12).Gap(4).
				Radius(theme.Radius).Background(theme.Surface).Border(1, theme.Border).Children(func() {
				for _, line := range d.evidence {
					color := theme.TextMuted
					switch line.tone {
					case "success":
						color = theme.Success
					case "warning":
						color = theme.Warning
					}
					ui.Text(c, line.text).FontSize(12).TextColor(color)
				}
			})
		}

		ui.Row(c.Key("docker-recovery-tools")).Gap(8).Children(func() {
			if ui.Button(c.Key("docker-recovery-terminal"), tr("进入容器终端")).Clicked() {
				a.enterDockerTerminal(d.repo)
			}
			if ui.Button(c.Key("docker-recovery-copy"), tr("复制检查命令")).Clicked() {
				if target, ok := parseDockerRepoKey(d.repo); ok {
					words := []string{"docker", "--host", target.endpoint, "exec", "--workdir", target.path, target.container, "sh", "-c", "ps -ef; git --no-optional-locks status; git rev-parse --git-path index.lock"}
					for i := range words {
						words[i] = shellQuoteDocker(words[i])
					}
					c.Services().WriteClipboard(strings.Join(words, " "))
				}
			}
		})
	}, func() {
		if ui.Button(c.Key("docker-recovery-recheck"), tr("重新核对")).Disabled(d.busy).Clicked() {
			a.refreshDockerRecovery()
		}
		if ui.Button(c.Key("docker-recovery-cancel"), tr("关闭")).Disabled(d.busy).Clicked() {
			d.open = false
		}
		if dangerButton(c, "docker-recovery-confirm", tr("已确认结束，解除保护")).Disabled(d.busy).Clicked() {
			repo := d.repo
			if err := a.clearDockerWrite(repo); err != nil {
				d.err = err.Error()
				return
			}
			d.open = false
			if t := a.tabByPath(repo); t != nil {
				t.err = ""
				t.nextRepoCheck, t.repoCheckDelay = time.Time{}, 0
				a.checkActiveRepo()
				a.reload(t)
			}
			a.pendingToast = "已解除写入保护"
		}
	})
}

// dockerWriteGuardBanner 是恢复入口本身：关掉弹窗、重开标签都不能绕过它。
func (a *app) dockerWriteGuardBanner(c *ui.Context, tab *repoTab) {
	if tab == nil || !isDockerRepoPath(tab.path) {
		return
	}
	reason := a.dockerWriteBlockReason(tab.path)
	if reason == "" {
		return
	}
	theme := c.Theme()
	ui.Row(c.Key("docker-write-guard")).FillWidth().Margin(0, 12, 6, 12).Padding(8, 12).Gap(10).
		AlignItems(ui.Center).Background(theme.Warning.Alpha(0.12)).Border(1, theme.Warning.Alpha(0.3)).
		Radius(theme.Radius).Shrink(0).Children(func() {
		ui.Icon(c, iconAlertCircle).Size(16, 16).TextColor(theme.Warning).Shrink(0)
		ui.Text(c, tr("上次写操作结果未确认，已暂停写入")).FontSize(12.5).TextColor(theme.Text).Grow(1).MinWidth(0).Tooltip(reason)
		if ui.Button(c.Key("docker-write-guard-check"), tr("核对…")).Shrink(0).Clicked() {
			a.openDockerRecovery(tab)
		}
	})
}

// dockerActionBlocked 是所有未支持动作在入口处的统一拒绝。只隐藏按钮不够：
// 菜单、快捷键和底层调用都必须落到同一个判断。
func (a *app) dockerActionBlocked(t *repoTab) bool {
	if t == nil || !isDockerRepoPath(t.path) {
		return false
	}
	a.setBanner(tr("容器仓库暂不支持这个操作"), t.path)
	return true
}

// dockerHistoryOnly 只放行只读的历史类操作。
func dockerOperationAllowedInContainer(action string) bool {
	switch action {
	case "Blame", "行历史":
		return true
	}
	return false
}
