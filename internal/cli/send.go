package cli

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
)

// SendOpts — параметры команды send.
type SendOpts struct {
	Chat   string
	ChatID int64
	Alias  string
	Self   bool // Saved Messages (Избранное)

	Text     string
	TextFile string
	Files    []string

	Topic   int
	ReplyTo int
	Edit    int
	Delete  []int
	Do      bool // --send: выполнить (иначе dry-run)
}

// Send отправляет/редактирует/удаляет сообщения. По умолчанию dry-run.
func Send(ctx context.Context, client *telegram.Client, dir string, o SendOpts) error {
	text, err := resolveText(o)
	if err != nil {
		return err
	}

	peer, desc, err := resolveTarget(ctx, client, dir, o)
	if err != nil {
		return err
	}

	switch {
	case len(o.Delete) > 0:
		payload := approve.Canon("delete", peerKey(peer), intsKey(o.Delete))
		if err := Gate(o.Do, "send/delete", preview("DELETE", desc, "", nil, 0, 0, 0, o.Delete), payload); err != nil {
			return err
		}
		if !o.Do {
			return nil
		}
		if _, err := client.API().MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{Revoke: true, ID: o.Delete}); err != nil {
			return err
		}
		fmt.Printf("✅ удалено: %v\n", o.Delete)
		return nil

	case o.Edit != 0:
		payload := approve.Canon("edit", peerKey(peer), strconv.Itoa(o.Edit), text)
		if err := Gate(o.Do, "send/edit", preview("EDIT", desc, text, nil, 0, 0, o.Edit, nil), payload); err != nil {
			return err
		}
		if !o.Do {
			return nil
		}
		req := &tg.MessagesEditMessageRequest{Peer: peer, ID: o.Edit}
		req.SetMessage(text)
		if _, err := client.API().MessagesEditMessage(ctx, req); err != nil {
			return err
		}
		fmt.Printf("✅ отредактировано msg_id=%d\n", o.Edit)
		return nil

	default:
		if text == "" && len(o.Files) == 0 {
			return fmt.Errorf("нечего делать: нужен -text/-text-file/-file, либо -edit, либо -delete")
		}
		payload := approve.Canon("send", peerKey(peer), text, filesKey(o.Files), strconv.Itoa(o.ReplyTo), strconv.Itoa(o.Topic))
		if err := Gate(o.Do, "send", preview("SEND", desc, text, o.Files, o.ReplyTo, o.Topic, 0, nil), payload); err != nil {
			return err
		}
		if !o.Do {
			return nil
		}
		reply := replyTo(o)
		api := client.API()

		if len(o.Files) == 0 {
			if _, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
				Peer: peer, Message: text, RandomID: randomID(), ReplyTo: reply,
			}); err != nil {
				return err
			}
			fmt.Printf("✅ отправлено%s\n", lastID(ctx, client, peer))
			return nil
		}

		up := uploader.NewUploader(api)
		for i, path := range o.Files {
			file, err := up.FromPath(ctx, path)
			if err != nil {
				return fmt.Errorf("upload %s: %w", path, err)
			}
			caption := ""
			if i == 0 {
				caption = text
			}
			media := &tg.InputMediaUploadedDocument{
				File:       file,
				MimeType:   mimeByExt(path),
				Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: filepath.Base(path)}},
			}
			if _, err := api.MessagesSendMedia(ctx, &tg.MessagesSendMediaRequest{
				Peer: peer, Media: media, Message: caption, RandomID: randomID(), ReplyTo: reply,
			}); err != nil {
				return fmt.Errorf("send %s: %w", path, err)
			}
		}
		fmt.Printf("✅ отправлено файлов: %d%s\n", len(o.Files), lastID(ctx, client, peer))
		return nil
	}
}

func resolveText(o SendOpts) (string, error) {
	if o.TextFile != "" {
		b, err := os.ReadFile(o.TextFile)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	if o.Text == "-" {
		b, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return o.Text, nil
}

func replyTo(o SendOpts) tg.InputReplyToClass {
	if o.ReplyTo > 0 {
		return &tg.InputReplyToMessage{ReplyToMsgID: o.ReplyTo}
	}
	if o.Topic > 0 {
		return &tg.InputReplyToMessage{ReplyToMsgID: o.Topic}
	}
	return nil
}

func resolveTarget(ctx context.Context, client *telegram.Client, dir string, o SendOpts) (tg.InputPeerClass, string, error) {
	t, err := resolveChatTarget(ctx, client, dir, o.Chat, o.ChatID, o.Alias, o.Self)
	if err != nil {
		return nil, "", err
	}
	return t.Peer, t.Desc, nil
}

func preview(action, desc, text string, files []string, replyTo, topic, edit int, del []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nDRY-RUN: %s\nКуда:    %s\n", strings.Repeat("=", 60), action, desc)
	if topic > 0 {
		fmt.Fprintf(&b, "Топик:   %d\n", topic)
	}
	if replyTo > 0 {
		fmt.Fprintf(&b, "Reply:   msg_id=%d\n", replyTo)
	}
	if edit > 0 {
		fmt.Fprintf(&b, "Edit:    msg_id=%d\n", edit)
	}
	if len(del) > 0 {
		fmt.Fprintf(&b, "Delete:  %v\n", del)
	}
	if len(files) > 0 {
		fmt.Fprintf(&b, "Файлы:   %v\n", files)
	}
	if text != "" {
		fmt.Fprintf(&b, "%s\n%s\n", strings.Repeat("-", 60), text)
	}
	b.WriteString(strings.Repeat("=", 60))
	return b.String()
}

func intsKey(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}

func filesKey(paths []string) string {
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if fi, err := os.Stat(p); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", abs, fi.Size(), fi.ModTime().Unix()))
		} else {
			parts = append(parts, abs)
		}
	}
	return strings.Join(parts, ",")
}

func randomID() int64 {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.LittleEndian.Uint64(b[:]) & 0x7fffffffffffffff)
}

func lastID(ctx context.Context, client *telegram.Client, peer tg.InputPeerClass) string {
	msgs, _ := history(ctx, client, peer, 1)
	if len(msgs) == 0 {
		return ""
	}
	return fmt.Sprintf(" msg_id=%d", msgs[0].ID)
}

func mimeByExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	}
	return "application/octet-stream"
}
