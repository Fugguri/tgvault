package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// State — состояние импорта записи (watermark).
type State struct {
	LastID    int    `json:"last_id"`
	LastDate  string `json:"last_date,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

const stateName = ".tg-state.json"

// LoadState читает состояние из директории слага.
// Отсутствие файла — это норма (состояния ещё нет), пустое состояние без ошибки.
// А вот битый файл — ошибка: молчаливый нулевой watermark означает либо повторный
// импорт всей истории, либо, что хуже, тихую потерю сообщений — и то и другое
// выглядит для пользователя как «Готово: 0 новых сообщений».
func LoadState(slugDir string) (State, error) {
	var s State
	p := filepath.Join(slugDir, stateName)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, fmt.Errorf("состояние %s: %w", p, err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("состояние %s повреждено (%v) — удали файл, чтобы импортировать запись заново", p, err)
	}
	return s, nil
}

// SaveState пишет состояние в директорию слага.
func SaveState(slugDir string, s State) error {
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(slugDir, stateName), append(b, '\n'), 0o644)
}
