package cli

import (
	"context"
	"fmt"
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
	All      bool      // импортировать все записи, а не только default
	Entry    string    // импортировать только запись с этим slug
	Full     bool      // полный пересбор (_log.md с нуля), игнорируя watermark
	NoRedact bool      // не выносить токены/ключи из переписки
	From, To time.Time // границы (нулевое = без границы)
	Tr       Transcriber
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

	from, to := o.From, o.To
	if from.IsZero() {
		from = ParseDate(cfg.From)
	}
	if to.IsZero() {
		to = ParseDate(cfg.To)
	}

	fmt.Printf("Импорт: %s → %s (%d записей)\n", cfgPath, out, len(chosen))

	var q *importer.Queue
	if o.Tr != nil {
		q = importer.NewQueue(ctx, o.Tr, 1)
	}

	grand := 0
	for _, e := range chosen {
		e = ensurePeer(ctx, client, e)
		slugDir := filepath.Join(out, e.Slug)
		minID := 0
		if !o.Full {
			minID = importer.LoadState(slugDir).LastID
		}
		n, last, err := importer.Entry(ctx, client, e, importer.Options{
			Out: out, From: from, To: to, MinID: minID, Rebuild: o.Full, Loc: time.Local, NoRedact: o.NoRedact,
		}, o.Tr, q)
		if err != nil {
			// access_hash мог протухнуть — резолвим заново по имени и пробуем ещё раз
			if p, ok, rerr := tgx.Find(ctx, client.API(), e.Chat); rerr == nil && ok {
				e.ChatID, e.AccessHash, e.Kind = p.ID, p.AccessHash, p.Kind
				n, last, err = importer.Entry(ctx, client, e, importer.Options{
					Out: out, From: from, To: to, MinID: minID, Rebuild: o.Full, Loc: time.Local, NoRedact: o.NoRedact,
				}, o.Tr, q)
			}
		}
		if err != nil {
			fmt.Printf("  ⚠ %s: %v\n", e.Slug, err)
			continue
		}
		if err := importer.SaveState(slugDir, importer.State{
			LastID: last, UpdatedAt: time.Now().Format(time.RFC3339),
		}); err != nil {
			fmt.Printf("  ⚠ состояние %s: %v\n", e.Slug, err)
		}
		grand += n
	}
	if q != nil {
		q.Wait()
	}
	fmt.Printf("Готово: %d новых сообщений\n", grand)
	return nil
}

func ParseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
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
