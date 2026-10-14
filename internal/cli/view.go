package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/tgx"
)

// ViewOpts — параметры команды view: быстрый просмотр сообщений за период
// напрямую из Telegram, без импорта (на диск ничего не пишется).
type ViewOpts struct {
	// цель: -entry (запись конфига) или -chat/-id/-alias/-self
	Chat   string
	ChatID int64
	Alias  string
	Self   bool
	Entry  string
	Topic  int

	From, To time.Time // границы (нулевое = без границы); To включается в период
	Last     int       // только последние N сообщений

	MaxLen  int // обрезка текста одного сообщения (символов, 0 — без обрезки)
	MaxMsgs int // максимум сообщений в выводе (0 — без лимита)

	Media      bool // скачать медиа в кэш и печатать пути (иначе только пометки)
	Transcribe bool // транскрибировать голосовые (требует Tr)
	Tr         Transcriber
}

// View читает сообщения за период и печатает их текстом для агента.
// Ничего не импортирует: медиа не качается (кроме -media), логи не пишутся.
func View(ctx context.Context, client *telegram.Client, dir string, o ViewOpts) error {
	peer, desc, topic, err := resolveViewTarget(ctx, client, dir, o)
	if err != nil {
		return err
	}

	from, to := o.From, o.To
	if from.IsZero() && to.IsZero() && o.Last == 0 {
		to = time.Now()
		from = to.AddDate(0, 0, -6) // последние 7 дней
	}
	var upper time.Time
	if !to.IsZero() {
		upper = to.AddDate(0, 0, 1) // -to включительно (по конец дня)
	}
	loc := time.Local
	api := client.API()

	users := map[int64]*tg.User{}
	channels := map[int64]*tg.Channel{}
	chats := map[int64]*tg.Chat{}

	var msgs []*tg.Message
	offsetID := 0
	for {
		page, err := tgx.HistoryPageEx(ctx, api, peer, topic, offsetID, 100)
		if err != nil {
			return err
		}
		if len(page.Messages) == 0 {
			break
		}
		for _, u := range page.Users {
			if us, ok := u.(*tg.User); ok {
				users[us.ID] = us
			}
		}
		for _, c := range page.Chats {
			if ch, ok := c.(*tg.Channel); ok {
				channels[ch.ID] = ch
			}
			if ch, ok := c.(*tg.Chat); ok {
				chats[ch.ID] = ch
			}
		}
		minID := 0
		oldest := time.Time{}
		for _, mc := range page.Messages {
			m, ok := mc.(*tg.Message)
			if !ok {
				continue
			}
			if minID == 0 || m.ID < minID {
				minID = m.ID
			}
			d := time.Unix(int64(m.Date), 0).In(loc)
			if oldest.IsZero() || d.Before(oldest) {
				oldest = d
			}
			if !to.IsZero() && !d.Before(upper) {
				continue
			}
			if !from.IsZero() && d.Before(from) {
				continue
			}
			msgs = append(msgs, m)
		}
		if minID == 0 {
			break
		}
		offsetID = minID
		if o.Last > 0 && len(msgs) >= o.Last {
			break
		}
		if !from.IsZero() && !oldest.IsZero() && oldest.Before(from) {
			break
		}
		if len(page.Messages) < 100 {
			break
		}
	}

	sort.Slice(msgs, func(i, j int) bool {
		if msgs[i].Date != msgs[j].Date {
			return msgs[i].Date < msgs[j].Date
		}
		return msgs[i].ID < msgs[j].ID
	})
	if o.Last > 0 && len(msgs) > o.Last {
		msgs = msgs[len(msgs)-o.Last:]
	}
	if o.MaxMsgs > 0 && len(msgs) > o.MaxMsgs {
		fmt.Fprintf(os.Stderr, "• показаны последние %d из %d (лимит -max-msgs)\n", o.MaxMsgs, len(msgs))
		msgs = msgs[len(msgs)-o.MaxMsgs:]
	}
	if len(msgs) == 0 {
		fmt.Fprintln(os.Stderr, "нет сообщений за указанный период")
		return nil
	}

	// превью для reply-ссылок (одна дозагрузка на все ответы)
	previews := map[int]string{}
	seen := map[int]bool{}
	var need []int
	for _, m := range msgs {
		if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			if id, ok := rh.GetReplyToMsgID(); ok && id > 0 && !seen[id] {
				seen[id] = true
				need = append(need, id)
			}
		}
	}
	for k, v := range tgx.MessagePreviews(ctx, api, need) {
		previews[k] = v
	}

	var cacheDir string
	if o.Media {
		if c, err := os.UserCacheDir(); err == nil {
			cacheDir = filepath.Join(c, "tgvault", "view")
			_ = os.MkdirAll(cacheDir, 0o700)
		} else {
			cacheDir = os.TempDir()
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s", desc)
	if topic > 0 {
		fmt.Fprintf(&b, " · топик %d", topic)
	}
	b.WriteByte('\n')
	fmt.Fprintf(&b, "период: %s … %s · сообщений: %d\n",
		dayOf(msgs[0].Date, loc), dayOf(msgs[len(msgs)-1].Date, loc), len(msgs))

	lastDay := ""
	for _, m := range msgs {
		day := dayOf(m.Date, loc)
		if day != lastDay {
			fmt.Fprintf(&b, "\n## %s\n", day)
			lastDay = day
		}
		at := time.Unix(int64(m.Date), 0).In(loc).Format("15:04")
		fmt.Fprintf(&b, "\n**%s — %s**\n", at, viewAuthor(m, users, channels, chats, desc))
		if rh, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok {
			if id, ok := rh.GetReplyToMsgID(); ok && id > 0 {
				if pv := previews[id]; pv != "" {
					fmt.Fprintf(&b, "↩ msg:%d — «%s»\n", id, pv)
				} else {
					fmt.Fprintf(&b, "↩ msg:%d\n", id)
				}
			}
		}
		if text := truncateText(m.Message, o.MaxLen); text != "" {
			b.WriteString(text)
			b.WriteByte('\n')
		}
		if line := renderMedia(ctx, client, m, cacheDir, o); line != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	fmt.Print(b.String())
	fmt.Fprintf(os.Stderr, "• %s: %d сообщений\n", desc, len(msgs))
	return nil
}
