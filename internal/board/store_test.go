package board

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"os/exec"
	"testing"
	"time"
)

func withTempBoardConfig(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config")
	homeDir := filepath.Join(root, "home")
	oldCfg := UserConfigDir
	oldHome := BoardHomeDir
	UserConfigDir = func() (string, error) { return cfgDir, nil }
	BoardHomeDir = func() (string, error) { return homeDir, nil }
	t.Setenv(BoardEnvDirKey, "")
	t.Cleanup(func() {
		UserConfigDir = oldCfg
		BoardHomeDir = oldHome
	})
	return cfgDir
}

func TestBoardStoreCRUDAndLinks(t *testing.T) {
	withTempBoardConfig(t)
	repoA := filepath.Join(t.TempDir(), "repoA")
	repoB := filepath.Join(t.TempDir(), "repoB")

	bf, t1, err := BoardAddTask("打通跨项目看板", "先做存储和 CLI", BoardTodo, []string{repoA})
	if err != nil {
		t.Fatalf("BoardAddTask: %v", err)
	}
	if t1.ID != "T-1" || len(bf.Tasks) != 1 {
		t.Fatalf("unexpected created task: %+v", t1)
	}

	_, t2, err := BoardAddTask("补测试", "", BoardDoing, nil)
	if err != nil || t2.ID != "T-2" {
		t.Fatalf("second task id=%q err=%v", t2.ID, err)
	}

	newStatus := BoardReview
	_, updated, err := BoardUpdateTask("1", BoardTaskPatch{Status: &newStatus})
	if err != nil || updated.Status != BoardReview {
		t.Fatalf("update status: %+v err=%v", updated, err)
	}

	_, linked, err := BoardLinkCommit("t-1", BoardCommitLink{
		Repo:    repoB,
		Hash:    "abcdef0123456789abcdef0123456789abcdef01",
		Subject: "feat: board cli",
		Agent:   "Claude Code",
		Model:   "Claude Opus",
	})
	if err != nil {
		t.Fatalf("BoardLinkCommit: %v", err)
	}
	if len(linked.Commits) != 1 || len(linked.Repos) != 2 {
		t.Fatalf("expected 1 commit and 2 repos, got commits=%d repos=%v", len(linked.Commits), linked.Repos)
	}
	// Duplicate link should merge instead of appending.
	_, linked, err = BoardLinkCommit("T-1", BoardCommitLink{
		Repo: repoB,
		Hash: "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
	})
	if err != nil || len(linked.Commits) != 1 {
		t.Fatalf("duplicate link should merge, got %d err=%v", len(linked.Commits), err)
	}

	_, noted, err := BoardAddNote("T-1", "已完成联调", "Codex", "GPT-5")
	if err != nil || len(noted.Notes) != 1 || noted.Notes[0].Author != "Codex" {
		t.Fatalf("BoardAddNote: %+v err=%v", noted, err)
	}

	_, unlinked, err := BoardUnlinkCommit("T-1", repoB, "abcdef0")
	if err != nil || len(unlinked.Commits) != 0 {
		t.Fatalf("BoardUnlinkCommit: %+v err=%v", unlinked, err)
	}

	doneStatus := BoardDone
	if _, _, err := BoardUpdateTask("T-1", BoardTaskPatch{Status: &doneStatus}); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	bf, archivedCount, err := BoardArchiveDoneTasks([]string{"T-1"})
	if err != nil || archivedCount != 1 {
		t.Fatalf("BoardArchiveDoneTasks count=%d err=%v", archivedCount, err)
	}
	if !bf.Tasks[1].Archived && !bf.Tasks[0].Archived {
		t.Fatalf("expected T-1 archived, tasks=%+v", bf.Tasks)
	}

	if _, err := BoardDeleteTask("T-2"); err != nil {
		t.Fatalf("BoardDeleteTask: %v", err)
	}
	loaded, err := LoadBoardFile()
	if err != nil || len(loaded.Tasks) != 1 || loaded.Tasks[0].ID != "T-1" {
		t.Fatalf("LoadBoardFile after delete: %+v err=%v", loaded, err)
	}
}

func TestBoardStoreConcurrentMutations(t *testing.T) {
	withTempBoardConfig(t)
	_, task, err := BoardAddTask("并发追加备注", "", BoardDoing, nil)
	if err != nil {
		t.Fatalf("BoardAddTask: %v", err)
	}

	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _, err := BoardAddNote(task.ID, fmt.Sprintf("note-%d", n), "Agent", "")
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent BoardAddNote error: %v", err)
	}
	bf, err := LoadBoardFile()
	if err != nil {
		t.Fatalf("LoadBoardFile: %v", err)
	}
	if len(bf.Tasks[0].Notes) != workers {
		t.Fatalf("expected %d notes, got %d", workers, len(bf.Tasks[0].Notes))
	}
}

