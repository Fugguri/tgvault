package cli

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
	"github.com/Fugguri/tgvault/internal/tgx"
)

// ViewOpts — параметры команды view: быстрый просмотр сообщений за период
// напрямую из Telegram, без импорта (на диск ничего не пишется).
type ViewOpts struct {
	// цель: -entry (запись конфига) или -chat/-id/-alias/-self
	Chat   string
	ChatID int64
	Alias  string
	Self   bool
	Entry  string
	Topic  int

	From, To time.Time // границы (нулевое = без границы); To включается в период
	Last     int       // только последние N сообщений

	MaxLen  int // обрезка текста одного сообщения (символов, 0 — без обрезки)
	MaxMsgs int // максимум сообщений в выводе (0 — без лимита)

	Media      bool // скачать медиа в кэш и печатать пути (иначе только пометки)
	Transcribe bool // транскрибировать голосовые (требует Tr)
	Tr         Transcriber
}

// View читает сообщения за период и печатает их текстом для агента.
// Ничего не импортирует: медиа не качается (кроме -media), логи не пишутся.
func View(ctx context.Context, client *telegram.Client, dir string, o ViewOpts) error {
	peer, desc, topic, err := resolveViewTarget(ctx, client, dir, o)
	if err != nil {
		return err
	}

	from, to := o.From, o.To
	if from.IsZero() && to.IsZero() && o.Last == 0 {
		to = time.Now()
		from = to.AddDate(0, 0, -6) // последние 7 дней
	}
	var upper time.Time
	if !to.IsZero() {
		upper = to.AddDate(0, 0, 1) // -to включительно (по конец дня)
	}
	loc := time.Local
	api := client.API()

	users := map[int64]*tg.User{}
	channels := map[int64]*tg.Channel{}
	chats := map[int64]*tg.Chat{}

	var msgs []*tg.Message
	offsetID := 0
	for {
		page, err := tgx.HistoryPageEx(ctx, api, peer, topic, offsetID, 100)
		if err != nil {
			return err
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
				continue
			}
			if minID == 0 || m.ID < minID {
				minID = m.ID
			}
			d := time.Unix(int64(m.Date), 0).In(loc)
			if oldest.IsZero() || d.Before(oldest) {
				oldest = d
			}
			if !to.IsZero() && !d.Before(upper) {
				continue
			}
			if !from.IsZero() && d.Before(from) {
				continue
			}
			msgs = append(msgs, m)
		}
		if minID == 0 {
			break
		}
		offsetID = minID
		if o.Last > 0 && len(msgs) >= o.Last {
			break
		}
		if !from.IsZero() && !oldest.IsZero() && oldest.Before(from) {
			break
		}
		if len(page.Messages) < 100 {
			break
		}
	}

	sort.Slice(msgs, func(i, j int) bool {
		if msgs[i].Date != msgs[j].Date {
			return msgs[i].Date < msgs[j].Date
		}
		return msgs[i].ID < msgs[j].ID
	})
	if o.Last > 0 && len(msgs) > o.Last {
		msgs = msgs[len(msgs)-o.Last:]
	}
	if o.MaxMsgs > 0 && len(msgs) > o.MaxMsgs {
		fmt.Fprintf(os.Stderr, "• показаны последние %d из %d (лимит -max-msgs)\n", o.MaxMsgs, len(msgs))
		msgs = msgs[len(msgs)-o.MaxMsgs:]
	}
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "нет сообщений за указанный период")
		return nil
	}

	// превью для reply-ссылок (одна дозагрузка на все ответы)
	previews := map[int]string{}
	seen := map[int]bool{}
	var need []int
	for _, m := range msgs {
		if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			if id, ok := rh.GetReplyToMsgID(); ok && id > 0 && !seen[id] {
				seen[id] = true
				need = append(need, id)
			}
		}
	}
	for k, v := range tgx.MessagePreviews(ctx, api, need) {
		previews[k] = v
	}

	var cacheDir string
	if o.Media {
		if c, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(c, "tgvault", "view")
			_ = os.MkdirAll(cacheDir, 0o700)
		} else {
			cacheDir = os.TempDir()
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s", desc)
	if topic > 0 {
		fmt.Fprintf(&b, " · топик %d", topic)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "период: %s … %s · сообщений: %d\n",
		dayOf(msgs[0].Date, loc), dayOf(msgs[len(msgs)-1].Date, loc), len(msgs))

	lastDay := ""
	for _, m := range msgs {
		day := dayOf(m.Date, loc)
		if day != lastDay {
			fmt.Fprintf(&b, "\n## %s\n", day)
			lastDay = day
		}
		at := time.Unix(int64(m.Date), 0).In(loc).Format("15:04")
		fmt.Fprintf(&b, "\n**%s — %s**\n", at, viewAuthor(m, users, channels, chats, desc))
		if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			if id, ok := rh.GetReplyToMsgID(); ok && id > 0 {
				if pv := previews[id]; pv != "" {
					fmt.Fprintf(&b, "↩ msg:%d — «%s»\n", id, pv)
				} else {
					fmt.Fprintf(&b, "↩ msg:%d\n", id)
				}
			}
		}
		if text := truncateText(m.Message, o.MaxLen); text != "" {
			b.WriteString(text)
			b.WriteByte('\n')
		}
		if line := renderMedia(ctx, client, m, cacheDir, o); line != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	fmt.Print(b.String())
	fmt.Fprintf(os.Stderr, "• %s: %d сообщений\n", desc, len(msgs))
	return nil
}

