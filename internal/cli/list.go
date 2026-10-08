package cli

import (
	"fmt"
	"os"

	"github.com/Fugguri/tgvault/internal/config"
)

// List печатает компактную сводку записей проекта (для агента/человека).
func List(dir string) error {
	cfg, cfgPath, err := config.Load(dir)
	if err != nil {
		return fmt.Errorf("конфиг не найден: %w", err)
	}
	fmt.Printf("# %s  (%s)\n", cfg.Project, cfgPath)
	fmt.Printf("# out: %s\n", cfg.Out)
	for _, e := range cfg.Entries {
		def := " "
		if e.Default {
			def = "*"
		}
		note := e.Note
		if e.When != "" {
			if note != "" {
				note += " | "
			}
			note += e.When
		}
		if len(note) > 60 {
			note = note[:60] + "…"
		}
		topic := ""
		if e.Topic != "" {
			topic = " [" + e.Topic + "]"
		}
		fmt.Printf("%s %-28s %s%s  %s\n", def, e.Slug, e.Chat, topic, note)
	}
	fmt.Fprintln(os.Stderr, "(* = импортируется каждый раз)")
	return nil
}
