package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"0.2.0", "0.1.0", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "dev", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.9.9", "v1.0.0", false},
		{"v0.10.0", "v0.9.0", true},
		{"", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestFetchBuildsAssetURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Fugguri/tgvault/releases/latest" {
			t.Errorf("путь запроса: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"https://example/release"}`))
	}))
	defer srv.Close()

	oldAPI, oldDL := APIBase, DownloadBase
	APIBase, DownloadBase = srv.URL, srv.URL
	t.Cleanup(func() { APIBase, DownloadBase = oldAPI, oldDL })

	rel, err := Fetch(context.Background(), "Fugguri/tgvault", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v9.9.9" {
		t.Fatalf("Tag = %q", rel.Tag)
	}
	want := srv.URL + "/Fugguri/tgvault/releases/download/v9.9.9/tgvault_linux_amd64"
	if rel.URL != want {
		t.Fatalf("URL = %q, want %q", rel.URL, want)
	}
}

func TestInstallReplacesFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("new-binary"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "tgvault")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	rel := &Release{Tag: "v9.9.9", URL: srv.URL + "/asset"}
	if err := Install(context.Background(), rel, exe); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-binary" {
		t.Fatalf("содержимое = %q", got)
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("нет права на исполнение: %o", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("остались временные файлы: %v", entries)
	}
}
