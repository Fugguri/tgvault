package cli

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// target — разрешённый получатель команды: готовый InputPeer + подпись + тип.
type target struct {
	Peer      tg.InputPeerClass
	Desc      string
	Kind      string // chat | channel | user
	Megagroup bool
}

// resolveChatTarget ищет чат по self / alias(slug) / id / части имени.
func resolveChatTarget(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias string, self bool) (target, error) {
	api := client.API()
	if self {
		return target{Peer: &tg.InputPeerSelf{}, Desc: "Saved Messages", Kind: "user"}, nil
	}
	if alias != "" {
		if cfg, _, err := config.Load(dir); err == nil {
			for _, e := range cfg.Entries {
				if e.Slug == alias || e.Chat == alias {
					return target{
						Peer: tgx.InputPeer(e.Kind, e.ChatID, e.AccessHash),
						Desc: fmt.Sprintf("%s (slug=%s)", e.Chat, e.Slug),
						Kind: e.Kind,
					}, nil
				}
			}
		}
		return target{}, fmt.Errorf("alias %q не найден в конфиге", alias)
	}
	if chatID != 0 {
		p, ok, err := tgx.Resolve(ctx, api, chatID)
		if err != nil {
			return target{}, err
		}
		if !ok {
			return target{}, fmt.Errorf("chat id=%d не найден", chatID)
		}
		return target{Peer: p.Input, Desc: fmt.Sprintf("%s (id=%d)", p.Title, p.ID), Kind: p.Kind, Megagroup: p.Megagroup}, nil
	}
	if chat == "" {
		return target{}, fmt.Errorf("не задан чат: -chat / -id / -alias")
	}
	p, ok, err := tgx.Find(ctx, api, chat)
	if err != nil {
		return target{}, err
	}
	if !ok {
		return target{}, fmt.Errorf("чат %q не найден", chat)
	}
	return target{Peer: p.Input, Desc: fmt.Sprintf("%s (id=%d)", p.Title, p.ID), Kind: p.Kind, Megagroup: p.Megagroup}, nil
}

// inputChannel превращает InputPeerChannel в InputChannel (для Channels* методов).
func inputChannel(p tg.InputPeerClass) (tg.InputChannelClass, bool) {
	if pc, ok := p.(*tg.InputPeerChannel); ok {
		return &tg.InputChannel{ChannelID: pc.ChannelID, AccessHash: pc.AccessHash}, true
	}
	return nil, false
}

// peerKey — стабильный ключ цели для хэша согласования (не зависит от имени).
func peerKey(p tg.InputPeerClass) string {
	switch v := p.(type) {
	case *tg.InputPeerSelf:
		return "self"
	case *tg.InputPeerUser:
		return fmt.Sprintf("user:%d", v.UserID)
	case *tg.InputPeerChat:
		return fmt.Sprintf("chat:%d", v.ChatID)
	case *tg.InputPeerChannel:
		return fmt.Sprintf("channel:%d", v.ChannelID)
	}
	return "unknown"
}
