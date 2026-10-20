package main

import (
	"context"
	"testing"
)

func TestRestoreSelectionBeyondFirstPage(t *testing.T) {
	dir, _ := initTestRepo(t)
	importTestHistory(t, dir, 650)
	tab := newTab(1, dir)
	err := streamCommits(context.Background(), dir, 0, 600, func(c commit) error {
		tab.commits = append(tab.commits, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tab.selected = 549
	want := tab.selectedCommit().hash
	tab.releaseHeavy()
	if tab.commits != nil || tab.refs != nil {
		t.Fatal("background tab retains history")
	}
	// reload captures again after release; the saved selection and extent must survive.
	tab.captureSelection()
	err = streamCommits(context.Background(), dir, 0, tab.historyCount(), func(c commit) error {
		tab.commits = append(tab.commits, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tab.reselect()
	if got := tab.selectedCommit(); got == nil || got.hash != want || tab.selected != 549 {
		t.Fatalf("restored selection=%d commit=%v", tab.selected, got)
	}
}

func TestReleaseCancelsAllReads(t *testing.T) {
	tab := newTab(1, "/tmp/repo")
	var contexts []context.Context
	for _, slot := range []*context.CancelFunc{&tab.historyCancel, &tab.refsCancel, &tab.detailCancel, &tab.diffCancel, &tab.navCancel} {
		ctx, cancel := context.WithCancel(context.Background())
		*slot = cancel
		contexts = append(contexts, ctx)
	}
	tab.releaseHeavy()
	for _, ctx := range contexts {
		if ctx.Err() != context.Canceled {
			t.Fatal("background task was not canceled")
		}
	}
	tab.releaseHeavy()
}

func TestRestoreUncommittedSelection(t *testing.T) {
	tab := newTab(1, "/tmp/repo")
	tab.dirty = 1
	tab.selected = 0
	tab.releaseHeavy()
	tab.captureSelection()
	tab.reselect()
	if tab.selected != 0 || tab.selectionKey() != "u" {
		t.Fatal("uncommitted selection was lost")
	}
}
