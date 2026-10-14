package netx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Загрузка, которая встала на середине, должна обрываться по сторожу: именно на
// этом висел `tgvault update` (4+ минуты) — таймаут на заголовки такой случай
// не ловит, потому что заголовки уже пришли.
//
// Отдельно про конструкцию: отменять надо тот контекст, с которым сделан запрос.
// Первая версия сторожа отменяла свой производный контекст, который транспорт не
// слушал, — и не обрывала ничего. Поэтому здесь проверяется именно связка
// «контекст запроса + обёрнутое тело».
func TestStallAbortsStalledStream(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("начало"))
		w.(http.Flusher).Flush() // заголовки и первые байты ушли — дальше тишина
		<-release
	}))
	defer srv.Close()
	defer close(release) // раньше srv.Close, иначе он будет ждать обработчик

	old := StallTimeout
	StallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { StallTimeout = old })

	stall := NewStall(context.Background())
	defer stall.Stop()
	req, err := http.NewRequestWithContext(stall.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	start := time.Now()
	_, err = io.Copy(io.Discard, stall.Body(resp.Body))
	if err == nil {
		t.Fatal("вставший поток дочитан до конца — сторож не сработал")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("обрыв через %s — сторож не сработал вовремя", d)
	}
}

// Живой поток сторож обрывать не должен, даже если он медленный.
func TestStallKeepsLiveStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte("порция"))
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	}))
	defer srv.Close()

	old := StallTimeout
	StallTimeout = 200 * time.Millisecond
	t.Cleanup(func() { StallTimeout = old })

	stall := NewStall(context.Background())
	defer stall.Stop()
	req, err := http.NewRequestWithContext(stall.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	n, err := io.Copy(io.Discard, stall.Body(resp.Body))
	if err != nil {
		t.Fatalf("живой поток оборван: %v", err)
	}
	if want := int64(len("порция") * 5); n != want {
		t.Fatalf("прочитано %d байт, ждали %d", n, want)
	}
}
