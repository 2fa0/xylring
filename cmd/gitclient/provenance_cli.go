package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func runRingCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：xylring stamp … 或 ring install|uninstall|status|snippet|script 或 ring strict install|uninstall|status|script")
	}
	// CLI commands intentionally resolve the repository from the caller's directory.
	if args[0] == "stamp" || args[0] == "ring" && len(args) > 1 && args[1] != "script" && args[1] != "snippet" {
		if root, err := repoRoot(ctx, dir); err == nil {
			dir = root
		}
	}
	if args[0] == "ring" {
		if len(args) >= 2 && args[1] == "strict" {
			if len(args) != 3 {
				return errors.New("用法：xylring ring strict install|uninstall|status|script")
			}
			switch args[2] {
			case "install":
				return installProvenanceHook(ctx, dir, "commit-msg", strictHookMarker, strictRingScript)
			case "uninstall":
				return uninstallProvenanceHook(ctx, dir, "commit-msg", strictHookMarker)
			case "status":
				path, err := provenanceHookPath(ctx, dir, "commit-msg")
				if err != nil {
					return err
				}
				b, err := os.ReadFile(path)
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				status := "严格模式未开启"
				if strings.Contains(string(b), strictHookMarker) {
					status = "严格模式已开启（v1）"
				}
				fmt.Fprintln(out, status)
				return nil
			case "script":
				fmt.Fprint(out, strictRingScript)
				return nil
			}
			return errors.New("未知 ring strict 子命令")
		}
		if len(args) != 2 {
			return errors.New("用法：xylring ring install|uninstall|status|snippet|script 或 ring strict install|uninstall|status|script")
		}
		switch args[1] {
		case "install":
			return installRingHook(ctx, dir)
		case "uninstall":
			return uninstallRingHook(ctx, dir)
		case "status":
			fmt.Fprintln(out, ringHookStatus(ctx, dir))
			return nil
		case "snippet":
			fmt.Fprint(out, provenanceRuleSnippet())
			return nil
		case "script":
			fmt.Fprint(out, ringHookScript())
			return nil
		}
		return errors.New("未知 ring 子命令")
	}
	fs := flag.NewFlagSet("stamp", flag.ContinueOnError)
	fs.SetOutput(out)
	agent := fs.String("agent", "", "Agent 名称")
	model := fs.String("model", "", "实际模型名（AI 印章必填）")
	ttl := fs.Duration("ttl", 30*time.Minute, "有效期")
	once := fs.Bool("once", false, "单次消费")
	clear := fs.Bool("clear", false, "清除印章")
	show := fs.Bool("show", false, "显示印章")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("不支持的位置参数")
	}
	if *clear {
		return clearStamp(ctx, dir)
	}
	if *show {
		s, err := readStamp(ctx, dir)
		if err != nil {
			return err
		}
		if s.p.agent == "" {
			fmt.Fprintln(out, "无生效印章")
		} else {
			fmt.Fprintf(out, "%s · 剩余 %s · once=%t\n", s.p.description(), time.Until(time.Unix(s.expires, 0)).Round(time.Second), s.once)
		}
		return nil
	}
	if *ttl <= 0 {
		return errors.New("TTL 必须大于零")
	}
	return writeStamp(ctx, dir, ringStamp{p: provenance{agent: *agent, model: *model}, expires: time.Now().Add(*ttl).Unix(), once: *once})
}
