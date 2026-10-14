package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ensureGitignore добавляет папку раскладки и файл конфига в .gitignore проекта.
// Ошибку возвращает наружу: если строки не дописались, переписка и .secrets.json
// могут уехать в git, а молчаливый возврат делал бы это незаметным.
func ensureGitignore(projectDir, out string) error {
	out = strings.TrimSpace(out)
	if out == "" {
		out = "docs/telegram_chats"
	}
	want := []string{
		strings.TrimSuffix(out, "/") + "/",
		".tg-import.json",
		"**/.secrets.json",
	}

	gi := filepath.Join(projectDir, ".gitignore")
	b, err := os.ReadFile(gi)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("прочитать %s: %w", gi, err)
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(l)
		have[s] = true
		have[strings.TrimSuffix(s, "/")] = true
	}

	var add []string
	for _, w := range want {
		if !have[w] {
			add = append(add, w)
			have[w] = true
		}
	}
	if len(add) == 0 {
		return nil
	}
	f, err := os.OpenFile(gi, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("открыть %s: %w", gi, err)
	}
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	var werr error
	if _, err := fmt.Fprintf(f, "%s# tgvault\n", prefix); err != nil {
		werr = err
	}
	for _, a := range add {
		if werr != nil {
			break
		}
		if _, err := fmt.Fprintln(f, a); err != nil {
			werr = err
		}
	}
	if err := f.Close(); err != nil && werr == nil {
		werr = err
	}
	if werr != nil {
		return fmt.Errorf("записать %s: %w", gi, werr)
	}
	fmt.Printf("✓ .gitignore: %s\n", strings.Join(add, ", "))
	return nil
}
