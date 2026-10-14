package main

import (
	"os"
	"path/filepath"

	"github.com/Fugguri/tgvault/internal/cli"
	"github.com/Fugguri/tgvault/internal/transcribe"
)

// buildTranscriber собирает транскрибер: whisper.cpp вшит в бинарь (cgo),
// модель грузится в процесс один раз. Внешний whisper-cli не нужен.
func buildTranscriber(model, ffmpeg, lang string) (cli.Transcriber, func()) {
	model = resolveModel(model)
	if !fileExists(model) {
		return nil, func() {}
	}
	nt := &transcribe.Native{Model: model, FFmpeg: ffmpeg, Lang: lang}
	return nt.Transcribe, func() { _ = nt.Close() }
}

// resolveModel ищет модель: явный путь → глобальный каталог setup (~/.local/share/tgvault/models).
func resolveModel(p string) string {
	if fileExists(p) {
		return p
	}
	base := filepath.Base(p)
	dirs := []string{}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "tgvault", "models"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "tgvault", "models"))
	}
	for _, d := range dirs {
		if cand := filepath.Join(d, base); fileExists(cand) {
			return cand
		}
	}
	return p
}
