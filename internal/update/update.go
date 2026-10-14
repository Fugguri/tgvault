// Package update проверяет и ставит свежий релиз tgvault с GitHub.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Fugguri/tgvault/internal/netx"
)

// DefaultRepo — владелец/имя репозитория релизов.
const DefaultRepo = "Fugguri/tgvault"

// APIBase и DownloadBase вынесены в переменные, чтобы тесты могли подменить их.
var (
	APIBase      = "https://api.github.com"
	DownloadBase = "https://github.com"
	RawBase      = "https://raw.githubusercontent.com"
)

// Release — найденный релиз и прямая ссылка на нужный ассет.
type Release struct {
	Tag  string
	Page string
	URL  string
}

// Fetch спрашивает последний релиз и строит ссылку на ассет под goos/goarch.
func Fetch(ctx context.Context, repo, goos, goarch string) (*Release, error) {
	ctx, cancel := netx.MetadataContext(ctx)
	defer cancel()
	url := APIBase + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tgvault")
	resp, err := netx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.TagName == "" {
		return nil, fmt.Errorf("в ответе нет tag_name")
	}
	asset := fmt.Sprintf("tgvault_%s_%s", goos, goarch)
	return &Release{
		Tag:  body.TagName,
		Page: body.HTMLURL,
		URL:  fmt.Sprintf("%s/%s/releases/download/%s/%s", DownloadBase, repo, body.TagName, asset),
	}, nil
}

// FetchRaw скачивает файл из репозитория на конкретном теге.
// Тег, а не main: скилл агента должен соответствовать установленному бинарю,
// иначе инструкция описывает не тот CLI, который лежит на диске.
func FetchRaw(ctx context.Context, repo, tag, path string) ([]byte, error) {
	ctx, cancel := netx.MetadataContext(ctx)
	defer cancel()
	url := fmt.Sprintf("%s/%s/%s/%s", RawBase, repo, tag, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tgvault")
	resp, err := netx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: GitHub ответил %s", url, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", url, err)
	}
	return b, nil
}

// Install скачивает ассет и атомарно заменяет исполняемый файл execPath.
// Общего таймаута нет: файл крупный, и медленная живая загрузка не должна
// обрываться. От подвисаний защищают таймауты транспорта плюс сторож потока —
// вставшая на середине передача иначе висит вечно (наблюдалось вживую).
func Install(ctx context.Context, r *Release, execPath string) error {
	stall := netx.NewStall(ctx)
	defer stall.Stop()
	req, err := http.NewRequestWithContext(stall.Context(), http.MethodGet, r.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "tgvault")
	resp, err := netx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("скачивание: %s", resp.Status)
	}
	dir := filepath.Dir(execPath)
	tmp, err := os.CreateTemp(dir, ".tgvault-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, stall.Body(resp.Body)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, execPath); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// Normalize убирает ведущий "v" и пробелы.
func Normalize(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// Newer сообщает, что latest новее current (dev считается старым).
func Newer(latest, current string) bool {
	l := Normalize(latest)
	c := Normalize(current)
	if l == "" {
		return false
	}
	if c == "" || c == "dev" {
		return true
	}
	return compare(l, c) > 0
}

// compare сравнивает две версии по числовым компонентам: 1 > 0, -1 < 0, 0 == 0.
func compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var ai, bi int
		if i < len(as) {
			ai = leadingInt(as[i])
		}
		if i < len(bs) {
			bi = leadingInt(bs[i])
		}
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return 0
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}
