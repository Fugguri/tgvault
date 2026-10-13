package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// strList — флаг, который можно повторять (а также через запятую).
type strList []string

func (s *strList) String() string     { return strings.Join(*s, ",") }
func (s *strList) Set(v string) error { *s = append(*s, v); return nil }

const manageHelp = `tgvault manage — управление чатами, топиками и людьми.

Использование:
  tgvault manage <подкоманда> [флаги] [-send]

Подкоманды:
  create    создать группу/канал           -title T [-kind group|channel|chat] [-forum] [-about A] [-user U]...
  topic     создать топик в форум-группе   -chat C| -id N| -alias S  -title T
  forward   переслать сообщения            [-chat C|-id N|-alias S|-self] -msg ID...| -last N  -dest D|-dest-id N| -dest-topic T
  invite    добавить людей в чат           -chat C| -id N| -alias S  -user U...  [-fwd-limit N]
  rights    выдать/снять права админа      -chat C| -id N| -alias S  -user U  [-preset admin|moderator]| -rights a,b,c  [-rank R] [-revoke]
  kick      убрать/забанить                 -chat C| -id N| -alias S  -user U  [-ban] [-days N]
  delete    удалить группу/канал (свой)      -chat C| -id N| -alias S

Общие:
  -user U   пользователь: @username | id | +7999… (можно несколько, в т.ч. через запятую)
  -send     ВЫПОЛНИТЬ (dry-run по умолчанию; нужно согласование: tgvault approve <id>)

Права для -rights (через запятую):
  changeinfo, post, edit, delete, ban, invite, pin, addadmins, anonymous,
  managecall, topics, other, poststories, editstories, deletestories`

// Manage выполняет подкоманды управления чатами (аналог secret — свой flagset).
func Manage(ctx context.Context, client *telegram.Client, dir string, args []string) error {
	fs := flag.NewFlagSet("manage", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, manageHelp) }

	var users, msgs strList

	title := fs.String("title", "", "create/topic: название")
	about := fs.String("about", "", "create: описание")
	kind := fs.String("kind", "group", "create: group|channel|chat")
	forum := fs.Bool("forum", false, "create: включить топики (для group)")

	chat := fs.String("chat", "", "чат по части имени")
	chatID := fs.Int64("id", 0, "чат по id")
	alias := fs.String("alias", "", "чат по slug из конфига")
	self := fs.Bool("self", false, "Saved Messages")

	dest := fs.String("dest", "", "forward: куда (часть имени)")
	destID := fs.Int64("dest-id", 0, "forward: куда (id)")
	destAlias := fs.String("dest-alias", "", "forward: куда (slug)")
	destTopic := fs.Int("dest-topic", 0, "forward: топик назначения")
	dropAuthor := fs.Bool("drop-author", false, "forward: без автора")
	silent := fs.Bool("silent", false, "forward: без звука")
	last := fs.Int("last", 0, "forward: последние N сообщений из источника")

	preset := fs.String("preset", "", "rights: admin|moderator")
	rights := fs.String("rights", "", "rights: список прав через запятую")
	rank := fs.String("rank", "", "rights: должность (rank)")
	revoke := fs.Bool("revoke", false, "rights: снять админку")
	ban := fs.Bool("ban", false, "kick: забанить (иначе просто убрать)")
	days := fs.Int("days", 0, "kick -ban: срок в днях (0 — навсегда)")
	fwdLimit := fs.Int("fwd-limit", 0, "invite: сколько последних сообщений переслать")

	do := fs.Bool("send", false, "ВЫПОЛНИТЬ (иначе dry-run)")

	fs.Var(&users, "user", "пользователь: @name | id | +phone")
	fs.Var(&msgs, "msg", "forward: id сообщения")

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, manageHelp)
		return fmt.Errorf("manage: укажи подкоманду")
	}
	sub := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	pos := fs.Args()
	for _, p := range pos {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			msgs = append(msgs, strconv.Itoa(n))
		}
	}

	switch sub {
	case "create", "new":
		return manageCreate(ctx, client, *title, *about, *kind, *forum, users, *do)
	case "topic":
		return manageTopic(ctx, client, dir, *chat, *chatID, *alias, *title, *do)
	case "forward", "fwd":
		return manageForward(ctx, client, dir, forwardOpts{
			from: *chat, fromID: *chatID, fromAlias: *alias, self: *self,
			dest: *dest, destID: *destID, destAlias: *destAlias, destTopic: *destTopic,
			msgs: toInts(msgs), last: *last, dropAuthor: *dropAuthor, silent: *silent, do: *do,
		})
	case "invite", "add-user", "adduser":
		return manageInvite(ctx, client, dir, *chat, *chatID, *alias, *self, users, *fwdLimit, *do)
	case "rights", "admin", "promote":
		return manageRights(ctx, client, dir, *chat, *chatID, *alias, users, *preset, *rights, *rank, *revoke, *do)
	case "kick", "ban":
		return manageKick(ctx, client, dir, *chat, *chatID, *alias, users, *ban, *days, *do)
	case "delete", "del", "rm":
		return manageDelete(ctx, client, dir, *chat, *chatID, *alias, *do)
	default:
		return fmt.Errorf("manage: неизвестная подкоманда %q", sub)
	}
}

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

