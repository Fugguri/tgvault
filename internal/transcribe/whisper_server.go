package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WhisperServer — транскрибация через постоянный whisper-server: модель
// грузится один раз на процесс, а не на каждое голосовое.
type WhisperServer struct {
	Bin    string
	Model  string
	FFmpeg string
	Lang   string

	mu   sync.Mutex
	cmd  *exec.Cmd
	port int
}

// Transcribe запускает сервер (при первом вызове) и распознаёт файл.
func (w *WhisperServer) Transcribe(ctx context.Context, audio string) (string, error) {
	if err := w.ensure(ctx); err != nil {
		return "", err
	}
	wav, cleanup, err := toWav(ctx, w.FFmpeg, audio)
	if err != nil {
		return "", err
	}
	defer cleanup()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(wav))
	if err != nil {
		return "", err
	}
	f, err := os.Open(wav)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(fw, f); err != nil {
		return "", err
	}
	mw.Close()

	url := fmt.Sprintf("http://127.0.0.1:%d/inference", w.port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}

// Close останавливает сервер.
func (w *WhisperServer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
		_, _ = w.cmd.Process.Wait()
		w.cmd = nil
	}
	return nil
}

func (w *WhisperServer) ensure(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cmd != nil {
		return nil
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	w.port = port
	cmd := exec.Command(w.Bin,
		"-m", w.Model,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(port),
		"-l", w.Lang,
		"-nt",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("запуск whisper-server: %w", err)
	}
	w.cmd = cmd
	// ждём готовности
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("whisper-server не поднялся на %s", addr)
}

func toWav(ctx context.Context, ffmpeg, audio string) (string, func(), error) {
	tmp, err := os.MkdirTemp("", "tgvault-*")
	if err != nil {
		return "", func() {}, err
	}
	wav := filepath.Join(tmp, "audio.wav")
	conv := exec.CommandContext(ctx, ffmpeg, "-y", "-loglevel", "error", "-i", audio, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav)
	if out, err := conv.CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
		return "", func() {}, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return wav, func() { os.RemoveAll(tmp) }, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
