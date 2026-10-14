package importer

import (
	"testing"
	"time"
)

// testNow — фиксированное «сейчас», чтобы решение о границах было воспроизводимым.
var testNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.Local)

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

// --- Поведение, которое должно сохраниться ---

func TestResolveRangeExplicitFromWins(t *testing.T) {
	from := day("2026-01-01")
	r, err := ResolveRange(from, time.Time{}, "", "", 0, false, false, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !r.From.Equal(from) {
		t.Errorf("явная нижняя граница потеряна: получили %v, ждали %v", r.From, from)
	}
	if r.Windowed {
		t.Error("окно применено, хотя нижняя граница задана явно")
	}
}

func TestResolveRangeFirstPassGetsWindow(t *testing.T) {
	r, err := ResolveRange(time.Time{}, time.Time{}, "", "", 0, false, false, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !r.Windowed {
		t.Fatal("первый проход без границ должен быть ограничен окном по умолчанию")
	}
	if want := testNow.Add(-DefaultWindow); !r.From.Equal(want) {
		t.Errorf("граница окна: получили %v, ждали %v", r.From, want)
	}
}

func TestResolveRangeFullSkipsWindow(t *testing.T) {
	r, err := ResolveRange(time.Time{}, time.Time{}, "", "", 0, false, true, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if r.Windowed {
		t.Error("-full не должен получать окно: он и так пересобирает всё")
	}
}

func TestParseDateEmpty(t *testing.T) {
	tm, err := ParseDate("")
	if err != nil {
		t.Fatalf("пустая строка не должна быть ошибкой: %v", err)
	}
	if !tm.IsZero() {
		t.Errorf("пустая строка должна давать нулевое время, получили %v", tm)
	}
}

// --- Дыры. Каждый тест падает, пока дыра открыта ---

// Дыра 1: задана только верхняя граница — окно делает диапазон пустым.
func TestResolveRangeToWithoutFrom(t *testing.T) {
	to := day("2026-01-01")
	r, err := ResolveRange(time.Time{}, to, "", "", 0, false, false, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if r.Windowed {
		t.Fatalf("задана только -to %s, а подставлено окно с %s: пересечение пусто, импорт вернёт 0 сообщений вместо истории до %s",
			to.Format("2006-01-02"), r.From.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	if !r.From.IsZero() {
		t.Errorf("нижняя граница должна остаться незаданной, получили %v", r.From)
	}
}

// Дыра 2: окно применяется и на инкрементальном проходе, а не только на первом.
func TestResolveRangeNoWindowWhenStateExists(t *testing.T) {
	r, err := ResolveRange(time.Time{}, time.Time{}, "", "", 500, false, false, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if r.Windowed {
		t.Fatalf("есть watermark last_id=500, но применено окно с %s: сообщения выше watermark, но старше окна, будут отброшены, а новый watermark уйдёт ещё выше — дыру потом не закрыть даже через -from",
			r.From.Format("2006-01-02"))
	}
}

// Дыра 3: -all-history не сбрасывает watermark, поэтому не добирает историю.
func TestResolveRangeAllHistoryIgnoresWatermark(t *testing.T) {
	r, err := ResolveRange(time.Time{}, time.Time{}, "", "", 500, true, false, testNow)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if !r.IgnoreWatermark {
		t.Fatal("-all-history с watermark last_id=500: MinID не сбрасывается, пагинация обрывается на первой странице — 0 сообщений, хотя CHANGELOG обещает полную историю")
	}
}

// Дыра 4: опечатка в дате молча превращается в окно по умолчанию.
func TestResolveRangeMalformedFrom(t *testing.T) {
	r, err := ResolveRange(time.Time{}, time.Time{}, "2026-1-5", "", 0, false, false, testNow)
	if err == nil {
		t.Fatalf("опечатка в дате (\"2026-1-5\") принята молча: подставлено окно с %s — пользователь думает, что его граница учтена",
			r.From.Format("2006-01-02"))
	}
}

func TestParseDateMalformed(t *testing.T) {
	for _, s := range []string{"2026-1-5", "01.01.2026", "2026-02-30", "вчера"} {
		if _, err := ParseDate(s); err == nil {
			t.Errorf("ParseDate(%q): ошибки нет — дата принята молча", s)
		}
	}
}
