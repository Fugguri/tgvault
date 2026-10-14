package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
	"github.com/Fugguri/tgvault/internal/tgx"
)

func manageInvite(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias string, self bool, users strList, fwdLimit int, do bool) error {
	t, err := resolveChatTarget(ctx, client, dir, chat, chatID, alias, false)
	if err != nil {
		return err
	}
	us, err := resolveUsers(ctx, client, users)
	if err != nil {
		return err
	}

	sum := fmt.Sprintf("INVITE\nКуда:  %s\nКого:  %s", t.Desc, joinUsers(us))
	if err := Gate(do, "manage/invite", sum, approve.Canon("manage/invite", peerKey(t.Peer), usersKey(us), strconv.Itoa(fwdLimit))); err != nil {
		return err
	}
	if !do {
		return nil
	}

	switch p := t.Peer.(type) {
	case *tg.InputPeerChannel:
		ch, _ := inputChannel(t.Peer)
		var ius []tg.InputUserClass
		for _, u := range us {
			ius = append(ius, u.inputUser())
		}
		res, err := client.API().ChannelsInviteToChannel(ctx, &tg.ChannelsInviteToChannelRequest{Channel: ch, Users: ius})
		if err != nil {
			return err
		}
		for _, u := range us {
			fmt.Printf("✅ приглашён: %s\n", u)
		}
		for _, mi := range res.MissingInvitees {
			fmt.Printf("⚠ не приглашён id=%d: %s\n", mi.UserID, missingReason(mi))
		}
		return nil

	case *tg.InputPeerChat:
		for _, u := range us {
			if _, err := client.API().MessagesAddChatUser(ctx, &tg.MessagesAddChatUserRequest{
				ChatID: p.ChatID, UserID: u.inputUser(), FwdLimit: fwdLimit,
			}); err != nil {
				return fmt.Errorf("добавить %s: %w", u, err)
			}
			fmt.Printf("✅ добавлен: %s\n", u)
		}
		return nil

	default:
		return fmt.Errorf("invite: %s — это не группа/канал", t.Desc)
	}
}

func manageKick(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias string, users strList, ban bool, days int, do bool) error {
	t, err := resolveChatTarget(ctx, client, dir, chat, chatID, alias, false)
	if err != nil {
		return err
	}
	us, err := resolveUsers(ctx, client, users)
	if err != nil {
		return err
	}

	verb := "убрать"
	if ban {
		verb = "забанить"
	}
	until := 0
	if ban && days > 0 {
		until = int(time.Now().Add(time.Duration(days) * 24 * time.Hour).Unix())
	}

	var b strings.Builder
	fmt.Fprintf(&b, "KICK\nГде:  %s\nКого: %s\nДействие: %s\n", t.Desc, joinUsers(us), verb)
	if ban {
		if days > 0 {
			fmt.Fprintf(&b, "Срок: %d дн.\n", days)
		} else {
			b.WriteString("Срок: навсегда\n")
		}
	}
	payload := approve.Canon("manage/kick", peerKey(t.Peer), usersKey(us), fmt.Sprint(ban), strconv.Itoa(days))
	if err := Gate(do, "manage/kick", b.String(), payload); err != nil {
		return err
	}
	if !do {
		return nil
	}

	switch p := t.Peer.(type) {
	case *tg.InputPeerChannel:
		ch, _ := inputChannel(t.Peer)
		for _, u := range us {
			banReq := &tg.ChannelsEditBannedRequest{
				Channel: ch, Participant: u.inputPeer(),
				BannedRights: tg.ChatBannedRights{ViewMessages: true, UntilDate: until},
			}
			if _, err := client.API().ChannelsEditBanned(ctx, banReq); err != nil {
				return fmt.Errorf("%s %s: %w", verb, u, err)
			}
			if !ban {
				// снять бан, чтобы человек мог вернуться по ссылке
				if _, err := client.API().ChannelsEditBanned(ctx, &tg.ChannelsEditBannedRequest{
					Channel: ch, Participant: u.inputPeer(), BannedRights: tg.ChatBannedRights{},
				}); err != nil {
					return fmt.Errorf("снять бан %s: %w", u, err)
				}
			}
			fmt.Printf("✅ %s: %s\n", verb, u)
		}
		return nil

	case *tg.InputPeerChat:
		for _, u := range us {
			if _, err := client.API().MessagesDeleteChatUser(ctx, &tg.MessagesDeleteChatUserRequest{
				ChatID: p.ChatID, UserID: u.inputUser(),
			}); err != nil {
				return fmt.Errorf("удалить %s: %w", u, err)
			}
			fmt.Printf("✅ удалён: %s\n", u)
		}
		return nil

	default:
		return fmt.Errorf("kick: %s — это не группа/канал", t.Desc)
	}
}

