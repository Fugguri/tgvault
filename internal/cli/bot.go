package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/tgx"
)

// BotOpts — параметры клик-теста бота.
type BotOpts struct {
	Bot         string   // @username или id
	Self        bool     // Saved Messages (Избранное)
	Steps       []string // "send:/start", "click:Начать", "expect:Привет", "expect-doc", "sleep:2"
	Go          bool     // --go: выполнить (иначе план)
	AllowDanger bool
	Pause       time.Duration
}

var dangerRe = regexp.MustCompile(`(?i)(спис|удал|delete|charge|оплат|remove|снять|подтвердить опл)`)

type step struct {
	kind  string
	value string
}

// Bot прогоняет клик-тест бота: шаги send/click/expect/expect-doc/sleep.
func Bot(ctx context.Context, client *telegram.Client, o BotOpts) error {
	steps, err := parseSteps(o.Steps)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		return fmt.Errorf("нет шагов (-step)")
	}

	peer, title, err := resolveBot(ctx, client, o.Bot, o.Self)
	if err != nil {
		return err
	}
	fmt.Printf("бот: %s\n", title)

	if !o.Go {
		fmt.Println("ПЛАН (dry-run; добавь -go для прогона):")
		for _, s := range steps {
			danger := ""
			if s.kind == "click" && dangerRe.MatchString(s.value) && !o.AllowDanger {
				danger = " ⛔НЕОБРАТИМО"
			}
			fmt.Printf("  %s: %s%s\n", s.kind, s.value, danger)
		}
		return nil
	}

	api := client.API()
	failures := 0
	for _, s := range steps {
		switch s.kind {
		case "sleep":
			d, _ := time.ParseDuration(s.value + "s")
			time.Sleep(d)
			continue
		case "send":
			fmt.Printf("→ send: %q\n", s.value)
			if _, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: peer, Message: s.value, RandomID: randomID()}); err != nil {
				return err
			}
		case "click":
			msgs, err := history(ctx, client, peer, 4)
			if err != nil {
				return err
			}
			m, data, text, ok := findButton(msgs, s.value)
			if !ok {
				fmt.Printf("  ✗ кнопка ~%q не найдена\n", s.value)
				failures++
				goto done
			}
			if dangerRe.MatchString(text) && !o.AllowDanger {
				fmt.Printf("  ⛔ кнопка %q необратима — пропуск (нужен -allow-danger). Стоп.\n", text)
				failures++
				goto done
			}
			fmt.Printf("→ click: %q\n", text)
			req := &tg.MessagesGetBotCallbackAnswerRequest{Peer: peer, MsgID: m.ID}
			req.SetData(data)
			if _, err := api.MessagesGetBotCallbackAnswer(ctx, req); err != nil {
				return err
			}
		case "expect":
			last := lastMessage(ctx, client, peer)
			ok := last != nil && strings.Contains(strings.ToLower(last.Message), strings.ToLower(s.value))
			fmt.Printf("  %s expect %q\n", tick(ok), s.value)
			if !ok {
				failures++
			}
			continue
		case "expect-doc":
			last := lastMessage(ctx, client, peer)
			_, ok := docOf(last)
			fmt.Printf("  %s expect-doc\n", tick(ok))
			if !ok {
				failures++
			}
			continue
		}

		time.Sleep(o.Pause)
		last := lastMessage(ctx, client, peer)
		if last != nil {
			text := last.Message
			if len(text) > 300 {
				text = text[:300]
			}
			fmt.Printf("  бот: %s\n", strings.ReplaceAll(text, "\n", " | "))
			if btns := buttonsOf(last); len(btns) > 0 {
				fmt.Printf("  кнопки: %v\n", btns)
			}
		}
	}
done:
	if failures > 0 {
		return fmt.Errorf("РЕЗУЛЬТАТ: %d проверок не прошло", failures)
	}
	fmt.Println("РЕЗУЛЬТАТ: OK")
	return nil
}

func tick(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}

