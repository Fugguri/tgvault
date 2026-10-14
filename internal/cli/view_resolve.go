package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

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
