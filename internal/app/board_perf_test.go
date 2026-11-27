package app

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func boardWallFixture(n int) []boardTask {
	tasks := make([]boardTask, n)
	for i := range tasks {
		tasks[i] = boardTask{
			ID:          fmt.Sprintf("T-%d", i+1),
			Title:       fmt.Sprintf("Task %d", i+1),
			Status:      boardTodo,
			Description: "- [ ] First validation\n- [x] Second validation\n- [ ] Third validation",
		}
	}
	return tasks
}

func boardWallBuiltCards(tt *ui.Tester) int {
	built := 0
	for _, part := range tt.AccessAll() {
		if part.Role == ui.RoleGroup && strings.HasPrefix(part.Name, "T-") {
			built++
		}
	}
	return built
}

// TestBoardWallBuildsOnlyCardsNearTheViewport keeps the wall a windowed build:
// a board of hundreds of tasks must not create every card each frame, while
// scrolling still reaches the far end.
func TestBoardWallBuildsOnlyCardsNearTheViewport(t *testing.T) {
	tasks := boardWallFixture(500)
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if a.board.wall.total != len(tasks) {
		t.Fatalf("wall holds %d tasks, want %d", a.board.wall.total, len(tasks))
	}
	if built := boardWallBuiltCards(tt); built == 0 || built > 60 {
		t.Fatalf("built %d of %d cards; the wall should build only the window", built, len(tasks))
	}
	// Scrolling to the end still builds the last cards.
	a.board.wallScroll.Y = math.MaxFloat32
	tt.Frame()
	tt.Frame()
	if !tt.HasText("Task 500") {
		t.Fatalf("the far end of the wall is unreachable, texts=%q", tt.Texts())
	}
}

func TestBoardWallKeepsPinnedAndDoneReachable(t *testing.T) {
	tasks := boardWallFixture(300)
	tasks[0].Pinned = true
	tasks[0].Status = boardDoing
	tasks[1].Status = boardDone
	tasks[1].Title = "Completed finish line"
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1280, 900)
	defer retireTester(tt)
	tt.Frame()
	tt.Frame()
	if !tt.HasText(tasks[0].Title) {
		t.Fatalf("pinned card is not built, texts=%q", tt.Texts())
	}
	a.board.wallScroll.Y = math.MaxFloat32
	tt.Frame()
	tt.Frame()
	if err := tt.Click("已完成"); err != nil {
		t.Fatalf("completed header not reachable at the end of the wall: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Completed finish line") {
		t.Fatalf("completed rows are not reachable at the end of the wall")
	}
}

func TestBoardWallKeepsAFocusedCardBuilt(t *testing.T) {
	tasks := boardWallFixture(300)
	a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tasks}}}
	tt := ui.NewTester(a.view, 1280, 820)
	defer retireTester(tt)
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("Task 5"); !ok {
		t.Fatal("Task 5 is not built at the top")
	}
	a.boardWallCardHandle("T-5").Focus()
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Task 5") {
		t.Fatalf("the card did not take focus, texts=%q", tt.Texts())
	}
	// Scrolling far away keeps the focused card built, without building all.
	a.board.wallScroll.Y = math.MaxFloat32
	tt.Frame()
	tt.Frame()
	if !tt.Focused("Task 5") {
		t.Fatal("scrolling away dropped the focused card")
	}
	if built := boardWallBuiltCards(tt); built > 70 {
		t.Fatalf("focused-card frame built %d cards, the window grew", built)
	}
}

// BenchmarkBoardWallFrame measures a frame of a settled board: the first frames
// build and measure the cards, the loop measures the windowed steady state.
func BenchmarkBoardWallFrame(b *testing.B) {
	for _, count := range []int{50, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			tasks := boardWallFixture(count)
			a := &app{showBoard: true, board: boardState{loaded: true, file: boardFile{Tasks: tasks}}}
			tt := ui.NewTester(a.view, 1280, 820)
			defer retireTester(tt)
			for i := 0; i < 4; i++ {
				tt.Frame()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}
