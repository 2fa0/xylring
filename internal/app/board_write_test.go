package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// boardEncodingSample is a board with every field the writer encodes, so the
// encoding test covers links, notes, repos and timestamps.
func boardEncodingSample() boardFile {
	bf := boardFile{Version: 1, NextSeq: 3}
	bf.Tasks = []boardTask{{
		ID:          "T-1",
		Title:       "让看板写入不再重排整份文件",
		Description: "保留可读的缩进格式，同时压掉一次写入的临时分配。",
		Status:      boardDoing,
		Priority:    boardPriorityImportant,
		DueDate:     "2026-12-01",
		Repos:       []string{"/tmp/repos/xylring", "/tmp/repos/snapover"},
		Commits: []boardCommitLink{{
			Repo:    "/tmp/repos/xylring",
			Hash:    strings.Repeat("a", 40),
			Subject: "perf(board): 写入改用复用缓冲",
			Branch:  "main",
			Agent:   "Cindy",
			Model:   "test-model",
			AddedBy: "human",
		}},
		Notes: []boardNote{{ID: "T-1-N-1", Text: "写完对比一次分配。", Author: "human"}},
	}, {
		ID:       "T-2",
		Title:    "归档已完成的看板任务",
		Status:   boardDone,
		Archived: true,
	}}
	return bf
}

func TestBoardEncodeMatchesIndentedJSON(t *testing.T) {
	boards := map[string]boardFile{
		"with tasks": boardEncodingSample(),
		"empty":      {Version: 1, NextSeq: 1, Tasks: []boardTask{}},
		"no tasks":   {Version: 1, NextSeq: 1},
	}
	for name, bf := range boards {
		want, err := json.MarshalIndent(bf, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')

		var got bytes.Buffer
		if err := renderBoardFile(&got, bf); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("%s: streamed board differs from MarshalIndent output:\n got=%q\nwant=%q", name, got.Bytes(), want)
		}

		var back boardFile
		if err := json.Unmarshal(got.Bytes(), &back); err != nil {
			t.Fatalf("%s: encoding must stay readable as a board file: %v", name, err)
		}
		if len(back.Tasks) != len(bf.Tasks) {
			t.Fatalf("%s: round trip lost tasks: %+v", name, back.Tasks)
		}
		if len(bf.Tasks) > 0 && back.Tasks[0].Title != bf.Tasks[0].Title {
			t.Fatalf("%s: round trip lost data: %+v", name, back.Tasks[0])
		}
	}
}

// TestBoardWriteAllocationStaysSmall guards the write path's cost: a board file
// written whole used to allocate about eight times the file with
// json.MarshalIndent. Reused buffers keep a steady stream of edits cheap.
func TestBoardWriteAllocationStaysSmall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.json")
	bf := boardWriteAllocationSample(300)

	if err := writeBoardFileAtomic(path, bf); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const writes = 5
	for i := 0; i < writes; i++ {
		if err := writeBoardFileAtomic(path, bf); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)

	perWrite := (after.TotalAlloc - before.TotalAlloc) / writes
	const budget = 2 << 20
	if perWrite > budget {
		t.Fatalf("写入 %.1f MiB 文件每次分配 %.1f MiB，超出 %.1f MiB 预算",
			float64(info.Size())/(1<<20), float64(perWrite)/(1<<20), float64(budget)/(1<<20))
	}

	got, err := readBoardFileAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tasks) != len(bf.Tasks) || got.Tasks[len(got.Tasks)-1].ID != bf.Tasks[len(bf.Tasks)-1].ID {
		t.Fatalf("written board does not read back: %d tasks", len(got.Tasks))
	}
}

func boardWriteAllocationSample(n int) boardFile {
	bf := boardFile{Version: 1, NextSeq: n + 1}
	repos := []string{"/tmp/repos/xylring", "/tmp/repos/snapover", "/tmp/repos/dashboard"}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("T-%d", i+1)
		task := boardTask{
			ID:          id,
			Title:       fmt.Sprintf("第 %d 个任务：把看板卡片的提交归属显示完整", i+1),
			Description: strings.Repeat("任务描述用来占住真实看板的体积。", 6),
			Status:      boardStatuses[i%len(boardStatuses)],
			Repos:       []string{repos[i%len(repos)]},
		}
		for c := 0; c < 2; c++ {
			task.Commits = append(task.Commits, boardCommitLink{
				Repo:    repos[c%len(repos)],
				Hash:    strings.Repeat("b", 39) + fmt.Sprint(c),
				Subject: "feat(board): 一条长度接近真实的提交标题",
				Branch:  "main",
			})
		}
		task.Notes = append(task.Notes, boardNote{ID: id + "-N-1", Text: strings.Repeat("进度备注。", 8), Author: "human"})
		bf.Tasks = append(bf.Tasks, task)
	}
	return bf
}
