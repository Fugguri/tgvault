// Package importer переносит сообщения Telegram в Markdown-логи
// docs/telegram_chats/<slug>/upd_<день>/_log.md с дедупом по msg id.
package importer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/redact"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// Transcriber — транскрибация аудиофайла в текст (может быть nil).
type Transcriber func(ctx context.Context, path string) (string, error)

// Options — параметры импорта.
type Options struct {
	Out         string    // абсолютный корень раскладки
	From, To    time.Time // границы (нулевое = без границы)
	MinID       int       // watermark: тянуть только сообщения с id > MinID
	Rebuild     bool      // пересобрать _log.md с нуля
	IncludeBots bool
	NoRedact    bool // не выносить токены/ключи из переписки
	Loc         *time.Location
}

// Entry импортирует одну запись конфига.
// Возвращает (новых сообщений, новый watermark).
func Entry(ctx context.Context, client *telegram.Client, e config.Entry, opt Options, tr Transcriber, q *Queue) (int, int, error) {
	api := client.API()
	peer := tgx.InputPeer(e.Kind, e.ChatID, e.AccessHash)
	loc := opt.Loc
	if loc == nil {
		loc = time.Local
	}

	users := map[int64]*tg.User{}
	channels := map[int64]*tg.Channel{}
	chats := map[int64]*tg.Chat{}

	newLast := opt.MinID
	var msgs []*tg.Message
	offsetID := 0
	for {
		page, err := tgx.HistoryPageEx(ctx, api, peer, e.TopicID, offsetID, 100)
		if err != nil {
			return 0, opt.MinID, err
		}
		if len(page.Messages) == 0 {
			break
		}
		for _, u := range page.Users {
			if us, ok := u.(*tg.User); ok {
				users[us.ID] = us
			}
		}
		for _, c := range page.Chats {
			if ch, ok := c.(*tg.Channel); ok {
				channels[ch.ID] = ch
			}
			if ch, ok := c.(*tg.Chat); ok {
				chats[ch.ID] = ch
			}
		}
		minID := 0
		oldest := time.Time{}
		for _, mc := range page.Messages {
			m, ok := mc.(*tg.Message)
			if !ok {
				continue // сервисные (MessageService) пропускаем
			}
			if minID == 0 || m.ID < minID {
				minID = m.ID
			}
			d := time.Unix(int64(m.Date), 0).In(loc)
			if oldest.IsZero() || d.Before(oldest) {
				oldest = d
			}
			if !opt.Rebuild && opt.MinID > 0 && m.ID <= opt.MinID {
				continue // уже импортировано
			}
			if !opt.To.IsZero() && d.After(opt.To) {
				continue
			}
			if !opt.From.IsZero() && d.Before(opt.From) {
				continue
			}
			msgs = append(msgs, m)
			if m.ID > newLast {
				newLast = m.ID
			}
		}
		if minID == 0 {
			break
		}
		offsetID = minID
		// дошли до watermark — дальше только старое
		if !opt.Rebuild && opt.MinID > 0 && minID <= opt.MinID {
			break
		}
		if !opt.From.IsZero() && !oldest.IsZero() && oldest.Before(opt.From) {
			break
		}
		if len(page.Messages) < 100 {
			break
		}
	}

	if len(msgs) == 0 {
		return 0, newLast, nil
	}
	sort.Slice(msgs, func(i, j int) bool {
		if msgs[i].Date != msgs[j].Date {
			return msgs[i].Date < msgs[j].Date
		}
		return msgs[i].ID < msgs[j].ID
	})

	slug := e.Slug
	if slug == "" {
		slug = config.Slug(e.Chat, e.Topic)
	}
	logRoot := filepath.Join(opt.Out, slug)
	filesDir := filepath.Join(logRoot, "files")

	// секреты чата: токены из переписки выносим в файл, в логах — ссылки
	var sec *redact.SecretLog
	if !opt.NoRedact {
		sec = redact.LoadSecretLog(filepath.Join(logRoot, ".secrets.json"))
	}

	// группировка по локальным дням
	byDay := map[string][]*tg.Message{}
	var days []string
	for _, m := range msgs {
		day := time.Unix(int64(m.Date), 0).In(loc).Format("2006-01-02")
		if _, ok := byDay[day]; !ok {
			days = append(days, day)
		}
		byDay[day] = append(byDay[day], m)
	}
	sort.Strings(days)

	auth := &authorizer{users: users, channels: channels, chats: chats, fallback: e.Chat}

	// превью для reply-ссылок: из текущей партии + дозагрузка недостающих
	previews := map[int]string{}
	for _, m := range msgs {
		previews[m.ID] = tgx.PreviewText(m.Message)
	}
	var need []int
	seen := map[int]bool{}
	for _, m := range msgs {
		if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			if id, ok := rh.GetReplyToMsgID(); ok && id > 0 {
				if _, in := previews[id]; !in && !seen[id] {
					seen[id] = true
					need = append(need, id)
				}
			}
		}
	}
	for k, v := range tgx.MessagePreviews(ctx, api, need) {
		previews[k] = v
	}

	total := 0
	for _, day := range days {
		updDir := filepath.Join(logRoot, "upd_"+day)
		if err := os.MkdirAll(updDir, 0o755); err != nil {
			return total, newLast, err
		}
		logPath := filepath.Join(updDir, "_log.md")
		existing := map[int]bool{}
		if !opt.Rebuild {
			existing = exportedIDs(logPath)
		}
		fresh := byDay[day]
		if !opt.Rebuild {
			filtered := fresh[:0:0]
			for _, m := range fresh {
				if !existing[m.ID] {
					filtered = append(filtered, m)
				}
			}
			fresh = filtered
			if len(fresh) == 0 {
				continue
			}
		}
		header := fmt.Sprintf("# Замечания — %s\n\n**Проект**: %s\n\n---\n\n", day, e.Chat)
		flags := os.O_CREATE | os.O_WRONLY
		if opt.Rebuild {
			flags |= os.O_TRUNC
		} else {
			flags |= os.O_APPEND
		}
		fh, err := os.OpenFile(logPath, flags, 0o644)
		if err != nil {
			return total, newLast, err
		}
		if opt.Rebuild {
			if _, err := fh.WriteString(header); err != nil {
				fh.Close()
				return total, newLast, err
			}
		} else if _, err := os.Stat(logPath); os.IsNotExist(err) {
			if _, err := fh.WriteString(header); err != nil {
				fh.Close()
				return total, newLast, err
			}
		}
		for _, m := range fresh {
			section, job, err := buildSection(ctx, client, m, auth, filesDir, updDir, tr, loc, previews, q, sec)
			if err != nil {
				fh.Close()
				return total, newLast, err
			}
			if _, err := fh.WriteString(section); err != nil {
				fh.Close()
				return total, newLast, err
			}
			if job != nil {
				q.submit(*job)
			}
			total++
		}
		fh.Close()
		fmt.Printf("  %s — +%d (upd_%s)\n", slug, len(fresh), day)
	}
	if sec != nil && sec.Added() > 0 {
		if err := sec.Save(); err != nil {
			return total, newLast, err
		}
		fmt.Printf("  🔑 %s — вынесено секретов: %d (.secrets.json)\n", slug, sec.Added())
	}
	return total, newLast, nil
}

type authorizer struct {
	users    map[int64]*tg.User
	channels map[int64]*tg.Channel
	chats    map[int64]*tg.Chat
	fallback string
}

func (a *authorizer) author(m *tg.Message) string {
	switch p := m.FromID.(type) {
	case *tg.PeerUser:
		if u := a.users[p.UserID]; u != nil {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if u.Username != "" {
				return "@" + u.Username + " (" + name + ")"
			}
			if name != "" {
				return name
			}
		}
	case *tg.PeerChannel:
		if c := a.channels[p.ChannelID]; c != nil {
			return c.Title
		}
	case *tg.PeerChat:
		if c := a.chats[p.ChatID]; c != nil {
			return c.Title
		}
	}
	if m.Post {
		return a.fallback
	}
	return "unknown"
}

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
