#!/usr/bin/env bash
# Установка tgvault одной строкой:
#   curl -fsSL https://raw.githubusercontent.com/<REPO>/main/install.sh | bash
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

# скилл для агентов (если есть соответствующие каталоги)
skill_url="https://raw.githubusercontent.com/${REPO}/main/SKILL.md"
for d in "$HOME/.claude/skills/tg-import" "$HOME/.config/opencode/skills/tg-import"; do
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
