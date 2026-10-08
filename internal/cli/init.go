package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/charmbracelet/huh"
	"github.com/gotd/td/telegram"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// Init запускает интерактивный мастер проекта: вопросы, выбор чатов и
// топиков (с поиском), выбор дефолтных записей, запись .tg-import.json.
func Init(ctx context.Context, client *telegram.Client, dir string) error {
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

	cfg := &config.Config{Project: project, Out: out, Entries: entries}
	path, err := config.Save(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("✓ конфиг: %s\n  проект: %s\n  раскладка: %s\n  записей: %d (дефолтных: %d)\n",
		path, project, out, len(entries), len(defs))
	return nil
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
