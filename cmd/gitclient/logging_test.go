package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2fa0/xylring/internal/crashlog"
)

func TestLoggerWritesFileWithoutConsole(t *testing.T) {
	if dir := os.Getenv("XYLRING_TEST_LOG_DIR"); dir != "" {
		if _, err := crashlog.Install(dir); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("XYLRING_TEST_LOG_BLOCKED") == "1" {
			if err := os.Mkdir(filepath.Join(dir, "logs", "xylring.log"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		// A GUI process may have no usable stderr. Isolate the closed
		// handle and logger lifetime from the parent test process.
		if err := os.Stderr.Close(); err != nil {
			t.Fatal(err)
		}
		log := newLogger(dir)
		log.Info("startup without console")
		log.Warn("keepalive without console")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprint(blocked), func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(exe, "-test.run=^TestLoggerWritesFileWithoutConsole$")
			cmd.Env = append(os.Environ(), "XYLRING_TEST_LOG_DIR="+dir, "XYLRING_TEST_UI=0", "XYLRING_TEST_LOG_BLOCKED=0")
			if blocked {
				cmd.Env = append(cmd.Env, "XYLRING_TEST_LOG_BLOCKED=1")
			}
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("logger subprocess: %v\n%s", err, out)
			}
			data, err := os.ReadFile(filepath.Join(dir, "logs", "crash.log"))
			if err != nil {
				t.Fatal(err)
			}
			marker := "普通日志写入已验证"
			if blocked {
				marker = "普通日志初始化失败"
			}
			if !strings.Contains(string(data), marker) || !strings.Contains(string(data), "xylring.log") {
				t.Fatalf("missing logger initialization diagnostic: %s", data)
			}
			if blocked {
				return
			}
			data, err = os.ReadFile(filepath.Join(dir, "logs", "xylring.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range []string{"startup without console", "keepalive without console"} {
				if !strings.Contains(string(data), message) {
					t.Fatalf("missing log %q without a console: %s", message, data)
				}
			}
		})
	}
}

func TestBackgroundCrashLog(t *testing.T) {
	if dir := os.Getenv("XYLRING_TEST_CRASH_DIR"); dir != "" {
		initLogging(dir)
		log.Print("before background crash")
		crashlog.Record("opening test repository")
		go func() { panic("test background crash") }()
		select {}
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestBackgroundCrashLog$")
	cmd.Env = append(os.Environ(), "XYLRING_TEST_CRASH_DIR="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected subprocess crash")
	}
	for file, messages := range map[string][]string{
		"xylring.log":  {"before background crash"},
		"crash.log":    {"opening test repository", "panic: test background crash", "logging_test.go"},
		"session.open": {"pid="},
	} {
		data, err := os.ReadFile(filepath.Join(dir, "logs", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range messages {
			if !strings.Contains(string(data), message) {
				t.Errorf("missing %q in %s", message, file)
			}
		}
	}
}
