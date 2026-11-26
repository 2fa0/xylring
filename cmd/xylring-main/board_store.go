package main

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
)

type boardStatus string

const (
	boardTodo   boardStatus = "todo"
	boardDoing  boardStatus = "doing"
	boardReview boardStatus = "review"
	boardDone   boardStatus = "done"
)

var boardStatuses = []boardStatus{
	boardTodo,
	boardDoing,
	boardReview,
	boardDone,
}

func parseBoardStatus(raw string) (boardStatus, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "todo", "待办":
		return boardTodo, true
	case "doing", "in_progress", "in-progress", "进行中":
		return boardDoing, true
	case "review", "待审阅":
		return boardReview, true
	case "done", "completed", "已完成":
		return boardDone, true
	default:
		return "", false
	}
}

func boardStatusLabel(s boardStatus) string {
	switch s {
	case boardTodo:
		return "待办"
	case boardDoing:
		return "进行中"
	case boardReview:
		return "待审阅"
	case boardDone:
		return "已完成"
	default:
		return "待办"
	}
}

type boardPriority string

const (
	boardPriorityNone            boardPriority = ""
	boardPriorityUrgentImportant boardPriority = "urgent_important"
	boardPriorityImportant       boardPriority = "important"
	boardPriorityUrgent          boardPriority = "urgent"
	boardPriorityLow             boardPriority = "low"
)

var boardPriorities = []boardPriority{
	boardPriorityUrgentImportant,
	boardPriorityImportant,
	boardPriorityUrgent,
	boardPriorityLow,
}

func parseBoardPriority(raw string) (boardPriority, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none", "clear", "unset", "无", "未设置":
		return boardPriorityNone, true
	case "urgent_important", "urgent-important", "important_urgent", "important-urgent", "q1", "p1", "重要且紧急", "重要紧急":
		return boardPriorityUrgentImportant, true
	case "important", "important_not_urgent", "important-not-urgent", "q2", "p2", "重要不紧急":
		return boardPriorityImportant, true
	case "urgent", "urgent_not_important", "urgent-not-important", "q3", "p3", "紧急不重要":
		return boardPriorityUrgent, true
	case "low", "not_important_not_urgent", "not-important-not-urgent", "neither", "q4", "p4", "不重要不紧急":
		return boardPriorityLow, true
	default:
		return "", false
	}
}

func boardPriorityLabel(p boardPriority) string {
	switch p {
	case boardPriorityUrgentImportant:
		return "重要且紧急"
	case boardPriorityImportant:
		return "重要不紧急"
	case boardPriorityUrgent:
		return "紧急不重要"
	case boardPriorityLow:
		return "不重要不紧急"
	default:
		return "未设置"
	}
}

type boardDueState int

const (
	boardDueNone boardDueState = iota
	boardDueUpcoming
	boardDueToday
	boardDueOverdue
)

func parseBoardDueDate(raw string) (string, bool) {
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

func boardTaskDueInfo(raw string, doneOrArchived bool, now time.Time) (parsed time.Time, state boardDueState, ok bool) {
	norm, valid := parseBoardDueDate(raw)
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
		return dueDay, boardDueOverdue, true
	case dueDay.Equal(today):
		return dueDay, boardDueToday, true
	default:
		return dueDay, boardDueUpcoming, true
	}
}

