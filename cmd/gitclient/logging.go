package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gitclient/internal/crashlog"
)

// The crash recorder and rotating writer follow Snapover's implementation.
func initLogging(dataDir string) {
	crashPath, crashErr := crashlog.Install(dataDir)
	slog.SetDefault(newLogger(dataDir))
	if crashErr != nil {
		log.Printf("崩溃日志不可用：%v", crashErr)
	} else {
		log.Printf("崩溃日志：%s", crashPath)
	}
}

func newLogger(dataDir string) *slog.Logger {
	fallback := slog.New(slog.NewTextHandler(os.Stderr, nil))
	dir := filepath.Join(dataDir, "logs")
	path := filepath.Join(dir, "xylring.log")
	fail := func(err error) *slog.Logger {
		crashlog.Record(fmt.Sprintf("普通日志初始化失败 path=%q pid=%d err=%v", path, os.Getpid(), err))
		fallback.Error("普通日志初始化失败", "path", path, "err", err)
		return fallback
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(err)
	}
	f, err := crashlog.OpenOrdinary(path)
	if err != nil {
		return fail(err)
	}
	crashlog.PruneInactive(dataDir)
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "普通日志已启用", 0)
	record.Add("path", path, "pid", os.Getpid())
	if err := slog.NewTextHandler(f, nil).Handle(context.Background(), record); err != nil {
		_ = f.Close()
		return fail(err)
	}
	crashlog.Record(fmt.Sprintf("普通日志写入已验证 path=%q pid=%d", path, os.Getpid()))
	// GUI processes may have no usable stderr; persist the file first.
	return slog.New(slog.NewTextHandler(io.MultiWriter(f, os.Stderr), nil))
}

func startLogging() {
	path, err := configFile("logs")
	if err != nil {
		log.Printf("无法启用本地日志，继续使用 stderr：%v", err)
		return
	}
	initLogging(filepath.Dir(path))
	log.Printf("started version=%s go=%s platform=%s/%s pid=%d", getAppVersion(), runtime.Version(), runtime.GOOS, runtime.GOARCH, os.Getpid())
}
