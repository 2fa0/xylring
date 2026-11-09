package main

import (
	"os"
	"path/filepath"
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
	if info.Mode().Perm()&0077 != 0 {
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
