package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	s, err := LoadState(t.TempDir())
	if err != nil {
		t.Fatalf("отсутствие состояния — это норма, а не ошибка: %v", err)
	}
	if s.LastID != 0 {
		t.Errorf("ожидали нулевой watermark, получили %d", s.LastID)
	}
}

func TestLoadStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := SaveState(dir, State{LastID: 42, UpdatedAt: "2026-10-14T00:00:00Z"}); err != nil {
		t.Fatalf("сохранение состояния: %v", err)
	}
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	if s.LastID != 42 {
		t.Errorf("watermark: получили %d, ждали 42", s.LastID)
	}
}

// Битый state раньше принимался молча: json.Unmarshal возвращал ошибку, её
// отбрасывали, watermark обнулялся — и импорт либо тянул всю историю заново,
// либо тихо терял сообщения. Снаружи это выглядело как «Готово: 0 новых».
func TestLoadStateCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateName), []byte("{не json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(dir)
	if err == nil {
		t.Fatal("битый .tg-state.json принят молча: watermark обнулился бы без единого сигнала")
	}
	if !strings.Contains(err.Error(), stateName) {
		t.Errorf("в ошибке нет имени файла, чинить нечего: %v", err)
	}
}
