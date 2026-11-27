package app

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"os"
	"slices"
	"strings"
	"time"
)

type savedRepo struct {
	Path             string
	Draft            string
	Amend            bool
	Commit           string
	File             string
	DockerName       string          `json:"docker_name,omitempty"`
	Provenance       savedProvenance `json:"provenance,omitempty"`
	ProvenanceEdited bool            `json:"provenance_edited,omitempty"`
}
type savedProvenance struct {
	Agent, Model string
	Attribution  string `json:"Attribution,omitempty"`
}
type savedSession struct {
	Preferences      *appPreferences   `json:"preferences,omitempty"`
	RepoAutoFetch    map[string]string `json:"repo_auto_fetch,omitempty"`
	RepoColors       map[string]string `json:"repo_colors,omitempty"`
	ThemeMode        string            `json:"theme_mode,omitempty"`
	RingSkipNotice   bool              `json:"ring_skip_notice,omitempty"`
	RecentProvenance []savedProvenance `json:"recent_provenance,omitempty"`
	Repos            []savedRepo
	Active           string
	BottomFullWidth  *bool    `json:"bottom_full_width,omitempty"`
	RepoOrder        []string `json:"repo_order,omitempty"`
	Found            []string `json:"found,omitempty"`
	FoundFrom        string   `json:"found_from,omitempty"`
	BoardSort        string   `json:"board_sort,omitempty"`
}

// restoredRepoUsable decides whether a saved repository stays in the session.
// A repository that only failed because Git itself is missing or broken must
// stay: its draft and tab belong to the user, and dropping it would also drop
// them from the session file written on the next save.
func restoredRepoUsable(root, want string, err error) bool {
	// 容器仓库离线、停止或被删除时仍保留标签和草稿：重开标签会显示真实原因，
	// 用户启动容器后可以重试。同名新容器不会被自动当成旧仓库。
	if isDockerRepoPath(want) {
		return true
	}
	if isGitUnavailable(err) {
		return true
	}
	return err == nil && root == want
}

func loadSession() savedSession {
	var s savedSession
	p, e := configFile("session.json")
	if e != nil {
		return s
	}
	b, e := os.ReadFile(p)
	if e == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}
func (a *app) queueSession() {
	a.queueSessionWithPending(false)
}

func (a *app) flushSessionOnClose() {
	a.queueSessionWithPending(true)
	if a.sessionWrites != nil {
		close(a.sessionWrites)
		a.sessionWrites = nil
	}
}

