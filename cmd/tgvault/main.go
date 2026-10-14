// Command tgvault — импорт Telegram-чатов в Markdown (Go, спайк-ядро).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/joho/godotenv"

	"github.com/Fugguri/tgvault/internal/cli"
	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/secrets"
	"github.com/Fugguri/tgvault/internal/tgclient"
	"github.com/Fugguri/tgvault/internal/transcribe"
	"github.com/Fugguri/tgvault/internal/update"
)

// version подставляется на релизе через -ldflags "-X main.version=…".
var version = "dev"

func main() {
	// Команду ищем как первый аргумент из известных; остальное — флаги.
	cmd := "login"
	rest := os.Args[1:]
	for i, a := range rest {
		if commands[a] {
			cmd = a
			rest = append(append([]string{}, rest[:i]...), rest[i+1:]...)
			break
		}
	}

	// Ссылки на секреты подставляем до чтения флагов. Для команд, которым ключи
	// не нужны (secret/update), отсутствие секрета не фатально.
	if err := loadEnv(needsKeys(cmd)); err != nil {
		die("%v", err)
	}

	var (
		apiID      = flag.Int("api-id", atoiOr(env("TG_API_ID")), "Telegram api_id")
		apiHash    = flag.String("api-hash", env("TG_API_HASH"), "Telegram api_hash")
		phone      = flag.String("phone", env("TG_PHONE"), "phone (+7...)")
		password   = flag.String("password", env("TG_2FA"), "2FA password (optional)")
		sessionPt  = flag.String("session", env("TG_SESSION", defaultSession()), "session file")
		whisperMdl = flag.String("whisper-model", env("WHISPER_MODEL", "models/ggml-base.bin"), "ggml model path")
		ffmpegPt   = flag.String("ffmpeg", env("FFMPEG", "ffmpeg"), "ffmpeg path")
		lang       = flag.String("lang", env("LANG_ASR", "ru"), "transcription language")
		topicID    = flag.Int("topic", 0, "topic id (voice/send/view)")
		allEntries = flag.Bool("all", false, "import: импортировать все записи (иначе только default)")
		entrySlug  = flag.String("entry", "", "import/view: запись с этим slug")
		fullReb    = flag.Bool("full", false, "import: полный пересбор логов")
		allHistory = flag.Bool("all-history", false, "import: тянуть всю историю (иначе окно по умолчанию 30 дней)")
		fromStr    = flag.String("from", "", "import/view: с даты YYYY-MM-DD")
		toStr      = flag.String("to", "", "import/view: по дату YYYY-MM-DD")
		noSecrets  = flag.Bool("no-secrets", false, "import: не выносить токены из переписки")
		newOut     = flag.String("new-out", "", "migrate: новый корень (default docs/telegram_chats)")

		// setup (неинтерактивный режим)
		setupModels   = flag.String("models", "", "setup: модели через запятую (tiny,base,...)")
		setupDefault  = flag.String("default-model", "", "setup: модель по умолчанию")
		setupUnattend = flag.Bool("unattended", false, "setup: без вопросов")

		// add (неинтерактивное добавление записи)
		addDefault = flag.Bool("default", false, "add: импортировать каждый раз")
		addSlug    = flag.String("slug", "", "add: имя папки")
		addNote    = flag.String("note", "", "add: что за чат")
		addWhen    = flag.String("when", "", "add: когда идти")
		addWriteTo = flag.String("write-to", "", "add: кому писать")
		addOut     = flag.String("out", "", "add: корень раскладки (для нового конфига)")

		// secret / update
		forceFlag   = flag.Bool("force", false, "secret set: перезаписать; update: переустановить")
		updateCheck = flag.Bool("check", false, "update: только проверить, не устанавливать")

		// send
		sendChat  = flag.String("chat", "", "send/view: имя чата")
		sendID    = flag.Int64("id", 0, "send/view: id чата")
		sendAlias = flag.String("alias", "", "send/view: alias/slug из конфига")
		sendSelf  = flag.Bool("self", false, "send/view: Saved Messages (Избранное)")
		sendText  = flag.String("text", "", "send: текст ('-' = stdin)")
		sendTextF = flag.String("text-file", "", "send: текст из файла")
		sendReply = flag.Int("reply-to", 0, "send: msg_id для ответа")
		sendEdit  = flag.Int("edit", 0, "send: msg_id для редактирования")
		sendDo    = flag.Bool("send", false, "send: ВЫПОЛНИТЬ (иначе dry-run)")

		// view — быстрый просмотр сообщений за период без импорта
		viewLast       = flag.Int("last", 0, "view: только последние N сообщений")
		viewMaxLen     = flag.Int("max-len", 1500, "view: обрезка текста сообщения, символов (0 — без обрезки)")
		viewMaxMsgs    = flag.Int("max-msgs", 200, "view: максимум сообщений в выводе (0 — без лимита)")
		viewMedia      = flag.Bool("media", false, "view: скачать медиа в кэш и печатать пути")
		viewTranscribe = flag.Bool("transcribe", false, "view: транскрибировать голосовые (с -media)")

		// approve
		apprTTL = flag.Duration("ttl", 5*time.Minute, "approve: срок действия согласования")
		apprYes = flag.Bool("y", false, "approve: подтвердить без вопроса (только с согласия пользователя)")
	)
	var sendFiles, sendDeletes stringSlice
	flag.Var(&sendFiles, "file", "send: файл/медиа (можно несколько)")
	flag.Var(&sendDeletes, "delete", "send: msg_id для удаления (можно несколько)")

	// bot (клик-тест)
	var (
		botUser     = flag.String("bot", "", "bot: @username или id")
		goRun       = flag.Bool("go", false, "bot: выполнить (иначе план)")
		allowDanger = flag.Bool("allow-danger", false, "bot: разрешить необратимые кнопки")
		pause       = flag.Float64("pause", 4, "bot: пауза после действия, сек")
		botSteps    stringSlice
	)
	flag.Var(&botSteps, "step", "bot: шаг send:/start | click:Текст | expect:Текст | expect-doc | sleep:2")

	// Значения по умолчанию могут быть подставленными секретами — прячем их в usage.
	flag.CommandLine.SetOutput(maskWriter{os.Stderr})
	flag.Usage = usage
	flag.CommandLine.Parse(rest)

	ctx := context.Background()

	// команды без Telegram-сессии
	if cmd == "setup" {
		opt := cli.SetupOpts{
			Unattended: *setupUnattend,
			Default:    *setupDefault,
			APIHash:    *apiHash,
		}
		if *apiID > 0 {
			opt.APIID = strconv.Itoa(*apiID)
		}
		if *setupModels != "" {
			opt.Models = strings.Split(*setupModels, ",")
		}
		if err := cli.Setup(ctx, opt); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "list" {
		wd, _ := os.Getwd()
		if err := cli.List(wd); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "approve" {
		if err := cli.Approve(flag.Args(), *apprTTL, *apprYes); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "pending" {
		if err := cli.PendingList(); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "secret" {
		if err := cli.Secret(flag.Args(), *forceFlag); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "update" {
		if err := cli.Update(ctx, version, *updateCheck, *forceFlag); err != nil {
			die("%v", err)
		}
		return
	}
	if cmd == "import" || cmd == "migrate" {
		wd, _ := os.Getwd()
		cfg, cfgPath, err := config.Load(wd)
		if err != nil {
			die("конфиг не найден: %s вверх по дереву", config.Path)
		}
		// Даты из конфига проверяются здесь же — до входа в Telegram: опечатка
		// должна падать сразу, а не после коннекта и логина.
		if _, err := cli.ParseDate(cfg.From); err != nil {
			die("from в конфиге %s: %v", cfgPath, err)
		}
		if _, err := cli.ParseDate(cfg.To); err != nil {
			die("to в конфиге %s: %v", cfgPath, err)
		}
	}

	// Даты из флагов разбираются до коннекта к Telegram: опечатка должна падать
	// с внятным сообщением, а не молча становиться «граница не задана».
	fromD, errFrom := cli.ParseDate(*fromStr)
	if errFrom != nil {
		die("-from: %v", errFrom)
	}
	toD, errTo := cli.ParseDate(*toStr)
	if errTo != nil {
		die("-to: %v", errTo)
	}

	if *apiID == 0 || *apiHash == "" {
		die("нужны -api-id и -api-hash (или TG_API_ID/TG_API_HASH)")
	}
	if err := os.MkdirAll(filepath.Dir(*sessionPt), 0o700); err != nil {
		die("session dir: %v", err)
	}

	tr, cleanup := buildTranscriber(*whisperMdl, *ffmpegPt, *lang)
	defer cleanup()
	// Модель не нашлась — голосовые будут без расшифровки. Молчаливая деградация
	// выглядит как «транскрибация сломана», поэтому говорим вслух и подсказываем,
	// что делать. Для команд, которые голосовые не трогают, молчим.
	if tr == nil {
		switch cmd {
		case "import", "view", "voice":
			fmt.Fprintf(os.Stderr, "⚠ модель %s не найдена — голосовые не будут распознаны; `tgvault setup` её скачает\n", *whisperMdl)
		}
	}

	cfg := tgclient.Config{
		APIID:    *apiID,
		APIHash:  *apiHash,
		Phone:    *phone,
		Password: *password,
		Session:  *sessionPt,
	}

	err := tgclient.Run(ctx, cfg, func(ctx context.Context, c *telegram.Client) error {
		me, err := c.Self(ctx)
		if err != nil {
			return fmt.Errorf("self: %w", err)
		}
		fmt.Printf("✓ залогинен: id=%d @%s %s\n", me.ID, me.Username, strings.TrimSpace(me.FirstName+" "+me.LastName))

		pos := flag.Args()
		wd, _ := os.Getwd()
		switch cmd {
		case "login":
			return nil
		case "init":
			return cli.Init(ctx, c, wd)
		case "add":
			return cli.Add(ctx, c, wd, cli.AddOpts{
				Chat: *sendChat, ChatID: *sendID, Topic: *topicID, Default: *addDefault,
				Slug: *addSlug, Note: *addNote, When: *addWhen, WriteTo: *addWriteTo, Out: *addOut,
			})
		case "migrate":
			return cli.Migrate(ctx, c, wd, cli.MigrateOpts{NewOut: *newOut})
		case "import":
			return cli.Import(ctx, c, wd, cli.ImportOpts{
				All:       *allEntries,
				Entry:     *entrySlug,
				Full:      *fullReb,
				Unbounded: *allHistory,
				NoRedact:  *noSecrets,
				From:      fromD,
				To:        toD,
				Tr:        tr,
			})
		case "dialogs":
			return cli.Dialogs(ctx, c)
		case "topics":
			if len(pos) == 0 {
				return fmt.Errorf(`укажи имя чата: topics "часть имени"`)
			}
			return cli.Topics(ctx, c, strings.Join(pos, " "))
		case "voice":
			if len(pos) == 0 {
				return fmt.Errorf(`укажи имя чата: voice "часть имени"`)
			}
			return cli.Voice(ctx, c, tr, strings.Join(pos, " "), *topicID)
		case "view":
			return cli.View(ctx, c, wd, cli.ViewOpts{
				Chat: *sendChat, ChatID: *sendID, Alias: *sendAlias, Self: *sendSelf,
				Entry: *entrySlug, Topic: *topicID,
				From: fromD, To: toD,
				Last: *viewLast, MaxLen: *viewMaxLen, MaxMsgs: *viewMaxMsgs,
				Media: *viewMedia, Transcribe: *viewTranscribe, Tr: tr,
			})
		case "send":
			return cli.Send(ctx, c, wd, cli.SendOpts{
				Chat: *sendChat, ChatID: *sendID, Alias: *sendAlias, Self: *sendSelf,
				Text: *sendText, TextFile: *sendTextF, Files: sendFiles,
				Topic: *topicID, ReplyTo: *sendReply, Edit: *sendEdit,
				Delete: toInts(sendDeletes), Do: *sendDo,
			})
		case "bot":
			return cli.Bot(ctx, c, cli.BotOpts{
				Bot: *botUser, Self: *sendSelf, Steps: botSteps, Go: *goRun,
				AllowDanger: *allowDanger, Pause: time.Duration(*pause * float64(time.Second)),
			})
		case "manage":
			return cli.Manage(ctx, c, wd, pos)
		default:
			return fmt.Errorf("неизвестная команда %q", cmd)
		}
	})
	if err != nil {
		die("%v", err)
	}
	updateNotice(ctx, version)
}

var commands = map[string]bool{
	"login": true, "init": true, "import": true, "migrate": true,
	"setup": true, "list": true, "add": true, "send": true, "bot": true, "dialogs": true, "topics": true, "voice": true, "secret": true, "update": true, "manage": true, "approve": true, "pending": true, "view": true,
}

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func toInts(ss []string) []int {
	out := make([]int, 0, len(ss))
	for _, s := range ss {
		if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// buildTranscriber собирает транскрибер: whisper.cpp вшит в бинарь (cgo),
// модель грузится в процесс один раз. Внешний whisper-cli не нужен.
func buildTranscriber(model, ffmpeg, lang string) (cli.Transcriber, func()) {
	model = resolveModel(model)
	if !fileExists(model) {
		return nil, func() {}
	}
	nt := &transcribe.Native{Model: model, FFmpeg: ffmpeg, Lang: lang}
	return nt.Transcribe, func() { _ = nt.Close() }
}

// resolveModel ищет модель: явный путь → глобальный каталог setup (~/.local/share/tgvault/models).
func resolveModel(p string) string {
	if fileExists(p) {
		return p
	}
	base := filepath.Base(p)
	dirs := []string{}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "tgvault", "models"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "tgvault", "models"))
	}
	for _, d := range dirs {
		if cand := filepath.Join(d, base); fileExists(cand) {
			return cand
		}
	}
	return p
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "tgvault")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tgvault")
}

func defaultSession() string {
	return filepath.Join(configDir(), "session.json")
}

func loadEnv(strict bool) error {
	// глобальный .env (ключи, пути к whisper) — приоритетнее локального
	if p := filepath.Join(configDir(), ".env"); fileExists(p) {
		if err := godotenv.Load(p); err != nil {
			// Без этого битый .env выглядел бы как «ключи не заданы» — и уводил
			// бы искать причину не там.
			fmt.Fprintf(os.Stderr, "⚠ %s не прочитан: %v\n", p, err)
		}
	}
	dir, _ := os.Getwd()
	found := false
	for i := 0; i < 6 && dir != "" && dir != "/"; i++ {
		if p := filepath.Join(dir, ".env"); fileExists(p) {
			if err := godotenv.Load(p); err != nil {
				fmt.Fprintf(os.Stderr, "⚠ %s не прочитан: %v\n", p, err)
			}
			found = true
			break
		}
		dir = filepath.Dir(dir)
	}
	if !found && fileExists(".env") {
		if err := godotenv.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ .env не прочитан: %v\n", err)
		}
	}
	if err := secrets.Default().ExpandEnv(); err != nil {
		if strict {
			return fmt.Errorf("%w\n  заведи секрет: tgvault secret set NAME", err)
		}
		// Не падаем, но и не молчим: нераскрытая ссылка {{secret:…}} уходит
		// в вывод и в чат обычным текстом.
		fmt.Fprintf(os.Stderr, "⚠ %v\n", err)
	}
	return nil
}

// needsKeys — командам, которые работают с Telegram, нужны валидные ключи.
func needsKeys(cmd string) bool {
	switch cmd {
	case "secret", "update", "list", "setup", "approve", "pending":
		return false
	}
	return true
}

// updateNotice раз в сутки (и то best-effort) напоминает, что вышел релиз.
// Не тянет обновление само — только ссылку, чтобы не запускать update вслепую.
func updateNotice(ctx context.Context, version string) {
	if os.Getenv("TGVAULT_NO_UPDATE_CHECK") != "" {
		return
	}
	stamp := filepath.Join(configDir(), ".update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	_ = os.MkdirAll(filepath.Dir(stamp), 0o700)
	_ = os.WriteFile(stamp, nil, 0o644)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rel, err := update.Fetch(cctx, update.DefaultRepo, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return
	}
	if update.Newer(rel.Tag, version) {
		fmt.Fprintf(os.Stderr, "• доступна версия %s (у тебя %s): tgvault update\n", rel.Tag, update.Normalize(version))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func env(k string, def ...string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	if len(def) > 0 {
		return def[0]
	}
	return ""
}

func atoiOr(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

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