// --- пользователи ---

type resolvedUser struct {
	ID         int64
	AccessHash int64
	Username   string
	Name       string
}

func (u resolvedUser) inputUser() tg.InputUserClass {
	return &tg.InputUser{UserID: u.ID, AccessHash: u.AccessHash}
}

func (u resolvedUser) inputPeer() tg.InputPeerClass {
	return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}
}

func (u resolvedUser) String() string {
	name := u.Name
	switch {
	case u.Username != "" && name != "":
		return fmt.Sprintf("@%s (%s, id=%d)", u.Username, name, u.ID)
	case u.Username != "":
		return fmt.Sprintf("@%s (id=%d)", u.Username, u.ID)
	case name != "":
		return fmt.Sprintf("%s (id=%d)", name, u.ID)
	default:
		return fmt.Sprintf("id=%d", u.ID)
	}
}

func resolveUsers(ctx context.Context, client *telegram.Client, refs []string) ([]resolvedUser, error) {
	var out []resolvedUser
	for _, r := range refs {
		for _, part := range strings.Split(r, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			u, err := resolveUser(ctx, client, part)
			if err != nil {
				return nil, err
			}
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("не заданы пользователи (-user @name|id|+phone)")
	}
	return out, nil
}

func resolveUser(ctx context.Context, client *telegram.Client, ref string) (resolvedUser, error) {
	api := client.API()
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "+"):
		return resolveByPhone(ctx, api, ref)
	case strings.HasPrefix(ref, "@"):
		return resolveByUsername(ctx, api, strings.TrimPrefix(ref, "@"))
	case isDigits(ref):
		id, _ := strconv.ParseInt(ref, 10, 64)
		return resolveByID(ctx, api, id)
	default:
		return resolveByUsername(ctx, api, strings.TrimPrefix(ref, "@"))
	}
}

func resolveByUsername(ctx context.Context, api *tg.Client, username string) (resolvedUser, error) {
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: username})
	if err != nil {
		return resolvedUser{}, fmt.Errorf("@%s: %w", username, err)
	}
	u, ok := userFromResolved(res)
	if !ok {
		return resolvedUser{}, fmt.Errorf("@%s — не пользователь", username)
	}
	return u, nil
}

func resolveByPhone(ctx context.Context, api *tg.Client, phone string) (resolvedUser, error) {
	res, err := api.ContactsResolvePhone(ctx, phone)
	if err != nil {
		return resolvedUser{}, fmt.Errorf("%s: %w", phone, err)
	}
	u, ok := userFromResolved(res)
	if !ok {
		return resolvedUser{}, fmt.Errorf("%s — не пользователь (или скрыт настройками приватности)", phone)
	}
	return u, nil
}

func resolveByID(ctx context.Context, api *tg.Client, id int64) (resolvedUser, error) {
	peers, err := tgx.Dialogs(ctx, api, 200)
	if err != nil {
		return resolvedUser{}, err
	}
	for _, p := range peers {
		if p.Kind == "user" && p.ID == id {
			return resolvedUser{ID: p.ID, AccessHash: p.AccessHash, Name: p.Title}, nil
		}
	}
	return resolvedUser{}, fmt.Errorf("пользователь id=%d не найден среди диалогов (используй @username)", id)
}

func userFromResolved(res *tg.ContactsResolvedPeer) (resolvedUser, bool) {
	pu, ok := res.Peer.(*tg.PeerUser)
	if !ok {
		return resolvedUser{}, false
	}
	for _, u := range res.Users {
		if us, ok := u.(*tg.User); ok && us.ID == pu.UserID {
			return resolvedUser{
				ID: us.ID, AccessHash: us.AccessHash, Username: us.Username,
				Name: strings.TrimSpace(us.FirstName + " " + us.LastName),
			}, true
		}
	}
	return resolvedUser{}, false
}

func missingReason(mi tg.MissingInvitee) string {
	switch {
	case mi.PremiumWouldAllowInvite:
		return "нужен Telegram Premium для приглашения"
	case mi.PremiumRequiredForPm:
		return "приватность: нужен Premium, чтобы прислать ссылку"
	default:
		return "приватность: можно прислать ссылку вручную"
	}
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
