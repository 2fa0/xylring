package main

import (
	"github.com/2fa0/xylring/internal/board"
)

func init() {
	board.UserConfigDir = func() (string, error) { return userConfigDir() }
	board.CanonicalRepoDir = canonicalRepoDir
	board.RepoTitle = repoTitle
	board.PrepareGitCommand = prepareGitCommand
	board.AppVersion = getAppVersion
}

type (
	boardStatus            = board.BoardStatus
	boardPriority          = board.BoardPriority
	boardDueState          = board.BoardDueState
	boardCommitLink        = board.BoardCommitLink
	boardNote              = board.BoardNote
	boardTask              = board.BoardTask
	boardFile              = board.BoardFile
	boardStorageInfo       = board.BoardStorageInfo
	boardTaskPatch         = board.BoardTaskPatch
	boardAddTaskOptions    = board.BoardAddTaskOptions
	boardBackupInfo        = board.BoardBackupInfo
	boardChecklistItem     = board.BoardChecklistItem
	boardDirSwitchStrategy = board.BoardDirSwitchStrategy
	boardExportOptions     = board.BoardExportOptions
	boardImportOptions     = board.BoardImportOptions
	boardImportPlan        = board.BoardImportPlan
	boardImportResult      = board.BoardImportResult
	boardRepoMapping       = board.BoardRepoMapping
)

const (
	boardTodo   = board.BoardTodo
	boardDoing  = board.BoardDoing
	boardReview = board.BoardReview
	boardDone   = board.BoardDone

	boardPriorityNone            = board.BoardPriorityNone
	boardPriorityLow             = board.BoardPriorityLow
	boardPriorityImportant       = board.BoardPriorityImportant
	boardPriorityUrgent          = board.BoardPriorityUrgent
	boardPriorityUrgentImportant = board.BoardPriorityUrgentImportant

	boardDueToday   = board.BoardDueToday
	boardDueOverdue = board.BoardDueOverdue

	boardImportMerge   = board.BoardImportMerge
	boardImportReplace = board.BoardImportReplace

	boardSwitchAuto            = board.BoardSwitchAuto
	boardSwitchUseTarget       = board.BoardSwitchUseTarget
	boardSwitchMergeTarget     = board.BoardSwitchMergeTarget
	boardSwitchOverwriteTarget = board.BoardSwitchOverwriteTarget
)

var (
	boardStatuses   = board.BoardStatuses
	boardPriorities = board.BoardPriorities

	parseBoardStatus         = board.ParseBoardStatus
	boardStatusLabel         = board.BoardStatusLabel
	parseBoardPriority       = board.ParseBoardPriority
	boardPriorityLabel       = board.BoardPriorityLabel
	parseBoardDueDate        = board.ParseBoardDueDate
	boardTaskDueInfo         = board.BoardTaskDueInfo
	boardLocationInfo        = board.BoardLocationInfo
	writeBoardLocationConfig = board.WriteBoardLocationConfig
	boardConfigPath          = board.BoardConfigPath
	normalizeBoardRepo       = board.NormalizeBoardRepo
	findBoardTaskIndex       = board.FindBoardTaskIndex
	readBoardFileAt          = board.ReadBoardFileAt
	loadBoardFile            = board.LoadBoardFile
	writeBoardFileAtomic     = board.WriteBoardFileAtomic
	renderBoardFile          = board.RenderBoardFile
	mutateBoardFile          = board.MutateBoardFile
	boardAddTask             = board.BoardAddTask
	boardAddTaskWithOptions  = board.BoardAddTaskWithOptions
	boardUpdateTask          = board.BoardUpdateTask
	boardMoveTask            = board.BoardMoveTask
	boardDeleteTask          = board.BoardDeleteTask
	boardArchiveDoneTasks    = board.BoardArchiveDoneTasks
	boardLinkCommit          = board.BoardLinkCommit
	boardUnlinkCommit        = board.BoardUnlinkCommit
	boardAddNote             = board.BoardAddNote
	boardUpdateNote          = board.BoardUpdateNote
	boardDeleteNote          = board.BoardDeleteNote
	boardMoveNote            = board.BoardMoveNote
	boardMatchesFilter       = board.BoardMatchesFilter
	boardTaskAllRepos        = board.BoardTaskAllRepos
	boardExport              = board.BoardExport
	writeBoardExportFile     = board.WriteBoardExportFile
	buildBoardImportPlan     = board.BuildBoardImportPlan
	boardImport              = board.BoardImport
	listBoardBackups         = board.ListBoardBackups
	boardRestoreBackup       = board.BoardRestoreBackup
	boardProbeDir            = board.BoardProbeDir
	boardSwitchStorageDir    = board.BoardSwitchStorageDir
	parseBoardChecklist      = board.ParseBoardChecklist
	findBoardChecklistItem   = board.FindBoardChecklistItem
	toggleBoardChecklistItem = board.ToggleBoardChecklistItem
	boardToggleChecklistTask = board.BoardToggleChecklistTask
	boardNewestCommits       = board.BoardNewestCommits
	latestBoardNote          = board.LatestBoardNote
	formatBoardAgentContext  = board.FormatBoardAgentContext
	isBoardRepoAvailable     = board.IsBoardRepoAvailable
)
