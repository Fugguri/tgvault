package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Fugguri/tgvault/internal/update"
)

// updateNotice раз в сутки (и то best-effort) напоминает, что вышел релиз.
// Не тянет обновление само — только ссылку, чтобы не запускать update вслепую.
func updateNotice(ctx context.Context, version string) {
	if os.Getenv("TGVAULT_NO_UPDATE_CHECK") != "" {
		return
	}
	stamp := filepath.Join(configDir(), ".update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	_ = os.MkdirAll(filepath.Dir(stamp), 0o700)
	_ = os.WriteFile(stamp, nil, 0o644)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rel, err := update.Fetch(cctx, update.DefaultRepo, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return
	}
	if update.Newer(rel.Tag, version) {
		fmt.Fprintf(os.Stderr, "• доступна версия %s (у тебя %s): tgvault update\n", rel.Tag, update.Normalize(version))
	}
}
