package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Fugguri/tgvault/internal/secrets"
)

// Secret выполняет подкоманды управления хранилищем секретов. Значения не
// печатаются в stdout — наружу видно только имя.
func Secret(args []string, force bool) error {
	st := secrets.Default()
	if len(args) == 0 {
		return fmt.Errorf("secret: подкоманда set|ls|ref|rm|check (напр. tgvault secret set TG_API_HASH)")
	}
	switch args[0] {
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("secret set NAME (значение — из stdin)")
		}
		value, err := readSecret(os.Stdin)
		if err != nil {
			return err
		}
		value = strings.TrimRight(value, "\r\n")
		if value == "" {
			return fmt.Errorf("пустое значение — не записываю")
		}
		if err := st.Set(args[1], value, force); err != nil {
			return err
		}
		r, _ := st.Parse(args[1])
		fmt.Printf("✓ секрет записан: %s\n", r.String())
		return nil
	case "ls", "list":
		refs := st.List()
		if len(refs) == 0 {
			fmt.Printf("Секретов нет (%s)\n", st.Root)
			return nil
		}
		for _, r := range refs {
			fmt.Println(r.String())
		}
		return nil
	case "ref":
		if len(args) < 2 {
			return fmt.Errorf("secret ref NAME")
		}
		r, err := st.Parse(args[1])
		if err != nil {
			return err
		}
		fmt.Println(r.Tag())
		return nil
	case "rm", "remove":
		if len(args) < 2 {
			return fmt.Errorf("secret rm NAME")
		}
		if err := st.Remove(args[1]); err != nil {
			return err
		}
		r, _ := st.Parse(args[1])
		fmt.Printf("✓ удалён: %s\n", r.String())
		return nil
	case "check":
		issues := st.Check()
		if len(issues) == 0 {
			fmt.Printf("✓ хранилище в порядке (%s)\n", st.Root)
			return nil
		}
		for _, is := range issues {
			fmt.Printf("! %s: %s\n", is.Ref, is.Message)
		}
		return nil
	default:
		return fmt.Errorf("secret: неизвестная подкоманда %q", args[0])
	}
}

// readSecret читает значение: скрыто с терминала, как есть — из пайпа.
func readSecret(f *os.File) (string, error) {
	if term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(os.Stderr, "Значение (не отображается): ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
