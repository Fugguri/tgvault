package importer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/redact"
)

func TestBuildSectionRedactsSecrets(t *testing.T) {
	dir := t.TempDir()
	sec := redact.LoadSecretLog(filepath.Join(dir, ".secrets.json"))
	auth := &authorizer{fallback: "чат"}
	m := &tg.Message{ID: 1, Date: 0, Message: "вот токен 5123456789:AAFabcdefghijklmnopqrstuvwxyz0123456789 и всё"}

	out, _, err := buildSection(context.Background(), nil, m, auth, dir, dir, nil, time.UTC, map[int]string{}, nil, sec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "5123456789:AAF") {
		t.Fatalf("токен остался в логе:\n%s", out)
	}
	if !strings.Contains(out, "{{secret:tg_") {
		t.Fatalf("нет ссылки на секрет:\n%s", out)
	}
	if sec.Added() != 1 {
		t.Fatalf("Added = %d, want 1", sec.Added())
	}
	if err := sec.Save(); err != nil {
		t.Fatal(err)
	}

	// второе сообщение с тем же токеном — то же имя, без нового секрета
	m2 := &tg.Message{ID: 2, Message: "снова 5123456789:AAFabcdefghijklmnopqrstuvwxyz0123456789"}
	sec2 := redact.LoadSecretLog(filepath.Join(dir, ".secrets.json"))
	out2, _, err := buildSection(context.Background(), nil, m2, auth, dir, dir, nil, time.UTC, map[int]string{}, nil, sec2)
	if err != nil {
		t.Fatal(err)
	}
	if sec2.Added() != 0 {
		t.Fatalf("повтор дал новый секрет: Added = %d", sec2.Added())
	}
	i := strings.Index(out, "{{secret:")
	tag := out[i : strings.Index(out[i:], "}}")+i+2]
	if !strings.Contains(out2, tag) {
		t.Fatalf("повтор дал другую ссылку (%s != %s):\n%s\n%s", tag, out2, out, out2)
	}
}
