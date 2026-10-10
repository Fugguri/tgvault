//go:build cgo

// Пакет transcribe — локальное распознавание речи. whisper.cpp слинкован
// статически (cgo), модель грузится в процесс один раз; внешний whisper-cli
// не нужен. Аудио (ogg/opus) декодируется ffmpeg в raw PCM f32le.
// libstdc++/libgomp — обычные системные зависимости cgo-сборки.
package transcribe

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/whisper.cpp/include -I${SRCDIR}/../../third_party/whisper.cpp/ggml/include
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/whisper.cpp/build_go/src -L${SRCDIR}/../../third_party/whisper.cpp/build_go/ggml/src -lwhisper -lggml -lggml-base -lggml-cpu -lstdc++ -lm
#cgo linux LDFLAGS: -fopenmp
#cgo darwin LDFLAGS: -lggml-metal -lggml-blas -framework Accelerate -framework Foundation -framework Metal -framework MetalKit -framework CoreGraphics

#include <whisper.h>
#include <stdlib.h>
#include <string.h>

// Тихий лог whisper.cpp, чтобы не сыпать служебными строками в вывод.
static void tgv_log_silent(enum ggml_log_level level, const char * text, void * user_data) {
    (void) level; (void) text; (void) user_data;
}

static void tgv_log_disable(void) {
    whisper_log_set(tgv_log_silent, NULL);
}

// Распознаёт PCM (16 кГц, mono, float32) и возвращает склеенный текст сегментов.
// lang == NULL → автоопределение. Возвращает NULL при ошибке.
static char * tgv_transcribe(struct whisper_context * ctx, int n_threads, const char * lang,
                             const float * samples, int n) {
    struct whisper_full_params p = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
    p.n_threads       = n_threads;
    p.language        = lang;
    p.translate       = 0;
    p.no_context      = 1;
    p.no_timestamps   = 1;
    p.single_segment  = 0;
    p.print_special   = 0;
    p.print_progress  = 0;
    p.print_realtime  = 0;
    p.print_timestamps = 0;

    if (whisper_full(ctx, p, samples, n) != 0) {
        return NULL;
    }

    const int ns = whisper_full_n_segments(ctx);
    size_t total = 1;
    for (int i = 0; i < ns; i++) {
        total += strlen(whisper_full_get_segment_text(ctx, i)) + 1;
    }
    char * out = malloc(total);
    if (out == NULL) {
        return NULL;
    }
    out[0] = '\0';
    for (int i = 0; i < ns; i++) {
        if (i > 0) {
            strcat(out, " ");
        }
        strcat(out, whisper_full_get_segment_text(ctx, i));
    }
    return out;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// Native — транскрибация в процессе: whisper.cpp вшит в бинарь, ggml-модель
// открывается напрямую (без whisper-cli), декодирование аудио делает ffmpeg.
type Native struct {
	Model   string // путь к ggml-*.bin
	FFmpeg  string // путь к ffmpeg
	Lang    string // язык, напр. "ru"; "" или "auto" — автоопределение
	Threads int    // 0 → runtime.NumCPU()

	once sync.Once
	ctx  *C.struct_whisper_context
	err  error
	mu   sync.Mutex
}

// Transcribe возвращает текст аудиофайла.
func (n *Native) Transcribe(ctx context.Context, audio string) (string, error) {
	if err := n.load(); err != nil {
		return "", err
	}
	samples, err := decode(ctx, n.FFmpeg, audio)
	if err != nil {
		return "", err
	}
	if len(samples) == 0 {
		return "", nil
	}

	threads := n.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	var clang *C.char
	if l := strings.TrimSpace(n.Lang); l != "" && l != "auto" {
		clang = C.CString(l)
		defer C.free(unsafe.Pointer(clang))
	}

	n.mu.Lock()
	defer n.mu.Unlock()
	out := C.tgv_transcribe(n.ctx, C.int(threads), clang,
		(*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)))
	if out == nil {
		return "", fmt.Errorf("whisper: распознавание не удалось")
	}
	defer C.free(unsafe.Pointer(out))

	return normalize(C.GoString(out)), nil
}

// Close освобождает модель.
func (n *Native) Close() error {
	if n.ctx != nil {
		C.whisper_free(n.ctx)
		n.ctx = nil
	}
	return nil
}

func (n *Native) load() error {
	n.once.Do(func() {
		if _, err := os.Stat(n.Model); err != nil {
			n.err = fmt.Errorf("модель whisper не найдена: %s (запусти tgvault setup)", n.Model)
			return
		}
		C.tgv_log_disable()
		cm := C.CString(n.Model)
		defer C.free(unsafe.Pointer(cm))
		n.ctx = C.whisper_init_from_file_with_params(cm, C.whisper_context_default_params())
		if n.ctx == nil {
			n.err = fmt.Errorf("не удалось загрузить модель whisper: %s", n.Model)
		}
	})
	return n.err
}

// decode декодирует аудио в mono PCM 16 кГц float32 через ffmpeg.
func decode(ctx context.Context, ffmpeg, audio string) ([]float32, error) {
	if ffmpeg == "" {
		ffmpeg = "ffmpeg"
	}
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error",
		"-i", audio,
		"-ar", "16000", "-ac", "1",
		"-f", "f32le", "-",
	)
	raw, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	n := len(raw) / 4
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return samples, nil
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
