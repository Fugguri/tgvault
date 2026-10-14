package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/redact"
)

func buildSection(ctx context.Context, client *telegram.Client, m *tg.Message, auth *authorizer,
	filesDir, updDir string, tr Transcriber, loc *time.Location, previews map[int]string, q *Queue, sec *redact.SecretLog) (string, *voiceJob, error) {
	timeLabel := time.Unix(int64(m.Date), 0).In(loc).Format("15:04")
	author := auth.author(m)
	text := m.Message
	if sec != nil {
		text, _ = redact.Redact(text, sec.Put)
	}

	reply := ""
	if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
		if id, ok := rh.GetReplyToMsgID(); ok && id > 0 {
			if pv := previews[id]; pv != "" {
				if sec != nil {
					pv, _ = redact.Redact(pv, sec.Put)
				}
				reply = fmt.Sprintf("↩ в ответ на msg:%d — «%s»\n\n", id, pv)
			}
			// нет превью (сообщение недоступно) — ссылку не пишем, чтобы не было висячих msg:N
		}
	}

	voice := ""
	media := ""
	var job *voiceJob
	switch md := m.Media.(type) {
	case *tg.MessageMediaDocument:
		doc, ok := md.Document.(*tg.Document)
		if !ok {
			break
		}
		_ = os.MkdirAll(filesDir, 0o755)
		name := fmt.Sprintf("doc_%d", m.ID)
		switch {
		case md.Voice:
			name = fmt.Sprintf("voice_%d.ogg", m.ID)
		default:
			if fn := docFileName(doc); fn != "" {
				name = fmt.Sprintf("%d_%s", m.ID, fn)
			} else if ext := extByMime(doc.MimeType); ext != "" {
				name = fmt.Sprintf("file_%d%s", m.ID, ext)
			}
		}
		path := filepath.Join(filesDir, name)
		loc2 := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
		if _, err := downloadIfMissing(ctx, client, loc2, path); err != nil {
			media = fmt.Sprintf("\n⚠ ошибка скачивания: %v", err)
			break
		}
		rel := relLink(updDir, path)
		if md.Voice {
			switch {
			case tr == nil:
				voice = fmt.Sprintf("\n🎤 [🔊 %s](%s)", name, rel)
			case q != nil:
				voice = fmt.Sprintf("\n🎤 (транскрибируется…)\n[🔊 %s](%s)", name, rel)
				job = &voiceJob{logPath: filepath.Join(updDir, "_log.md"), msgID: m.ID, audio: path}
			default:
				if txt, err := tr(ctx, path); err != nil {
					voice = fmt.Sprintf("\n🎤 (ошибка транскрибации: %v)\n[🔊 %s](%s)", err, name, rel)
				} else {
					voice = fmt.Sprintf("\n🎤 %s\n[🔊 %s](%s)", txt, name, rel)
				}
			}
		} else {
			media = fmt.Sprintf("\n📎 [%s](%s)", name, rel)
		}
	case *tg.MessageMediaPhoto:
		ph, ok := md.Photo.(*tg.Photo)
		if !ok {
			break
		}
		_ = os.MkdirAll(filesDir, 0o755)
		name := fmt.Sprintf("photo_%d.jpg", m.ID)
		path := filepath.Join(filesDir, name)
		loc2 := &tg.InputPhotoFileLocation{ID: ph.ID, AccessHash: ph.AccessHash, FileReference: ph.FileReference, ThumbSize: largestThumb(ph)}
		if _, err := downloadIfMissing(ctx, client, loc2, path); err != nil {
			media = fmt.Sprintf("\n⚠ ошибка скачивания: %v", err)
			break
		}
		media = fmt.Sprintf("\n![фото](%s)", relLink(updDir, path))
	}

	if text == "" && voice == "" && media == "" {
		text = "(без текста)"
	}
	return fmt.Sprintf("<!-- msg:%d -->\n## %s — %s\n\n%s%s%s%s\n\n---\n\n",
		m.ID, timeLabel, author, reply, text, voice, media), job, nil
}

func downloadIfMissing(ctx context.Context, client *telegram.Client, loc tg.InputFileLocationClass, path string) (tg.StorageFileTypeClass, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nil
	}
	return client.Download(loc).ToPath(ctx, path)
}

func relLink(fromDir, target string) string {
	rel, err := filepath.Rel(fromDir, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func largestThumb(ph *tg.Photo) string {
	best, bestSize := "y", 0
	for _, s := range ph.Sizes {
		if ps, ok := s.(*tg.PhotoSize); ok && ps.Size > bestSize {
			best, bestSize = ps.Type, ps.Size
		}
	}
	return best
}

func docFileName(doc *tg.Document) string {
	for _, a := range doc.Attributes {
		if f, ok := a.(*tg.DocumentAttributeFilename); ok {
			return f.FileName
		}
	}
	return ""
}

func extByMime(mime string) string {
	switch mime {
	case "video/mp4":
		return ".mp4"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "application/pdf":
		return ".pdf"
	case "audio/mpeg":
		return ".mp3"
	}
	return ""
}

func exportedIDs(logPath string) map[int]bool {
	out := map[int]bool{}
	b, err := os.ReadFile(logPath)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "<!-- msg:") {
			continue
		}
		s := strings.TrimSuffix(strings.TrimPrefix(line, "<!-- msg:"), " -->")
		var id int
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &id); err == nil {
			out[id] = true
		}
	}
	return out
}