type boardCommitLink struct {
	Repo    string    `json:"repo"`
	Hash    string    `json:"hash"`
	Subject string    `json:"subject,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Model   string    `json:"model,omitempty"`
	AddedBy string    `json:"added_by,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

type boardNote struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

func (n boardNote) displayTime() time.Time {
	if !n.UpdatedAt.IsZero() && n.UpdatedAt.After(n.CreatedAt) {
		return n.UpdatedAt
	}
	return n.CreatedAt
}

type boardTask struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      boardStatus       `json:"status"`
	Priority    boardPriority     `json:"priority,omitempty"`
	DueDate     string            `json:"due_date,omitempty"`
	Archived    bool              `json:"archived,omitempty"`
	Repos       []string          `json:"repos,omitempty"`
	Commits     []boardCommitLink `json:"commits,omitempty"`
	Notes       []boardNote       `json:"notes,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type boardFile struct {
	Version int         `json:"version"`
	NextSeq int         `json:"next_seq"`
	Tasks   []boardTask `json:"tasks"`
}

var boardHomeDir = os.UserHomeDir

const (
	boardEnvDirKey  = "XYLRING_BOARD_DIR"
	maxBoardBackups = 15
)

type boardLocationConfig struct {
	Dir string `json:"dir,omitempty"`
}

type boardStorageInfo struct {
	ActiveDir  string `json:"active_dir"`
	DefaultDir string `json:"default_dir"`
	BoardPath  string `json:"board_path"`
	BackupsDir string `json:"backups_dir"`
	IsCustom   bool   `json:"is_custom"`
	FromEnv    bool   `json:"from_env"`
}

func boardDefaultDir() (string, error) {
	home, err := boardHomeDir()
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

func writeBoardLocationConfig(dir string) error {
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
	if envDir := strings.TrimSpace(os.Getenv(boardEnvDirKey)); envDir != "" {
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

func boardLocationInfo() (boardStorageInfo, error) {
	defDir, err := boardDefaultDir()
	if err != nil {
		return boardStorageInfo{}, err
	}
	activeDir, _, isCustom, fromEnv, err := resolveBoardDir()
	if err != nil {
		return boardStorageInfo{}, err
	}
	boardPath := filepath.Join(activeDir, "board.json")
	return boardStorageInfo{
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
	bf, err := readBoardFileAt(legacyPath)
	if err != nil {
		return nil
	}
	if err := writeBoardFileAtomic(targetPath, bf); err != nil {
		return err
	}
	migrated := legacyPath + ".migrated.bak"
	_ = os.Remove(migrated)
	_ = os.Rename(legacyPath, migrated)
	return nil
}

func boardConfigPath() (string, error) {
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

func normalizeBoardRepo(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return ""
	}
	return canonicalRepoDir(repo)
}

func normalizeBoardRepos(repos []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range repos {
		norm := normalizeBoardRepo(r)
		if norm == "" || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}

func appendBoardRepo(repos []string, repo string) []string {
	norm := normalizeBoardRepo(repo)
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

func findBoardTaskIndex(tasks []boardTask, rawID string) int {
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

func readBoardFileAt(path string) (boardFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return boardFile{Version: 1, NextSeq: 1}, nil
		}
		return boardFile{}, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return boardFile{Version: 1, NextSeq: 1}, nil
	}
	var bf boardFile
	if err := json.Unmarshal(b, &bf); err != nil {
		return boardFile{}, fmt.Errorf("解析看板数据失败: %w", err)
	}
	if bf.Version == 0 {
		bf.Version = 1
	}
	if bf.NextSeq <= 0 {
		bf.NextSeq = maxBoardSeq(bf.Tasks) + 1
	}
	for i := range bf.Tasks {
		if _, ok := parseBoardStatus(string(bf.Tasks[i].Status)); !ok {
			bf.Tasks[i].Status = boardTodo
		}
		if p, ok := parseBoardPriority(string(bf.Tasks[i].Priority)); ok {
			bf.Tasks[i].Priority = p
		} else {
			bf.Tasks[i].Priority = boardPriorityNone
		}
		if d, ok := parseBoardDueDate(bf.Tasks[i].DueDate); ok {
			bf.Tasks[i].DueDate = d
		} else {
			bf.Tasks[i].DueDate = ""
		}
	}
	return bf, nil
}

func maxBoardSeq(tasks []boardTask) int {
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

func loadBoardFile() (boardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, err
	}
	return readBoardFileAt(path)
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

func writeBoardFileAtomic(path string, bf boardFile) error {
	if bf.Version == 0 {
		bf.Version = 1
	}
	if bf.NextSeq <= 0 {
		bf.NextSeq = maxBoardSeq(bf.Tasks) + 1
	}
	return writeAtomic(path, ".board-*.tmp", 0o644, func(w io.Writer) error {
		buf := bufio.NewWriterSize(w, 32<<10)
		if err := renderBoardFile(buf, bf); err != nil {
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

func renderBoardFile(w io.Writer, bf boardFile) error {
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

func mutateBoardFile(fn func(*boardFile) error) (boardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, err
	}
	return mutateBoardFileAt(path, fn)
}

func mutateBoardFileAt(path string, fn func(*boardFile) error) (boardFile, error) {
	var updated boardFile
	err := withBoardLock(path, func() error {
		bf, err := readBoardFileAt(path)
		if err != nil {
			return err
		}
		if err := fn(&bf); err != nil {
			return err
		}
		if err := writeBoardFileAtomic(path, bf); err != nil {
			return err
		}
		updated = bf
		return nil
	})
	return updated, err
}

type boardTaskPatch struct {
	Title       *string
	Description *string
	Status      *boardStatus
	Priority    *boardPriority
	DueDate     *string
	Archived    *bool
	Repos       *[]string
	AddRepos    []string
}

type boardAddTaskOptions struct {
	commit      *boardCommitLink
	Title       string
	Description string
	Status      boardStatus
	Priority    boardPriority
	DueDate     string
	Repos       []string
}

func boardAddTask(title, desc string, status boardStatus, repos []string) (boardFile, boardTask, error) {
	return boardAddTaskWithOptions(boardAddTaskOptions{
		Title:       title,
		Description: desc,
		Status:      status,
		Repos:       repos,
	})
}

func boardAddTaskWithOptions(opts boardAddTaskOptions) (boardFile, boardTask, error) {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return boardFile{}, boardTask{}, errors.New("任务标题不能为空")
	}
	status := opts.Status
	if status == "" {
		status = boardTodo
	}
	if _, ok := parseBoardStatus(string(status)); !ok {
		return boardFile{}, boardTask{}, fmt.Errorf("未知任务状态: %s", status)
	}
	priority, ok := parseBoardPriority(string(opts.Priority))
	if !ok {
		return boardFile{}, boardTask{}, fmt.Errorf("未知任务优先级: %s", opts.Priority)
	}
	dueDate, ok := parseBoardDueDate(opts.DueDate)
	if !ok {
		return boardFile{}, boardTask{}, fmt.Errorf("无效截止日期: %s", opts.DueDate)
	}
	var created boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		seq := f.NextSeq
		if seq <= maxBoardSeq(f.Tasks) {
			seq = maxBoardSeq(f.Tasks) + 1
		}
		f.NextSeq = seq + 1
		now := time.Now().UTC()
		created = boardTask{
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
		if opts.commit != nil {
			created.Commits = []boardCommitLink{*opts.commit}
		}
		f.Tasks = append([]boardTask{created}, f.Tasks...)
		return nil
	})
	return bf, created, err
}

func boardUpdateTask(id string, patch boardTaskPatch) (boardFile, boardTask, error) {
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
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
			if _, ok := parseBoardStatus(string(*patch.Status)); !ok {
				return fmt.Errorf("未知任务状态: %s", *patch.Status)
			}
			t.Status = *patch.Status
		}
		if patch.Priority != nil {
			p, ok := parseBoardPriority(string(*patch.Priority))
			if !ok {
				return fmt.Errorf("未知任务优先级: %s", *patch.Priority)
			}
			t.Priority = p
		}
		if patch.DueDate != nil {
			d, ok := parseBoardDueDate(*patch.DueDate)
			if !ok {
				return fmt.Errorf("无效截止日期: %s", *patch.DueDate)
			}
			t.DueDate = d
		}
		if patch.Archived != nil {
			t.Archived = *patch.Archived
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

// boardMoveTask updates a task's status (when non-empty) and/or repositions it
// before or after anchorID inside boardFile.Tasks.
func boardMoveTask(id string, status boardStatus, anchorID string, insertAfter bool) (boardFile, boardTask, error) {
	if status != "" {
		if _, ok := parseBoardStatus(string(status)); !ok {
			return boardFile{}, boardTask{}, fmt.Errorf("未知任务状态: %s", status)
		}
	}
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		fromIdx := findBoardTaskIndex(f.Tasks, id)
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
		anchorOrigIdx := findBoardTaskIndex(f.Tasks, anchor)
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
		toIdx := findBoardTaskIndex(tasks, anchor)
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

func boardDeleteTask(id string) (boardFile, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, err
	}
	return mutateBoardFileAt(path, func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		_ = snapshotBoardFileLocked(path, *f, "delete")
		f.Tasks = slices.Delete(f.Tasks, idx, idx+1)
		return nil
	})
}

func boardArchiveDone() (boardFile, int, error) {
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, 0, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, 0, err
	}
	count := 0
	bf, err := mutateBoardFileAt(path, func(f *boardFile) error {
		hasDone := false
		for i := range f.Tasks {
			if f.Tasks[i].Status == boardDone && !f.Tasks[i].Archived {
				hasDone = true
				break
			}
		}
		if hasDone {
			_ = snapshotBoardFileLocked(path, *f, "archive")
		}
		now := time.Now().UTC()
		for i := range f.Tasks {
			if f.Tasks[i].Status == boardDone && !f.Tasks[i].Archived {
				f.Tasks[i].Archived = true
				f.Tasks[i].UpdatedAt = now
				count++
			}
		}
		return nil
	})
	return bf, count, err
}

func boardLinkCommit(id string, link boardCommitLink) (boardFile, boardTask, error) {
	link.Repo = normalizeBoardRepo(link.Repo)
	link.Hash = strings.ToLower(strings.TrimSpace(link.Hash))
	if link.Repo == "" {
		return boardFile{}, boardTask{}, errors.New("关联提交必须指定仓库路径")
	}
	if link.Hash == "" {
		return boardFile{}, boardTask{}, errors.New("关联提交必须指定哈希")
	}
	if link.AddedAt.IsZero() {
		link.AddedAt = time.Now().UTC()
	}
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		t.Repos = appendBoardRepo(t.Repos, link.Repo)
		merged := false
		for i := range t.Commits {
			if normalizeBoardRepo(t.Commits[i].Repo) == link.Repo && strings.EqualFold(t.Commits[i].Hash, link.Hash) {
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

func boardUnlinkCommit(id, repo, hashPrefix string) (boardFile, boardTask, error) {
	normRepo := normalizeBoardRepo(repo)
	hashPrefix = strings.ToLower(strings.TrimSpace(hashPrefix))
	if hashPrefix == "" {
		return boardFile{}, boardTask{}, errors.New("请指定要移除的提交哈希")
	}
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		kept := make([]boardCommitLink, 0, len(t.Commits))
		removed := 0
		for _, c := range t.Commits {
			repoMatch := normRepo == "" || normalizeBoardRepo(c.Repo) == normRepo
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

func boardAddNote(id, text, author, model string) (boardFile, boardTask, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return boardFile{}, boardTask{}, errors.New("备注内容不能为空")
	}
	author = strings.TrimSpace(author)
	if author == "" {
		author = "human"
	}
	model = strings.TrimSpace(model)
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		t.Notes = append(t.Notes, boardNote{
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

func boardUpdateNote(id, noteID, text string) (boardFile, boardTask, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return boardFile{}, boardTask{}, errors.New("备注内容不能为空")
	}
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		noteIdx := slices.IndexFunc(t.Notes, func(n boardNote) bool { return n.ID == noteID })
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

func boardDeleteNote(id, noteID string) (boardFile, boardTask, error) {
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		noteIdx := slices.IndexFunc(t.Notes, func(n boardNote) bool { return n.ID == noteID })
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

func boardMoveNote(id, noteID string, toIdx int) (boardFile, boardTask, error) {
	var out boardTask
	bf, err := mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		t := f.Tasks[idx]
		fromIdx := slices.IndexFunc(t.Notes, func(n boardNote) bool { return n.ID == noteID })
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

func boardTaskAllRepos(t boardTask) []string {
	repos := normalizeBoardRepos(t.Repos)
	for _, c := range t.Commits {
		repos = appendBoardRepo(repos, c.Repo)
	}
	return repos
}

func boardMatchesFilter(t boardTask, repoFilter, search string, showArchived bool) bool {
	if t.Archived && !showArchived {
		return false
	}
	if repoFilter != "" {
		norm := normalizeBoardRepo(repoFilter)
		if !slices.Contains(boardTaskAllRepos(t), norm) {
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

func boardTaskContainsWord(t boardTask, word string) bool {
	if strings.Contains(strings.ToLower(t.ID), word) ||
		strings.Contains(strings.ToLower(t.Title), word) ||
		strings.Contains(strings.ToLower(t.Description), word) {
		return true
	}
	if t.Priority != boardPriorityNone {
		if strings.Contains(strings.ToLower(string(t.Priority)), word) ||
			strings.Contains(strings.ToLower(boardPriorityLabel(t.Priority)), word) ||
			strings.Contains(strings.ToLower(tr(boardPriorityLabel(t.Priority))), word) {
			return true
		}
	}
	if t.DueDate != "" && strings.Contains(strings.ToLower(t.DueDate), word) {
		return true
	}
	for _, r := range boardTaskAllRepos(t) {
		if strings.Contains(strings.ToLower(repoTitle(r)), word) || strings.Contains(strings.ToLower(r), word) {
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

type boardBackupInfo struct {
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

func snapshotBoardFileLocked(boardPath string, bf boardFile, reason string) error {
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
	if err := writeBoardFileAtomic(target, bf); err != nil {
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

func listBoardBackups() ([]boardBackupInfo, error) {
	info, err := boardLocationInfo()
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
	var out []boardBackupInfo
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
		out = append(out, boardBackupInfo{
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

func boardRestoreBackup(backupPath string) (boardFile, error) {
	bundle, err := parseBoardImportFile(backupPath)
	if err != nil {
		return boardFile{}, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, err
	}
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, err
	}
	var restored boardFile
	err = withBoardLock(path, func() error {
		if cur, curErr := readBoardFileAt(path); curErr == nil && len(cur.Tasks) > 0 {
			if snapErr := snapshotBoardFileLocked(path, cur, "restore"); snapErr != nil {
				return snapErr
			}
		}
		restored = boardFile{
			Version: 1,
			NextSeq: max(bundle.NextSeq, maxBoardSeq(bundle.Tasks)+1),
			Tasks:   cloneBoardTasks(bundle.Tasks),
		}
		return writeBoardFileAtomic(path, restored)
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
	Tasks           []boardTask              `json:"tasks"`
	RepoHints       map[string]boardRepoHint `json:"repo_hints,omitempty"`
	IncludeArchived *bool                    `json:"include_archived,omitempty"`
}

type boardExportOptions struct {
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
	cmd, _, err := prepareGitCommand(subCtx, repo, nil, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func boardExport(ctx context.Context, opts boardExportOptions) (boardBackupBundle, error) {
	bf, err := loadBoardFile()
	if err != nil {
		return boardBackupBundle{}, err
	}
	var tasks []boardTask
	hints := map[string]boardRepoHint{}
	for _, t := range bf.Tasks {
		if !boardMatchesFilter(t, opts.RepoFilter, "", opts.IncludeArchived) {
			continue
		}
		cloned := cloneBoardTask(t)
		tasks = append(tasks, cloned)
		for _, r := range boardTaskAllRepos(cloned) {
			if _, ok := hints[r]; ok {
				continue
			}
			hints[r] = boardRepoHint{
				Name:      repoTitle(r),
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
		AppVersion:      getAppVersion(),
		NextSeq:         nextSeq,
		Tasks:           tasks,
		IncludeArchived: &incArch,
	}
	if len(hints) > 0 {
		bundle.RepoHints = hints
	}
	return bundle, nil
}

func writeBoardExportFile(path string, bundle boardBackupBundle) error {
	if bundle.Version == 0 {
		bundle.Version = 1
	}
	if bundle.Tasks == nil {
		bundle.Tasks = []boardTask{}
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
	boardImportMerge   boardImportMode = "merge"
	boardImportReplace boardImportMode = "replace"
)

type boardRepoMapping struct {
	OldPath       string
	SuggestedPath string
	ChosenPath    string
	Hint          boardRepoHint
	Status        string // "matched", "suggested", "missing"
}

type boardImportPlan struct {
	SourcePath   string
	Bundle       boardBackupBundle
	TotalTasks   int
	ConflictIDs  map[string]string
	RepoMappings []boardRepoMapping
}

type boardImportOptions struct {
	Mode    boardImportMode
	RepoMap map[string]string
}

type boardImportResult struct {
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
		if _, ok := parseBoardStatus(string(t.Status)); !ok {
			t.Status = boardTodo
		}
		if p, ok := parseBoardPriority(string(t.Priority)); ok {
			t.Priority = p
		} else {
			t.Priority = boardPriorityNone
		}
		if d, ok := parseBoardDueDate(t.DueDate); ok {
			t.DueDate = d
		} else {
			t.DueDate = ""
		}
		t.Repos = normalizeBoardRepos(t.Repos)
		for j := range t.Commits {
			t.Commits[j].Repo = normalizeBoardRepo(t.Commits[j].Repo)
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

func cloneBoardTask(t boardTask) boardTask {
	out := t
	out.Repos = slices.Clone(t.Repos)
	out.Commits = slices.Clone(t.Commits)
	out.Notes = slices.Clone(t.Notes)
	return out
}

func cloneBoardTasks(tasks []boardTask) []boardTask {
	if tasks == nil {
		return nil
	}
	out := make([]boardTask, len(tasks))
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

func isBoardRepoAvailable(dir string) bool {
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

func buildBoardImportPlan(ctx context.Context, sourcePath string, knownRepos []string) (boardImportPlan, error) {
	bundle, err := parseBoardImportFile(sourcePath)
	if err != nil {
		return boardImportPlan{}, err
	}
	cur, curErr := loadBoardFile()
	if curErr != nil {
		cur = boardFile{Version: 1, NextSeq: 1}
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
		for _, r := range boardTaskAllRepos(t) {
			if r != "" && !slices.Contains(seenRepos, r) {
				seenRepos = append(seenRepos, r)
			}
		}
	}

	mappings := make([]boardRepoMapping, 0, len(seenRepos))
	for _, oldRepo := range seenRepos {
		hint := bundle.RepoHints[oldRepo]
		if hint.Name == "" {
			hint.Name = repoTitle(oldRepo)
		}
		m := boardRepoMapping{
			OldPath: oldRepo,
			Hint:    hint,
		}
		if isBoardRepoAvailable(oldRepo) {
			norm := normalizeBoardRepo(oldRepo)
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

	return boardImportPlan{
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
		wantName = repoTitle(oldPath)
	}
	if wantName == "" {
		return ""
	}
	var nameMatches []string
	for _, kr := range knownRepos {
		if strings.EqualFold(repoTitle(kr), wantName) {
			nameMatches = append(nameMatches, kr)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0]
	}
	return ""
}

func applyRepoMapToTask(t boardTask, repoMap map[string]string) boardTask {
	out := cloneBoardTask(t)
	if len(repoMap) == 0 {
		return out
	}
	mapRepo := func(orig string) string {
		norm := normalizeBoardRepo(orig)
		if replacement, ok := repoMap[orig]; ok && strings.TrimSpace(replacement) != "" {
			return normalizeBoardRepo(replacement)
		}
		if replacement, ok := repoMap[norm]; ok && strings.TrimSpace(replacement) != "" {
			return normalizeBoardRepo(replacement)
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

func computeBoardMergePreview(existing boardFile, incoming []boardTask) (boardFile, map[string]string) {
	merged := boardFile{
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

func boardImport(sourcePath string, opts boardImportOptions) (boardFile, boardImportResult, error) {
	if opts.Mode == "" {
		opts.Mode = boardImportMerge
	}
	if opts.Mode != boardImportMerge && opts.Mode != boardImportReplace {
		return boardFile{}, boardImportResult{}, fmt.Errorf("未知导入模式: %s", opts.Mode)
	}
	bundle, err := parseBoardImportFile(sourcePath)
	if err != nil {
		return boardFile{}, boardImportResult{}, err
	}
	if err := ensureCustomBoardDirExists(); err != nil {
		return boardFile{}, boardImportResult{}, err
	}
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, boardImportResult{}, err
	}
	incoming := make([]boardTask, len(bundle.Tasks))
	for i, t := range bundle.Tasks {
		incoming[i] = applyRepoMapToTask(t, opts.RepoMap)
	}

	var result boardImportResult
	result.Mode = opts.Mode
	result.ImportedTasks = len(incoming)

	var updated boardFile
	err = withBoardLock(path, func() error {
		cur, readErr := readBoardFileAt(path)
		if readErr != nil && opts.Mode == boardImportMerge {
			return readErr
		}
		if readErr == nil && len(cur.Tasks) > 0 {
			if snapErr := snapshotBoardFileLocked(path, cur, "import"); snapErr != nil {
				return snapErr
			}
		}
		switch opts.Mode {
		case boardImportReplace:
			updated = boardFile{
				Version: 1,
				NextSeq: max(bundle.NextSeq, maxBoardSeq(incoming)+1),
				Tasks:   incoming,
			}
		case boardImportMerge:
			merged, renamed := computeBoardMergePreview(cur, incoming)
			updated = merged
			result.RenamedIDs = renamed
		}
		result.TotalTasks = len(updated.Tasks)
		return writeBoardFileAtomic(path, updated)
	})
	return updated, result, err
}

type boardDirSwitchStrategy string

const (
	boardSwitchAuto            boardDirSwitchStrategy = ""
	boardSwitchUseTarget       boardDirSwitchStrategy = "use_target"
	boardSwitchMergeTarget     boardDirSwitchStrategy = "merge_target"
	boardSwitchOverwriteTarget boardDirSwitchStrategy = "overwrite_target"
)

func boardProbeDir(dir string) (hasBoard bool, taskCount int, err error) {
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
	bf, err := readBoardFileAt(boardPath)
	if err != nil {
		return true, 0, err
	}
	return true, len(bf.Tasks), nil
}

func boardSwitchStorageDir(newDir string, strategy boardDirSwitchStrategy) (boardFile, error) {
	if strings.TrimSpace(os.Getenv(boardEnvDirKey)) != "" {
		return boardFile{}, errors.New("当前通过环境变量 XYLRING_BOARD_DIR 指定了看板目录，无法在应用内修改")
	}
	clean := filepath.Clean(strings.TrimSpace(newDir))
	if clean == "" {
		return boardFile{}, errors.New("目录路径不能为空")
	}
	absDir, err := filepath.Abs(clean)
	if err == nil {
		clean = absDir
	}
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return boardFile{}, err
	}
	info, err := boardLocationInfo()
	if err != nil {
		return boardFile{}, err
	}
	if filepath.Clean(info.ActiveDir) == clean {
		return loadBoardFile()
	}

	cur, curErr := loadBoardFile()
	if curErr != nil {
		cur = boardFile{Version: 1, NextSeq: 1}
	}

	targetPath := filepath.Join(clean, "board.json")
	hasTarget, _, probeErr := boardProbeDir(clean)
	if probeErr != nil && strategy != boardSwitchOverwriteTarget {
		return boardFile{}, probeErr
	}

	var result boardFile
	if !hasTarget {
		err = withBoardLock(targetPath, func() error {
			if err := writeBoardFileAtomic(targetPath, cur); err != nil {
				return err
			}
			result = cur
			return nil
		})
		if err != nil {
			return boardFile{}, err
		}
		copyBoardBackups(info.BackupsDir, boardBackupsDir(targetPath))
	} else {
		switch strategy {
		case boardSwitchUseTarget:
			bf, err := readBoardFileAt(targetPath)
			if err != nil {
				return boardFile{}, err
			}
			result = bf
		case boardSwitchMergeTarget:
			err = withBoardLock(targetPath, func() error {
				targetBF, err := readBoardFileAt(targetPath)
				if err != nil {
					return err
				}
				if len(targetBF.Tasks) > 0 {
					if snapErr := snapshotBoardFileLocked(targetPath, targetBF, "switch-merge"); snapErr != nil {
						return snapErr
					}
				}
				merged, _ := computeBoardMergePreview(targetBF, cur.Tasks)
				if err := writeBoardFileAtomic(targetPath, merged); err != nil {
					return err
				}
				result = merged
				return nil
			})
			if err != nil {
				return boardFile{}, err
			}
		case boardSwitchOverwriteTarget:
			err = withBoardLock(targetPath, func() error {
				if targetBF, err := readBoardFileAt(targetPath); err == nil && len(targetBF.Tasks) > 0 {
					if snapErr := snapshotBoardFileLocked(targetPath, targetBF, "switch-overwrite"); snapErr != nil {
						return snapErr
					}
				}
				if err := writeBoardFileAtomic(targetPath, cur); err != nil {
					return err
				}
				result = cur
				return nil
			})
			if err != nil {
				return boardFile{}, err
			}
		default:
			return boardFile{}, fmt.Errorf("目标目录已存在看板数据: %s", targetPath)
		}
	}

	if err := writeBoardLocationConfig(clean); err != nil {
		return boardFile{}, err
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
