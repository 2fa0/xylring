package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/xylring/internal/board"
)

func withTempBoardConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	homeDir := filepath.Join(root, "home")
	oldCfg := userConfigDir
	oldBoardCfg := board.UserConfigDir
	oldHome := board.BoardHomeDir
	userConfigDir = func() (string, error) { return cfgDir, nil }
	board.UserConfigDir = userConfigDir
	board.BoardHomeDir = func() (string, error) { return homeDir, nil }
	t.Setenv(board.BoardEnvDirKey, "")
	t.Cleanup(func() {
		userConfigDir = oldCfg
		board.UserConfigDir = oldBoardCfg
		board.BoardHomeDir = oldHome
	})
	return cfgDir
}

func TestBoardCLIEndToEndWithGitRepo(t *testing.T) {
	withTempBoardConfig(t)
	repoA, gitA := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoA, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitA("add", "a.txt")
	gitA("commit", "-m", "feat: add board core\n\nAI-Agent: Claude Code\nAI-Model: Claude Opus 4")

	repoB, gitB := initTestRepo(t)
	if err := os.WriteFile(filepath.Join(repoB, "b.txt"), []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitB("add", "b.txt")
	gitB("commit", "-m", "fix: sync worker")

	ctx := context.Background()
	var out bytes.Buffer

	if err := runBoardCLI(ctx, repoA, []string{"add", "打通多仓库联动", "--desc", "需求说明", "--json"}, &out); err != nil {
		t.Fatalf("board add: %v", err)
	}
	var created boardTask
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal add json: %v (%s)", err, out.String())
	}
	if created.ID != "T-1" || created.Status != boardTodo {
		t.Fatalf("unexpected created task: %+v", created)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"update", "T-1", "--status", "doing"}, &out); err != nil {
		t.Fatalf("board update doing: %v", err)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"link", "T-1", "HEAD"}, &out); err != nil {
		t.Fatalf("board link repoA: %v", err)
	}
	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"link", "T-1", "HEAD", "--repo", repoB, "--json"}, &out); err != nil {
		t.Fatalf("board link repoB: %v", err)
	}
	var linked boardTask
	if err := json.Unmarshal(out.Bytes(), &linked); err != nil {
		t.Fatalf("unmarshal link json: %v", err)
	}
	if len(linked.Commits) != 2 || len(linked.Repos) != 2 {
		t.Fatalf("expected 2 commits and 2 repos, got commits=%d repos=%v", len(linked.Commits), linked.Repos)
	}
	if linked.Commits[0].Agent != "Claude Code" || linked.Commits[0].Model != "Claude Opus 4" {
		t.Fatalf("expected provenance captured on link, got %+v", linked.Commits[0])
	}
	if linked.Commits[0].Subject != "feat: add board core" || linked.Commits[0].Branch != "main" {
		t.Fatalf("expected subject and branch snapshot, got %+v", linked.Commits[0])
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"note", "T-1", "两端都已提交", "--agent", "Codex", "--model", "GPT-5"}, &out); err != nil {
		t.Fatalf("board note: %v", err)
	}
	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"update", "T-1", "--done"}, &out); err != nil {
		t.Fatalf("board update --done: %v", err)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"list", "--status", "done", "--repo", repoB}, &out); err != nil {
		t.Fatalf("board list: %v", err)
	}
	if !strings.Contains(out.String(), "T-1") || !strings.Contains(out.String(), "打通多仓库联动") {
		t.Fatalf("unexpected list output: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"show", "T-1"}, &out); err != nil {
		t.Fatalf("board show: %v", err)
	}
	if !strings.Contains(out.String(), "Claude Code") || !strings.Contains(out.String(), "两端都已提交") {
		t.Fatalf("unexpected show output: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoA, []string{"snippet"}, &out); err != nil {
		t.Fatalf("board snippet: %v", err)
	}
	if !strings.Contains(out.String(), "xylring board link") {
		t.Fatalf("unexpected snippet: %q", out.String())
	}
}

func TestBoardUpdateCLIRequiresAField(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := boardAddTask("空更新测试", "", boardTodo, nil); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runBoardUpdateCLI(context.Background(), "", []string{"T-1"}, &out); err == nil {
		t.Fatalf("expected an error for a no-op update, got %q", out.String())
	}
	bf, err := loadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if len(bf.Tasks) != 1 || bf.Tasks[0].Title != "空更新测试" {
		t.Fatalf("board changed by a no-op update: %+v", bf.Tasks)
	}
}

func TestBoardCLIBackupExportImportAndRestore(t *testing.T) {
	withTempBoardConfig(t)
	repoOld, _ := initTestRepo(t)
	repoNew, _ := initTestRepo(t)
	ctx := context.Background()
	var out bytes.Buffer

	if err := runBoardCLI(ctx, repoOld, []string{"add", "CLI备份验证", "--repo", repoOld}, &out); err != nil {
		t.Fatalf("board add: %v", err)
	}

	out.Reset()
	if err := runBoardCLI(ctx, repoOld, []string{"path", "--json"}, &out); err != nil {
		t.Fatalf("board path: %v", err)
	}
	var info boardStorageInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil || info.BoardPath == "" {
		t.Fatalf("unexpected board path json: %q err=%v", out.String(), err)
	}

	exportFile := filepath.Join(t.TempDir(), "cli-backup.json")
	out.Reset()
	if err := runBoardCLI(ctx, repoOld, []string{"export", exportFile}, &out); err != nil {
		t.Fatalf("board export: %v", err)
	}
	if !strings.Contains(out.String(), "已导出 1 个任务") {
		t.Fatalf("unexpected export output: %q", out.String())
	}

	// Delete task -> triggers auto backup
	out.Reset()
	if err := runBoardCLI(ctx, repoOld, []string{"delete", "T-1"}, &out); err != nil {
		t.Fatalf("board delete: %v", err)
	}
	out.Reset()
	if err := runBoardCLI(ctx, repoOld, []string{"backups", "--json"}, &out); err != nil {
		t.Fatalf("board backups: %v", err)
	}
	var backups []boardBackupInfo
	if err := json.Unmarshal(out.Bytes(), &backups); err != nil || len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %+v err=%v", backups, err)
	}

	// Dry-run import
	out.Reset()
	if err := runBoardCLI(ctx, repoNew, []string{"import", exportFile, "--dry-run"}, &out); err != nil {
		t.Fatalf("board import dry-run: %v", err)
	}
	if !strings.Contains(out.String(), "预览导入") {
		t.Fatalf("unexpected dry-run output: %q", out.String())
	}

	// Actual import with repo remap
	out.Reset()
	if err := runBoardCLI(ctx, repoNew, []string{"import", exportFile, "--mode", "merge", "--map-repo", canonicalRepoDir(repoOld) + "=" + repoNew}, &out); err != nil {
		t.Fatalf("board import: %v", err)
	}
	bf, err := loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 || bf.Tasks[0].Repos[0] != canonicalRepoDir(repoNew) {
		t.Fatalf("unexpected board after CLI import: %+v err=%v", bf.Tasks, err)
	}

	// Restore from automatic backup by filename
	out.Reset()
	if err := runBoardCLI(ctx, repoOld, []string{"restore", backups[0].Name}, &out); err != nil {
		t.Fatalf("board restore: %v", err)
	}
	bf, err = loadBoardFile()
	if err != nil || len(bf.Tasks) != 1 || bf.Tasks[0].Repos[0] != canonicalRepoDir(repoOld) {
		t.Fatalf("unexpected board after CLI restore: %+v err=%v", bf.Tasks, err)
	}
}

