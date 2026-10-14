package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

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
