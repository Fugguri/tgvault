package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"
)

type modelDef struct {
	name string
	size string
}

var whisperModels = []modelDef{
	{"tiny", "~75 МБ"},
	{"base", "~142 МБ"},
	{"small", "~466 МБ"},
	{"medium", "~1.5 ГБ"},
	{"large-v3", "~3 ГБ"},
}

// SetupOpts — параметры установки (для неинтерактивного режима).
type SetupOpts struct {
	WhisperBin   string
	Unattended   bool
	Models       []string
	Default      string
	APIID        string
	APIHash      string
	BuildWhisper bool
}

// Setup — установочный мастер: deps, whisper-cli, модели, Telegram-ключи,
// глобальный .env (~/.config/tgvault/.env). При opt.Unattended не спрашивает.
func Setup(ctx context.Context, opt SetupOpts) error {
	cfgDir := configDir()
	dataDir := filepath.Join(userDataDir(), "tgvault")
	modelsDir := filepath.Join(dataDir, "models")
	_ = os.MkdirAll(modelsDir, 0o755)

	fmt.Println("Проверка зависимостей:")
	checkBin("ffmpeg", "нужен для ogg→wav")

	// 1. whisper-cli (авто-резолв: env → ранее собранный → PATH → типовые пути)
	whisperBin := ResolveWhisper(opt.WhisperBin)
	if whisperBin != "" {
		fmt.Printf("  ✓ whisper-cli: %s\n", whisperBin)
	} else {
		fmt.Println("whisper-cli не найден.")
		whisperBin = filepath.Join(dataDir, "whisper", "build", "bin", "whisper-cli")
		build := opt.BuildWhisper
		if !opt.Unattended {
			if err := huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("Собрать whisper.cpp из исходников? (нужны git и cmake)").
					Affirmative("Собрать").Negative("Пропустить").Value(&build),
			)).Run(); err != nil {
				return err
			}
		}
		if build {
			p, err := buildWhisper(ctx, filepath.Join(dataDir, "whisper"))
			if err != nil {
				fmt.Printf("  ⚠ сборка не удалась: %v\n", err)
			} else {
				whisperBin = p
				fmt.Printf("  ✓ собран: %s\n", p)
			}
		}
	}

	// 2. модели
	picked := opt.Models
	if !opt.Unattended {
		var sel []string
		opts := make([]huh.Option[string], 0, len(whisperModels))
		for _, m := range whisperModels {
			opts = append(opts, huh.NewOption(fmt.Sprintf("%-9s %s", m.name, m.size), m.name))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Скачать модели whisper.cpp (x — отметить)").
				Options(opts...).
				Value(&sel),
		)).Run(); err != nil {
			return err
		}
		picked = sel
	}
	fmt.Println("Модели:")
	for _, name := range picked {
		downloadModel(ctx, modelsDir, name)
	}

	// 3. модель по умолчанию
	def := opt.Default
	if def == "" {
		if len(picked) > 0 {
			def = picked[0]
		} else {
			def = "base"
		}
	}
	if have := availableModels(modelsDir); len(have) > 0 && !opt.Unattended {
		defOpts := make([]huh.Option[string], 0, len(have))
		for _, m := range have {
			defOpts = append(defOpts, huh.NewOption(m, m))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Модель по умолчанию").Options(defOpts...).Value(&def),
		)).Run(); err != nil {
			return err
		}
	}
	// гарантируем, что дефолтная модель скачана
	if def != "" && !fileExists(filepath.Join(modelsDir, "ggml-"+def+".bin")) {
		fmt.Printf("Дефолтная модель %q не скачана — качаю.\n", def)
		downloadModel(ctx, modelsDir, def)
	}

	// 4. Telegram-ключи
	cur, _ := godotenv.Read(filepath.Join(cfgDir, ".env"))
	apiID := opt.APIID
	apiHash := opt.APIHash
	if !opt.Unattended {
		if cur != nil {
			if apiID == "" {
				apiID = cur["TG_API_ID"]
			}
			if apiHash == "" {
				apiHash = cur["TG_API_HASH"]
			}
		}
		fmt.Println("Telegram api_id/api_hash — с https://my.telegram.org → API development tools")
		if err := huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("TG api_id").Value(&apiID),
			huh.NewInput().Title("TG api_hash").Value(&apiHash),
		)).Run(); err != nil {
			return err
		}
	}

	// 5. запись глобального .env: значения как есть — бинарь читает их напрямую,
	// а от LLM они прячутся маскировкой в выводе (см. secrets.MaskEnv).
	env := map[string]string{
		"WHISPER_BIN":   whisperBin,
		"WHISPER_MODEL": filepath.Join(modelsDir, "ggml-"+def+".bin"),
		"FFMPEG":        "ffmpeg",
	}
	if apiID != "" {
		env["TG_API_ID"] = apiID
	}
	if apiHash != "" {
		env["TG_API_HASH"] = apiHash
	}
	if err := upsertEnv(filepath.Join(cfgDir, ".env"), env); err != nil {
		return err
	}
	fmt.Printf("✓ глобальный конфиг: %s\n  модель: %s\n  дальше: tgvault login\n", filepath.Join(cfgDir, ".env"), def)
	return nil
}

