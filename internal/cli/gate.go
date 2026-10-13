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

// Gate — предохранитель для исходящих действий: без согласования не отправляет.
//
//   - dry-run (do=false): регистрирует запрос, печатает превью и id. Реальное
//     действие не выполняется.
//   - do=true: требует согласование ровно этого содержимого (одноразовое).
//     Если его нет — регистрирует запрос и возвращает ошибку с id.
//
// Согласование выдаёт `tgvault approve <id>`: после согласия пользователя в чате
// агент может выполнить его сам (`approve <id> -y`). Если запрос запущен из
// терминала, подтверждение спрашивается интерактивно.
func Gate(do bool, action, summary string, payload []byte) error {
	id := approve.ID(action, payload)
	if do {
		if err := approve.Consume(id); err != nil {
			_ = approve.Request(id, action, summary)
			return fmt.Errorf("отправка не согласована (запрос #%s).\n  покажи это пользователю, получи явное «да» и выполни: tgvault approve %s -y — затем повтори команду", id, id)
		}
		return nil
	}
	if err := approve.Request(id, action, summary); err != nil {
		return err
	}
	fmt.Println(summary)
	fmt.Printf("\nЗапрос на согласование #%s. Согласуй: tgvault approve %s\n", id, id)
	return nil
}

// Approve — подтверждение запроса. Из терминала спрашивает интерактивно; без
// терминала (агент) требует явный -y и только после согласия пользователя.
func Approve(args []string, ttl time.Duration, yesFlag bool) error {
	args, ttl, yes := splitApprove(args, ttl)
	yes = yes || yesFlag
	if len(args) == 0 {
		return fmt.Errorf("approve: укажи id запроса (список — tgvault pending)")
	}
	id := strings.TrimPrefix(strings.TrimSpace(args[0]), "#")
	p, err := approve.Load(id)
	if err != nil {
		return fmt.Errorf("запрос #%s не найден", id)
	}
	fmt.Printf("Запрос #%s (%s):\n\n%s\n\n", p.ID, p.Action, p.Summary)

	if !approveGranted(yes) {
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

// approveGranted решает, подтверждать ли: строгий режим — только TTY-ответ;
// иначе -y/--yes, а при наличии терминала — интерактивный вопрос.
func approveGranted(yes bool) bool {
	if strictApproval() {
		if hasTTY() {
			return confirmTTY("Согласовать отправку? [y/N]: ")
		}
		fmt.Fprintln(os.Stderr, "строгий режим (TGVAULT_APPROVAL=strict): подтвердить может только человек в своём терминале")
		return false
	}
	if hasTTY() {
		return confirmTTY("Согласовать отправку? [y/N]: ")
	}
	if yes {
		return true
	}
	fmt.Fprintln(os.Stderr, "нет терминала: подтверди флагом -y (только с согласия пользователя)")
	return false
}

func strictApproval() bool {
	v := strings.ToLower(os.Getenv("TGVAULT_APPROVAL"))
	return v == "strict" || os.Getenv("TGVAULT_REQUIRE_TTY") != ""
}

// hasTTY сообщает, доступен ли управляющий терминал.
func hasTTY() bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		if t, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err != nil {
			return false
		} else {
			t.Close()
		}
	}
	return true
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

// confirmTTY спрашивает подтверждение из терминала (/dev/tty или stdin).
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
	return false
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes", "д", "да", "1":
		return true
	}
	return false
}

// splitApprove вынимает -ttl/--ttl и -y/--yes из позиционных аргументов
// (flag.Parse останавливается на id).
func splitApprove(args []string, ttl time.Duration) (rest []string, outTTL time.Duration, yes bool) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-y" || a == "--yes":
			yes = true
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
	return out, ttl, yes
}
