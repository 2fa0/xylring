package radar

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// CollectSnapshot gathers pulses, Codex JSONL sessions, tmux panes, and Xylring worktrees
// into a deduplicated, conflict-checked, urgency-sorted snapshot.
func CollectSnapshot(ctx context.Context, opts ScanOptions) Snapshot {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	canon := opts.CanonicalPath
	if canon == nil {
		canon = func(p string) string {
			p = strings.TrimSpace(p)
			if p == "" {
				return ""
			}
			return filepath.Clean(p)
		}
	}

	knownRepos := make([]string, 0, len(opts.KnownRepos)+len(opts.Worktrees))
	for _, r := range opts.KnownRepos {
		if cr := canon(r); cr != "" {
			knownRepos = append(knownRepos, cr)
		}
	}
	wtByRepo := make(map[string]WorktreeHint, len(opts.Worktrees))
	for _, wt := range opts.Worktrees {
		cp := canon(wt.Path)
		if cp == "" {
			continue
		}
		wt.Path = cp
		if wt.CommonDir != "" {
			wt.CommonDir = canon(wt.CommonDir)
		}
		wtByRepo[cp] = wt
		knownRepos = append(knownRepos, cp)
	}

	pulses, _ := ListPulses(now)
	var codex []Session
	if !opts.SkipCodex {
		codex = CollectCodexSessions(ctx, opts.CodexHome, now)
	}
	var tmux []Session
	if !opts.SkipTmux {
		tmux = CollectTmuxSessions(ctx, knownRepos, now)
	}

	all := make([]Session, 0, len(pulses)+len(codex)+len(tmux)+len(opts.Worktrees))
	all = append(all, pulses...)
	all = append(all, codex...)
	all = append(all, tmux...)
	for i := range all {
		if all[i].Repo != "" {
			all[i].Repo = canon(all[i].Repo)
		}
	}

	// Merge higher-priority signals with complementary fields (e.g. tmux pane target).
	merged := mergeSessions(all)

	// Synthesize fallback entries for dirty worktrees with no active agent signal.
	coveredRepos := map[string]bool{}
	for _, s := range merged {
		if s.Repo != "" {
			coveredRepos[s.Repo] = true
		}
	}
	for repo, wt := range wtByRepo {
		if len(wt.DirtyFiles) == 0 || coveredRepos[repo] {
			continue
		}
		ts := wt.UpdatedAt
		if ts.IsZero() {
			ts = now
		}
		merged = append(merged, Session{
			ID:           "wt-" + repo,
			Source:       SourceWorktree,
			State:        StateReview,
			Agent:        "Worktree",
			Repo:         repo,
			Branch:       wt.Branch,
			DirtyFiles:   slices.Clone(wt.DirtyFiles),
			ChangedFiles: len(wt.DirtyFiles),
			Summary:      worktreeDirtySummary(wt.DirtyFiles),
			UpdatedAt:    ts,
			Target:       FocusTarget{Kind: "path"},
		})
	}

	// Enrich with worktree branch, dirty file counts, and cross-worktree overlap warnings.
	overlaps := computeWorktreeOverlaps(wtByRepo)
	for i := range merged {
		s := &merged[i]
		if s.Repo == "" {
			continue
		}
		if wt, ok := wtByRepo[s.Repo]; ok {
			if s.Branch == "" {
				s.Branch = wt.Branch
			}
			if len(s.DirtyFiles) == 0 && len(wt.DirtyFiles) > 0 {
				s.DirtyFiles = slices.Clone(wt.DirtyFiles)
				s.ChangedFiles = len(wt.DirtyFiles)
			}
		}
		if ov, ok := overlaps[s.Repo]; ok {
			s.OverlapWith = ov.with
			s.OverlapFiles = ov.files
		}
	}

	dismissed := LoadDismissed()
	visible := make([]Session, 0, len(merged))
	for _, s := range merged {
		if ackAt, ok := dismissed[s.ID]; ok && !ackAt.IsZero() && !s.UpdatedAt.After(ackAt) {
			continue
		}
		visible = append(visible, s)
	}

	sortSessions(visible)

	var attention, running int
	for _, s := range visible {
		switch {
		case s.State.NeedsAttention():
			attention++
		case s.State == StateRunning:
			running++
		}
	}
	return Snapshot{
		Sessions:       visible,
		AttentionCount: attention,
		RunningCount:   running,
		UpdatedAt:      now,
	}
}

