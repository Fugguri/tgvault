// Package netx — общий HTTP-клиент для внешних запросов tgvault.
//
// Зачем: у http.DefaultClient нет таймаутов вообще, поэтому зависшее соединение
// подвешивает команду навсегда (наблюдалось на `tgvault update`: процесс висел
// 4+ минуты). Здесь заданы таймауты на установку соединения, TLS и ожидание
// заголовков ответа.
//
// Общего таймаута на весь запрос сознательно нет: этим же клиентом качаются
// бинарь релиза и модели whisper (сотни мегабайт), и медленная, но живая
// загрузка не должна обрываться. Небольшие запросы ограничиваются по времени
// через MetadataTimeout.
package netx

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

// Client — использовать для всех внешних HTTP-запросов.
var Client = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	},
}

// MetadataTimeout — предел для небольших запросов (GitHub API, SKILL.md).
// Переменная, а не константа: тесты подменяют её, чтобы не ждать по-настоящему.
var MetadataTimeout = 30 * time.Second

// MetadataContext ограничивает по времени запрос, который не должен длиться
// долго. Возвращённый CancelFunc обязан быть вызван.
func MetadataContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, MetadataTimeout)
}

// StallTimeout — сколько ждать данных, прежде чем считать загрузку зависшей.
// Переменная, а не константа: тесты подменяют её.
var StallTimeout = 60 * time.Second

// Stall следит за потоком данных: если байты перестали приходить дольше
// StallTimeout, связанный контекст отменяется — и висящий запрос обрывается.
//
// Это закрывает дыру, которую таймауты транспорта не закрывают: они ловят
// подвисание до прихода заголовков, а вставшая на середине передача (наблюдалось
// на скачивании релизного бинаря: команда висела 4+ минуты) иначе висит вечно.
//
// Контекст обязан быть тем, с которым сделан запрос, — иначе отменять нечего.
// Поэтому порядок такой:
//
//	stall := netx.NewStall(ctx)
//	defer stall.Stop()
//	req, _ := http.NewRequestWithContext(stall.Context(), ...)
//	resp, _ := netx.Client.Do(req)
//	io.Copy(dst, stall.Body(resp.Body))
type Stall struct {
	ctx    context.Context
	cancel context.CancelFunc
	timer  *time.Timer
}

// NewStall готовит сторож для одного запроса.
func NewStall(parent context.Context) *Stall {
	ctx, cancel := context.WithCancel(parent)
	return &Stall{ctx: ctx, cancel: cancel, timer: time.AfterFunc(StallTimeout, cancel)}
}

// Context — контекст для http.NewRequestWithContext.
func (s *Stall) Context() context.Context { return s.ctx }

// Body оборачивает тело ответа: пока байты идут, срок продлевается.
func (s *Stall) Body(r io.Reader) io.Reader { return &stallReader{s: s, r: r} }

// Stop снимает сторож. Вызывать обязательно.
func (s *Stall) Stop() {
	s.timer.Stop()
	s.cancel()
}

type stallReader struct {
	s *Stall
	r io.Reader
}

func (r *stallReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		// Данные идут — продлеваем срок.
		r.s.timer.Reset(StallTimeout)
	}
	return n, err
}
