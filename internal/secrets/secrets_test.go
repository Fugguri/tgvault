package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{Root: t.TempDir(), Scope: DefaultScope}
}

func TestSetGetListRemove(t *testing.T) {
	s := newStore(t)
	if err := s.Set("TOKEN", "s3cret", false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get("TOKEN")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "s3cret" {
		t.Fatalf("Get = %q, want s3cret", got)
	}
	if refs := s.List(); len(refs) != 1 || refs[0].String() != "tgvault/TOKEN" {
		t.Fatalf("List = %v", refs)
	}
	if err := s.Remove("TOKEN"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := s.Get("TOKEN"); err == nil {
		t.Fatal("Get после Remove не вернул ошибку")
	}
}

func TestSetRefusesOverwriteWithoutForce(t *testing.T) {
	s := newStore(t)
	if err := s.Set("TOKEN", "a", false); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("TOKEN", "b", false); err == nil {
		t.Fatal("повторная запись без force прошла")
	}
	if err := s.Set("TOKEN", "b", true); err != nil {
		t.Fatalf("force-запись: %v", err)
	}
	if v, _ := s.Get("TOKEN"); v != "b" {
		t.Fatalf("после force = %q", v)
	}
}

func TestSetModeIsPrivate(t *testing.T) {
	s := newStore(t)
	if err := s.Set("TOKEN", "x", false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(s.Root, "tgvault", "TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("права %04o, want 0600", info.Mode().Perm())
	}
}

func TestParseRejectsTraversal(t *testing.T) {
	s := newStore(t)
	for _, bad := range []string{"../evil", "a/b/c", "", "a b", "a/../b"} {
		if _, err := s.Parse(bad); err == nil {
			t.Fatalf("Parse(%q) не отклонил", bad)
		}
	}
	if r, err := s.Parse("other/NAME"); err != nil || r.String() != "other/NAME" {
		t.Fatalf("Parse(scope/NAME) = %v, %v", r, err)
	}
}

func TestExpandValue(t *testing.T) {
	s := newStore(t)
	_ = s.Set("HASH", "abc123", false)
	got, err := s.ExpandValue("api={{secret:HASH}};")
	if err != nil {
		t.Fatal(err)
	}
	if got != "api=abc123;" {
		t.Fatalf("ExpandValue = %q", got)
	}
	if _, err := s.ExpandValue("{{secret:MISSING}}"); err == nil {
		t.Fatal("отсутствующий секрет не дал ошибку")
	}
}

func TestExpandEnv(t *testing.T) {
	s := newStore(t)
	_ = s.Set("HASH", "abc123", false)
	t.Setenv("TEST_TG_HASH", "{{secret:HASH}}")
	if err := s.ExpandEnv(); err != nil {
		t.Fatal(err)
	}
	if v := os.Getenv("TEST_TG_HASH"); v != "abc123" {
		t.Fatalf("ExpandEnv привёл к %q", v)
	}
}

func TestMaskUsesCanonicalNameForRepeat(t *testing.T) {
	s := newStore(t)
	// одно и то же значение под двумя именами: короткое — каноническое
	_ = s.Set("VERYLONGNAME", "same-value", false)
	_ = s.Set("SHORT", "same-value", false)
	masked := s.Mask("token=same-value end")
	if !strings.Contains(masked, "{{secret:tgvault/SHORT}}") {
		t.Fatalf("Mask = %q, ожидалась ссылка на SHORT", masked)
	}
	if strings.Contains(masked, "same-value") {
		t.Fatalf("значение не замаскировано: %q", masked)
	}
}

func TestMaskKeepsLongest(t *testing.T) {
	s := newStore(t)
	_ = s.Set("SHORT", "abc", false)
	_ = s.Set("LONG", "abcdef", false)
	masked := s.Mask("abcdef abc")
	if masked != "{{secret:tgvault/LONG}} {{secret:tgvault/SHORT}}" {
		t.Fatalf("Mask = %q", masked)
	}
}

func TestMaskEnvHidesSecretVars(t *testing.T) {
	t.Setenv("TG_API_HASH", "deadbeefcafe1234")
	t.Setenv("SOME_PLAIN", "visible")
	got := MaskEnv("hash=deadbeefcafe1234 plain=visible")
	if strings.Contains(got, "deadbeefcafe1234") {
		t.Fatalf("значение секретной переменной не скрыто: %q", got)
	}
	if !strings.Contains(got, "{{secret:TG_API_HASH}}") {
		t.Fatalf("нет ссылки на переменную: %q", got)
	}
	if !strings.Contains(got, "visible") {
		t.Fatalf("обычное значение затронуто: %q", got)
	}
}

func TestCheckFlagsDuplicateAndPerms(t *testing.T) {
	s := newStore(t)
	_ = s.Set("ONE", "dup", false)
	_ = s.Set("TWO", "dup", false)
	p := filepath.Join(s.Root, "tgvault", "ONE")
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	issues := s.Check()
	var dup, perm bool
	for _, is := range issues {
		if strings.Contains(is.Message, "несколькими именами") {
			dup = true
		}
		if strings.Contains(is.Message, "читаемо другими") {
			perm = true
		}
	}
	if !dup {
		t.Fatalf("дубль значения не найден: %v", issues)
	}
	if !perm {
		t.Fatalf("слабые права не найдены: %v", issues)
	}
}