func mergeSessions(all []Session) []Session {
	// Order highest-priority source first so primary records come first.
	ordered := slices.Clone(all)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Source.priority() != ordered[j].Source.priority() {
			return ordered[i].Source.priority() > ordered[j].Source.priority()
		}
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})

	var out []Session
	for _, cand := range ordered {
		idx := findMergeIndex(out, cand)
		if idx < 0 {
			out = append(out, cand)
			continue
		}
		primary := &out[idx]
		if primary.Target.Kind == "" && cand.Target.Kind != "" {
			primary.Target = cand.Target
		} else if cand.Target.Kind == "tmux" && primary.Target.TmuxPane == "" {
			primary.Target.Kind = "tmux"
			primary.Target.TmuxPane = cand.Target.TmuxPane
			primary.Target.TmuxTarget = cand.Target.TmuxTarget
			if primary.Target.TerminalApp == "" {
				primary.Target.TerminalApp = cand.Target.TerminalApp
			}
		} else if primary.Target.Kind == "tmux" && cand.Target.Kind == "tmux" {
			if primary.Target.TmuxTarget == "" {
				primary.Target.TmuxTarget = cand.Target.TmuxTarget
			}
			if primary.Target.TerminalApp == "" {
				primary.Target.TerminalApp = cand.Target.TerminalApp
			}
		}
		if primary.Model == "" {
			primary.Model = cand.Model
		}
		if primary.Branch == "" {
			primary.Branch = cand.Branch
		}
		if primary.Summary == "" {
			primary.Summary = cand.Summary
		}
		if cand.UpdatedAt.After(primary.UpdatedAt) {
			// If a newer lower-priority tmux signal shows running while pulse/codex is older,
			// only adopt running if it is actively fresh; keep explicit waiting/error from primary.
			if cand.Source == SourceTmux && cand.State == StateRunning && !primary.State.NeedsAttention() {
				primary.State = StateRunning
				primary.UpdatedAt = cand.UpdatedAt
			}
		}
	}
	return out
}

func findMergeIndex(existing []Session, cand Session) int {
	for i := range existing {
		cur := &existing[i]
		if cur.ID == cand.ID {
			return i
		}
		if cur.Repo == "" || cand.Repo == "" || cur.Repo != cand.Repo {
			continue
		}
		if strings.EqualFold(cur.Agent, cand.Agent) {
			// Two signals for the same repo + agent (e.g. Codex JSONL + Codex in tmux pane, or Pulse + Codex).
			return i
		}
	}
	return -1
}

type worktreeOverlap struct {
	with  []string
	files []string
}

func computeWorktreeOverlaps(wtByRepo map[string]WorktreeHint) map[string]worktreeOverlap {
	byCommon := map[string][]WorktreeHint{}
	for _, wt := range wtByRepo {
		if wt.CommonDir == "" || len(wt.DirtyFiles) == 0 {
			continue
		}
		byCommon[wt.CommonDir] = append(byCommon[wt.CommonDir], wt)
	}
	out := map[string]worktreeOverlap{}
	for _, group := range byCommon {
		if len(group) < 2 {
			continue
		}
		for i := range group {
			a := group[i]
			aSet := make(map[string]bool, len(a.DirtyFiles))
			for _, f := range a.DirtyFiles {
				aSet[f] = true
			}
			withSet := map[string]bool{}
			fileSet := map[string]bool{}
			for j := range group {
				if i == j {
					continue
				}
				b := group[j]
				shared := false
				for _, f := range b.DirtyFiles {
					if aSet[f] {
						shared = true
						fileSet[f] = true
					}
				}
				if shared {
					label := strings.TrimSpace(b.Branch)
					if label == "" {
						label = filepath.Base(b.Path)
					}
					withSet[label] = true
				}
			}
			if len(fileSet) == 0 {
				continue
			}
			var withList, fileList []string
			for w := range withSet {
				withList = append(withList, w)
			}
			for f := range fileSet {
				fileList = append(fileList, f)
			}
			sort.Strings(withList)
			sort.Strings(fileList)
			out[a.Path] = worktreeOverlap{with: withList, files: fileList}
		}
	}
	return out
}

func worktreeDirtySummary(files []string) string {
	if len(files) == 0 {
		return ""
	}
	preview := files
	suffix := ""
	if len(preview) > 3 {
		preview = preview[:3]
		suffix = ", …"
	}
	names := make([]string, len(preview))
	for i, f := range preview {
		names[i] = filepath.Base(f)
	}
	return fmt.Sprintf("%d: %s%s", len(files), strings.Join(names, ", "), suffix)
}

func sortSessions(sessions []Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		a, b := sessions[i], sessions[j]
		if a.State.Rank() != b.State.Rank() {
			return a.State.Rank() < b.State.Rank()
		}
		// Waiting sessions sorted oldest first (longest waiting on top).
		if a.State == StateWaiting {
			return a.UpdatedAt.Before(b.UpdatedAt)
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
}
