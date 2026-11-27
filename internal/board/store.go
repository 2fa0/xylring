package board

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2fa0/xylring/internal/i18n"
)

type BoardStatus string

const (
	BoardTodo   BoardStatus = "todo"
	BoardDoing  BoardStatus = "doing"
	BoardReview BoardStatus = "review"
	BoardDone   BoardStatus = "done"
)

var BoardStatuses = []BoardStatus{
	BoardTodo,
	BoardDoing,
	BoardReview,
	BoardDone,
}

func ParseBoardStatus(raw string) (BoardStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "todo", "待办":
		return BoardTodo, true
	case "doing", "in_progress", "in-progress", "进行中":
		return BoardDoing, true
	case "review", "待审阅":
		return BoardReview, true
	case "done", "completed", "已完成":
		return BoardDone, true
	default:
		return "", false
	}
}

func BoardStatusLabel(s BoardStatus) string {
	switch s {
	case BoardTodo:
		return "待办"
	case BoardDoing:
		return "进行中"
	case BoardReview:
		return "待审阅"
	case BoardDone:
		return "已完成"
	default:
		return "待办"
	}
}

type BoardPriority string

const (
	BoardPriorityNone            BoardPriority = ""
	BoardPriorityUrgentImportant BoardPriority = "urgent_important"
	BoardPriorityImportant       BoardPriority = "important"
	BoardPriorityUrgent          BoardPriority = "urgent"
	BoardPriorityLow             BoardPriority = "low"
)

var BoardPriorities = []BoardPriority{
	BoardPriorityUrgentImportant,
	BoardPriorityImportant,
	BoardPriorityUrgent,
	BoardPriorityLow,
}

func ParseBoardPriority(raw string) (BoardPriority, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "clear", "unset", "无", "未设置":
		return BoardPriorityNone, true
	case "urgent_important", "urgent-important", "important_urgent", "important-urgent", "q1", "p1", "重要且紧急", "重要紧急":
		return BoardPriorityUrgentImportant, true
	case "important", "important_not_urgent", "important-not-urgent", "q2", "p2", "重要不紧急":
		return BoardPriorityImportant, true
	case "urgent", "urgent_not_important", "urgent-not-important", "q3", "p3", "紧急不重要":
		return BoardPriorityUrgent, true
	case "low", "not_important_not_urgent", "not-important-not-urgent", "neither", "q4", "p4", "不重要不紧急":
		return BoardPriorityLow, true
	default:
		return "", false
	}
}

func BoardPriorityLabel(p BoardPriority) string {
	switch p {
	case BoardPriorityUrgentImportant:
		return "重要且紧急"
	case BoardPriorityImportant:
		return "重要不紧急"
	case BoardPriorityUrgent:
		return "紧急不重要"
	case BoardPriorityLow:
		return "不重要不紧急"
	default:
		return "未设置"
	}
}

type BoardDueState int

const (
	boardDueNone BoardDueState = iota
	boardDueUpcoming
	BoardDueToday
	BoardDueOverdue
)

func ParseBoardDueDate(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	switch strings.ToLower(s) {
	case "", "none", "clear", "unset", "无", "未设置":
		return "", true
	}
	s = strings.ReplaceAll(s, "/", "-")
	for _, layout := range []string{"2006-01-02", "2006-1-2"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	return "", false
}

func BoardTaskDueInfo(raw string, doneOrArchived bool, now time.Time) (parsed time.Time, state BoardDueState, ok bool) {
	norm, valid := ParseBoardDueDate(raw)
	if !valid || norm == "" {
		return time.Time{}, boardDueNone, false
	}
	t, err := time.ParseInLocation("2006-01-02", norm, time.Local)
	if err != nil {
		return time.Time{}, boardDueNone, false
	}
	localNow := now.In(time.Local)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)
	dueDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	if doneOrArchived {
		return dueDay, boardDueUpcoming, true
	}
	switch {
	case dueDay.Before(today):
		return dueDay, BoardDueOverdue, true
	case dueDay.Equal(today):
		return dueDay, BoardDueToday, true
	default:
		return dueDay, boardDueUpcoming, true
	}
}

