package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Fugguri/tgvault/internal/update"
)

// Скилл агента живёт вне проекта, поэтому обновлять его обязан `tgvault update`:
// раньше SKILL.md клал только install.sh, и после смены бинаря агент продолжал
// читать инструкцию прошлых релизов — не знал про `view`, `manage` и протокол
// согласования. Файл, который правили руками, не теряем: прежняя версия
// сохраняется рядом как `.old`, а свежая встаёт рабочей.

const (
	skillFile     = "SKILL.md"
	skillHashFile = "skill.sha"
)

// skillDirs — те же каталоги, что раскладывает install.sh. Синкаем только те,
// чей родитель уже существует: скилл-каталоги агентов, которых нет, не создаём.
func skillDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range []string{
		filepath.Join(home, ".claude", "skills", "tgvault"),
		filepath.Join(home, ".config", "opencode", "skills", "tgvault"),
	} {
		if _, err := os.Stat(filepath.Dir(d)); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// skillStatePath — файл с хешем последней выложенной нами версии скилла.
// Общий с install.sh, чтобы обе точки входа принимали одно решение.
func skillStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "tgvault", skillHashFile)
}

type skillAction int

const (
	skillCurrent       skillAction = iota // установлена уже эта версия
	skillInstall                          // писать поверх: файла нет либо он ровно наш
	skillInstallBackup                    // файл правили руками — прежний в .old, новый на место
)

// decideSkill выбирает действие по трём хешам: cur — установленного файла
// (пустая строка, если файла нет), old — последней выложенной нами версии,
// new — скачанной.
func decideSkill(cur, old, new string) skillAction {
	switch {
	case cur == new:
		return skillCurrent
	case cur == "" || cur == old:
		return skillInstall
	default:
		return skillInstallBackup
	}
}

// SyncSkill скачивает SKILL.md версии tag и раскладывает по каталогам агентов.
func SyncSkill(ctx context.Context, repo, tag string) error {
	dirs := skillDirs()
	if len(dirs) == 0 {
		return nil
	}
	body, err := update.FetchRaw(ctx, repo, tag, skillFile)
	if err != nil {
		return fmt.Errorf("скилл: %w", err)
	}
	newSHA := sha256Hex(body)
	oldSHA := readTrimmed(skillStatePath())

	for _, d := range dirs {
		p := filepath.Join(d, skillFile)
		cur, err := fileSHA(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ скилл %s: %v\n", p, err)
			continue
		}
		if err := applySkill(p, d, body, decideSkill(cur, oldSHA, newSHA)); err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ скилл %s: %v\n", p, err)
		}
	}

	// Хеш запоминаем только для той версии, которую действительно выложили:
	// иначе правка руками в следующий раз будет считаться «нашей».
	h := skillStatePath()
	if h == "" || newSHA == oldSHA {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(h), 0o700); err != nil {
		return fmt.Errorf("каталог состояния скилла: %w", err)
	}
	return os.WriteFile(h, []byte(newSHA+"\n"), 0o600)
}

func applySkill(path, dir string, body []byte, act skillAction) error {
	if act == skillCurrent {
		fmt.Printf("✓ скилл актуален: %s\n", path)
		return nil
	}
	if act == skillInstallBackup {
		// Правку руками не теряем, но и не оставляем её рабочей: агент должен
		// читать свежую инструкцию, поэтому прежняя версия уезжает в .old,
		// а свежая встаёт на её место.
		old, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path+".old", old, 0o644); err != nil {
			return err
		}
		fmt.Printf("• скилл правили руками — прежняя версия сохранена: %s.old\n", path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	fmt.Printf("✓ скилл обновлён: %s\n", path)
	return nil
}

// fileSHA — sha256 файла; для отсутствующего файла пустая строка без ошибки.
func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return sha256Hex(b), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func readTrimmed(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
