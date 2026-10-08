package importer

import (
	"encoding/json"
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

// LoadState читает состояние из директории слага (нет файла — пустое).
func LoadState(slugDir string) State {
	var s State
	b, err := os.ReadFile(filepath.Join(slugDir, stateName))
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
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