type BoardCommitLink struct {
	Repo    string    `json:"repo"`
	Hash    string    `json:"hash"`
	Subject string    `json:"subject,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Model   string    `json:"model,omitempty"`
	AddedBy string    `json:"added_by,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

type BoardNote struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (n BoardNote) DisplayTime() time.Time {
	if !n.UpdatedAt.IsZero() && n.UpdatedAt.After(n.CreatedAt) {
		return n.UpdatedAt
	}
	return n.CreatedAt
}

type BoardTask struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      BoardStatus       `json:"status"`
	Priority    BoardPriority     `json:"priority,omitempty"`
	DueDate     string            `json:"due_date,omitempty"`
	Archived    bool              `json:"archived,omitempty"`
	Pinned      bool              `json:"pinned,omitempty"`
	Repos       []string          `json:"repos,omitempty"`
	Commits     []BoardCommitLink `json:"commits,omitempty"`
	Notes       []BoardNote       `json:"notes,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type BoardFile struct {
	Version int         `json:"version"`
	NextSeq int         `json:"next_seq"`
	Tasks   []BoardTask `json:"tasks"`
}

var BoardHomeDir = os.UserHomeDir

const (
	BoardEnvDirKey  = "XYLRING_BOARD_DIR"
	maxBoardBackups = 15
)

type boardLocationConfig struct {
	Dir string `json:"dir,omitempty"`
}

type BoardStorageInfo struct {
	ActiveDir  string `json:"active_dir"`
	DefaultDir string `json:"default_dir"`
	BoardPath  string `json:"board_path"`
	BackupsDir string `json:"backups_dir"`
	IsCustom   bool   `json:"is_custom"`
	FromEnv    bool   `json:"from_env"`
}

func boardDefaultDir() (string, error) {
	home, err := BoardHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "xylring"), nil
}

func boardLocationFilePath() (string, error) {
	defDir, err := boardDefaultDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(defDir, "location.json"), nil
}

func readBoardLocationConfig() (boardLocationConfig, error) {
	p, err := boardLocationFilePath()
	if err != nil {
		return boardLocationConfig{}, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return boardLocationConfig{}, nil
		}
		return boardLocationConfig{}, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return boardLocationConfig{}, nil
	}
	var cfg boardLocationConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return boardLocationConfig{}, err
	}
	cfg.Dir = strings.TrimSpace(cfg.Dir)
	if cfg.Dir != "" {
		cfg.Dir = filepath.Clean(cfg.Dir)
	}
	return cfg, nil
}

func WriteBoardLocationConfig(dir string) error {
	p, err := boardLocationFilePath()
	if err != nil {
		return err
	}
	defDir, err := boardDefaultDir()
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(dir)
	if trimmed != "" {
		trimmed = filepath.Clean(trimmed)
	}
	if trimmed == "" || trimmed == filepath.Clean(defDir) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(boardLocationConfig{Dir: trimmed}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeBytesAtomic(p, data, ".location-*.tmp", 0o600)
}

func resolveBoardDir() (dir string, isDefault bool, isCustom bool, fromEnv bool, err error) {
	if envDir := strings.TrimSpace(os.Getenv(BoardEnvDirKey)); envDir != "" {
		return filepath.Clean(envDir), false, false, true, nil
	}
	defDir, err := boardDefaultDir()
	if err != nil {
		return "", false, false, false, err
	}
	defClean := filepath.Clean(defDir)
	cfg, cfgErr := readBoardLocationConfig()
	if cfgErr == nil && cfg.Dir != "" && cfg.Dir != defClean {
		return cfg.Dir, false, true, false, nil
	}
	return defClean, true, false, false, nil
}

func BoardLocationInfo() (BoardStorageInfo, error) {
	defDir, err := boardDefaultDir()
	if err != nil {
		return BoardStorageInfo{}, err
	}
	activeDir, _, isCustom, fromEnv, err := resolveBoardDir()
	if err != nil {
		return BoardStorageInfo{}, err
	}
	boardPath := filepath.Join(activeDir, "board.json")
	return BoardStorageInfo{
		ActiveDir:  activeDir,
		DefaultDir: filepath.Clean(defDir),
		BoardPath:  boardPath,
		BackupsDir: filepath.Join(activeDir, "backups"),
		IsCustom:   isCustom,
		FromEnv:    fromEnv,
	}, nil
}

func migrateLegacyBoardIfNeeded(targetPath string) error {
	if _, err := os.Stat(targetPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	legacyPath, err := configFile("board.json")
	if err != nil || filepath.Clean(legacyPath) == filepath.Clean(targetPath) {
		return nil
	}
	b, err := os.ReadFile(legacyPath)
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return nil
	}
	bf, err := ReadBoardFileAt(legacyPath)
	if err != nil {
		return nil
	}
	if err := WriteBoardFileAtomic(targetPath, bf); err != nil {
		return err
	}
	migrated := legacyPath + ".migrated.bak"
	_ = os.Remove(migrated)
	_ = os.Rename(legacyPath, migrated)
	return nil
}

func BoardConfigPath() (string, error) {
	dir, isDefault, _, _, err := resolveBoardDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "board.json")
	if isDefault {
		_ = migrateLegacyBoardIfNeeded(target)
	}
	return target, nil
}

func NormalizeBoardRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	return CanonicalRepoDir(repo)
}

func normalizeBoardRepos(repos []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range repos {
		norm := NormalizeBoardRepo(r)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

func appendBoardRepo(repos []string, repo string) []string {
	norm := NormalizeBoardRepo(repo)
	if norm == "" {
		return repos
	}
	if slices.Contains(repos, norm) {
		return repos
	}
	return append(repos, norm)
}

func newBoardNoteID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "N-" + hex.EncodeToString(b[:])
}

func FindBoardTaskIndex(tasks []BoardTask, rawID string) int {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return -1
	}
	if n, err := strconv.Atoi(id); err == nil && n > 0 {
		id = fmt.Sprintf("T-%d", n)
	}
	for i := range tasks {
		if strings.EqualFold(tasks[i].ID, id) {
			return i
		}
	}
	return -1
}

func ReadBoardFileAt(path string) (BoardFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BoardFile{Version: 1, NextSeq: 1}, nil
		}
		return BoardFile{}, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return BoardFile{Version: 1, NextSeq: 1}, nil
	}
	var bf BoardFile
	if err := json.Unmarshal(b, &bf); err != nil {
		return BoardFile{}, fmt.Errorf("解析看板数据失败: %w", err)
	}
	if bf.Version == 0 {
		bf.Version = 1
	}
	if bf.NextSeq <= 0 {
		bf.NextSeq = maxBoardSeq(bf.Tasks) + 1
	}
	for i := range bf.Tasks {
		if _, ok := ParseBoardStatus(string(bf.Tasks[i].Status)); !ok {
			bf.Tasks[i].Status = BoardTodo
		}
		if p, ok := ParseBoardPriority(string(bf.Tasks[i].Priority)); ok {
			bf.Tasks[i].Priority = p
		} else {
			bf.Tasks[i].Priority = BoardPriorityNone
		}
		if d, ok := ParseBoardDueDate(bf.Tasks[i].DueDate); ok {
			bf.Tasks[i].DueDate = d
		} else {
			bf.Tasks[i].DueDate = ""
		}
	}
	return bf, nil
}

func maxBoardSeq(tasks []BoardTask) int {
	maxSeq := 0
	for _, t := range tasks {
		upper := strings.ToUpper(strings.TrimSpace(t.ID))
		if strings.HasPrefix(upper, "T-") {
			if n, err := strconv.Atoi(strings.TrimPrefix(upper, "T-")); err == nil && n > maxSeq {
				maxSeq = n
			}
		}
	}
	return maxSeq
}

func ensureCustomBoardDirExists() error {
	dir, _, isCustom, _, err := resolveBoardDir()
	if err != nil {
		return err
	}
	if !isCustom {
		return nil
	}
	info, statErr := os.Stat(dir)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf("看板数据目录不存在: %s", dir)
		}
		return statErr
	}
	if !info.IsDir() {
		return fmt.Errorf("看板数据路径不是目录: %s", dir)
	}
	return nil
}

func LoadBoardFile() (BoardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, err
	}
	return ReadBoardFileAt(path)
}

func withBoardLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(3 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		// Windows NTFS 会在锁文件刚被 os.Remove 标记为待删除（DELETE_PENDING）时返回 Access is denied，
		// 因此在 Windows 上把 os.IsPermission 也视作瞬态锁冲突重试，直到超时再返回原始错误。
		if !os.IsExist(err) && !(runtime.GOOS == "windows" && os.IsPermission(err)) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			if !os.IsExist(err) {
				return err
			}
			return errors.New("等待看板文件锁超时")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func writeBytesAtomic(path string, data []byte, tmpPattern string, perm os.FileMode) error {
	return writeAtomic(path, tmpPattern, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// writeAtomic writes a file's replacement beside it and renames it into place,
// so a reader sees the old file or the new one, never a partial write.
func writeAtomic(path, tmpPattern string, perm os.FileMode, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if perm != 0 {
		_ = tmp.Chmod(perm)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func WriteBoardFileAtomic(path string, bf BoardFile) error {
	if bf.Version == 0 {
		bf.Version = 1
	}
	if bf.NextSeq <= 0 {
		bf.NextSeq = maxBoardSeq(bf.Tasks) + 1
	}
	return writeAtomic(path, ".board-*.tmp", 0o644, func(w io.Writer) error {
		buf := bufio.NewWriterSize(w, 32<<10)
		if err := RenderBoardFile(buf, bf); err != nil {
			return err
		}
		return buf.Flush()
	})
}

// Board files are written whole, so encoding them is most of a write's cost.
// Indenting the whole file with json.MarshalIndent allocates several times the
// file and needs a second buffer that big; writing them one task at a time
// keeps the indented form everyone reads and holds only a single task.
var boardTaskEncodeBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func RenderBoardFile(w io.Writer, bf BoardFile) error {
	if _, err := fmt.Fprintf(w, "{\n  \"version\": %d,\n  \"next_seq\": %d,\n  \"tasks\": ", int64(bf.Version), int64(bf.NextSeq)); err != nil {
		return err
	}
	if bf.Tasks == nil {
		_, err := io.WriteString(w, "null\n}\n")
		return err
	}
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	scratch := boardTaskEncodeBuffers.Get().(*bytes.Buffer)
	defer func() {
		scratch.Reset()
		boardTaskEncodeBuffers.Put(scratch)
	}()
	for i := range bf.Tasks {
		task, err := json.Marshal(bf.Tasks[i])
		if err != nil {
			return err
		}
		if i > 0 {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "\n    "); err != nil {
			return err
		}
		scratch.Reset()
		if err := json.Indent(scratch, task, "    ", "  "); err != nil {
			return err
		}
		if _, err := w.Write(scratch.Bytes()); err != nil {
			return err
		}
	}
	if len(bf.Tasks) > 0 {
		if _, err := io.WriteString(w, "\n  "); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "]\n}\n")
	return err
}

func MutateBoardFile(fn func(*BoardFile) error) (BoardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, err
	}
	return mutateBoardFileAt(path, fn)
}

func mutateBoardFileAt(path string, fn func(*BoardFile) error) (BoardFile, error) {
	var updated BoardFile
	err := withBoardLock(path, func() error {
		bf, err := ReadBoardFileAt(path)
		if err != nil {
			return err
		}
		if err := fn(&bf); err != nil {
			return err
		}
		if err := WriteBoardFileAtomic(path, bf); err != nil {
			return err
		}
		updated = bf
		return nil
	})
	return updated, err
}

type BoardTaskPatch struct {
	Title       *string
	Description *string
	Status      *BoardStatus
	Priority    *BoardPriority
	DueDate     *string
	Archived    *bool
	Pinned      *bool
	Repos       *[]string
	AddRepos    []string
}

type BoardAddTaskOptions struct {
	Commit *BoardCommitLink
	Title       string
	Description string
	Status      BoardStatus
	Priority    BoardPriority
	DueDate     string
	Repos       []string
}

func BoardAddTask(title, desc string, status BoardStatus, repos []string) (BoardFile, BoardTask, error) {
	return BoardAddTaskWithOptions(BoardAddTaskOptions{
		Title:       title,
		Description: desc,
		Status:      status,
		Repos:       repos,
	})
}

func BoardAddTaskWithOptions(opts BoardAddTaskOptions) (BoardFile, BoardTask, error) {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return BoardFile{}, BoardTask{}, errors.New("任务标题不能为空")
	}
	status := opts.Status
	if status == "" {
		status = BoardTodo
	}
	if _, ok := ParseBoardStatus(string(status)); !ok {
		return BoardFile{}, BoardTask{}, fmt.Errorf("未知任务状态: %s", status)
	}
	priority, ok := ParseBoardPriority(string(opts.Priority))
	if !ok {
		return BoardFile{}, BoardTask{}, fmt.Errorf("未知任务优先级: %s", opts.Priority)
	}
	dueDate, ok := ParseBoardDueDate(opts.DueDate)
	if !ok {
		return BoardFile{}, BoardTask{}, fmt.Errorf("无效截止日期: %s", opts.DueDate)
	}
	var created BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		seq := f.NextSeq
		if seq <= maxBoardSeq(f.Tasks) {
			seq = maxBoardSeq(f.Tasks) + 1
		}
		f.NextSeq = seq + 1
		now := time.Now().UTC()
		created = BoardTask{
			ID:          fmt.Sprintf("T-%d", seq),
			Title:       title,
			Description: strings.TrimSpace(opts.Description),
			Status:      status,
			Priority:    priority,
			DueDate:     dueDate,
			Repos:       normalizeBoardRepos(opts.Repos),
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if opts.Commit != nil {
			created.Commits = []BoardCommitLink{*opts.Commit}
		}
		f.Tasks = append([]BoardTask{created}, f.Tasks...)
		return nil
	})
	return bf, created, err
}

func BoardUpdateTask(id string, patch BoardTaskPatch) (BoardFile, BoardTask, error) {
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		if patch.Title != nil {
			trimmed := strings.TrimSpace(*patch.Title)
			if trimmed == "" {
				return errors.New("任务标题不能为空")
			}
			t.Title = trimmed
		}
		if patch.Description != nil {
			t.Description = *patch.Description
		}
		if patch.Status != nil {
			if _, ok := ParseBoardStatus(string(*patch.Status)); !ok {
				return fmt.Errorf("未知任务状态: %s", *patch.Status)
			}
			t.Status = *patch.Status
		}
		if patch.Priority != nil {
			p, ok := ParseBoardPriority(string(*patch.Priority))
			if !ok {
				return fmt.Errorf("未知任务优先级: %s", *patch.Priority)
			}
			t.Priority = p
		}
		if patch.DueDate != nil {
			d, ok := ParseBoardDueDate(*patch.DueDate)
			if !ok {
				return fmt.Errorf("无效截止日期: %s", *patch.DueDate)
			}
			t.DueDate = d
		}
		if patch.Archived != nil {
			t.Archived = *patch.Archived
		}
		if patch.Pinned != nil {
			t.Pinned = *patch.Pinned
		}
		if patch.Repos != nil {
			t.Repos = normalizeBoardRepos(*patch.Repos)
		}
		for _, r := range patch.AddRepos {
			t.Repos = appendBoardRepo(t.Repos, r)
		}
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

// BoardMoveTask updates a task's status (when non-empty) and/or repositions it
// before or after anchorID inside BoardFile.Tasks.
func BoardMoveTask(id string, status BoardStatus, anchorID string, insertAfter bool) (BoardFile, BoardTask, error) {
	if status != "" {
		if _, ok := ParseBoardStatus(string(status)); !ok {
			return BoardFile{}, BoardTask{}, fmt.Errorf("未知任务状态: %s", status)
		}
	}
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		fromIdx := FindBoardTaskIndex(f.Tasks, id)
		if fromIdx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[fromIdx]
		targetStatus := t.Status
		if status != "" {
			targetStatus = status
		}
		anchor := strings.TrimSpace(anchorID)
		if anchor == "" {
			if t.Status == targetStatus {
				out = t
				return nil
			}
			t.Status = targetStatus
			t.UpdatedAt = time.Now().UTC()
			f.Tasks[fromIdx] = t
			out = t
			return nil
		}
		anchorOrigIdx := FindBoardTaskIndex(f.Tasks, anchor)
		if anchorOrigIdx < 0 {
			return fmt.Errorf("未找到目标看板任务: %s", anchorID)
		}
		if anchorOrigIdx == fromIdx {
			if t.Status == targetStatus {
				out = t
				return nil
			}
			t.Status = targetStatus
			t.UpdatedAt = time.Now().UTC()
			f.Tasks[fromIdx] = t
			out = t
			return nil
		}
		tasks := slices.Delete(slices.Clone(f.Tasks), fromIdx, fromIdx+1)
		toIdx := FindBoardTaskIndex(tasks, anchor)
		if insertAfter {
			toIdx++
		}
		if toIdx == fromIdx && t.Status == targetStatus {
			out = t
			return nil
		}
		t.Status = targetStatus
		t.UpdatedAt = time.Now().UTC()
		tasks = slices.Insert(tasks, toIdx, t)
		f.Tasks = tasks
		out = t
		return nil
	})
	return bf, out, err
}

func BoardDeleteTask(id string) (BoardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, err
	}
	return mutateBoardFileAt(path, func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		_ = snapshotBoardFileLocked(path, *f, "delete")
		f.Tasks = slices.Delete(f.Tasks, idx, idx+1)
		return nil
	})
}

// BoardArchiveDoneTasks archives exactly the listed tasks that are still
// completed and not archived, checking again under the file lock: a task
// another client moved or archived meanwhile is left alone.
func BoardArchiveDoneTasks(ids []string) (BoardFile, int, error) {
	if len(ids) == 0 {
		bf, err := LoadBoardFile()
		return bf, 0, err
	}
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, 0, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, 0, err
	}
	targets := make(map[string]bool, len(ids))
	for _, id := range ids {
		targets[id] = true
	}
	count := 0
	bf, err := mutateBoardFileAt(path, func(f *BoardFile) error {
		eligible := func(t BoardTask) bool {
			return targets[t.ID] && t.Status == BoardDone && !t.Archived
		}
		hasAny := slices.ContainsFunc(f.Tasks, eligible)
		if hasAny {
			_ = snapshotBoardFileLocked(path, *f, "archive")
		}
		now := time.Now().UTC()
		for i := range f.Tasks {
			if !eligible(f.Tasks[i]) {
				continue
			}
			f.Tasks[i].Archived = true
			f.Tasks[i].UpdatedAt = now
			count++
		}
		return nil
	})
	return bf, count, err
}

func BoardLinkCommit(id string, link BoardCommitLink) (BoardFile, BoardTask, error) {
	link.Repo = NormalizeBoardRepo(link.Repo)
	link.Hash = strings.ToLower(strings.TrimSpace(link.Hash))
	if link.Repo == "" {
		return BoardFile{}, BoardTask{}, errors.New("关联提交必须指定仓库路径")
	}
	if link.Hash == "" {
		return BoardFile{}, BoardTask{}, errors.New("关联提交必须指定哈希")
	}
	if link.AddedAt.IsZero() {
		link.AddedAt = time.Now().UTC()
	}
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		t.Repos = appendBoardRepo(t.Repos, link.Repo)
		merged := false
		for i := range t.Commits {
			if NormalizeBoardRepo(t.Commits[i].Repo) == link.Repo && strings.EqualFold(t.Commits[i].Hash, link.Hash) {
				if t.Commits[i].Subject == "" {
					t.Commits[i].Subject = link.Subject
				}
				if t.Commits[i].Branch == "" {
					t.Commits[i].Branch = link.Branch
				}
				if t.Commits[i].Agent == "" {
					t.Commits[i].Agent = link.Agent
				}
				if t.Commits[i].Model == "" {
					t.Commits[i].Model = link.Model
				}
				merged = true
				break
			}
		}
		if !merged {
			t.Commits = append(t.Commits, link)
		}
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardUnlinkCommit(id, repo, hashPrefix string) (BoardFile, BoardTask, error) {
	normRepo := NormalizeBoardRepo(repo)
	hashPrefix = strings.ToLower(strings.TrimSpace(hashPrefix))
	if hashPrefix == "" {
		return BoardFile{}, BoardTask{}, errors.New("请指定要移除的提交哈希")
	}
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		kept := make([]BoardCommitLink, 0, len(t.Commits))
		removed := 0
		for _, c := range t.Commits {
			repoMatch := normRepo == "" || NormalizeBoardRepo(c.Repo) == normRepo
			hashMatch := strings.HasPrefix(strings.ToLower(c.Hash), hashPrefix)
			if repoMatch && hashMatch {
				removed++
				continue
			}
			kept = append(kept, c)
		}
		if removed == 0 {
			return fmt.Errorf("未找到匹配的关联提交: %s", hashPrefix)
		}
		t.Commits = kept
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardAddNote(id, text, author, model string) (BoardFile, BoardTask, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return BoardFile{}, BoardTask{}, errors.New("备注内容不能为空")
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = "human"
	}
	model = strings.TrimSpace(model)
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		t.Notes = append(t.Notes, BoardNote{
			ID:        newBoardNoteID(),
			Text:      text,
			Author:    author,
			Model:     model,
			CreatedAt: time.Now().UTC(),
		})
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardUpdateNote(id, noteID, text string) (BoardFile, BoardTask, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return BoardFile{}, BoardTask{}, errors.New("备注内容不能为空")
	}
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		noteIdx := slices.IndexFunc(t.Notes, func(n BoardNote) bool { return n.ID == noteID })
		if noteIdx < 0 {
			return fmt.Errorf("未找到备注: %s", noteID)
		}
		if t.Notes[noteIdx].Text == text {
			out = t
			return nil
		}
		now := time.Now().UTC()
		t.Notes[noteIdx].Text = text
		t.Notes[noteIdx].UpdatedAt = now
		t.UpdatedAt = now
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardDeleteNote(id, noteID string) (BoardFile, BoardTask, error) {
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		noteIdx := slices.IndexFunc(t.Notes, func(n BoardNote) bool { return n.ID == noteID })
		if noteIdx < 0 {
			return fmt.Errorf("未找到备注: %s", noteID)
		}
		t.Notes = slices.Delete(t.Notes, noteIdx, noteIdx+1)
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardMoveNote(id, noteID string, toIdx int) (BoardFile, BoardTask, error) {
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		fromIdx := slices.IndexFunc(t.Notes, func(n BoardNote) bool { return n.ID == noteID })
		if fromIdx < 0 {
			return fmt.Errorf("未找到备注: %s", noteID)
		}
		if toIdx < 0 || toIdx >= len(t.Notes) {
			return fmt.Errorf("无效的备注顺序: %d", toIdx)
		}
		if fromIdx == toIdx {
			out = t
			return nil
		}
		note := t.Notes[fromIdx]
		notes := slices.Delete(slices.Clone(t.Notes), fromIdx, fromIdx+1)
		notes = slices.Insert(notes, toIdx, note)
		t.Notes = notes
		t.UpdatedAt = time.Now().UTC()
		f.Tasks[idx] = t
		out = t
		return nil
	})
	return bf, out, err
}

func BoardTaskAllRepos(t BoardTask) []string {
	repos := normalizeBoardRepos(t.Repos)
	for _, c := range t.Commits {
		repos = appendBoardRepo(repos, c.Repo)
	}
	return repos
}

func BoardMatchesFilter(t BoardTask, repoFilter, search string, showArchived bool) bool {
	if t.Archived && !showArchived {
		return false
	}
	if repoFilter != "" {
		norm := NormalizeBoardRepo(repoFilter)
		if !slices.Contains(BoardTaskAllRepos(t), norm) {
			return false
		}
	}
	q := strings.ToLower(strings.TrimSpace(search))
	if q == "" {
		return true
	}
	for _, word := range strings.Fields(q) {
		if !boardTaskContainsWord(t, word) {
			return false
		}
	}
	return true
}

func boardTaskContainsWord(t BoardTask, word string) bool {
	if strings.Contains(strings.ToLower(t.ID), word) ||
		strings.Contains(strings.ToLower(t.Title), word) ||
		strings.Contains(strings.ToLower(t.Description), word) {
		return true
	}
	if t.Priority != BoardPriorityNone {
		if strings.Contains(strings.ToLower(string(t.Priority)), word) ||
			strings.Contains(strings.ToLower(BoardPriorityLabel(t.Priority)), word) ||
			strings.Contains(strings.ToLower(i18n.Tr(BoardPriorityLabel(t.Priority))), word) {
			return true
		}
	}
	if t.DueDate != "" && strings.Contains(strings.ToLower(t.DueDate), word) {
		return true
	}
	for _, r := range BoardTaskAllRepos(t) {
		if strings.Contains(strings.ToLower(RepoTitle(r)), word) || strings.Contains(strings.ToLower(r), word) {
			return true
		}
	}
	for _, c := range t.Commits {
		if strings.Contains(strings.ToLower(c.Hash), word) ||
			strings.Contains(strings.ToLower(c.Subject), word) ||
			strings.Contains(strings.ToLower(c.Agent), word) ||
			strings.Contains(strings.ToLower(c.Model), word) {
			return true
		}
	}
	for _, n := range t.Notes {
		if strings.Contains(strings.ToLower(n.Text), word) ||
			strings.Contains(strings.ToLower(n.Author), word) {
			return true
		}
	}
	return false
}

type BoardBackupInfo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
	TaskCount int       `json:"task_count"`
}

func boardBackupsDir(boardPath string) string {
	return filepath.Join(filepath.Dir(boardPath), "backups")
}

func snapshotBoardFileLocked(boardPath string, bf BoardFile, reason string) error {
	if len(bf.Tasks) == 0 {
		return nil
	}
	dir := boardBackupsDir(boardPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	reason = sanitizeBackupReason(reason)
	stamp := time.Now().Format("20060102-150405")
	name := fmt.Sprintf("board-%s-%s.json", stamp, reason)
	target := filepath.Join(dir, name)
	for seq := 1; seq < 100; seq++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		target = filepath.Join(dir, fmt.Sprintf("board-%s-%s-%d.json", stamp, reason, seq))
	}
	if err := WriteBoardFileAtomic(target, bf); err != nil {
		return err
	}
	pruneBoardBackups(dir, maxBoardBackups)
	return nil
}

func sanitizeBackupReason(reason string) string {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return "auto"
	}
	var b strings.Builder
	for _, r := range reason {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "auto"
	}
	return b.String()
}

func pruneBoardBackups(dir string, keep int) {
	if keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		mod  time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "board-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{name: e.Name(), mod: info.ModTime()})
	}
	if len(items) <= keep {
		return
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].mod.Equal(items[j].mod) {
			return items[i].mod.After(items[j].mod)
		}
		return items[i].name > items[j].name
	})
	for _, old := range items[keep:] {
		_ = os.Remove(filepath.Join(dir, old.name))
	}
}

func parseBackupFilenameMeta(name string) (time.Time, string) {
	trimmed := strings.TrimSuffix(strings.TrimPrefix(name, "board-"), ".json")
	parts := strings.Split(trimmed, "-")
	var ts time.Time
	if len(parts) >= 2 {
		if parsed, err := time.ParseInLocation("20060102-150405", parts[0]+"-"+parts[1], time.Local); err == nil {
			ts = parsed
		}
	}
	reason := ""
	if len(parts) >= 3 {
		reason = parts[2]
	}
	return ts, reason
}

func ListBoardBackups() ([]BoardBackupInfo, error) {
	info, err := BoardLocationInfo()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(info.BackupsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BoardBackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "board-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(info.BackupsDir, e.Name())
		bundle, parseErr := parseBoardImportFile(fullPath)
		if parseErr != nil {
			continue
		}
		ts, reason := parseBackupFilenameMeta(e.Name())
		if ts.IsZero() {
			ts = fi.ModTime()
		}
		out = append(out, BoardBackupInfo{
			Path:      fullPath,
			Name:      e.Name(),
			Reason:    reason,
			CreatedAt: ts,
			Size:      fi.Size(),
			TaskCount: len(bundle.Tasks),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

func BoardRestoreBackup(backupPath string) (BoardFile, error) {
	bundle, err := parseBoardImportFile(backupPath)
	if err != nil {
		return BoardFile{}, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, err
	}
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, err
	}
	var restored BoardFile
	err = withBoardLock(path, func() error {
		if cur, curErr := ReadBoardFileAt(path); curErr == nil && len(cur.Tasks) > 0 {
			if snapErr := snapshotBoardFileLocked(path, cur, "restore"); snapErr != nil {
				return snapErr
			}
		}
		restored = BoardFile{
			Version: 1,
			NextSeq: max(bundle.NextSeq, maxBoardSeq(bundle.Tasks)+1),
			Tasks:   cloneBoardTasks(bundle.Tasks),
		}
		return WriteBoardFileAtomic(path, restored)
	})
	return restored, err
}

type boardRepoHint struct {
	Name      string `json:"name,omitempty"`
	RemoteURL string `json:"remote_url,omitempty"`
}

type boardBackupBundle struct {
	Version         int                      `json:"version"`
	ExportedAt      time.Time                `json:"exported_at,omitempty"`
	AppVersion      string                   `json:"app_version,omitempty"`
	NextSeq         int                      `json:"next_seq"`
	Tasks           []BoardTask              `json:"tasks"`
	RepoHints       map[string]boardRepoHint `json:"repo_hints,omitempty"`
	IncludeArchived *bool                    `json:"include_archived,omitempty"`
}

type BoardExportOptions struct {
	RepoFilter      string
	IncludeArchived bool
}

func readBoardRepoRemoteURL(ctx context.Context, repo string) string {
	info, err := os.Stat(repo)
	if err != nil || !info.IsDir() {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	subCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd, _, err := PrepareGitCommand(subCtx, repo, nil, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func BoardExport(ctx context.Context, opts BoardExportOptions) (boardBackupBundle, error) {
	bf, err := LoadBoardFile()
	if err != nil {
		return boardBackupBundle{}, err
	}
	var tasks []BoardTask
	hints := map[string]boardRepoHint{}
	for _, t := range bf.Tasks {
		if !BoardMatchesFilter(t, opts.RepoFilter, "", opts.IncludeArchived) {
			continue
		}
		cloned := cloneBoardTask(t)
		tasks = append(tasks, cloned)
		for _, r := range BoardTaskAllRepos(cloned) {
			if _, ok := hints[r]; ok {
				continue
			}
			hints[r] = boardRepoHint{
				Name:      RepoTitle(r),
				RemoteURL: readBoardRepoRemoteURL(ctx, r),
			}
		}
	}
	nextSeq := maxBoardSeq(tasks) + 1
	if opts.RepoFilter == "" && opts.IncludeArchived && bf.NextSeq > nextSeq {
		nextSeq = bf.NextSeq
	}
	incArch := opts.IncludeArchived
	bundle := boardBackupBundle{
		Version:         1,
		ExportedAt:      time.Now().UTC(),
		AppVersion:      AppVersion(),
		NextSeq:         nextSeq,
		Tasks:           tasks,
		IncludeArchived: &incArch,
	}
	if len(hints) > 0 {
		bundle.RepoHints = hints
	}
	return bundle, nil
}

func WriteBoardExportFile(path string, bundle boardBackupBundle) error {
	if bundle.Version == 0 {
		bundle.Version = 1
	}
	if bundle.Tasks == nil {
		bundle.Tasks = []BoardTask{}
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeBytesAtomic(path, data, ".board-export-*.tmp", 0o644)
}

type boardImportMode string

const (
	BoardImportMerge   boardImportMode = "merge"
	BoardImportReplace boardImportMode = "replace"
)

type BoardRepoMapping struct {
	OldPath       string
	SuggestedPath string
	ChosenPath    string
	Hint          boardRepoHint
	Status        string // "matched", "suggested", "missing"
}

type BoardImportPlan struct {
	SourcePath   string
	Bundle       boardBackupBundle
	TotalTasks   int
	ConflictIDs  map[string]string
	RepoMappings []BoardRepoMapping
}

type BoardImportOptions struct {
	Mode    boardImportMode
	RepoMap map[string]string
}

type BoardImportResult struct {
	ImportedTasks int               `json:"imported_tasks"`
	TotalTasks    int               `json:"total_tasks"`
	RenamedIDs    map[string]string `json:"renamed_ids,omitempty"`
	Mode          boardImportMode   `json:"mode"`
	BackupPath    string            `json:"backup_path,omitempty"`
}

func parseBoardImportFile(path string) (boardBackupBundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return boardBackupBundle{}, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return boardBackupBundle{}, errors.New("备份文件为空")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return boardBackupBundle{}, fmt.Errorf("解析看板备份失败: %w", err)
	}
	if _, hasTasks := raw["tasks"]; !hasTasks {
		return boardBackupBundle{}, errors.New("无效的看板备份格式：缺少 tasks 字段")
	}
	var bundle boardBackupBundle
	if err := json.Unmarshal(b, &bundle); err != nil {
		return boardBackupBundle{}, fmt.Errorf("解析看板备份失败: %w", err)
	}
	if bundle.Version == 0 {
		bundle.Version = 1
	}
	for i := range bundle.Tasks {
		t := &bundle.Tasks[i]
		t.Title = strings.TrimSpace(t.Title)
		if t.Title == "" {
			return boardBackupBundle{}, fmt.Errorf("备份中的任务缺少标题: %s", t.ID)
		}
		if _, ok := ParseBoardStatus(string(t.Status)); !ok {
			t.Status = BoardTodo
		}
		if p, ok := ParseBoardPriority(string(t.Priority)); ok {
			t.Priority = p
		} else {
			t.Priority = BoardPriorityNone
		}
		if d, ok := ParseBoardDueDate(t.DueDate); ok {
			t.DueDate = d
		} else {
			t.DueDate = ""
		}
		t.Repos = normalizeBoardRepos(t.Repos)
		for j := range t.Commits {
			t.Commits[j].Repo = NormalizeBoardRepo(t.Commits[j].Repo)
			t.Commits[j].Hash = strings.ToLower(strings.TrimSpace(t.Commits[j].Hash))
		}
		for j := range t.Notes {
			if strings.TrimSpace(t.Notes[j].Author) == "" {
				t.Notes[j].Author = "human"
			}
		}
	}
	if bundle.NextSeq <= 0 {
		bundle.NextSeq = maxBoardSeq(bundle.Tasks) + 1
	}
	return bundle, nil
}

func cloneBoardTask(t BoardTask) BoardTask {
	out := t
	out.Repos = slices.Clone(t.Repos)
	out.Commits = slices.Clone(t.Commits)
	out.Notes = slices.Clone(t.Notes)
	return out
}

func cloneBoardTasks(tasks []BoardTask) []BoardTask {
	if tasks == nil {
		return nil
	}
	out := make([]BoardTask, len(tasks))
	for i, t := range tasks {
		out[i] = cloneBoardTask(t)
	}
	return out
}

func normalizeGitRemoteURL(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if strings.HasPrefix(s, "git@") {
		s = strings.TrimPrefix(s, "git@")
		s = strings.Replace(s, ":", "/", 1)
	} else if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	return s
}

func IsBoardRepoAvailable(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, gitErr := os.Stat(filepath.Join(dir, ".git"))
	return gitErr == nil
}

func BuildBoardImportPlan(ctx context.Context, sourcePath string, knownRepos []string) (BoardImportPlan, error) {
	bundle, err := parseBoardImportFile(sourcePath)
	if err != nil {
		return BoardImportPlan{}, err
	}
	cur, curErr := LoadBoardFile()
	if curErr != nil {
		cur = BoardFile{Version: 1, NextSeq: 1}
	}
	_, conflicts := computeBoardMergePreview(cur, bundle.Tasks)

	normKnown := normalizeBoardRepos(knownRepos)
	knownRemotes := map[string]string{}
	for _, kr := range normKnown {
		if u := normalizeGitRemoteURL(readBoardRepoRemoteURL(ctx, kr)); u != "" {
			knownRemotes[kr] = u
		}
	}

	var seenRepos []string
	for _, t := range bundle.Tasks {
		for _, r := range BoardTaskAllRepos(t) {
			if r != "" && !slices.Contains(seenRepos, r) {
				seenRepos = append(seenRepos, r)
			}
		}
	}

	mappings := make([]BoardRepoMapping, 0, len(seenRepos))
	for _, oldRepo := range seenRepos {
		hint := bundle.RepoHints[oldRepo]
		if hint.Name == "" {
			hint.Name = RepoTitle(oldRepo)
		}
		m := BoardRepoMapping{
			OldPath: oldRepo,
			Hint:    hint,
		}
		if IsBoardRepoAvailable(oldRepo) {
			norm := NormalizeBoardRepo(oldRepo)
			m.Status = "matched"
			m.SuggestedPath = norm
			m.ChosenPath = norm
		} else {
			suggested := matchKnownBoardRepo(oldRepo, hint, normKnown, knownRemotes)
			if suggested != "" {
				m.Status = "suggested"
				m.SuggestedPath = suggested
				m.ChosenPath = suggested
			} else {
				m.Status = "missing"
			}
		}
		mappings = append(mappings, m)
	}

	return BoardImportPlan{
		SourcePath:   sourcePath,
		Bundle:       bundle,
		TotalTasks:   len(bundle.Tasks),
		ConflictIDs:  conflicts,
		RepoMappings: mappings,
	}, nil
}

func matchKnownBoardRepo(oldPath string, hint boardRepoHint, knownRepos []string, knownRemotes map[string]string) string {
	wantRemote := normalizeGitRemoteURL(hint.RemoteURL)
	if wantRemote != "" {
		for _, kr := range knownRepos {
			if knownRemotes[kr] == wantRemote {
				return kr
			}
		}
	}
	wantName := strings.TrimSpace(hint.Name)
	if wantName == "" {
		wantName = RepoTitle(oldPath)
	}
	if wantName == "" {
		return ""
	}
	var nameMatches []string
	for _, kr := range knownRepos {
		if strings.EqualFold(RepoTitle(kr), wantName) {
			nameMatches = append(nameMatches, kr)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0]
	}
	return ""
}

func applyRepoMapToTask(t BoardTask, repoMap map[string]string) BoardTask {
	out := cloneBoardTask(t)
	if len(repoMap) == 0 {
		return out
	}
	mapRepo := func(orig string) string {
		norm := NormalizeBoardRepo(orig)
		if replacement, ok := repoMap[orig]; ok && strings.TrimSpace(replacement) != "" {
			return NormalizeBoardRepo(replacement)
		}
		if replacement, ok := repoMap[norm]; ok && strings.TrimSpace(replacement) != "" {
			return NormalizeBoardRepo(replacement)
		}
		return norm
	}
	remappedRepos := make([]string, 0, len(out.Repos))
	for _, r := range out.Repos {
		remappedRepos = appendBoardRepo(remappedRepos, mapRepo(r))
	}
	out.Repos = remappedRepos
	for i := range out.Commits {
		out.Commits[i].Repo = mapRepo(out.Commits[i].Repo)
	}
	return out
}

func computeBoardMergePreview(existing BoardFile, incoming []BoardTask) (BoardFile, map[string]string) {
	merged := BoardFile{
		Version: 1,
		Tasks:   cloneBoardTasks(existing.Tasks),
	}
	usedIDs := map[string]bool{}
	usedNoteIDs := map[string]bool{}
	for _, t := range merged.Tasks {
		usedIDs[strings.ToUpper(strings.TrimSpace(t.ID))] = true
		for _, n := range t.Notes {
			if n.ID != "" {
				usedNoteIDs[n.ID] = true
			}
		}
	}
	nextSeq := max(existing.NextSeq, maxBoardSeq(merged.Tasks)+1)
	if nextSeq <= 0 {
		nextSeq = 1
	}
	renamed := map[string]string{}
	for _, src := range incoming {
		t := cloneBoardTask(src)
		origID := strings.TrimSpace(t.ID)
		upperID := strings.ToUpper(origID)
		if upperID == "" || usedIDs[upperID] {
			newID := fmt.Sprintf("T-%d", nextSeq)
			nextSeq++
			if origID != "" {
				renamed[origID] = newID
			}
			t.ID = newID
		} else {
			t.ID = upperID
			if strings.HasPrefix(upperID, "T-") {
				if n, err := strconv.Atoi(strings.TrimPrefix(upperID, "T-")); err == nil && n >= nextSeq {
					nextSeq = n + 1
				}
			}
		}
		usedIDs[strings.ToUpper(t.ID)] = true
		for j := range t.Notes {
			if t.Notes[j].ID == "" || usedNoteIDs[t.Notes[j].ID] {
				t.Notes[j].ID = newBoardNoteID()
			}
			usedNoteIDs[t.Notes[j].ID] = true
		}
		merged.Tasks = append(merged.Tasks, t)
	}
	merged.NextSeq = nextSeq
	return merged, renamed
}

func BoardImport(sourcePath string, opts BoardImportOptions) (BoardFile, BoardImportResult, error) {
	if opts.Mode == "" {
		opts.Mode = BoardImportMerge
	}
	if opts.Mode != BoardImportMerge && opts.Mode != BoardImportReplace {
		return BoardFile{}, BoardImportResult{}, fmt.Errorf("未知导入模式: %s", opts.Mode)
	}
	bundle, err := parseBoardImportFile(sourcePath)
	if err != nil {
		return BoardFile{}, BoardImportResult{}, err
	}
	if err := ensureCustomBoardDirExists(); err != nil {
		return BoardFile{}, BoardImportResult{}, err
	}
	path, err := BoardConfigPath()
	if err != nil {
		return BoardFile{}, BoardImportResult{}, err
	}
	incoming := make([]BoardTask, len(bundle.Tasks))
	for i, t := range bundle.Tasks {
		incoming[i] = applyRepoMapToTask(t, opts.RepoMap)
	}

	var result BoardImportResult
	result.Mode = opts.Mode
	result.ImportedTasks = len(incoming)

	var updated BoardFile
	err = withBoardLock(path, func() error {
		cur, readErr := ReadBoardFileAt(path)
		if readErr != nil && opts.Mode == BoardImportMerge {
			return readErr
		}
		if readErr == nil && len(cur.Tasks) > 0 {
			if snapErr := snapshotBoardFileLocked(path, cur, "import"); snapErr != nil {
				return snapErr
			}
		}
		switch opts.Mode {
		case BoardImportReplace:
			updated = BoardFile{
				Version: 1,
				NextSeq: max(bundle.NextSeq, maxBoardSeq(incoming)+1),
				Tasks:   incoming,
			}
		case BoardImportMerge:
			merged, renamed := computeBoardMergePreview(cur, incoming)
			updated = merged
			result.RenamedIDs = renamed
		}
		result.TotalTasks = len(updated.Tasks)
		return WriteBoardFileAtomic(path, updated)
	})
	return updated, result, err
}

type BoardDirSwitchStrategy string

const (
	BoardSwitchAuto            BoardDirSwitchStrategy = ""
	BoardSwitchUseTarget       BoardDirSwitchStrategy = "use_target"
	BoardSwitchMergeTarget     BoardDirSwitchStrategy = "merge_target"
	BoardSwitchOverwriteTarget BoardDirSwitchStrategy = "overwrite_target"
)

func BoardProbeDir(dir string) (hasBoard bool, taskCount int, err error) {
	clean := filepath.Clean(strings.TrimSpace(dir))
	if clean == "" {
		return false, 0, errors.New("目录路径不能为空")
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return false, 0, err
	}
	boardPath := filepath.Join(clean, "board.json")
	b, err := os.ReadFile(boardPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, 0, nil
		}
		return false, 0, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return false, 0, nil
	}
	bf, err := ReadBoardFileAt(boardPath)
	if err != nil {
		return true, 0, err
	}
	return true, len(bf.Tasks), nil
}

func BoardSwitchStorageDir(newDir string, strategy BoardDirSwitchStrategy) (BoardFile, error) {
	if strings.TrimSpace(os.Getenv(BoardEnvDirKey)) != "" {
		return BoardFile{}, errors.New("当前通过环境变量 XYLRING_BOARD_DIR 指定了看板目录，无法在应用内修改")
	}
	clean := filepath.Clean(strings.TrimSpace(newDir))
	if clean == "" {
		return BoardFile{}, errors.New("目录路径不能为空")
	}
	absDir, err := filepath.Abs(clean)
	if err == nil {
		clean = absDir
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return BoardFile{}, err
	}
	info, err := BoardLocationInfo()
	if err != nil {
		return BoardFile{}, err
	}
	if filepath.Clean(info.ActiveDir) == clean {
		return LoadBoardFile()
	}

	cur, curErr := LoadBoardFile()
	if curErr != nil {
		cur = BoardFile{Version: 1, NextSeq: 1}
	}

	targetPath := filepath.Join(clean, "board.json")
	hasTarget, _, probeErr := BoardProbeDir(clean)
	if probeErr != nil && strategy != BoardSwitchOverwriteTarget {
		return BoardFile{}, probeErr
	}

	var result BoardFile
	if !hasTarget {
		err = withBoardLock(targetPath, func() error {
			if err := WriteBoardFileAtomic(targetPath, cur); err != nil {
				return err
			}
			result = cur
			return nil
		})
		if err != nil {
			return BoardFile{}, err
		}
		copyBoardBackups(info.BackupsDir, boardBackupsDir(targetPath))
	} else {
		switch strategy {
		case BoardSwitchUseTarget:
			bf, err := ReadBoardFileAt(targetPath)
			if err != nil {
				return BoardFile{}, err
			}
			result = bf
		case BoardSwitchMergeTarget:
			err = withBoardLock(targetPath, func() error {
				targetBF, err := ReadBoardFileAt(targetPath)
				if err != nil {
					return err
				}
				if len(targetBF.Tasks) > 0 {
					if snapErr := snapshotBoardFileLocked(targetPath, targetBF, "switch-merge"); snapErr != nil {
						return snapErr
					}
				}
				merged, _ := computeBoardMergePreview(targetBF, cur.Tasks)
				if err := WriteBoardFileAtomic(targetPath, merged); err != nil {
					return err
				}
				result = merged
				return nil
			})
			if err != nil {
				return BoardFile{}, err
			}
		case BoardSwitchOverwriteTarget:
			err = withBoardLock(targetPath, func() error {
				if targetBF, err := ReadBoardFileAt(targetPath); err == nil && len(targetBF.Tasks) > 0 {
					if snapErr := snapshotBoardFileLocked(targetPath, targetBF, "switch-overwrite"); snapErr != nil {
						return snapErr
					}
				}
				if err := WriteBoardFileAtomic(targetPath, cur); err != nil {
					return err
				}
				result = cur
				return nil
			})
			if err != nil {
				return BoardFile{}, err
			}
		default:
			return BoardFile{}, fmt.Errorf("目标目录已存在看板数据: %s", targetPath)
		}
	}

	if err := WriteBoardLocationConfig(clean); err != nil {
		return BoardFile{}, err
	}
	return result, nil
}

func copyBoardBackups(srcDir, dstDir string) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "board-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		dst := filepath.Join(dstDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			continue
		}
		_ = writeBytesAtomic(dst, data, ".backup-copy-*.tmp", 0o644)
	}
	pruneBoardBackups(dstDir, maxBoardBackups)
}

type BoardChecklistItem struct {
	LineIndex int
	Checked   bool
	Text      string
	// MarkIndex is the byte offset in the line of the checkbox character
	// itself, so toggling rewrites that byte and nothing else.
	MarkIndex int
}

// boardChecklistMarker reads a Markdown task item from a line without its
// indentation: "- [ ]", "* [x]", "+ [ ]" or "1. [ ]". It reports where the
// character between the brackets sits so a toggle touches only that byte.
func boardChecklistMarker(rest string) (checked bool, markIndex int, ok bool) {
	start := 0
	switch {
	case strings.HasPrefix(rest, "- "), strings.HasPrefix(rest, "* "), strings.HasPrefix(rest, "+ "):
		start = 2
	default:
		digits := 0
		for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
			digits++
		}
		if digits == 0 || digits > 9 || digits >= len(rest) || (rest[digits] != '.' && rest[digits] != ')') {
			return false, 0, false
		}
		if digits+1 >= len(rest) || rest[digits+1] != ' ' {
			return false, 0, false
		}
		start = digits + 2
	}
	if start+2 >= len(rest) || rest[start] != '[' || rest[start+2] != ']' {
		return false, 0, false
	}
	if end := start + 3; end < len(rest) && rest[end] != ' ' && rest[end] != '\t' {
		return false, 0, false
	}
	switch rest[start+1] {
	case ' ':
		return false, start + 1, true
	case 'x', 'X':
		return true, start + 1, true
	default:
		return false, 0, false
	}
}

// boardFenceDelimiter reports the code fence a line opens or closes, so
// checklist examples inside fenced code are not taken for real items.
func boardFenceDelimiter(trimmed string) string {
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}

func ParseBoardChecklist(desc string) []BoardChecklistItem {
	if desc == "" {
		return nil
	}
	lines := strings.Split(desc, "\n")
	var items []BoardChecklistItem
	fence := ""
	for i, line := range lines {
		body := strings.TrimLeft(line, " \t")
		body = strings.TrimSuffix(body, "\r")
		trimmed := strings.TrimSpace(body)
		if delim := boardFenceDelimiter(trimmed); delim != "" {
			switch {
			case fence == "":
				fence = delim
			case delim[0] == fence[0] && len(delim) >= len(fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		checked, mark, ok := boardChecklistMarker(body)
		if !ok {
			continue
		}
		lead := len(line) - len(strings.TrimLeft(line, " \t"))
		items = append(items, BoardChecklistItem{
			LineIndex: i,
			Checked:   checked,
			Text:      strings.TrimSpace(body[mark+2:]),
			MarkIndex: lead + mark,
		})
	}
	return items
}

// FindBoardChecklistItem locates the item the user acted on: the line it sat
// on with the same text first, then the only item carrying that text. A
// description changed by another client fails instead of toggling a different
// line, and so does one whose checkbox already moved.
func FindBoardChecklistItem(items []BoardChecklistItem, lineIndex int, text string) (BoardChecklistItem, bool) {
	for _, item := range items {
		if item.LineIndex == lineIndex && item.Text == text {
			return item, true
		}
	}
	var match BoardChecklistItem
	found := 0
	for _, item := range items {
		if item.Text == text {
			match = item
			found++
		}
	}
	if found == 1 {
		return match, true
	}
	return BoardChecklistItem{}, false
}

func toggleBoardChecklistMarker(desc string, item BoardChecklistItem) (string, bool) {
	lines := strings.Split(desc, "\n")
	if item.LineIndex < 0 || item.LineIndex >= len(lines) || item.MarkIndex < 0 || item.MarkIndex >= len(lines[item.LineIndex]) {
		return desc, false
	}
	mark := byte('x')
	if item.Checked {
		mark = ' '
	}
	line := lines[item.LineIndex]
	b := []byte(line)
	b[item.MarkIndex] = mark
	lines[item.LineIndex] = string(b)
	return strings.Join(lines, "\n"), true
}

// ToggleBoardChecklistItem flips the expected item in desc, leaving every
// other byte alone. It fails when the item is gone, ambiguous, or no longer
// in the state the caller saw.
func ToggleBoardChecklistItem(desc string, lineIndex int, text string, checked bool) (string, bool) {
	item, ok := FindBoardChecklistItem(ParseBoardChecklist(desc), lineIndex, text)
	if !ok || item.Checked != checked {
		return desc, false
	}
	return toggleBoardChecklistMarker(desc, item)
}

// BoardToggleChecklistTask flips one checklist item in the description stored
// on disk. The line and text identify the item, and checked is the state the
// user saw: a description another client changed, or an item that moved,
// fails instead of overwriting it.
func BoardToggleChecklistTask(id string, lineIndex int, text string, checked bool) (BoardFile, BoardTask, error) {
	var out BoardTask
	bf, err := MutateBoardFile(func(f *BoardFile) error {
		idx := FindBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		next, ok := ToggleBoardChecklistItem(t.Description, lineIndex, text, checked)
		if !ok {
			return errors.New("清单项已被其他程序改动，请重新打开任务再试")
		}
		if next != t.Description {
			t.Description = next
			t.UpdatedAt = time.Now().UTC()
			f.Tasks[idx] = t
		}
		out = t
		return nil
	})
	return bf, out, err
}

func FormatBoardAgentContext(task BoardTask) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] %s\n", task.ID, task.Title)
	if task.Status != "" {
		fmt.Fprintf(&sb, "Status: %s\n", task.Status)
	}
	repos := BoardTaskAllRepos(task)
	if len(repos) > 0 {
		sb.WriteString("Repos:\n")
		for _, r := range repos {
			fmt.Fprintf(&sb, "- %s\n", r)
		}
	}

	// The whole description goes out: background, constraints and acceptance
	// criteria may sit around a checklist, and handoff text must not depend on
	// whether the description happens to hold one.
	if desc := strings.TrimSpace(task.Description); desc != "" {
		sb.WriteString("\nDescription:\n")
		sb.WriteString(desc)
		sb.WriteByte('\n')
	}

	items := ParseBoardChecklist(task.Description)
	if len(items) > 0 {
		var unchecked []BoardChecklistItem
		for _, item := range items {
			if !item.Checked {
				unchecked = append(unchecked, item)
			}
		}
		if len(unchecked) > 0 {
			sb.WriteString("\nRemaining Checklist:\n")
			for _, item := range unchecked {
				fmt.Fprintf(&sb, "- [ ] %s\n", item.Text)
			}
		} else {
			sb.WriteString("\nChecklist: all items completed.\n")
		}
	}

	if commits := BoardNewestCommits(task.Commits, 2); len(commits) > 0 {
		sb.WriteString("\nRecent Commits:\n")
		for _, cm := range commits {
			short := cm.Hash
			if len(short) > 7 {
				short = short[:7]
			}
			line := fmt.Sprintf("- %s", short)
			if cm.Subject != "" {
				line += " " + cm.Subject
			}
			if cm.Repo != "" {
				line += fmt.Sprintf(" (%s)", RepoTitle(cm.Repo))
			}
			if cm.Agent != "" {
				line += fmt.Sprintf(" [%s]", cm.Agent)
			}
			sb.WriteString(line + "\n")
		}
	}

	if latest, ok := LatestBoardNote(task.Notes); ok {
		author := latest.Author
		if author == "" {
			author = "human"
		}
		sb.WriteString("\nLatest Note:\n")
		if ts := latest.DisplayTime(); !ts.IsZero() {
			fmt.Fprintf(&sb, "%s (%s): %s\n", author, ts.Local().Format("2006-01-02 15:04"), strings.TrimSpace(latest.Text))
		} else {
			fmt.Fprintf(&sb, "%s: %s\n", author, strings.TrimSpace(latest.Text))
		}
	}

	// Only standard task IDs can be pasted back into a shell: an imported or
	// hand-edited ID would make the samples fail or run something else.
	if isStandardBoardTaskID(task.ID) {
		sb.WriteString("\nXylring Board CLI:\n")
		fmt.Fprintf(&sb, "- xylring board update %s --status review\n", task.ID)
		fmt.Fprintf(&sb, "- xylring board note %s \"<progress>\" --agent \"<agent>\" --model \"<model>\"\n", task.ID)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// isStandardBoardTaskID reports whether id is one the CLI generates: T-
// followed by a positive decimal number without padding.
func isStandardBoardTaskID(id string) bool {
	digits, ok := strings.CutPrefix(id, "T-")
	if !ok || digits == "" || len(digits) > 12 || digits[0] == '0' {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(digits)
	return err == nil && n > 0 && strconv.Itoa(n) == digits
}

// LatestBoardNote picks the note with the most recent effective time, and
// the later one in the list on a tie. Notes saved without times keep their
// stored order, as older clients wrote them.
func LatestBoardNote(notes []BoardNote) (BoardNote, bool) {
	if len(notes) == 0 {
		return BoardNote{}, false
	}
	best := notes[len(notes)-1]
	bestTime := best.DisplayTime()
	for i := len(notes) - 2; i >= 0; i-- {
		t := notes[i].DisplayTime()
		if t.After(bestTime) {
			best, bestTime = notes[i], t
		}
	}
	return best, true
}

// BoardNewestCommits returns up to n commit links, most recently linked
// first. Links saved without a time keep their stored order.
func BoardNewestCommits(links []BoardCommitLink, n int) []BoardCommitLink {
	if n <= 0 || len(links) == 0 {
		return nil
	}
	order := make([]int, len(links))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := links[order[i]].AddedAt, links[order[j]].AddedAt
		if a.Equal(b) {
			return order[i] > order[j]
		}
		return a.After(b)
	})
	out := make([]BoardCommitLink, 0, min(n, len(order)))
	for _, idx := range order[:min(n, len(order))] {
		out = append(out, links[idx])
	}
	return out
}
