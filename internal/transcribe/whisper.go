package transcribe

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Whisper — локальная транскрибация через whisper.cpp (whisper-cli).
// Ogg/opus конвертируется в 16 kHz mono WAV (ffmpeg), затем распознаётся.
type Whisper struct {
	Bin    string // путь к whisper-cli
	Model  string // путь к ggml-*.bin
	FFmpeg string // путь к ffmpeg
	Lang   string // язык, напр. "ru"
}

// Transcribe возвращает текст аудиофайла.
func (w Whisper) Transcribe(ctx context.Context, audio string) (string, error) {
	tmp, err := os.MkdirTemp("", "tgvault-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	wav := filepath.Join(tmp, "audio.wav")
	conv := exec.CommandContext(ctx, w.FFmpeg,
		"-y", "-loglevel", "error",
		"-i", audio,
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
		wav,
	)
	if out, err := conv.CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}

	prefix := filepath.Join(tmp, "out")
	cli := exec.CommandContext(ctx, w.Bin,
		"-m", w.Model,
		"-f", wav,
		"-l", w.Lang,
		"--no-timestamps",
		"-np",
		"-otxt", "-of", prefix,
	)
	if out, err := cli.CombinedOutput(); err != nil {
		return "", fmt.Errorf("whisper-cli: %w: %s", err, strings.TrimSpace(string(out)))
	}
	b, err := os.ReadFile(prefix + ".txt")
	if err != nil {
		return "", fmt.Errorf("чтение транскрипта: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}
