package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/gotd/td/telegram"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// AddOpts — параметры неинтерактивного добавления записи в конфиг.
type AddOpts struct {
	Chat    string
	ChatID  int64
	Topic   int
	Default bool
	Slug    string
	Note    string
	When    string
	WriteTo string
	Out     string
}

// Add добавляет чат/топик в конфиг проекта (создаёт конфиг, если его нет).
// Неинтерактивно — чтобы это мог делать агент.
func Add(ctx context.Context, client *telegram.Client, dir string, o AddOpts) error {
	api := client.API()
	var (
		p   tgx.Peer
		ok  bool
		err error
	)
	if o.ChatID != 0 {
		p, ok, err = tgx.Resolve(ctx, api, o.ChatID)
	} else if o.Chat != "" {
		p, ok, err = tgx.Find(ctx, api, o.Chat)
	} else {
		return fmt.Errorf("укажи чат: -chat <имя> или -id <id>")
	}
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("чат не найден: %s", o.Chat)
	}

	e := config.Entry{
		Chat: p.Title, ChatID: p.ID, AccessHash: p.AccessHash, Kind: p.Kind,
		Default: o.Default, Note: o.Note, When: o.When, WriteTo: o.WriteTo,
	}
	if o.Topic > 0 {
		e.TopicID = o.Topic
		if topics, err := tgx.Topics(ctx, api, p.Input, 200); err == nil {
			for _, t := range topics {
				if t.ID == o.Topic {
					e.Topic = t.Title
				}
			}
		}
	}
	e.Slug = o.Slug
	if e.Slug == "" {
		e.Slug = config.Slug(p.Title, e.Topic)
	}

	cfg, _, err := config.Load(dir)
	if err != nil {
		out := o.Out
		if out == "" {
			out = "docs/telegram_chats"
		}
		cfg = &config.Config{Project: filepath.Base(dir), Out: out}
	}
	for _, x := range cfg.Entries {
		if x.ChatID == e.ChatID && x.TopicID == e.TopicID {
			return fmt.Errorf("уже есть: %s", x.Slug)
		}
	}
	cfg.Entries = append(cfg.Entries, e)
	path, err := config.Save(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("✓ добавлено: %s (%s%s) default=%v → %s\n",
		e.Slug, e.Chat, topicSuffix(e.Topic), e.Default, path)
	ensureGitignore(dir, cfg.Out)
	return nil
}

func topicSuffix(topic string) string {
	if topic == "" {
		return ""
	}
	return ", " + topic
}
