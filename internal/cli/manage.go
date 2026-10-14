package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram"
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

// --- утилиты ---

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

func toInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, n)
		}
	}
	return out
}
