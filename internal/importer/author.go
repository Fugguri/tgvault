package importer

import (
	"strings"

	"github.com/gotd/td/tg"
)

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
