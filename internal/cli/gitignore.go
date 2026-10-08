package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ensureGitignore добавляет папку раскладки и файл конфига в .gitignore проекта.
func ensureGitignore(projectDir, out string) {
	out = strings.TrimSpace(out)
	if out == "" {
		out = "docs/telegram_chats"
	}
	want := []string{
		strings.TrimSuffix(out, "/") + "/",
		".tg-import.json",
	}

	gi := filepath.Join(projectDir, ".gitignore")
	b, _ := os.ReadFile(gi)
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
		return
	}
	f, err := os.OpenFile(gi, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	fmt.Fprintf(f, "%s# tgvault\n", prefix)
	for _, a := range add {
		fmt.Fprintln(f, a)
	}
	fmt.Printf("✓ .gitignore: %s\n", strings.Join(add, ", "))
}
