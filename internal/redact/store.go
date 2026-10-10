package redact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const fileVersion = 1

// SecretLog — секреты одного чата: docs/telegram_chats/<slug>/.secrets.json.
// Имя секрета детерминировано от значения, поэтому повтор значения даёт одно имя.
type SecretLog struct {
	path    string
	byValue map[string]string
	values  map[string]string
	added   int
	dirty   bool
}

// LoadSecretLog читает файл секретов чата (или создаёт пустой, если его нет).
func LoadSecretLog(path string) *SecretLog {
	l := &SecretLog{path: path, byValue: map[string]string{}, values: map[string]string{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return l
	}
	var f struct {
		Secrets map[string]string `json:"secrets"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return l
	}
	for name, val := range f.Secrets {
		l.values[name] = val
		if _, ok := l.byValue[val]; !ok {
			l.byValue[val] = name
		}
	}
	return l
}

// Put возвращает имя для значения (создавая и запоминая новое при необходимости).
func (l *SecretLog) Put(kind, value string) string {
	if name, ok := l.byValue[value]; ok {
		return name
	}
	name := l.uniqueName(kind, value)
	l.values[name] = value
	l.byValue[value] = name
	l.added++
	l.dirty = true
	return name
}

func (l *SecretLog) uniqueName(kind, value string) string {
	h := sha256.Sum256([]byte(value))
	base := kind + "_" + hex.EncodeToString(h[:4])
	name := base
	for i := 0; ; i++ {
		if _, exists := l.values[name]; !exists {
			return name
		}
		name = fmt.Sprintf("%s_%d", base, i)
	}
}

// Names — отсортированные имена секретов (без значений).
func (l *SecretLog) Names() []string {
	out := make([]string, 0, len(l.values))
	for n := range l.values {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Value возвращает значение по имени.
func (l *SecretLog) Value(name string) (string, bool) {
	v, ok := l.values[name]
	return v, ok
}

// Added — сколько новых секретов добавлено с момента загрузки.
func (l *SecretLog) Added() int { return l.added }

// Save пишет файл (0600) атомарно; без изменений — ничего не делает.
func (l *SecretLog) Save() error {
	if !l.dirty {
		return nil
	}
	doc := struct {
		Version int               `json:"version"`
		Secrets map[string]string `json:"secrets"`
	}{Version: fileVersion, Secrets: l.values}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
