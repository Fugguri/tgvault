// Package redact находит в тексте сообщений ключи и токены и заменяет их на
// ссылку {{secret:NAME}}. Значения уходят в .secrets.json рядом с логом чата,
// так что в Markdown (и в контексте агента) остаётся только имя.
package redact

import (
	"regexp"
	"sort"
	"strings"
)

type rule struct {
	kind  string
	re    *regexp.Regexp
	group int // захваченная группа со значением (1, если есть скобки)
}

// rules — консервативный набор: сначала точные форматы, затем key=value.
var rules = []rule{
	{"privatekey", regexp.MustCompile(`(?s)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----)`), 1},
	{"tg", regexp.MustCompile(`\b(\d{6,12}:[A-Za-z0-9_-]{30,})\b`), 1},
	{"openai", regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{20,})\b`), 1},
	{"github", regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{36,})\b`), 1},
	{"google", regexp.MustCompile(`\b(AIza[0-9A-Za-z_\-]{35})\b`), 1},
	{"aws", regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`), 1},
	{"slack", regexp.MustCompile(`\b(xox[baprs]-[A-Za-z0-9-]{10,})\b`), 1},
	{"jwt", regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b`), 1},
	{"apikey", regexp.MustCompile(`(?i)\b(?:api[_-]?key|apikey|secret[_-]?key|secret|access[_-]?token|auth[_-]?token|client[_-]?secret|password|passwd|pwd)\b\s*[:=]\s*["']?([A-Za-z0-9_\-.]{12,})["']?`), 1},
}

type match struct {
	start, end  int
	kind, value string
}

// Scan возвращает непересекающиеся совпадения, от длинных к коротким.
func Scan(text string) []match {
	var all []match
	for _, r := range rules {
		for _, idx := range r.re.FindAllStringSubmatchIndex(text, -1) {
			g := r.group * 2
			if g+1 >= len(idx) || idx[g] < 0 {
				continue
			}
			all = append(all, match{idx[g], idx[g+1], r.kind, text[idx[g]:idx[g+1]]})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end-all[i].start > all[j].end-all[j].start
	})
	var out []match
	last := -1
	for _, m := range all {
		if m.start < last {
			continue
		}
		out = append(out, m)
		last = m.end
	}
	return out
}

// Redact заменяет найденные секреты на {{secret:NAME}}, где имя даёт put.
// Возвращает текст и число замен.
func Redact(text string, put func(kind, value string) string) (string, int) {
	ms := Scan(text)
	if len(ms) == 0 {
		return text, 0
	}
	var b strings.Builder
	prev := 0
	for _, m := range ms {
		b.WriteString(text[prev:m.start])
		b.WriteString("{{secret:" + put(m.kind, m.value) + "}}")
		prev = m.end
	}
	b.WriteString(text[prev:])
	return b.String(), len(ms)
}