func TestBoardCLIPriorityAndDueDate(t *testing.T) {
	withTempBoardConfig(t)
	ctx := context.Background()
	var out bytes.Buffer

	if err := runBoardCLI(ctx, "", []string{"add", "修复线上告警", "--priority", "重要且紧急", "--due", "2026-11-20", "--json"}, &out); err != nil {
		t.Fatalf("board add with priority/due: %v", err)
	}
	var created boardTask
	if err := json.Unmarshal(out.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal add json: %v", err)
	}
	if created.Priority != boardPriorityUrgentImportant || created.DueDate != "2026-11-20" {
		t.Fatalf("unexpected priority/due on add: %+v", created)
	}

	out.Reset()
	if err := runBoardCLI(ctx, "", []string{"show", "T-1"}, &out); err != nil {
		t.Fatalf("board show: %v", err)
	}
	if !strings.Contains(out.String(), "重要且紧急") || !strings.Contains(out.String(), "2026-11-20") {
		t.Fatalf("show missing priority/due: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, "", []string{"list", "--priority", "urgent_important"}, &out); err != nil {
		t.Fatalf("board list --priority: %v", err)
	}
	if !strings.Contains(out.String(), "T-1") || !strings.Contains(out.String(), "截止 2026-11-20") {
		t.Fatalf("list output missing task/due: %q", out.String())
	}

	out.Reset()
	if err := runBoardCLI(ctx, "", []string{"update", "T-1", "--priority", "important", "--due", "2026-11-28", "--json"}, &out); err != nil {
		t.Fatalf("board update priority/due: %v", err)
	}
	var updated boardTask
	if err := json.Unmarshal(out.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update json: %v", err)
	}
	if updated.Priority != boardPriorityImportant || updated.DueDate != "2026-11-28" {
		t.Fatalf("unexpected updated priority/due: %+v", updated)
	}

	out.Reset()
	if err := runBoardCLI(ctx, "", []string{"update", "T-1", "--priority", "none", "--due", "clear", "--json"}, &out); err != nil {
		t.Fatalf("board clear priority/due: %v", err)
	}
	var cleared boardTask
	if err := json.Unmarshal(out.Bytes(), &cleared); err != nil {
		t.Fatalf("unmarshal clear json: %v", err)
	}
	if cleared.Priority != boardPriorityNone || cleared.DueDate != "" {
		t.Fatalf("expected cleared priority/due, got %+v", cleared)
	}

	if err := runBoardCLI(ctx, "", []string{"update", "T-1", "--due", "not-a-date"}, &out); err == nil {
		t.Fatal("expected error for invalid due date")
	}
}
