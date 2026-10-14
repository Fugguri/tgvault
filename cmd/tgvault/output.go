package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/Fugguri/tgvault/internal/secrets"
)

func die(format string, a ...any) {
	msg := maskText(fmt.Sprintf(format, a...))
	fmt.Fprintln(os.Stderr, "ошибка: "+msg)
	os.Exit(1)
}

// maskText прячет ключи от LLM: значения из хранилища и значения секретных
// переменных окружения (.env). Сам бинарь читает .env как обычно.
func maskText(s string) string {
	return secrets.MaskEnv(secrets.Default().Mask(s))
}

// maskWriter затирает секреты по ссылкам во всём, что пишется в поток
// (например, в выводе флагов по умолчанию).
type maskWriter struct{ w io.Writer }

func (m maskWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(m.w, maskText(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// usage печатает список команд и флаги (через maskWriter — без значений секретов).
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprint(out, `tgvault — импорт Telegram-чатов в Markdown.

Использование:
  tgvault <команда> [флаги]

Команды:
  login              вход в Telegram (сессия глобальная)
  setup              мастер установки: модели, зависимости, ключи
  init / add         конфиг проекта (init — интерактивно, add — для агентов)
  import             импорт по конфигу (-all, -entry, -from/-to, -full)
  view               просмотр сообщений за период БЕЗ импорта (-chat/-entry, -from/-to, -last)
  migrate            старый конфиг v1 → v2
  list               сводка записей проекта
  dialogs / topics   список диалогов / форум-топиков
  voice              найти и транскрибировать голосовое
  send               отправить/править/удалить сообщение (требует согласования)
  manage             чаты/топики/люди: create|topic|forward|invite|rights|kick|delete
  bot                клик-тест бота (требует согласования при -go)
  approve / pending  согласовать исходящее (-y после согласия) / список запросов
  secret             хранилище ключей: set|ls|ref|rm|check|chat
  update             обновление до свежего релиза (-check — только проверить)

Флаги:
`)
	flag.PrintDefaults()
}

func toInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, n)
		}
	}
	return out
}
