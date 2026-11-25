package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSessionPersistsWorkspaceAndDraft(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	tab := newTab(1, "/tmp/repo")
	tab.commitMsg = "unfinished draft"
	tab.commitAmend = true
	tab.inspecting = hashN(1)
	tab.compareFiles = []fileChange{{path: "file.go"}}
	tab.fileSel = 0
	a := &app{tabs: []*repoTab{tab}, persist: true, sessionReady: true}
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	s := loadSession()
	if len(s.Repos) != 1 || s.Active != "/tmp/repo" || s.Repos[0].Draft != "unfinished draft" || s.Repos[0].File != "file.go" || s.Repos[0].Commit != hashHex(hashN(1)) || !s.Repos[0].Amend {
		t.Fatalf("%+v", s)
	}
	info, e := os.Stat(filepath.Join(config, "Xylring", "session.json"))
	if e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("session contains drafts and must remain private")
	}
}

func TestSessionPersistsBottomFullWidth(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	tab := newTab(1, "/tmp/repo")
	a := newApp()
	a.tabs = []*repoTab{tab}
	a.persist = true
	a.sessionReady = true
	a.bottomFullWidth = false
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	s := loadSession()
	if s.BottomFullWidth == nil || *s.BottomFullWidth != false {
		t.Fatalf("expected BottomFullWidth=false in saved session, got %+v", s.BottomFullWidth)
	}
}

func TestSessionPersistsRepoOrder(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	tab := newTab(1, "/tmp/repo")
	a := newApp()
	a.tabs = []*repoTab{tab}
	a.persist = true
	a.sessionReady = true
	a.repoOrder = []string{"/tmp/repo2", "/tmp/repo"}
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone
	s := loadSession()
	if len(s.RepoOrder) != 2 || s.RepoOrder[0] != "/tmp/repo2" || s.RepoOrder[1] != "/tmp/repo" {
		t.Fatalf("expected RepoOrder in saved session, got %+v", s.RepoOrder)
	}
}

