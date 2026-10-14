package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/Fugguri/tgvault/internal/importer"
)

// «Готово: 0 новых сообщений» само по себе не отличимо от «фильтры отсекли всё».
// whyNothing обязан назвать причину для каждого случая — иначе агент не поймёт,
// надо ли что-то менять, а пользователь не поймёт, ждать ли ещё.
func TestWhyNothingExplainsEachCase(t *testing.T) {
	day := time.Date(2026, 9, 14, 0, 0, 0, 0, time.Local)
	cases := []struct {
		name string
		rng  importer.Range
		id   int
		want string
	}{
		{"окно по умолчанию", importer.Range{From: day, Windowed: true}, 0, "-all-history"},
		{"watermark снят, но пусто", importer.Range{IgnoreWatermark: true}, 500, "watermark снят"},
		{"состояние актуально", importer.Range{}, 500, "состояние актуально"},
		{"диапазон ничего не захватил", importer.Range{From: day}, 0, "-from/-to"},
		{"в чате пусто", importer.Range{}, 0, "нет сообщений"},
	}
	for _, c := range cases {
		got := whyNothing(c.rng, c.id)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: получили %q, ждали упоминание %q", c.name, got, c.want)
		}
	}
}
