// Package approve — одноразовые согласования исходящих действий.
//
// Модель: содержимое действия (payload) хэшируется в id. Перед реальным
// действием (send/manage/bot) команда требует согласование именно этого id.
// Согласование выдаёт только человек в терминале (`tgvault approve <id>`), оно
// живёт ограниченное время (TTL) и расходуется один раз. Меняется текст — id
// другой — старое согласование не подходит.
package approve

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultTTL — сколько действует согласование по умолчанию.
const DefaultTTL = 5 * time.Minute

// Pending — зарегистрированный запрос на согласование.
type Pending struct {
	ID      string    `json:"id"`
	Action  string    `json:"action"`
	Summary string    `json:"summary"`
	Created time.Time `json:"created"`
	Expiry  time.Time `json:"expiry,omitempty"`
}

// Approved сообщает, что запрос согласован и ещё не истёк.
func (p Pending) Approved() bool {
	return !p.Expiry.IsZero() && time.Now().Before(p.Expiry)
}

// NotApprovedError — согласования нет/оно истекло/его не выдавали.
type NotApprovedError struct {
	ID     string
	Reason string
}

func (e *NotApprovedError) Error() string {
	return fmt.Sprintf("действие #%s не согласовано (%s)", e.ID, e.Reason)
}

// Dir возвращает каталог согласований (можно переопределить TGVAULT_APPROVE_DIR).
func Dir() string {
	if d := os.Getenv("TGVAULT_APPROVE_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "tgvault", "approvals")
}

// ID — короткий детерминированный хэш действия и его содержимого.
func ID(action string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(action))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Canon склеивает поля в стабильную каноническую строку.
func Canon(parts ...string) []byte {
	return []byte(strings.Join(parts, "\x1f"))
}

func path(id string) string { return filepath.Join(Dir(), id+".json") }

// Request регистрирует (или обновляет) запрос на согласование.
func Request(id, action, summary string) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	p := Pending{ID: id, Action: action, Summary: summary, Created: time.Now()}
	if old, err := Load(id); err == nil {
		p.Created = old.Created
		if old.Approved() {
			p.Expiry = old.Expiry
		}
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(id), b, 0o600)
}

// Load читает запрос по id.
func Load(id string) (Pending, error) {
	b, err := os.ReadFile(path(id))
	if err != nil {
		return Pending{}, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return Pending{}, err
	}
	return p, nil
}

// Grant отмечает запрос согласованным до now+ttl.
func Grant(id string, ttl time.Duration) error {
	p, err := Load(id)
	if err != nil {
		return fmt.Errorf("запрос #%s не найден", id)
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	p.Expiry = time.Now().Add(ttl)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(id), b, 0o600)
}

// Consume проверяет согласование и съедает его (одноразово).
func Consume(id string) error {
	p, err := Load(id)
	if err != nil {
		return &NotApprovedError{ID: id, Reason: "запрос не найден"}
	}
	if p.Expiry.IsZero() {
		return &NotApprovedError{ID: id, Reason: "ожидает подтверждения"}
	}
	if time.Now().After(p.Expiry) {
		_ = os.Remove(path(id))
		return &NotApprovedError{ID: id, Reason: "согласование истекло"}
	}
	if err := os.Remove(path(id)); err != nil {
		return err
	}
	return nil
}

// List перечисляет запросы (свежие сверху).
func List() ([]Pending, error) {
	entries, err := os.ReadDir(Dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Pending
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if p, err := Load(strings.TrimSuffix(e.Name(), ".json")); err == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Clean удаляет просроченные невыданные запросы старше maxAge.
func Clean(maxAge time.Duration) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := Load(id)
		if err != nil {
			continue
		}
		if !p.Approved() && time.Since(p.Created) > maxAge {
			_ = os.Remove(path(id))
		}
	}
}
