package importer

import (
	"fmt"
	"time"
)

// ParseDate разбирает дату вида YYYY-MM-DD в локальной зоне.
// Пустая строка — нулевое время без ошибки.
func ParseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("дата %q: нужен формат YYYY-MM-DD", s)
	}
	return t, nil
}

// Range — итоговые границы импорта.
type Range struct {
	From, To        time.Time
	Windowed        bool // From подставлен окном по умолчанию, а не пользователем
	IgnoreWatermark bool // тянуть историю заново, не отсекая по MinID (-all-history)
}

// ResolveRange вычисляет границы импорта для одной записи конфига.
// minID — watermark из состояния записи (0 = состояния нет, это первый проход).
// cfgFrom/cfgTo — даты из конфига проекта, нужны только там, где граница не
// задана явно.
//
// now передаётся параметром, а не берётся из time.Now, чтобы решение было
// воспроизводимым в тестах.
func ResolveRange(from, to time.Time, cfgFrom, cfgTo string, minID int, unbounded, full bool, now time.Time) (Range, error) {
	// Кривая дата в конфиге — это сломанный конфиг, а не «граница не задана»,
	// поэтому ошибка идёт наружу, а не превращается молча в нулевое время.
	if from.IsZero() {
		t, err := ParseDate(cfgFrom)
		if err != nil {
			return Range{}, fmt.Errorf("from в конфиге: %w", err)
		}
		from = t
	}
	if to.IsZero() {
		t, err := ParseDate(cfgTo)
		if err != nil {
			return Range{}, fmt.Errorf("to в конфиге: %w", err)
		}
		to = t
	}

	r := Range{From: from, To: to}

	// Окно по умолчанию — только для самого первого прохода по записи
	// (состояния ещё нет). На инкрементальном проходе окно отсекает сообщения
	// выше watermark, но старше окна, а новый watermark уезжает выше них —
	// дыра остаётся навсегда и штатными флагами не закрывается.
	// При заданной верхней границе окно тоже не нужно: пересечение пусто.
	if from.IsZero() && to.IsZero() && minID == 0 && !unbounded && !full {
		r.From = now.Add(-DefaultWindow)
		r.Windowed = true
	}

	// -all-history значит «всю историю заново»: watermark надо снять, иначе
	// пагинация обрывается на первой же странице и импорт даёт 0 сообщений.
	if unbounded {
		r.IgnoreWatermark = true
	}
	return r, nil
}
