package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"
)

func runRingCLI(ctx context.Context, dir string, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("用法：xylring stamp … 或 ring install|uninstall|status|snippet|script")
	}
	if args[0] == "ring" {
		if len(args) != 2 {
			return errors.New("用法：xylring ring install|uninstall|status|snippet|script")
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
	model := fs.String("model", "", "已知模型名")
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
