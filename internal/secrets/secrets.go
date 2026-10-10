// Package secrets хранит ключи отдельно от кода и подменяет ссылки на них.
//
// Ссылка — {{secret:NAME}} или {{secret:scope/NAME}}. Значение лежит в файле
// <SECRETS_DIR>/<scope>/<NAME> (права 600), поэтому в .env, репозитории и
// контексте агента видно только имя, а не сам ключ. Одно значение может
// повторяться под разными именами — тогда для маскировки берётся одно
// каноническое имя (короткое, затем лексикографически).
package secrets

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultScope — область хранилища, если она не указана в ссылке.
const DefaultScope = "tgvault"

const prefix = "{{secret:"

var (
	refRe  = regexp.MustCompile(`\{\{secret:([A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)?)\}\}`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// Ref — имя секрета: область и собственно имя.
type Ref struct {
	Scope string
	Name  string
}

// String — каноническое представление "scope/NAME".
func (r Ref) String() string { return r.Scope + "/" + r.Name }

// Tag — ссылка, которая ставится на место значения.
func (r Ref) Tag() string { return prefix + r.String() + "}}" }

// Store — каталог секретов.
type Store struct {
	Root  string
	Scope string
}

// Default собирает Store из окружения: SECRETS_DIR (по умолчанию ~/.secrets)
// и SECRETS_SCOPE (по умолчанию tgvault).
func Default() Store {
	root := os.Getenv("SECRETS_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".secrets")
	}
	scope := os.Getenv("SECRETS_SCOPE")
	if scope == "" {
		scope = DefaultScope
	}
	return Store{Root: root, Scope: scope}
}

// Parse разбирает ссылку без области или "scope/NAME" в Ref.
func (s Store) Parse(raw string) (Ref, error) {
	parts := strings.Split(raw, "/")
	switch len(parts) {
	case 1:
		if !validName(parts[0]) {
			return Ref{}, fmt.Errorf("недопустимое имя секрета %q", raw)
		}
		return Ref{Scope: s.Scope, Name: parts[0]}, nil
	case 2:
		if !validName(parts[0]) || !validName(parts[1]) {
			return Ref{}, fmt.Errorf("недопустимая ссылка на секрет %q", raw)
		}
		return Ref{Scope: parts[0], Name: parts[1]}, nil
	default:
		return Ref{}, fmt.Errorf("недопустимая ссылка на секрет %q", raw)
	}
}

func validName(s string) bool {
	return s != "." && s != ".." && nameRe.MatchString(s)
}

// Tag возвращает ссылку {{secret:scope/NAME}} для имени без области.
func (s Store) Tag(raw string) string {
	r, err := s.Parse(raw)
	if err != nil {
		return raw
	}
	return r.Tag()
}

func (s Store) path(r Ref) string { return filepath.Join(s.Root, r.Scope, r.Name) }

// Get возвращает значение секрета.
func (s Store) Get(raw string) (string, error) {
	r, err := s.Parse(raw)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(s.path(r))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("секрет %s не найден", r)
		}
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// Set записывает значение (0600). Без force существующее имя не перезаписывается.
func (s Store) Set(raw, value string, force bool) error {
	r, err := s.Parse(raw)
	if err != nil {
		return err
	}
	p := s.path(r)
	if fileExists(p) && !force {
		return fmt.Errorf("секрет %s уже есть (перезапись: --force)", r)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Remove удаляет секрет.
func (s Store) Remove(raw string) error {
	r, err := s.Parse(raw)
	if err != nil {
		return err
	}
	if err := os.Remove(s.path(r)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("секрет %s не найден", r)
		}
		return err
	}
	return nil
}

// List перечисляет имена секретов, не читая значений.
func (s Store) List() []Ref {
	scopes, _ := os.ReadDir(s.Root)
	var out []Ref
	for _, sc := range scopes {
		if !sc.IsDir() || !nameRe.MatchString(sc.Name()) {
			continue
		}
		names, _ := os.ReadDir(filepath.Join(s.Root, sc.Name()))
		for _, n := range names {
			if n.IsDir() || strings.HasSuffix(n.Name(), ".tmp") || !nameRe.MatchString(n.Name()) {
				continue
			}
			out = append(out, Ref{Scope: sc.Name(), Name: n.Name()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// ExpandValue заменяет все ссылки на значения. Первая ошибка (обычно отсутствие
// секрета) возвращается вместе с частично подставленной строкой.
func (s Store) ExpandValue(v string) (string, error) {
	if !strings.Contains(v, prefix) {
		return v, nil
	}
	var firstErr error
	out := refRe.ReplaceAllStringFunc(v, func(m string) string {
		raw := refRe.FindStringSubmatch(m)[1]
		val, err := s.Get(raw)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return m
		}
		return val
	})
	return out, firstErr
}

// ExpandEnv подставляет значения во все переменные окружения, где есть ссылки.
func (s Store) ExpandEnv() error {
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if !strings.Contains(v, prefix) {
			continue
		}
		nv, err := s.ExpandValue(v)
		if err != nil {
			return err
		}
		if err := os.Setenv(k, nv); err != nil {
			return err
		}
	}
	return nil
}

// Mask заменяет все известные значения на их ссылки — для логов и сообщений.
// Порядок — от длинных значений к коротким, чтобы короткое не ранило длинное.
func (s Store) Mask(text string) string {
	type pair struct{ value, tag string }
	byValue := s.values()
	pairs := make([]pair, 0, len(byValue))
	for val, refs := range byValue {
		pairs = append(pairs, pair{value: val, tag: canonical(refs).Tag()})
	}
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })
	for _, p := range pairs {
		text = strings.ReplaceAll(text, p.value, p.tag)
	}
	return text
}

// secretEnvRe — имена переменных окружения, значения которых прячем от LLM.
var secretEnvRe = regexp.MustCompile(`(?i)(API[_-]?HASH|API[_-]?KEY|API[_-]?ID|SECRET|TOKEN|PASSWORD|PASSWD|2FA|PRIVATE[_-]?KEY)`)

// MaskEnv заменяет значения секретных переменных окружения (по имени) на ссылку
// {{secret:VAR}} — чтобы ключи из .env не засветились LLM в выводе бинаря.
// Сам бинарь при этом читает .env как обычно: меняется только вид текста.
func MaskEnv(text string) string {
	type pair struct{ value, tag string }
	var pairs []pair
	for _, kv := range os.Environ() {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		k, v := kv[:i], kv[i+1:]
		if v == "" || strings.Contains(v, prefix) || !secretEnvRe.MatchString(k) {
			continue
		}
		pairs = append(pairs, pair{value: v, tag: prefix + k + "}}"})
	}
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })
	for _, p := range pairs {
		text = strings.ReplaceAll(text, p.value, p.tag)
	}
	return text
}

// values читает все секреты и группирует ссылки по значению.
func (s Store) values() map[string][]Ref {
	out := map[string][]Ref{}
	for _, r := range s.List() {
		b, err := os.ReadFile(s.path(r))
		if err != nil {
			continue
		}
		val := strings.TrimRight(string(b), "\r\n")
		if val == "" {
			continue
		}
		out[val] = append(out[val], r)
	}
	return out
}

// canonical выбирает одно имя для повторяющегося значения: короткое, затем
// лексикографически — результат стабилен между запусками.
func canonical(refs []Ref) Ref {
	sort.Slice(refs, func(i, j int) bool {
		if len(refs[i].Name) != len(refs[j].Name) {
			return len(refs[i].Name) < len(refs[j].Name)
		}
		return refs[i].String() < refs[j].String()
	})
	return refs[0]
}

// Issue — замечание по хранилищу.
type Issue struct {
	Ref     string
	Message string
}

// Check проверяет права, пустые значения и повтор одного значения под разными
// именами.
func (s Store) Check() []Issue {
	var issues []Issue
	for _, refs := range s.values() {
		if len(refs) > 1 {
			names := make([]string, len(refs))
			for i, r := range refs {
				names[i] = r.String()
			}
			sort.Strings(names)
			issues = append(issues, Issue{
				Ref:     names[0],
				Message: "одно значение под несколькими именами: " + strings.Join(names, ", "),
			})
		}
	}
	for _, r := range s.List() {
		p := s.path(r)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.Mode().Perm()&0o077 != 0 {
			issues = append(issues, Issue{
				Ref:     r.String(),
				Message: fmt.Sprintf("права %04o — читаемо другими (надо 600)", info.Mode().Perm()),
			})
		}
		b, err := os.ReadFile(p)
		if err == nil && strings.TrimRight(string(b), "\r\n") == "" {
			issues = append(issues, Issue{Ref: r.String(), Message: "пустое значение"})
		}
	}
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Ref != issues[j].Ref {
			return issues[i].Ref < issues[j].Ref
		}
		return issues[i].Message < issues[j].Message
	})
	return issues
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
