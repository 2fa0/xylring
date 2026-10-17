package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

type credentialRequest struct {
	Prompt string
	Reply  chan credentialAnswer
	Value  string
}
type credentialAnswer struct {
	Value     string
	Cancelled bool
}

// Askpass uses a private local socket; credentials never enter command arguments or logs.
func askpassMain() bool {
	if len(os.Args) < 2 || os.Args[1] != "--git-askpass" {
		return false
	}
	conn, err := net.DialTimeout("unix", os.Getenv("GITCLIENT_ASKPASS_SOCKET"), 10*time.Second)
	if err != nil {
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	prompt := strings.Join(os.Args[2:], " ")
	if json.NewEncoder(conn).Encode(prompt) != nil {
		os.Exit(1)
	}
	var answer credentialAnswer
	if json.NewDecoder(conn).Decode(&answer) != nil || answer.Cancelled {
		os.Exit(1)
	}
	fmt.Println(answer.Value)
	return true
}
func credentialBridge(ctx context.Context, ask func(context.Context, string) (string, error)) (env []string, cleanup func(), err error) {
	temp, err := os.MkdirTemp("", "gitclient-auth-")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { os.RemoveAll(temp) }
	sock := filepath.Join(temp, "s")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	cleanup = func() { listener.Close(); os.RemoveAll(temp) }
	exe, err := os.Executable()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	// Git expects one executable path. The helper script quotes the application path, including spaces.
	script := filepath.Join(temp, "askpass")
	quoted := "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "'"
	if err = os.WriteFile(script, []byte("#!/bin/sh\nexec "+quoted+" --git-askpass \"$@\"\n"), 0700); err != nil {
		cleanup()
		return nil, nil, err
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
				var prompt string
				if json.NewDecoder(conn).Decode(&prompt) != nil {
					return
				}
				value, err := ask(ctx, prompt)
				_ = json.NewEncoder(conn).Encode(credentialAnswer{Value: value, Cancelled: err != nil})
			}()
		}
	}()
	env = []string{"GIT_ASKPASS=" + script, "SSH_ASKPASS=" + script, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=gitclient", "GITCLIENT_ASKPASS_SOCKET=" + sock}
	return env, cleanup, nil
}
func (a *app) askCredential(ctx context.Context, prompt string) (string, error) {
	request := &credentialRequest{Prompt: prompt, Reply: make(chan credentialAnswer, 1)}
	a.post(func() { a.credentials = append(a.credentials, request) })
	select {
	case answer := <-request.Reply:
		if answer.Cancelled {
			return "", errors.New("认证已取消")
		}
		return answer.Value, nil
	case <-ctx.Done():
		a.post(func() {
			for i, r := range a.credentials {
				if r == request {
					a.credentials = append(a.credentials[:i], a.credentials[i+1:]...)
					break
				}
			}
		})
		return "", ctx.Err()
	}
}
func (a *app) credentialModal(c *ui.Context) {
	if len(a.credentials) == 0 {
		return
	}
	r := a.credentials[0]
	open := true
	finish := func(cancel bool) {
		r.Reply <- credentialAnswer{Value: r.Value, Cancelled: cancel}
		r.Value = ""
		a.credentials = a.credentials[1:]
	}
	ui.Modal(c.Key("credential"), &open, func() {
		ui.Column(c).Width(500).Gap(8).Children(func() {
			ui.Text(c, "Git 认证").Bold()
			ui.Text(c, r.Prompt)
			e := ui.TextInput(c.Key("credential-value"), &r.Value).Width(460).AutoFocus()
			lower := strings.ToLower(r.Prompt)
			if !strings.Contains(lower, "username") && !strings.Contains(lower, "user name") && !strings.Contains(lower, "yes/no") {
				e.Password()
			}
			ui.Text(c, "输入仅用于本次请求；凭据保存由系统 Git 的 credential helper 管理。").FontSize(12)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c.Key("credential-cancel"), "取消认证").Clicked() {
					finish(true)
				}
				if ui.PrimaryButton(c.Key("credential-submit"), "提交认证").Clicked() || e.Submitted() {
					finish(false)
				}
			})
		})
	})
	if !open && len(a.credentials) > 0 && a.credentials[0] == r {
		finish(true)
	}
}
func runWriteGit(ctx context.Context, dir string, args []string, ask func(context.Context, string) (string, error)) (string, error) {
	if err := acquireGit(ctx); err != nil {
		return "", err
	}
	defer func() { <-gitSlots }()
	bin, err := gitPath()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, gitArgs(args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if ask != nil {
		env, clean, err := credentialBridge(ctx, ask)
		if err != nil {
			return "", err
		}
		defer clean()
		keys := map[string]bool{}
		for _, e := range env {
			k, _, _ := strings.Cut(e, "=")
			keys[k] = true
		}
		var base []string
		for _, e := range cmd.Env {
			k, _, _ := strings.Cut(e, "=")
			if !keys[k] {
				base = append(base, e)
			}
		}
		cmd.Env = append(base, env...)
	}
	configureWriteProcess(cmd)
	output := &boundedOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	text := output.String()
	if ctx.Err() != nil {
		return text, fmt.Errorf("Git 已取消；请检查仓库状态：%w", ctx.Err())
	}
	if err != nil {
		return text, fmt.Errorf("git %s 失败：%s", args[0], text)
	}
	return text, nil
}

// os/exec serializes a shared stdout/stderr writer. Keep only the most recent 64 KiB.
type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 64 << 10
	if len(p) >= limit {
		b.Reset()
		p = p[len(p)-limit:]
	} else if b.Len()+len(p) > limit {
		b.Next(b.Len() + len(p) - limit)
	}
	_, err := b.Buffer.Write(p)
	return n, err
}
