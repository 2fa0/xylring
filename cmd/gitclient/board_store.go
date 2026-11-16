package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
}

type boardTask struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Status      boardStatus       `json:"status"`
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

func boardConfigPath() (string, error) {
	return configFile("board.json")
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
	if len(strings.TrimSpace(string(b))) == 0 {
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

func loadBoardFile() (boardFile, error) {
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
		if !os.IsExist(err) {
			return err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("等待看板文件锁超时")
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func writeBoardFileAtomic(path string, bf boardFile) error {
	if bf.Version == 0 {
		bf.Version = 1
	}
	if bf.NextSeq <= 0 {
		bf.NextSeq = maxBoardSeq(bf.Tasks) + 1
	}
	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".board-*.tmp")
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
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
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

func mutateBoardFile(fn func(*boardFile) error) (boardFile, error) {
	path, err := boardConfigPath()
	if err != nil {
		return boardFile{}, err
	}
	var updated boardFile
	err = withBoardLock(path, func() error {
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
	Archived    *bool
	Repos       *[]string
	AddRepos    []string
}

func boardAddTask(title, desc string, status boardStatus, repos []string) (boardFile, boardTask, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return boardFile{}, boardTask{}, errors.New("任务标题不能为空")
	}
	if status == "" {
		status = boardTodo
	}
	if _, ok := parseBoardStatus(string(status)); !ok {
		return boardFile{}, boardTask{}, fmt.Errorf("未知任务状态: %s", status)
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
			Description: strings.TrimSpace(desc),
			Status:      status,
			Repos:       normalizeBoardRepos(repos),
			CreatedAt:   now,
			UpdatedAt:   now,
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

func boardDeleteTask(id string) (boardFile, error) {
	return mutateBoardFile(func(f *boardFile) error {
		idx := findBoardTaskIndex(f.Tasks, id)
		if idx < 0 {
			return fmt.Errorf("未找到看板任务: %s", id)
		}
		f.Tasks = slices.Delete(f.Tasks, idx, idx+1)
		return nil
	})
}

func boardArchiveDone() (boardFile, int, error) {
	count := 0
	bf, err := mutateBoardFile(func(f *boardFile) error {
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