func TestConfigDirMigratesFromLegacy(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()
	legacy := filepath.Join(config, "GitClientPrototype")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(legacy, "recent.json")
	if err := os.WriteFile(marker, []byte("[\"/tmp/repo\"]"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := configFile("recent.json")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(config, "Xylring", "recent.json")
	if path != want {
		t.Fatalf("path=%q want=%q", path, want)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present: %v", err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDirMigratesMissingFilesWhenNewDirExists(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	legacy := filepath.Join(config, "GitClientPrototype")
	cur := filepath.Join(config, "Xylring")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cur, 0o700); err != nil {
		t.Fatal(err)
	}
	// Existing file in new dir must NOT be overwritten.
	if err := os.WriteFile(filepath.Join(legacy, "recent.json"), []byte(`["/old"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cur, "recent.json"), []byte(`["/new"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Missing file in new dir MUST be migrated from legacy.
	if err := os.WriteFile(filepath.Join(legacy, "session.json"), []byte(`{"theme_mode":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	gotSession, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("expected migrated session.json: %v", err)
	}
	if string(gotSession) != `{"theme_mode":"dark"}` {
		t.Fatalf("migrated session.json = %q", string(gotSession))
	}
	gotRecent, err := os.ReadFile(filepath.Join(cur, "recent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotRecent) != `["/new"]` {
		t.Fatalf("existing recent.json overwritten: %q", string(gotRecent))
	}
}

func TestRestoreDisabledPreservesSavedReposAndDrafts(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	initial := savedSession{
		Preferences:    &appPreferences{RestoreDisabled: true},
		RingSkipNotice: true,
		RepoOrder:      []string{"/tmp/a", "/tmp/b"},
		Repos: []savedRepo{
			{Path: "/tmp/a", Draft: "keep this draft", Amend: true},
		},
		Active:    "/tmp/a",
		Found:     []string{"/tmp/a"},
		FoundFrom: "/tmp",
	}
	if err := writeJSON(p, initial); err != nil {
		t.Fatal(err)
	}

	a := newApp()
	a.restoreSessionWithRepos(true) // restoreDisabled=true -> should NOT auto-open tabs, but MUST preserve drafts
	if len(a.tabs) != 0 {
		t.Fatalf("expected no auto-opened tabs when restoreDisabled=true, got %d", len(a.tabs))
	}
	a.themeMode = "dark"
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone

	after := loadSession()
	if len(after.Repos) != 1 || after.Repos[0].Path != "/tmp/a" || after.Repos[0].Draft != "keep this draft" {
		t.Fatalf("saved repos/drafts lost after save with restoreDisabled=true: %+v", after.Repos)
	}
	if !after.RingSkipNotice || len(after.RepoOrder) != 2 || after.Active != "/tmp/a" {
		t.Fatalf("shared session fields lost: %+v", after)
	}
}

func TestLaunchWithCLIPathLoadsSharedStateAndPreservesOtherDrafts(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	initial := savedSession{
		RingSkipNotice: true,
		RepoOrder:      []string{"/tmp/a", "/tmp/b"},
		Repos: []savedRepo{
			{Path: "/tmp/a", Draft: "draft A"},
			{Path: "/tmp/b", Draft: "draft B"},
		},
		Active: "/tmp/a",
	}
	if err := writeJSON(p, initial); err != nil {
		t.Fatal(err)
	}

	a := newApp()
	a.restoreSessionWithRepos(false) // simulates launch with CLI repo path
	if !a.ringSkipNotice || len(a.repoOrder) != 2 {
		t.Fatalf("shared state not loaded on CLI launch: ringSkipNotice=%v repoOrder=%v", a.ringSkipNotice, a.repoOrder)
	}
	// Simulate opening /tmp/b via CLI; /tmp/a draft should remain preserved.
	tabB := newTab(1, "/tmp/b")
	if saved, ok := a.consumePreservedRepo("/tmp/b"); ok {
		tabB.commitMsg = saved.Draft
	}
	if tabB.commitMsg != "draft B" {
		t.Fatalf("expected restored draft B, got %q", tabB.commitMsg)
	}
	a.tabs = []*repoTab{tabB}
	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone

	after := loadSession()
	if len(after.Repos) != 2 {
		t.Fatalf("expected both repos preserved, got %+v", after.Repos)
	}
}

func TestAsyncRestorePreservesUnreachableRepoDrafts(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	goodRepo, _ := initTestRepo(t)
	missingRepo := filepath.Join(t.TempDir(), "offline-drive-repo")
	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	initial := savedSession{
		Repos: []savedRepo{
			{Path: goodRepo, Draft: "draft good"},
			{Path: missingRepo, Draft: "draft offline", Amend: true},
		},
		Active: goodRepo,
	}
	if err := writeJSON(p, initial); err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 64)
	a := newApp()
	a.persist = true
	a.dispatch = func(fn func()) { updates <- fn }
	a.restoreSessionWithRepos(true)
	waitUpdates(t, updates, func() bool { return a.sessionReady })
	for _, tab := range a.tabs {
		defer tab.cancel()
	}

	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone

	after := loadSession()
	if len(after.Repos) != 2 {
		t.Fatalf("expected unreachable repo draft to be preserved, got %+v", after.Repos)
	}
	var gotOffline savedRepo
	for _, r := range after.Repos {
		if r.Path == missingRepo {
			gotOffline = r
		}
	}
	if gotOffline.Draft != "draft offline" || !gotOffline.Amend {
		t.Fatalf("offline repo draft lost: %+v", gotOffline)
	}
}

func TestAsyncRestoreMergesWithoutOverwritingUserDraft(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	repoA, _ := initTestRepo(t)
	repoB, _ := initTestRepo(t)
	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	initial := savedSession{
		Repos: []savedRepo{
			{Path: repoA, Draft: "saved draft A", Amend: true},
			{Path: repoB, Draft: "saved draft B"},
		},
		Active: repoA,
	}
	if err := writeJSON(p, initial); err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 64)
	a := newApp()
	a.persist = true
	a.dispatch = func(fn func()) { updates <- fn }
	a.restoreSessionWithRepos(true)

	// User opens repoA while async restore is still probing, and edits its draft.
	a.openRoot(repoA)
	tabA := a.tabByPath(repoA)
	if tabA == nil {
		t.Fatal("expected repoA tab")
	}
	defer tabA.cancel()
	if tabA.commitMsg != "saved draft A" || !tabA.commitAmend {
		t.Fatalf("expected immediate draft merge on openRoot, got msg=%q amend=%v", tabA.commitMsg, tabA.commitAmend)
	}
	tabA.commitMsg = "user edited draft A"

	waitUpdates(t, updates, func() bool { return a.sessionReady })
	if tabA.commitMsg != "user edited draft A" {
		t.Fatalf("async restore overwrote user draft: got %q", tabA.commitMsg)
	}

	a.queueSession()
	close(a.sessionWrites)
	<-a.sessionDone

	after := loadSession()
	if len(after.Repos) != 2 {
		t.Fatalf("expected both repos saved, got %+v", after.Repos)
	}
	byPath := map[string]savedRepo{}
	for _, r := range after.Repos {
		byPath[r.Path] = r
	}
	if byPath[repoA].Draft != "user edited draft A" {
		t.Fatalf("expected user draft for repoA, got %+v", byPath[repoA])
	}
	if byPath[repoB].Draft != "saved draft B" {
		t.Fatalf("expected preserved draft for repoB, got %+v", byPath[repoB])
	}
}

func TestFlushSessionOnCloseBeforeAsyncRestoreCompletes(t *testing.T) {
	config := t.TempDir()
	old := userConfigDir
	userConfigDir = func() (string, error) { return config, nil }
	defer func() { userConfigDir = old }()

	repoA, _ := initTestRepo(t)
	repoB, _ := initTestRepo(t)
	repoC, _ := initTestRepo(t)
	p, err := configFile("session.json")
	if err != nil {
		t.Fatal(err)
	}
	initial := savedSession{
		Repos: []savedRepo{
			{Path: repoA, Draft: "saved draft A", Amend: true},
			{Path: repoB, Draft: "saved draft B"},
		},
		Active: repoB,
	}
	if err := writeJSON(p, initial); err != nil {
		t.Fatal(err)
	}

	updates := make(chan func(), 64)
	a := newApp()
	a.persist = true
	a.dispatch = func(fn func()) { updates <- fn }
	a.restoreSessionWithRepos(true)
	if a.sessionReady {
		t.Fatal("expected async restore to still be pending")
	}

	// User opens repoA and repoC and edits drafts BEFORE async probing finishes, then closes window.
	a.openRoot(repoA)
	a.openRoot(repoC)
	tabA := a.tabByPath(repoA)
	tabC := a.tabByPath(repoC)
	if tabA == nil || tabC == nil {
		t.Fatal("expected opened tabs")
	}
	defer tabA.cancel()
	defer tabC.cancel()
	tabA.commitMsg = "edited draft A before restore"
	tabC.commitMsg = "brand new draft C"

	// Close before sessionReady becomes true.
	a.flushSessionOnClose()
	a.closed = true
	if a.sessionDone == nil {
		t.Fatal("expected session writer to start and flush on close")
	}
	<-a.sessionDone

	after := loadSession()
	if len(after.Repos) != 3 {
		t.Fatalf("expected all 3 repos saved (2 opened + 1 preserved), got %+v", after.Repos)
	}
	byPath := map[string]savedRepo{}
	for _, r := range after.Repos {
		byPath[r.Path] = r
	}
	if byPath[repoA].Draft != "edited draft A before restore" || !byPath[repoA].Amend {
		t.Fatalf("repoA draft/amend lost on early close: %+v", byPath[repoA])
	}
	if byPath[repoB].Draft != "saved draft B" {
		t.Fatalf("preserved repoB draft lost on early close: %+v", byPath[repoB])
	}
	if byPath[repoC].Draft != "brand new draft C" {
		t.Fatalf("new repoC draft lost on early close: %+v", byPath[repoC])
	}
}