func TestBoardNoteUpdateDeleteAndMove(t *testing.T) {
	withTempBoardConfig(t)
	_, task, err := BoardAddTask("备注增删改与排序", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"第一条", "第二条", "第三条"} {
		if _, task, err = BoardAddNote(task.ID, text, "human", ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(task.Notes) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(task.Notes))
	}
	n1, n2, n3 := task.Notes[0].ID, task.Notes[1].ID, task.Notes[2].ID

	// Update second note
	createdAt := task.Notes[1].CreatedAt
	if !task.Notes[1].UpdatedAt.IsZero() {
		t.Fatalf("new note should not have UpdatedAt yet, got %v", task.Notes[1].UpdatedAt)
	}
	if _, task, err = BoardUpdateNote(task.ID, n2, "第二条已修改"); err != nil {
		t.Fatal(err)
	}
	if task.Notes[1].Text != "第二条已修改" {
		t.Fatalf("expected updated note text, got %q", task.Notes[1].Text)
	}
	if !task.Notes[1].CreatedAt.Equal(createdAt) {
		t.Fatalf("expected CreatedAt preserved, got %v want %v", task.Notes[1].CreatedAt, createdAt)
	}
	if task.Notes[1].UpdatedAt.IsZero() || task.Notes[1].UpdatedAt.Before(createdAt) {
		t.Fatalf("expected UpdatedAt set after edit, got %v (created %v)", task.Notes[1].UpdatedAt, createdAt)
	}
	if got := task.Notes[1].DisplayTime(); !got.Equal(task.Notes[1].UpdatedAt) {
		t.Fatalf("expected displayTime to return UpdatedAt %v, got %v", task.Notes[1].UpdatedAt, got)
	}
	updatedAt := task.Notes[1].UpdatedAt
	if _, task, err = BoardUpdateNote(task.ID, n2, "第二条已修改"); err != nil {
		t.Fatal(err)
	}
	if !task.Notes[1].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("unchanged note update should keep UpdatedAt %v, got %v", updatedAt, task.Notes[1].UpdatedAt)
	}
	if _, _, err = BoardUpdateNote(task.ID, n2, "   "); err == nil {
		t.Fatal("expected empty note update error")
	}

	// Move first note to last position
	if _, task, err = BoardMoveNote(task.ID, n1, 2); err != nil {
		t.Fatal(err)
	}
	if task.Notes[0].ID != n2 || task.Notes[1].ID != n3 || task.Notes[2].ID != n1 {
		t.Fatalf("unexpected order after moving down: %+v", task.Notes)
	}

	// Move last note back to top
	if _, task, err = BoardMoveNote(task.ID, n1, 0); err != nil {
		t.Fatal(err)
	}
	if task.Notes[0].ID != n1 || task.Notes[1].ID != n2 || task.Notes[2].ID != n3 {
		t.Fatalf("unexpected order after moving up: %+v", task.Notes)
	}

	// Delete middle note
	if _, task, err = BoardDeleteNote(task.ID, n2); err != nil {
		t.Fatal(err)
	}
	if len(task.Notes) != 2 || task.Notes[0].ID != n1 || task.Notes[1].ID != n3 {
		t.Fatalf("unexpected notes after delete: %+v", task.Notes)
	}
}