func parseSteps(raw []string) ([]step, error) {
	var out []step
	for _, r := range raw {
		kind, val, _ := strings.Cut(r, ":")
		kind = strings.TrimSpace(kind)
		switch kind {
		case "send", "click", "expect", "sleep":
			out = append(out, step{kind, val})
		case "expect-doc", "expect_doc":
			out = append(out, step{"expect-doc", ""})
		default:
			return nil, fmt.Errorf("неизвестный шаг %q", r)
		}
	}
	return out, nil
}

func resolveBot(ctx context.Context, client *telegram.Client, bot string, self bool) (tg.InputPeerClass, string, error) {
	api := client.API()
	if self {
		return &tg.InputPeerSelf{}, "Saved Messages", nil
	}
	if strings.HasPrefix(bot, "@") || bot == "" {
		uname := strings.TrimPrefix(bot, "@")
		res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: uname})
		if err != nil {
			return nil, "", fmt.Errorf("resolve @%s: %w", uname, err)
		}
		if pu, ok := res.Peer.(*tg.PeerUser); ok {
			for _, u := range res.Users {
				if us, ok := u.(*tg.User); ok && us.ID == pu.UserID {
					return &tg.InputPeerUser{UserID: us.ID, AccessHash: us.AccessHash}, fmt.Sprintf("@%s (id=%d)", us.Username, us.ID), nil
				}
			}
		}
		return nil, "", fmt.Errorf("не удалось получить peer для @%s", uname)
	}
	// по id — ищем в диалогах
	p, ok, err := tgx.Find(ctx, api, bot)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", fmt.Errorf("бот %q не найден", bot)
	}
	return p.Input, fmt.Sprintf("%s (id=%d)", p.Title, p.ID), nil
}

func history(ctx context.Context, client *telegram.Client, peer tg.InputPeerClass, limit int) ([]*tg.Message, error) {
	res, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, Limit: limit})
	if err != nil {
		return nil, err
	}
	var out []*tg.Message
	switch v := res.(type) {
	case *tg.MessagesMessages:
		for _, m := range v.Messages {
			if mm, ok := m.(*tg.Message); ok {
				out = append(out, mm)
			}
		}
	case *tg.MessagesMessagesSlice:
		for _, m := range v.Messages {
			if mm, ok := m.(*tg.Message); ok {
				out = append(out, mm)
			}
		}
	case *tg.MessagesChannelMessages:
		for _, m := range v.Messages {
			if mm, ok := m.(*tg.Message); ok {
				out = append(out, mm)
			}
		}
	}
	return out, nil
}

func lastMessage(ctx context.Context, client *telegram.Client, peer tg.InputPeerClass) *tg.Message {
	msgs, _ := history(ctx, client, peer, 1)
	if len(msgs) == 0 {
		return nil
	}
	return msgs[0]
}

// findButton ищет инлайн-кнопку по подстроке среди сообщений.
func findButton(msgs []*tg.Message, pattern string) (*tg.Message, []byte, string, bool) {
	pat := strings.ToLower(pattern)
	for _, m := range msgs {
		rm, ok := m.ReplyMarkup.(*tg.ReplyInlineMarkup)
		if !ok {
			continue
		}
		for _, row := range rm.Rows {
			for _, b := range row.Buttons {
				if strings.Contains(strings.ToLower(b.Text), pat) {
					if cb, ok := b.Type.(*tg.InlineButtonTypeCallback); ok {
						return m, cb.Data, b.Text, true
					}
					return m, nil, b.Text, true
				}
			}
		}
	}
	return nil, nil, "", false
}

func buttonsOf(m *tg.Message) [][]string {
	rm, ok := m.ReplyMarkup.(*tg.ReplyInlineMarkup)
	if !ok {
		return nil
	}
	var out [][]string
	for _, row := range rm.Rows {
		var r []string
		for _, b := range row.Buttons {
			r = append(r, b.Text)
		}
		out = append(out, r)
	}
	return out
}

func docOf(m *tg.Message) (*tg.Document, bool) {
	if m == nil {
		return nil, false
	}
	md, ok := m.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, false
	}
	d, ok := md.Document.(*tg.Document)
	return d, ok
}
