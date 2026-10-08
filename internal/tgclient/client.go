package tgclient

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"golang.org/x/term"
)

// Config — параметры подключения к Telegram (личная сессия пользователя).
type Config struct {
	APIID    int
	APIHash  string
	Phone    string
	Password string // 2FA, может быть пустым — тогда спросим интерактивно
	Session  string // путь к файлу сессии
}

// Run поднимает клиент, при необходимости логинит и отдаёт управление в fn.
// Доступ к сессии сериализован блокирующим файловым локом: параллельные
// процессы ждут друг друга, а не портят сессию и не душат рейт-лимит.
func Run(ctx context.Context, c Config, fn func(context.Context, *telegram.Client) error) error {
	unlock, err := lockSession(c.Session)
	if err != nil {
		return err
	}
	defer unlock()

	client := telegram.NewClient(c.APIID, c.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: c.Session},
	})
	return client.Run(ctx, func(ctx context.Context) error {
		flow := auth.NewFlow(
			interactiveAuth{phone: c.Phone, password: c.Password},
			auth.SendCodeOptions{},
		)
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return fmt.Errorf("авторизация: %w", err)
		}
		return fn(ctx, client)
	})
}

// lockSession берёт эксклюзивный лок на <session>.lock. Если занято —
// ждёт (блокирующий flock), пока другой процесс не закончит.
func lockSession(sessionPath string) (func(), error) {
	lockPath := sessionPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == syscall.EWOULDBLOCK {
		fmt.Fprintln(os.Stderr, "… Telegram-сессия занята другим процессом, ждём завершения…")
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("lock: %w", err)
		}
	} else if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// interactiveAuth спрашивает телефон/код/пароль 2FA в терминале.
type interactiveAuth struct {
	phone, password string
}

func (a interactiveAuth) Phone(context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	fmt.Print("Телефон (+7...): ")
	var p string
	fmt.Scanln(&p)
	return strings.TrimSpace(p), nil
}

func (a interactiveAuth) Password(context.Context) (string, error) {
	if a.password != "" {
		return a.password, nil
	}
	fmt.Print("Пароль 2FA: ")
	b, err := promptSecret()
	return strings.TrimSpace(b), err
}

func (a interactiveAuth) Code(context.Context, *tg.AuthSentCode) (string, error) {
	fmt.Print("Код из Telegram: ")
	b, err := promptSecret()
	return strings.TrimSpace(b), err
}

func (a interactiveAuth) AcceptTermsOfService(context.Context, tg.HelpTermsOfService) error {
	return nil
}

func (a interactiveAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("регистрация нового аккаунта не поддерживается")
}

func promptSecret() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		return string(b), err
	}
	var s string
	_, err := fmt.Scanln(&s)
	return s, err
}
