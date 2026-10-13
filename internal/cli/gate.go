package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/Fugguri/tgvault/internal/approve"
)

// Gate — общий предохранитель для исходящих действий.
//
//   - dry-run (do=false): регистрирует запрос, печатает превью и подсказку, как
//     согласовать. Реальное действие не выполняется.
//   - do=true: требует одноразовое согласование ровно этого содержимого.
//     Если его нет — регистрирует запрос и возвращает ошибку с id, чтобы
//     пользователь мог подтвердить.
func Gate(do bool, action, summary string, payload []byte) error {
	id := approve.ID(action, payload)
	if do {
		if err := approve.Consume(id); err != nil {
			_ = approve.Request(id, action, summary)
			return fmt.Errorf("отправка не согласована (запрос #%s).\n  покажи это пользователю и попроси подтвердить: tgvault approve %s — затем повтори команду", id, id)
		}
		return nil
	}
	if err := approve.Request(id, action, summary); err != nil {
		return err
	}
	fmt.Println(summary)
	fmt.Printf("\nЗапрос на согласование #%s. Пользователь должен выполнить:\n  tgvault approve %s\n", id, id)
	return nil
}

// Approve — интерактивное подтверждение запроса (только из терминала).
func Approve(args []string, ttl time.Duration) error {
	args, ttl = splitTTL(args, ttl)
	if len(args) == 0 {
		return fmt.Errorf("approve: укажи id запроса (список — tgvault pending)")
	}
	id := strings.TrimPrefix(strings.TrimSpace(args[0]), "#")
	p, err := approve.Load(id)
	if err != nil {
		return fmt.Errorf("запрос #%s не найден", id)
	}
	fmt.Printf("Запрос #%s (%s):\n\n%s\n\n", p.ID, p.Action, p.Summary)

	if !confirmTTY("Согласовать отправку? [y/N]: ") {
		return fmt.Errorf("согласование отменено")
	}
	if err := approve.Grant(id, ttl); err != nil {
		return err
	}
	d := ttl
	if d <= 0 {
		d = approve.DefaultTTL
	}
	fmt.Printf("✓ согласовано #%s на %s (одноразово)\n", id, d)
	return nil
}

// PendingList печатает очередь запросов на согласование.
func PendingList() error {
	approve.Clean(24 * time.Hour)
	list, err := approve.List()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("Запросов на согласование нет")
		return nil
	}
	for _, p := range list {
		status := "ожидает подтверждения"
		if p.Approved() {
			status = fmt.Sprintf("✓ согласовано до %s", p.Expiry.Format("15:04:05"))
		} else if !p.Expiry.IsZero() {
			status = "истекло"
		}
		first := firstLine(p.Summary)
		fmt.Printf("#%s  [%s]  %s — %s\n", p.ID, p.Action, status, first)
	}
	return nil
}

// firstLine берёт первую содержательную строку summary (без разделителей).
func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.Trim(t, "=-") == "" {
			continue
		}
		return t
	}
	return ""
}

// confirmTTY спрашивает подтверждение строго из терминала (/dev/tty). Без
// интерактивного терминала (например, из агента) вернёт false.
func confirmTTY(prompt string) bool {
	if t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer t.Close()
		fmt.Fprint(t, prompt)
		line, _ := bufio.NewReader(t).ReadString('\n')
		return isYes(line)
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		return isYes(line)
	}
	fmt.Fprintln(os.Stderr, "нет интерактивного терминала — подтвердить может только человек в своём терминале")
	return false
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "д", "да", "1":
		return true
	}
	return false
}

// splitTTL вынимает -ttl/--ttl из позиционных аргументов (flag.Parse останавливается на id).
func splitTTL(args []string, ttl time.Duration) ([]string, time.Duration) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-ttl" || a == "--ttl":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					ttl = d
					i++
				}
			}
		case strings.HasPrefix(a, "-ttl="), strings.HasPrefix(a, "--ttl="):
			if _, v, ok := strings.Cut(a, "="); ok {
				if d, err := time.ParseDuration(v); err == nil {
					ttl = d
				}
			}
		default:
			out = append(out, a)
		}
	}
	return out, ttl
}
