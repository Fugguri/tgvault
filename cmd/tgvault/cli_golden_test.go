package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldenFixture — минимальный конфиг v2. Командам, которые не ходят в Telegram,
// его хватает, чтобы дойти до проверки ключей.
const goldenFixture = `{
  "version": 2,
  "project": "golden",
  "out": "docs/telegram_chats",
  "entries": [
    {
      "chat": "Тестовый чат",
      "chat_id": 1001,
      "access_hash": 42,
      "kind": "channel",
      "slug": "test",
      "default": true,
      "note": "фикстура"
    },
    {
      "chat": "Второй чат",
      "chat_id": 1002,
      "access_hash": 43,
      "kind": "chat",
      "slug": "second"
    }
  ]
}
`

// goldenMatrix — команды, которые не подключаются к Telegram и не ходят в сеть.
// Ключей в фикстуре нет, поэтому всё, что доходит до коннекта, умирает на их
// отсутствии. Команды с реальными действиями (send/manage/bot/dialogs/add/init)
// сюда не входят намеренно: до них дело не доходит, а если дойдёт — это уже
// не сравнение вывода, а отправка в живой чат.
var goldenMatrix = [][]string{
	{"-h"},
	{"list"},
	{"secret", "ls"},
	{"pending"},
	{"import"},
	{"import", "-all", "-full", "-all-history", "-no-secrets"},
	{"import", "-from", "2026-01-01"},
	{"import", "-from", "2026-01-01", "-to", "2026-02-01"},
	{"import", "-from", "2026-1-5"},
	{"import", "-to", "01.02.2026"},
	{"import", "-from", "вчера"},
	{"import", "-from", "2026-02-30"},
	{"import", "-nosuchflag"},
	{"migrate"},
	{"view", "-entry", "test", "-last", "5"},
	{"view", "-from", "вчера"},
	{"view", "-to", "2026-02-30"},
	{"nosuchcommand"},
}

// TestCLIGolden сверяет вывод CLI с эталоном testdata/cli_golden.txt.
// Ловит правки, меняющие поведение командной строки, — то, чего не видят тесты
// пакетов: они проверяют функции, а не то, что из них собрал main.
//
// Обновить эталон после намеренной правки: UPDATE_GOLDEN=1 go test ./cmd/tgvault
func TestCLIGolden(t *testing.T) {
	if testing.Short() {
		t.Skip("сборка бинаря под -short не гоняется")
	}
	// Бинарь линкуется с whisper.cpp через cgo — без собранных либ его не собрать.
	lib := filepath.Join("..", "..", "third_party", "whisper.cpp", "build_go", "src", "libwhisper.a")
	if _, err := os.Stat(lib); err != nil {
		t.Skipf("нет %s — сначала `make whisper-libs`", lib)
	}

	bin := filepath.Join(t.TempDir(), "tgvault")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("сборка бинаря: %v", err)
	}

	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, ".tg-import.json"), []byte(goldenFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	// Пустой HOME: ни глобального .env с ключами, ни хранилища секретов —
	// иначе вывод зависел бы от машины, где гоняются тесты.
	homeDir := t.TempDir()

	got := runGoldenMatrix(t, bin, workDir, homeDir)
	got = normalizeGolden(t, got, bin, workDir, homeDir)

	goldenPath := filepath.Join("testdata", "cli_golden.txt")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("эталон обновлён: %s (%d строк)", goldenPath, strings.Count(got, "\n"))
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("эталон не прочитан: %v (обнови: UPDATE_GOLDEN=1 go test ./cmd/tgvault)", err)
	}
	if got == string(want) {
		return
	}
	t.Errorf("вывод CLI разошёлся с эталоном:\n%s", firstDiff(string(want), got))
}

// runGoldenMatrix прогоняет матрицу и возвращает склеенный вывод: для каждой
// команды — её argv, stdout+stderr и код возврата.
func runGoldenMatrix(t *testing.T, bin, workDir, homeDir string) string {
	t.Helper()
	var out bytes.Buffer
	for _, args := range goldenMatrix {
		var body bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = workDir
		cmd.Env = []string{"HOME=" + homeDir, "PATH=" + os.Getenv("PATH")}
		cmd.Stdout = &body
		cmd.Stderr = &body
		err := cmd.Run()
		cancel()
		rc := 0
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		fmt.Fprintf(&out, "### %s\nrc=%d\n%s", strings.Join(args, " "), rc, body.String())
	}
	return out.String()
}

// normalizeGolden заменяет пути временных каталогов на метки: иначе эталон
// зависел бы от машины и от номера прогона.
func normalizeGolden(t *testing.T, s, bin, workDir, homeDir string) string {
	t.Helper()
	for _, r := range []struct{ path, label string }{
		{bin, "<BIN>"},
		{homeDir, "<HOME>"},
		{workDir, "<DIR>"},
	} {
		paths := []string{r.path}
		if real, err := filepath.EvalSymlinks(r.path); err == nil {
			paths = append(paths, real) // macOS: /tmp → /private/tmp
		}
		for _, p := range paths {
			s = strings.ReplaceAll(s, p, r.label)
		}
	}
	// Нормализация должна быть полной: оставшийся путь сделал бы эталон
	// зависимым от машины, и тест падал бы «на ровном месте».
	for _, p := range []string{bin, homeDir, workDir} {
		if strings.Contains(s, p) {
			t.Fatalf("в выводе остался путь %s — нормализация неполная", p)
		}
	}
	return s
}

// firstDiff показывает первые расхождения с контекстом, чтобы падение читалось
// без похода в файлы.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	shown := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl == gl {
			continue
		}
		fmt.Fprintf(&b, "  строка %d:\n    эталон: %q\n    получено: %q\n", i+1, wl, gl)
		if shown++; shown >= 10 {
			b.WriteString("  …\n")
			break
		}
	}
	return b.String()
}
