package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
)

func manageRights(ctx context.Context, client *telegram.Client, dir, chat string, chatID int64, alias string, users strList, preset, rights, rank string, revoke, do bool) error {
	t, err := resolveChatTarget(ctx, client, dir, chat, chatID, alias, false)
	if err != nil {
		return err
	}
	us, err := resolveUsers(ctx, client, users)
	if err != nil {
		return err
	}

	var ar tg.ChatAdminRights
	if !revoke {
		if preset == "" && rights == "" {
			return fmt.Errorf("rights: укажи -preset admin|moderator, -rights a,b,c или -revoke")
		}
		if preset != "" {
			if err := applyPreset(&ar, preset, t.Kind == "channel"); err != nil {
				return err
			}
		}
		if rights != "" {
			if err := applyRights(&ar, rights); err != nil {
				return err
			}
		}
	}

	action := "выдать права"
	if revoke {
		action = "снять админку"
	}
	payload := approve.Canon("manage/rights", peerKey(t.Peer), usersKey(us), preset, rights, rank, fmt.Sprint(revoke))
	if err := Gate(do, "manage/rights", rightsPreview(action, t.Desc, us, preset, rights, rank, revoke), payload); err != nil {
		return err
	}
	if !do {
		return nil
	}
	switch t.Peer.(type) {
	case *tg.InputPeerChannel:
		ch, _ := inputChannel(t.Peer)
		for _, u := range us {
			req := &tg.ChannelsEditAdminRequest{Channel: ch, UserID: u.inputUser(), AdminRights: ar}
			if rank != "" {
				req.SetRank(rank)
			}
			if _, err := client.API().ChannelsEditAdmin(ctx, req); err != nil {
				return fmt.Errorf("%s %s: %w", action, u, err)
			}
			fmt.Printf("✅ %s: %s\n", action, u)
		}
		return nil

	case *tg.InputPeerChat:
		p := t.Peer.(*tg.InputPeerChat)
		for _, u := range us {
			if _, err := client.API().MessagesEditChatAdmin(ctx, &tg.MessagesEditChatAdminRequest{
				ChatID: p.ChatID, UserID: u.inputUser(), IsAdmin: !revoke,
			}); err != nil {
				return fmt.Errorf("%s %s: %w", action, u, err)
			}
			fmt.Printf("✅ %s: %s\n", action, u)
		}
		return nil

	default:
		return fmt.Errorf("rights: %s — это не группа/канал", t.Desc)
	}
}

func rightsPreview(action, desc string, us []resolvedUser, preset, rights, rank string, revoke bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RIGHTS (%s)\nГде:   %s\nКого:  %s\n", action, desc, joinUsers(us))
	if preset != "" {
		fmt.Fprintf(&b, "Пресет: %s\n", preset)
	}
	if rights != "" {
		fmt.Fprintf(&b, "Права:  %s\n", rights)
	}
	if rank != "" {
		fmt.Fprintf(&b, "Rank:   %s\n", rank)
	}
	if revoke {
		b.WriteString("Режим:  снять админку\n")
	}
	return b.String()
}

// --- права ---

func applyPreset(r *tg.ChatAdminRights, name string, broadcast bool) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "admin", "full":
		r.ChangeInfo = true
		r.DeleteMessages = true
		r.BanUsers = true
		r.InviteUsers = true
		r.PinMessages = true
		r.AddAdmins = true
		r.ManageTopics = true
		r.ManageCall = true
		r.Other = true
		if broadcast {
			r.PostMessages = true
		}
	case "moderator", "mod":
		r.DeleteMessages = true
		r.BanUsers = true
		r.InviteUsers = true
		r.PinMessages = true
		r.ManageTopics = true
		r.Other = true
	default:
		return fmt.Errorf("неизвестный пресет %q (admin|moderator)", name)
	}
	return nil
}

func applyRights(r *tg.ChatAdminRights, list string) error {
	for _, n := range strings.Split(list, ",") {
		n = strings.ToLower(strings.TrimSpace(n))
		switch strings.NewReplacer("-", "", "_", "").Replace(n) {
		case "":
		case "changeinfo":
			r.ChangeInfo = true
		case "post", "postmessages":
			r.PostMessages = true
		case "edit", "editmessages":
			r.EditMessages = true
		case "delete", "deletemessages":
			r.DeleteMessages = true
		case "ban", "banusers":
			r.BanUsers = true
		case "invite", "inviteusers":
			r.InviteUsers = true
		case "pin", "pinmessages":
			r.PinMessages = true
		case "addadmins":
			r.AddAdmins = true
		case "anonymous":
			r.Anonymous = true
		case "managecall":
			r.ManageCall = true
		case "other":
			r.Other = true
		case "topics", "managetopics":
			r.ManageTopics = true
		case "poststories":
			r.PostStories = true
		case "editstories":
			r.EditStories = true
		case "deletestories":
			r.DeleteStories = true
		default:
			return fmt.Errorf("неизвестное право %q", n)
		}
	}
	return nil
}
