// Package config описывает проектный конфиг tg-import v2 (.tg-import.json).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Version — текущая версия формата конфига.
const Version = 2

// Config — описание проекта: что импортировать и куда складывать.
type Config struct {
	Version int     `json:"version"`
	Project string  `json:"project"`
	Out     string  `json:"out"`  // корень раскладки (docs/telegram_chats)
	From    string  `json:"from,omitempty"` // YYYY-MM-DD, нижняя граница импорта
	To      string  `json:"to,omitempty"`   // YYYY-MM-DD, верхняя граница
	Entries []Entry `json:"entries"`
}

// Entry — запись реестра: чат (и, возможно, конкретный топик).
type Entry struct {
	Chat       string `json:"chat"`       // человекочитаемое имя чата
	ChatID     int64  `json:"chat_id"`    // raw entity id
	AccessHash int64  `json:"access_hash"` // access_hash для построения peer
	Kind       string `json:"kind"`       // user | chat | channel
	Topic      string `json:"topic,omitempty"`    // имя топика (если форум)
	TopicID    int    `json:"topic_id,omitempty"` // top message id топика
	Slug       string `json:"slug"`               // имя папки (плоско, <chat>__<topic>)
	Default    bool   `json:"default"`            // импортировать каждый раз
	Note       string `json:"note,omitempty"`     // справочник: что за чат
	When       string `json:"when,omitempty"`     // справочник: когда идти
	WriteTo    string `json:"write_to,omitempty"` // справочник: кому писать
}

// Path — путь к конфигу проекта.
const Path = ".tg-import.json"

// Load читает конфиг из startPath (или вверх по дереву).
func Load(startPath string) (*Config, string, error) {
	dir, err := filepath.Abs(startPath)
	if err != nil {
		return nil, "", err
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, Path)
		if b, err := os.ReadFile(p); err == nil {
			var c Config
			if err := json.Unmarshal(b, &c); err != nil {
				return nil, p, fmt.Errorf("парсинг %s: %w", p, err)
			}
			return &c, p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", os.ErrNotExist
}

// Save пишет конфиг в dir по стандартному имени.
func Save(dir string, c *Config) (string, error) {
	c.Version = Version
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, Path)
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return p, nil
}

// Slug строит безопасное имя папки: <chat> или <chat>_<topic>.
func Slug(chat, topic string) string {
	s := sanitize(chat)
	if topic != "" {
		s += "_" + sanitize(topic)
	}
	if s == "" {
		s = "chat"
	}
	return s
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case unicode.IsSpace(r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "_")
}
