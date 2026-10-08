// Command tgvault — импорт Telegram-чатов в Markdown (Go, спайк-ядро).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/joho/godotenv"

	"github.com/Fugguri/tgvault/internal/cli"
	"github.com/Fugguri/tgvault/internal/config"
	"github.com/Fugguri/tgvault/internal/tgclient"
	"github.com/Fugguri/tgvault/internal/transcribe"
)

func main() {
	loadEnv()

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

	var (
		apiID      = flag.Int("api-id", atoiOr(env("TG_API_ID")), "Telegram api_id")
		apiHash    = flag.String("api-hash", env("TG_API_HASH"), "Telegram api_hash")
		phone      = flag.String("phone", env("TG_PHONE"), "phone (+7...)")
		password   = flag.String("password", env("TG_2FA"), "2FA password (optional)")
		sessionPt  = flag.String("session", env("TG_SESSION", defaultSession()), "session file")
		whisperBin = flag.String("whisper-bin", env("WHISPER_BIN", "third_party/whisper.cpp/build/bin/whisper-cli"), "whisper-cli path")
		whisperMdl = flag.String("whisper-model", env("WHISPER_MODEL", "models/ggml-base.bin"), "ggml model path")
		ffmpegPt   = flag.String("ffmpeg", env("FFMPEG", "ffmpeg"), "ffmpeg path")
		lang       = flag.String("lang", env("LANG_ASR", "ru"), "transcription language")
		topicID    = flag.Int("topic", 0, "topic id (voice/send)")
		allEntries = flag.Bool("all", false, "import: импортировать все записи (иначе только default)")
		entrySlug  = flag.String("entry", "", "import: только запись с этим slug")
		fullReb    = flag.Bool("full", false, "import: полный пересбор логов")
		fromStr    = flag.String("from", "", "import: с даты YYYY-MM-DD")
		toStr      = flag.String("to", "", "import: по дату YYYY-MM-DD")
		newOut     = flag.String("new-out", "", "migrate: новый корень (default docs/telegram_chats)")

		// setup (неинтерактивный режим)
		setupModels     = flag.String("models", "", "setup: модели через запятую (tiny,base,...)")
		setupDefault    = flag.String("default-model", "", "setup: модель по умолчанию")
		setupUnattend   = flag.Bool("unattended", false, "setup: без вопросов")
		setupBuildWhisp = flag.Bool("build-whisper", false, "setup: собрать whisper.cpp, если нет")

		// add (неинтерактивное добавление записи)
		addDefault = flag.Bool("default", false, "add: импортировать каждый раз")
		addSlug    = flag.String("slug", "", "add: имя папки")
		addNote    = flag.String("note", "", "add: что за чат")
		addWhen    = flag.String("when", "", "add: когда идти")
		addWriteTo = flag.String("write-to", "", "add: кому писать")
		addOut     = flag.String("out", "", "add: корень раскладки (для нового конфига)")

		// send
		sendChat   = flag.String("chat", "", "send: имя чата")
		sendID     = flag.Int64("id", 0, "send: id чата")
		sendAlias  = flag.String("alias", "", "send: alias/slug из конфига")
		sendSelf   = flag.Bool("self", false, "send: Saved Messages (Избранное)")
		sendText   = flag.String("text", "", "send: текст ('-' = stdin)")
		sendTextF  = flag.String("text-file", "", "send: текст из файла")
		sendReply  = flag.Int("reply-to", 0, "send: msg_id для ответа")
		sendEdit   = flag.Int("edit", 0, "send: msg_id для редактирования")
		sendDo     = flag.Bool("send", false, "send: ВЫПОЛНИТЬ (иначе dry-run)")
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

	flag.CommandLine.Parse(rest)

	ctx := context.Background()

	// команды без Telegram-сессии
	if cmd == "setup" {
		opt := cli.SetupOpts{
			WhisperBin:   *whisperBin,
			Unattended:   *setupUnattend,
			Default:      *setupDefault,
			APIHash:      *apiHash,
			BuildWhisper: *setupBuildWhisp,
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
	if cmd == "import" || cmd == "migrate" {
		wd, _ := os.Getwd()
		if _, _, err := config.Load(wd); err != nil {
			die("конфиг не найден: %s вверх по дереву", config.Path)
		}
	}

	if *apiID == 0 || *apiHash == "" {
		die("нужны -api-id и -api-hash (или TG_API_ID/TG_API_HASH)")
	}
	if err := os.MkdirAll(filepath.Dir(*sessionPt), 0o700); err != nil {
		die("session dir: %v", err)
	}

	tr, cleanup := buildTranscriber(*whisperBin, *whisperMdl, *ffmpegPt, *lang)
	defer cleanup()

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
				All:   *allEntries,
				Entry: *entrySlug,
				Full:  *fullReb,
				From:  cli.ParseDate(*fromStr),
				To:    cli.ParseDate(*toStr),
				Tr:    tr,
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
		default:
			return fmt.Errorf("неизвестная команда %q", cmd)
		}
	})
	if err != nil {
		die("%v", err)
	}
}

var commands = map[string]bool{
	"login": true, "init": true, "import": true, "migrate": true,
	"setup": true, "list": true, "add": true, "send": true, "bot": true, "dialogs": true, "topics": true, "voice": true,
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

func buildTranscriber(bin, model, ffmpeg, lang string) (cli.Transcriber, func()) {
	bin = cli.ResolveWhisper(bin)
	if bin == "" || !fileExists(model) {
		return nil, func() {}
	}
	isServer := filepath.Base(bin) == "whisper-server"
	serverBin := bin
	if !isServer {
		serverBin = filepath.Join(filepath.Dir(bin), "whisper-server")
	}
	// предпочитаем whisper-server: модель грузится один раз
	if isServer || fileExists(serverBin) {
		ws := &transcribe.WhisperServer{Bin: serverBin, Model: model, FFmpeg: ffmpeg, Lang: lang}
		return ws.Transcribe, func() { _ = ws.Close() }
	}
	w := transcribe.Whisper{Bin: bin, Model: model, FFmpeg: ffmpeg, Lang: lang}
	return w.Transcribe, func() {}
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

func loadEnv() {
	// глобальный .env (ключи, пути к whisper) — приоритетнее локального
	_ = godotenv.Load(filepath.Join(configDir(), ".env"))
	dir, _ := os.Getwd()
	for i := 0; i < 6 && dir != "" && dir != "/"; i++ {
		if p := filepath.Join(dir, ".env"); fileExists(p) {
			_ = godotenv.Load(p)
			return
		}
		dir = filepath.Dir(dir)
	}
	_ = godotenv.Load()
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
	fmt.Fprintf(os.Stderr, "ошибка: "+format+"\n", a...)
	os.Exit(1)
}
