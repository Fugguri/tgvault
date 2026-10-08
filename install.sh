#!/usr/bin/env bash
# Установка tgvault одной строкой:
#   curl -fsSL https://raw.githubusercontent.com/Fugguri/tgvault/main/install.sh | bash
set -euo pipefail

REPO="${TGIMPORT_REPO:-Fugguri/tgvault}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "неизвестная архитектура: $arch" >&2; exit 1 ;;
esac

asset="tgvault_${os}_${arch}"
url="https://github.com/${REPO}/releases/latest/download/${asset}"

mkdir -p "$BIN_DIR"
echo "Скачиваю ${asset} из ${REPO} …"
if ! curl -fsSL "$url" -o "$BIN_DIR/tgvault"; then
  echo "не удалось скачать. Проверь, что релиз собран: $url" >&2
  exit 1
fi
chmod +x "$BIN_DIR/tgvault"

echo "✓ установлено: $BIN_DIR/tgvault"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "добавь в PATH: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

# скилл для агентов + замена старого Python-скилла tg-import
skill_url="https://raw.githubusercontent.com/${REPO}/main/SKILL.md"

legacy=""
for d in "$HOME/.claude/skills/tg-import" "$HOME/.config/opencode/skills/tg-import"; do
  [ -d "$d" ] && legacy="$legacy $d"
done
if [ -n "$legacy" ]; then
  ans=n
  if [ -r /dev/tty ]; then
    printf "Найден старый скилл tg-import (Python). Заменить на tgvault? [y/N] " >/dev/tty
    read -r ans </dev/tty || ans=n
  fi
  case "$ans" in
    [yY]*)
      mkdir -p "$HOME/.config/tgvault/legacy-skills"
      for d in $legacy; do
        base="$(basename "$(dirname "$d")")-tg-import"
        cp -r "$d" "$HOME/.config/tgvault/legacy-skills/$base" 2>/dev/null || true
        rm -rf "$d"
      done
      echo "✓ старый tg-import убран (бэкап: ~/.config/tgvault/legacy-skills)"
      ;;
    *) echo "• старый tg-import оставлен как есть" ;;
  esac
fi

for d in "$HOME/.claude/skills/tgvault" "$HOME/.config/opencode/skills/tgvault"; do
  if [ -d "$(dirname "$d")" ]; then
    mkdir -p "$d"
    if curl -fsSL "$skill_url" -o "$d/SKILL.md"; then
      echo "✓ скилл: $d/SKILL.md"
    fi
  fi
done
echo
echo "Дальше:"
echo "  1) tgvault setup   # модели, ключи, зависимости"
echo "  2) tgvault login   # вход в Telegram (один раз)"
echo "  3) cd <проект> && tgvault init"
