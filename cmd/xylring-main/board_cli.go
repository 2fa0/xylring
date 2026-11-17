package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// readBoardCommitSnapshot resolves ref inside repoDir and snapshots its subject,
// branch, and AI provenance trailers so the card remains readable even if later rewritten.
func readBoardCommitSnapshot(ctx context.Context, repoDir, ref, addedBy string) (boardCommitLink, error) {
	root, err := repoRoot(ctx, repoDir)
	if err != nil {
		return boardCommitLink{}, err
	}
	root = canonicalRepoDir(root)
	h, err := resolveCommit(ctx, root, ref)
	if err != nil {
		return boardCommitLink{}, err
	}
	hexHash := hashHex(h)
	out, err := runGit(ctx, root, "log", "-1", "--format=%s%x00%(trailers:unfold,only)", hexHash)
	if err != nil {
		return boardCommitLink{}, err
	}
	subject, trailers, _ := strings.Cut(string(out), "\x00")
	prov := parseProvenance(trailers)

	branch := ""
	if bOut, bErr := runGit(ctx, root, "symbolic-ref", "-q", "--short", "HEAD"); bErr == nil {
		branch = strings.TrimSpace(string(bOut))
	}
	return boardCommitLink{
		Repo:    root,
		Hash:    hexHash,
		Subject: strings.TrimSpace(subject),
		Branch:  branch,
		Agent:   prov.agent,
		Model:   prov.model,
		AddedBy: strings.TrimSpace(addedBy),
		AddedAt: time.Now().UTC(),
	}, nil
}

type parsedBoardArgs struct {
	positional []string
	values     map[string][]string
	bools      map[string]bool
}

// parseBoardFlags supports flags before or after positional arguments so Agents
// can naturally write either `xylring board update T-1 --done` or `--done T-1`.
func parseBoardFlags(args []string, valueFlags map[string]bool, boolFlags map[string]bool) (parsedBoardArgs, error) {
	res := parsedBoardArgs{
		values: map[string][]string{},
		bools:  map[string]bool{},
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			res.positional = append(res.positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			res.positional = append(res.positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		val := ""
		hasEq := false
		if k, v, ok := strings.Cut(name, "="); ok {
			name, val, hasEq = k, v, true
		}
		if boolFlags[name] {
			if hasEq {
				res.bools[name] = val == "1" || strings.EqualFold(val, "true")
			} else {
				res.bools[name] = true
			}
			continue
		}
		if valueFlags[name] {
			if !hasEq {
				if i+1 >= len(args) {
					return res, fmt.Errorf("参数 --%s 缺少取值", name)
				}
				i++
				val = args[i]
			}
			res.values[name] = append(res.values[name], val)
			continue
		}
		return res, fmt.Errorf("未知参数: %s", a)
	}
	return res, nil
}

func (p parsedBoardArgs) lastValue(name string) (string, bool) {
	vals := p.values[name]
	if len(vals) == 0 {
		return "", false
	}
	return vals[len(vals)-1], true
}

func runBoardCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printBoardCLIHelp(out)
		return nil
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		return runBoardListCLI(ctx, dir, rest, out)
	case "show":
		return runBoardShowCLI(rest, out)
	case "add":
		return runBoardAddCLI(ctx, dir, rest, out)
	case "update":
		return runBoardUpdateCLI(ctx, dir, rest, out)
	case "link":
		return runBoardLinkCLI(ctx, dir, rest, out)
	case "unlink":
		return runBoardUnlinkCLI(ctx, dir, rest, out)
	case "note":
		return runBoardNoteCLI(rest, out)
	case "delete":
		return runBoardDeleteCLI(rest, out)
	case "snippet":
		fmt.Fprint(out, boardRuleSnippet())
		return nil
	default:
		return fmt.Errorf("未知 board 子命令: %s（运行 xylring board --help 查看用法）", sub)
	}
}

