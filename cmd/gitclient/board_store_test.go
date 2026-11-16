package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func withTempBoardConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userConfigDir = old })
	return dir
}

func TestBoardStoreCRUDAndLinks(t *testing.T) {
	withTempBoardConfig(t)
	repoA := filepath.Join(t.TempDir(), "repoA")
	repoB := filepath.Join(t.TempDir(), "repoB")

	bf, t1, err := boardAddTask("打通跨项目看板", "先做存储和 CLI", boardTodo, []string{repoA})
	if err != nil {
		t.Fatalf("boardAddTask: %v", err)
	}
	if t1.ID != "T-1" || len(bf.Tasks) != 1 {
		t.Fatalf("unexpected created task: %+v", t1)
	}

	_, t2, err := boardAddTask("补测试", "", boardDoing, nil)
	if err != nil || t2.ID != "T-2" {
		t.Fatalf("second task id=%q err=%v", t2.ID, err)
	}

	newStatus := boardReview
	_, updated, err := boardUpdateTask("1", boardTaskPatch{Status: &newStatus})
	if err != nil || updated.Status != boardReview {
		t.Fatalf("update status: %+v err=%v", updated, err)
	}

	_, linked, err := boardLinkCommit("t-1", boardCommitLink{
		Repo:    repoB,
		Hash:    "abcdef0123456789abcdef0123456789abcdef01",
		Subject: "feat: board cli",
		Agent:   "Claude Code",
		Model:   "Claude Opus",
	})
	if err != nil {
		t.Fatalf("boardLinkCommit: %v", err)
	}
	if len(linked.Commits) != 1 || len(linked.Repos) != 2 {
		t.Fatalf("expected 1 commit and 2 repos, got commits=%d repos=%v", len(linked.Commits), linked.Repos)
	}
	// Duplicate link should merge instead of appending.
	_, linked, err = boardLinkCommit("T-1", boardCommitLink{
		Repo: repoB,
		Hash: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
	})
	if err != nil || len(linked.Commits) != 1 {
		t.Fatalf("duplicate link should merge, got %d err=%v", len(linked.Commits), err)
	}

	_, noted, err := boardAddNote("T-1", "已完成联调", "Codex", "GPT-5")
	if err != nil || len(noted.Notes) != 1 || noted.Notes[0].Author != "Codex" {
		t.Fatalf("boardAddNote: %+v err=%v", noted, err)
	}

	_, unlinked, err := boardUnlinkCommit("T-1", repoB, "abcdef0")
	if err != nil || len(unlinked.Commits) != 0 {
		t.Fatalf("boardUnlinkCommit: %+v err=%v", unlinked, err)
	}

	doneStatus := boardDone
	if _, _, err := boardUpdateTask("T-1", boardTaskPatch{Status: &doneStatus}); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	bf, archivedCount, err := boardArchiveDone()
	if err != nil || archivedCount != 1 {
		t.Fatalf("boardArchiveDone count=%d err=%v", archivedCount, err)
	}
	if !bf.Tasks[1].Archived && !bf.Tasks[0].Archived {
		t.Fatalf("expected T-1 archived, tasks=%+v", bf.Tasks)
	}

	if _, err := boardDeleteTask("T-2"); err != nil {
		t.Fatalf("boardDeleteTask: %v", err)
	}
	loaded, err := loadBoardFile()
	if err != nil || len(loaded.Tasks) != 1 || loaded.Tasks[0].ID != "T-1" {
		t.Fatalf("loadBoardFile after delete: %+v err=%v", loaded, err)
	}
}

func TestBoardStoreConcurrentMutations(t *testing.T) {
	withTempBoardConfig(t)
	_, task, err := boardAddTask("并发追加备注", "", boardDoing, nil)
	if err != nil {
		t.Fatalf("boardAddTask: %v", err)
	}

	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _, err := boardAddNote(task.ID, fmt.Sprintf("note-%d", n), "Agent", "")
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent boardAddNote error: %v", err)
	}
	bf, err := loadBoardFile()
	if err != nil {
		t.Fatalf("loadBoardFile: %v", err)
	}
	if len(bf.Tasks[0].Notes) != workers {
		t.Fatalf("expected %d notes, got %d", workers, len(bf.Tasks[0].Notes))
	}
}
