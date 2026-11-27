package app

import (
	"strings"

	"github.com/2fa0/xylring/internal/gitx"
)

func init() {
	gitx.CanonicalizeSpecialRepoDir = func(dir string) (string, bool) {
		if target, ok := parseDockerRepoKey(dir); ok {
			return dockerRepoKey(target), true
		}
		return "", false
	}
	gitx.ResolveDockerGitPath = func(repoDir, gitOutPath string) (string, bool) {
		if target, ok := parseDockerRepoKey(repoDir); ok {
			// 容器内的路径就是容器内的路径，不映射成主机路径。
			return containerPath(target.path, strings.TrimSuffix(gitOutPath, "\n")), true
		}
		return "", false
	}
}

const (
	gitRetryFailedAfter = gitx.GitRetryFailedAfter

	gitLaneLocalRead  = gitx.GitLaneLocalRead
	gitLaneLocalWrite = gitx.GitLaneLocalWrite
	gitLaneRemote     = gitx.GitLaneRemote
	gitLaneBoth       = gitx.GitLaneBoth

	gitLogMaxCalls  = gitx.GitLogMaxCalls
	gitLogMaxBytes  = gitx.GitLogMaxBytes
	gitLogTextLimit = gitx.GitLogTextLimit

	gitCallRunning   = gitx.GitCallRunning
	gitCallOK        = gitx.GitCallOK
	gitCallFailed    = gitx.GitCallFailed
	gitCallCancelled = gitx.GitCallCancelled
)

type (
	gitUnavailableError = gitx.GitUnavailableError
	gitProbe            = gitx.GitProbe
	gitLaneKind         = gitx.GitLaneKind
	gitCallStatus       = gitx.GitCallStatus
	gitCallOutput       = gitx.GitCallOutput
	gitCall             = gitx.GitCall
)

var (
	skippedRepoDirs = gitx.SkippedRepoDirs
	gitResolve      = &gitx.GitResolve
	gitLanes        = &gitx.GitLanes
	remoteLanes     = &gitx.RemoteLanes
	gitLog          = gitx.GitLog

	skipRepoDir       = gitx.SkipRepoDir
	gitEnv            = gitx.GitEnv
	mergeGitEnv       = gitx.MergeGitEnv
	trimMsg           = gitx.TrimMsg
	hideConsoleWindow = gitx.HideConsoleWindow

	isGitUnavailable     = gitx.IsGitUnavailable
	setGitExecutablePath = gitx.SetGitExecutablePath
	resetGitResolution   = gitx.ResetGitResolution
	gitPath              = gitx.GitPath
	resolveGit           = gitx.ResolveGit
	samePath             = gitx.SamePath
	gitVersion           = gitx.GitVersion
	firstLine            = gitx.FirstLine
	gitBinaryEnv         = gitx.GitBinaryEnv
	gitExecError         = gitx.GitExecError

	gitLaneHeld              = gitx.GitLaneHeld
	acquireWorktreeLanes     = gitx.AcquireWorktreeLanes
	canonicalRepoDir         = gitx.CanonicalRepoDir
	acquireGitLane           = gitx.AcquireGitLane
	acquireRemoteLane        = gitx.AcquireRemoteLane
	releaseGitLane           = gitx.ReleaseGitLane
	gitCommandLane           = gitx.GitCommandLane
	gitCommandWrites         = gitx.GitCommandWrites
	gitCommandUsesRemoteSlot = gitx.GitCommandUsesRemoteSlot
	acquireRepoLanes         = gitx.AcquireRepoLanes

	summarizeGitOutput = gitx.SummarizeGitOutput
	trimUTF8Prefix     = gitx.TrimUTF8Prefix
	redactGitArgs      = gitx.RedactGitArgs
	redactGitText      = gitx.RedactGitText
	gitExitCode        = gitx.GitExitCode

	parseWSLUNCPath                  = gitx.ParseWSLUNCPath
	formatWSLUNCPath                 = gitx.FormatWSLUNCPath
	wslGitEnv                        = gitx.WSLGitEnv
	wslPath                          = gitx.WSLPath
	resolveRepoGitPath               = gitx.ResolveRepoGitPath
	wslCommandArgs                   = gitx.WSLCommandArgs
	wslCommand                       = gitx.WSLCommand
	decodeWSLCommandOutput           = gitx.DecodeWSLCommandOutput
	parseWSLDistroList               = gitx.ParseWSLDistroList
	parseWSLHomeOutput               = gitx.ParseWSLHomeOutput
	resolveDefaultWSLRepoRoot        = gitx.ResolveDefaultWSLRepoRoot
	defaultWSLRepoRoot               = gitx.DefaultWSLRepoRoot
	wslFindGitReposArgs              = gitx.WSLFindGitReposArgs
	parseWSLFindGitOutput            = gitx.ParseWSLFindGitOutput
	classifyWSLFindError             = gitx.ClassifyWSLFindError
	scanWSLGitRepos                  = gitx.ScanWSLGitRepos
	resolveWindowsWSLTerminalCommand = gitx.ResolveWindowsWSLTerminalCommand
	resolveWindowsWSLEditorArgs      = gitx.ResolveWindowsWSLEditorArgs
)