type forwardOpts struct {
	from, dest             string
	fromID, destID         int64
	fromAlias, destAlias   string
	self                   bool
	destTopic              int
	msgs                   []int
	last                   int
	dropAuthor, silent, do bool
}

func manageForward(ctx context.Context, client *telegram.Client, dir string, o forwardOpts) error {
	src, err := resolveChatTarget(ctx, client, dir, o.from, o.fromID, o.fromAlias, o.self)
	if err != nil {
		return fmt.Errorf("источник: %w", err)
	}
	dst, err := resolveChatTarget(ctx, client, dir, o.dest, o.destID, o.destAlias, false)
	if err != nil {
		return fmt.Errorf("назначение: %w", err)
	}

	ids := o.msgs
	if len(ids) == 0 && o.last > 0 {
		msgs, err := tgx.History(ctx, client.API(), src.Peer, o.last)
		if err != nil {
			return err
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			if m, ok := msgs[i].(*tg.Message); ok {
				ids = append(ids, m.ID)
			}
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("forward: нужны -msg ID… или -last N")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FORWARD\nОткуда:  %s\nКуда:    %s\n", src.Desc, dst.Desc)
	if o.destTopic > 0 {
		fmt.Fprintf(&b, "Топик:   %d\n", o.destTopic)
	}
	fmt.Fprintf(&b, "Сообщения: %v\n", ids)
	if o.dropAuthor {
		b.WriteString("Автор:   скрыть\n")
	}
	if o.silent {
		b.WriteString("Тихо:    да\n")
	}
	payload := approve.Canon("manage/forward", peerKey(src.Peer), peerKey(dst.Peer),
		intsKey(ids), strconv.Itoa(o.destTopic), fmt.Sprint(o.dropAuthor), fmt.Sprint(o.silent))
	if err := Gate(o.do, "manage/forward", b.String(), payload); err != nil {
		return err
	}
	if !o.do {
		return nil
	}

	req := &tg.MessagesForwardMessagesRequest{
		FromPeer: src.Peer, ToPeer: dst.Peer, ID: ids, RandomID: randomIDs(len(ids)),
		DropAuthor: o.dropAuthor, Silent: o.silent,
	}
	if o.destTopic > 0 {
		req.SetTopMsgID(o.destTopic)
	}
	if _, err := client.API().MessagesForwardMessages(ctx, req); err != nil {
		return err
	}
	fmt.Printf("✅ переслано сообщений: %d (%s → %s)\n", len(ids), src.Desc, dst.Desc)
	return nil
}

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

// --- утилиты ---

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

func joinUsers(us []resolvedUser) string {
	parts := make([]string, 0, len(us))
	for _, u := range us {
		parts = append(parts, u.String())
	}
	return strings.Join(parts, ", ")
}

func usersKey(us []resolvedUser) string {
	parts := make([]string, 0, len(us))
	for _, u := range us {
		parts = append(parts, strconv.FormatInt(u.ID, 10))
	}
	return strings.Join(parts, ",")
}

func randomIDs(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = randomID()
	}
	return out
}

func toInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, n)
		}
	}
	return out
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