// resolveViewTarget определяет цель: запись конфига (-entry) либо чат/-id/-alias/-self.
func resolveViewTarget(ctx context.Context, client *telegram.Client, dir string, o ViewOpts) (tg.InputPeerClass, string, int, error) {
	if o.Entry != "" {
		cfg, _, err := config.Load(dir)
		if err != nil {
			return nil, "", 0, fmt.Errorf("конфиг не найден: %w", err)
		}
		e, ok := findEntry(cfg, o.Entry)
		if !ok {
			return nil, "", 0, fmt.Errorf("запись %q не найдена в конфиге (см. tgvault list)", o.Entry)
		}
		e = ensurePeer(ctx, client, e)
		return tgx.InputPeer(e.Kind, e.ChatID, e.AccessHash),
			fmt.Sprintf("%s (slug=%s)", e.Chat, e.Slug), e.TopicID, nil
	}
	t, err := resolveChatTarget(ctx, client, dir, o.Chat, o.ChatID, o.Alias, o.Self)
	if err != nil {
		return nil, "", 0, err
	}
	return t.Peer, t.Desc, o.Topic, nil
}

// findEntry ищет запись по точному slug, затем по вхождению в slug/имя чата.
func findEntry(cfg *config.Config, needle string) (config.Entry, bool) {
	for _, e := range cfg.Entries {
		if e.Slug == needle {
			return e, true
		}
	}
	n := strings.ToLower(needle)
	for _, e := range cfg.Entries {
		if strings.Contains(strings.ToLower(e.Slug), n) || strings.Contains(strings.ToLower(e.Chat), n) {
			return e, true
		}
	}
	return config.Entry{}, false
}

func dayOf(unix int, loc *time.Location) string {
	return time.Unix(int64(unix), 0).In(loc).Format("2006-01-02")
}

func viewAuthor(m *tg.Message, users map[int64]*tg.User, channels map[int64]*tg.Channel, chats map[int64]*tg.Chat, fallback string) string {
	switch p := m.FromID.(type) {
	case *tg.PeerUser:
		if u := users[p.UserID]; u != nil {
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if u.Username != "" {
				return "@" + u.Username + " (" + name + ")"
			}
			if name != "" {
				return name
			}
		}
	case *tg.PeerChannel:
		if c := channels[p.ChannelID]; c != nil {
			return c.Title
		}
	case *tg.PeerChat:
		if c := chats[p.ChatID]; c != nil {
			return c.Title
		}
	}
	if m.Post {
		return fallback
	}
	return "unknown"
}

// truncateText обрезает длинный текст, помечая, сколько символов скрыто.
func truncateText(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + fmt.Sprintf("…(+%d симв.)", len(r)-max)
}

// mediaInfo описывает вложение сообщения.
type mediaInfo struct {
	kind  string // photo|voice|video|video_note|audio|sticker|gif|document|geo|contact|poll|webpage
	label string // длительность («0:12») или имя файла
}

func mediaOf(m *tg.Message) (mediaInfo, bool) {
	switch md := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		return mediaInfo{kind: "photo"}, true
	case *tg.MessageMediaDocument:
		doc, ok := md.Document.(*tg.Document)
		if !ok {
			return mediaInfo{}, false
		}
		if md.Voice {
			return mediaInfo{kind: "voice", label: attrDuration(doc)}, true
		}
		for _, a := range doc.Attributes {
			switch at := a.(type) {
			case *tg.DocumentAttributeVideo:
				if at.RoundMessage {
					return mediaInfo{kind: "video_note", label: durString(at.Duration)}, true
				}
				return mediaInfo{kind: "video", label: durString(at.Duration)}, true
			case *tg.DocumentAttributeAudio:
				return mediaInfo{kind: "audio", label: durString(float64(at.Duration))}, true
			case *tg.DocumentAttributeSticker:
				return mediaInfo{kind: "sticker"}, true
			case *tg.DocumentAttributeAnimated:
				return mediaInfo{kind: "gif"}, true
			}
		}
		return mediaInfo{kind: "document", label: viewDocName(doc)}, true
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive:
		return mediaInfo{kind: "geo"}, true
	case *tg.MessageMediaContact:
		return mediaInfo{kind: "contact"}, true
	case *tg.MessageMediaPoll:
		return mediaInfo{kind: "poll"}, true
	case *tg.MessageMediaWebPage:
		return mediaInfo{kind: "webpage"}, true
	}
	return mediaInfo{}, false
}

