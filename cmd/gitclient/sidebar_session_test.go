package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestScansPreserveOtherImportedRepos(t *testing.T) {
	updates := make(chan func(), 64)
	a := &app{dispatch: func(fn func()) { updates <- fn }}
	src := []string{"/work/src", "/work/src/api", "/work/src/front", "/work/src/tools", "/work/src/docs"}
	a.setFound("/work/src", src)
	a.setFound("/work/gateway", []string{"/work/gateway", "/work/gateway/api"})
	for _, path := range src {
		if !slices.Contains(a.repoItems(), path) {
			t.Fatalf("importing gateway lost %s: %v", path, a.repoItems())
		}
	}
	// Refreshing src removes deleted children without losing the other import.
	a.setFound("/work/src", src[:2])
	want := []string{"/work/src", "/work/src/api", "/work/gateway", "/work/gateway/api"}
	if !slices.Equal(a.found, want) {
		t.Fatalf("found=%v want=%v", a.found, want)
	}
	a.setFound("/work/src", src[:2])
	if !slices.Equal(a.found, want) {
		t.Fatalf("repeated scan changed list: %v", a.found)
	}
	// Keep even a single unopened repository from a different import.
	a.setFound("/work/gateway", nil)
	a.setFound("/work/src/api", nil)
	if !slices.Equal(a.found, src[:1]) {
		t.Fatalf("remaining repository lost: %v", a.found)
	}
}

func TestSessionRestoresUnopenedSidebarRepos(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{root}
	for _, name := range []string{"api", "front", "tools", "docs"} {
		paths = append(paths, filepath.Join(root, name))
	}
	for _, path := range paths {
		initGitDir(t, path)
	}
	tab := newTab(1, paths[2])
	tab.loaded = true
	a := &app{tabs: []*repoTab{tab}, found: slices.Clone(paths), foundFrom: root, persist: true, sessionReady: true}
	a.queueSession()
	// Sidebar-only changes must save too, with independent queued snapshots.
	a.found[4] = filepath.Join(root, "gone")
	if !slices.Equal(a.lastSession.Found, paths) {
		t.Fatal("queued session aliases the mutable sidebar")
	}
	a.queueSession()
	if a.lastSession.Found[4] != a.found[4] {
		t.Fatal("sidebar-only change was not saved")
	}
	a.found = slices.Clone(paths)
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	s := loadSession()
	if !slices.Equal(s.Found, paths) || s.FoundFrom != root {
		t.Fatalf("saved sidebar=%v from=%q", s.Found, s.FoundFrom)
	}
	updates := make(chan func(), 64)
	restored := &app{dispatch: func(fn func()) { updates <- fn }}
	defer func() {
		restored.closed = true
		for _, tab := range restored.tabs {
			tab.closed = true
			tab.cancel()
		}
	}()
	restored.restoreSession()
	deadline := time.After(5 * time.Second)
	for !restored.sessionReady {
		select {
		case fn := <-updates:
			fn()
		case <-deadline:
			t.Fatal("session restore timed out")
		}
	}
	if !slices.Equal(restored.repoItems(), paths) || restored.foundFrom != root {
		t.Fatalf("restored sidebar=%v from=%q", restored.repoItems(), restored.foundFrom)
	}
	if len(restored.tabs) != 1 || restored.activeTab().path != paths[2] {
		t.Fatal("unopened repositories should stay unopened")
	}
}
