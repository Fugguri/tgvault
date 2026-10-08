package tgx

import (
	"context"
	"strings"

	"github.com/gotd/td/tg"
)

// Peer — найденный диалог: готовый InputPeer + человекочитаемое имя.
type Peer struct {
	Input      tg.InputPeerClass
	ID         int64
	AccessHash int64
	Title      string
	Kind       string // user | chat | channel (storage kind)
	Megagroup  bool   // супергруппа (channel, но отображаем как group)
}

// Label — человекочитаемый тип для вывода.
func (p Peer) Label() string {
	if p.Kind == "channel" && p.Megagroup {
		return "group"
	}
	return p.Kind
}

// Topic — форум-топик супергруппы.
type Topic struct {
	ID    int
	Title string
}

// Dialogs возвращает список диалогов пользователя.
func Dialogs(ctx context.Context, api *tg.Client, limit int) ([]Peer, error) {
	res, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit:      limit,
		OffsetPeer: &tg.InputPeerEmpty{},
	})
	if err != nil {
		return nil, err
	}
	dialogs, chats, users := splitDialogs(res)
	out := make([]Peer, 0, len(dialogs))
	for _, d := range dialogs {
		if p, ok := resolvePeer(peerOf(d), chats, users); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// Find ищет диалог по подстроке имени (без учёта регистра).
func Find(ctx context.Context, api *tg.Client, substr string) (Peer, bool, error) {
	peers, err := Dialogs(ctx, api, 200)
	if err != nil {
		return Peer{}, false, err
	}
	needle := strings.ToLower(substr)
	for _, p := range peers {
		if strings.Contains(strings.ToLower(p.Title), needle) {
			return p, true, nil
		}
	}
	return Peer{}, false, nil
}

// Resolve находит диалог по raw entity id.
func Resolve(ctx context.Context, api *tg.Client, id int64) (Peer, bool, error) {
	peers, err := Dialogs(ctx, api, 200)
	if err != nil {
		return Peer{}, false, err
	}
	for _, p := range peers {
		if p.ID == id {
			return p, true, nil
		}
	}
	return Peer{}, false, nil
}

// Topics перечисляет форум-топики чата.
func Topics(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, limit int) ([]Topic, error) {
	res, err := api.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{Peer: peer, Limit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Topic, 0, len(res.Topics))
	for _, t := range res.Topics {
		if f, ok := t.(*tg.ForumTopic); ok {
			out = append(out, Topic{ID: f.ID, Title: f.Title})
		}
	}
	return out, nil
}

// Page — страница сообщений вместе с упоминаемыми users/chats.
type Page struct {
	Messages []tg.MessageClass
	Users    []tg.UserClass
	Chats    []tg.ChatClass
}

// HistoryPageEx читает страницу сообщений и отдаёт вместе с users/chats.
func HistoryPageEx(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, topicID, offsetID, limit int) (Page, error) {
	if topicID > 0 {
		res, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{
			Peer: peer, MsgID: topicID, OffsetID: offsetID, Limit: limit,
		})
		if err != nil {
			return Page{}, err
		}
		return pageOf(res), nil
	}
	res, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offsetID, Limit: limit})
	if err != nil {
		return Page{}, err
	}
	return pageOf(res), nil
}

func pageOf(res tg.MessagesMessagesClass) Page {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return Page{v.Messages, v.Users, v.Chats}
	case *tg.MessagesMessagesSlice:
		return Page{v.Messages, v.Users, v.Chats}
	case *tg.MessagesChannelMessages:
		return Page{v.Messages, v.Users, v.Chats}
	}
	return Page{}
}

// History читает последние limit сообщений чата (по убыванию даты).
func History(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, limit int) ([]tg.MessageClass, error) {
	p, err := HistoryPageEx(ctx, api, peer, 0, 0, limit)
	if err != nil {
		return nil, err
	}
	return p.Messages, nil
}

