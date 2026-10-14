package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
)

func manageCreate(ctx context.Context, client *telegram.Client, title, about, kind string, forum bool, users strList, do bool) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("create: нужен -title")
	}

	var megagroup, broadcast, basic bool
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "group", "megagroup", "supergroup", "группа", "супергруппа":
		megagroup, kind = true, "group"
	case "channel", "broadcast", "канал":
		broadcast, kind = true, "channel"
	case "chat", "basic", "обычная":
		basic, kind = true, "chat"
	default:
		return fmt.Errorf("create: неизвестный -kind %q (group|channel|chat)", kind)
	}
	if forum && !megagroup {
		return fmt.Errorf("create: -forum доступен только для -kind group")
	}

	var us []resolvedUser
	if len(users) > 0 {
		var err error
		if us, err = resolveUsers(ctx, client, users); err != nil {
			return err
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CREATE %s\nНазвание:  %s\n", kindLabel(kind), title)
	if about != "" {
		fmt.Fprintf(&b, "Описание:  %s\n", about)
	}
	if forum {
		b.WriteString("Топики:    да\n")
	}
	if len(us) > 0 {
		fmt.Fprintf(&b, "Участники: %s\n", joinUsers(us))
	}
	payload := approve.Canon("manage/create", kind, title, about, fmt.Sprint(forum), usersKey(us))
	if err := Gate(do, "manage/create", b.String(), payload); err != nil {
		return err
	}
	if !do {
		return nil
	}

	api := client.API()

	if basic {
		req := &tg.MessagesCreateChatRequest{Title: title}
		for _, u := range us {
			req.Users = append(req.Users, u.inputUser())
		}
		res, err := api.MessagesCreateChat(ctx, req)
		if err != nil {
			return err
		}
		if ch, ok := createdBasicChat(res); ok {
			fmt.Printf("✅ создана группа: %s (id=%d)\n", ch.Title, ch.ID)
		} else {
			fmt.Println("✅ группа создана")
		}
		return nil
	}

	req := &tg.ChannelsCreateChannelRequest{Title: title, About: about, Broadcast: broadcast, Megagroup: megagroup}
	upd, err := api.ChannelsCreateChannel(ctx, req)
	if err != nil {
		return err
	}
	ch, ok := createdChannel(upd)
	if !ok {
		fmt.Println("✅ создано, но id не удалось прочитать из ответа")
		return nil
	}
	chInput := &tg.InputChannel{ChannelID: ch.ID, AccessHash: ch.AccessHash}

	if len(us) > 0 {
		var ius []tg.InputUserClass
		for _, u := range us {
			ius = append(ius, u.inputUser())
		}
		if _, err := api.ChannelsInviteToChannel(ctx, &tg.ChannelsInviteToChannelRequest{Channel: chInput, Users: ius}); err != nil {
			fmt.Printf("⚠ создано, но пригласить не удалось: %v\n", err)
		} else {
			fmt.Printf("✓ приглашены: %s\n", joinUsers(us))
		}
	}
	if forum {
		if _, err := api.ChannelsToggleForum(ctx, &tg.ChannelsToggleForumRequest{Channel: chInput, Enabled: true}); err != nil {
			fmt.Printf("⚠ не удалось включить топики: %v\n", err)
		} else {
			fmt.Println("✓ топики включены")
		}
	}
	fmt.Printf("✅ создан %s: %s (id=%d)\n", kindLabel(kind), ch.Title, ch.ID)
	return nil
}

func manageTopic(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias, title string, do bool) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("topic: нужен -title")
	}
	t, err := resolveChatTarget(ctx, client, dir, chat, chatID, alias, false)
	if err != nil {
		return err
	}
	sum := fmt.Sprintf("CREATE TOPIC\nКуда:     %s\nНазвание: %s", t.Desc, title)
	payload := approve.Canon("manage/topic", peerKey(t.Peer), title)
	if err := Gate(do, "manage/topic", sum, payload); err != nil {
		return err
	}
	if !do {
		return nil
	}
	req := &tg.MessagesCreateForumTopicRequest{Peer: t.Peer, Title: title, RandomID: randomID()}
	if _, err := client.API().MessagesCreateForumTopic(ctx, req); err != nil {
		return err
	}
	fmt.Printf("✅ топик создан: %s в %s\n", title, t.Desc)
	return nil
}

func manageDelete(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias string, do bool) error {
	t, err := resolveChatTarget(ctx, client, dir, chat, chatID, alias, false)
	if err != nil {
		return err
	}
	sum := fmt.Sprintf("DELETE\nЧат: %s\n⚠ Удаление необратимо.", t.Desc)
	if err := Gate(do, "manage/delete", sum, approve.Canon("manage/delete", peerKey(t.Peer))); err != nil {
		return err
	}
	if !do {
		return nil
	}
	switch t.Peer.(type) {
	case *tg.InputPeerChannel:
		ch, _ := inputChannel(t.Peer)
		if _, err := client.API().ChannelsDeleteChannel(ctx, ch); err != nil {
			return err
		}
		fmt.Printf("✅ удалён: %s\n", t.Desc)
		return nil
	case *tg.InputPeerChat:
		p := t.Peer.(*tg.InputPeerChat)
		if _, err := client.API().MessagesDeleteChat(ctx, p.ChatID); err != nil {
			return err
		}
		fmt.Printf("✅ удалена группа: %s\n", t.Desc)
		return nil
	default:
		return fmt.Errorf("delete: %s — это не группа/канал", t.Desc)
	}
}

func kindLabel(kind string) string {
	switch kind {
	case "group":
		return "супергруппа"
	case "channel":
		return "канал"
	case "chat":
		return "группа"
	default:
		return kind
	}
}

func createdChannel(u tg.UpdatesClass) (*tg.Channel, bool) {
	for _, c := range updatesChats(u) {
		if ch, ok := c.(*tg.Channel); ok {
			return ch, true
		}
	}
	return nil, false
}

func createdBasicChat(res *tg.MessagesInvitedUsers) (*tg.Chat, bool) {
	if res == nil {
		return nil, false
	}
	for _, c := range updatesChats(res.Updates) {
		if ch, ok := c.(*tg.Chat); ok {
			return ch, true
		}
	}
	return nil, false
}

func updatesChats(u tg.UpdatesClass) []tg.ChatClass {
	switch v := u.(type) {
	case *tg.Updates:
		return v.Chats
	case *tg.UpdatesCombined:
		return v.Chats
	}
	return nil
}
