//go:build !cgo

package transcribe

import (
	"context"
	"errors"
)

// Native — заглушка для сборки без cgo: whisper.cpp не вшит, транскрибация
// недоступна. Собери бинарь с CGO_ENABLED=1 (см. Makefile: target whisper-libs).
type Native struct {
	Model   string
	FFmpeg  string
	Lang    string
	Threads int
}

// Transcribe всегда возвращает ошибку — cgo выключен.
func (n *Native) Transcribe(ctx context.Context, audio string) (string, error) {
	return "", errors.New("транскрибация недоступна: бинарь собран без cgo (CGO_ENABLED=0)")
}

// Close ничего не делает.
func (n *Native) Close() error { return nil }
