package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/gotd/td/telegram"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// Init запускает интерактивный мастер проекта: вопросы, выбор чатов и
// топиков (с поиском), выбор дефолтных записей, запись .tg-import.json.
func Init(ctx context.Context, client *telegram.Client, dir string) error {
	// если конфиг уже есть — спросим, что делать (дополнить/перезаписать/отмена)
	cfgPath := filepath.Join(dir, config.Path)
	mode := "new"
	if raw, err := os.ReadFile(cfgPath); err == nil {
		if config.IsV1(raw) {
			return fmt.Errorf("найден старый конфиг v1 (%s) — выполни `tgvault migrate`", cfgPath)
		}
		var m string
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Конфиг уже существует — что делаем?").
				Options(
					huh.NewOption("Дополнить (сохранить существующие записи)", "merge"),
					huh.NewOption("Перезаписать (со старым сделаю бэкап)", "replace"),
					huh.NewOption("Отмена", "cancel"),
				).Value(&m),
		)).Run(); err != nil {
			return err
		}
		mode = m
	}
	if mode == "cancel" {
		fmt.Println("отменено")
		return nil
	}

	project := filepath.Base(dir)
	out := "docs/telegram_chats"

	if err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Имя проекта").Value(&project),
		huh.NewInput().Title("Куда складывать (docs/telegram_chats)").Value(&out),
	)).Run(); err != nil {
		return err
	}

	peers, err := tgx.Dialogs(ctx, client.API(), 200)
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		return fmt.Errorf("диалогов не найдено")
	}

	chatOpts := make([]huh.Option[string], 0, len(peers))
	for i, p := range peers {
		chatOpts = append(chatOpts, huh.NewOption(fmt.Sprintf("%-8s %s", p.Label(), p.Title), strconv.Itoa(i)))
	}
	var selChats []string
	if err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Чаты проекта (поиск — /)").
			Options(chatOpts...).
			Filterable(true).
			Value(&selChats),
	)).Run(); err != nil {
		return err
	}
	if len(selChats) == 0 {
		return fmt.Errorf("ничего не выбрано")
	}

	var entries []config.Entry
	for _, ci := range selChats {
		p := peers[atoi(ci)]
		topics, _ := tgx.Topics(ctx, client.API(), p.Input, 200)
		if len(topics) == 0 {
			entries = append(entries, entryFor(p, nil))
			continue
		}
		topicOpts := make([]huh.Option[string], 0, len(topics))
		for i, t := range topics {
			topicOpts = append(topicOpts, huh.NewOption(fmt.Sprintf("%6d  %s", t.ID, t.Title), strconv.Itoa(i)))
		}
		var selTopics []string
		if err := huh.NewForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Топики «"+p.Title+"» (поиск — /, пусто = весь чат)").
				Options(topicOpts...).
				Filterable(true).
				Value(&selTopics),
		)).Run(); err != nil {
			return err
		}
		if len(selTopics) == 0 {
			entries = append(entries, entryFor(p, nil))
			continue
		}
		for _, ti := range selTopics {
			t := topics[atoi(ti)]
			entries = append(entries, entryFor(p, &t))
		}
	}

	defOpts := make([]huh.Option[string], 0, len(entries))
	for i, e := range entries {
		defOpts = append(defOpts, huh.NewOption(e.Slug, strconv.Itoa(i)))
	}
	var defs []string
	if err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Импортировать каждый раз (дефолтные)").
			Options(defOpts...).
			Value(&defs),
	)).Run(); err != nil {
		return err
	}
	for _, di := range defs {
		entries[atoi(di)].Default = true
	}

	// справочник по записям (можно пропустить — заполнится позже вручную)
	var fillRef bool
	if err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Заполнить справочник по записям?").
			Affirmative("Да").
			Negative("Позже").
			Value(&fillRef),
	)).Run(); err != nil {
		return err
	}
	if fillRef {
		for i := range entries {
			e := &entries[i]
			if err := huh.NewForm(huh.NewGroup(
				huh.NewInput().Title(e.Slug + " — что за чат (note)").Value(&e.Note),
				huh.NewInput().Title(e.Slug + " — когда идти (when)").Value(&e.When),
				huh.NewInput().Title(e.Slug + " — кому писать (write_to)").Value(&e.WriteTo),
			)).Run(); err != nil {
				return err
			}
		}
	}

	// применяем режим к существующему конфигу
	bakPath := ""
	switch mode {
	case "replace":
		b, err := backupFile(cfgPath)
		if err != nil {
			return err
		}
		bakPath = b
	case "merge":
		if old, _, err := config.Load(dir); err == nil {
			entries = mergeEntries(old.Entries, entries)
		}
	}

	cfg := &config.Config{Project: project, Out: out, Entries: entries}
	path, err := config.Save(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("✓ конфиг: %s\n  проект: %s\n  раскладка: %s\n  записей: %d (дефолтных: %d)\n",
		path, project, out, len(entries), len(defs))
	if bakPath != "" {
		fmt.Printf("  бэкап старого: %s\n", bakPath)
	}
	return nil
}

func backupFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	bak := path + ".bak-" + time.Now().Format("20060102-150405")
	if err := os.WriteFile(bak, b, 0o644); err != nil {
		return "", err
	}
	return bak, nil
}

// mergeEntries: существующие записи + новые, которых ещё нет (по chat_id+topic_id).
func mergeEntries(existing, added []config.Entry) []config.Entry {
	key := func(e config.Entry) string { return fmt.Sprintf("%d:%d", e.ChatID, e.TopicID) }
	seen := map[string]bool{}
	out := append([]config.Entry{}, existing...)
	for _, e := range existing {
		seen[key(e)] = true
	}
	for _, e := range added {
		if !seen[key(e)] {
			out = append(out, e)
			seen[key(e)] = true
		}
	}
	return out
}

func entryFor(p tgx.Peer, t *tgx.Topic) config.Entry {
	e := config.Entry{Chat: p.Title, ChatID: p.ID, AccessHash: p.AccessHash, Kind: p.Kind}
	if t != nil {
		e.Topic = t.Title
		e.TopicID = t.ID
	}
	e.Slug = config.Slug(p.Title, e.Topic)
	return e
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