func TestBoardDefaultLocationAndLegacyMigration(t *testing.T) {
	withTempBoardConfig(t)
	legacyPath, err := configFile("board.json")
	if err != nil {
		t.Fatal(err)
	}
	legacyData := BoardFile{
		Version: 1,
		NextSeq: 3,
		Tasks: []BoardTask{
			{ID: "T-2", Title: "旧位置任务", Status: BoardDoing, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		},
	}
	if err := WriteBoardFileAtomic(legacyPath, legacyData); err != nil {
		t.Fatal(err)
	}

	info, err := BoardLocationInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.IsCustom || !strings.HasSuffix(info.DefaultDir, filepath.Join(".local", "share", "xylring")) {
		t.Fatalf("unexpected default location info: %+v", info)
	}

	bf, err := LoadBoardFile()
	if err != nil {
		t.Fatalf("LoadBoardFile after migration: %v", err)
	}
	if len(bf.Tasks) != 1 || bf.Tasks[0].ID != "T-2" {
		t.Fatalf("expected migrated task T-2, got %+v", bf.Tasks)
	}
	if _, err := os.Stat(info.BoardPath); err != nil {
		t.Fatalf("expected board.json at default .local path: %v", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated.bak"); err != nil {
		t.Fatalf("expected legacy board.json renamed to .migrated.bak: %v", err)
	}
}

func TestBoardAutoBackupAndRestore(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := BoardAddTask("第一项任务", "", BoardTodo, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BoardAddTask("将被删除的任务", "", BoardDoing, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := BoardDeleteTask("T-2"); err != nil {
		t.Fatal(err)
	}
	backups, err := ListBoardBackups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected 1 backup after delete, got %d err=%v", len(backups), err)
	}
	if backups[0].Reason != "delete" || backups[0].TaskCount != 2 {
		t.Fatalf("unexpected backup metadata: %+v", backups[0])
	}

	restored, err := BoardRestoreBackup(backups[0].Path)
	if err != nil {
		t.Fatalf("BoardRestoreBackup: %v", err)
	}
	if len(restored.Tasks) != 2 || FindBoardTaskIndex(restored.Tasks, "T-2") < 0 {
		t.Fatalf("expected T-2 restored, got %+v", restored.Tasks)
	}
}

func TestBoardExportImportMergeReplaceAndRepoRemap(t *testing.T) {
	withTempBoardConfig(t)
	repoOld, gitOld := initTestRepo(t)
	gitOld("remote", "add", "origin", "https://github.com/example/xylring-demo.git")
	repoNew, gitNew := initTestRepo(t)
	gitNew("remote", "add", "origin", "git@github.com:example/xylring-demo.git")

	_, created, err := BoardAddTask("导出任务", "带提交和备注", BoardReview, []string{repoOld})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BoardLinkCommit(created.ID, BoardCommitLink{
		Repo:    repoOld,
		Hash:    "1111111111111111111111111111111111111111",
		Subject: "feat: demo",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BoardAddNote(created.ID, "保留备注", "Codex", "GPT-5"); err != nil {
		t.Fatal(err)
	}

	bundle, err := BoardExport(context.Background(), BoardExportOptions{IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.RepoHints[CanonicalRepoDir(repoOld)].RemoteURL == "" {
		t.Fatalf("expected remote_url hint captured, got %+v", bundle.RepoHints)
	}
	exportPath := filepath.Join(t.TempDir(), "board-backup.json")
	if err := WriteBoardExportFile(exportPath, bundle); err != nil {
		t.Fatal(err)
	}

	// Simulate moving to a new machine where repoOld no longer exists and only repoNew is known.
	if err := os.RemoveAll(repoOld); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildBoardImportPlan(context.Background(), exportPath, []string{repoNew})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ConflictIDs) != 1 || plan.ConflictIDs["T-1"] != "T-2" {
		t.Fatalf("expected T-1 -> T-2 conflict preview, got %+v", plan.ConflictIDs)
	}
	if len(plan.RepoMappings) != 1 || plan.RepoMappings[0].Status != "suggested" || plan.RepoMappings[0].ChosenPath != CanonicalRepoDir(repoNew) {
		t.Fatalf("expected remote URL auto-match to repoNew, got %+v", plan.RepoMappings)
	}

	// Merge import with remap
	bf, res, err := BoardImport(exportPath, BoardImportOptions{
		Mode:    BoardImportMerge,
		RepoMap: map[string]string{plan.RepoMappings[0].OldPath: repoNew},
	})
	if err != nil {
		t.Fatalf("BoardImport merge: %v", err)
	}
	if res.RenamedIDs["T-1"] != "T-2" || len(bf.Tasks) != 2 || bf.NextSeq != 3 {
		t.Fatalf("unexpected merge result: res=%+v nextSeq=%d tasks=%+v", res, bf.NextSeq, bf.Tasks)
	}
	idx2 := FindBoardTaskIndex(bf.Tasks, "T-2")
	if idx2 < 0 || bf.Tasks[idx2].Repos[0] != CanonicalRepoDir(repoNew) || bf.Tasks[idx2].Commits[0].Repo != CanonicalRepoDir(repoNew) {
		t.Fatalf("expected remapped repos in T-2, got %+v", bf.Tasks[idx2])
	}

	// Replace import restores only 1 task and snapshots prior state
	bf, res, err = BoardImport(exportPath, BoardImportOptions{Mode: BoardImportReplace})
	if err != nil {
		t.Fatalf("BoardImport replace: %v", err)
	}
	if len(bf.Tasks) != 1 || res.Mode != BoardImportReplace {
		t.Fatalf("unexpected replace result: res=%+v tasks=%+v", res, bf.Tasks)
	}
	backups, err := ListBoardBackups()
	if err != nil || len(backups) < 2 {
		t.Fatalf("expected pre-import snapshots kept, got %+v err=%v", backups, err)
	}
}

func TestBoardSwitchStorageDirStrategiesAndMissingDir(t *testing.T) {
	withTempBoardConfig(t)
	if _, _, err := BoardAddTask("默认目录任务", "", BoardTodo, nil); err != nil {
		t.Fatal(err)
	}

	customA := filepath.Join(t.TempDir(), "icloud-board")
	bf, err := BoardSwitchStorageDir(customA, BoardSwitchAuto)
	if err != nil {
		t.Fatalf("switch to empty custom dir: %v", err)
	}
	if len(bf.Tasks) != 1 || bf.Tasks[0].Title != "默认目录任务" {
		t.Fatalf("expected task migrated to customA, got %+v", bf.Tasks)
	}
	info, err := BoardLocationInfo()
	if err != nil || !info.IsCustom || info.ActiveDir != customA {
		t.Fatalf("unexpected custom location info: %+v err=%v", info, err)
	}

	// Prepare customB with existing board data and test merge strategy.
	customB := filepath.Join(t.TempDir(), "synced-board")
	if err := WriteBoardFileAtomic(filepath.Join(customB, "board.json"), BoardFile{
		Version: 1,
		NextSeq: 2,
		Tasks: []BoardTask{
			{ID: "T-1", Title: "另一台设备的任务", Status: BoardDoing, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if has, count, err := BoardProbeDir(customB); err != nil || !has || count != 1 {
		t.Fatalf("BoardProbeDir failed: has=%v count=%d err=%v", has, count, err)
	}
	if _, err := BoardSwitchStorageDir(customB, BoardSwitchAuto); err == nil {
		t.Fatal("expected error when switching to dir with existing board.json without strategy")
	}
	bf, err = BoardSwitchStorageDir(customB, BoardSwitchMergeTarget)
	if err != nil {
		t.Fatalf("BoardSwitchMergeTarget: %v", err)
	}
	if len(bf.Tasks) != 2 || bf.NextSeq != 3 {
		t.Fatalf("expected 2 merged tasks in customB, got %+v nextSeq=%d", bf.Tasks, bf.NextSeq)
	}

	// If custom dir disappears, LoadBoardFile must error instead of creating an empty board.
	if err := os.RemoveAll(customB); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBoardFile(); err == nil {
		t.Fatal("expected error when custom board directory is missing")
	}

	// Switching back to default dir uses existing default board.json and clears location.json.
	bf, err = BoardSwitchStorageDir(info.DefaultDir, BoardSwitchUseTarget)
	if err != nil {
		t.Fatalf("reset to default dir: %v", err)
	}
	infoAfter, err := BoardLocationInfo()
	if err != nil || infoAfter.IsCustom || len(bf.Tasks) != 1 {
		t.Fatalf("expected reset to default dir, info=%+v tasks=%+v err=%v", infoAfter, bf.Tasks, err)
	}
}

func TestBoardMoveTask(t *testing.T) {
	withTempBoardConfig(t)
	_, t3, err := BoardAddTask("任务三", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, t2, err := BoardAddTask("任务二", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, t1, err := BoardAddTask("任务一", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, d1, err := BoardAddTask("进行中任务", "", BoardDoing, nil)
	if err != nil {
		t.Fatal(err)
	}

	taskIDs := func(bf BoardFile) []string {
		ids := make([]string, len(bf.Tasks))
		for i, task := range bf.Tasks {
			ids[i] = task.ID
		}
		return ids
	}

	// Move t1 after t3 within todo
	bf, moved, err := BoardMoveTask(t1.ID, BoardTodo, t3.ID, true)
	if err != nil {
		t.Fatalf("move t1 after t3: %v", err)
	}
	if moved.Status != BoardTodo || fmt.Sprint(taskIDs(bf)) != fmt.Sprint([]string{d1.ID, t2.ID, t3.ID, t1.ID}) {
		t.Fatalf("unexpected order after moving down: %v", taskIDs(bf))
	}

	// Move t1 before t2 within todo
	bf, _, err = BoardMoveTask(t1.ID, BoardTodo, t2.ID, false)
	if err != nil {
		t.Fatalf("move t1 before t2: %v", err)
	}
	if fmt.Sprint(taskIDs(bf)) != fmt.Sprint([]string{d1.ID, t1.ID, t2.ID, t3.ID}) {
		t.Fatalf("unexpected order after moving up: %v", taskIDs(bf))
	}

	// Move t3 across columns before d1 in doing
	bf, moved, err = BoardMoveTask(t3.ID, BoardDoing, d1.ID, false)
	if err != nil {
		t.Fatalf("move t3 before d1: %v", err)
	}
	if moved.Status != BoardDoing || fmt.Sprint(taskIDs(bf)) != fmt.Sprint([]string{t3.ID, d1.ID, t1.ID, t2.ID}) {
		t.Fatalf("unexpected order after cross-column move: %v status=%s", taskIDs(bf), moved.Status)
	}

	// Error cases
	if _, _, err := BoardMoveTask("T-999", BoardTodo, t1.ID, true); err == nil {
		t.Fatal("expected error for missing task")
	}
	if _, _, err := BoardMoveTask(t1.ID, BoardTodo, "T-999", true); err == nil {
		t.Fatal("expected error for missing anchor task")
	}
	if _, _, err := BoardMoveTask(t1.ID, BoardStatus("invalid"), t2.ID, true); err == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestBoardPriorityAndDueDateStore(t *testing.T) {
	withTempBoardConfig(t)

	// Priority parsing & aliases
	for raw, want := range map[string]BoardPriority{
		"重要且紧急":            BoardPriorityUrgentImportant,
		"q1":               BoardPriorityUrgentImportant,
		"urgent_important": BoardPriorityUrgentImportant,
		"重要不紧急":            BoardPriorityImportant,
		"p2":               BoardPriorityImportant,
		"紧急不重要":            BoardPriorityUrgent,
		"不重要不紧急":           BoardPriorityLow,
		"clear":            BoardPriorityNone,
		"":                 BoardPriorityNone,
	} {
		got, ok := ParseBoardPriority(raw)
		if !ok || got != want {
			t.Fatalf("ParseBoardPriority(%q) = (%q, %v), want (%q, true)", raw, got, ok, want)
		}
	}
	if _, ok := ParseBoardPriority("invalid-prio"); ok {
		t.Fatal("expected invalid priority to fail")
	}

	// Due date parsing & normalization
	if got, ok := ParseBoardDueDate("2026/11/5"); !ok || got != "2026-11-05" {
		t.Fatalf("ParseBoardDueDate slash = (%q, %v)", got, ok)
	}
	if got, ok := ParseBoardDueDate("none"); !ok || got != "" {
		t.Fatalf("ParseBoardDueDate clear = (%q, %v)", got, ok)
	}
	if _, ok := ParseBoardDueDate("2026-02-30"); ok {
		t.Fatal("expected invalid calendar date 2026-02-30 to fail")
	}

	// Due state calculation
	now := time.Date(2026, 11, 17, 15, 0, 0, 0, time.Local)
	if _, st, ok := BoardTaskDueInfo("2026-11-16", false, now); !ok || st != BoardDueOverdue {
		t.Fatalf("yesterday should be overdue, got ok=%v st=%v", ok, st)
	}
	if _, st, ok := BoardTaskDueInfo("2026-11-17", false, now); !ok || st != BoardDueToday {
		t.Fatalf("today should be due today, got ok=%v st=%v", ok, st)
	}
	if _, st, ok := BoardTaskDueInfo("2026-11-18", false, now); !ok || st != boardDueUpcoming {
		t.Fatalf("tomorrow should be upcoming, got ok=%v st=%v", ok, st)
	}
	if _, st, ok := BoardTaskDueInfo("2026-11-16", true, now); !ok || st != boardDueUpcoming {
		t.Fatalf("completed overdue task should stay neutral, got ok=%v st=%v", ok, st)
	}

	// Create with priority and due date, search, update, clear
	_, created, err := BoardAddTaskWithOptions(BoardAddTaskOptions{
		Title:    "排查网关耗时",
		Status:   BoardDoing,
		Priority: BoardPriorityUrgentImportant,
		DueDate:  "2026-11-20",
	})
	if err != nil {
		t.Fatalf("BoardAddTaskWithOptions: %v", err)
	}
	if created.Priority != BoardPriorityUrgentImportant || created.DueDate != "2026-11-20" {
		t.Fatalf("unexpected created task: %+v", created)
	}
	if !BoardMatchesFilter(created, "", "重要且紧急", false) || !BoardMatchesFilter(created, "", "2026-11-20", false) {
		t.Fatalf("expected search filter to match priority and due date")
	}

	newPrio := BoardPriorityImportant
	newDue := "2026-11-25"
	_, updated, err := BoardUpdateTask(created.ID, BoardTaskPatch{Priority: &newPrio, DueDate: &newDue})
	if err != nil || updated.Priority != BoardPriorityImportant || updated.DueDate != "2026-11-25" {
		t.Fatalf("update priority/due: %+v err=%v", updated, err)
	}

	clearPrio := BoardPriorityNone
	clearDue := ""
	_, cleared, err := BoardUpdateTask(created.ID, BoardTaskPatch{Priority: &clearPrio, DueDate: &clearDue})
	if err != nil || cleared.Priority != BoardPriorityNone || cleared.DueDate != "" {
		t.Fatalf("clear priority/due: %+v err=%v", cleared, err)
	}
}

func TestBoardChecklistParseAndToggle(t *testing.T) {
	desc := strings.Join([]string{
		"Background context line",
		"- [ ] First item",
		"  - [x] Nested done item",
		"* [X] Starred done item",
		"  * [ ] Starred todo item",
		"- plain bullet",
		"Trailing note",
	}, "\n")

	items := ParseBoardChecklist(desc)
	if len(items) != 4 {
		t.Fatalf("expected 4 checklist items, got %d: %+v", len(items), items)
	}
	for i, want := range []BoardChecklistItem{
		{LineIndex: 1, Checked: false, Text: "First item", MarkIndex: 3},
		{LineIndex: 2, Checked: true, Text: "Nested done item", MarkIndex: 5},
		{LineIndex: 3, Checked: true, Text: "Starred done item", MarkIndex: 3},
		{LineIndex: 4, Checked: false, Text: "Starred todo item", MarkIndex: 5},
	} {
		if items[i] != want {
			t.Fatalf("item[%d] = %+v, want %+v", i, items[i], want)
		}
	}

	// Toggle unchecked -> checked and back
	toggled, ok := ToggleBoardChecklistItem(desc, 1, "First item", false)
	if !ok || !strings.Contains(toggled, "\n- [x] First item\n") {
		t.Fatalf("expected line 1 checked, got ok=%v:\n%s", ok, toggled)
	}
	roundtrip, ok := ToggleBoardChecklistItem(toggled, 1, "First item", true)
	if !ok || roundtrip != desc {
		t.Fatalf("roundtrip mismatch, ok=%v:\ngot:\n%s\nwant:\n%s", ok, roundtrip, desc)
	}

	// Toggle indented starred item preserves indentation and bullet char
	toggledStar, ok := ToggleBoardChecklistItem(desc, 4, "Starred todo item", false)
	if !ok || !strings.Contains(toggledStar, "\n  * [x] Starred todo item\n") {
		t.Fatalf("expected indented star preserved, got ok=%v:\n%s", ok, toggledStar)
	}
	// A line without an item, or one outside the description, changes nothing
	if got, ok := ToggleBoardChecklistItem(desc, 0, "Background context line", false); ok || got != desc {
		t.Fatalf("non-checklist line should not change")
	}
	if got, ok := ToggleBoardChecklistItem(desc, 99, "Missing item", false); ok || got != desc {
		t.Fatalf("an item that is not there should not change the description")
	}
	// A line added above moves the index; the text still names the item.
	shifted, ok := ToggleBoardChecklistItem(desc, 99, "First item", false)
	if !ok || !strings.Contains(shifted, "\n- [x] First item\n") {
		t.Fatalf("a moved item was not found by its text, ok=%v:\n%s", ok, shifted)
	}
}

func TestBoardPinnedPersistence(t *testing.T) {
	withTempBoardConfig(t)
	_, created, err := BoardAddTask("置顶聚焦任务", "", BoardDoing, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.Pinned {
		t.Fatalf("new task should default to unpinned")
	}

	pin := true
	if _, updated, err := BoardUpdateTask(created.ID, BoardTaskPatch{Pinned: &pin}); err != nil || !updated.Pinned {
		t.Fatalf("pin task failed: updated=%+v err=%v", updated, err)
	}
	loaded, err := LoadBoardFile()
	if err != nil || len(loaded.Tasks) != 1 || !loaded.Tasks[0].Pinned {
		t.Fatalf("expected pinned persisted to disk: %+v err=%v", loaded.Tasks, err)
	}

	unpin := false
	if _, updated, err := BoardUpdateTask(created.ID, BoardTaskPatch{Pinned: &unpin}); err != nil || updated.Pinned {
		t.Fatalf("unpin task failed: updated=%+v err=%v", updated, err)
	}
	loaded, err = LoadBoardFile()
	if err != nil || len(loaded.Tasks) != 1 || loaded.Tasks[0].Pinned {
		t.Fatalf("expected unpinned persisted to disk: %+v err=%v", loaded.Tasks, err)
	}
}

func TestFormatBoardAgentContext(t *testing.T) {
	task := BoardTask{
		ID:          "T-42",
		Title:       "Refactor Context Wall",
		Status:      BoardDoing,
		Description: "- [x] Add data model\n- [ ] Build masonry canvas\n- [ ] Write UI tests",
		Repos:       []string{"/tmp/xylring"},
		Commits: []BoardCommitLink{
			{Repo: "/tmp/xylring", Hash: "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Subject: "oldest commit"},
			{Repo: "/tmp/xylring", Hash: "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Subject: "second commit", Agent: "Codex"},
			{Repo: "/tmp/xylring", Hash: "3333333ccccccccccccccccccccccccccccccccc", Subject: "latest commit", Agent: "Claude Code"},
		},
		Notes: []BoardNote{
			{ID: "N-1", Text: "Old note", Author: "human", CreatedAt: time.Now().Add(-time.Hour)},
			{ID: "N-2", Text: "Card layout ready, next add tests.", Author: "Codex", CreatedAt: time.Now()},
		},
	}

	out := FormatBoardAgentContext(task)
	wantRepo := filepath.Clean("/tmp/xylring")
	for _, want := range []string{
		"[T-42] Refactor Context Wall",
		wantRepo,
		"- [ ] Build masonry canvas",
		"- [ ] Write UI tests",
		"3333333 latest commit",
		"2222222 second commit",
		"Card layout ready, next add tests.",
		"xylring board update T-42",
		"xylring board note T-42",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatBoardAgentContext missing %q in:\n%s", want, out)
		}
	}
	// The whole description travels with the handoff, checklist included: the
	// background and acceptance text may sit around or inside it.
	if !strings.Contains(out, "- [x] Add data model") {
		t.Fatalf("context should keep the full description:\n%s", out)
	}
	if !strings.Contains(out, "Remaining Checklist:\n- [ ] Build masonry canvas") {
		t.Fatalf("context should list the unchecked items separately:\n%s", out)
	}
	if strings.Contains(out, "1111111") {
		t.Fatalf("expected at most latest 2 commits in context:\n%s", out)
	}

	// Description fallback when no checklist items exist
	plainTask := BoardTask{
		ID:          "T-43",
		Title:       "Plain description task",
		Description: "Investigate flaky test in CI pipeline.",
	}
	plainOut := FormatBoardAgentContext(plainTask)
	if !strings.Contains(plainOut, "Investigate flaky test in CI pipeline.") {
		t.Fatalf("expected description excerpt in plain context:\n%s", plainOut)
	}
}

func TestBoardChecklistIgnoresFencedExamples(t *testing.T) {
	desc := strings.Join([]string{
		"Background before the example",
		"```md",
		"- [ ] example only",
		"1. [x] ordered example",
		"```",
		"- [ ] real bullet",
		"+ [ ] plus bullet",
		"1. [ ] ordered item",
		"2) [x] parenthesized item",
		"~~~",
		"- [ ] tilde example",
		"~~~",
		"- [ ] after the fence",
		"- plain bullet",
		"- [",
		"- [ ]nospace",
	}, "\n")
	items := ParseBoardChecklist(desc)
	want := []struct {
		checked bool
		text    string
	}{
		{false, "real bullet"},
		{false, "plus bullet"},
		{false, "ordered item"},
		{true, "parenthesized item"},
		{false, "after the fence"},
	}
	if len(items) != len(want) {
		t.Fatalf("parsed %d items, want %d: %+v", len(items), len(want), items)
	}
	for i, expect := range want {
		if items[i].Checked != expect.checked || items[i].Text != expect.text {
			t.Fatalf("item[%d] = %+v, want checked=%v text=%q", i, items[i], expect.checked, expect.text)
		}
	}
	// An unclosed fence hides the rest, so a stray example cannot be toggled.
	if got := ParseBoardChecklist("```\n- [ ] inside\n"); len(got) != 0 {
		t.Fatalf("unclosed fence should hide examples, got %+v", got)
	}
}

func TestBoardChecklistToggleChangesOnlyTheMarker(t *testing.T) {
	desc := "Intro text\r\n- [ ] keep   spacing  \r\n  1. [x] ordered\r\nOutro\r\n"
	next, ok := ToggleBoardChecklistItem(desc, 1, "keep   spacing", false)
	if !ok {
		t.Fatal("toggle failed")
	}
	wantLine := "- [x] keep   spacing  \r"
	if !strings.Contains(next, wantLine) {
		t.Fatalf("marker toggle changed more than the marker: %q", next)
	}
	if strings.Count(next, "\r\n") != strings.Count(desc, "\r\n") {
		t.Fatalf("line endings changed: %q", next)
	}
	if strings.Replace(next, "keep   spacing", "keep   spacing", 1) == "" || !strings.HasSuffix(next, "Outro\r\n") {
		t.Fatalf("trailing text changed: %q", next)
	}
	back, ok := ToggleBoardChecklistItem(next, 1, "keep   spacing", true)
	if !ok || back != desc {
		t.Fatalf("round trip = %q, want %q", back, desc)
	}
	// A different state than the caller saw fails safely.
	if _, ok := ToggleBoardChecklistItem(desc, 1, "keep   spacing", true); ok {
		t.Fatal("toggling an item that had another state should fail")
	}
	if _, ok := ToggleBoardChecklistItem(desc, 1, "missing", false); ok {
		t.Fatal("toggling a missing item should fail")
	}
}

func TestBoardToggleChecklistTaskKeepsConcurrentEdits(t *testing.T) {
	withTempBoardConfig(t)
	_, task, err := BoardAddTask("Concurrent checklist", "- [ ] Alpha\n- [ ] Beta", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Another client adds a line before the items while the card is open.
	edited := "- [ ] Inserted by the CLI\n- [ ] Alpha\n- [ ] Beta"
	if _, _, err := BoardUpdateTask(task.ID, BoardTaskPatch{Description: &edited}); err != nil {
		t.Fatal(err)
	}
	// The user clicks Beta as it showed on the card: line 2 used to be Beta.
	bf, updated, err := BoardToggleChecklistTask(task.ID, 2, "Beta", false)
	if err != nil {
		t.Fatalf("toggle by identity failed: %v", err)
	}
	want := "- [ ] Inserted by the CLI\n- [ ] Alpha\n- [x] Beta"
	if got := bf.Tasks[0].Description; got != want {
		t.Fatalf("description = %q, want %q", got, want)
	}
	if updated.UpdatedAt.IsZero() {
		t.Fatal("toggle should touch the task")
	}
	// Another client renamed the item; the click fails instead of guessing.
	renamed := "- [ ] Inserted by the CLI\n- [ ] Alpha renamed\n- [ ] Beta"
	if _, _, err := BoardUpdateTask(task.ID, BoardTaskPatch{Description: &renamed}); err != nil {
		t.Fatal(err)
	}
	before, err := LoadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := BoardToggleChecklistTask(task.ID, 1, "Alpha", false); err == nil {
		t.Fatal("ambiguous or changed items should fail safely")
	}
	after, err := LoadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	if after.Tasks[0].Description != before.Tasks[0].Description {
		t.Fatalf("failed toggle wrote: %q", after.Tasks[0].Description)
	}
}

func TestBoardArchiveDoneTasksOnlyArchivesListedVisibleOnes(t *testing.T) {
	withTempBoardConfig(t)
	_, hidden, err := BoardAddTask("Hidden done", "", BoardDone, []string{"/tmp/hidden-repo"})
	if err != nil {
		t.Fatal(err)
	}
	_, visible, err := BoardAddTask("Visible done", "", BoardDone, []string{"/tmp/visible-repo"})
	if err != nil {
		t.Fatal(err)
	}
	_, moved, err := BoardAddTask("Moved on meanwhile", "", BoardDone, []string{"/tmp/visible-repo"})
	if err != nil {
		t.Fatal(err)
	}
	// Another client moved one task out of done after the list was built.
	doing := BoardDoing
	if _, _, err := BoardUpdateTask(moved.ID, BoardTaskPatch{Status: &doing}); err != nil {
		t.Fatal(err)
	}
	bf, count, err := BoardArchiveDoneTasks([]string{visible.ID, moved.ID})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("archived %d tasks, want 1", count)
	}
	byID := map[string]BoardTask{}
	for _, task := range bf.Tasks {
		byID[task.ID] = task
	}
	if !byID[visible.ID].Archived {
		t.Fatalf("listed done task was not archived: %+v", byID[visible.ID])
	}
	if byID[moved.ID].Archived {
		t.Fatalf("task that is no longer done was archived: %+v", byID[moved.ID])
	}
	if byID[hidden.ID].Archived {
		t.Fatalf("task outside the list was archived: %+v", byID[hidden.ID])
	}
	// The backup taken before archiving still exists.
	backups, err := ListBoardBackups()
	if err != nil || len(backups) == 0 {
		t.Fatalf("expected a backup before archiving, got %v err=%v", backups, err)
	}
}

func TestBoardContextKeepsRequirementsAndPicksLatestNote(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("背景与约束。", 200) + "\n最后一条验收条件"
	task := BoardTask{
		ID:          "T-7",
		Title:       "Long handoff",
		Description: long + "\n- [ ] Run validation",
		Commits: []BoardCommitLink{
			{Repo: "/tmp/a", Hash: "aaaaaaa1111111111111111111111111111111111", AddedAt: now.Add(-2 * time.Hour)},
			{Repo: "/tmp/a", Hash: "bbbbbbb2222222222222222222222222222222222", AddedAt: now.Add(-time.Minute)},
			{Repo: "/tmp/a", Hash: "ccccccc3333333333333333333333333333333333", AddedAt: now},
		},
		Notes: []BoardNote{
			{ID: "N-1", Text: "Edited most recently", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
			{ID: "N-2", Text: "Older by creation", CreatedAt: now.Add(-time.Minute)},
		},
	}
	clip := FormatBoardAgentContext(task)
	for _, want := range []string{"最后一条验收条件", "- [ ] Run validation", "Edited most recently", "- ccccccc ", "- bbbbbbb "} {
		if !strings.Contains(clip, want) {
			t.Fatalf("context is missing %q:\n%s", want, clip)
		}
	}
	if strings.Contains(clip, "- aaaaaaa ") {
		t.Fatalf("context should hold the two newest commits by link time:\n%s", clip)
	}
	// Notes saved without times keep their stored order.
	if latest, ok := LatestBoardNote([]BoardNote{{Text: "first"}, {Text: "second"}}); !ok || latest.Text != "second" {
		t.Fatalf("timeless notes picked %+v", latest)
	}
	if latest, ok := LatestBoardNote(nil); ok {
		t.Fatalf("no notes should not pick one: %+v", latest)
	}
	// The same holds for commit links written before times were saved.
	commits := BoardNewestCommits([]BoardCommitLink{
		{Hash: "first"}, {Hash: "second"}, {Hash: "third"},
	}, 2)
	if len(commits) != 2 || commits[0].Hash != "third" || commits[1].Hash != "second" {
		t.Fatalf("timeless commits picked %+v", commits)
	}
}

func TestBoardContextCLISamplesOnlyForStandardTaskIDs(t *testing.T) {
	for _, id := range []string{"OLD-TASK", "T-0", "T- 5", "001", "T-3\nT-4", "T-03", "T-", "T--1", "T-999999999999999999999"} {
		clip := FormatBoardAgentContext(BoardTask{ID: id, Title: "Imported task"})
		if strings.Contains(clip, "xylring board update ") || strings.Contains(clip, "xylring board note ") {
			t.Fatalf("nonstandard ID %q appears in executable CLI samples:\n%s", id, clip)
		}
	}
	for _, id := range []string{"T-1", "T-52", "T-1000"} {
		clip := FormatBoardAgentContext(BoardTask{ID: id, Title: "Valid task"})
		if !strings.Contains(clip, "xylring board update "+id) || !strings.Contains(clip, "xylring board note "+id) {
			t.Fatalf("standard ID %q lost its CLI samples:\n%s", id, clip)
		}
	}
}

func TestBoardMoveTaskPersistsManualOrderAndFallsBackToDefaultSort(t *testing.T) {
	withTempBoardConfig(t)
	_, first, err := BoardAddTask("First todo", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := BoardAddTask("Second todo", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, third, err := BoardAddTask("Third todo", "", BoardTodo, nil)
	if err != nil {
		t.Fatal(err)
	}
	// BoardAddTask puts new tasks first: third, second, first.
	if _, _, err := BoardMoveTask(first.ID, "", third.ID, false); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	loaded, err := LoadBoardFile()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, task := range loaded.Tasks {
		order = append(order, task.ID)
	}
	if want := []string{first.ID, third.ID, second.ID}; !slices.Equal(order, want) {
		t.Fatalf("saved order = %v, want %v", order, want)
	}
}

func initTestRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("config", "user.name", "Test")
	run("config", "user.email", "test@example.com")
	return dir, run
}

