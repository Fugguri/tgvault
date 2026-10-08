# tgvault

Импорт переписки Telegram в Markdown: читает чаты и форум-топики, скачивает
медиа, транскрибирует голосовые локально (whisper.cpp) и раскладывает по дням.
Плюс отправка/правка/удаление сообщений и клик-тест ботов.

Один бинарь, без Python. MTProto через [gotd/td](https://github.com/gotd/td).

## Установка

```bash
curl -fsSL https://raw.githubusercontent.com/Fugguri/tgvault/main/install.sh | bash
```

Ставит `tgvault` в `~/.local/bin` и `SKILL.md` в каталоги агентов. Дальше:

```bash
tgvault setup      # whisper-cli, модели, Telegram api_id/api_hash -> ~/.config/tgvault/.env
tgvault login      # вход в Telegram (один раз, сессия глобальная)
```

## Возможности

- **Импорт** чатов, групп, каналов и конкретных форум-топиков в
  `docs/telegram_chats/<chat>_<topic>/upd_<день>/_log.md`.
- **Инкрементально** — тянет только новые сообщения (watermark), правки/удаления — через `-full`.
- **Медиа** — фото, документы, голосовые скачиваются в `<slug>/files/`, привязаны к сообщению.
- **Транскрибация** голосовых локально (whisper.cpp, `whisper-server`), без сети.
- **Справочник чатов** в конфиге проекта: что за чат, когда идти, кому писать.
- **Отправка** `send`/`edit`/`delete` (dry-run по умолчанию).
- **Клик-тест ботов** `bot` (шаги send/click/expect, защита от необратимых кнопок).

## Требования

- `ffmpeg` (ogg → wav).
- `whisper.cpp` — бинарь `whisper-cli`/`whisper-server`. `tgvault setup` умеет
  собрать его из исходников автоматически (нужны `git` и `cmake`), либо укажите
  готовый путь через `WHISPER_BIN`.

## Сборка

```bash
make build          # -> ./tgvault
make cross          # релизы под linux/macOS/windows (amd64/arm64)
```

## Быстрый старт

```bash
./tgvault setup                 # мастер: whisper-cli, модели, TG api_id/api_hash -> глобальный .env
./tgvault login                 # вход в Telegram (один раз, сессия глобальная)
cd ~/projects/my-project
../../tgvault init              # мастер: чаты, топики, справочник -> .tg-import.json
../../tgvault import            # импорт дефолтных записей
```

`api_id`/`api_hash` берутся на https://my.telegram.org → API development tools.
Всё, что спрашивает `setup`, пишется в `~/.config/tgvault/.env`.

Неинтерактивная установка (для скриптов):

```bash
./tgvault -unattended -models base,tiny -default-model base \
    -api-id <ID> -api-hash <HASH> -build-whisper setup
```

## Команды

| Команда | Что делает |
|---|---|
| `setup` | мастер установки: модели (выбор), проверка deps, глобальный `.env` |
| `login` | вход в Telegram, сессия в `~/.config/tgvault/session.json` |
| `init` | создать/пересоздать конфиг проекта (поиск чатов/топиков) |
| `import` | импорт по конфигу (`-all`, `-entry`, `-from/-to`, `-full`) |
| `migrate` | перевод старого конфига v1 → v2 |
| `dialogs` | список диалогов |
| `topics "<чат>"` | форум-топики чата |
| `send` | отправить/править/удалить (`-text`, `-file`, `-reply-to`, `-topic`, `-edit`, `-delete`, `-send`) |
| `bot` | клик-тест бота (`-bot`, `-step send:/start`, `-step click:…`, `-go`) |

## Конфиг проекта `.tg-import.json`

```json
{
  "version": 2,
  "project": "my-project",
  "out": "docs/telegram_chats",
  "entries": [
    {
      "chat": "Mind", "chat_id": 3759202592, "access_hash": 0, "kind": "channel",
      "topic": "Сториз", "topic_id": 112, "slug": "Mind_Сториз",
      "default": true,
      "note": "внутренний чат проекта", "when": "обсуждение фич", "write_to": "заказчику"
    }
  ]
}
```

## Модели транскрибации

Локально, whisper.cpp: `tiny` / `base` (дефолт) / `small` / `medium` / `large-v3`.
Выбор — в `setup`. Голосовые короткие, качество `base` достаточно для LLM.

## Транскрибация: очередь

Текст пишется сразу, а голосовые ставятся во **внутреннюю накапливающуюся очередь**:
вместо транскрипта сначала идёт плейсхолдер `🎤 (транскрибируется…)`, воркер
распознаёт и **обновляет секцию на месте** (атомарно, под мьютексом на файл).
Так импорт не блокируется на распознавании, а файл постепенно «оживает».

## Планы (будущее)

- Отложенная/фоновая транскрибация: detached-воркер + state + команда `status`,
  уведомления (desktop/Telegram), опциональный пуш агенту через плагин.
  Ядро остаётся агент-независимым; пуш — необязательный адаптер.
- **Тёплая модель (холодный старт):** демон держит маленькую модель (напр. `base`)
  в памяти; большая модель включается опцией в конфиге. Так нет загрузки модели
  на каждый запуск.

## Сессия и конкурентность

Сессия общая на машину. Доступ сериализован файловым локом: параллельные запуски
ждут друг друга, а не портят сессию.

## Лицензия

MIT — см. [LICENSE](LICENSE).
