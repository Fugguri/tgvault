package importer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// voiceJob — задание на транскрибацию голосового и обновление секции.
type voiceJob struct {
	logPath string
	msgID   int
	audio   string
}

// Queue — внутренняя накапливающаяся очередь транскрибации: текст пишется
// сразу с плейсхолдером, воркеры распознают и обновляют секции на месте.
type Queue struct {
	jobs chan voiceJob
	wg   sync.WaitGroup
	tr   Transcriber
	ctx  context.Context

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewQueue создаёт очередь с N воркерами (N<=0 → 1).
func NewQueue(ctx context.Context, tr Transcriber, workers int) *Queue {
	if workers <= 0 {
		workers = 1
	}
	q := &Queue{
		jobs:  make(chan voiceJob, 64),
		tr:    tr,
		ctx:   ctx,
		locks: map[string]*sync.Mutex{},
	}
	for i := 0; i < workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	return q
}

func (q *Queue) submit(j voiceJob) { q.jobs <- j }

// Wait ждёт завершения всех заданий.
func (q *Queue) Wait() {
	close(q.jobs)
	q.wg.Wait()
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for j := range q.jobs {
		text, err := q.tr(q.ctx, j.audio)
		if err != nil {
			text = fmt.Sprintf("(ошибка транскрибации: %v)", err)
		}
		if err := q.updateSection(j.logPath, j.msgID, text); err != nil {
			fmt.Printf("  ⚠ обновление транскрипта msg:%d: %v\n", j.msgID, err)
		}
	}
}

// updateSection заменяет плейсхолдер транскрипта в секции сообщения (атомарно).
func (q *Queue) updateSection(logPath string, msgID int, text string) error {
	mu := q.fileLock(logPath)
	mu.Lock()
	defer mu.Unlock()

	b, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	s := string(b)
	marker := fmt.Sprintf("<!-- msg:%d -->", msgID)
	i := strings.Index(s, marker)
	if i < 0 {
		return nil
	}
	end := strings.Index(s[i:], "\n---\n")
	if end < 0 {
		end = len(s) - i
	}
	section := s[i : i+end]
	const placeholder = "🎤 (транскрибируется…)"
	if !strings.Contains(section, placeholder) {
		return nil
	}
	newSection := strings.Replace(section, placeholder, "🎤 "+strings.ReplaceAll(text, "\n", " "), 1)
	out := s[:i] + newSection + s[i+end:]

	tmp := logPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, logPath)
}

func (q *Queue) fileLock(path string) *sync.Mutex {
	q.mu.Lock()
	defer q.mu.Unlock()
	m, ok := q.locks[path]
	if !ok {
		m = &sync.Mutex{}
		q.locks[path] = m
	}
	return m
}
