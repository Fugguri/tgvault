package tgclient

import (
	"context"
	"errors"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"
)

// authenticate проверяет сессию и при необходимости проводит вход.
// В отличие от auth.IfNecessary, умеет перезапросить код (`auth.resendCode`),
// когда Telegram отправил его в приложение, а доступа к нему нет.
func authenticate(ctx context.Context, client *telegram.Client, a interactiveAuth, useQR bool, loggedIn qrlogin.LoggedIn) error {
	status, err := client.Auth().Status(ctx)
	if err != nil {
		return fmt.Errorf("get auth status: %w", err)
	}
	if status.Authorized {
		return nil
	}
	if useQR {
		return loginQR(ctx, client, a, loggedIn)
	}

	phone, err := a.Phone(ctx)
	if err != nil {
		return fmt.Errorf("get phone: %w", err)
	}

	sent, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return fmt.Errorf("send code: %w", err)
	}
	sc, ok := sent.(*tg.AuthSentCode)
	if !ok {
		return nil
	}
	hash := sc.PhoneCodeHash
	reportCodeTarget(sc)

	for attempt := 0; attempt < 5; attempt++ {
		code, err := a.Code(ctx, sc)
		if err != nil {
			return fmt.Errorf("get code: %w", err)
		}
		if code == "" {
			next, err := client.Auth().ResendCode(ctx, phone, hash)
			if err != nil {
				return fmt.Errorf("resend code: %w", err)
			}
			nsc, ok := next.(*tg.AuthSentCode)
			if !ok {
				return nil
			}
			hash = nsc.PhoneCodeHash
			sc = nsc
			reportCodeTarget(nsc)
			continue
		}

		_, signErr := client.Auth().SignIn(ctx, phone, code, hash)
		if errors.Is(signErr, auth.ErrPasswordAuthNeeded) {
			return loginPassword(ctx, client, a)
		}
		if signErr == nil {
			return nil
		}
		if tgerr.Is(signErr, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY", "PHONE_CODE_EXPIRED") {
			fmt.Printf("⚠ код не подошёл: %v\n", signErr)
			if tgerr.Is(signErr, "PHONE_CODE_EXPIRED") {
				next, err := client.Auth().ResendCode(ctx, phone, hash)
				if err != nil {
					return fmt.Errorf("resend code: %w", err)
				}
				if nsc, ok := next.(*tg.AuthSentCode); ok {
					hash = nsc.PhoneCodeHash
					reportCodeTarget(nsc)
				}
			}
			continue
		}
		return fmt.Errorf("sign in: %w", signErr)
	}
	return fmt.Errorf("код не принят после нескольких попыток")
}

// reportCodeTarget говорит, куда Telegram отправил код. Без этого вход молча
// висит на «Код из Telegram:», хотя код ушёл в приложение или на почту.
func reportCodeTarget(sc *tg.AuthSentCode) {
	switch t := sc.Type.(type) {
	case *tg.AuthSentCodeTypeApp:
		fmt.Printf("Код отправлен в приложение Telegram (%d цифр).\n", t.Length)
	case *tg.AuthSentCodeTypeSMS:
		fmt.Printf("Код отправлен по SMS (%d цифр).\n", t.Length)
	case *tg.AuthSentCodeTypeCall:
		fmt.Printf("Код придёт звонком (%d цифр).\n", t.Length)
	case *tg.AuthSentCodeTypeFlashCall:
		fmt.Printf("Код придёт flash-звонком (шаблон %s).\n", t.Pattern)
	case *tg.AuthSentCodeTypeMissedCall:
		fmt.Printf("Код придёт пропущенным звонком (префикс %s).\n", t.Prefix)
	case *tg.AuthSentCodeTypeEmailCode:
		fmt.Printf("Код отправлен на почту %s.\n", t.EmailPattern)
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		fmt.Println("Telegram просит сначала привязать почту — код уйдёт туда.")
	case *tg.AuthSentCodeTypeFirebaseSMS:
		fmt.Printf("Код отправлен через Firebase/SMS (%d цифр).\n", t.Length)
	case *tg.AuthSentCodeTypeSMSWord:
		fmt.Printf("Код отправлен по SMS (слово на %q).\n", t.Beginning)
	case *tg.AuthSentCodeTypeSMSPhrase:
		fmt.Printf("Код отправлен по SMS (фраза на %q).\n", t.Beginning)
	case *tg.AuthSentCodeTypeFragmentSMS:
		fmt.Printf("Бесплатного SMS нет: код нужно купить на %s\n", t.URL)
	default:
		fmt.Printf("Код отправлен (%T).\n", t)
	}
	fmt.Println("Код не пришёл? Нажми Enter — запросим SMS/звонок.")
}

// loginQR входит по QR-коду: Telegram с января 2026 не доставляет код в
// сторонние клиенты (auth.sentCodeTypeApp, но сообщение не приходит), а QR
// подтверждается уже залогиненным устройством и кода не требует.
func loginQR(ctx context.Context, client *telegram.Client, a interactiveAuth, loggedIn qrlogin.LoggedIn) error {
	fmt.Println("Вход по QR: Telegram → Настройки → Устройства → Подключить устройство.")
	fmt.Println("Отсканируй код телефоном (он обновляется сам). Ctrl+C — отмена.")
	_, err := client.QR().Auth(ctx, loggedIn, func(_ context.Context, token qrlogin.Token) error {
		if err := renderQR(token.URL()); err != nil {
			return err
		}
		fmt.Println(token.URL())
		return nil
	})
	if err == nil {
		return nil
	}
	// При включённой 2FA подтверждение по QR не завершает вход: сервер просит
	// облачный пароль (SESSION_PASSWORD_NEEDED).
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return loginPassword(ctx, client, a)
	}
	return fmt.Errorf("qr login: %w", err)
}

// loginPassword вводит облачный пароль 2FA после QR/кода.
func loginPassword(ctx context.Context, client *telegram.Client, a interactiveAuth) error {
	for attempt := 0; attempt < 3; attempt++ {
		password, err := a.Password(ctx)
		if err != nil {
			return fmt.Errorf("get password: %w", err)
		}
		if _, err := client.Auth().Password(ctx, password); err != nil {
			if tgerr.Is(err, "PASSWORD_HASH_INVALID") {
				fmt.Printf("⚠ пароль не подошёл: %v\n", err)
				continue
			}
			return fmt.Errorf("sign in with password: %w", err)
		}
		return nil
	}
	return fmt.Errorf("пароль 2FA не принят после нескольких попыток")
}

// renderQR печатает QR в терминал половинными блоками: символ = 1 модуль в
// ширину и 2 в высоту, поэтому код остаётся квадратным и влезает в консоль.
func renderQR(text string) error {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return fmt.Errorf("encode qr: %w", err)
	}
	const border = 2
	black := func(x, y int) bool {
		x -= border
		y -= border
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false
		}
		return code.Black(x, y)
	}
	total := code.Size + border*2
	for y := 0; y < total; y += 2 {
		for x := 0; x < total; x++ {
			switch top, bottom := black(x, y), black(x, y+1); {
			case top && bottom:
				fmt.Print("█")
			case top:
				fmt.Print("▀")
			case bottom:
				fmt.Print("▄")
			default:
				fmt.Print(" ")
			}
		}
		fmt.Println()
	}
	return nil
}