func buildWhisper(ctx context.Context, dir string) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("нет git")
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		return "", fmt.Errorf("нет cmake")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := run(ctx, "", "git", "clone", "--depth", "1", "https://github.com/ggml-org/whisper.cpp", dir); err != nil {
			return "", err
		}
	}
	if err := run(ctx, dir, "cmake", "-B", "build", "-DCMAKE_BUILD_TYPE=Release", "-DWHISPER_BUILD_TESTS=OFF", "-DWHISPER_BUILD_SERVER=ON"); err != nil {
		return "", err
	}
	if err := run(ctx, dir, "cmake", "--build", "build", "-j"); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "build", "bin", "whisper-cli")
	if !fileExists(bin) {
		return "", fmt.Errorf("whisper-cli не появился после сборки")
	}
	return bin, nil
}

func run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func checkBin(name, why string) {
	if p, err := exec.LookPath(name); err == nil {
		fmt.Printf("  ✓ %s: %s\n", name, p)
	} else {
		fmt.Printf("  ⚠ %s не найден — %s\n", name, why)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ResolveWhisper ищет whisper-cli: явный путь → ранее собранный → PATH → типовые пути.
func ResolveWhisper(explicit string) string {
	if explicit != "" && fileExists(explicit) {
		if abs, err := filepath.Abs(explicit); err == nil {
			return abs
		}
		return explicit
	}
	if p := filepath.Join(userDataDir(), "tgvault", "whisper", "build", "bin", "whisper-cli"); fileExists(p) {
		return p
	}
	for _, n := range []string{"whisper-cli", "whisper-server"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "whisper-cli"),
		"/usr/local/bin/whisper-cli",
		"/usr/bin/whisper-cli",
	} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func availableModels(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "ggml-") && strings.HasSuffix(n, ".bin") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "ggml-"), ".bin"))
		}
	}
	return out
}

func downloadModel(ctx context.Context, modelsDir, name string) {
	dst := filepath.Join(modelsDir, "ggml-"+name+".bin")
	if fileExists(dst) {
		fmt.Printf("  ✓ модель %s уже есть\n", name)
		return
	}
	url := "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-" + name + ".bin"
	fmt.Printf("  ↓ модель %s … ", name)
	if err := download(ctx, url, dst); err != nil {
		fmt.Printf("ошибка: %v\n", err)
		return
	}
	fmt.Println("ок")
}

func download(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dst)
}

func upsertEnv(path string, kv map[string]string) error {
	cur, _ := godotenv.Read(path)
	if cur == nil {
		cur = map[string]string{}
	}
	for k, v := range kv {
		cur[k] = v
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	for k, v := range cur {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func userDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

func configDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "tgvault")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tgvault")
}
