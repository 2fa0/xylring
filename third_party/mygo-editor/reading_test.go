package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestReadingPositionSurvivesBufferRelease(t *testing.T) {
	for _, layout := range []DiffLayout{DiffSplit, DiffUnified} {
		t.Run(fmt.Sprint(layout), func(t *testing.T) {
			text := strings.Repeat("line changed\n", 200)
			d := &DiffState{Old: NewBuffer(""), New: NewBuffer(text), Layout: layout}
			tt := ui.NewTester(func(c *ui.Context) { DiffView(c, d).Fill() }, 800, 400)
			tt.Scroll(600, 200, 0, 600)
			tt.Frame()
			position := d.ReadingPosition()
			if position.y <= 0 {
				t.Fatal("fixture did not scroll")
			}
			d = &DiffState{Old: NewBuffer(""), New: NewBuffer(text), Layout: layout}
			d.RestoreReadingPosition(position)
			tt.Frame()
			restored := d.ReadingPosition()
			if restored.y != position.y {
				t.Fatalf("lost viewport: %v -> %v", position.y, restored.y)
			}
		})
	}
}