func printBoardCLIHelp(out io.Writer) {
	fmt.Fprint(out, `用法:
  xylring board list [--status todo,doing,review,done] [--repo DIR] [--archived] [--json]
  xylring board show <ID> [--json]
  xylring board add <TITLE> [--desc TEXT] [--status todo] [--repo DIR]... [--json]
  xylring board update <ID> [--title TEXT] [--desc TEXT] [--status STATUS] [--done] [--archive] [--unarchive] [--repo DIR]... [--json]
  xylring board link <ID> <REF_OR_HASH>... [--repo DIR] [--by NAME] [--json]
  xylring board unlink <ID> <HASH> [--repo DIR] [--json]
  xylring board note <ID> <TEXT> [--agent NAME] [--model NAME] [--json]
  xylring board delete <ID>
  xylring board snippet
`)
}

func resolveBoardRepoArg(ctx context.Context, baseDir, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = baseDir
	}
	if root, err := repoRoot(ctx, raw); err == nil {
		return canonicalRepoDir(root)
	}
	return normalizeBoardRepo(raw)
}

func runBoardListCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"status": true, "repo": true},
		map[string]bool{"archived": true, "json": true},
	)
	if err != nil {
		return err
	}
	bf, err := loadBoardFile()
	if err != nil {
		return err
	}
	var statusFilter map[boardStatus]bool
	if rawStatuses, ok := parsed.lastValue("status"); ok {
		statusFilter = map[boardStatus]bool{}
		for _, part := range strings.Split(rawStatuses, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			st, valid := parseBoardStatus(part)
			if !valid {
				return fmt.Errorf("未知状态: %s", part)
			}
			statusFilter[st] = true
		}
	}
	repoFilter := ""
	if r, ok := parsed.lastValue("repo"); ok {
		repoFilter = resolveBoardRepoArg(ctx, dir, r)
	}
	showArchived := parsed.bools["archived"]

	var items []boardTask
	for _, t := range bf.Tasks {
		if !boardMatchesFilter(t, repoFilter, "", showArchived) {
			continue
		}
		if len(statusFilter) > 0 && !statusFilter[t.Status] {
			continue
		}
		items = append(items, t)
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, items)
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "暂无匹配任务")
		return nil
	}
	for _, t := range items {
		var repoNames []string
		for _, r := range boardTaskAllRepos(t) {
			repoNames = append(repoNames, repoTitle(r))
		}
		meta := ""
		if len(repoNames) > 0 {
			meta += " [" + strings.Join(repoNames, ", ") + "]"
		}
		if len(t.Commits) > 0 {
			meta += fmt.Sprintf(" (%d commits)", len(t.Commits))
		}
		fmt.Fprintf(out, "%s\t[%s]\t%s%s\n", t.ID, boardStatusLabel(t.Status), t.Title, meta)
	}
	return nil
}

func runBoardShowCLI(args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args, nil, map[string]bool{"json": true})
	if err != nil {
		return err
	}
	if len(parsed.positional) != 1 {
		return errors.New("用法: xylring board show <ID> [--json]")
	}
	bf, err := loadBoardFile()
	if err != nil {
		return err
	}
	idx := findBoardTaskIndex(bf.Tasks, parsed.positional[0])
	if idx < 0 {
		return fmt.Errorf("未找到看板任务: %s", parsed.positional[0])
	}
	t := bf.Tasks[idx]
	if parsed.bools["json"] {
		return writeJSONLine(out, t)
	}
	fmt.Fprintf(out, "%s [%s] %s\n", t.ID, boardStatusLabel(t.Status), t.Title)
	if repos := boardTaskAllRepos(t); len(repos) > 0 {
		fmt.Fprintf(out, "关联项目: %s\n", strings.Join(repos, ", "))
	}
	if t.Description != "" {
		fmt.Fprintf(out, "描述:\n%s\n", t.Description)
	}
	if len(t.Commits) > 0 {
		fmt.Fprintln(out, "关联提交:")
		for _, c := range t.Commits {
			short := c.Hash
			if len(short) > 7 {
				short = short[:7]
			}
			prov := ""
			if c.Agent != "" {
				prov = " · " + c.Agent
				if c.Model != "" {
					prov += " (" + c.Model + ")"
				}
			}
			fmt.Fprintf(out, "  - %s %s %s%s\n", repoTitle(c.Repo), short, c.Subject, prov)
		}
	}
	if len(t.Notes) > 0 {
		fmt.Fprintln(out, "备注:")
		for _, n := range t.Notes {
			who := n.Author
			if n.Model != "" {
				who += " (" + n.Model + ")"
			}
			fmt.Fprintf(out, "  - [%s %s] %s\n", n.CreatedAt.Local().Format("01-02 15:04"), who, n.Text)
		}
	}
	return nil
}

func runBoardAddCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"desc": true, "status": true, "repo": true},
		map[string]bool{"json": true},
	)
	if err != nil {
		return err
	}
	if len(parsed.positional) == 0 {
		return errors.New("用法: xylring board add <TITLE> [--desc TEXT] [--status todo] [--repo DIR]...")
	}
	title := strings.Join(parsed.positional, " ")
	desc, _ := parsed.lastValue("desc")
	st := boardTodo
	if rawSt, ok := parsed.lastValue("status"); ok {
		parsedSt, valid := parseBoardStatus(rawSt)
		if !valid {
			return fmt.Errorf("未知状态: %s", rawSt)
		}
		st = parsedSt
	}
	var repos []string
	for _, r := range parsed.values["repo"] {
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				repos = append(repos, resolveBoardRepoArg(ctx, dir, part))
			}
		}
	}
	_, task, err := boardAddTask(title, desc, st, repos)
	if err != nil {
		return err
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, task)
	}
	fmt.Fprintf(out, "已创建 %s [%s] %s\n", task.ID, boardStatusLabel(task.Status), task.Title)
	return nil
}

func runBoardUpdateCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"title": true, "desc": true, "status": true, "repo": true},
		map[string]bool{"done": true, "archive": true, "unarchive": true, "json": true},
	)
	if err != nil {
		return err
	}
	if len(parsed.positional) != 1 {
		return errors.New("用法: xylring board update <ID> [--title TEXT] [--desc TEXT] [--status STATUS] [--done] [--archive]")
	}
	var patch boardTaskPatch
	if v, ok := parsed.lastValue("title"); ok {
		patch.Title = &v
	}
	if v, ok := parsed.lastValue("desc"); ok {
		patch.Description = &v
	}
	if rawSt, ok := parsed.lastValue("status"); ok {
		st, valid := parseBoardStatus(rawSt)
		if !valid {
			return fmt.Errorf("未知状态: %s", rawSt)
		}
		patch.Status = &st
	}
	if parsed.bools["done"] {
		st := boardDone
		patch.Status = &st
	}
	if parsed.bools["archive"] {
		v := true
		patch.Archived = &v
	} else if parsed.bools["unarchive"] {
		v := false
		patch.Archived = &v
	}
	for _, r := range parsed.values["repo"] {
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				patch.AddRepos = append(patch.AddRepos, resolveBoardRepoArg(ctx, dir, part))
			}
		}
	}
	if patch.Title == nil && patch.Description == nil && patch.Status == nil && patch.Archived == nil && patch.Repos == nil && len(patch.AddRepos) == 0 {
		return errors.New("没有要更新的字段；用 --title、--desc、--status、--done、--archive 或 --repo 指定")
	}
	_, task, err := boardUpdateTask(parsed.positional[0], patch)
	if err != nil {
		return err
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, task)
	}
	fmt.Fprintf(out, "已更新 %s [%s] %s\n", task.ID, boardStatusLabel(task.Status), task.Title)
	return nil
}

func runBoardLinkCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"repo": true, "by": true},
		map[string]bool{"json": true},
	)
	if err != nil {
		return err
	}
	if len(parsed.positional) < 2 {
		return errors.New("用法: xylring board link <ID> <REF_OR_HASH>... [--repo DIR] [--by NAME]")
	}
	id := parsed.positional[0]
	refs := parsed.positional[1:]
	repoDir := dir
	if r, ok := parsed.lastValue("repo"); ok {
		repoDir = r
	}
	addedBy, _ := parsed.lastValue("by")
	var task boardTask
	for _, ref := range refs {
		link, snapErr := readBoardCommitSnapshot(ctx, repoDir, ref, addedBy)
		if snapErr != nil {
			return snapErr
		}
		_, task, err = boardLinkCommit(id, link)
		if err != nil {
			return err
		}
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, task)
	}
	fmt.Fprintf(out, "已关联 %d 个提交到 %s\n", len(refs), task.ID)
	return nil
}

func runBoardUnlinkCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"repo": true},
		map[string]bool{"json": true},
	)
	if err != nil {
		return err
	}
	if len(parsed.positional) != 2 {
		return errors.New("用法: xylring board unlink <ID> <HASH> [--repo DIR]")
	}
	repo := ""
	if r, ok := parsed.lastValue("repo"); ok {
		repo = resolveBoardRepoArg(ctx, dir, r)
	}
	_, task, err := boardUnlinkCommit(parsed.positional[0], repo, parsed.positional[1])
	if err != nil {
		return err
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, task)
	}
	fmt.Fprintf(out, "已从 %s 移除提交关联\n", task.ID)
	return nil
}

func runBoardNoteCLI(args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args,
		map[string]bool{"agent": true, "model": true},
		map[string]bool{"json": true},
	)
	if err != nil {
		return err
	}
	if len(parsed.positional) < 2 {
		return errors.New("用法: xylring board note <ID> <TEXT> [--agent NAME] [--model NAME]")
	}
	id := parsed.positional[0]
	text := strings.Join(parsed.positional[1:], " ")
	author, _ := parsed.lastValue("agent")
	if author == "" {
		author = "agent"
	}
	model, _ := parsed.lastValue("model")
	_, task, err := boardAddNote(id, text, author, model)
	if err != nil {
		return err
	}
	if parsed.bools["json"] {
		return writeJSONLine(out, task)
	}
	fmt.Fprintf(out, "已为 %s 添加备注\n", task.ID)
	return nil
}

func runBoardDeleteCLI(args []string, out io.Writer) error {
	parsed, err := parseBoardFlags(args, nil, nil)
	if err != nil {
		return err
	}
	if len(parsed.positional) != 1 {
		return errors.New("用法: xylring board delete <ID>")
	}
	if _, err := boardDeleteTask(parsed.positional[0]); err != nil {
		return err
	}
	fmt.Fprintf(out, "已删除 %s\n", strings.ToUpper(strings.TrimSpace(parsed.positional[0])))
	return nil
}

func writeJSONLine(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func boardRuleSnippet() string {
	return `## 看板协作约定（Xylring Board）

如本次任务对应 Xylring 看板卡片（可用 ` + "`xylring board list`" + ` 查看任务编号 ` + "`T-x`" + `）：
1. 开始处理某项任务时，将状态更新为进行中：
   ` + "`xylring board update T-x --status doing`" + `
2. 在各仓库完成提交后，把相关提交哈希绑定到任务卡片（会自动识别当前仓库与年轮来源）：
   ` + "`xylring board link T-x HEAD`" + `
3. 完成后补充简短进展备注，并将状态推至待审阅（或按要求直接标完成）：
   ` + "`xylring board note T-x \"已完成……\" --agent \"<Agent名>\" --model \"<模型名>\"`" + `
   ` + "`xylring board update T-x --status review`" + `   # 或 --done
`
}

// suppress unused import if slices isn't used elsewhere in this file
var _ = slices.Contains[[]string, string]