func (a *app) queueSessionWithPending(allowPendingRestore bool) {
	if !a.persist || a.closed {
		return
	}
	if !a.sessionReady && !(allowPendingRestore && a.sessionLoaded) {
		return
	}
	select {
	case err := <-a.sessionResults:
		if err != nil {
			a.setBanner("保存会话失败，将自动重试："+err.Error(), "")
		} else if strings.HasPrefix(a.banner, "保存会话失败，将自动重试：") {
			a.setBanner("", "")
		}
	default:
	}
	// Compare the persisted fields directly; unchanged frames allocate no JSON.
	prefs := a.preferences()
	bw := a.bottomFullWidth
	s := savedSession{
		Preferences:     &prefs,
		RepoAutoFetch:   a.repoAutoFetch,
		RepoColors:      a.repoColors,
		ThemeMode:       a.themeMode,
		RingSkipNotice:  a.ringSkipNotice,
		Repos:           make([]savedRepo, 0, len(a.tabs)),
		BottomFullWidth: &bw,
		RepoOrder:       a.repoOrder,
		Found:           a.found,
		FoundFrom:       a.foundFrom,
		BoardSort:       string(a.board.sortMode),
	}
	for _, p := range a.recentProvenance {
		s.RecentProvenance = append(s.RecentProvenance, savedProvenance{Agent: p.Agent, Model: p.Model})
	}
	livePaths := make(map[string]bool, len(a.tabs))
	for _, t := range a.tabs {
		livePaths[t.path] = true
		f := ""
		if t.fileSel >= 0 && t.fileSel < len(t.compareFiles) {
			f = t.compareFiles[t.fileSel].path
		}
		commit := ""
		if t.inspecting != zeroHash && t.inspecting != worktreeHash {
			commit = hashHex(t.inspecting)
		}
		if t.restore != nil {
			commit, f = "", t.restore.file
			if t.restore.hash != zeroHash && t.restore.hash != worktreeHash {
				commit = hashHex(t.restore.hash)
			}
		}
		if t.pendingSession != nil {
			commit = t.pendingSession.Commit
			f = t.pendingSession.File
		}
		name := ""
		if isDockerRepoPath(t.path) {
			name = repoTitle(t.path)
		}
		s.Repos = append(s.Repos, savedRepo{
			Path: t.path, Draft: t.commitMsg, Amend: t.commitAmend, Commit: commit, File: f, DockerName: name,
			Provenance:       savedProvenance{Agent: t.commitProv.Agent, Model: t.commitProv.Model, Attribution: t.commitProv.Attribution},
			ProvenanceEdited: t.commitProvEdited,
		})
	}
	for _, r := range a.preservedRepos {
		if !livePaths[r.Path] {
			s.Repos = append(s.Repos, r)
		}
	}
	if t := a.activeTab(); t != nil {
		s.Active = t.path
	} else if a.preservedActive != "" {
		s.Active = a.preservedActive
	}
	if len(s.Found) == 0 && s.FoundFrom == "" && (len(a.preservedFound) > 0 || a.preservedFoundFrom != "") {
		s.Found = a.preservedFound
		s.FoundFrom = a.preservedFoundFrom
	}
	bwEqual := (s.BottomFullWidth == nil && a.lastSession != nil && a.lastSession.BottomFullWidth == nil) ||
		(s.BottomFullWidth != nil && a.lastSession != nil && a.lastSession.BottomFullWidth != nil && *s.BottomFullWidth == *a.lastSession.BottomFullWidth)
	repoOrderEqual := (a.lastSession == nil && len(s.RepoOrder) == 0) ||
		(a.lastSession != nil && slices.Equal(s.RepoOrder, a.lastSession.RepoOrder))
	prefsEqual := a.lastSession != nil && a.lastSession.Preferences != nil && *a.lastSession.Preferences == prefs
	if prefsEqual && maps.Equal(s.RepoAutoFetch, a.lastSession.RepoAutoFetch) && maps.Equal(s.RepoColors, a.lastSession.RepoColors) && s.ThemeMode == a.lastSession.ThemeMode && s.Active == a.lastSession.Active && s.RingSkipNotice == a.lastSession.RingSkipNotice && slices.Equal(s.RecentProvenance, a.lastSession.RecentProvenance) && slices.Equal(s.Repos, a.lastSession.Repos) && bwEqual && repoOrderEqual && s.FoundFrom == a.lastSession.FoundFrom && slices.Equal(s.Found, a.lastSession.Found) && s.BoardSort == a.lastSession.BoardSort {
		return
	}
	if a.sessionWrites == nil {
		p, err := configFile("session.json")
		if err != nil {
			a.setBanner("保存会话失败："+err.Error(), "")
			return
		}
		a.sessionWrites = make(chan savedSession, 1)
		a.sessionDone = make(chan struct{})
		a.sessionResults = make(chan error, 1)
		ch, done, results := a.sessionWrites, a.sessionDone, a.sessionResults
		go func() {
			defer close(done)
			writeSessionQueue(ch, results, func(s savedSession) error { return writeJSON(p, s) })
		}()
	}
	s.RepoAutoFetch = maps.Clone(s.RepoAutoFetch)
	s.RepoColors = maps.Clone(s.RepoColors)
	s.RepoOrder = slices.Clone(s.RepoOrder)
	s.Found = slices.Clone(s.Found)
	a.lastSession = &s
	select {
	case <-a.sessionWrites:
	default:
	}
	a.sessionWrites <- s
}

// Coalesce edits, retry failed writes, and flush the newest snapshot on shutdown.
func writeSessionQueue(ch <-chan savedSession, results chan error, write func(savedSession) error) {
	var pending *savedSession
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var tick <-chan time.Time
	save := func() bool {
		err := write(*pending)
		if err != nil {
			log.Printf("save session: %v", err)
		}
		select {
		case <-results:
		default:
		}
		select {
		case results <- err:
		default:
		}
		return err == nil
	}
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				if pending != nil {
					save()
				}
				return
			}
			pending = &s
			timer.Reset(250 * time.Millisecond)
			tick = timer.C
		case <-tick:
			if save() {
				pending, tick = nil, nil
			} else {
				timer.Reset(time.Second)
			}
		}
	}
}

func (a *app) restoreSession() {
	a.restoreSessionWithRepos(true)
}