// renderMedia печатает строку про вложение: пометку либо (с -media) путь к файлу.
func renderMedia(ctx context.Context, client *telegram.Client, m *tg.Message, cacheDir string, o ViewOpts) string {
	info, ok := mediaOf(m)
	if !ok {
		return ""
	}
	marker := mediaMarker(info)
	if !o.Media || !downloadable(info.kind) {
		return marker
	}
	path, err := downloadMedia(ctx, client, m, info, cacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ медиа msg:%d: %v\n", m.ID, err)
		return marker
	}
	switch info.kind {
	case "photo":
		return fmt.Sprintf("![фото](%s)", path)
	case "voice":
		if o.Transcribe && o.Tr != nil {
			if txt, err := o.Tr(ctx, path); err == nil && txt != "" {
				return fmt.Sprintf("🎤 %s\n[🔊 %s]", txt, path)
			}
		}
		return fmt.Sprintf("🎤 [🔊 голосовое %s](%s)", info.label, path)
	case "document":
		label := info.label
		if label == "" {
			label = filepath.Base(path)
		}
		return fmt.Sprintf("📎 [%s](%s)", label, path)
	default:
		return fmt.Sprintf("%s [%s](%s)", marker, filepath.Base(path), path)
	}
}

// downloadable — виды вложений, которые можно сохранить файлом.
func downloadable(kind string) bool {
	switch kind {
	case "photo", "voice", "video", "video_note", "audio", "sticker", "gif", "document":
		return true
	}
	return false
}

func mediaMarker(info mediaInfo) string {
	switch info.kind {
	case "photo":
		return "🖼 [фото]"
	case "voice":
		return "🎤 [голосовое " + info.label + "]"
	case "video":
		return "🎬 [видео " + info.label + "]"
	case "video_note":
		return "⭕ [видео-сообщение]"
	case "audio":
		return "🎵 [аудио " + info.label + "]"
	case "sticker":
		return "🧩 [стикер]"
	case "gif":
		return "🎞 [gif]"
	case "document":
		if info.label != "" {
			return "📎 [документ: " + info.label + "]"
		}
		return "📎 [документ]"
	case "geo":
		return "📍 [гео]"
	case "contact":
		return "👤 [контакт]"
	case "poll":
		return "📊 [опрос]"
	case "webpage":
		return ""
	}
	return ""
}

// downloadMedia сохраняет вложение в кэш и возвращает абсолютный путь.
func downloadMedia(ctx context.Context, client *telegram.Client, m *tg.Message, info mediaInfo, cacheDir string) (string, error) {
	switch md := m.Media.(type) {
	case *tg.MessageMediaPhoto:
		ph, ok := md.Photo.(*tg.Photo)
		if !ok {
			return "", fmt.Errorf("photo: неожиданный тип")
		}
		path := filepath.Join(cacheDir, fmt.Sprintf("%d_photo.jpg", m.ID))
		if !fileExists(path) {
			loc := &tg.InputPhotoFileLocation{ID: ph.ID, AccessHash: ph.AccessHash, FileReference: ph.FileReference, ThumbSize: viewThumb(ph)}
			if _, err := client.Download(loc).ToPath(ctx, path); err != nil {
				return "", err
			}
		}
		return path, nil
	case *tg.MessageMediaDocument:
		doc, ok := md.Document.(*tg.Document)
		if !ok {
			return "", fmt.Errorf("document: неожиданный тип")
		}
		name := viewDocName(doc)
		if name == "" {
			name = fmt.Sprintf("doc_%d%s", m.ID, viewExtByMime(doc.MimeType))
		}
		if info.kind == "voice" {
			name = fmt.Sprintf("voice_%d.ogg", m.ID)
		}
		path := filepath.Join(cacheDir, fmt.Sprintf("%d_%s", m.ID, name))
		if !fileExists(path) {
			loc := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
			if _, err := client.Download(loc).ToPath(ctx, path); err != nil {
				return "", err
			}
		}
		return path, nil
	}
	return "", fmt.Errorf("нет файла для %s", info.kind)
}

func attrDuration(doc *tg.Document) string {
	for _, a := range doc.Attributes {
		if at, ok := a.(*tg.DocumentAttributeAudio); ok {
			return durString(float64(at.Duration))
		}
	}
	return ""
}

func durString(sec float64) string {
	s := int(sec + 0.5)
	if s <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func viewDocName(doc *tg.Document) string {
	for _, a := range doc.Attributes {
		if f, ok := a.(*tg.DocumentAttributeFilename); ok {
			return f.FileName
		}
	}
	return ""
}

func viewThumb(ph *tg.Photo) string {
	best, bestSize := "y", 0
	for _, s := range ph.Sizes {
		if ps, ok := s.(*tg.PhotoSize); ok && ps.Size > bestSize {
			best, bestSize = ps.Type, ps.Size
		}
	}
	return best
}

func viewExtByMime(mime string) string {
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
	case "audio/ogg":
		return ".ogg"
	}
	return ""
}
