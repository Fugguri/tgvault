package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gotd/td/telegram"

	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgx"
)

// MigrateOpts — параметры миграции v1→v2.
type MigrateOpts struct {
	NewOut string // новый корень раскладки (default docs/telegram_chats)
}

// Migrate переписывает старый конфиг (.tg-import.json v1) в формат v2 и
// переносит старые папки логов под новый корень. Делает бэкап старого конфига.
func Migrate(ctx context.Context, client *telegram.Client, dir string, o MigrateOpts) error {
	cfgPath := filepath.Join(dir, config.Path)
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("нет %s: %w", config.Path, err)
	}
	if !config.IsV1(raw) {
		return fmt.Errorf("%s не похож на v1 (нет chats/уже есть version)", config.Path)
	}
	v1, err := config.ParseV1(raw)
	if err != nil {
		return fmt.Errorf("парсинг v1: %w", err)
	}

	oldOut := v1.Out
	if oldOut == "" {
		oldOut = "docs"
	}
	oldRoot := resolveDir(dir, oldOut)
	newOut := o.NewOut
	if newOut == "" {
		newOut = "docs/telegram_chats"
	}
	newRoot := resolveDir(dir, newOut)

	// бэкап старого конфига
	bak := cfgPath + ".v1.bak"
	if err := os.WriteFile(bak, raw, 0o644); err != nil {
		return fmt.Errorf("бэкап: %w", err)
	}
	fmt.Printf("бэкап старого конфига: %s\n", bak)

	api := client.API()
	var entries []config.Entry
	for _, ch := range v1.Chats {
		var peer tgx.Peer
		var ok bool
		switch {
		case ch.ID != 0:
			peer, ok, _ = tgx.Resolve(ctx, api, ch.ID)
		case ch.Name != "":
			peer, ok, _ = tgx.Find(ctx, api, ch.Name)
		}
		if !ok {
			fmt.Printf("  ⚠ не найден: %+v\n", ch)
			continue
		}

		oldSlug := ch.Alias
		if oldSlug == "" {
			oldSlug = ch.Slug
		}
		if oldSlug == "" {
			oldSlug = config.Slug(peer.Title, "")
		}

		e := config.Entry{
			Chat:       peer.Title,
			ChatID:     peer.ID,
			AccessHash: peer.AccessHash,
			Kind:       peer.Kind,
			Slug:       oldSlug,
			Default:    true,
		}
		if tid := ch.TopicIDOf(); tid != 0 {
			e.TopicID = tid
			if topics, err := tgx.Topics(ctx, api, peer.Input, 200); err == nil {
				for _, t := range topics {
					if t.ID == tid {
						e.Topic = t.Title
					}
				}
			}
		}
		entries = append(entries, e)

		// перенос папки логов <oldRoot>/<oldSlug> → <newRoot>/<oldSlug>
		from := filepath.Join(oldRoot, oldSlug)
		to := filepath.Join(newRoot, oldSlug)
		if from == to {
			continue
		}
		if _, err := os.Stat(from); err != nil {
			fmt.Printf("  ⚠ папка не найдена, пропуск переноса: %s\n", from)
			continue
		}
		if err := moveDir(from, to); err != nil {
			fmt.Printf("  ⚠ перенос %s → %s: %v\n", from, to, err)
		} else {
			fmt.Printf("  перенесено: %s → %s\n", from, to)
		}
	}

	cfg := &config.Config{
		Project: filepath.Base(dir),
		Out:     newOut,
		From:    v1.From,
		To:      v1.To,
		Entries: entries,
	}
	saved, err := config.Save(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Printf("✓ v2 конфиг: %s (%d записей)\n", saved, len(entries))
	return nil
}

func resolveDir(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}

func moveDir(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	// другой раздел — копируем
	return copyTree(from, to)
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}
		return copyFile(path, dst, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