func (a *app) applySharedSessionState(s savedSession) {
	a.ringSkipNotice = s.RingSkipNotice
	if len(a.recentProvenance) == 0 {
		for _, p := range s.RecentProvenance {
			if len(a.recentProvenance) >= 8 {
				break
			}
			a.recentProvenance = append(a.recentProvenance, provenance{Agent: p.Agent, Model: p.Model})
		}
	}
	if len(a.repoOrder) == 0 && len(s.RepoOrder) > 0 {
		a.repoOrder = slices.Clone(s.RepoOrder)
	}
	if a.board.sortMode == "" && s.BoardSort != "" {
		a.board.sortMode = parseBoardSortMode(s.BoardSort)
	}
}

func (a *app) preserveSessionRepos(s savedSession) {
	a.preservedRepos = slices.Clone(s.Repos)
	a.preservedActive = s.Active
	a.preservedFound = slices.Clone(s.Found)
	a.preservedFoundFrom = s.FoundFrom
	for _, r := range s.Repos {
		if isDockerRepoPath(r.Path) && r.DockerName != "" {
			if t, ok := parseDockerRepoKey(r.Path); ok {
				rememberDockerContainer(t.container, r.DockerName)
			}
		}
	}
}

func (a *app) consumePreservedRepo(path string) (savedRepo, bool) {
	for i, r := range a.preservedRepos {
		if r.Path == path {
			a.preservedRepos = slices.Delete(a.preservedRepos, i, i+1)
			return r, true
		}
	}
	return savedRepo{}, false
}

func mergeSavedRepoIntoTab(tab *repoTab, saved savedRepo) {
	if tab == nil {
		return
	}
	if tab.commitMsg == "" && saved.Draft != "" {
		tab.commitMsg = saved.Draft
	}
	if !tab.commitAmend && saved.Amend {
		tab.commitAmend = true
	}
	if !tab.commitProvEdited && tab.commitProv == (provenance{}) && (saved.Provenance != (savedProvenance{}) || saved.ProvenanceEdited) {
		tab.commitProv = provenance{
			Agent:       saved.Provenance.Agent,
			Model:       saved.Provenance.Model,
			Attribution: saved.Provenance.Attribution,
		}
		tab.commitProvEdited = saved.ProvenanceEdited
	}
	if tab.pendingSession == nil && tab.inspecting == zeroHash && tab.restore == nil && (saved.Commit != "" || saved.File != "") {
		savedCopy := saved
		tab.pendingSession = &savedCopy
	}
}

func (a *app) restoreSessionWithRepos(openRepos bool) {
	s := loadSession()
	a.applySharedSessionState(s)
	a.preserveSessionRepos(s)
	a.sessionLoaded = true
	for _, t := range a.tabs {
		if saved, ok := a.consumePreservedRepo(t.path); ok {
			mergeSavedRepoIntoTab(t, saved)
		}
	}
	restoreRepos := openRepos && !a.restoreDisabled
	if !restoreRepos {
		a.sessionReady = true
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var valid []savedRepo
		var found []string
		for _, path := range s.Found {
			if isGitWorkTree(path) {
				found = append(found, path)
			}
		}
		for _, r := range s.Repos {
			if isDockerRepoPath(r.Path) {
				// 容器可能尚未启动；不要在恢复会话时把它当成失效记录丢掉。
				if r.DockerName != "" {
					if t, ok := parseDockerRepoKey(r.Path); ok {
						rememberDockerContainer(t.container, r.DockerName)
					}
				}
				valid = append(valid, r)
				continue
			}
			root, e := repoRoot(ctx, r.Path)
			if restoredRepoUsable(root, r.Path, e) {
				valid = append(valid, r)
			}
		}
		a.post(func() {
			if len(a.tabs) == 0 {
				if a.foundFrom == "" {
					a.found, a.foundFrom = found, s.FoundFrom
				}
				for _, r := range valid {
					a.openRoot(r.Path)
					if t := a.tabByPath(r.Path); t != nil {
						mergeSavedRepoIntoTab(t, r)
					}
				}
				for i, t := range a.tabs {
					if t.path == s.Active {
						a.activate(i)
						break
					}
				}
			} else {
				for _, t := range a.tabs {
					if saved, ok := a.consumePreservedRepo(t.path); ok {
						mergeSavedRepoIntoTab(t, saved)
					}
				}
			}
			a.sessionReady = true
		})
	}()
}
