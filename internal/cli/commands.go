package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/tgx"
)

// Dialogs печатает список диалогов пользователя.
func Dialogs(ctx context.Context, client *telegram.Client) error {
	peers, err := tgx.Dialogs(ctx, client.API(), 200)
	if err != nil {
		return err
	}
	for _, p := range peers {
		fmt.Printf("%-8s  id=%-14d  %s\n", p.Label(), p.ID, p.Title)
	}
	return nil
}

// Topics печатает форум-топики чата.
func Topics(ctx context.Context, client *telegram.Client, needle string) error {
	peer, ok, err := tgx.Find(ctx, client.API(), needle)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("чат %q не найден", needle)
	}
	fmt.Printf("Чат: %s (%s)\n", peer.Title, peer.Label())
	topics, err := tgx.Topics(ctx, client.API(), peer.Input, 200)
	if err != nil {
		return err
	}
	if len(topics) == 0 {
		fmt.Println("Это не форум (топиков нет)")
		return nil
	}
	for _, t := range topics {
		fmt.Printf("%6d  %s\n", t.ID, t.Title)
	}
	return nil
}

// Voice находит чат по имени, берёт первое голосовое из последних 100
// сообщений (или из топика topicID), скачивает и (если задан transcriber)
// транскрибирует.
func Voice(ctx context.Context, client *telegram.Client, tr Transcriber, needle string, topicID int) error {
	peer, ok, err := tgx.Find(ctx, client.API(), needle)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("чат %q не найден", needle)
	}
	fmt.Printf("Чат: %s (%s)\n", peer.Title, peer.Label())

	var msgs []tg.MessageClass
	if topicID > 0 {
		fmt.Printf("Топик: %d\n", topicID)
		msgs, err = tgx.HistoryPage(ctx, client.API(), peer.Input, topicID, 0, 100)
	} else {
		msgs, err = tgx.History(ctx, client.API(), peer.Input, 100)
	}
	if err != nil {
		return err
	}
	for _, mc := range msgs {
		m, ok := mc.(*tg.Message)
		if !ok {
			continue
		}
		media, ok := m.Media.(*tg.MessageMediaDocument)
		if !ok || !media.Voice {
			continue
		}
		doc, ok := media.Document.(*tg.Document)
		if !ok {
			continue
		}
		fmt.Printf("Голосовое: msg:%d  %d bytes  mime=%s\n", m.ID, doc.Size, doc.MimeType)

		out := fmt.Sprintf("voice_%d.ogg", m.ID)
		loc := &tg.InputDocumentFileLocation{ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}
		if _, err := client.Download(loc).ToPath(ctx, out); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		fmt.Printf("Скачано: %s\n", out)

		if tr == nil {
			fmt.Println("транскрибация не настроена — пропускаю")
			return nil
		}
		t0 := time.Now()
		text, err := tr(ctx, out)
		if err != nil {
			return fmt.Errorf("транскрибация: %w", err)
		}
		fmt.Printf("Транскрипт (%.1fs): %s\n", time.Since(t0).Seconds(), text)
		return nil
	}
	fmt.Printf("Голосовых в последних 100 сообщениях не найдено (прочитано %d)\n", len(msgs))
	return nil
}
