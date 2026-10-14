package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"github.com/Fugguri/tgvault/internal/secrets"
)

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "tgvault")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tgvault")
}

func defaultSession() string {
	return filepath.Join(configDir(), "session.json")
}

func loadEnv(strict bool) error {
	// глобальный .env (ключи, пути к whisper) — приоритетнее локального
	if p := filepath.Join(configDir(), ".env"); fileExists(p) {
		if err := godotenv.Load(p); err != nil {
			// Без этого битый .env выглядел бы как «ключи не заданы» — и уводил
			// бы искать причину не там.
			fmt.Fprintf(os.Stderr, "⚠ %s не прочитан: %v\n", p, err)
		}
	}
	dir, _ := os.Getwd()
	found := false
	for i := 0; i < 6 && dir != "" && dir != "/"; i++ {
		if p := filepath.Join(dir, ".env"); fileExists(p) {
			if err := godotenv.Load(p); err != nil {
				fmt.Fprintf(os.Stderr, "⚠ %s не прочитан: %v\n", p, err)
			}
			found = true
			break
		}
		dir = filepath.Dir(dir)
	}
	if !found && fileExists(".env") {
		if err := godotenv.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ .env не прочитан: %v\n", err)
		}
	}
	if err := secrets.Default().ExpandEnv(); err != nil {
		if strict {
			return fmt.Errorf("%w\n  заведи секрет: tgvault secret set NAME", err)
		}
		// Не падаем, но и не молчим: нераскрытая ссылка {{secret:…}} уходит
		// в вывод и в чат обычным текстом.
		fmt.Fprintf(os.Stderr, "⚠ %v\n", err)
	}
	return nil
}

// needsKeys — командам, которые работают с Telegram, нужны валидные ключи.
func needsKeys(cmd string) bool {
	switch cmd {
	case "secret", "update", "list", "setup", "approve", "pending":
		return false
	}
	return true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func env(k string, def ...string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
