package redact

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactDetectsTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		kind string
	}{
		{"telegram", "вот токен 5123456789:AAF-abcdefghijklmnopqrstuvwxyz0123456789 и всё", "tg"},
		{"openai", "ключ sk-abcdefghijklmnopqrstuvwxyz0123456789", "openai"},
		{"github", "ghp_" + strings.Repeat("a", 36), "github"},
		{"aws", "AKIAIOSFODNN7EXAMPLE", "aws"},
		{"google", "AIzaSyA1234567890abcdefghijklmnopqrstUV", "google"},
		{"apikey", "api_key = abcdef1234567890xyz", "apikey"},
	}
	for _, c := range cases {
		got, n := Redact(c.text, func(kind, value string) string {
			if kind != c.kind {
				t.Errorf("%s: kind = %q, want %q", c.name, kind, c.kind)
			}
			return "X"
		})
		if n == 0 {
			t.Errorf("%s: ничего не найдено в %q", c.name, c.text)
		}
		if strings.Contains(got, "{{secret:X}}") == false {
			t.Errorf("%s: нет ссылки в %q", c.name, got)
		}
	}
}

func TestRedactIgnoresOrdinaryText(t *testing.T) {
	text := "привет, как дела? срок 2026-10-08, id 123456"
	got, n := Redact(text, func(kind, value string) string { return "X" })
	if n != 0 || got != text {
		t.Fatalf("ложное срабатывание: n=%d, %q", n, got)
	}
}

func TestRedactNoOverlap(t *testing.T) {
	// telegram-токен не должен разъехаться по частям
	text := "5123456789:AAFabcdefghijklmnopqrstuvwxyz0123456789"
	got, n := Redact(text, func(kind, value string) string { return "tg" })
	if n != 1 {
		t.Fatalf("ожидалась одна замена, получили %d: %q", n, got)
	}
	if got != "{{secret:tg}}" {
		t.Fatalf("got %q", got)
	}
}

func TestPutDedupesByValue(t *testing.T) {
	l := &SecretLog{path: filepath.Join(t.TempDir(), ".secrets.json"), byValue: map[string]string{}, values: map[string]string{}}
	a := l.Put("tg", "same-token-value")
	b := l.Put("tg", "same-token-value")
	if a != b {
		t.Fatalf("одно значение дало разные имена: %q vs %q", a, b)
	}
	if l.Added() != 1 {
		t.Fatalf("Added = %d, want 1", l.Added())
	}
	if c := l.Put("apikey", "другое"); c == a {
		t.Fatal("разные значения дали одно имя")
	}
}

func TestSecretLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", ".secrets.json")
	l := LoadSecretLog(path)
	name := l.Put("tg", "5123456789:AAFabcdefghijklmnopqrstuvwxyz0123456789")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := LoadSecretLog(path)
	v, ok := reloaded.Value(name)
	if !ok || v != "5123456789:AAFabcdefghijklmnopqrstuvwxyz0123456789" {
		t.Fatalf("после перезагрузки Value = %q, %v", v, ok)
	}
	if reloaded.Added() != 0 {
		t.Fatalf("Added после загрузки = %d, want 0", reloaded.Added())
	}
	// повторное сохранение без изменений — no-op
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
}
