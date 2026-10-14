package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gotd/td/telegram"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/importer"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// Transcriber — транскрибация аудиофайла в текст.
type Transcriber = importer.Transcriber

// ImportOpts — параметры команды import.
type ImportOpts struct {
	All       bool      // импортировать все записи, а не только default
	Entry     string    // импортировать только запись с этим slug
	Full      bool      // полный пересбор (_log.md с нуля), игнорируя watermark
	Unbounded bool      // тянуть всю историю, без окна по умолчанию
	NoRedact  bool      // не выносить токены/ключи из переписки
	From, To  time.Time // границы (нулевое = без границы)
	Tr        Transcriber
}

// Import прогоняет импорт по конфигу проекта: дефолтные записи (или все).
func Import(ctx context.Context, client *telegram.Client, dir string, o ImportOpts) error {
	cfg, cfgPath, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("конфиг не найден (%s вверх по дереву): %w", config.Path, err)
	}

	out := cfg.Out
	if out == "" {
		out = "docs/telegram_chats"
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(filepath.Dir(cfgPath), out)
	}

	var chosen []config.Entry
	for _, e := range cfg.Entries {
		switch {
		case o.Entry != "":
			if e.Slug == o.Entry {
				chosen = append(chosen, e)
			}
		case o.All:
			chosen = append(chosen, e)
		case e.Default:
			chosen = append(chosen, e)
		}
	}
	if len(chosen) == 0 {
		if o.Entry != "" {
			return fmt.Errorf("запись %q не найдена в конфиге", o.Entry)
		}
		return fmt.Errorf("нет дефолтных записей (запусти init или добавь -all)")
	}

	// Даты из конфига проверяются и здесь, хотя cmd/ ловит их раньше (до коннекта):
	// границы нужны до первой записи на диск, чтобы часть записей не уехала в логи
	// при сломанном конфиге.
	if _, err := importer.ParseDate(cfg.From); err != nil {
		return fmt.Errorf("from в конфиге %s: %w", cfgPath, err)
	}
	if _, err := importer.ParseDate(cfg.To); err != nil {
		return fmt.Errorf("to в конфиге %s: %w", cfgPath, err)
	}

	fmt.Printf("Импорт: %s → %s (%d записей)\n", cfgPath, out, len(chosen))

	var q *importer.Queue
	if o.Tr != nil {
		q = importer.NewQueue(ctx, o.Tr, 1)
	}

	grand := 0
	windowNotice := false
	var silent []string // записи, давшие 0 сообщений, вместе с причиной
	for _, e := range chosen {
		e = ensurePeer(ctx, client, e)
		slugDir := filepath.Join(out, e.Slug)
		loadedID := 0
		if !o.Full {
			st, err := importer.LoadState(slugDir)
			if err != nil {
				// Битое состояние — громко: молчаливый нулевой watermark означает
				// повторный импорт всей истории или тихую потерю сообщений.
				fmt.Fprintf(os.Stderr, "  ⚠ %s: %v\n", e.Slug, err)
				continue
			}
			loadedID = st.LastID
		}
		minID := loadedID
		// Границы считаются на каждую запись отдельно: окно по умолчанию
		// полагается только тем, у кого ещё нет состояния.
		rng, err := importer.ResolveRange(o.From, o.To, cfg.From, cfg.To, minID, o.Unbounded, o.Full, time.Now())
		if err != nil {
			return err
		}
		if rng.IgnoreWatermark {
			minID = 0
		}
		if rng.Windowed && !windowNotice {
			windowNotice = true
			fmt.Printf("Диапазон по умолчанию: с %s (всю историю тянет -all-history; полный пересбор логов — -full; свой диапазон — -from или from в конфиге)\n",
				rng.From.Format("2006-01-02"))
		}
		n, last, err := importer.Entry(ctx, client, e, importer.Options{
			Out: out, From: rng.From, To: rng.To, MinID: minID, Rebuild: o.Full, Loc: time.Local, NoRedact: o.NoRedact,
		}, o.Tr, q)
		if err != nil {
			// access_hash мог протухнуть — резолвим заново по имени и пробуем ещё раз
			if p, ok, rerr := tgx.Find(ctx, client.API(), e.Chat); rerr == nil && ok {
				e.ChatID, e.AccessHash, e.Kind = p.ID, p.AccessHash, p.Kind
				n, last, err = importer.Entry(ctx, client, e, importer.Options{
					Out: out, From: rng.From, To: rng.To, MinID: minID, Rebuild: o.Full, Loc: time.Local, NoRedact: o.NoRedact,
				}, o.Tr, q)
			}
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ %s: %v\n", e.Slug, err)
			continue
		}
		if err := importer.SaveState(slugDir, importer.State{
			LastID: last, UpdatedAt: time.Now().Format(time.RFC3339),
		}); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ состояние %s: %v\n", e.Slug, err)
		}
		if n == 0 {
			silent = append(silent, e.Slug+" — "+whyNothing(rng, loadedID))
		}
		grand += n
	}
	if q != nil {
		q.Wait()
	}
	fmt.Printf("Готово: %d новых сообщений\n", grand)
	// «0 новых» само по себе не отличимо от «фильтры отсекли всё»: пользователь
	// не знает, ждать ли ещё, а агент — надо ли что-то менять. Скажи причину.
	if grand == 0 && len(silent) > 0 {
		fmt.Println("Ничего не импортировано:")
		for _, s := range silent {
			fmt.Println("  " + s)
		}
	}
	return nil
}

// whyNothing объясняет, почему запись не дала ни одного сообщения.
func whyNothing(rng importer.Range, loadedID int) string {
	switch {
	case rng.Windowed:
		return fmt.Sprintf("в окне по умолчанию (с %s) сообщений нет — всю историю тянет -all-history",
			rng.From.Format("2006-01-02"))
	case rng.IgnoreWatermark:
		return "watermark снят (-all-history), но в чате ничего не нашлось"
	case loadedID > 0:
		return fmt.Sprintf("состояние актуально (last_id=%d), новых нет — переимпорт: -all-history", loadedID)
	case !rng.From.IsZero() || !rng.To.IsZero():
		return "диапазон -from/-to не захватил ни одного сообщения"
	default:
		return "в чате нет сообщений"
	}
}

// ParseDate разбирает дату из флага (-from/-to). Пустая строка — нулевое время.
// Ошибка отдаётся наружу: проглотить её нельзя — опечатка в дате тогда выглядит
// как «граница не задана» и молча подменяется окном по умолчанию.
func ParseDate(s string) (time.Time, error) {
	return importer.ParseDate(s)
}

// ensurePeer нормализует запись (в т.ч. старый формат) и добивает access_hash.
func ensurePeer(ctx context.Context, client *telegram.Client, e config.Entry) config.Entry {
	if e.Kind == "group" || e.Kind == "" {
		e.Kind = "channel"
	}
	// старый peer-id вида -100xxxxxxxxxx → raw entity id
	if e.ChatID <= -1_000_000_000_000 {
		e.ChatID = -1_000_000_000_000 - e.ChatID
	}
	if e.AccessHash == 0 && e.Kind != "chat" {
		if p, ok, err := tgx.Resolve(ctx, client.API(), e.ChatID); err == nil && ok {
			e.AccessHash, e.Kind = p.AccessHash, p.Kind
		} else if e.Chat != "" {
			if p, ok, err := tgx.Find(ctx, client.API(), e.Chat); err == nil && ok {
				e.ChatID, e.AccessHash, e.Kind = p.ID, p.AccessHash, p.Kind
			}
		}
	}
	return e
}