// HistoryPage читает страницу сообщений чата (по убыванию даты).
func HistoryPage(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, topicID, offsetID, limit int) ([]tg.MessageClass, error) {
	p, err := HistoryPageEx(ctx, api, peer, topicID, offsetID, limit)
	if err != nil {
		return nil, err
	}
	return p.Messages, nil
}

// InputPeer строит InputPeer из сохранённых в конфиге kind/id/access_hash.
func InputPeer(kind string, id, accessHash int64) tg.InputPeerClass {
	switch kind {
	case "user":
		return &tg.InputPeerUser{UserID: id, AccessHash: accessHash}
	case "chat":
		return &tg.InputPeerChat{ChatID: id}
	default: // channel (broadcast или супергруппа)
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: accessHash}
	}
}

// MessagePreviews дозагружает сообщения по id и отдаёт их текстовые превью.
// Нужно для reply-ссылок на сообщения вне окна (напр. корень топика).
func MessagePreviews(ctx context.Context, api *tg.Client, ids []int) map[int]string {
	if len(ids) == 0 {
		return nil
	}
	inputs := make([]tg.InputMessageClass, 0, len(ids))
	for _, id := range ids {
		inputs = append(inputs, &tg.InputMessageID{ID: id})
	}
	res, err := api.MessagesGetMessages(ctx, inputs)
	if err != nil {
		return nil
	}
	out := map[int]string{}
	for _, mc := range messagesOf(res) {
		if m, ok := mc.(*tg.Message); ok {
			out[m.ID] = PreviewText(m.Message)
		}
	}
	return out
}

// PreviewText делает короткое превью текста (первая строка, обрезка).
func PreviewText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return s
}

func splitDialogs(res tg.MessagesDialogsClass) ([]tg.DialogClass, []tg.ChatClass, []tg.UserClass) {
	switch v := res.(type) {
	case *tg.MessagesDialogs:
		return v.Dialogs, v.Chats, v.Users
	case *tg.MessagesDialogsSlice:
		return v.Dialogs, v.Chats, v.Users
	}
	return nil, nil, nil
}

func messagesOf(res tg.MessagesMessagesClass) []tg.MessageClass {
	switch v := res.(type) {
	case *tg.MessagesMessages:
		return v.Messages
	case *tg.MessagesMessagesSlice:
		return v.Messages
	case *tg.MessagesChannelMessages:
		return v.Messages
	}
	return nil
}

func peerOf(d tg.DialogClass) tg.PeerClass {
	if dlg, ok := d.(*tg.Dialog); ok {
		return dlg.Peer
	}
	return nil
}

func resolvePeer(p tg.PeerClass, chats []tg.ChatClass, users []tg.UserClass) (Peer, bool) {
	switch v := p.(type) {
	case *tg.PeerChannel:
		for _, c := range chats {
			if ch, ok := c.(*tg.Channel); ok && ch.ID == v.ChannelID {
				return Peer{
					Input:      &tg.InputPeerChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash},
					ID:         ch.ID,
					AccessHash: ch.AccessHash,
					Title:      ch.Title,
					Kind:       "channel",
					Megagroup:  ch.Megagroup,
				}, true
			}
		}
	case *tg.PeerChat:
		for _, c := range chats {
			if ch, ok := c.(*tg.Chat); ok && ch.ID == v.ChatID {
				return Peer{Input: &tg.InputPeerChat{ChatID: ch.ID}, ID: ch.ID, Title: ch.Title, Kind: "chat"}, true
			}
		}
	case *tg.PeerUser:
		for _, u := range users {
			if us, ok := u.(*tg.User); ok && us.ID == v.UserID {
				return Peer{
					Input:      &tg.InputPeerUser{UserID: us.ID, AccessHash: us.AccessHash},
					ID:         us.ID,
					AccessHash: us.AccessHash,
					Title:      strings.TrimSpace(us.FirstName + " " + us.LastName),
					Kind:       "user",
				}, true
			}
		}
	}
	return Peer{}, false
}
